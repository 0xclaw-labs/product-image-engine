// Package i2vcontract implements the HUI-1702 product-image-to-video (I2V)
// qualification gate, immutable fixed product reference binding, night/idle queue
// execution policy with pre-dispatch URL renewal & authorization re-verification,
// idempotent recovery & stage-scoped retries, and merchant-candidate-only return
// with video fidelity metadata for downstream consumers (HUI-5196 / HUI-1745 / HUI-2232).
//
// Core invariants:
//   - Only empirically verified image-conditioned I2V models/adapters may execute
//     product I2V. Pure T2V models (including h3-fast), memory fixtures, still
//     images, and placeholder URLs are strictly rejected fail-closed.
//   - Absence of a real I2V adapter fails closed honestly with missing adapter
//     seams and NEVER blocks basic product-image paths.
//   - Single-image input without captured back views always marks
//     BackViewUnproven=true and CannotClaimTrue360=true; true-360 claims are forbidden.
//   - Zero second billing ledger: paid flows delegate Hold/Spend/Refund/Reconcile
//     exclusively to fidelitygate.BillingRecoveryStateMachine; expired free quota
//     NEVER auto-charges and free tier NEVER accesses internal R&D GPU cards.
//   - Artifacts return strictly to the merchant candidate library (AutoPublished=false,
//     RewardTriggered=false).
package i2vcontract

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/bianjiefilm/product-image-engine/server/internal/i2v"
)

const (
	// ModeImageToVideo is the only permitted generation mode for product I2V.
	ModeImageToVideo = i2v.ModeImageToVideo
	// ModeTextToVideo is strictly forbidden as a substitute for product I2V.
	ModeTextToVideo = i2v.ModeTextToVideo

	// DefaultAdapterSeamOwner identifies the owner role for the missing real I2V adapter seam.
	DefaultAdapterSeamOwner = "Catalog/Adapter owner (HUI-2295 / GoBoost XModel)"

	// DefaultNoAdapterNotice is the honest user-facing notice when no real I2V adapter is registered.
	DefaultNoAdapterNotice = "当前无已实证的真实图生视频（I2V）适配器，商品图视频能力保持诚实阻断（fail-closed）；严禁使用纯 T2V（如 h3-fast）、静图或占位地址冒充，基础商品图路径不受阻塞。"
)

// QualificationStatus is the structured pre-flight verdict for an I2V model/request.
type QualificationStatus string

const (
	QualificationAllowed QualificationStatus = "QUALIFIED"
	QualificationBlocked QualificationStatus = "BLOCKED"
	QualificationUnknown QualificationStatus = "UNKNOWN"
)

// Machine-readable fail-closed block codes.
const (
	BlockNoRealI2VAdapter              = "BLOCKED_NO_REAL_I2V_ADAPTER"
	BlockT2VMasquerade                 = "BLOCKED_T2V_MASQUERADE_FORBIDDEN"
	BlockFixtureOrPlaceholder          = "BLOCKED_FIXTURE_OR_PLACEHOLDER_FORBIDDEN"
	BlockUnverifiedImageBinding        = "BLOCKED_UNVERIFIED_IMAGE_CONDITION_BINDING"
	BlockUnsupportedVideoParams        = "BLOCKED_UNSUPPORTED_VIDEO_PARAMS"
	BlockUnprovenTrue360Claim          = "BLOCKED_UNPROVEN_TRUE_360_CLAIM"
	BlockMissingImageCondition         = "BLOCKED_MISSING_IMAGE_CONDITION"
	BlockInvalidExecutionPreference    = "BLOCKED_INVALID_EXECUTION_PREFERENCE"
	BlockArbitraryPriorityForbidden    = "BLOCKED_ARBITRARY_PRIORITY_FORBIDDEN"
	BlockFreeQuotaExpiredNoAutoCharge  = "BLOCKED_FREE_QUOTA_EXPIRED_NO_AUTO_CHARGE"
	BlockFreeTierInternalCardForbidden = "BLOCKED_FREE_TIER_INTERNAL_RD_CARD_FORBIDDEN"
	BlockBackendCostWalletMisuse       = "BLOCKED_BACKEND_COST_WALLET_AS_CUSTOMER_BALANCE"
	BlockInternalTokenLeak             = "BLOCKED_INTERNAL_TOKEN_EXPOSURE_FORBIDDEN"
	BlockAuthorizationRevoked          = "BLOCKED_AUTHORIZATION_REVOKED"
	BlockProductRefTampered            = "BLOCKED_PRODUCT_REF_TAMPERED"
	BlockSignedURLExpired              = "BLOCKED_SIGNED_URL_EXPIRED"
)

