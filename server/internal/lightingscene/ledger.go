package lightingscene

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

const (
	// CapabilityLightingRelight is the platform-billing capability identifier
	// for lighting/scene relighting holds.
	CapabilityLightingRelight = "product_image.lighting_relight"
)

// TaskStatus tracks the execution lifecycle of a lighting derivation task.
type TaskStatus string

const (
	TaskStatusBlocked   TaskStatus = "BLOCKED"
	TaskStatusQueued    TaskStatus = "QUEUED"
	TaskStatusSucceeded TaskStatus = "SUCCEEDED"
	TaskStatusFailed    TaskStatus = "FAILED"
	TaskStatusUnknown   TaskStatus = "UNKNOWN"
)

var (
	ErrNilBillingMachine      = errors.New("lightingscene: fidelitygate billing state machine is nil")
	ErrProviderUnavailable    = errors.New("lightingscene: relight provider is not configured")
	ErrReceiptNotFound        = errors.New("lightingscene: derivation receipt not found")
	ErrTaskOutcomeUnknown     = errors.New("lightingscene: task outcome is UNKNOWN; reconcile/refresh instead of re-invoking provider")
	ErrFidelityGateRejected   = errors.New("lightingscene: candidate rejected by lighting/subject fidelity gate")
	ErrVersionNotSelectable   = errors.New("lightingscene: version cannot be selected until fidelity gate passes and billing spend settles")
	ErrIdempotencyKeyConflict = errors.New("lightingscene: idempotency_key already bound to a different task_fingerprint")
	ErrIncompleteReceipt      = errors.New("lightingscene: derivation receipt missing required traceable fields")
	ErrInvalidFailurePolicy   = errors.New("lightingscene: explicit gate failure billing policy is required")
)

// DerivationReceipt is the unified traceable lighting/scene derivation record
// directly consumable by HUI-2232 and GoBoost.
type DerivationReceipt struct {
	TaskID          string `json:"task_id"`
	AssetID         string `json:"asset_id"`
	VersionID       string `json:"version_id"`
	ModelID         string `json:"model_id"`
	ProviderID      string `json:"provider_id"`
	TaskFingerprint string `json:"task_fingerprint"`
	QuoteID         string `json:"quote_id"`
	ActualCostFen   int64  `json:"actual_cost_fen"`
	GrantSource     string `json:"grant_source"`
	Mode            Mode   `json:"mode"`
	Selected        bool   `json:"selected"`

	// Platform-billing and HUI-2232 fidelitygate coordination links.
	AppID           string                              `json:"app_id"`
	PrincipalID     string                              `json:"principal_id"`
	RequestKey      string                              `json:"request_key"`
	IdempotencyKey  string                              `json:"idempotency_key"`
	HoldID          string                              `json:"hold_id,omitempty"`
	SpendID         string                              `json:"spend_id,omitempty"`
	RefundID        string                              `json:"refund_id,omitempty"`
	BillingState    fidelitygate.BillingState           `json:"billing_state"`
	CandidateState  fidelitygate.CandidateAdoptionState `json:"candidate_state"`
	TaskStatus      TaskStatus                          `json:"task_status"`
	SampleClass     LightingSampleClass                 `json:"sample_class,omitempty"`
	SubjectAssetID  string                              `json:"subject_asset_id"`
	SubjectSHA256   string                              `json:"subject_sha256"`
	CandidateSHA256 string                              `json:"candidate_sha256,omitempty"`
	GateVerdict     fidelitygate.GateVerdict            `json:"gate_verdict,omitempty"`
	FailureReasons  []string                            `json:"failure_reasons,omitempty"`
}

func (r DerivationReceipt) clone() DerivationReceipt {
	cp := r
	if r.FailureReasons != nil {
		cp.FailureReasons = append([]string(nil), r.FailureReasons...)
	}
	return cp
}

