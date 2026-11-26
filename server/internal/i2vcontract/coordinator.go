package i2vcontract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

const (
	// CapabilityProductI2V is the canonical platform-billing capability identifier
	// for product-image-to-video holds.
	CapabilityProductI2V = "product_image.i2v_showcase"

	// EnvelopeSchemaVersion identifies the downstream handoff contract version.
	EnvelopeSchemaVersion = "product-image-i2v-envelope/v1"
)

var (
	ErrInvalidScope         = errors.New("i2vcontract: scope requires app_id=product-image, tenant_id, principal_id, project_id, request_key, and idempotency_key")
	ErrCrossTenantForbidden = errors.New("i2vcontract: cross-tenant or cross-principal task access is forbidden")
	ErrTaskNotFound         = errors.New("i2vcontract: i2v task not found")
	ErrIdempotencyConflict  = errors.New("i2vcontract: idempotency_key already bound to a different task fingerprint")
	ErrURLRenewerRequired   = errors.New("i2vcontract: URL renewer is required before dispatching queued night/idle task")
	ErrBillingRequired      = errors.New("i2vcontract: fidelitygate billing recovery state machine is required for paid tasks")
	ErrTaskNotQueued        = errors.New("i2vcontract: task is not waiting in night/idle queue")
	ErrStageRetryNotAllowed = errors.New("i2vcontract: requested stage cannot be retried")
	ErrTaskOutcomeUnknown   = errors.New("i2vcontract: task outcome is unknown; refresh/reconcile instead of regenerating")
	ErrInvalidFailurePolicy = errors.New("i2vcontract: explicit gate failure billing policy is required")
)

// TaskScope uniquely scopes an I2V task to platform AppID, TenantID, PrincipalID,
// ProjectID, RequestKey, and IdempotencyKey.
type TaskScope struct {
	AppID          string `json:"app_id"`
	TenantID       string `json:"tenant_id"`
	PrincipalID    string `json:"principal_id"`
	ProjectID      string `json:"project_id"`
	RequestKey     string `json:"request_key"`
	IdempotencyKey string `json:"idempotency_key"`
	TraceID        string `json:"trace_id,omitempty"`
}

// Validate checks required tenant/principal/app bindings.
func (s TaskScope) Validate() error {
	if strings.TrimSpace(s.AppID) != fidelitygate.AppID ||
		strings.TrimSpace(s.TenantID) == "" ||
		strings.TrimSpace(s.PrincipalID) == "" ||
		strings.TrimSpace(s.ProjectID) == "" ||
		strings.TrimSpace(s.RequestKey) == "" ||
		strings.TrimSpace(s.IdempotencyKey) == "" {
		return ErrInvalidScope
	}
	return nil
}

func (s TaskScope) billingScope() fidelitygate.BillingScope {
	return fidelitygate.BillingScope{
		AppID:       strings.TrimSpace(s.AppID),
		PrincipalID: strings.TrimSpace(s.PrincipalID),
		RequestKey:  strings.TrimSpace(s.RequestKey),
	}
}

// AccessScope identifies a caller reading, refreshing, dispatching, or retrying a task.
type AccessScope struct {
	TenantID    string
	PrincipalID string
	ProjectID   string
}

// ProviderSubmitRequest is sent to a qualified real I2V adapter only after all
// pre-flight, authorization, renewal, and billing hold checks pass.
type ProviderSubmitRequest struct {
	TaskID          string
	TraceID         string
	IdempotencyKey  string
	TaskFingerprint string
	ModelID         string
	ProductRef      FixedProductRef
	SignedImageURL  string
	Prompt          string
	CameraMove      string
	DurationSeconds int
	AspectRatio     string
	Preference      ExecutionPreference
}

// ProviderSubmitResponse reports the provider job & generated artifact details.
type ProviderSubmitResponse struct {
	ProviderJobID   string
	OutputAssetID   string
	OutputVersionID string
	RawVideoURL     string
	ProviderCharged bool
	ActualCostFen   int64
}

// ProviderPollResponse reports read-only reconciliation status for an UNKNOWN task.
type ProviderPollResponse struct {
	Status          QueueState
	OutputAssetID   string
	OutputVersionID string
	RawVideoURL     string
	ProviderCharged bool
	ActualCostFen   int64
}

// ProviderEnhanceRequest runs post-generation video enhancement without regenerating.
type ProviderEnhanceRequest struct {
	TaskID          string
	ProviderJobID   string
	InputVideoURL   string
	OutputAssetID   string
	OutputVersionID string
}

// ProviderArtifactResponse captures an enhanced artifact URL and version.
type ProviderArtifactResponse struct {
	EnhancedVideoURL string
	OutputAssetID    string
	OutputVersionID  string
}

// ProviderDownloadRequest downloads & inspects a completed video for fidelity checks.
type ProviderDownloadRequest struct {
	TaskID          string
	VideoURL        string
	OutputAssetID   string
	OutputVersionID string
	ProductRef      FixedProductRef
}

// VideoAdapter is the real image-conditioned video adapter seam.
// A nil VideoAdapter represents the honest no-supplier state (fail-closed, 0 calls).
type VideoAdapter interface {
	Qualification() ModelQualification
	SubmitI2V(ctx context.Context, req ProviderSubmitRequest) (ProviderSubmitResponse, error)
	PollI2V(ctx context.Context, providerJobID string) (ProviderPollResponse, error)
	EnhanceVideo(ctx context.Context, req ProviderEnhanceRequest) (ProviderArtifactResponse, error)
	DownloadAndInspect(ctx context.Context, req ProviderDownloadRequest) (VideoFidelityMetrics, error)
}

// URLRenewer renews expiring signed product-image URLs before night/idle dispatch.
type URLRenewer interface {
	RenewSignedURL(ctx context.Context, ref FixedProductRef) (RenewedAssetURL, error)
}

// AuthorizationCheck is the server-verified authorization re-check result before dispatch.
type AuthorizationCheck struct {
	Authorized bool
	Reason     string
	Ref        FixedProductRef
}

// AuthorizationVerifier re-verifies product authorization right before night/idle execution.
type AuthorizationVerifier interface {
	VerifyAuthorization(ctx context.Context, scope TaskScope, ref FixedProductRef) (AuthorizationCheck, error)
}

