// Package lightingscene implements the HUI-1700 product-image lighting and scene
// relighting hardening domain layer:
//
//   - Strict separation between ModeFidelityLocked and ModeCreativeExplicit (never
//     silently switching/downgrading to creative when fidelity fails).
//   - Model eligibility enforcement rejecting text-only, T2V (including h3-fast),
//     and non-image-editing models from masquerading as relighting providers.
//   - Fail-closed credential and authorized quota pre-flight before any provider call.
//   - Fixed subject anchor and three-sample lighting fidelity evaluation integrating
//     directly with server/internal/fidelitygate.
//   - Unified traceable DerivationReceipt and idempotent UNKNOWN/refresh/retry
//     coordination backed by fidelitygate.BillingRecoveryStateMachine (zero second
//     image engine and zero second billing ledger).
package lightingscene

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

// Mode strictly separates subject-locked factual relighting from explicit
// creative exploration.
type Mode string

const (
	// ModeFidelityLocked preserves product structure, logo, text, and geometry.
	// It is the strict factual mode and MUST NEVER silently switch to
	// ModeCreativeExplicit on failure.
	ModeFidelityLocked Mode = "fidelity_locked"

	// ModeCreativeExplicit allows creative scene/lighting divergence ONLY after
	// explicit user confirmation.
	ModeCreativeExplicit Mode = "creative_explicit"
)

// ClaimMode maps Mode directly to fidelitygate's canonical claim mode constants.
func (m Mode) ClaimMode() string {
	switch m {
	case ModeFidelityLocked:
		return fidelitygate.ClaimFactualFidelity
	case ModeCreativeExplicit:
		return fidelitygate.ClaimCreativeExploration
	default:
		return ""
	}
}

// ModelCapability declares what a model endpoint is actually capable of doing.
type ModelCapability string

const (
	CapabilityImageEditRelight ModelCapability = "image_edit_relight"
	CapabilityTextToImage      ModelCapability = "text_to_image"
	CapabilityTextToVideo      ModelCapability = "text_to_video"
	CapabilityTextOnly         ModelCapability = "text_only"
)

// PreflightStatus captures the fail-closed outcome of ValidateLightingSceneRequest.
type PreflightStatus string

const (
	PreflightAllowed PreflightStatus = "ALLOWED"
	PreflightBlocked PreflightStatus = "BLOCKED"
)

// ModelDescriptor describes the target provider model being requested for relighting.
type ModelDescriptor struct {
	ModelID            string            `json:"model_id"`
	ProviderID         string            `json:"provider_id"`
	Capabilities       []ModelCapability `json:"capabilities"`
	SupportsImageInput bool              `json:"supports_image_input"`
	SupportsMaskLock   bool              `json:"supports_mask_lock"`
	SupportsRelighting bool              `json:"supports_relighting"`
}

// CredentialQuotaStatus reports server-verified credential presence and
// authorized quota availability. It never carries raw API keys or secrets.
type CredentialQuotaStatus struct {
	CredentialConfigured bool   `json:"credential_configured"`
	CredentialRef        string `json:"credential_ref,omitempty"`
	QuotaAuthorized      bool   `json:"quota_authorized"`
	AvailableQuotaFen    int64  `json:"available_quota_fen"`
	GrantSource          string `json:"grant_source,omitempty"`
}

// LightingSceneRequest is the validated pre-flight contract required before
// placing a billing hold or invoking an image relighting provider.
type LightingSceneRequest struct {
	Scope                      fidelitygate.BillingScope `json:"scope"`
	IdempotencyKey             string                    `json:"idempotency_key"`
	TaskID                     string                    `json:"task_id,omitempty"`
	QuoteID                    string                    `json:"quote_id"`
	EstimatedCostFen           int64                     `json:"estimated_cost_fen"`
	PricingVersion             string                    `json:"pricing_version"`
	Mode                       Mode                      `json:"mode"`
	CreativeUserConfirmed      bool                      `json:"creative_user_confirmed"`
	PriorFidelityFailed        bool                      `json:"prior_fidelity_failed"`
	ExplicitSwitchAfterFailure bool                      `json:"explicit_switch_after_failure"`
	Model                      ModelDescriptor           `json:"model"`
	Credentials                CredentialQuotaStatus     `json:"credentials"`
	Anchor                     SubjectAnchor             `json:"anchor"`
	Lighting                   LightingSpec              `json:"lighting"`
	SampleClass                LightingSampleClass       `json:"sample_class"`
	SampleOrigin               string                    `json:"sample_origin"`
}

// ValidationResult is returned by ValidateLightingSceneRequest with an explicit
// ALLOWED or BLOCKED status and deterministic task fingerprint.
type ValidationResult struct {
	Status          PreflightStatus `json:"status"`
	BlockCode       string          `json:"block_code,omitempty"`
	TaskFingerprint string          `json:"task_fingerprint"`
}