// ValidateTraceable verifies that a succeeded receipt contains every required
// traceable identifier for HUI-2232 and GoBoost downstream consumers.
func (r DerivationReceipt) ValidateTraceable() error {
	if r.TaskStatus != TaskStatusSucceeded {
		return fmt.Errorf("%w: task_status is %q", ErrIncompleteReceipt, r.TaskStatus)
	}
	if strings.TrimSpace(r.TaskID) == "" ||
		strings.TrimSpace(r.AssetID) == "" ||
		strings.TrimSpace(r.VersionID) == "" ||
		strings.TrimSpace(r.ModelID) == "" ||
		strings.TrimSpace(r.ProviderID) == "" ||
		strings.TrimSpace(r.TaskFingerprint) == "" ||
		strings.TrimSpace(r.QuoteID) == "" ||
		strings.TrimSpace(r.GrantSource) == "" ||
		(r.Mode != ModeFidelityLocked && r.Mode != ModeCreativeExplicit) {
		return ErrIncompleteReceipt
	}
	if r.BillingState != fidelitygate.BillingSpent || r.ActualCostFen <= 0 {
		return fmt.Errorf("%w: succeeded receipt must reflect settled platform spend", ErrIncompleteReceipt)
	}
	if r.CandidateState != fidelitygate.CandidateAdopted || r.GateVerdict != fidelitygate.VerdictPass {
		return fmt.Errorf("%w: succeeded receipt must reflect passed gate and adopted candidate", ErrIncompleteReceipt)
	}
	return nil
}

// ProviderRelightRequest is sent to the configured image relighting provider
// only after ValidateLightingSceneRequest and platform-billing Hold succeed.
type ProviderRelightRequest struct {
	TaskID          string
	IdempotencyKey  string
	TaskFingerprint string
	Mode            Mode
	Model           ModelDescriptor
	Anchor          SubjectAnchor
	Lighting        LightingSpec
}

// ProviderRelightResponse captures the provider execution output and candidate
// measurements for fidelity gating.
type ProviderRelightResponse struct {
	ProviderTaskID  string
	AssetID         string
	VersionID       string
	ProviderCharged bool
	ActualCostFen   int64
	Candidate       LightingCandidateMetrics
}

// ProviderLookupResponse captures read-only reconciliation state from the
// provider during UNKNOWN recovery / Refresh without re-invoking generation.
type ProviderLookupResponse struct {
	Status          TaskStatus
	AssetID         string
	VersionID       string
	ProviderCharged bool
	ActualCostFen   int64
	Candidate       *LightingCandidateMetrics
}

// RelightProvider is the upstream image-editing relighting port.
type RelightProvider interface {
	Relight(ctx context.Context, req ProviderRelightRequest) (ProviderRelightResponse, error)
	LookupTask(ctx context.Context, taskID, idempotencyKey string) (ProviderLookupResponse, error)
}

type idempotencyIndexKey struct {
	principalID    string
	idempotencyKey string
}

type fingerprintIndexKey struct {
	principalID     string
	taskFingerprint string
}

type subjectIndexKey struct {
	principalID    string
	subjectAssetID string
}

type ledgerEntry struct {
	fingerprint string // immutable after construction
	mu          sync.Mutex
	executed    bool
	req         LightingSceneRequest
	receipt     DerivationReceipt
	lastErr     error
}

// LightingLedger coordinates lighting/scene derivation pre-flight, single-flight
// idempotent execution, fidelity evaluation, unified DerivationReceipt recording,
// UNKNOWN/refresh recovery, and user-selected version protection.
//
// Zero second billing ledger: all Hold/Spend/Refund/Reconcile transitions are
// delegated strictly to *fidelitygate.BillingRecoveryStateMachine.
type LightingLedger struct {
	billing       *fidelitygate.BillingRecoveryStateMachine
	provider      RelightProvider
	fidelityCfg   LightingFidelityConfig
	failurePolicy fidelitygate.GateFailureBillingPolicy

	selectMu sync.Mutex // serializes user SelectVersion updates (outermost lock)

	mu                sync.Mutex // protects only map lookups/updates; never locks entry.mu inside
	byKey             map[idempotencyIndexKey]*ledgerEntry
	byFingerprint     map[fingerprintIndexKey]*ledgerEntry
	selectedEntries   map[subjectIndexKey]*ledgerEntry
	selectedReceipts  map[subjectIndexKey]DerivationReceipt
	priorFidelityFail map[subjectIndexKey]bool
}