// SubmitRequest is the full HUI-1702 product I2V task request.
type SubmitRequest struct {
	Scope           TaskScope              `json:"scope"`
	ProductRef      FixedProductRef        `json:"product_ref"`
	SignedImageURL  string                 `json:"-"` // internal signed URL, never re-exposed
	Prompt          string                 `json:"prompt"`
	CameraMove      string                 `json:"camera_move"`
	DurationSeconds int                    `json:"duration_seconds"`
	AspectRatio     string                 `json:"aspect_ratio"`
	EnhanceEnabled  bool                   `json:"enhance_enabled"`
	ModelOverride   *ModelQualification    `json:"model_override,omitempty"`
	Policy          ExecutionPolicyRequest `json:"policy"`
	Cost            CostAttribution        `json:"cost"`
}

// TaskSnapshot is the user-safe, immutable-copy view of one I2V task.
// It contains zero pool/AutoDL/runtime token fields.
type TaskSnapshot struct {
	TaskID                string                 `json:"task_id"`
	TraceID               string                 `json:"trace_id"`
	TenantID              string                 `json:"tenant_id"`
	ProjectID             string                 `json:"project_id"`
	IdempotencyKey        string                 `json:"idempotency_key"`
	TaskFingerprint       string                 `json:"task_fingerprint"`
	Idempotent            bool                   `json:"idempotent"`
	BasicImagePathBlocked bool                   `json:"basic_image_path_blocked"`
	Qualification         QualificationDecision  `json:"qualification"`
	FixedProductRef       FixedProductRef        `json:"fixed_product_ref"`
	Queue                 QueueProjection        `json:"queue"`
	Billing               BillingReceiptRef      `json:"billing"`
	Candidate             *CandidateReturnRecord `json:"candidate,omitempty"`
	ProviderGenerateCalls int                    `json:"provider_generate_calls"`
	EnhanceCalls          int                    `json:"enhance_calls"`
	DownloadCalls         int                    `json:"download_calls"`
	URLRenewalCount       int                    `json:"url_renewal_count"`
	AuthReverifyCount     int                    `json:"auth_reverify_count"`
	FailedStage           TaskStage              `json:"failed_stage,omitempty"`
	BlockCode             string                 `json:"block_code,omitempty"`
	LastError             string                 `json:"last_error,omitempty"`
}

func (s TaskSnapshot) clone() TaskSnapshot {
	cp := s
	cp.FixedProductRef = s.FixedProductRef.CanonicalNormalized()
	if s.Qualification.MissingSeams != nil {
		cp.Qualification.MissingSeams = append([]string(nil), s.Qualification.MissingSeams...)
	}
	if s.Qualification.QualityLimitations != nil {
		cp.Qualification.QualityLimitations = append([]string(nil), s.Qualification.QualityLimitations...)
	}
	if s.Qualification.ViewAssessment.Limitations != nil {
		cp.Qualification.ViewAssessment.Limitations = append([]string(nil), s.Qualification.ViewAssessment.Limitations...)
	}
	if s.Queue.NextWindowAt != nil {
		t := *s.Queue.NextWindowAt
		cp.Queue.NextWindowAt = &t
	}
	if s.Queue.ETASeconds != nil {
		v := *s.Queue.ETASeconds
		cp.Queue.ETASeconds = &v
	}
	if s.Candidate != nil {
		c := *s.Candidate
		if c.QualityLimitations != nil {
			c.QualityLimitations = append([]string(nil), c.QualityLimitations...)
		}
		if c.Fidelity.FailureReasons != nil {
			c.Fidelity.FailureReasons = append([]string(nil), c.Fidelity.FailureReasons...)
		}
		if c.Fidelity.QualityLimitations != nil {
			c.Fidelity.QualityLimitations = append([]string(nil), c.Fidelity.QualityLimitations...)
		}
		cp.Candidate = &c
	}
	return cp
}