// Quality limitation tags preserved in results and downstream envelopes.
const (
	LimitationSingleImageBackViewUnproven = "single_image_back_view_unproven"
	LimitationCannotClaimTrue360          = "cannot_claim_true_360_reconstruction"
	LimitationNoRealI2VAdapter            = "no_real_i2v_adapter_configured"
)

var (
	// ErrNoRealI2VAdapter indicates no real image-conditioned video adapter is available.
	ErrNoRealI2VAdapter = errors.New("i2vcontract: no empirically verified real I2V adapter configured")
	// ErrT2VMasqueradeForbidden rejects pure text-to-video models (e.g. h3-fast) posing as product I2V.
	ErrT2VMasqueradeForbidden = errors.New("i2vcontract: text-to-video model cannot masquerade as product image-to-video")
	// ErrFixtureOrPlaceholderForbidden rejects fixtures, still images, and placeholder endpoints/URLs.
	ErrFixtureOrPlaceholderForbidden = errors.New("i2vcontract: fixture, still image, or placeholder URL cannot substitute for real I2V")
	// ErrUnverifiedImageBinding rejects models lacking empirical image-conditioning verification.
	ErrUnverifiedImageBinding = errors.New("i2vcontract: model lacks empirical image-condition binding verification")
	// ErrUnsupportedVideoParams rejects duration or aspect ratio outside the verified adapter contract.
	ErrUnsupportedVideoParams = errors.New("i2vcontract: duration or aspect_ratio is outside verified I2V model support")
	// ErrTrue360ClaimForbidden blocks claiming true 360 reconstruction or proven back view from single-image input.
	ErrTrue360ClaimForbidden = errors.New("i2vcontract: single-image input without captured back view cannot claim true 360 reconstruction")
)

// PoolClass describes the server-side compute pool class. Internal values are
// evaluated strictly server-side and never exposed to user-facing projections.
type PoolClass string

const (
	PoolPublicShared    PoolClass = "public_shared"
	PoolNightIdleShared PoolClass = "night_idle_shared"
	PoolDedicatedPaid   PoolClass = "dedicated_paid"
	PoolInternalRDCard  PoolClass = "internal_rd_card"
)

// ModelQualification captures server-verified facts about a candidate I2V model/adapter.
type ModelQualification struct {
	ModelID                       string    `json:"model_id"`
	ProviderID                    string    `json:"provider_id"`
	AdapterID                     string    `json:"adapter_id"`
	AdapterRegistered             bool      `json:"adapter_registered"`
	Mode                          string    `json:"mode"`
	ImageConditioned              bool      `json:"image_conditioned"`
	EmpiricalImageBindingVerified bool      `json:"empirical_image_binding_verified"`
	VerifiedEvidenceRef           string    `json:"verified_evidence_ref,omitempty"`
	IsFixture                     bool      `json:"is_fixture"`
	IsPlaceholder                 bool      `json:"is_placeholder"`
	EndpointURL                   string    `json:"-"` // never leak endpoint details to end users
	SupportedDurations            []int     `json:"supported_durations,omitempty"`
	SupportedAspectRatios         []string  `json:"supported_aspect_ratios,omitempty"`
	TargetPoolClass               PoolClass `json:"-"` // never leak pool classification to end users
}

// ViewProvenance records what photographic angles were actually supplied for the product.
type ViewProvenance struct {
	CapturedViewCount        int      `json:"captured_view_count"`
	CapturedAngles           []string `json:"captured_angles,omitempty"`
	HasCapturedBackView      bool     `json:"has_captured_back_view"`
	ClaimTrue360             bool     `json:"claim_true_360"`
	ClaimBackViewProven      bool     `json:"claim_back_view_proven"`
	CameraMove               string   `json:"camera_move,omitempty"`
}

// ViewClaimAssessment is the explicit honesty verdict on unseen product geometry.
type ViewClaimAssessment struct {
	CapturedViewCount  int      `json:"captured_view_count"`
	BackViewUnproven   bool     `json:"back_view_unproven"`
	CannotClaimTrue360 bool     `json:"cannot_claim_true_360"`
	Limitations        []string `json:"limitations,omitempty"`
}

