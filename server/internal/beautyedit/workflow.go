package beautyedit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

const (
	// CapabilityBeautyPortraitEdit is the platform-billing capability identifier
	// for restricted beauty-portrait editing holds (zero second billing ledger).
	CapabilityBeautyPortraitEdit = "product_image.beauty_portrait_edit"
)

var (
	ErrInvalidConfig          = errors.New("beautyedit: invalid coordinator configuration")
	ErrSessionNotFound        = errors.New("beautyedit: edit session not found")
	ErrSessionAlreadyExists   = errors.New("beautyedit: edit session already exists")
	ErrVersionNotFound        = errors.New("beautyedit: asset version not found")
	ErrPrincipalMismatch      = errors.New("beautyedit: session belongs to a different principal_id")
	ErrMissingCommandID       = errors.New("beautyedit: command_id is required for idempotent submit")
	ErrCommandIDConflict      = errors.New("beautyedit: command_id already bound to a different request payload")
	ErrStaleRevisionConflict  = errors.New("beautyedit: stale revision cannot overwrite a newer adopted or rolled-back version")
	ErrCandidateNotAdoptable  = errors.New("beautyedit: candidate did not pass quality gate and billing settlement; cannot adopt")
	ErrInvalidRedoScope       = errors.New("beautyedit: targeted local redo regions must be a non-empty subset of verified allowed_modify_regions")
	ErrInvalidHandoffConsumer = errors.New("beautyedit: handoff consumer must be shuhai, bot, or matrix_candidate")
	ErrInvalidExportPurpose   = errors.New("beautyedit: unsupported export purpose")
	ErrInvalidRequest         = errors.New("beautyedit: invalid edit request")
)

// ProviderEditRequest is sent to an eligible verified image-editing adapter
// only after portrait authorization, mode/adapter verification, region checks,
// and platform-billing Hold succeed.
type ProviderEditRequest struct {
	CommandID             string
	TaskRef               string
	SessionID             string
	InputAssetVersion     string
	BaseVersionID         string
	Mode                  EditMode
	UsageType             UsageType
	ModelAdapterVersion   string
	AdapterEditCapability AdapterEditCapability
	AllowedModifyRegions  []RegionKind
	PreservedRegions      []RegionKind
	RedoReason            string
	Prompt                string
}

// ProviderEditResponse captures the adapter output version and multi-dimension
// quality evidence evaluated by EvaluateBeautyEditGate.
type ProviderEditResponse struct {
	TaskRef                    string
	OutputAssetVersion         string
	PreviewRef                 string
	OutputSHA256               string
	ProviderCharged            bool
	ActualCostFen              int64
	ClaimsPixelLockedLocalEdit bool
	QualityReport              BeautyEditQualityReport
}

// PortraitEditProvider is the upstream image-edit adapter port.
type PortraitEditProvider interface {
	Edit(ctx context.Context, req ProviderEditRequest) (ProviderEditResponse, error)
}

// AssetVersion is an immutable snapshot of either the original portrait asset
// or a generated restricted-edit candidate.
type AssetVersion struct {
	VersionID               string                    `json:"version_id"`
	AssetID                 string                    `json:"asset_id"`
	ParentVersionID         string                    `json:"parent_version_id,omitempty"`
	IsOriginal              bool                      `json:"is_original"`
	Mode                    EditMode                  `json:"mode,omitempty"`
	UsageType               UsageType                 `json:"usage_type"`
	IsRealServiceProof      bool                      `json:"is_real_service_proof"`
	CreativeDisclosureLabel string                    `json:"creative_disclosure_label,omitempty"`
	AllowedModifyRegions    []RegionKind              `json:"allowed_modify_regions,omitempty"`
	PreservedRegions        []RegionKind              `json:"preserved_regions,omitempty"`
	ModelAdapterVersion     string                    `json:"model_adapter_version,omitempty"`
	AdapterEditCapability   AdapterEditCapability     `json:"adapter_edit_capability,omitempty"`
	TaskRef                 string                    `json:"task_ref,omitempty"`
	PreviewRef              string                    `json:"preview_ref"`
	SHA256                  string                    `json:"sha256"`
	PortraitAuthRef         string                    `json:"portrait_auth_ref,omitempty"`
	CommandID               string                    `json:"command_id,omitempty"`
	RedoReason              string                    `json:"redo_reason,omitempty"`
	QualityReport           *BeautyEditQualityReport  `json:"quality_report,omitempty"`
	GateDecision            *BeautyEditGateDecision   `json:"gate_decision,omitempty"`
	BillingState            fidelitygate.BillingState `json:"billing_state,omitempty"`
	HoldID                  string                    `json:"hold_id,omitempty"`
	SpendID                 string                    `json:"spend_id,omitempty"`
	RefundID                string                    `json:"refund_id,omitempty"`
	ActualCostFen           int64                     `json:"actual_cost_fen,omitempty"`
	Adoptable               bool                      `json:"adoptable"`
	CreatedAt               time.Time                 `json:"created_at"`
}

func (v AssetVersion) clone() AssetVersion {
	cp := v
	cp.AllowedModifyRegions = slices.Clone(v.AllowedModifyRegions)
	cp.PreservedRegions = slices.Clone(v.PreservedRegions)
	if v.QualityReport != nil {
		qr := *v.QualityReport
		qr.ScopeMatch.DeclaredAllowedRegions = slices.Clone(qr.ScopeMatch.DeclaredAllowedRegions)
		qr.ScopeMatch.DeclaredPreservedRegions = slices.Clone(qr.ScopeMatch.DeclaredPreservedRegions)
		qr.ScopeMatch.ObservedModifiedRegions = slices.Clone(qr.ScopeMatch.ObservedModifiedRegions)
		cp.QualityReport = &qr
	}
	if v.GateDecision != nil {
		gd := *v.GateDecision
		gd.FailureReasons = slices.Clone(gd.FailureReasons)
		cp.GateDecision = &gd
	}
	return cp
}

