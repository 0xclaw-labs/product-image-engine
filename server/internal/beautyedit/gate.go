package beautyedit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"slices"
	"strings"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

const (
	DefaultBeautyEvaluatorVersion = "beauty-portrait-eval/v1"
	DefaultBeautyThresholdVersion = "beauty-portrait-thresholds/v1"

	DefaultMinFaceIdentityScoreFidelity = 0.92
	DefaultMinFaceIdentityScoreCreative = 0.88
	DefaultMaxFacialGeometryDrift       = 0.06
	DefaultMaxSkinTextureDrift          = 0.05
	DefaultMaxRealMakeupDrift           = 0.04
	DefaultMaxNonTargetPixelDrift       = 0.05
	DefaultMinTargetChangedRatio        = 0.08

	maskAlphaThreshold  = 128
	minTargetPixelDelta = 8
)

// Machine-readable failure reasons for EvaluateBeautyEditGate.
const (
	ReasonInvalidAppID                    = "invalid_app_id"
	ReasonMissingPrincipalID              = "missing_principal_id"
	ReasonInvalidGateThresholds           = "invalid_beauty_gate_thresholds"
	ReasonFakeSampleRejected              = "fake_or_synthetic_sample_rejected"
	ReasonUnverifiedEditMode              = "edit_mode_not_verified"
	ReasonDishonestPixelLockClaim         = "full_redraw_claimed_pixel_locked_local_edit"
	ReasonFullRedrawMissingPostEditVerify = "full_redraw_missing_post_edit_region_verification"
	ReasonMissingOrInvalidSHA256          = "missing_or_invalid_source_or_candidate_sha256"
	ReasonMissingEvaluatorOrThresholdVer  = "missing_or_mismatched_evaluator_or_threshold_version"
	ReasonEchoCopyDetected                = "echo_copy_no_target_edit_detected"
	ReasonSimilarityOnlyRejected          = "similarity_percentage_only_certification_rejected"
	ReasonIdentityCheckMissing            = "identity_feature_check_not_evaluated"
	ReasonFaceIdentityDriftExceeded       = "face_identity_score_below_threshold"
	ReasonFacialGeometryDriftExceeded     = "facial_geometry_drift_exceeded"
	ReasonRegionInvarianceCheckMissing    = "non_target_region_invariance_not_evaluated"
	ReasonSkinTextureDriftExceeded        = "skin_texture_drift_exceeded"
	ReasonUnapprovedRealMakeupAlteration  = "unapproved_real_makeup_alteration"
	ReasonNonTargetPixelDriftExceeded     = "non_target_region_pixel_drift_exceeded"
	ReasonUnapprovedRegionAltered         = "unapproved_preserved_region_altered"
	ReasonTargetRegionNotModified         = "target_region_change_below_threshold"
	ReasonScopeMatchCheckMissing          = "declared_edit_scope_match_not_evaluated"
	ReasonDeclaredRegionContractInvalid   = "declared_region_contract_invalid"
	ReasonObservedEditOutsideAllowedScope = "observed_modified_region_outside_allowed_scope"
	ReasonObservedEditHitPreservedRegion  = "observed_modified_region_hit_preserved_region"
	ReasonUsageDisclosureCheckMissing     = "usage_disclosure_check_not_evaluated"
	ReasonCreativeClaimedAsRealProof      = "creative_makeup_demo_claimed_as_real_service_proof"
	ReasonCreativeDisclosureMissing       = "creative_makeup_demo_missing_disclosure_label"
	ReasonUsageTypeModeMismatch           = "usage_type_does_not_match_edit_mode"
	ReasonInvalidMetricValue              = "invalid_or_non_finite_metric_value"
)

var (
	// ErrSimilarityOnlyCertificationRejected rejects QA reports that attempt to
	// certify quality, originality, or publish-readiness using only a similarity percentage.
	ErrSimilarityOnlyCertificationRejected = errors.New("beautyedit: similarity percentage alone cannot certify quality, originality, or publish-readiness")

	// ErrInvalidPNGInput indicates malformed or mismatched PNG dimensions.
	ErrInvalidPNGInput = errors.New("beautyedit: invalid PNG bytes or mismatched image dimensions")

	// ErrDegenerateRegionMask indicates empty preserved or target mask regions.
	ErrDegenerateRegionMask = errors.New("beautyedit: preserved or target region mask is empty")
)