var (
	// ErrBlocked is the parent sentinel error for fail-closed pre-flight blocks
	// where zero provider calls and zero billing charges are permitted.
	ErrBlocked = errors.New("lightingscene: BLOCKED")

	// ErrBlockedMissingCredentials indicates missing server-side image relighting
	// credentials (0 provider calls, 0 charge).
	ErrBlockedMissingCredentials = fmt.Errorf("%w: missing image relighting credentials", ErrBlocked)

	// ErrBlockedQuotaUnauthorized indicates missing quota authorization or
	// insufficient authorized budget before any provider call.
	ErrBlockedQuotaUnauthorized = fmt.Errorf("%w: authorized quota missing or insufficient", ErrBlocked)

	// ErrModelNotImageEditEligible rejects text-only, T2V (including h3-fast),
	// and non-image-editing models from masquerading as relighting providers.
	ErrModelNotImageEditEligible = errors.New("lightingscene: model is not eligible for image lighting/scene editing")

	// ErrInvalidMode rejects empty or unknown lighting modes.
	ErrInvalidMode = errors.New("lightingscene: mode must be fidelity_locked or creative_explicit")

	// ErrCreativeConfirmationRequired blocks ModeCreativeExplicit without explicit
	// user confirmation.
	ErrCreativeConfirmationRequired = errors.New("lightingscene: creative_explicit mode requires explicit user confirmation")

	// ErrSilentCreativeSwitchForbidden blocks any silent fallback or unconfirmed
	// switch from fidelity_locked to creative_explicit after fidelity failure.
	ErrSilentCreativeSwitchForbidden = errors.New("lightingscene: silent switch from fidelity_locked to creative_explicit is forbidden")

	// ErrInvalidRequest indicates malformed quote, idempotency, anchor, lighting,
	// or sample-origin fields.
	ErrInvalidRequest = errors.New("lightingscene: invalid lighting scene request")
)