// QualificationDecision is the machine-readable fail-closed decision returned by
// EvaluateModelQualification and exposed safely to UI/downstream callers.
type QualificationDecision struct {
	Status                QualificationStatus `json:"status"`
	Eligible              bool                `json:"eligible"`
	BlockCode             string              `json:"block_code,omitempty"`
	Reason                string              `json:"reason,omitempty"`
	ModelID               string              `json:"model_id,omitempty"`
	ProviderID            string              `json:"provider_id,omitempty"`
	AdapterID             string              `json:"adapter_id,omitempty"`
	ImageConditioned      bool                `json:"image_conditioned"`
	RealVideoCapable      bool                `json:"real_video_capable"`
	ProviderCalls         int                 `json:"provider_calls"`
	BasicImagePathBlocked bool                `json:"basic_image_path_blocked"`
	MissingSeams          []string            `json:"missing_seams,omitempty"`
	AdapterSeamOwner      string              `json:"adapter_seam_owner,omitempty"`
	ViewAssessment        ViewClaimAssessment `json:"view_assessment"`
	QualityLimitations    []string            `json:"quality_limitations,omitempty"`
	Notice                string              `json:"notice"`
}

// EvaluateViewProvenance computes explicit BackViewUnproven and CannotClaimTrue360
// flags from the supplied photographic views and blocks dishonest true-360 claims.
func EvaluateViewProvenance(view ViewProvenance) (ViewClaimAssessment, error) {
	capturedCount := view.CapturedViewCount
	if capturedCount <= 0 {
		capturedCount = len(view.CapturedAngles)
	}
	if capturedCount <= 0 {
		capturedCount = 1
	}

	hasBack := view.HasCapturedBackView
	if !hasBack {
		for _, angle := range view.CapturedAngles {
			norm := strings.ToLower(strings.TrimSpace(angle))
			if norm == "back" || norm == "rear" || norm == "back_view" || norm == "背面" {
				hasBack = true
				break
			}
		}
	}

	backUnproven := capturedCount < 2 || !hasBack
	cannotClaim360 := backUnproven

	assessment := ViewClaimAssessment{
		CapturedViewCount:  capturedCount,
		BackViewUnproven:   backUnproven,
		CannotClaimTrue360: cannotClaim360,
	}
	if backUnproven {
		assessment.Limitations = []string{
			LimitationSingleImageBackViewUnproven,
			LimitationCannotClaimTrue360,
		}
	}

	if cannotClaim360 && (view.ClaimTrue360 || view.ClaimBackViewProven) {
		return assessment, ErrTrue360ClaimForbidden
	}
	return assessment, nil
}

// DefaultNoAdapterDecision returns the honest fail-closed decision when no real
// image-conditioned video adapter is registered in the deployment.
func DefaultNoAdapterDecision() QualificationDecision {
	viewAssessment, _ := EvaluateViewProvenance(ViewProvenance{CapturedViewCount: 1})
	return QualificationDecision{
		Status:                QualificationBlocked,
		Eligible:              false,
		BlockCode:             BlockNoRealI2VAdapter,
		Reason:                ErrNoRealI2VAdapter.Error(),
		ImageConditioned:      false,
		RealVideoCapable:      false,
		ProviderCalls:         0,
		BasicImagePathBlocked: false,
		MissingSeams:          i2v.MissingSeams(),
		AdapterSeamOwner:      DefaultAdapterSeamOwner,
		ViewAssessment:        viewAssessment,
		QualityLimitations:    appendUniqueStrings(viewAssessment.Limitations, LimitationNoRealI2VAdapter),
		Notice:                DefaultNoAdapterNotice,
	}
}