// BeautyEditThresholds configures deterministic fail-closed thresholds for
// beauty-portrait restricted editing QA. Zero-value fails closed.
type BeautyEditThresholds struct {
	ThresholdVersion             string  `json:"threshold_version"`
	MinFaceIdentityScoreFidelity float64 `json:"min_face_identity_score_fidelity"`
	MinFaceIdentityScoreCreative float64 `json:"min_face_identity_score_creative"`
	MaxFacialGeometryDrift       float64 `json:"max_facial_geometry_drift"`
	MaxSkinTextureDrift          float64 `json:"max_skin_texture_drift"`
	MaxRealMakeupDrift           float64 `json:"max_real_makeup_drift"`
	MaxNonTargetPixelDrift       float64 `json:"max_non_target_pixel_drift"`
	MinTargetChangedRatio        float64 `json:"min_target_changed_ratio"`
	DisallowSameHash             bool    `json:"disallow_same_hash"`
}

// DefaultBeautyEditThresholds returns strict baseline thresholds for HUI-3902.
func DefaultBeautyEditThresholds() BeautyEditThresholds {
	return BeautyEditThresholds{
		ThresholdVersion:             DefaultBeautyThresholdVersion,
		MinFaceIdentityScoreFidelity: DefaultMinFaceIdentityScoreFidelity,
		MinFaceIdentityScoreCreative: DefaultMinFaceIdentityScoreCreative,
		MaxFacialGeometryDrift:       DefaultMaxFacialGeometryDrift,
		MaxSkinTextureDrift:          DefaultMaxSkinTextureDrift,
		MaxRealMakeupDrift:           DefaultMaxRealMakeupDrift,
		MaxNonTargetPixelDrift:       DefaultMaxNonTargetPixelDrift,
		MinTargetChangedRatio:        DefaultMinTargetChangedRatio,
		DisallowSameHash:             true,
	}
}

// IdentityFeatureCheck verifies human facial identity and geometry preservation.
type IdentityFeatureCheck struct {
	Evaluated           bool    `json:"evaluated"`
	FaceIdentityScore   float64 `json:"face_identity_score"`
	FacialGeometryDrift float64 `json:"facial_geometry_drift"`
}

// RegionInvarianceCheck verifies non-target pixel/texture/makeup preservation
// and confirms the target edit region actually changed.
type RegionInvarianceCheck struct {
	Evaluated                bool    `json:"evaluated"`
	PostEditPixelVerified    bool    `json:"post_edit_pixel_verified"`
	SkinTextureDrift         float64 `json:"skin_texture_drift"`
	MakeupRegionDrift        float64 `json:"makeup_region_drift"`
	NonTargetPixelDrift      float64 `json:"non_target_pixel_drift"`
	TargetRegionChangedRatio float64 `json:"target_region_changed_ratio"`
	UnapprovedRegionAltered  bool    `json:"unapproved_region_altered"`
}

// ScopeMatchCheck compares declared allowed/preserved regions against observed
// modified regions in the generated candidate.
type ScopeMatchCheck struct {
	Evaluated                bool         `json:"evaluated"`
	DeclaredAllowedRegions   []RegionKind `json:"declared_allowed_regions"`
	DeclaredPreservedRegions []RegionKind `json:"declared_preserved_regions"`
	ObservedModifiedRegions  []RegionKind `json:"observed_modified_regions"`
}

// UsageDisclosureCheck verifies strict separation between real-service proof
// and creative makeup demo labeling.
type UsageDisclosureCheck struct {
	Evaluated               bool      `json:"evaluated"`
	UsageType               UsageType `json:"usage_type"`
	IsRealServiceProof      bool      `json:"is_real_service_proof"`
	CreativeDisclosureLabel string    `json:"creative_disclosure_label,omitempty"`
}