// TaskFingerprint computes a deterministic fingerprint binding tenant, principal,
// project, immutable product identity, video parameters, and execution preference.
func TaskFingerprint(req SubmitRequest) string {
	ref := req.ProductRef.CanonicalNormalized()
	parts := []string{
		strings.TrimSpace(req.Scope.AppID),
		strings.TrimSpace(req.Scope.TenantID),
		strings.TrimSpace(req.Scope.PrincipalID),
		strings.TrimSpace(req.Scope.ProjectID),
		ref.AssetID,
		ref.VersionID,
		ref.SHA256,
		ref.SKURef,
		ref.SubjectLock.MaskRef,
		strings.TrimSpace(req.CameraMove),
		strconv.Itoa(req.DurationSeconds),
		strings.TrimSpace(req.AspectRatio),
		strings.TrimSpace(req.Prompt),
		strconv.FormatBool(req.EnhanceEnabled),
		string(req.Policy.SelectedPreference),
		string(req.Cost.PayerType),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

type idempotencyKey struct {
	tenantID    string
	principalID string
	key         string
}

type fingerprintKey struct {
	tenantID    string
	principalID string
	fingerprint string
}

type projectCounterKey struct {
	tenantID  string
	projectID string
}

type taskEntry struct {
	mu                 sync.Mutex
	fingerprint        string
	scope              TaskScope
	req                SubmitRequest
	snapshot           TaskSnapshot
	providerJobID      string
	generatedAssetID   string
	generatedVersionID string
	generatedVideoURL  string
	enhancedVideoURL   string
	generateSucceeded  bool
	enhanceSucceeded   bool
	providerCharged    bool
	actualCostFen      int64
	holdActive         bool
	billingSettled     bool
	executed           bool
}

// CoordinatorConfig wires optional dependencies for Coordinator.
// Nil VideoAdapter fails closed honestly with zero provider calls.
type CoordinatorConfig struct {
	Adapter       VideoAdapter
	Billing       *fidelitygate.BillingRecoveryStateMachine
	Renewer       URLRenewer
	AuthVerifier  AuthorizationVerifier
	Thresholds    VideoFidelityThresholds
	FailurePolicy fidelitygate.GateFailureBillingPolicy
	Now           func() time.Time
}

// Coordinator manages the HUI-1702 I2V qualification, night/idle queue & renewal,
// single-flight idempotency, stage-scoped retries, and merchant-candidate return.
type Coordinator struct {
	adapter       VideoAdapter
	billing       *fidelitygate.BillingRecoveryStateMachine
	renewer       URLRenewer
	authVerifier  AuthorizationVerifier
	thresholds    VideoFidelityThresholds
	failurePolicy fidelitygate.GateFailureBillingPolicy
	now           func() time.Time

	mu            sync.Mutex // protects maps & project counters ONLY; never lock entry.mu while holding mu
	nextSeq       int64
	byID          map[string]*taskEntry
	byIdempotency map[idempotencyKey]*taskEntry
	byFingerprint map[fingerprintKey]*taskEntry
	counters      map[projectCounterKey]*BatchStageCounters
}

// NewCoordinator constructs a thread-safe HUI-1702 Coordinator.
func NewCoordinator(cfg CoordinatorConfig) (*Coordinator, error) {
	thr := cfg.Thresholds
	if strings.TrimSpace(thr.EvaluatorVersion) == "" {
		thr = DefaultVideoFidelityThresholds()
	}
	policy := cfg.FailurePolicy
	if policy == "" {
		policy = fidelitygate.GateFailurePolicyRefundPlatformAbsorbed
	}
	if policy != fidelitygate.GateFailurePolicySettleProviderSpend &&
		policy != fidelitygate.GateFailurePolicyRefundPlatformAbsorbed {
		return nil, ErrInvalidFailurePolicy
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	return &Coordinator{
		adapter:       cfg.Adapter,
		billing:       cfg.Billing,
		renewer:       cfg.Renewer,
		authVerifier:  cfg.AuthVerifier,
		thresholds:    thr,
		failurePolicy: policy,
		now:           nowFn,
		byID:          make(map[string]*taskEntry),
		byIdempotency: make(map[idempotencyKey]*taskEntry),
		byFingerprint: make(map[fingerprintKey]*taskEntry),
		counters:      make(map[projectCounterKey]*BatchStageCounters),
	}, nil
}

// RecordImageStage records an image generate or enhance stage event in the project's
// shared BatchStageCounters so batches report image vs video and generate vs enhance
// counts cleanly without conflating modalities.
func (c *Coordinator) RecordImageStage(tenantID, projectID string, stage TaskStage, succeeded bool, retried bool) {
	c.updateCounter(tenantID, projectID, func(cnt *BatchStageCounters) {
		var target *StageCounter
		switch stage {
		case StageGenerate:
			target = &cnt.ImageGenerate
		case StageEnhance:
			target = &cnt.ImageEnhance
		default:
			return
		}
		target.Total++
		if succeeded {
			target.Succeeded++
		} else {
			target.Failed++
		}
		if retried {
			target.Retried++
		}
	})
}

// StageCounters returns a copy of the per-stage counters for a tenant+project.
func (c *Coordinator) StageCounters(tenantID, projectID string) BatchStageCounters {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := projectCounterKey{
		tenantID:  strings.TrimSpace(tenantID),
		projectID: strings.TrimSpace(projectID),
	}
	if cnt, ok := c.counters[key]; ok {
		return *cnt
	}
	return BatchStageCounters{}
}

// Submit validates real I2V qualification, immutable product reference, execution
// preference & cost attribution, deduplicates concurrent/repeated confirmations by
// (tenant, principal, idempotency_key/fingerprint), and either enqueues for night/idle
// window or executes immediately.
func (c *Coordinator) Submit(ctx context.Context, req SubmitRequest) (TaskSnapshot, error) {
	if err := req.Scope.Validate(); err != nil {
		return TaskSnapshot{BasicImagePathBlocked: false}, err
	}
	req.ProductRef = req.ProductRef.CanonicalNormalized()
	fp := TaskFingerprint(req)

	// 1. Check single-flight idempotency BEFORE creating any new task or holding funds.
	entry, isExisting, err := c.lookupOrReserve(req, fp)
	if err != nil {
		return TaskSnapshot{
			TenantID:              strings.TrimSpace(req.Scope.TenantID),
			ProjectID:             strings.TrimSpace(req.Scope.ProjectID),
			IdempotencyKey:        strings.TrimSpace(req.Scope.IdempotencyKey),
			TaskFingerprint:       fp,
			BasicImagePathBlocked: false,
		}, err
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if isExisting && entry.executed {
		snap := entry.snapshot.clone()
		snap.Idempotent = true
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, nil
	}

	now := c.now()
	traceID := strings.TrimSpace(req.Scope.TraceID)
	if traceID == "" {
		traceID = "trace_" + entry.snapshot.TaskID
	}
	entry.snapshot.TraceID = traceID
	entry.snapshot.BasicImagePathBlocked = false
	entry.snapshot.FixedProductRef = req.ProductRef
	entry.snapshot.Queue.ExecutionPreference = req.Policy.SelectedPreference
	entry.snapshot.Queue.PayerType = req.Cost.PayerType
	entry.snapshot.Queue.NextWindowAt = cloneTimePtr(req.Policy.NextWindowAt)
	entry.snapshot.Queue.ETASeconds = cloneInt64Ptr(req.Policy.ETASeconds)
	entry.snapshot.Billing = BillingReceiptRef{
		PayerType:       req.Cost.PayerType,
		QuoteID:         strings.TrimSpace(req.Cost.QuoteID),
		PricingVersion:  strings.TrimSpace(req.Cost.PricingVersion),
		BillingState:    string(fidelitygate.BillingInit),
		FreeQuotaGrant:  strings.TrimSpace(req.Cost.FreeQuotaGrantID),
		AutoChargedPaid: false,
	}

	// 2. Validate immutable fixed product reference & active authorization.
	if err := req.ProductRef.Validate(req.Scope.TenantID, req.Scope.ProjectID, now); err != nil {
		code := BlockMissingImageCondition
		if errors.Is(err, ErrAuthorizationRevoked) {
			code = BlockAuthorizationRevoked
		}
		return c.markBlockedLocked(entry, code, err)
	}

	// 3. Determine model qualification & enforce strict real-I2V / anti-T2V / anti-360-claim gate.
	modelQual := c.resolveModelQualification(req)
	qualDecision, qualErr := EvaluateModelQualification(modelQual, req.DurationSeconds, req.AspectRatio, req.ProductRef.SubjectLock.View)
	entry.snapshot.Qualification = qualDecision
	if qualErr != nil {
		return c.markBlockedLocked(entry, qualDecision.BlockCode, qualErr)
	}

	// 4. Enforce execution preference, no arbitrary priority, no free->internal R&D card,
	// no free-expired auto-charge, no backend cost wallet misuse.
	policyReq := req.Policy
	if policyReq.TargetPoolClass == "" {
		policyReq.TargetPoolClass = modelQual.TargetPoolClass
	}
	if blockCode, pErr := ValidateExecutionAndBillingPolicy(policyReq, req.Cost, now); pErr != nil {
		return c.markBlockedLocked(entry, blockCode, pErr)
	}

	// 5. Require real registered adapter instance before any execution or queue acceptance.
	if c.adapter == nil {
		entry.snapshot.Qualification = DefaultNoAdapterDecision()
		return c.markBlockedLocked(entry, BlockNoRealI2VAdapter, ErrNoRealI2VAdapter)
	}

	// 6. Paid flows MUST use fidelitygate.BillingRecoveryStateMachine (zero second ledger).
	if req.Cost.PayerType != PayerFreeGrant && c.billing == nil {
		return c.markBlockedLocked(entry, BlockInvalidExecutionPreference, ErrBillingRequired)
	}

	// 7. If deferred for night or idle execution window, persist immutable product ref + policy
	// and wait without dropping references or charging prematurely.
	if (req.Policy.SelectedPreference == PrefNight || req.Policy.SelectedPreference == PrefIdle) && req.Policy.DeferUntilWindow {
		entry.snapshot.Queue.QueueState = QueueStateWaitingWindow
		entry.snapshot.Queue.ActiveStage = StageGenerate
		if strings.TrimSpace(req.Policy.WaitReason) != "" {
			entry.snapshot.Queue.WaitReason = strings.TrimSpace(req.Policy.WaitReason)
		} else if req.Policy.SelectedPreference == PrefNight {
			entry.snapshot.Queue.WaitReason = "waiting_night_window"
		} else {
			entry.snapshot.Queue.WaitReason = "waiting_idle_slot"
		}
		entry.executed = true
		c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
			cnt.VideoGenerate.Total++
			cnt.VideoGenerate.Pending++
		})
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, nil
	}

	// 8. Immediate execution path.
	signedURL := strings.TrimSpace(req.SignedImageURL)
	if signedURL == "" || isPlaceholderURL(signedURL) {
		return c.markBlockedLocked(entry, BlockSignedURLExpired, ErrSignedURLInvalidOrExpired)
	}

	entry.executed = true
	c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
		cnt.VideoGenerate.Total++
		cnt.VideoGenerate.Running++
	})
	return c.executePipelineLocked(ctx, entry, signedURL, true)
}