// NewLightingLedger constructs a LightingLedger backed by fidelitygate's
// BillingRecoveryStateMachine.
func NewLightingLedger(
	billing *fidelitygate.BillingRecoveryStateMachine,
	provider RelightProvider,
	fidelityCfg LightingFidelityConfig,
	failurePolicy fidelitygate.GateFailureBillingPolicy,
) (*LightingLedger, error) {
	if billing == nil {
		return nil, ErrNilBillingMachine
	}
	if failurePolicy != fidelitygate.GateFailurePolicySettleProviderSpend &&
		failurePolicy != fidelitygate.GateFailurePolicyRefundPlatformAbsorbed {
		return nil, ErrInvalidFailurePolicy
	}
	return &LightingLedger{
		billing:           billing,
		provider:          provider,
		fidelityCfg:       fidelityCfg,
		failurePolicy:     failurePolicy,
		byKey:             make(map[idempotencyIndexKey]*ledgerEntry),
		byFingerprint:     make(map[fingerprintIndexKey]*ledgerEntry),
		selectedEntries:   make(map[subjectIndexKey]*ledgerEntry),
		selectedReceipts:  make(map[subjectIndexKey]DerivationReceipt),
		priorFidelityFail: make(map[subjectIndexKey]bool),
	}, nil
}