// BeautyEditQualityReport carries multi-dimension QA evidence for a beauty
// portrait editing candidate. OverallSimilarityPercent alone is NEVER sufficient.
type BeautyEditQualityReport struct {
	SourceSHA256             string                `json:"source_sha256"`
	CandidateSHA256          string                `json:"candidate_sha256"`
	EvaluatorVersion         string                `json:"evaluator_version"`
	ThresholdVersion         string                `json:"threshold_version"`
	OverallSimilarityPercent float64               `json:"overall_similarity_percent,omitempty"`
	SimilarityOnlyCertified  bool                  `json:"similarity_only_certified,omitempty"`
	Identity                 IdentityFeatureCheck  `json:"identity"`
	RegionInvariance         RegionInvarianceCheck `json:"region_invariance"`
	ScopeMatch               ScopeMatchCheck       `json:"scope_match"`
	UsageDisclosure          UsageDisclosureCheck  `json:"usage_disclosure"`
}

// RejectSimilarityOnlyCertification explicitly rejects any QA report that tries
// to certify a candidate via overall similarity percentage alone without full
// multi-dimension checks (identity, non-target region invariance, declared edit
// scope match, and real-service vs creative disclosure).
func RejectSimilarityOnlyCertification(report BeautyEditQualityReport) error {
	if report.SimilarityOnlyCertified {
		return ErrSimilarityOnlyCertificationRejected
	}
	allMultiDimEvaluated := report.Identity.Evaluated &&
		report.RegionInvariance.Evaluated &&
		report.ScopeMatch.Evaluated &&
		report.UsageDisclosure.Evaluated
	if !allMultiDimEvaluated && report.OverallSimilarityPercent > 0 {
		return fmt.Errorf("%w: overall_similarity_percent=%.2f supplied without all 4 multi-dimension checks",
			ErrSimilarityOnlyCertificationRejected, report.OverallSimilarityPercent)
	}
	if !allMultiDimEvaluated {
		return fmt.Errorf("%w: missing required multi-dimension verification (identity=%v, region_invariance=%v, scope_match=%v, usage_disclosure=%v)",
			ErrSimilarityOnlyCertificationRejected,
			report.Identity.Evaluated,
			report.RegionInvariance.Evaluated,
			report.ScopeMatch.Evaluated,
			report.UsageDisclosure.Evaluated)
	}
	return nil
}

// BeautyEditGateRequest wraps the quality report with platform scope, mode,
// sample origin, and adapter capability honesty declarations.
type BeautyEditGateRequest struct {
	AppID                      string                  `json:"app_id"`
	PrincipalID                string                  `json:"principal_id"`
	Mode                       EditMode                `json:"mode"`
	AdapterEditCapability      AdapterEditCapability   `json:"adapter_edit_capability"`
	ClaimsPixelLockedLocalEdit bool                    `json:"claims_pixel_locked_local_edit"`
	SampleOrigin               string                  `json:"sample_origin"`
	Report                     BeautyEditQualityReport `json:"report"`
}

// BeautyEditGateDecision is the deterministic fail-closed outcome of EvaluateBeautyEditGate.
type BeautyEditGateDecision struct {
	Passed                bool                     `json:"passed"`
	Verdict               fidelitygate.GateVerdict `json:"verdict"`
	FailureReasons        []string                 `json:"failure_reasons"`
	CanClaimRealService   bool                     `json:"can_claim_real_service"`
	EffectiveUsageType    UsageType                `json:"effective_usage_type"`
	EvaluatorVersion      string                   `json:"evaluator_version,omitempty"`
	ThresholdVersion      string                   `json:"threshold_version,omitempty"`
	PostEditPixelVerified bool                     `json:"post_edit_pixel_verified"`
}

// CanAdopt returns true only when the gate strictly passed with zero failure reasons.
func (d BeautyEditGateDecision) CanAdopt() bool {
	return d.Passed && d.Verdict == fidelitygate.VerdictPass && len(d.FailureReasons) == 0
}