// CreateSessionRequest initializes a versioned restricted beauty-editing session
// anchored to an immutable original authorized portrait asset.
type CreateSessionRequest struct {
	SessionID         string
	PrincipalID       string
	InputAssetID      string
	InputAssetVersion string
	InputPreviewRef   string
	InputSHA256       string
}

// SessionSnapshot is a concurrency-safe read view of an edit session.
type SessionSnapshot struct {
	SessionID            string         `json:"session_id"`
	PrincipalID          string         `json:"principal_id"`
	InputAssetID         string         `json:"input_asset_id"`
	InputAssetVersion    string         `json:"input_asset_version"`
	ActiveVersionID      string         `json:"active_version_id"`
	SelectedResultRef    string         `json:"selected_result_ref"`
	CurrentRevision      int64          `json:"current_revision"`
	RolledBackToOriginal bool           `json:"rolled_back_to_original"`
	Versions             []AssetVersion `json:"versions"`
}

// SubmitEditRequest submits an idempotent restricted beauty-portrait edit.
type SubmitEditRequest struct {
	CommandID               string                                `json:"command_id"`
	SessionID               string                                `json:"session_id"`
	BaseVersionID           string                                `json:"base_version_id,omitempty"`
	Scope                   fidelitygate.BillingScope             `json:"scope"`
	QuoteID                 string                                `json:"quote_id"`
	PricingVersion          string                                `json:"pricing_version"`
	EstimatedCostFen        int64                                 `json:"estimated_cost_fen"`
	GateFailurePolicy       fidelitygate.GateFailureBillingPolicy `json:"gate_failure_policy"`
	Mode                    EditMode                              `json:"mode"`
	UsageType               UsageType                             `json:"usage_type"`
	IsRealServiceProof      bool                                  `json:"is_real_service_proof"`
	CreativeDisclosureLabel string                                `json:"creative_disclosure_label,omitempty"`
	ModelAdapterVersion     string                                `json:"model_adapter_version"`
	AllowedModifyRegions    []RegionKind                          `json:"allowed_modify_regions"`
	PreservedRegions        []RegionKind                          `json:"preserved_regions"`
	PortraitAuthRef         string                                `json:"portrait_auth_ref"`
	PortraitAuth            *PortraitAuthRecord                   `json:"portrait_auth,omitempty"`
	SampleOrigin            string                                `json:"sample_origin"`
	RedoReason              string                                `json:"redo_reason,omitempty"`
	Prompt                  string                                `json:"prompt,omitempty"`
}

// SubmitEditResult returns the candidate version, QA gate decision, billing
// trace, and idempotent replay marker.
type SubmitEditResult struct {
	CommandID        string                    `json:"command_id"`
	SessionID        string                    `json:"session_id"`
	TaskRef          string                    `json:"task_ref"`
	CandidateVersion AssetVersion              `json:"candidate_version"`
	GateDecision     BeautyEditGateDecision    `json:"gate_decision"`
	BillingState     fidelitygate.BillingState `json:"billing_state,omitempty"`
	IdempotentReplay bool                      `json:"idempotent_replay"`
}

func (r SubmitEditResult) clone() SubmitEditResult {
	cp := r
	cp.CandidateVersion = r.CandidateVersion.clone()
	cp.GateDecision.FailureReasons = slices.Clone(r.GateDecision.FailureReasons)
	return cp
}

// LocalRedoRequest requests a targeted local redo restricted strictly within
// verified allowed_modify_regions.
type LocalRedoRequest struct {
	CommandID               string
	SessionID               string
	BaseVersionID           string
	Scope                   fidelitygate.BillingScope
	QuoteID                 string
	PricingVersion          string
	EstimatedCostFen        int64
	GateFailurePolicy       fidelitygate.GateFailureBillingPolicy
	Mode                    EditMode
	UsageType               UsageType
	IsRealServiceProof      bool
	CreativeDisclosureLabel string
	ModelAdapterVersion     string
	TargetRedoRegions       []RegionKind
	PreservedRegions        []RegionKind
	PortraitAuthRef         string
	PortraitAuth            *PortraitAuthRecord
	SampleOrigin            string
	RedoReason              string
	Prompt                  string
}

// SourceCandidateComparison supports side-by-side verification of original
// source vs active version vs candidate version prior to adoption.
type SourceCandidateComparison struct {
	SessionID             string                 `json:"session_id"`
	CurrentRevision       int64                  `json:"current_revision"`
	InputAssetVersion     string                 `json:"input_asset_version"`
	ActiveVersionID       string                 `json:"active_version_id"`
	OutputAssetVersion    string                 `json:"output_asset_version"`
	Source                AssetVersion           `json:"source"`
	Active                AssetVersion           `json:"active"`
	Candidate             AssetVersion           `json:"candidate"`
	Mode                  EditMode               `json:"mode"`
	UsageType             UsageType              `json:"usage_type"`
	IsRealServiceProof    bool                   `json:"is_real_service_proof"`
	AllowedModifyRegions  []RegionKind           `json:"allowed_modify_regions"`
	PreservedRegions      []RegionKind           `json:"preserved_regions"`
	AdapterEditCapability AdapterEditCapability  `json:"adapter_edit_capability"`
	GateDecision          BeautyEditGateDecision `json:"gate_decision"`
	CanAdopt              bool                   `json:"can_adopt"`
	KnownLimitations      []string               `json:"known_limitations,omitempty"`
}

// AdoptVersionRequest promotes a gate-passed candidate to active version using
// optimistic concurrency protection (ExpectedRevision).
type AdoptVersionRequest struct {
	SessionID          string
	PrincipalID        string
	CandidateVersionID string
	ExpectedRevision   int64
}

// RollbackRequest restores the session's active version back to the immutable
// original source asset with optimistic concurrency protection.
type RollbackRequest struct {
	SessionID        string
	PrincipalID      string
	ExpectedRevision int64
	Reason           string
}