// Execute validates a lighting/scene derivation request fail-closed, deduplicates
// concurrent retries on the same (idempotency_key / task_fingerprint), holds
// funds via fidelitygate, invokes the relighting provider at most once, evaluates
// subject+lighting fidelity, settles billing, and records a unified DerivationReceipt.
func (l *LightingLedger) Execute(ctx context.Context, req LightingSceneRequest) (DerivationReceipt, error) {
	// Server-side enforcement: remember prior fidelity failures per subject so
	// callers cannot omit PriorFidelityFailed to silently switch into creative mode.
	if req.Mode == ModeCreativeExplicit && l.hasPriorFidelityFailure(req.Scope.PrincipalID, req.Anchor.SubjectAssetID) {
		req.PriorFidelityFailed = true
	}

	valRes, err := ValidateLightingSceneRequest(req)
	if err != nil {
		status := TaskStatusFailed
		if errors.Is(err, ErrBlocked) || valRes.Status == PreflightBlocked {
			status = TaskStatusBlocked
		}
		return DerivationReceipt{
			TaskID:          strings.TrimSpace(req.TaskID),
			ModelID:         strings.TrimSpace(req.Model.ModelID),
			ProviderID:      strings.TrimSpace(req.Model.ProviderID),
			TaskFingerprint: valRes.TaskFingerprint,
			QuoteID:         strings.TrimSpace(req.QuoteID),
			ActualCostFen:   0,
			GrantSource:     strings.TrimSpace(req.Credentials.GrantSource),
			Mode:            req.Mode,
			Selected:        false,
			AppID:           strings.TrimSpace(req.Scope.AppID),
			PrincipalID:     strings.TrimSpace(req.Scope.PrincipalID),
			RequestKey:      strings.TrimSpace(req.Scope.RequestKey),
			IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
			BillingState:    fidelitygate.BillingInit,
			CandidateState:  fidelitygate.CandidateNone,
			TaskStatus:      status,
			SampleClass:     req.SampleClass,
			SubjectAssetID:  strings.TrimSpace(req.Anchor.SubjectAssetID),
			SubjectSHA256:   strings.TrimSpace(req.Anchor.SubjectSHA256),
		}, err
	}

	entry, conflict := l.getOrCreateEntry(req, valRes.TaskFingerprint)
	if conflict {
		return DerivationReceipt{
			TaskFingerprint: valRes.TaskFingerprint,
			QuoteID:         strings.TrimSpace(req.QuoteID),
			ActualCostFen:   0,
			Mode:            req.Mode,
			TaskStatus:      TaskStatusBlocked,
			BillingState:    fidelitygate.BillingInit,
			CandidateState:  fidelitygate.CandidateNone,
			IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
		}, ErrIdempotencyKeyConflict
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	// Idempotent replay / concurrent retry protection: NEVER double-invoke provider,
	// NEVER double-charge, and NEVER overwrite a user-selected version.
	if entry.executed {
		switch entry.receipt.TaskStatus {
		case TaskStatusSucceeded:
			return entry.receipt.clone(), nil
		case TaskStatusUnknown:
			return entry.receipt.clone(), ErrTaskOutcomeUnknown
		case TaskStatusFailed:
			if entry.lastErr != nil {
				return entry.receipt.clone(), entry.lastErr
			}
			return entry.receipt.clone(), ErrFidelityGateRejected
		default:
			return entry.receipt.clone(), entry.lastErr
		}
	}

	// Fail-closed if no provider is configured (0 provider calls, 0 billing holds).
	if l.provider == nil {
		entry.receipt.TaskStatus = TaskStatusBlocked
		entry.receipt.ActualCostFen = 0
		entry.lastErr = ErrProviderUnavailable
		return entry.receipt.clone(), ErrProviderUnavailable
	}

	// 1. Place platform-billing Hold via fidelitygate (zero second billing ledger).
	holdRec, holdErr := l.billing.BeginHold(
		ctx,
		req.Scope,
		CapabilityLightingRelight,
		req.PricingVersion,
		req.EstimatedCostFen,
	)
	if holdErr != nil {
		entry.receipt.HoldID = holdRec.HoldID
		entry.receipt.BillingState = holdRec.State
		entry.receipt.CandidateState = holdRec.CandidateState
		entry.receipt.ActualCostFen = 0
		if errors.Is(holdErr, fidelitygate.ErrBillingUnknown) || errors.Is(holdErr, fidelitygate.ErrReconcileRequired) {
			entry.executed = true
			entry.receipt.TaskStatus = TaskStatusUnknown
			entry.lastErr = ErrTaskOutcomeUnknown
			return entry.receipt.clone(), fmt.Errorf("%w: %v", ErrTaskOutcomeUnknown, holdErr)
		}
		entry.executed = true
		entry.receipt.TaskStatus = TaskStatusFailed
		entry.lastErr = holdErr
		return entry.receipt.clone(), holdErr
	}
	entry.receipt.HoldID = holdRec.HoldID
	entry.receipt.BillingState = holdRec.State
	entry.receipt.CandidateState = holdRec.CandidateState
	entry.receipt.TaskStatus = TaskStatusQueued

	// 2. Invoke upstream relighting provider AT MOST ONCE.
	entry.executed = true
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		taskID = "ltask_" + valRes.TaskFingerprint[:16]
	}
	entry.receipt.TaskID = taskID

	provResp, provErr := l.provider.Relight(ctx, ProviderRelightRequest{
		TaskID:          taskID,
		IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
		TaskFingerprint: valRes.TaskFingerprint,
		Mode:            req.Mode,
		Model:           req.Model,
		Anchor:          req.Anchor,
		Lighting:        req.Lighting,
	})
	if provErr != nil {
		if fidelitygate.IsAmbiguousTransportError(provErr) {
			unkRec, _ := l.billing.MarkTransportUnknown(req.Scope, fidelitygate.ActionNone, provErr.Error())
			entry.receipt.BillingState = unkRec.State
			entry.receipt.CandidateState = unkRec.CandidateState
			entry.receipt.TaskStatus = TaskStatusUnknown
			entry.receipt.ActualCostFen = 0
			entry.lastErr = ErrTaskOutcomeUnknown
			return entry.receipt.clone(), fmt.Errorf("%w: %v", ErrTaskOutcomeUnknown, provErr)
		}

		refRec, refErr := l.billing.HandleProviderFailureBeforeDelivery(ctx, req.Scope, provErr.Error())
		entry.receipt.BillingState = refRec.State
		entry.receipt.RefundID = refRec.RefundID
		entry.receipt.CandidateState = refRec.CandidateState
		entry.receipt.ActualCostFen = 0
		if refErr != nil && (errors.Is(refErr, fidelitygate.ErrBillingUnknown) || errors.Is(refErr, fidelitygate.ErrReconcileRequired)) {
			entry.receipt.TaskStatus = TaskStatusUnknown
			entry.lastErr = ErrTaskOutcomeUnknown
			return entry.receipt.clone(), fmt.Errorf("%w: %v", ErrTaskOutcomeUnknown, refErr)
		}
		entry.receipt.TaskStatus = TaskStatusFailed
		entry.lastErr = provErr
		return entry.receipt.clone(), provErr
	}

	if strings.TrimSpace(provResp.ProviderTaskID) != "" {
		entry.receipt.TaskID = strings.TrimSpace(provResp.ProviderTaskID)
	}

	// 3. Evaluate subject + lighting fidelity and settle billing.
	return l.evaluateAndSettleLocked(ctx, entry, provResp.AssetID, provResp.VersionID, provResp.ProviderCharged, provResp.ActualCostFen, provResp.Candidate)
}