// DispatchQueued executes a task previously queued in a night/idle window.
// Before invoking any provider or billing hold, it MUST:
//  1. Re-check free-tier expiration & pool policy at dispatch time (never auto-charge).
//  2. Re-verify product authorization (block immediately if revoked/expired).
//  3. Renew the signed URL and assert immutable FixedProductRef equality (cannot drop or swap product).
func (c *Coordinator) DispatchQueued(ctx context.Context, access AccessScope, taskID string) (TaskSnapshot, error) {
	entry, err := c.lookupTaskWithAccess(access, taskID)
	if err != nil {
		return TaskSnapshot{BasicImagePathBlocked: false}, err
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.snapshot.Queue.QueueState != QueueStateWaitingWindow {
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		if entry.snapshot.Queue.QueueState == QueueStateCompleted || entry.snapshot.Queue.QueueState == QueueStateRunning {
			snap.Idempotent = true
			return snap, nil
		}
		return snap, ErrTaskNotQueued
	}

	now := c.now()
	entry.snapshot.Queue.QueueState = QueueStateDispatching
	entry.snapshot.Queue.WaitReason = "verifying_auth_and_renewing_url"

	// 1. Re-validate execution & cost policy at dispatch time (catches expired free quota after wait).
	policyReq := entry.req.Policy
	if policyReq.TargetPoolClass == "" && c.adapter != nil {
		policyReq.TargetPoolClass = c.adapter.Qualification().TargetPoolClass
	}
	if blockCode, pErr := ValidateExecutionAndBillingPolicy(policyReq, entry.req.Cost, now); pErr != nil {
		c.decrementPendingVideoGenerate(entry.scope.TenantID, entry.scope.ProjectID)
		return c.markBlockedLocked(entry, blockCode, pErr)
	}

	// 2. Re-verify product authorization before touching provider/billing.
	entry.snapshot.AuthReverifyCount++
	if c.authVerifier != nil {
		authRes, authErr := c.authVerifier.VerifyAuthorization(ctx, entry.scope, entry.req.ProductRef)
		if authErr != nil || !authRes.Authorized {
			c.decrementPendingVideoGenerate(entry.scope.TenantID, entry.scope.ProjectID)
			cause := ErrAuthorizationRevoked
			if authErr != nil {
				cause = fmt.Errorf("%w: %v", ErrAuthorizationRevoked, authErr)
			} else if strings.TrimSpace(authRes.Reason) != "" {
				cause = fmt.Errorf("%w: %s", ErrAuthorizationRevoked, strings.TrimSpace(authRes.Reason))
			}
			return c.markBlockedLocked(entry, BlockAuthorizationRevoked, cause)
		}
		if err := entry.req.ProductRef.MatchesImmutable(authRes.Ref); err != nil {
			c.decrementPendingVideoGenerate(entry.scope.TenantID, entry.scope.ProjectID)
			return c.markBlockedLocked(entry, BlockProductRefTampered, err)
		}
	} else {
		if err := entry.req.ProductRef.Validate(entry.scope.TenantID, entry.scope.ProjectID, now); err != nil {
			c.decrementPendingVideoGenerate(entry.scope.TenantID, entry.scope.ProjectID)
			return c.markBlockedLocked(entry, BlockAuthorizationRevoked, err)
		}
	}

	// 3. Renew signed URL and verify immutable FixedProductRef binding.
	if c.renewer == nil {
		c.decrementPendingVideoGenerate(entry.scope.TenantID, entry.scope.ProjectID)
		return c.markBlockedLocked(entry, BlockSignedURLExpired, ErrURLRenewerRequired)
	}
	renewed, rErr := c.renewer.RenewSignedURL(ctx, entry.req.ProductRef)
	if rErr != nil {
		c.decrementPendingVideoGenerate(entry.scope.TenantID, entry.scope.ProjectID)
		return c.markBlockedLocked(entry, BlockSignedURLExpired, fmt.Errorf("%w: %v", ErrSignedURLInvalidOrExpired, rErr))
	}
	if err := renewed.Validate(entry.req.ProductRef, now); err != nil {
		c.decrementPendingVideoGenerate(entry.scope.TenantID, entry.scope.ProjectID)
		code := BlockSignedURLExpired
		if errors.Is(err, ErrProductReferenceTampered) {
			code = BlockProductRefTampered
		}
		return c.markBlockedLocked(entry, code, err)
	}
	entry.snapshot.URLRenewalCount++

	c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
		if cnt.VideoGenerate.Pending > 0 {
			cnt.VideoGenerate.Pending--
		}
		cnt.VideoGenerate.Running++
	})
	return c.executePipelineLocked(ctx, entry, renewed.SignedURL, true)
}