// EvaluateBeautyEditGate performs fail-closed multi-dimension QA verification for
// beauty-portrait restricted editing.
func EvaluateBeautyEditGate(req BeautyEditGateRequest, cfg BeautyEditThresholds) BeautyEditGateDecision {
	reasons := make([]string, 0)
	seen := make(map[string]struct{})
	addReason := func(code string) {
		if _, exists := seen[code]; exists {
			return
		}
		seen[code] = struct{}{}
		reasons = append(reasons, code)
	}

	inputInvalid := false

	// 1. Platform scope & principal identity.
	if strings.TrimSpace(req.AppID) != fidelitygate.AppID {
		inputInvalid = true
		addReason(ReasonInvalidAppID)
	}
	if strings.TrimSpace(req.PrincipalID) == "" {
		inputInvalid = true
		addReason(ReasonMissingPrincipalID)
	}

	// 2. Fail-closed threshold validation.
	cfgValid := validThresholds(cfg)
	if !cfgValid {
		inputInvalid = true
		addReason(ReasonInvalidGateThresholds)
	}

	// 3. Reject synthetic/fake/fixture sample origin.
	origin := strings.ToLower(strings.TrimSpace(req.SampleOrigin))
	if origin != "" && origin != fidelitygate.SampleOriginReal {
		inputInvalid = true
		addReason(ReasonFakeSampleRejected)
	}

	// 4. Verified mode & adapter capability honesty.
	if !IsKnownVerifiedMode(req.Mode) {
		inputInvalid = true
		addReason(ReasonUnverifiedEditMode)
	}
	if req.AdapterEditCapability == AdapterCapFullRedraw && req.ClaimsPixelLockedLocalEdit {
		addReason(ReasonDishonestPixelLockClaim)
	}
	if req.AdapterEditCapability != AdapterCapPixelLockedMask && req.AdapterEditCapability != AdapterCapFullRedraw {
		inputInvalid = true
		addReason(ReasonUnverifiedEditMode)
	}

	// 5. Digest and evaluator/threshold version checks.
	srcHash := strings.TrimSpace(req.Report.SourceSHA256)
	candHash := strings.TrimSpace(req.Report.CandidateSHA256)
	if !isValidSHA256Hex(srcHash) || !isValidSHA256Hex(candHash) {
		inputInvalid = true
		addReason(ReasonMissingOrInvalidSHA256)
	}
	evalVer := strings.TrimSpace(req.Report.EvaluatorVersion)
	threshVer := strings.TrimSpace(req.Report.ThresholdVersion)
	if evalVer == "" || threshVer == "" || (strings.TrimSpace(cfg.ThresholdVersion) != "" && threshVer != strings.TrimSpace(cfg.ThresholdVersion)) {
		inputInvalid = true
		addReason(ReasonMissingEvaluatorOrThresholdVer)
	}

	// 6. Similarity-only certification is strictly forbidden.
	if err := RejectSimilarityOnlyCertification(req.Report); err != nil {
		addReason(ReasonSimilarityOnlyRejected)
	}

	// 7. Anti-echo-copy check.
	if cfg.DisallowSameHash && isValidSHA256Hex(srcHash) && isValidSHA256Hex(candHash) && strings.EqualFold(srcHash, candHash) {
		addReason(ReasonEchoCopyDetected)
	}

	// 8. Dimension 1: Identity features.
	if !req.Report.Identity.Evaluated {
		addReason(ReasonIdentityCheckMissing)
	} else {
		if !validUnitFloat(req.Report.Identity.FaceIdentityScore) || !validUnitFloat(req.Report.Identity.FacialGeometryDrift) {
			inputInvalid = true
			addReason(ReasonInvalidMetricValue)
		} else if cfgValid {
			minIdentity := cfg.MinFaceIdentityScoreFidelity
			if req.Mode == ModeCreativeMakeupDemo {
				minIdentity = cfg.MinFaceIdentityScoreCreative
			}
			if req.Report.Identity.FaceIdentityScore < minIdentity {
				addReason(ReasonFaceIdentityDriftExceeded)
			}
			if req.Report.Identity.FacialGeometryDrift > cfg.MaxFacialGeometryDrift {
				addReason(ReasonFacialGeometryDriftExceeded)
			}
		}
	}

	// 9. Dimension 2: Non-target region invariance & pixel verification.
	if !req.Report.RegionInvariance.Evaluated {
		addReason(ReasonRegionInvarianceCheckMissing)
	} else {
		ri := req.Report.RegionInvariance
		if req.AdapterEditCapability == AdapterCapFullRedraw && !ri.PostEditPixelVerified {
			addReason(ReasonFullRedrawMissingPostEditVerify)
		}
		if !validUnitFloat(ri.SkinTextureDrift) ||
			!validUnitFloat(ri.MakeupRegionDrift) ||
			!validUnitFloat(ri.NonTargetPixelDrift) ||
			!validUnitFloat(ri.TargetRegionChangedRatio) {
			inputInvalid = true
			addReason(ReasonInvalidMetricValue)
		} else if cfgValid {
			if ri.UnapprovedRegionAltered {
				addReason(ReasonUnapprovedRegionAltered)
			}
			if ri.NonTargetPixelDrift > cfg.MaxNonTargetPixelDrift {
				addReason(ReasonNonTargetPixelDriftExceeded)
			}
			if ri.TargetRegionChangedRatio < cfg.MinTargetChangedRatio {
				addReason(ReasonTargetRegionNotModified)
			}
			// Fidelity background edit MUST preserve skin texture AND real makeup strictly.
			if req.Mode == ModeFidelityBackgroundEdit {
				if ri.SkinTextureDrift > cfg.MaxSkinTextureDrift {
					addReason(ReasonSkinTextureDriftExceeded)
				}
				if ri.MakeupRegionDrift > cfg.MaxRealMakeupDrift {
					addReason(ReasonUnapprovedRealMakeupAlteration)
				}
			}
		}
	}

	// 10. Dimension 3: Declared edit scope match.
	if !req.Report.ScopeMatch.Evaluated {
		addReason(ReasonScopeMatchCheckMissing)
	} else {
		sm := req.Report.ScopeMatch
		if IsKnownVerifiedMode(req.Mode) {
			if err := ValidateRegionContract(req.Mode, sm.DeclaredAllowedRegions, sm.DeclaredPreservedRegions); err != nil {
				addReason(ReasonDeclaredRegionContractInvalid)
			}
		}
		if len(sm.ObservedModifiedRegions) == 0 {
			addReason(ReasonTargetRegionNotModified)
		}
		for _, obs := range sm.ObservedModifiedRegions {
			if slices.Contains(sm.DeclaredPreservedRegions, obs) {
				addReason(ReasonObservedEditHitPreservedRegion)
				if req.Mode == ModeFidelityBackgroundEdit &&
					(obs == RegionRealMakeup || obs == RegionMakeupRegion || obs == RegionFaceIdentity || obs == RegionSkinTexture) {
					addReason(ReasonUnapprovedRealMakeupAlteration)
				}
			}
			if !slices.Contains(sm.DeclaredAllowedRegions, obs) {
				addReason(ReasonObservedEditOutsideAllowedScope)
			}
			if req.Mode == ModeFidelityBackgroundEdit && isMakeupRegion(obs) {
				addReason(ReasonUnapprovedRealMakeupAlteration)
			}
		}
	}

	// 11. Dimension 4: Real-service vs creative usage disclosure.
	effectiveUsage := UsageCreativeDemo
	if req.Mode == ModeFidelityBackgroundEdit {
		effectiveUsage = UsageRealServiceProof
	}
	if !req.Report.UsageDisclosure.Evaluated {
		addReason(ReasonUsageDisclosureCheckMissing)
	} else {
		ud := req.Report.UsageDisclosure
		switch req.Mode {
		case ModeFidelityBackgroundEdit:
			if ud.UsageType != UsageRealServiceProof || !ud.IsRealServiceProof {
				addReason(ReasonUsageTypeModeMismatch)
			}
		case ModeCreativeMakeupDemo:
			if ud.IsRealServiceProof || ud.UsageType == UsageRealServiceProof {
				addReason(ReasonCreativeClaimedAsRealProof)
			}
			if ud.UsageType != UsageCreativeDemo {
				addReason(ReasonUsageTypeModeMismatch)
			}
			if strings.TrimSpace(ud.CreativeDisclosureLabel) == "" {
				addReason(ReasonCreativeDisclosureMissing)
			}
		}
	}

	if len(reasons) == 0 {
		canClaimReal := req.Mode == ModeFidelityBackgroundEdit &&
			req.Report.UsageDisclosure.UsageType == UsageRealServiceProof &&
			req.Report.UsageDisclosure.IsRealServiceProof
		return BeautyEditGateDecision{
			Passed:                true,
			Verdict:               fidelitygate.VerdictPass,
			FailureReasons:        []string{},
			CanClaimRealService:   canClaimReal,
			EffectiveUsageType:    effectiveUsage,
			EvaluatorVersion:      evalVer,
			ThresholdVersion:      threshVer,
			PostEditPixelVerified: req.Report.RegionInvariance.PostEditPixelVerified,
		}
	}

	verdict := fidelitygate.VerdictFail
	if inputInvalid && onlyInputInvalidBeautyReasons(reasons) {
		verdict = fidelitygate.VerdictInvalidInput
	}
	return BeautyEditGateDecision{
		Passed:                false,
		Verdict:               verdict,
		FailureReasons:        reasons,
		CanClaimRealService:   false,
		EffectiveUsageType:    effectiveUsage,
		EvaluatorVersion:      evalVer,
		ThresholdVersion:      threshVer,
		PostEditPixelVerified: req.Report.RegionInvariance.PostEditPixelVerified,
	}
}