// Refresh reconciles an existing task by (principal_id, idempotency_key) without
// EVER re-invoking provider.Relight, double-charging, or overwriting a user-selected version.
func (l *LightingLedger) Refresh(ctx context.Context, principalID, idempotencyKey string) (DerivationReceipt, error) {
	entry, ok := l.lookupByKey(principalID, idempotencyKey)
	if !ok {
		return DerivationReceipt{}, ErrReceiptNotFound
	}
	return l.refreshEntry(ctx, entry)
}

// RefreshByFingerprint reconciles an existing task by (principal_id, task_fingerprint)
// without EVER re-invoking provider.Relight, double-charging, or overwriting a user-selected version.
func (l *LightingLedger) RefreshByFingerprint(ctx context.Context, principalID, taskFingerprint string) (DerivationReceipt, error) {
	entry, ok := l.lookupByFingerprint(principalID, taskFingerprint)
	if !ok {
		return DerivationReceipt{}, ErrReceiptNotFound
	}
	return l.refreshEntry(ctx, entry)
}

func (l *LightingLedger) refreshEntry(ctx context.Context, entry *ledgerEntry) (DerivationReceipt, error) {
	entry.mu.Lock()
	defer entry.mu.Unlock()

	// If this version is already selected by the user, NEVER overwrite its
	// AssetID, VersionID, Mode, or Selected flag.
	if entry.receipt.Selected {
		if entry.receipt.BillingState == fidelitygate.BillingUnknown {
			rec, err := l.billing.Reconcile(ctx, entry.req.Scope)
			if err == nil {
				entry.receipt.HoldID = rec.HoldID
				entry.receipt.SpendID = rec.SpendID
				entry.receipt.RefundID = rec.RefundID
				entry.receipt.BillingState = rec.State
			}
		}
		return entry.receipt.clone(), nil
	}

	// Terminal settled states are strictly read-only on refresh.
	if entry.receipt.TaskStatus == TaskStatusSucceeded || entry.receipt.TaskStatus == TaskStatusFailed || entry.receipt.TaskStatus == TaskStatusBlocked {
		return entry.receipt.clone(), nil
	}

	// UNKNOWN recovery: first reconcile platform-billing state, then check
	// read-only provider LookupTask (NEVER calls provider.Relight).
	rec, recErr := l.billing.Reconcile(ctx, entry.req.Scope)
	if recErr != nil {
		entry.receipt.BillingState = rec.State
		entry.receipt.TaskStatus = TaskStatusUnknown
		entry.lastErr = ErrTaskOutcomeUnknown
		return entry.receipt.clone(), fmt.Errorf("%w: %v", ErrTaskOutcomeUnknown, recErr)
	}
	entry.receipt.HoldID = rec.HoldID
	entry.receipt.SpendID = rec.SpendID
	entry.receipt.RefundID = rec.RefundID
	entry.receipt.BillingState = rec.State
	entry.receipt.CandidateState = rec.CandidateState

	// If platform-billing confirmed the initial Hold never landed, no provider
	// call was made and zero charge occurred; close cleanly as FAILED without
	// invoking provider on Refresh.
	if rec.State == fidelitygate.BillingInit {
		entry.receipt.ActualCostFen = 0
		entry.receipt.TaskStatus = TaskStatusFailed
		entry.lastErr = errors.New("lightingscene: initial billing hold did not land")
		return entry.receipt.clone(), entry.lastErr
	}

	if l.provider == nil {
		return entry.receipt.clone(), ErrProviderUnavailable
	}

	lookup, lerr := l.provider.LookupTask(ctx, entry.receipt.TaskID, entry.receipt.IdempotencyKey)
	if lerr != nil || lookup.Status == TaskStatusUnknown || lookup.Status == TaskStatusQueued || lookup.Status == "" {
		entry.receipt.TaskStatus = TaskStatusUnknown
		entry.lastErr = ErrTaskOutcomeUnknown
		if lerr != nil {
			return entry.receipt.clone(), fmt.Errorf("%w: %v", ErrTaskOutcomeUnknown, lerr)
		}
		return entry.receipt.clone(), ErrTaskOutcomeUnknown
	}

	if lookup.Status == TaskStatusFailed {
		if entry.receipt.BillingState == fidelitygate.BillingHeld {
			refRec, rerr := l.billing.HandleProviderFailureBeforeDelivery(ctx, entry.req.Scope, "provider_task_failed_on_refresh")
			entry.receipt.BillingState = refRec.State
			entry.receipt.RefundID = refRec.RefundID
			entry.receipt.CandidateState = refRec.CandidateState
			if rerr != nil {
				entry.receipt.TaskStatus = TaskStatusUnknown
				entry.lastErr = ErrTaskOutcomeUnknown
				return entry.receipt.clone(), fmt.Errorf("%w: %v", ErrTaskOutcomeUnknown, rerr)
			}
		}
		if entry.receipt.BillingState != fidelitygate.BillingSpent {
			entry.receipt.ActualCostFen = 0
		}
		entry.receipt.TaskStatus = TaskStatusFailed
		entry.lastErr = errors.New("lightingscene: upstream provider task failed")
		return entry.receipt.clone(), entry.lastErr
	}

	if lookup.Status == TaskStatusSucceeded && lookup.Candidate != nil {
		return l.evaluateAndSettleLocked(ctx, entry, lookup.AssetID, lookup.VersionID, lookup.ProviderCharged, lookup.ActualCostFen, *lookup.Candidate)
	}

	entry.receipt.TaskStatus = TaskStatusUnknown
	entry.lastErr = ErrTaskOutcomeUnknown
	return entry.receipt.clone(), ErrTaskOutcomeUnknown
}