// Refresh recovers an existing task after page reload, navigation away, or UNKNOWN state.
// It NEVER re-invokes SubmitI2V or double-charges; UNKNOWN tasks reconcile read-only via PollI2V.
func (c *Coordinator) Refresh(ctx context.Context, access AccessScope, taskID string) (TaskSnapshot, error) {
	entry, err := c.lookupTaskWithAccess(access, taskID)
	if err != nil {
		return TaskSnapshot{BasicImagePathBlocked: false}, err
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.snapshot.Queue.QueueState != QueueStateUnknown {
		snap := entry.snapshot.clone()
		snap.Idempotent = true
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, nil
	}

	// Reconcile billing first if paid billing is in UNKNOWN state.
	if entry.req.Cost.PayerType != PayerFreeGrant && c.billing != nil {
		if rec, ok := c.billing.Get(entry.scope.PrincipalID, entry.scope.RequestKey); ok && rec.State == fidelitygate.BillingUnknown {
			reconciled, recErr := c.billing.Reconcile(ctx, entry.scope.billingScope())
			entry.snapshot.Billing = billingReceiptFromRecovery(entry.req.Cost, reconciled)
			if recErr != nil {
				entry.snapshot.LastError = recErr.Error()
				snap := entry.snapshot.clone()
				snap.Idempotent = true
				snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
				return snap, recErr
			}
		}
	}

	if c.adapter == nil || strings.TrimSpace(entry.providerJobID) == "" {
		snap := entry.snapshot.clone()
		snap.Idempotent = true
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, ErrTaskOutcomeUnknown
	}

	pollResp, pollErr := c.adapter.PollI2V(ctx, entry.providerJobID)
	if pollErr != nil {
		entry.snapshot.LastError = pollErr.Error()
		snap := entry.snapshot.clone()
		snap.Idempotent = true
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, ErrTaskOutcomeUnknown
	}

	switch pollResp.Status {
	case QueueStateCompleted:
		entry.generatedAssetID = strings.TrimSpace(pollResp.OutputAssetID)
		entry.generatedVersionID = strings.TrimSpace(pollResp.OutputVersionID)
		entry.generatedVideoURL = strings.TrimSpace(pollResp.RawVideoURL)
		entry.generateSucceeded = true
		entry.providerCharged = pollResp.ProviderCharged
		entry.actualCostFen = pollResp.ActualCostFen
		c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
			if cnt.VideoGenerate.Running > 0 {
				cnt.VideoGenerate.Running--
			}
			cnt.VideoGenerate.Succeeded++
		})
		return c.finishPostGenerateLocked(ctx, entry, true, false)
	case QueueStateFailed, QueueStateCancelled:
		entry.snapshot.Queue.QueueState = pollResp.Status
		entry.snapshot.FailedStage = StageGenerate
		c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
			if cnt.VideoGenerate.Running > 0 {
				cnt.VideoGenerate.Running--
			}
			cnt.VideoGenerate.Failed++
		})
		if entry.holdActive && !entry.billingSettled && entry.req.Cost.PayerType != PayerFreeGrant && c.billing != nil {
			rec, _ := c.billing.HandleProviderFailureBeforeDelivery(ctx, entry.scope.billingScope(), "provider_i2v_failed")
			entry.snapshot.Billing = billingReceiptFromRecovery(entry.req.Cost, rec)
			entry.billingSettled = true
		}
		snap := entry.snapshot.clone()
		snap.Idempotent = true
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, nil
	default:
		snap := entry.snapshot.clone()
		snap.Idempotent = true
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, ErrTaskOutcomeUnknown
	}
}