// HandoffRequest constructs a candidate-only handoff package for downstream
// shuhai / Bot / matrix consumers.
type HandoffRequest struct {
	SessionID     string
	PrincipalID   string
	VersionID     string // optional; defaults to session's ActiveVersionID
	ExportPurpose ExportPurpose
	Consumer      HandoffConsumer
}

// HandoffPackage carries all standard capability contract fields required by
// HUI-3902 for downstream shuhai / Bot / matrix candidate consumption.
type HandoffPackage struct {
	Consumer                HandoffConsumer        `json:"consumer"`
	ExportPurpose           ExportPurpose          `json:"export_purpose"`
	SessionID               string                 `json:"session_id"`
	Revision                int64                  `json:"revision"`
	VerifiedEditModes       []EditMode             `json:"verified_edit_modes"`
	InputAssetVersion       string                 `json:"input_asset_version"`
	OutputAssetVersion      string                 `json:"output_asset_version"`
	AllowedModifyRegions    []RegionKind           `json:"allowed_modify_regions"`
	PreservedRegions        []RegionKind           `json:"preserved_regions"`
	ModelAdapterVersion     string                 `json:"model_adapter_version"`
	AdapterEditCapability   AdapterEditCapability  `json:"adapter_edit_capability"`
	UsageType               UsageType              `json:"usage_type"`
	IsRealServiceProof      bool                   `json:"is_real_service_proof"`
	CreativeDisclosureLabel string                 `json:"creative_disclosure_label,omitempty"`
	TaskRef                 string                 `json:"task_ref"`
	PreviewRef              string                 `json:"preview_ref"`
	SelectedResultRef       string                 `json:"selected_result_ref"`
	PortraitAuthRef         string                 `json:"portrait_auth_ref"`
	QualityDecision         BeautyEditGateDecision `json:"quality_decision"`
	KnownLimitations        []string               `json:"known_limitations,omitempty"`
	AutoPublishDisabled     bool                   `json:"auto_publish_disabled"`
	CRMReceptionDisabled    bool                   `json:"crm_reception_disabled"`
	SecondBillingDisabled   bool                   `json:"second_billing_disabled"`
}

// AttemptSocialPublish strictly refuses social publishing inside HUI-3902.
func (HandoffPackage) AttemptSocialPublish() error {
	return ErrSocialPublishOutOfScope
}

// AttemptCRMReception strictly refuses CRM/consultation reception inside HUI-3902.
func (HandoffPackage) AttemptCRMReception() error {
	return ErrCRMReceptionOutOfScope
}

// CoordinatorConfig wires the verified adapter registry, provider, portrait
// authorization lookup, optional platform-billing state machine, and QA thresholds.
type CoordinatorConfig struct {
	Registry   *AdapterRegistry
	Provider   PortraitEditProvider
	AuthLookup PortraitAuthLookup
	Billing    *fidelitygate.BillingRecoveryStateMachine
	Thresholds BeautyEditThresholds
	Now        func() time.Time
}

type editSessionState struct {
	sessionID            string
	principalID          string
	inputAssetID         string
	inputAssetVersion    string
	inputSHA256          string
	activeVersionID      string
	selectedResultRef    string
	currentRevision      int64
	rolledBackToOriginal bool
	versions             map[string]AssetVersion
	versionOrder         []string
}

func (s *editSessionState) snapshot() SessionSnapshot {
	versions := make([]AssetVersion, 0, len(s.versionOrder))
	for _, vid := range s.versionOrder {
		if v, ok := s.versions[vid]; ok {
			versions = append(versions, v.clone())
		}
	}
	return SessionSnapshot{
		SessionID:            s.sessionID,
		PrincipalID:          s.principalID,
		InputAssetID:         s.inputAssetID,
		InputAssetVersion:    s.inputAssetVersion,
		ActiveVersionID:      s.activeVersionID,
		SelectedResultRef:    s.selectedResultRef,
		CurrentRevision:      s.currentRevision,
		RolledBackToOriginal: s.rolledBackToOriginal,
		Versions:             versions,
	}
}

type commandKey struct {
	principalID string
	commandID   string
}

type commandRecord struct {
	fingerprint string
	result      SubmitEditResult
}

type inflightCommand struct {
	fingerprint string
	done        chan struct{}
	result      SubmitEditResult
	err         error
}

// Coordinator manages restricted beauty-portrait editing sessions, idempotent
// command execution, optimistic adoption, rollback, and candidate handoff.
type Coordinator struct {
	registry   *AdapterRegistry
	provider   PortraitEditProvider
	authLookup PortraitAuthLookup
	billing    *fidelitygate.BillingRecoveryStateMachine
	thresholds BeautyEditThresholds
	nowFn      func() time.Time

	mu       sync.Mutex
	sessions map[string]*editSessionState
	commands map[commandKey]commandRecord
	inflight map[commandKey]*inflightCommand
}