// EvaluateModelQualification enforces the HUI-1702 real-I2V qualification gate.
// It fails closed whenever:
//  1. A single-image input attempts to claim true 360° reconstruction or proven back view.
//  2. A pure T2V model (explicitly including h3-fast) tries to pose as product I2V.
//  3. A fixture, still-image stub, or placeholder endpoint/URL tries to substitute for real I2V.
//  4. No real registered adapter or empirical image-conditioning verification exists.
//  5. Requested duration/aspect ratio is outside the verified model capability.
//
// BasicImagePathBlocked is ALWAYS false so missing video capability never blocks
// standard product-image workflows.
func EvaluateModelQualification(
	model ModelQualification,
	durationSeconds int,
	aspectRatio string,
	view ViewProvenance,
) (QualificationDecision, error) {
	viewAssessment, viewErr := EvaluateViewProvenance(view)
	decision := QualificationDecision{
		Status:                QualificationBlocked,
		Eligible:              false,
		ModelID:               strings.TrimSpace(model.ModelID),
		ProviderID:            strings.TrimSpace(model.ProviderID),
		AdapterID:             strings.TrimSpace(model.AdapterID),
		ImageConditioned:      false,
		RealVideoCapable:      false,
		ProviderCalls:         0,
		BasicImagePathBlocked: false,
		ViewAssessment:        viewAssessment,
		QualityLimitations:    append([]string(nil), viewAssessment.Limitations...),
		Notice:                DefaultNoAdapterNotice,
	}

	if viewErr != nil {
		decision.BlockCode = BlockUnprovenTrue360Claim
		decision.Reason = viewErr.Error()
		decision.Notice = "单图未见背面时严禁宣称真实 360° 还原，已拒绝该声明；基础商品图路径不受影响。"
		return decision, viewErr
	}

	// 1. Explicitly reject pure T2V masquerading (including h3-fast).
	normMode := strings.ToLower(strings.TrimSpace(model.Mode))
	if normMode == ModeTextToVideo || normMode == "t2v" || isDisallowedT2VModel(model.ModelID) {
		decision.BlockCode = BlockT2VMasquerade
		decision.Reason = fmt.Sprintf("%s: model=%q mode=%q", ErrT2VMasqueradeForbidden.Error(), decision.ModelID, normMode)
		decision.MissingSeams = i2v.MissingSeams()
		decision.AdapterSeamOwner = DefaultAdapterSeamOwner
		decision.Notice = "纯文生视频（T2V，如 h3-fast）不得冒充商品图 I2V，已诚实拒绝；基础商品图路径不受阻塞。"
		return decision, ErrT2VMasqueradeForbidden
	}

	// 2. Reject memory fixtures, mock adapters, still-image stubs, or placeholder URLs.
	if model.IsFixture || model.IsPlaceholder ||
		isFixtureOrPlaceholderIdentifier(model.ModelID) ||
		isFixtureOrPlaceholderIdentifier(model.ProviderID) ||
		isFixtureOrPlaceholderIdentifier(model.AdapterID) ||
		isPlaceholderOrStillURL(model.EndpointURL) {
		decision.BlockCode = BlockFixtureOrPlaceholder
		decision.Reason = ErrFixtureOrPlaceholderForbidden.Error()
		decision.MissingSeams = i2v.MissingSeams()
		decision.AdapterSeamOwner = DefaultAdapterSeamOwner
		decision.Notice = "夹具、静图或占位地址不得冒充真实商品图 I2V；基础商品图路径不受阻塞。"
		return decision, ErrFixtureOrPlaceholderForbidden
	}

	// 3. Require a genuinely registered adapter with explicit model/provider/adapter identity.
	if !model.AdapterRegistered || decision.ModelID == "" || decision.ProviderID == "" || decision.AdapterID == "" {
		decision.BlockCode = BlockNoRealI2VAdapter
		decision.Reason = ErrNoRealI2VAdapter.Error()
		decision.MissingSeams = i2v.MissingSeams()
		decision.AdapterSeamOwner = DefaultAdapterSeamOwner
		decision.QualityLimitations = appendUniqueStrings(decision.QualityLimitations, LimitationNoRealI2VAdapter)
		return decision, ErrNoRealI2VAdapter
	}

	// 4. Require explicit image_to_video mode + empirical image-condition binding proof.
	if normMode != ModeImageToVideo || !model.ImageConditioned ||
		!model.EmpiricalImageBindingVerified || strings.TrimSpace(model.VerifiedEvidenceRef) == "" {
		decision.BlockCode = BlockUnverifiedImageBinding
		decision.Reason = ErrUnverifiedImageBinding.Error()
		decision.MissingSeams = i2v.MissingSeams()
		decision.AdapterSeamOwner = DefaultAdapterSeamOwner
		decision.Notice = "目标模型尚未完成实证图生视频图像条件绑定验收，暂不启用商品图 I2V；基础商品图路径不受阻塞。"
		return decision, ErrUnverifiedImageBinding
	}

	// 5. Check verified duration & aspect ratio bounds when supplied.
	if durationSeconds <= 0 || strings.TrimSpace(aspectRatio) == "" {
		decision.BlockCode = BlockUnsupportedVideoParams
		decision.Reason = fmt.Sprintf("%s: duration=%d aspect=%q", ErrUnsupportedVideoParams.Error(), durationSeconds, aspectRatio)
		return decision, ErrUnsupportedVideoParams
	}
	if len(model.SupportedDurations) > 0 && !containsInt(model.SupportedDurations, durationSeconds) {
		decision.BlockCode = BlockUnsupportedVideoParams
		decision.Reason = fmt.Sprintf("%s: duration %ds not in verified set", ErrUnsupportedVideoParams.Error(), durationSeconds)
		return decision, ErrUnsupportedVideoParams
	}
	if len(model.SupportedAspectRatios) > 0 && !containsTrimmedFold(model.SupportedAspectRatios, aspectRatio) {
		decision.BlockCode = BlockUnsupportedVideoParams
		decision.Reason = fmt.Sprintf("%s: aspect_ratio %q not in verified set", ErrUnsupportedVideoParams.Error(), aspectRatio)
		return decision, ErrUnsupportedVideoParams
	}

	decision.Status = QualificationAllowed
	decision.Eligible = true
	decision.ImageConditioned = true
	decision.RealVideoCapable = true
	decision.Notice = "已通过实证图生视频图像条件资格门禁。"
	if viewAssessment.BackViewUnproven {
		decision.Notice = "已通过实证 I2V 资格门禁；当前为单图输入（未见背面），结果仅限受限预览，严禁宣称真实 360° 还原。"
	}
	return decision, nil
}