// RetryStage retries ONLY a failed enhance or download stage without ever
// re-invoking SubmitI2V or double-charging generation.
func (c *Coordinator) RetryStage(ctx context.Context, access AccessScope, taskID string, stage TaskStage) (TaskSnapshot, error) {
	entry, err := c.lookupTaskWithAccess(access, taskID)
	if err != nil {
		return TaskSnapshot{BasicImagePathBlocked: false}, err
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if !entry.generateSucceeded {
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, fmt.Errorf("%w: generation has not succeeded", ErrStageRetryNotAllowed)
	}
	if stage != StageEnhance && stage != StageDownload {
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, fmt.Errorf("%w: only failed enhance or download stage may be retried without regenerating", ErrStageRetryNotAllowed)
	}
	if entry.snapshot.FailedStage != stage {
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, fmt.Errorf("%w: failed stage is %q, not %q", ErrStageRetryNotAllowed, entry.snapshot.FailedStage, stage)
	}

	if stage == StageEnhance {
		return c.finishPostGenerateLocked(ctx, entry, true, true)
	}
	return c.finishPostGenerateLocked(ctx, entry, false, true)
}

// GetTask returns a user-safe snapshot enforcing strict tenant/principal/project isolation.
func (c *Coordinator) GetTask(access AccessScope, taskID string) (TaskSnapshot, error) {
	entry, err := c.lookupTaskWithAccess(access, taskID)
	if err != nil {
		return TaskSnapshot{BasicImagePathBlocked: false}, err
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	snap := entry.snapshot.clone()
	snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
	return snap, nil
}

// GetDownstreamEnvelope constructs the traceable handoff envelope for HUI-5196 / HUI-1745 / HUI-2232.
func (c *Coordinator) GetDownstreamEnvelope(access AccessScope, taskID string) (DownstreamHandoffEnvelope, error) {
	snap, err := c.GetTask(access, taskID)
	if err != nil {
		return DownstreamHandoffEnvelope{}, err
	}
	return DownstreamHandoffEnvelope{
		SchemaVersion:         EnvelopeSchemaVersion,
		TraceID:               snap.TraceID,
		TaskID:                snap.TaskID,
		TaskFingerprint:       snap.TaskFingerprint,
		FixedProductRef:       snap.FixedProductRef,
		ExecutionPreference:   snap.Queue.ExecutionPreference,
		ModelID:               snap.Qualification.ModelID,
		ProviderID:            snap.Qualification.ProviderID,
		BasicImagePathBlocked: false,
		BackViewUnproven:      snap.Qualification.ViewAssessment.BackViewUnproven,
		CannotClaimTrue360:    snap.Qualification.ViewAssessment.CannotClaimTrue360,
		QualityLimitations:    append([]string(nil), snap.Qualification.QualityLimitations...),
		Billing:               snap.Billing,
		Candidate:             snap.Candidate,
	}, nil
}

func (c *Coordinator) executePipelineLocked(ctx context.Context, entry *taskEntry, signedURL string, runEnhance bool) (TaskSnapshot, error) {
	// 1. Place platform-billing hold for paid tasks (free_grant never places a hold).
	if entry.req.Cost.PayerType != PayerFreeGrant {
		rec, hErr := c.billing.BeginHold(
			ctx,
			entry.scope.billingScope(),
			CapabilityProductI2V,
			entry.req.Cost.PricingVersion,
			entry.req.Cost.EstimatedCostFen,
		)
		entry.snapshot.Billing = billingReceiptFromRecovery(entry.req.Cost, rec)
		if hErr != nil {
			entry.snapshot.LastError = hErr.Error()
			if errors.Is(hErr, fidelitygate.ErrBillingUnknown) || errors.Is(hErr, fidelitygate.ErrReconcileRequired) {
				entry.snapshot.Queue.QueueState = QueueStateUnknown
				entry.snapshot.Queue.WaitReason = "billing_hold_unknown_reconcile_required"
				snap := entry.snapshot.clone()
				snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
				return snap, hErr
			}
			c.finishFailedGenerateCounter(entry.scope.TenantID, entry.scope.ProjectID)
			entry.snapshot.Queue.QueueState = QueueStateFailed
			entry.snapshot.FailedStage = StageGenerate
			snap := entry.snapshot.clone()
			snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
			return snap, hErr
		}
		entry.holdActive = true
	}

	// 2. Invoke qualified real I2V adapter ONCE.
	entry.snapshot.Queue.QueueState = QueueStateRunning
	entry.snapshot.Queue.ActiveStage = StageGenerate
	entry.snapshot.Queue.WaitReason = ""
	entry.snapshot.ProviderGenerateCalls++

	subResp, subErr := c.adapter.SubmitI2V(ctx, ProviderSubmitRequest{
		TaskID:          entry.snapshot.TaskID,
		TraceID:         entry.snapshot.TraceID,
		IdempotencyKey:  entry.scope.IdempotencyKey,
		TaskFingerprint: entry.fingerprint,
		ModelID:         entry.snapshot.Qualification.ModelID,
		ProductRef:      entry.req.ProductRef,
		SignedImageURL:  signedURL,
		Prompt:          strings.TrimSpace(entry.req.Prompt),
		CameraMove:      strings.TrimSpace(entry.req.CameraMove),
		DurationSeconds: entry.req.DurationSeconds,
		AspectRatio:     strings.TrimSpace(entry.req.AspectRatio),
		Preference:      entry.req.Policy.SelectedPreference,
	})
	if subErr != nil {
		entry.snapshot.LastError = subErr.Error()
		if fidelitygate.IsAmbiguousTransportError(subErr) {
			entry.providerJobID = strings.TrimSpace(subResp.ProviderJobID)
			entry.snapshot.Queue.QueueState = QueueStateUnknown
			entry.snapshot.Queue.WaitReason = "provider_submit_outcome_unknown"
			snap := entry.snapshot.clone()
			snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
			return snap, ErrTaskOutcomeUnknown
		}
		c.finishFailedGenerateCounter(entry.scope.TenantID, entry.scope.ProjectID)
		entry.snapshot.Queue.QueueState = QueueStateFailed
		entry.snapshot.FailedStage = StageGenerate
		if entry.holdActive && entry.req.Cost.PayerType != PayerFreeGrant {
			rec, _ := c.billing.HandleProviderFailureBeforeDelivery(ctx, entry.scope.billingScope(), subErr.Error())
			entry.snapshot.Billing = billingReceiptFromRecovery(entry.req.Cost, rec)
			entry.billingSettled = true
		}
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, subErr
	}

	entry.providerJobID = strings.TrimSpace(subResp.ProviderJobID)
	entry.generatedAssetID = strings.TrimSpace(subResp.OutputAssetID)
	entry.generatedVersionID = strings.TrimSpace(subResp.OutputVersionID)
	entry.generatedVideoURL = strings.TrimSpace(subResp.RawVideoURL)
	entry.generateSucceeded = true
	entry.providerCharged = subResp.ProviderCharged
	entry.actualCostFen = subResp.ActualCostFen

	c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
		if cnt.VideoGenerate.Running > 0 {
			cnt.VideoGenerate.Running--
		}
		cnt.VideoGenerate.Succeeded++
	})

	return c.finishPostGenerateLocked(ctx, entry, runEnhance, false)
}