// ValidateLightingSceneRequest enforces strict mode separation, model eligibility,
// fail-closed credential & authorized quota checks, and subject/lighting anchor
// invariants before any billing hold or provider call.
func ValidateLightingSceneRequest(req LightingSceneRequest) (ValidationResult, error) {
	fp := TaskFingerprint(req)
	res := ValidationResult{
		Status:          PreflightBlocked,
		TaskFingerprint: fp,
	}

	if err := req.Scope.Validate(); err != nil {
		res.BlockCode = "BLOCKED_INVALID_SCOPE"
		return res, fmt.Errorf("%w: %v", ErrInvalidRequest, err)
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		res.BlockCode = "BLOCKED_MISSING_IDEMPOTENCY_KEY"
		return res, fmt.Errorf("%w: idempotency_key is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.QuoteID) == "" {
		res.BlockCode = "BLOCKED_MISSING_QUOTE"
		return res, fmt.Errorf("%w: quote_id is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.PricingVersion) == "" {
		res.BlockCode = "BLOCKED_MISSING_PRICING_VERSION"
		return res, fmt.Errorf("%w: pricing_version is required", ErrInvalidRequest)
	}
	if req.EstimatedCostFen <= 0 {
		res.BlockCode = "BLOCKED_INVALID_COST"
		return res, fmt.Errorf("%w: estimated_cost_fen must be positive", ErrInvalidRequest)
	}

	// 1. Strict mode separation & anti-silent-switch guard.
	switch req.Mode {
	case ModeFidelityLocked:
		// Strict factual relighting mode.
	case ModeCreativeExplicit:
		if req.PriorFidelityFailed && !req.ExplicitSwitchAfterFailure {
			res.BlockCode = "BLOCKED_SILENT_CREATIVE_SWITCH"
			return res, ErrSilentCreativeSwitchForbidden
		}
		if !req.CreativeUserConfirmed {
			res.BlockCode = "BLOCKED_CREATIVE_UNCONFIRMED"
			return res, ErrCreativeConfirmationRequired
		}
	default:
		res.BlockCode = "BLOCKED_INVALID_MODE"
		return res, ErrInvalidMode
	}

	// 2. Reject non-image-editing / text-only / h3-fast models.
	if err := CheckModelImageEditEligible(req.Model, req.Mode); err != nil {
		res.BlockCode = "BLOCKED_MODEL_INELIGIBLE"
		return res, err
	}

	// 3. Fail-closed credential & authorized quota checks before any provider call.
	if !req.Credentials.CredentialConfigured {
		res.BlockCode = "BLOCKED_MISSING_CREDENTIALS"
		return res, ErrBlockedMissingCredentials
	}
	if !req.Credentials.QuotaAuthorized ||
		strings.TrimSpace(req.Credentials.GrantSource) == "" ||
		req.Credentials.AvailableQuotaFen < req.EstimatedCostFen {
		res.BlockCode = "BLOCKED_QUOTA_UNAUTHORIZED"
		return res, ErrBlockedQuotaUnauthorized
	}

	// 4. Reject fake/synthetic/fixture sample origins fail-closed.
	origin := strings.ToLower(strings.TrimSpace(req.SampleOrigin))
	if origin != "" && origin != fidelitygate.SampleOriginReal {
		res.BlockCode = "BLOCKED_FAKE_SAMPLE_ORIGIN"
		return res, fmt.Errorf("%w: sample_origin %q rejected", ErrInvalidRequest, req.SampleOrigin)
	}

	// 5. Validate fixed subject anchor & lighting specification.
	if err := req.Anchor.Validate(req.Mode); err != nil {
		res.BlockCode = "BLOCKED_INVALID_SUBJECT_ANCHOR"
		return res, err
	}
	if err := req.Lighting.Validate(req.Mode); err != nil {
		res.BlockCode = "BLOCKED_INVALID_LIGHTING_SPEC"
		return res, err
	}

	res.Status = PreflightAllowed
	res.BlockCode = ""
	return res, nil
}

// CheckModelImageEditEligible rejects text-only, T2V (explicitly including
// h3-fast), T2I-only, and non-image-editing models from masquerading as image
// lighting/scene relighting providers.
func CheckModelImageEditEligible(desc ModelDescriptor, mode Mode) error {
	modelID := strings.TrimSpace(desc.ModelID)
	providerID := strings.TrimSpace(desc.ProviderID)
	if modelID == "" || providerID == "" {
		return fmt.Errorf("%w: model_id and provider_id are required", ErrModelNotImageEditEligible)
	}
	if isExplicitlyDisallowedRelightModel(modelID) {
		return fmt.Errorf("%w: model %q is not an image-editing relight model", ErrModelNotImageEditEligible, modelID)
	}

	hasImageEditRelight := false
	for _, capName := range desc.Capabilities {
		switch capName {
		case CapabilityImageEditRelight:
			hasImageEditRelight = true
		case CapabilityTextOnly, CapabilityTextToVideo:
			return fmt.Errorf("%w: model %q declares incompatible capability %q", ErrModelNotImageEditEligible, modelID, capName)
		}
	}
	if !hasImageEditRelight {
		return fmt.Errorf("%w: model %q lacks %q capability", ErrModelNotImageEditEligible, modelID, CapabilityImageEditRelight)
	}
	if !desc.SupportsImageInput || !desc.SupportsRelighting {
		return fmt.Errorf("%w: model %q must support image input and relighting", ErrModelNotImageEditEligible, modelID)
	}
	if mode == ModeFidelityLocked && !desc.SupportsMaskLock {
		return fmt.Errorf("%w: model %q lacks subject mask lock required by %s", ErrModelNotImageEditEligible, modelID, ModeFidelityLocked)
	}
	return nil
}

// GuardModeTransition enforces that a failed ModeFidelityLocked attempt NEVER
// silently switches to ModeCreativeExplicit. Switching to creative requires
// explicit user confirmation and explicit post-failure acknowledgment.
func GuardModeTransition(from Mode, to Mode, fidelityFailed bool, userConfirmed bool, explicitSwitchAfterFailure bool) (Mode, error) {
	if from != ModeFidelityLocked && from != ModeCreativeExplicit {
		return "", ErrInvalidMode
	}
	if to != ModeFidelityLocked && to != ModeCreativeExplicit {
		return "", ErrInvalidMode
	}
	if from == ModeFidelityLocked && to == ModeCreativeExplicit {
		if fidelityFailed && !explicitSwitchAfterFailure {
			return ModeFidelityLocked, ErrSilentCreativeSwitchForbidden
		}
		if !userConfirmed {
			return ModeFidelityLocked, ErrCreativeConfirmationRequired
		}
	}
	return to, nil
}

// TaskFingerprint returns a deterministic SHA-256 hex digest binding scope
// identity, quote, mode, provider/model, subject anchor, and lighting parameters.
// It intentionally excludes transient caller request/idempotency tokens so
// refreshes or retries of the same quoted derivation deduplicate cleanly.
func TaskFingerprint(req LightingSceneRequest) string {
	parts := []string{
		strings.TrimSpace(req.Scope.AppID),
		strings.TrimSpace(req.Scope.PrincipalID),
		strings.TrimSpace(req.QuoteID),
		string(req.Mode),
		strings.TrimSpace(req.Model.ProviderID),
		strings.TrimSpace(req.Model.ModelID),
		strings.TrimSpace(req.Anchor.SubjectAssetID),
		strings.ToLower(strings.TrimSpace(req.Anchor.SubjectSHA256)),
		strings.ToLower(strings.TrimSpace(req.Anchor.StructureHash)),
		strings.TrimSpace(req.Lighting.ScenePreset),
		strings.TrimSpace(req.Lighting.LightDirection),
		strconv.FormatBool(req.Lighting.ContactShadowRequired),
		formatRatio(req.Lighting.EdgeMaterialPreservation),
		string(req.SampleClass),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

func isExplicitlyDisallowedRelightModel(modelID string) bool {
	norm := strings.ToLower(strings.TrimSpace(modelID))
	if norm == "" {
		return true
	}
	blockedTokens := []string{
		"h3-fast",
		"h3_fast",
		"text-only",
		"text_only",
		"t2v",
		"text-to-video",
		"text_to_video",
		"llm-",
		"chat-",
	}
	for _, tok := range blockedTokens {
		if strings.Contains(norm, tok) {
			return true
		}
	}
	return false
}

func formatRatio(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "invalid"
	}
	return strconv.FormatFloat(v, 'f', 6, 64)
}