func isDisallowedT2VModel(modelID string) bool {
	norm := strings.ToLower(strings.TrimSpace(modelID))
	if norm == "" {
		return false
	}
	if norm == "h3-fast" || strings.HasPrefix(norm, "h3-fast-") || strings.HasPrefix(norm, "h3-fast:") || strings.Contains(norm, "h3-fast") {
		return true
	}
	if norm == "t2v" || norm == "text-to-video" || norm == "text_to_video" || norm == "text-only" || norm == "text_only" {
		return true
	}
	if strings.HasPrefix(norm, "t2v-") || strings.HasSuffix(norm, "-t2v") || strings.Contains(norm, "/t2v") || strings.Contains(norm, "_t2v") {
		return true
	}
	if strings.Contains(norm, "text-to-video") || strings.Contains(norm, "text_to_video") {
		return true
	}
	return false
}

func isFixtureOrPlaceholderIdentifier(id string) bool {
	norm := strings.ToLower(strings.TrimSpace(id))
	if norm == "" {
		return false
	}
	for _, token := range []string{"videofixture", "fixture", "placeholder", "fake-video", "fake_video", "still-video", "still_video", "stub-video", "mock-video"} {
		if strings.Contains(norm, token) {
			return true
		}
	}
	return false
}

func isPlaceholderURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, bad := range []string{"example.invalid", "placeholder", "fake.mp4", "dummy.mp4"} {
		if strings.Contains(lower, bad) {
			return true
		}
	}
	if parsed, err := url.Parse(trimmed); err == nil {
		host := strings.ToLower(parsed.Hostname())
		if host == "example.invalid" || strings.HasSuffix(host, ".invalid") {
			return true
		}
	}
	return false
}

func isPlaceholderOrStillURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if isPlaceholderURL(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "data:image/") {
		return true
	}
	for _, bad := range []string{"still.png", "still.jpg"} {
		if strings.Contains(lower, bad) {
			return true
		}
	}
	if parsed, err := url.Parse(trimmed); err == nil {
		pathLower := strings.ToLower(parsed.Path)
		for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp"} {
			if strings.HasSuffix(pathLower, ext) {
				return true
			}
		}
	}
	return false
}

func containsInt(items []int, want int) bool {
	for _, v := range items {
		if v == want {
			return true
		}
	}
	return false
}

func containsTrimmedFold(items []string, want string) bool {
	target := strings.ToLower(strings.TrimSpace(want))
	for _, v := range items {
		if strings.ToLower(strings.TrimSpace(v)) == target {
			return true
		}
	}
	return false
}

func appendUniqueStrings(base []string, extras ...string) []string {
	out := append([]string(nil), base...)
	for _, item := range extras {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		exists := false
		for _, have := range out {
			if have == item {
				exists = true
				break
			}
		}
		if !exists {
			out = append(out, item)
		}
	}
	return out
}