func (c *Coordinator) finishPostGenerateLocked(ctx context.Context, entry *taskEntry, runEnhance bool, isRetry bool) (TaskSnapshot, error) {
	videoURL := entry.generatedVideoURL
	assetID := entry.generatedAssetID
	versionID := entry.generatedVersionID

	// Stage: Enhance (optional; retryable independently without regenerating).
	if entry.req.EnhanceEnabled && runEnhance && !entry.enhanceSucceeded {
		entry.snapshot.Queue.QueueState = QueueStateEnhancing
		entry.snapshot.Queue.ActiveStage = StageEnhance
		entry.snapshot.EnhanceCalls++
		c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
			if isRetry && entry.snapshot.FailedStage == StageEnhance {
				cnt.VideoEnhance.Retried++
			} else {
				cnt.VideoEnhance.Total++
			}
			cnt.VideoEnhance.Running++
		})

		enhResp, enhErr := c.adapter.EnhanceVideo(ctx, ProviderEnhanceRequest{
			TaskID:          entry.snapshot.TaskID,
			ProviderJobID:   entry.providerJobID,
			InputVideoURL:   entry.generatedVideoURL,
			OutputAssetID:   entry.generatedAssetID,
			OutputVersionID: entry.generatedVersionID,
		})
		if enhErr != nil {
			c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
				if cnt.VideoEnhance.Running > 0 {
					cnt.VideoEnhance.Running--
				}
				cnt.VideoEnhance.Failed++
			})
			entry.snapshot.Queue.QueueState = QueueStateFailed
			entry.snapshot.FailedStage = StageEnhance
			entry.snapshot.LastError = enhErr.Error()
			snap := entry.snapshot.clone()
			snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
			return snap, enhErr
		}
		c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
			if cnt.VideoEnhance.Running > 0 {
				cnt.VideoEnhance.Running--
			}
			cnt.VideoEnhance.Succeeded++
		})
		entry.enhanceSucceeded = true
		entry.enhancedVideoURL = strings.TrimSpace(enhResp.EnhancedVideoURL)
		if strings.TrimSpace(enhResp.OutputAssetID) != "" {
			entry.generatedAssetID = strings.TrimSpace(enhResp.OutputAssetID)
		}
		if strings.TrimSpace(enhResp.OutputVersionID) != "" {
			entry.generatedVersionID = strings.TrimSpace(enhResp.OutputVersionID)
		}
	}

	if entry.enhanceSucceeded && entry.enhancedVideoURL != "" {
		videoURL = entry.enhancedVideoURL
		assetID = entry.generatedAssetID
		versionID = entry.generatedVersionID
	}

	// Stage: Download & full-clip decode/fidelity inspection (retryable independently).
	entry.snapshot.Queue.QueueState = QueueStateDownloading
	entry.snapshot.Queue.ActiveStage = StageDownload
	entry.snapshot.DownloadCalls++
	c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
		if isRetry && entry.snapshot.FailedStage == StageDownload {
			cnt.Download.Retried++
		} else {
			cnt.Download.Total++
		}
		cnt.Download.Running++
	})

	metrics, dlErr := c.adapter.DownloadAndInspect(ctx, ProviderDownloadRequest{
		TaskID:          entry.snapshot.TaskID,
		VideoURL:        videoURL,
		OutputAssetID:   assetID,
		OutputVersionID: versionID,
		ProductRef:      entry.req.ProductRef,
	})
	if dlErr != nil {
		c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
			if cnt.Download.Running > 0 {
				cnt.Download.Running--
			}
			cnt.Download.Failed++
		})
		entry.snapshot.Queue.QueueState = QueueStateFailed
		entry.snapshot.FailedStage = StageDownload
		entry.snapshot.LastError = dlErr.Error()
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, dlErr
	}
	c.updateCounter(entry.scope.TenantID, entry.scope.ProjectID, func(cnt *BatchStageCounters) {
		if cnt.Download.Running > 0 {
			cnt.Download.Running--
		}
		cnt.Download.Succeeded++
	})

	// Stage: Fidelity gate & billing settlement & merchant candidate return.
	entry.snapshot.Queue.ActiveStage = StageVerify
	fidelityAssessment := EvaluateVideoFidelity(metrics, entry.req.ProductRef.SubjectLock.View, c.thresholds)

	gateDecision := fidelitygate.GateDecision{
		Passed:           fidelityAssessment.Passed,
		Verdict:          fidelitygate.VerdictFail,
		FailureReasons:   append([]string(nil), fidelityAssessment.FailureReasons...),
		EvaluatorVersion: fidelityAssessment.EvaluatorVersion,
		ThresholdVersion: fidelityAssessment.ThresholdVersion,
	}
	if fidelityAssessment.Passed {
		gateDecision.Verdict = fidelitygate.VerdictPass
		gateDecision.RepairAction = fidelitygate.RepairActionNone
	} else {
		gateDecision.RepairAction = fidelitygate.RepairActionManualReviewOrRegenerate
	}

	if entry.req.Cost.PayerType != PayerFreeGrant && entry.holdActive && !entry.billingSettled {
		rec, sErr := c.billing.SettleAfterQualityGate(
			ctx,
			entry.scope.billingScope(),
			gateDecision,
			entry.providerCharged,
			c.failurePolicy,
		)
		entry.snapshot.Billing = billingReceiptFromRecovery(entry.req.Cost, rec)
		if entry.actualCostFen > 0 && rec.State == fidelitygate.BillingSpent {
			entry.snapshot.Billing.ActualCostFen = entry.actualCostFen
		} else if rec.State == fidelitygate.BillingSpent {
			entry.snapshot.Billing.ActualCostFen = entry.req.Cost.EstimatedCostFen
		}
		if sErr != nil {
			entry.snapshot.LastError = sErr.Error()
			if errors.Is(sErr, fidelitygate.ErrBillingUnknown) || errors.Is(sErr, fidelitygate.ErrReconcileRequired) {
				entry.snapshot.Queue.QueueState = QueueStateUnknown
				entry.snapshot.Queue.WaitReason = "billing_settle_unknown_reconcile_required"
				snap := entry.snapshot.clone()
				snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
				return snap, sErr
			}
			entry.snapshot.Queue.QueueState = QueueStateFailed
			entry.snapshot.FailedStage = StageVerify
			snap := entry.snapshot.clone()
			snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
			return snap, sErr
		}
		entry.billingSettled = true
	} else if entry.req.Cost.PayerType == PayerFreeGrant {
		entry.snapshot.Billing.BillingState = "free_grant_consumed"
		entry.snapshot.Billing.ActualCostFen = 0
		entry.snapshot.Billing.AutoChargedPaid = false
	}

	if !fidelityAssessment.Passed {
		entry.snapshot.Queue.QueueState = QueueStateFailed
		entry.snapshot.FailedStage = StageVerify
		entry.snapshot.LastError = ErrVideoFidelityFailed.Error()
		entry.snapshot.Candidate = &CandidateReturnRecord{
			Destination:           CandidateDestinationMerchantLibrary,
			CandidateID:           "cand_" + entry.snapshot.TaskID,
			TaskID:                entry.snapshot.TaskID,
			TenantID:              entry.scope.TenantID,
			ProjectID:             entry.scope.ProjectID,
			OutputAssetID:         assetID,
			OutputVersionID:       versionID,
			OutputSHA256:          strings.ToLower(strings.TrimSpace(metrics.FileSHA256)),
			ActualDurationSeconds: metrics.ActualDurationSeconds,
			VideoURL:              "", // do not surface rejected video URL for playback
			AutoPublished:         false,
			RewardTriggered:       false,
			MerchantConfirmed:     false,
			BackViewUnproven:      fidelityAssessment.BackViewUnproven,
			CannotClaimTrue360:    fidelityAssessment.CannotClaimTrue360,
			QualityLimitations:    appendUniqueStrings(fidelityAssessment.QualityLimitations, "fidelity_gate_failed"),
			Fidelity:              fidelityAssessment,
		}
		snap := entry.snapshot.clone()
		snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
		return snap, ErrVideoFidelityFailed
	}

	// Candidate returns strictly to merchant candidate library (NEVER auto-published or auto-rewarded).
	entry.snapshot.Queue.QueueState = QueueStateCompleted
	entry.snapshot.Queue.WaitReason = ""
	entry.snapshot.FailedStage = ""
	entry.snapshot.LastError = ""
	entry.snapshot.Candidate = &CandidateReturnRecord{
		Destination:           CandidateDestinationMerchantLibrary,
		CandidateID:           "cand_" + entry.snapshot.TaskID,
		TaskID:                entry.snapshot.TaskID,
		TenantID:              entry.scope.TenantID,
		ProjectID:             entry.scope.ProjectID,
		OutputAssetID:         assetID,
		OutputVersionID:       versionID,
		OutputSHA256:          strings.ToLower(strings.TrimSpace(metrics.FileSHA256)),
		ActualDurationSeconds: metrics.ActualDurationSeconds,
		VideoURL:              strings.TrimSpace(metrics.VideoURL),
		AutoPublished:         false,
		RewardTriggered:       false,
		MerchantConfirmed:     false,
		BackViewUnproven:      fidelityAssessment.BackViewUnproven,
		CannotClaimTrue360:    fidelityAssessment.CannotClaimTrue360,
		QualityLimitations:    append([]string(nil), fidelityAssessment.QualityLimitations...),
		Fidelity:              fidelityAssessment,
	}

	snap := entry.snapshot.clone()
	snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
	return snap, nil
}