func (l *LightingLedger) evaluateAndSettleLocked(
	ctx context.Context,
	entry *ledgerEntry,
	assetID string,
	versionID string,
	providerCharged bool,
	providerActualCostFen int64,
	candidate LightingCandidateMetrics,
) (DerivationReceipt, error) {
	req := entry.req
	evalRes := EvaluateLightingFidelity(LightingFidelityInput{
		Scope:        req.Scope,
		Mode:         req.Mode,
		SampleClass:  req.SampleClass,
		SampleOrigin: req.SampleOrigin,
		Anchor:       req.Anchor,
		Lighting:     req.Lighting,
		Candidate:    candidate,
	}, l.fidelityCfg)

	combinedDecision := evalRes.CombinedGateDecision()
	entry.receipt.GateVerdict = combinedDecision.Verdict
	entry.receipt.FailureReasons = append([]string(nil), combinedDecision.FailureReasons...)
	entry.receipt.CandidateSHA256 = strings.TrimSpace(candidate.Report.CandidateSHA256)
	// Mode NEVER silently switches to ModeCreativeExplicit on fidelity failure.
	entry.receipt.Mode = req.Mode

	if !evalRes.Passed && req.Mode == ModeFidelityLocked {
		l.recordPriorFidelityFailure(req.Scope.PrincipalID, req.Anchor.SubjectAssetID)
	}

	settledRec, settleErr := l.billing.SettleAfterQualityGate(
		ctx,
		req.Scope,
		combinedDecision,
		providerCharged,
		l.failurePolicy,
	)
	if settleErr != nil {
		entry.receipt.HoldID = settledRec.HoldID
		entry.receipt.SpendID = settledRec.SpendID
		entry.receipt.RefundID = settledRec.RefundID
		entry.receipt.BillingState = settledRec.State
		entry.receipt.CandidateState = settledRec.CandidateState
		if errors.Is(settleErr, fidelitygate.ErrBillingUnknown) || errors.Is(settleErr, fidelitygate.ErrReconcileRequired) {
			entry.receipt.TaskStatus = TaskStatusUnknown
			entry.receipt.ActualCostFen = 0
			entry.lastErr = ErrTaskOutcomeUnknown
			return entry.receipt.clone(), fmt.Errorf("%w: %v", ErrTaskOutcomeUnknown, settleErr)
		}
		// If billing already settled as BillingSpent during prior reconcile on a passed candidate,
		// do not fail closed on ErrAlreadySettled if we already hold a valid SpendID.
		if !(errors.Is(settleErr, fidelitygate.ErrAlreadySettled) && settledRec.State == fidelitygate.BillingSpent && evalRes.Passed) {
			entry.receipt.TaskStatus = TaskStatusFailed
			entry.lastErr = settleErr
			return entry.receipt.clone(), settleErr
		}
	}

	entry.receipt.HoldID = settledRec.HoldID
	entry.receipt.SpendID = settledRec.SpendID
	entry.receipt.RefundID = settledRec.RefundID
	entry.receipt.BillingState = settledRec.State
	entry.receipt.CandidateState = settledRec.CandidateState

	// Protect already-selected asset/version invariants.
	if !entry.receipt.Selected {
		entry.receipt.AssetID = strings.TrimSpace(assetID)
		entry.receipt.VersionID = strings.TrimSpace(versionID)
	}

	if settledRec.State == fidelitygate.BillingSpent {
		cost := providerActualCostFen
		if cost <= 0 {
			cost = req.EstimatedCostFen
		}
		entry.receipt.ActualCostFen = cost
	} else {
		entry.receipt.ActualCostFen = 0
	}

	if evalRes.Passed && settledRec.State == fidelitygate.BillingSpent &&
		(settledRec.CandidateState == fidelitygate.CandidateAdopted || errors.Is(settleErr, fidelitygate.ErrAlreadySettled)) {
		entry.receipt.CandidateState = fidelitygate.CandidateAdopted
		entry.receipt.TaskStatus = TaskStatusSucceeded
		entry.lastErr = nil
		return entry.receipt.clone(), nil
	}

	entry.receipt.TaskStatus = TaskStatusFailed
	entry.lastErr = ErrFidelityGateRejected
	return entry.receipt.clone(), ErrFidelityGateRejected
}