// PNGPortraitRegionMeasurement contains deterministic pixel-level measurements
// across preserved non-target regions, optional makeup region, and allowed target regions.
type PNGPortraitRegionMeasurement struct {
	SourceSHA256             string  `json:"source_sha256"`
	CandidateSHA256          string  `json:"candidate_sha256"`
	NonTargetPixelDrift      float64 `json:"non_target_pixel_drift"`
	MakeupRegionDrift        float64 `json:"makeup_region_drift"`
	TargetRegionChangedRatio float64 `json:"target_region_changed_ratio"`
	EchoCopyDetected         bool    `json:"echo_copy_detected"`
}

// MeasurePNGPortraitRegions computes deterministic pixel-level drift and target
// change ratios directly from PNG source/candidate images and region alpha masks.
//
// Mask convention (alpha >= 128 marks membership):
//   - preservedMaskPNG: pixels that MUST remain invariant (e.g., face/skin/non-target)
//   - targetMaskPNG: pixels allowed to be edited (e.g., background or makeup zone)
//   - makeupMaskPNG: optional dedicated mask for makeup area (if nil/empty, MakeupRegionDrift
//     defaults to NonTargetPixelDrift)
func MeasurePNGPortraitRegions(sourcePNG, candidatePNG, preservedMaskPNG, targetMaskPNG, makeupMaskPNG []byte) (PNGPortraitRegionMeasurement, error) {
	if len(sourcePNG) == 0 || len(candidatePNG) == 0 || len(preservedMaskPNG) == 0 || len(targetMaskPNG) == 0 {
		return PNGPortraitRegionMeasurement{}, ErrInvalidPNGInput
	}
	srcImg, err := png.Decode(bytes.NewReader(sourcePNG))
	if err != nil {
		return PNGPortraitRegionMeasurement{}, fmt.Errorf("%w: decode source: %v", ErrInvalidPNGInput, err)
	}
	candImg, err := png.Decode(bytes.NewReader(candidatePNG))
	if err != nil {
		return PNGPortraitRegionMeasurement{}, fmt.Errorf("%w: decode candidate: %v", ErrInvalidPNGInput, err)
	}
	presMask, err := png.Decode(bytes.NewReader(preservedMaskPNG))
	if err != nil {
		return PNGPortraitRegionMeasurement{}, fmt.Errorf("%w: decode preserved mask: %v", ErrInvalidPNGInput, err)
	}
	targMask, err := png.Decode(bytes.NewReader(targetMaskPNG))
	if err != nil {
		return PNGPortraitRegionMeasurement{}, fmt.Errorf("%w: decode target mask: %v", ErrInvalidPNGInput, err)
	}

	bounds := srcImg.Bounds()
	if bounds.Empty() ||
		bounds != candImg.Bounds() ||
		bounds != presMask.Bounds() ||
		bounds != targMask.Bounds() {
		return PNGPortraitRegionMeasurement{}, ErrInvalidPNGInput
	}

	var makeupMask image.Image
	if len(makeupMaskPNG) > 0 {
		makeupMask, err = png.Decode(bytes.NewReader(makeupMaskPNG))
		if err != nil {
			return PNGPortraitRegionMeasurement{}, fmt.Errorf("%w: decode makeup mask: %v", ErrInvalidPNGInput, err)
		}
		if bounds != makeupMask.Bounds() {
			return PNGPortraitRegionMeasurement{}, ErrInvalidPNGInput
		}
	}

	var (
		presCount    int
		presDriftSum float64
		targCount    int
		targChanged  int
		makeupCount  int
		makeupSum    float64
	)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			sp := nrgbaAt(srcImg, x, y)
			cp := nrgbaAt(candImg, x, y)
			if nrgbaAt(presMask, x, y).A >= maskAlphaThreshold {
				presCount++
				presDriftSum += pixelDrift(sp, cp)
			}
			if nrgbaAt(targMask, x, y).A >= maskAlphaThreshold {
				targCount++
				if pixelChannelMaxDelta(sp, cp) >= minTargetPixelDelta {
					targChanged++
				}
			}
			if makeupMask != nil && nrgbaAt(makeupMask, x, y).A >= maskAlphaThreshold {
				makeupCount++
				makeupSum += pixelDrift(sp, cp)
			}
		}
	}

	if presCount == 0 || targCount == 0 {
		return PNGPortraitRegionMeasurement{}, ErrDegenerateRegionMask
	}

	nonTargetDrift := presDriftSum / float64(presCount)
	makeupDrift := nonTargetDrift
	if makeupCount > 0 {
		makeupDrift = makeupSum / float64(makeupCount)
	}
	targetChangedRatio := float64(targChanged) / float64(targCount)

	srcHash := sha256Hex(sourcePNG)
	candHash := sha256Hex(candidatePNG)
	echo := strings.EqualFold(srcHash, candHash) || targetChangedRatio < DefaultMinTargetChangedRatio

	return PNGPortraitRegionMeasurement{
		SourceSHA256:             srcHash,
		CandidateSHA256:          candHash,
		NonTargetPixelDrift:      nonTargetDrift,
		MakeupRegionDrift:        makeupDrift,
		TargetRegionChangedRatio: targetChangedRatio,
		EchoCopyDetected:         echo,
	}, nil
}