func (c *Coordinator) resolveModelQualification(req SubmitRequest) ModelQualification {
	if req.ModelOverride != nil {
		return *req.ModelOverride
	}
	if c.adapter != nil {
		return c.adapter.Qualification()
	}
	return ModelQualification{}
}

func (c *Coordinator) markBlockedLocked(entry *taskEntry, blockCode string, cause error) (TaskSnapshot, error) {
	entry.snapshot.Queue.QueueState = QueueStateBlocked
	entry.snapshot.Queue.WaitReason = blockCode
	entry.snapshot.BlockCode = blockCode
	entry.snapshot.LastError = cause.Error()
	entry.snapshot.BasicImagePathBlocked = false
	entry.executed = true
	snap := entry.snapshot.clone()
	snap.Queue.StageCounters = c.StageCounters(entry.scope.TenantID, entry.scope.ProjectID)
	return snap, cause
}

func (c *Coordinator) lookupOrReserve(req SubmitRequest, fp string) (*taskEntry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tID := strings.TrimSpace(req.Scope.TenantID)
	pID := strings.TrimSpace(req.Scope.PrincipalID)
	idemKey := idempotencyKey{
		tenantID:    tID,
		principalID: pID,
		key:         strings.TrimSpace(req.Scope.IdempotencyKey),
	}
	fpKey := fingerprintKey{
		tenantID:    tID,
		principalID: pID,
		fingerprint: fp,
	}

	if existing, ok := c.byIdempotency[idemKey]; ok {
		if existing.fingerprint != fp {
			return nil, false, ErrIdempotencyConflict
		}
		return existing, true, nil
	}
	if existing, ok := c.byFingerprint[fpKey]; ok {
		return existing, true, nil
	}

	c.nextSeq++
	taskID := fmt.Sprintf("i2v_%06d", c.nextSeq)
	entry := &taskEntry{
		fingerprint: fp,
		scope:       req.Scope,
		req:         req,
		snapshot: TaskSnapshot{
			TaskID:                taskID,
			TenantID:              tID,
			ProjectID:             strings.TrimSpace(req.Scope.ProjectID),
			IdempotencyKey:        strings.TrimSpace(req.Scope.IdempotencyKey),
			TaskFingerprint:       fp,
			BasicImagePathBlocked: false,
			Queue: QueueProjection{
				JobID:               taskID,
				QueueState:          QueueStateQueued,
				ActiveStage:         StageGenerate,
				ExecutionPreference: req.Policy.SelectedPreference,
				PayerType:           req.Cost.PayerType,
			},
		},
	}
	c.byID[taskID] = entry
	c.byIdempotency[idemKey] = entry
	c.byFingerprint[fpKey] = entry
	return entry, false, nil
}

func (c *Coordinator) lookupTaskWithAccess(access AccessScope, taskID string) (*taskEntry, error) {
	c.mu.Lock()
	entry, ok := c.byID[strings.TrimSpace(taskID)]
	c.mu.Unlock()
	if !ok {
		return nil, ErrTaskNotFound
	}
	if strings.TrimSpace(access.TenantID) == "" ||
		strings.TrimSpace(access.PrincipalID) == "" ||
		strings.TrimSpace(access.TenantID) != strings.TrimSpace(entry.scope.TenantID) ||
		strings.TrimSpace(access.PrincipalID) != strings.TrimSpace(entry.scope.PrincipalID) {
		return nil, ErrCrossTenantForbidden
	}
	if strings.TrimSpace(access.ProjectID) != "" &&
		strings.TrimSpace(access.ProjectID) != strings.TrimSpace(entry.scope.ProjectID) {
		return nil, ErrCrossTenantForbidden
	}
	return entry, nil
}

func (c *Coordinator) updateCounter(tenantID, projectID string, fn func(*BatchStageCounters)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := projectCounterKey{
		tenantID:  strings.TrimSpace(tenantID),
		projectID: strings.TrimSpace(projectID),
	}
	cnt, ok := c.counters[key]
	if !ok {
		cnt = &BatchStageCounters{}
		c.counters[key] = cnt
	}
	fn(cnt)
}

func (c *Coordinator) decrementPendingVideoGenerate(tenantID, projectID string) {
	c.updateCounter(tenantID, projectID, func(cnt *BatchStageCounters) {
		if cnt.VideoGenerate.Pending > 0 {
			cnt.VideoGenerate.Pending--
		}
		cnt.VideoGenerate.Failed++
	})
}

func (c *Coordinator) finishFailedGenerateCounter(tenantID, projectID string) {
	c.updateCounter(tenantID, projectID, func(cnt *BatchStageCounters) {
		if cnt.VideoGenerate.Running > 0 {
			cnt.VideoGenerate.Running--
		}
		cnt.VideoGenerate.Failed++
	})
}

func billingReceiptFromRecovery(cost CostAttribution, rec fidelitygate.RecoveryRecord) BillingReceiptRef {
	actual := int64(0)
	if rec.State == fidelitygate.BillingSpent {
		actual = cost.EstimatedCostFen
	}
	return BillingReceiptRef{
		PayerType:       cost.PayerType,
		QuoteID:         strings.TrimSpace(cost.QuoteID),
		PricingVersion:  strings.TrimSpace(cost.PricingVersion),
		HoldID:          rec.HoldID,
		SpendID:         rec.SpendID,
		RefundID:        rec.RefundID,
		BillingState:    string(rec.State),
		ActualCostFen:   actual,
		FreeQuotaGrant:  strings.TrimSpace(cost.FreeQuotaGrantID),
		AutoChargedPaid: false,
	}
}

func cloneTimePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	cp := t.UTC()
	return &cp
}

func cloneInt64Ptr(v *int64) *int64 {
	if v == nil {
		return nil
	}
	cp := *v
	return &cp
}