// NewCoordinator constructs a fail-closed Coordinator.
func NewCoordinator(cfg CoordinatorConfig) (*Coordinator, error) {
	if cfg.Registry == nil || cfg.Provider == nil {
		return nil, fmt.Errorf("%w: registry and provider are required", ErrInvalidConfig)
	}
	thr := cfg.Thresholds
	if strings.TrimSpace(thr.ThresholdVersion) == "" {
		thr = DefaultBeautyEditThresholds()
	}
	if !validThresholds(thr) {
		return nil, fmt.Errorf("%w: invalid beauty edit thresholds", ErrInvalidConfig)
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &Coordinator{
		registry:   cfg.Registry,
		provider:   cfg.Provider,
		authLookup: cfg.AuthLookup,
		billing:    cfg.Billing,
		thresholds: thr,
		nowFn:      nowFn,
		sessions:   make(map[string]*editSessionState),
		commands:   make(map[commandKey]commandRecord),
		inflight:   make(map[commandKey]*inflightCommand),
	}, nil
}

// CreateSession anchors a new restricted-editing session to an immutable original
// portrait asset version at Revision 1.
func (c *Coordinator) CreateSession(req CreateSessionRequest) (SessionSnapshot, error) {
	sessionID := strings.TrimSpace(req.SessionID)
	principalID := strings.TrimSpace(req.PrincipalID)
	assetID := strings.TrimSpace(req.InputAssetID)
	versionID := strings.TrimSpace(req.InputAssetVersion)
	previewRef := strings.TrimSpace(req.InputPreviewRef)
	sha := strings.TrimSpace(req.InputSHA256)

	if sessionID == "" || principalID == "" || assetID == "" || versionID == "" || previewRef == "" || !isValidSHA256Hex(sha) {
		return SessionSnapshot{}, fmt.Errorf("%w: session_id, principal_id, input_asset_id, input_asset_version, input_preview_ref, and valid sha256 are required", ErrInvalidRequest)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.sessions[sessionID]; exists {
		return SessionSnapshot{}, fmt.Errorf("%w: %s", ErrSessionAlreadyExists, sessionID)
	}

	orig := AssetVersion{
		VersionID:          versionID,
		AssetID:            assetID,
		IsOriginal:         true,
		UsageType:          UsageOriginalSource,
		IsRealServiceProof: true,
		PreviewRef:         previewRef,
		SHA256:             sha,
		Adoptable:          true,
		CreatedAt:          c.nowFn().UTC(),
	}
	sess := &editSessionState{
		sessionID:         sessionID,
		principalID:       principalID,
		inputAssetID:      assetID,
		inputAssetVersion: versionID,
		inputSHA256:       sha,
		activeVersionID:   versionID,
		selectedResultRef: previewRef,
		currentRevision:   1,
		versions:          map[string]AssetVersion{versionID: orig},
		versionOrder:      []string{versionID},
	}
	c.sessions[sessionID] = sess
	return sess.snapshot(), nil
}

// GetSession returns a concurrency-safe snapshot of a session.
func (c *Coordinator) GetSession(principalID, sessionID string) (SessionSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sess, err := c.getSessionLocked(principalID, sessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	return sess.snapshot(), nil
}

// SubmitEdit executes a strictly gated, idempotent beauty-portrait restricted edit.
// Duplicate command_id replays return the exact cached result with ZERO extra
// provider calls and ZERO extra billing charges (including under concurrent races).
func (c *Coordinator) SubmitEdit(ctx context.Context, req SubmitEditRequest) (SubmitEditResult, error) {
	cmdID := strings.TrimSpace(req.CommandID)
	if cmdID == "" {
		return SubmitEditResult{}, ErrMissingCommandID
	}
	if err := req.Scope.Validate(); err != nil {
		return SubmitEditResult{}, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}

	// 1. Validate mode, usage separation, region contract, and adapter verification.
	expectedUsage, expectedRealProof, err := CanonicalUsageForMode(req.Mode)
	if err != nil {
		return SubmitEditResult{}, err
	}
	if req.UsageType != expectedUsage || req.IsRealServiceProof != expectedRealProof {
		if req.Mode == ModeCreativeMakeupDemo && (req.IsRealServiceProof || req.UsageType == UsageRealServiceProof) {
			return SubmitEditResult{}, ErrCreativeDemoExportAsRealProofForbidden
		}
		return SubmitEditResult{}, fmt.Errorf("%w: mode %q requires usage_type=%q and is_real_service_proof=%v",
			ErrUsageModeMismatch, req.Mode, expectedUsage, expectedRealProof)
	}
	if req.Mode == ModeCreativeMakeupDemo && strings.TrimSpace(req.CreativeDisclosureLabel) == "" {
		return SubmitEditResult{}, fmt.Errorf("%w: creative_makeup_demo requires creative_disclosure_label", ErrUsageModeMismatch)
	}

	spec, prof, err := c.registry.ResolveVerified(req.ModelAdapterVersion, req.Mode)
	if err != nil {
		return SubmitEditResult{}, err
	}

	allowed := req.AllowedModifyRegions
	if len(allowed) == 0 {
		allowed = slices.Clone(prof.AllowedModifyRegions)
	}
	preserved := req.PreservedRegions
	if len(preserved) == 0 {
		preserved = slices.Clone(prof.PreservedRegions)
	}
	if err := ValidateRegionContract(req.Mode, allowed, preserved); err != nil {
		return SubmitEditResult{}, err
	}
	// Requested regions MUST stay within the adapter's verified profile.
	for _, r := range allowed {
		if !slices.Contains(prof.AllowedModifyRegions, r) {
			return SubmitEditResult{}, fmt.Errorf("%w: region %q is not verified for adapter %s mode %s",
				ErrEditModeNotVerified, r, spec.ModelAdapterVersion, req.Mode)
		}
	}
	for _, reqPres := range prof.PreservedRegions {
		if !slices.Contains(preserved, reqPres) {
			return SubmitEditResult{}, fmt.Errorf("%w: adapter %s mode %s requires preserving %s",
				ErrInvalidRegionDeclaration, spec.ModelAdapterVersion, req.Mode, reqPres)
		}
	}

	// 2. Validate session ownership & base version before external calls.
	c.mu.Lock()
	sess, err := c.getSessionLocked(req.Scope.PrincipalID, req.SessionID)
	if err != nil {
		c.mu.Unlock()
		return SubmitEditResult{}, err
	}
	baseVersionID := strings.TrimSpace(req.BaseVersionID)
	if baseVersionID == "" {
		baseVersionID = sess.inputAssetVersion
	}
	if _, ok := sess.versions[baseVersionID]; !ok {
		c.mu.Unlock()
		return SubmitEditResult{}, fmt.Errorf("%w: base version %q", ErrVersionNotFound, baseVersionID)
	}
	inputAssetID := sess.inputAssetID
	inputAssetVersion := sess.inputAssetVersion
	c.mu.Unlock()

	// 3. Independent human portrait authorization check (NEVER expands product-image permission).
	authRecord, err := c.resolvePortraitAuth(ctx, req)
	if err != nil {
		return SubmitEditResult{}, err
	}
	if err := ValidatePortraitAuthorization(authRecord, req.Scope.PrincipalID, req.Mode, c.nowFn().UTC()); err != nil {
		return SubmitEditResult{}, err
	}

	// 4. Validate billing parameters when billing machine is configured.
	if c.billing != nil {
		if strings.TrimSpace(req.QuoteID) == "" || strings.TrimSpace(req.PricingVersion) == "" || req.EstimatedCostFen <= 0 {
			return SubmitEditResult{}, fmt.Errorf("%w: quote_id, pricing_version, and positive estimated_cost_fen are required when billing is enabled", ErrInvalidRequest)
		}
		if req.GateFailurePolicy != fidelitygate.GateFailurePolicySettleProviderSpend &&
			req.GateFailurePolicy != fidelitygate.GateFailurePolicyRefundPlatformAbsorbed {
			return SubmitEditResult{}, fmt.Errorf("%w: explicit gate_failure_policy is required", ErrInvalidRequest)
		}
	}

	// 5. Idempotency & in-flight deduplication keyed by (principal_id, command_id).
	fp := computeCommandFingerprint(req, baseVersionID, authRecord.PortraitAuthRef, allowed, preserved)
	key := commandKey{
		principalID: strings.TrimSpace(req.Scope.PrincipalID),
		commandID:   cmdID,
	}

	c.mu.Lock()
	if rec, exists := c.commands[key]; exists {
		c.mu.Unlock()
		if rec.fingerprint != fp {
			return SubmitEditResult{}, ErrCommandIDConflict
		}
		replayed := rec.result.clone()
		replayed.IdempotentReplay = true
		return replayed, nil
	}
	if flight, running := c.inflight[key]; running {
		if flight.fingerprint != fp {
			c.mu.Unlock()
			return SubmitEditResult{}, ErrCommandIDConflict
		}
		c.mu.Unlock()
		<-flight.done
		if flight.err != nil {
			return SubmitEditResult{}, flight.err
		}
		replayed := flight.result.clone()
		replayed.IdempotentReplay = true
		return replayed, nil
	}
	flight := &inflightCommand{
		fingerprint: fp,
		done:        make(chan struct{}),
	}
	c.inflight[key] = flight
	c.mu.Unlock()

	var (
		finalRes SubmitEditResult
		finalErr error
	)
	defer func() {
		c.mu.Lock()
		if finalErr == nil {
			c.commands[key] = commandRecord{
				fingerprint: fp,
				result:      finalRes.clone(),
			}
		}
		delete(c.inflight, key)
		flight.result = finalRes.clone()
		flight.err = finalErr
		close(flight.done)
		c.mu.Unlock()
	}()

	// 6. Platform-billing Hold (zero second billing system).
	var billingRec fidelitygate.RecoveryRecord
	if c.billing != nil {
		billingRec, err = c.billing.BeginHold(
			ctx,
			req.Scope,
			CapabilityBeautyPortraitEdit,
			strings.TrimSpace(req.PricingVersion),
			req.EstimatedCostFen,
		)
		if err != nil {
			finalErr = err
			return SubmitEditResult{}, err
		}
	}

	// 7. Execute provider edit.
	taskRef := "beauty-task:" + cmdID
	provResp, provErr := c.provider.Edit(ctx, ProviderEditRequest{
		CommandID:             cmdID,
		TaskRef:               taskRef,
		SessionID:             strings.TrimSpace(req.SessionID),
		InputAssetVersion:     inputAssetVersion,
		BaseVersionID:         baseVersionID,
		Mode:                  req.Mode,
		UsageType:             req.UsageType,
		ModelAdapterVersion:   spec.ModelAdapterVersion,
		AdapterEditCapability: spec.AdapterEditCapability,
		AllowedModifyRegions:  slices.Clone(allowed),
		PreservedRegions:      slices.Clone(preserved),
		RedoReason:            strings.TrimSpace(req.RedoReason),
		Prompt:                req.Prompt,
	})
	if provErr != nil {
		if c.billing != nil {
			_, _ = c.billing.HandleProviderFailureBeforeDelivery(ctx, req.Scope, "beauty_edit_provider_failed")
		}
		finalErr = provErr
		return SubmitEditResult{}, provErr
	}

	if strings.TrimSpace(provResp.TaskRef) != "" {
		taskRef = strings.TrimSpace(provResp.TaskRef)
	}
	outputVersionID := strings.TrimSpace(provResp.OutputAssetVersion)
	if outputVersionID == "" {
		outputVersionID = "ver-" + cmdID
	}

	// 8. Run fail-closed multi-dimension QA gate (including full_redraw post-edit check).
	decision := EvaluateBeautyEditGate(BeautyEditGateRequest{
		AppID:                      req.Scope.AppID,
		PrincipalID:                req.Scope.PrincipalID,
		Mode:                       req.Mode,
		AdapterEditCapability:      spec.AdapterEditCapability,
		ClaimsPixelLockedLocalEdit: provResp.ClaimsPixelLockedLocalEdit || spec.ClaimsPixelLockedLocalEdit,
		SampleOrigin:               req.SampleOrigin,
		Report:                     provResp.QualityReport,
	}, c.thresholds)

	// 9. Settle platform-billing hold according to gate outcome.
	billingSettledForAdopt := true
	if c.billing != nil {
		fgDecision := toFidelityGateDecision(decision)
		billingRec, err = c.billing.SettleAfterQualityGate(
			ctx,
			req.Scope,
			fgDecision,
			provResp.ProviderCharged,
			req.GateFailurePolicy,
		)
		if err != nil {
			finalErr = err
			return SubmitEditResult{}, err
		}
		billingSettledForAdopt = billingRec.State == fidelitygate.BillingSpent && billingRec.CandidateState == fidelitygate.CandidateAdopted
	}

	qrCopy := provResp.QualityReport
	gdCopy := decision
	gdCopy.FailureReasons = slices.Clone(decision.FailureReasons)

	canAdopt := decision.CanAdopt() && billingSettledForAdopt
	effectiveRealProof := req.Mode == ModeFidelityBackgroundEdit && canAdopt && decision.CanClaimRealService
	effectiveUsage := req.UsageType
	if req.Mode == ModeCreativeMakeupDemo {
		effectiveUsage = UsageCreativeDemo
		effectiveRealProof = false
	}

	candVer := AssetVersion{
		VersionID:               outputVersionID,
		AssetID:                 inputAssetID,
		ParentVersionID:         baseVersionID,
		IsOriginal:              false,
		Mode:                    req.Mode,
		UsageType:               effectiveUsage,
		IsRealServiceProof:      effectiveRealProof,
		CreativeDisclosureLabel: strings.TrimSpace(req.CreativeDisclosureLabel),
		AllowedModifyRegions:    slices.Clone(allowed),
		PreservedRegions:        slices.Clone(preserved),
		ModelAdapterVersion:     spec.ModelAdapterVersion,
		AdapterEditCapability:   spec.AdapterEditCapability,
		TaskRef:                 taskRef,
		PreviewRef:              strings.TrimSpace(provResp.PreviewRef),
		SHA256:                  strings.TrimSpace(provResp.OutputSHA256),
		PortraitAuthRef:         strings.TrimSpace(authRecord.PortraitAuthRef),
		CommandID:               cmdID,
		RedoReason:              strings.TrimSpace(req.RedoReason),
		QualityReport:           &qrCopy,
		GateDecision:            &gdCopy,
		BillingState:            billingRec.State,
		HoldID:                  billingRec.HoldID,
		SpendID:                 billingRec.SpendID,
		RefundID:                billingRec.RefundID,
		ActualCostFen:           provResp.ActualCostFen,
		Adoptable:               canAdopt,
		CreatedAt:               c.nowFn().UTC(),
	}

	// 10. Persist immutable candidate version on session (does NOT overwrite active version!).
	c.mu.Lock()
	sess, err = c.getSessionLocked(req.Scope.PrincipalID, req.SessionID)
	if err != nil {
		c.mu.Unlock()
		finalErr = err
		return SubmitEditResult{}, err
	}
	sess.versions[candVer.VersionID] = candVer.clone()
	if !slices.Contains(sess.versionOrder, candVer.VersionID) {
		sess.versionOrder = append(sess.versionOrder, candVer.VersionID)
	}
	c.mu.Unlock()

	finalRes = SubmitEditResult{
		CommandID:        cmdID,
		SessionID:        strings.TrimSpace(req.SessionID),
		TaskRef:          taskRef,
		CandidateVersion: candVer.clone(),
		GateDecision:     gdCopy,
		BillingState:     billingRec.State,
		IdempotentReplay: false,
	}
	return finalRes.clone(), nil
}

// SubmitLocalRedo runs a targeted local redo restricted strictly to a subset of
// the mode's verified allowed_modify_regions, bound to an explicit base version.
func (c *Coordinator) SubmitLocalRedo(ctx context.Context, req LocalRedoRequest) (SubmitEditResult, error) {
	if strings.TrimSpace(req.BaseVersionID) == "" {
		return SubmitEditResult{}, fmt.Errorf("%w: base_version_id is required for local redo", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.RedoReason) == "" {
		return SubmitEditResult{}, fmt.Errorf("%w: redo_reason is required for local redo", ErrInvalidRedoScope)
	}
	if len(req.TargetRedoRegions) == 0 {
		return SubmitEditResult{}, ErrInvalidRedoScope
	}
	_, prof, err := c.registry.ResolveVerified(req.ModelAdapterVersion, req.Mode)
	if err != nil {
		return SubmitEditResult{}, err
	}
	for _, r := range req.TargetRedoRegions {
		if !slices.Contains(prof.AllowedModifyRegions, r) || slices.Contains(prof.PreservedRegions, r) {
			return SubmitEditResult{}, fmt.Errorf("%w: region %q is outside verified allowed_modify_regions for %s",
				ErrInvalidRedoScope, r, req.Mode)
		}
	}

	preserved := req.PreservedRegions
	if len(preserved) == 0 {
		preserved = slices.Clone(prof.PreservedRegions)
	}

	return c.SubmitEdit(ctx, SubmitEditRequest{
		CommandID:               req.CommandID,
		SessionID:               req.SessionID,
		BaseVersionID:           req.BaseVersionID,
		Scope:                   req.Scope,
		QuoteID:                 req.QuoteID,
		PricingVersion:          req.PricingVersion,
		EstimatedCostFen:        req.EstimatedCostFen,
		GateFailurePolicy:       req.GateFailurePolicy,
		Mode:                    req.Mode,
		UsageType:               req.UsageType,
		IsRealServiceProof:      req.IsRealServiceProof,
		CreativeDisclosureLabel: req.CreativeDisclosureLabel,
		ModelAdapterVersion:     req.ModelAdapterVersion,
		AllowedModifyRegions:    slices.Clone(req.TargetRedoRegions),
		PreservedRegions:        preserved,
		PortraitAuthRef:         req.PortraitAuthRef,
		PortraitAuth:            req.PortraitAuth,
		SampleOrigin:            req.SampleOrigin,
		RedoReason:              req.RedoReason,
		Prompt:                  req.Prompt,
	})
}

// CompareVersions returns side-by-side comparison metadata between the original
// source, currently active version, and a generated candidate version.
func (c *Coordinator) CompareVersions(principalID, sessionID, candidateVersionID string) (SourceCandidateComparison, error) {
	c.mu.Lock()
	sess, err := c.getSessionLocked(principalID, sessionID)
	if err != nil {
		c.mu.Unlock()
		return SourceCandidateComparison{}, err
	}
	src := sess.versions[sess.inputAssetVersion].clone()
	active := sess.versions[sess.activeVersionID].clone()
	cand, ok := sess.versions[strings.TrimSpace(candidateVersionID)]
	if !ok {
		c.mu.Unlock()
		return SourceCandidateComparison{}, fmt.Errorf("%w: %s", ErrVersionNotFound, candidateVersionID)
	}
	cand = cand.clone()
	rev := sess.currentRevision
	c.mu.Unlock()

	var limitations []string
	if cand.ModelAdapterVersion != "" && cand.Mode != "" {
		if contract, err := c.registry.CapabilityContractFor(cand.ModelAdapterVersion, cand.Mode); err == nil {
			limitations = contract.KnownLimitations
		}
	}
	var gd BeautyEditGateDecision
	if cand.GateDecision != nil {
		gd = *cand.GateDecision
	}

	return SourceCandidateComparison{
		SessionID:             sess.sessionID,
		CurrentRevision:       rev,
		InputAssetVersion:     src.VersionID,
		ActiveVersionID:       active.VersionID,
		OutputAssetVersion:    cand.VersionID,
		Source:                src,
		Active:                active,
		Candidate:             cand,
		Mode:                  cand.Mode,
		UsageType:             cand.UsageType,
		IsRealServiceProof:    cand.IsRealServiceProof,
		AllowedModifyRegions:  slices.Clone(cand.AllowedModifyRegions),
		PreservedRegions:      slices.Clone(cand.PreservedRegions),
		AdapterEditCapability: cand.AdapterEditCapability,
		GateDecision:          gd,
		CanAdopt:              cand.Adoptable,
		KnownLimitations:      limitations,
	}, nil
}

// AdoptVersion explicitly adopts a gate-passed candidate version using optimistic
// revision locking so stale revisions NEVER overwrite newer adopted versions.
func (c *Coordinator) AdoptVersion(req AdoptVersionRequest) (SessionSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sess, err := c.getSessionLocked(req.PrincipalID, req.SessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if req.ExpectedRevision != sess.currentRevision {
		return SessionSnapshot{}, fmt.Errorf("%w: expected revision %d, current revision is %d",
			ErrStaleRevisionConflict, req.ExpectedRevision, sess.currentRevision)
	}
	cand, ok := sess.versions[strings.TrimSpace(req.CandidateVersionID)]
	if !ok {
		return SessionSnapshot{}, fmt.Errorf("%w: %s", ErrVersionNotFound, req.CandidateVersionID)
	}
	if !cand.IsOriginal {
		if !cand.Adoptable || cand.GateDecision == nil || !cand.GateDecision.CanAdopt() {
			return SessionSnapshot{}, ErrCandidateNotAdoptable
		}
	}

	sess.activeVersionID = cand.VersionID
	sess.selectedResultRef = cand.PreviewRef
	sess.currentRevision++
	sess.rolledBackToOriginal = cand.IsOriginal
	return sess.snapshot(), nil
}

// RollbackToOriginal restores the active version to the immutable original
// portrait source while monotonically bumping CurrentRevision so stale clients
// cannot silently re-overwrite the rollback.
func (c *Coordinator) RollbackToOriginal(req RollbackRequest) (SessionSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	sess, err := c.getSessionLocked(req.PrincipalID, req.SessionID)
	if err != nil {
		return SessionSnapshot{}, err
	}
	if req.ExpectedRevision != sess.currentRevision {
		return SessionSnapshot{}, fmt.Errorf("%w: expected revision %d, current revision is %d",
			ErrStaleRevisionConflict, req.ExpectedRevision, sess.currentRevision)
	}
	orig := sess.versions[sess.inputAssetVersion]
	sess.activeVersionID = orig.VersionID
	sess.selectedResultRef = orig.PreviewRef
	sess.currentRevision++
	sess.rolledBackToOriginal = true
	return sess.snapshot(), nil
}

// ValidateExportPurpose strictly prevents creative_makeup_demo assets from ever
// being exported or handed off as real_service_proof.
func ValidateExportPurpose(v AssetVersion, purpose ExportPurpose) error {
	switch purpose {
	case ExportPurposeRealServiceProof:
		if v.Mode == ModeCreativeMakeupDemo || v.UsageType == UsageCreativeDemo || !v.IsRealServiceProof {
			return ErrCreativeDemoExportAsRealProofForbidden
		}
		if !v.IsOriginal {
			if v.GateDecision == nil || !v.GateDecision.CanAdopt() || !v.GateDecision.CanClaimRealService {
				return fmt.Errorf("%w: edited version failed fidelity real-service verification", ErrCandidateNotAdoptable)
			}
		}
		return nil
	case ExportPurposeCreativeDemo:
		if !v.IsOriginal && (v.GateDecision == nil || !v.GateDecision.CanAdopt()) {
			return ErrCandidateNotAdoptable
		}
		return nil
	case ExportPurposeCandidateReview:
		return nil
	default:
		return fmt.Errorf("%w: %q", ErrInvalidExportPurpose, purpose)
	}
}

// BuildHandoffPackage produces a downstream candidate-only handoff package for
// shuhai / Bot / matrix consumers with full capability contract metadata and
// zero social publishing or CRM reception.
func (c *Coordinator) BuildHandoffPackage(req HandoffRequest) (HandoffPackage, error) {
	switch req.Consumer {
	case ConsumerShuhai, ConsumerBot, ConsumerMatrixCandidate:
		// Allowed candidate-only downstream consumers.
	default:
		return HandoffPackage{}, fmt.Errorf("%w: %q", ErrInvalidHandoffConsumer, req.Consumer)
	}

	c.mu.Lock()
	sess, err := c.getSessionLocked(req.PrincipalID, req.SessionID)
	if err != nil {
		c.mu.Unlock()
		return HandoffPackage{}, err
	}
	versionID := strings.TrimSpace(req.VersionID)
	if versionID == "" {
		versionID = sess.activeVersionID
	}
	ver, ok := sess.versions[versionID]
	if !ok {
		c.mu.Unlock()
		return HandoffPackage{}, fmt.Errorf("%w: %s", ErrVersionNotFound, versionID)
	}
	ver = ver.clone()
	inputAssetVersion := sess.inputAssetVersion
	selectedRef := sess.selectedResultRef
	if versionID != sess.activeVersionID {
		selectedRef = ver.PreviewRef
	}
	revision := sess.currentRevision
	c.mu.Unlock()

	if err := ValidateExportPurpose(ver, req.ExportPurpose); err != nil {
		return HandoffPackage{}, err
	}

	var (
		verifiedModes []EditMode
		limitations   []string
	)
	if ver.ModelAdapterVersion != "" && ver.Mode != "" {
		if contract, err := c.registry.CapabilityContractFor(ver.ModelAdapterVersion, ver.Mode); err == nil {
			verifiedModes = contract.VerifiedEditModes
			limitations = contract.KnownLimitations
		}
	}
	var gd BeautyEditGateDecision
	if ver.GateDecision != nil {
		gd = *ver.GateDecision
	}

	return HandoffPackage{
		Consumer:                req.Consumer,
		ExportPurpose:           req.ExportPurpose,
		SessionID:               sess.sessionID,
		Revision:                revision,
		VerifiedEditModes:       verifiedModes,
		InputAssetVersion:       inputAssetVersion,
		OutputAssetVersion:      ver.VersionID,
		AllowedModifyRegions:    slices.Clone(ver.AllowedModifyRegions),
		PreservedRegions:        slices.Clone(ver.PreservedRegions),
		ModelAdapterVersion:     ver.ModelAdapterVersion,
		AdapterEditCapability:   ver.AdapterEditCapability,
		UsageType:               ver.UsageType,
		IsRealServiceProof:      ver.IsRealServiceProof,
		CreativeDisclosureLabel: ver.CreativeDisclosureLabel,
		TaskRef:                 ver.TaskRef,
		PreviewRef:              ver.PreviewRef,
		SelectedResultRef:       selectedRef,
		PortraitAuthRef:         ver.PortraitAuthRef,
		QualityDecision:         gd,
		KnownLimitations:        limitations,
		AutoPublishDisabled:     true,
		CRMReceptionDisabled:    true,
		SecondBillingDisabled:   true,
	}, nil
}

func (c *Coordinator) resolvePortraitAuth(ctx context.Context, req SubmitEditRequest) (PortraitAuthRecord, error) {
	if req.PortraitAuth != nil && strings.TrimSpace(req.PortraitAuth.EmbeddedCredential) != "" {
		return PortraitAuthRecord{}, ErrLongLivedCredentialCopyForbidden
	}
	if looksLikeSecretRef(req.PortraitAuthRef) {
		return PortraitAuthRecord{}, ErrLongLivedCredentialCopyForbidden
	}
	if c.authLookup != nil {
		ref := strings.TrimSpace(req.PortraitAuthRef)
		if ref == "" && req.PortraitAuth != nil {
			ref = strings.TrimSpace(req.PortraitAuth.PortraitAuthRef)
		}
		if ref == "" {
			return PortraitAuthRecord{}, fmt.Errorf("%w: portrait_auth_ref is required", ErrPortraitAuthRequired)
		}
		rec, err := c.authLookup.LookupPortraitAuth(ctx, req.Scope.PrincipalID, ref)
		if err != nil {
			return PortraitAuthRecord{}, fmt.Errorf("%w: %v", ErrPortraitAuthRequired, err)
		}
		return rec, nil
	}
	if req.PortraitAuth == nil {
		return PortraitAuthRecord{}, fmt.Errorf("%w: portrait authorization reference not provided", ErrPortraitAuthRequired)
	}
	return *req.PortraitAuth, nil
}

func (c *Coordinator) getSessionLocked(principalID, sessionID string) (*editSessionState, error) {
	sess, ok := c.sessions[strings.TrimSpace(sessionID)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	if strings.TrimSpace(principalID) == "" || sess.principalID != strings.TrimSpace(principalID) {
		return nil, ErrPrincipalMismatch
	}
	return sess, nil
}

func computeCommandFingerprint(req SubmitEditRequest, baseVersionID, resolvedAuthRef string, allowed, preserved []RegionKind) string {
	allowedSorted := slices.Clone(allowed)
	slices.Sort(allowedSorted)
	preservedSorted := slices.Clone(preserved)
	slices.Sort(preservedSorted)

	var b strings.Builder
	b.WriteString(strings.TrimSpace(req.SessionID))
	b.WriteByte('|')
	b.WriteString(strings.TrimSpace(baseVersionID))
	b.WriteByte('|')
	b.WriteString(string(req.Mode))
	b.WriteByte('|')
	b.WriteString(string(req.UsageType))
	b.WriteByte('|')
	b.WriteString(strconv.FormatBool(req.IsRealServiceProof))
	b.WriteByte('|')
	b.WriteString(strings.TrimSpace(req.ModelAdapterVersion))
	b.WriteByte('|')
	b.WriteString(strings.TrimSpace(resolvedAuthRef))
	b.WriteByte('|')
	for _, r := range allowedSorted {
		b.WriteString(string(r))
		b.WriteByte(',')
	}
	b.WriteByte('|')
	for _, r := range preservedSorted {
		b.WriteString(string(r))
		b.WriteByte(',')
	}
	b.WriteByte('|')
	b.WriteString(strings.TrimSpace(req.RedoReason))
	b.WriteByte('|')
	b.WriteString(strings.TrimSpace(req.Prompt))

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func toFidelityGateDecision(d BeautyEditGateDecision) fidelitygate.GateDecision {
	action := fidelitygate.RepairActionNone
	if !d.CanAdopt() {
		action = fidelitygate.RepairActionManualReviewOrRegenerate
	}
	return fidelitygate.GateDecision{
		Passed:           d.Passed,
		Verdict:          d.Verdict,
		FailureReasons:   slices.Clone(d.FailureReasons),
		RepairAction:     action,
		EvaluatorVersion: d.EvaluatorVersion,
		ThresholdVersion: d.ThresholdVersion,
	}
}