func validThresholds(cfg BeautyEditThresholds) bool {
	if strings.TrimSpace(cfg.ThresholdVersion) == "" {
		return false
	}
	return validThresholdRatio(cfg.MinFaceIdentityScoreFidelity, true) &&
		validThresholdRatio(cfg.MinFaceIdentityScoreCreative, true) &&
		validThresholdRatio(cfg.MaxFacialGeometryDrift, false) &&
		validThresholdRatio(cfg.MaxSkinTextureDrift, false) &&
		validThresholdRatio(cfg.MaxRealMakeupDrift, false) &&
		validThresholdRatio(cfg.MaxNonTargetPixelDrift, false) &&
		validThresholdRatio(cfg.MinTargetChangedRatio, true)
}

func onlyInputInvalidBeautyReasons(reasons []string) bool {
	for _, r := range reasons {
		switch r {
		case ReasonInvalidAppID,
			ReasonMissingPrincipalID,
			ReasonInvalidGateThresholds,
			ReasonFakeSampleRejected,
			ReasonUnverifiedEditMode,
			ReasonMissingOrInvalidSHA256,
			ReasonMissingEvaluatorOrThresholdVer,
			ReasonInvalidMetricValue:
			continue
		default:
			return false
		}
	}
	return true
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isValidSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func validUnitFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1
}

func validThresholdRatio(v float64, strictlyPositive bool) bool {
	if math.IsNaN(v) || math.IsInf(v, 0) || v > 1 {
		return false
	}
	if strictlyPositive {
		return v > 0
	}
	return v >= 0
}

func nrgbaAt(img image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

func pixelDrift(a, b color.NRGBA) float64 {
	dr := math.Abs(float64(a.R)-float64(b.R)) / 255.0
	dg := math.Abs(float64(a.G)-float64(b.G)) / 255.0
	db := math.Abs(float64(a.B)-float64(b.B)) / 255.0
	return (dr + dg + db) / 3.0
}

func pixelChannelMaxDelta(a, b color.NRGBA) int {
	dr := absInt(int(a.R) - int(b.R))
	dg := absInt(int(a.G) - int(b.G))
	db := absInt(int(a.B) - int(b.B))
	m := dr
	if dg > m {
		m = dg
	}
	if db > m {
		m = db
	}
	return m
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