// SelectVersion marks a succeeded, fidelity-passed, billing-settled derivation
// version as the user-selected version for its subject. Subsequent failed,
// UNKNOWN, or refreshed tasks NEVER overwrite this selected version.
func (l *LightingLedger) SelectVersion(principalID, idempotencyKey string) (DerivationReceipt, error) {
	l.selectMu.Lock()
	defer l.selectMu.Unlock()

	entry, ok := l.lookupByKey(principalID, idempotencyKey)
	if !ok {
		return DerivationReceipt{}, ErrReceiptNotFound
	}

	entry.mu.Lock()
	if entry.receipt.TaskStatus != TaskStatusSucceeded ||
		entry.receipt.CandidateState != fidelitygate.CandidateAdopted ||
		entry.receipt.BillingState != fidelitygate.BillingSpent ||
		strings.TrimSpace(entry.receipt.AssetID) == "" ||
		strings.TrimSpace(entry.receipt.VersionID) == "" {
		cp := entry.receipt.clone()
		entry.mu.Unlock()
		return cp, ErrVersionNotSelectable
	}
	entry.receipt.Selected = true
	snap := entry.receipt.clone()
	subKey := subjectIndexKey{
		principalID:    strings.TrimSpace(entry.receipt.PrincipalID),
		subjectAssetID: strings.TrimSpace(entry.receipt.SubjectAssetID),
	}
	entry.mu.Unlock()

	l.mu.Lock()
	prev := l.selectedEntries[subKey]
	l.selectedEntries[subKey] = entry
	l.selectedReceipts[subKey] = snap
	l.mu.Unlock()

	if prev != nil && prev != entry {
		prev.mu.Lock()
		prev.receipt.Selected = false
		prev.mu.Unlock()
	}

	return snap, nil
}

// GetReceipt returns the latest receipt snapshot by (principal_id, idempotency_key).
func (l *LightingLedger) GetReceipt(principalID, idempotencyKey string) (DerivationReceipt, bool) {
	entry, ok := l.lookupByKey(principalID, idempotencyKey)
	if !ok {
		return DerivationReceipt{}, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.receipt.clone(), true
}

// GetReceiptByFingerprint returns the latest receipt snapshot by (principal_id, task_fingerprint).
func (l *LightingLedger) GetReceiptByFingerprint(principalID, taskFingerprint string) (DerivationReceipt, bool) {
	entry, ok := l.lookupByFingerprint(principalID, taskFingerprint)
	if !ok {
		return DerivationReceipt{}, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.receipt.clone(), true
}

// GetSelectedReceipt returns the user-selected version for (principal_id, subject_asset_id).
func (l *LightingLedger) GetSelectedReceipt(principalID, subjectAssetID string) (DerivationReceipt, bool) {
	key := subjectIndexKey{
		principalID:    strings.TrimSpace(principalID),
		subjectAssetID: strings.TrimSpace(subjectAssetID),
	}
	l.mu.Lock()
	rec, ok := l.selectedReceipts[key]
	l.mu.Unlock()
	if !ok {
		return DerivationReceipt{}, false
	}
	return rec.clone(), true
}

func (l *LightingLedger) getOrCreateEntry(req LightingSceneRequest, fp string) (*ledgerEntry, bool) {
	principal := strings.TrimSpace(req.Scope.PrincipalID)
	idemKey := idempotencyIndexKey{
		principalID:    principal,
		idempotencyKey: strings.TrimSpace(req.IdempotencyKey),
	}
	fpKey := fingerprintIndexKey{
		principalID:     principal,
		taskFingerprint: fp,
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if existing, ok := l.byKey[idemKey]; ok {
		if existing.fingerprint != fp {
			return nil, true
		}
		return existing, false
	}
	if existing, ok := l.byFingerprint[fpKey]; ok {
		return existing, false
	}

	entry := &ledgerEntry{
		fingerprint: fp,
		req:         req,
		receipt: DerivationReceipt{
			TaskID:          strings.TrimSpace(req.TaskID),
			ModelID:         strings.TrimSpace(req.Model.ModelID),
			ProviderID:      strings.TrimSpace(req.Model.ProviderID),
			TaskFingerprint: fp,
			QuoteID:         strings.TrimSpace(req.QuoteID),
			ActualCostFen:   0,
			GrantSource:     strings.TrimSpace(req.Credentials.GrantSource),
			Mode:            req.Mode,
			Selected:        false,
			AppID:           strings.TrimSpace(req.Scope.AppID),
			PrincipalID:     principal,
			RequestKey:      strings.TrimSpace(req.Scope.RequestKey),
			IdempotencyKey:  strings.TrimSpace(req.IdempotencyKey),
			BillingState:    fidelitygate.BillingInit,
			CandidateState:  fidelitygate.CandidateNone,
			TaskStatus:      TaskStatusQueued,
			SampleClass:     req.SampleClass,
			SubjectAssetID:  strings.TrimSpace(req.Anchor.SubjectAssetID),
			SubjectSHA256:   strings.TrimSpace(req.Anchor.SubjectSHA256),
		},
	}
	l.byKey[idemKey] = entry
	l.byFingerprint[fpKey] = entry
	return entry, false
}

func (l *LightingLedger) lookupByKey(principalID, idempotencyKey string) (*ledgerEntry, bool) {
	key := idempotencyIndexKey{
		principalID:    strings.TrimSpace(principalID),
		idempotencyKey: strings.TrimSpace(idempotencyKey),
	}
	l.mu.Lock()
	entry, ok := l.byKey[key]
	l.mu.Unlock()
	return entry, ok
}

func (l *LightingLedger) lookupByFingerprint(principalID, taskFingerprint string) (*ledgerEntry, bool) {
	key := fingerprintIndexKey{
		principalID:     strings.TrimSpace(principalID),
		taskFingerprint: strings.TrimSpace(taskFingerprint),
	}
	l.mu.Lock()
	entry, ok := l.byFingerprint[key]
	l.mu.Unlock()
	return entry, ok
}

func (l *LightingLedger) recordPriorFidelityFailure(principalID, subjectAssetID string) {
	key := subjectIndexKey{
		principalID:    strings.TrimSpace(principalID),
		subjectAssetID: strings.TrimSpace(subjectAssetID),
	}
	l.mu.Lock()
	l.priorFidelityFail[key] = true
	l.mu.Unlock()
}

func (l *LightingLedger) hasPriorFidelityFailure(principalID, subjectAssetID string) bool {
	key := subjectIndexKey{
		principalID:    strings.TrimSpace(principalID),
		subjectAssetID: strings.TrimSpace(subjectAssetID),
	}
	l.mu.Lock()
	failed := l.priorFidelityFail[key]
	l.mu.Unlock()
	return failed
}
