package lightingscene

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

// LightingSampleClass defines the three-sample lighting/scene fidelity standard
// required for product relighting verification.
type LightingSampleClass string

const (
	// SampleLogoTextPackaging validates dense brand logo & packaging typography
	// preservation under directional relighting.
	SampleLogoTextPackaging LightingSampleClass = "logo_text_packaging"

	// SampleReflectiveMaterial validates glossy, metallic, or glass surfaces
	// where specular highlights must not distort product geometry or material.
	SampleReflectiveMaterial LightingSampleClass = "reflective_material"

	// SampleComplexEdgeSilhouette validates fine product edges and grounded
	// contact shadows without cutout haloing or floating artifacts.
	SampleComplexEdgeSilhouette LightingSampleClass = "complex_edge_silhouette"
)

// ThreeSampleStandard returns the canonical three-sample lighting verification set.
func ThreeSampleStandard() []LightingSampleClass {
	return []LightingSampleClass{
		SampleLogoTextPackaging,
		SampleReflectiveMaterial,
		SampleComplexEdgeSilhouette,
	}
}

// Machine-readable lighting/anchor failure reason codes augmenting fidelitygate.
const (
	ReasonInvalidLightingConfig               = "invalid_lighting_config"
	ReasonInvalidSubjectAnchor                = "invalid_subject_anchor"
	ReasonInvalidLightingSpec                 = "invalid_lighting_spec"
	ReasonSubjectAnchorMismatch               = "subject_anchor_sha256_mismatch"
	ReasonStructureDriftExceeded              = "subject_structure_drift_exceeded"
	ReasonLogoRegionBroken                    = "logo_region_preservation_failed"
	ReasonTextRegionBroken                    = "text_region_preservation_failed"
	ReasonContactShadowMissingOrWeak          = "contact_shadow_missing_or_weak"
	ReasonEdgeMaterialDegraded                = "edge_material_preservation_below_threshold"
	ReasonInvalidLightingMetricRange          = "invalid_lighting_metric_range"
	ReasonPrettyBackgroundHidingBrokenProduct = "pretty_background_hiding_broken_product"
)

const (
	DefaultMinStructureSimilarity     = 0.94
	DefaultMinTextRegionOCRSimilarity = 0.95
	DefaultMinContactShadowScore      = 0.80
	DefaultMinEdgeMaterialScore       = 0.90
	DefaultPrettyBackgroundThreshold  = 0.80
)

// NormalizedRegion defines a normalized [0,1] bounding box on the product subject
// for protected logo or text anchors.
type NormalizedRegion struct {
	Label        string  `json:"label,omitempty"`
	ExpectedText string  `json:"expected_text,omitempty"`
	X            float64 `json:"x"`
	Y            float64 `json:"y"`
	Width        float64 `json:"width"`
	Height       float64 `json:"height"`
}

// Validate ensures a normalized bounding box lies strictly within [0,1].
func (r NormalizedRegion) Validate() error {
	if !validUnitFloat(r.X) || !validUnitFloat(r.Y) ||
		!validUnitFloat(r.Width) || !validUnitFloat(r.Height) {
		return fmt.Errorf("%w: region coordinates must be finite values in [0,1]", ErrInvalidRequest)
	}
	if r.Width <= 0 || r.Height <= 0 {
		return fmt.Errorf("%w: region width and height must be positive", ErrInvalidRequest)
	}
	if r.X+r.Width > 1.0001 || r.Y+r.Height > 1.0001 {
		return fmt.Errorf("%w: region extends outside normalized [0,1] bounds", ErrInvalidRequest)
	}
	return nil
}

// SubjectAnchor locks the immutable identity, digest, protected logo/text
// regions, and structural geometry hash of the source product.
type SubjectAnchor struct {
	SubjectAssetID string             `json:"subject_asset_id"`
	SubjectSHA256  string             `json:"subject_sha256"`
	LogoRegions    []NormalizedRegion `json:"logo_regions"`
	TextRegions    []NormalizedRegion `json:"text_regions"`
	StructureHash  string             `json:"structure_hash"`
}

// Validate checks fixed subject anchor invariants.
func (a SubjectAnchor) Validate(mode Mode) error {
	if strings.TrimSpace(a.SubjectAssetID) == "" {
		return fmt.Errorf("%w: subject_asset_id is required", ErrInvalidRequest)
	}
	if !isValidSHA256Hex(strings.TrimSpace(a.SubjectSHA256)) {
		return fmt.Errorf("%w: subject_sha256 must be a 64-char hex digest", ErrInvalidRequest)
	}
	if mode == ModeFidelityLocked && strings.TrimSpace(a.StructureHash) == "" {
		return fmt.Errorf("%w: structure_hash is required in %s mode", ErrInvalidRequest, ModeFidelityLocked)
	}
	for i, r := range a.LogoRegions {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("logo_regions[%d]: %w", i, err)
		}
	}
	for i, r := range a.TextRegions {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("text_regions[%d]: %w", i, err)
		}
	}
	return nil
}

// LightingSpec describes the target directional lighting, contact shadow
// requirement, edge/material preservation bar, and scene preset.
type LightingSpec struct {
	LightDirection           string  `json:"light_direction"`
	ContactShadowRequired    bool    `json:"contact_shadow_required"`
	EdgeMaterialPreservation float64 `json:"edge_material_preservation"`
	ScenePreset              string  `json:"scene_preset"`
}

// Validate ensures the lighting specification is well-formed.
func (s LightingSpec) Validate(mode Mode) error {
	if strings.TrimSpace(s.LightDirection) == "" {
		return fmt.Errorf("%w: light_direction is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(s.ScenePreset) == "" {
		return fmt.Errorf("%w: scene_preset is required", ErrInvalidRequest)
	}
	if !validUnitFloat(s.EdgeMaterialPreservation) {
		return fmt.Errorf("%w: edge_material_preservation must be in [0,1]", ErrInvalidRequest)
	}
	if mode == ModeFidelityLocked && s.EdgeMaterialPreservation <= 0 {
		return fmt.Errorf("%w: edge_material_preservation must be positive in %s mode", ErrInvalidRequest, ModeFidelityLocked)
	}
	return nil
}

// LightingCandidateMetrics combines fidelitygate.SubjectFidelityReport with
// relighting-specific structure, text-region, contact-shadow, and edge/material
// measurements.
type LightingCandidateMetrics struct {
	Report                   fidelitygate.SubjectFidelityReport `json:"report"`
	CandidateStructureHash   string                             `json:"candidate_structure_hash"`
	StructureSimilarity      float64                            `json:"structure_similarity"`
	TextRegionOCRSimilarity  float64                            `json:"text_region_ocr_similarity"`
	ContactShadowPresent     bool                               `json:"contact_shadow_present"`
	ContactShadowScore       float64                            `json:"contact_shadow_score"`
	EdgeMaterialScore        float64                            `json:"edge_material_score"`
	BackgroundAestheticScore float64                            `json:"background_aesthetic_score"`
	ViewSource               string                             `json:"view_source,omitempty"`
	InferredViewVerified     bool                               `json:"inferred_view_verified,omitempty"`
}

// LightingFidelityConfig configures fail-closed lighting and subject fidelity
// thresholds on top of fidelitygate.GateConfig. Zero-value config fails closed.
type LightingFidelityConfig struct {
	GateConfig                 fidelitygate.GateConfig `json:"gate_config"`
	MinStructureSimilarity     float64                 `json:"min_structure_similarity"`
	MinTextRegionOCRSimilarity float64                 `json:"min_text_region_ocr_similarity"`
	MinContactShadowScore      float64                 `json:"min_contact_shadow_score"`
	MinEdgeMaterialScore       float64                 `json:"min_edge_material_score"`
	PrettyBackgroundThreshold  float64                 `json:"pretty_background_threshold"`
}

// DefaultLightingFidelityConfig returns strict baseline thresholds aligned with
// fidelitygate.DefaultGateConfig.
func DefaultLightingFidelityConfig() LightingFidelityConfig {
	return LightingFidelityConfig{
		GateConfig:                 fidelitygate.DefaultGateConfig(),
		MinStructureSimilarity:     DefaultMinStructureSimilarity,
		MinTextRegionOCRSimilarity: DefaultMinTextRegionOCRSimilarity,
		MinContactShadowScore:      DefaultMinContactShadowScore,
		MinEdgeMaterialScore:       DefaultMinEdgeMaterialScore,
		PrettyBackgroundThreshold:  DefaultPrettyBackgroundThreshold,
	}
}

// LightingFidelityInput supplies everything needed to evaluate a relit candidate
// against its fixed SubjectAnchor, LightingSpec, and fidelitygate rules.
type LightingFidelityInput struct {
	Scope        fidelitygate.BillingScope `json:"scope"`
	Mode         Mode                      `json:"mode"`
	SampleClass  LightingSampleClass       `json:"sample_class"`
	SampleOrigin string                    `json:"sample_origin"`
	Anchor       SubjectAnchor             `json:"anchor"`
	Lighting     LightingSpec              `json:"lighting"`
	Candidate    LightingCandidateMetrics  `json:"candidate"`
}

// LightingFidelityResult is the deterministic fail-closed result of
// EvaluateLightingFidelity.
type LightingFidelityResult struct {
	Mode                          Mode                      `json:"mode"`
	SampleClass                   LightingSampleClass       `json:"sample_class"`
	Passed                        bool                      `json:"passed"`
	Verdict                       fidelitygate.GateVerdict  `json:"verdict"`
	GateDecision                  fidelitygate.GateDecision `json:"gate_decision"`
	LightingFailureReasons        []string                  `json:"lighting_failure_reasons"`
	PrettyBackgroundMaskingDefect bool                      `json:"pretty_background_masking_defect"`
	SilentCreativeSwitchBlocked   bool                      `json:"silent_creative_switch_blocked"`
}

// CombinedGateDecision merges base fidelitygate.GateDecision with lighting and
// subject-anchor failure reasons so downstream HUI-2232 billing settlement never
// adopts a candidate that failed contact-shadow, edge/material, or anchor checks.
func (r LightingFidelityResult) CombinedGateDecision() fidelitygate.GateDecision {
	merged := r.GateDecision
	reasons := make([]string, 0, len(r.GateDecision.FailureReasons)+len(r.LightingFailureReasons))
	seen := make(map[string]struct{})
	for _, code := range r.GateDecision.FailureReasons {
		if _, ok := seen[code]; !ok {
			seen[code] = struct{}{}
			reasons = append(reasons, code)
		}
	}
	for _, code := range r.LightingFailureReasons {
		if _, ok := seen[code]; !ok {
			seen[code] = struct{}{}
			reasons = append(reasons, code)
		}
	}
	merged.FailureReasons = reasons
	if r.Passed && r.GateDecision.CanAdopt() && len(reasons) == 0 {
		merged.Passed = true
		merged.Verdict = fidelitygate.VerdictPass
		merged.RepairAction = fidelitygate.RepairActionNone
		return merged
	}
	merged.Passed = false
	if r.Verdict != "" && r.Verdict != fidelitygate.VerdictPass {
		merged.Verdict = r.Verdict
	} else if merged.Verdict == "" || merged.Verdict == fidelitygate.VerdictPass {
		merged.Verdict = fidelitygate.VerdictFail
	}
	if merged.RepairAction == "" || merged.RepairAction == fidelitygate.RepairActionNone {
		merged.RepairAction = chooseLightingRepairAction(reasons)
	}
	return merged
}

// EvaluateLightingFidelity evaluates a relit product candidate against:
//  1. Core I2X subject fidelity via fidelitygate.EvaluateI2XQualityGate.
//  2. Fixed SubjectAnchor digest, structure similarity, logo regions, and text regions.
//  3. Directional contact shadow and edge/material preservation requirements.
//  4. Anti-"pretty background hiding broken product" enforcement in ModeFidelityLocked.
//
// Invariant: failing fidelity in ModeFidelityLocked NEVER silently switches Mode
// to ModeCreativeExplicit.
func EvaluateLightingFidelity(in LightingFidelityInput, cfg LightingFidelityConfig) LightingFidelityResult {
	lightingReasons := make([]string, 0)
	seen := make(map[string]struct{})
	addReason := func(code string) {
		if _, exists := seen[code]; exists {
			return
		}
		seen[code] = struct{}{}
		lightingReasons = append(lightingReasons, code)
	}

	inputInvalid := false

	// 1. Fail-closed config check.
	if !validPositiveUnitFloat(cfg.MinStructureSimilarity) ||
		!validPositiveUnitFloat(cfg.MinTextRegionOCRSimilarity) ||
		!validPositiveUnitFloat(cfg.MinContactShadowScore) ||
		!validPositiveUnitFloat(cfg.MinEdgeMaterialScore) ||
		!validUnitFloat(cfg.PrettyBackgroundThreshold) {
		inputInvalid = true
		addReason(ReasonInvalidLightingConfig)
	}

	// 2. Validate mode, anchor, and lighting spec.
	if in.Mode != ModeFidelityLocked && in.Mode != ModeCreativeExplicit {
		inputInvalid = true
	}
	if err := in.Anchor.Validate(in.Mode); err != nil {
		inputInvalid = true
		addReason(ReasonInvalidSubjectAnchor)
	}
	if err := in.Lighting.Validate(in.Mode); err != nil {
		inputInvalid = true
		addReason(ReasonInvalidLightingSpec)
	}

	// 3. Delegate core I2X subject fidelity directly to fidelitygate.
	viewSource := strings.TrimSpace(in.Candidate.ViewSource)
	if viewSource == "" {
		viewSource = fidelitygate.ViewSourceCaptured
	}
	baseDecision := fidelitygate.EvaluateI2XQualityGate(fidelitygate.I2XGateRequest{
		AppID:                in.Scope.AppID,
		PrincipalID:          in.Scope.PrincipalID,
		RequestKey:           in.Scope.RequestKey,
		ClaimMode:            in.Mode.ClaimMode(),
		ViewSource:           viewSource,
		InferredViewVerified: in.Candidate.InferredViewVerified,
		SampleOrigin:         in.SampleOrigin,
		Report:               in.Candidate.Report,
	}, cfg.GateConfig)

	// 4. Verify SubjectAnchor digest matches the evaluated source digest.
	anchorSHA := strings.TrimSpace(in.Anchor.SubjectSHA256)
	reportSrcSHA := strings.TrimSpace(in.Candidate.Report.SourceSHA256)
	if isValidSHA256Hex(anchorSHA) && isValidSHA256Hex(reportSrcSHA) && !strings.EqualFold(anchorSHA, reportSrcSHA) {
		addReason(ReasonSubjectAnchorMismatch)
	}

	// 5. Validate lighting-specific metric numeric ranges.
	metricsValid := true
	if !validUnitFloat(in.Candidate.StructureSimilarity) ||
		!validUnitFloat(in.Candidate.TextRegionOCRSimilarity) ||
		!validUnitFloat(in.Candidate.ContactShadowScore) ||
		!validUnitFloat(in.Candidate.EdgeMaterialScore) ||
		!validUnitFloat(in.Candidate.BackgroundAestheticScore) {
		metricsValid = false
		inputInvalid = true
		addReason(ReasonInvalidLightingMetricRange)
	}

	productBroken := hasBaseProductFidelityDefect(baseDecision.FailureReasons)

	// 6. Subject structure, logo regions, and text regions enforcement.
	if metricsValid && validPositiveUnitFloat(cfg.MinStructureSimilarity) {
		structOK := in.Candidate.StructureSimilarity >= cfg.MinStructureSimilarity
		if in.Mode == ModeFidelityLocked && strings.TrimSpace(in.Anchor.StructureHash) != "" {
			if strings.TrimSpace(in.Candidate.CandidateStructureHash) == "" || !structOK {
				addReason(ReasonStructureDriftExceeded)
				productBroken = true
			}
		} else if !structOK {
			addReason(ReasonStructureDriftExceeded)
			productBroken = true
		}
	}

	if len(in.Anchor.LogoRegions) > 0 &&
		validUnitFloat(in.Candidate.Report.LogoOCRSimilarity) &&
		validPositiveUnitFloat(cfg.GateConfig.MinOCRSimilarity) &&
		in.Candidate.Report.LogoOCRSimilarity < cfg.GateConfig.MinOCRSimilarity {
		addReason(ReasonLogoRegionBroken)
		productBroken = true
	}

	if len(in.Anchor.TextRegions) > 0 &&
		metricsValid &&
		validPositiveUnitFloat(cfg.MinTextRegionOCRSimilarity) &&
		in.Candidate.TextRegionOCRSimilarity < cfg.MinTextRegionOCRSimilarity {
		addReason(ReasonTextRegionBroken)
		productBroken = true
	}

	// 7. Contact shadow & edge/material preservation checks.
	if in.Lighting.ContactShadowRequired && metricsValid && validPositiveUnitFloat(cfg.MinContactShadowScore) {
		if !in.Candidate.ContactShadowPresent || in.Candidate.ContactShadowScore < cfg.MinContactShadowScore {
			addReason(ReasonContactShadowMissingOrWeak)
		}
	}

	if metricsValid && validPositiveUnitFloat(cfg.MinEdgeMaterialScore) {
		requiredEdge := cfg.MinEdgeMaterialScore
		if validUnitFloat(in.Lighting.EdgeMaterialPreservation) && in.Lighting.EdgeMaterialPreservation > requiredEdge {
			requiredEdge = in.Lighting.EdgeMaterialPreservation
		}
		if in.Candidate.EdgeMaterialScore < requiredEdge {
			addReason(ReasonEdgeMaterialDegraded)
		}
	}

	// 8. Reject "pretty background hiding broken product" in ModeFidelityLocked.
	prettyMaskingDefect := false
	if in.Mode == ModeFidelityLocked &&
		productBroken &&
		metricsValid &&
		validUnitFloat(cfg.PrettyBackgroundThreshold) &&
		in.Candidate.BackgroundAestheticScore >= cfg.PrettyBackgroundThreshold {
		prettyMaskingDefect = true
		addReason(ReasonPrettyBackgroundHidingBrokenProduct)
	}

	passed := baseDecision.CanAdopt() && len(lightingReasons) == 0 && !inputInvalid
	verdict := fidelitygate.VerdictPass
	if !passed {
		baseInvalidOnly := baseDecision.Verdict == fidelitygate.VerdictInvalidInput
		lightingInvalidOnly := len(lightingReasons) == 0 || onlyLightingInputInvalid(lightingReasons)
		if (baseInvalidOnly || (baseDecision.CanAdopt() && inputInvalid)) && lightingInvalidOnly {
			verdict = fidelitygate.VerdictInvalidInput
		} else {
			verdict = fidelitygate.VerdictFail
		}
	}

	silentSwitchBlocked := in.Mode == ModeFidelityLocked && !passed

	return LightingFidelityResult{
		Mode:                          in.Mode,
		SampleClass:                   in.SampleClass,
		Passed:                        passed,
		Verdict:                       verdict,
		GateDecision:                  baseDecision,
		LightingFailureReasons:        lightingReasons,
		PrettyBackgroundMaskingDefect: prettyMaskingDefect,
		SilentCreativeSwitchBlocked:   silentSwitchBlocked,
	}
}

// ThreeSampleBatchSummary reports whether all three canonical lighting sample
// classes passed evaluation under ModeFidelityLocked.
type ThreeSampleBatchSummary struct {
	AllPassed      bool                                           `json:"all_passed"`
	MissingSamples []LightingSampleClass                          `json:"missing_samples"`
	FailedSamples  []LightingSampleClass                          `json:"failed_samples"`
	Results        map[LightingSampleClass]LightingFidelityResult `json:"results"`
}

// EvaluateThreeSampleBatch evaluates a batch of relit samples against the
// three-sample lighting standard (logo_text_packaging, reflective_material,
// complex_edge_silhouette).
func EvaluateThreeSampleBatch(inputs []LightingFidelityInput, cfg LightingFidelityConfig) ThreeSampleBatchSummary {
	results := make(map[LightingSampleClass]LightingFidelityResult, len(inputs))
	for _, in := range inputs {
		results[in.SampleClass] = EvaluateLightingFidelity(in, cfg)
	}
	missing := make([]LightingSampleClass, 0)
	failed := make([]LightingSampleClass, 0)
	for _, sample := range ThreeSampleStandard() {
		res, ok := results[sample]
		if !ok {
			missing = append(missing, sample)
			continue
		}
		if !res.Passed || res.Mode != ModeFidelityLocked {
			failed = append(failed, sample)
		}
	}
	return ThreeSampleBatchSummary{
		AllPassed:      len(missing) == 0 && len(failed) == 0,
		MissingSamples: missing,
		FailedSamples:  failed,
		Results:        results,
	}
}

func hasBaseProductFidelityDefect(reasons []string) bool {
	for _, r := range reasons {
		switch r {
		case fidelitygate.ReasonMaskIoUBelowThreshold,
			fidelitygate.ReasonForegroundDriftExceeded,
			fidelitygate.ReasonAlphaIntegrityFailed,
			fidelitygate.ReasonLogoOCRBelowThreshold,
			fidelitygate.ReasonUnverifiedInferredFactual:
			return true
		}
	}
	return false
}

func onlyLightingInputInvalid(reasons []string) bool {
	if len(reasons) == 0 {
		return false
	}
	for _, r := range reasons {
		switch r {
		case ReasonInvalidLightingConfig,
			ReasonInvalidSubjectAnchor,
			ReasonInvalidLightingSpec,
			ReasonInvalidLightingMetricRange:
			continue
		default:
			return false
		}
	}
	return true
}

func chooseLightingRepairAction(reasons []string) fidelitygate.RepairAction {
	for _, r := range reasons {
		switch r {
		case ReasonInvalidLightingConfig, ReasonInvalidSubjectAnchor, ReasonInvalidLightingSpec, ReasonInvalidLightingMetricRange, ReasonSubjectAnchorMismatch:
			return fidelitygate.RepairActionRejectAndReupload
		case ReasonLogoRegionBroken, ReasonTextRegionBroken:
			return fidelitygate.RepairActionRestoreBrandText
		case ReasonStructureDriftExceeded, ReasonEdgeMaterialDegraded:
			return fidelitygate.RepairActionRelockSubjectMask
		case ReasonContactShadowMissingOrWeak:
			return fidelitygate.RepairActionRetryBackgroundSwap
		}
	}
	return fidelitygate.RepairActionManualReviewOrRegenerate
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

func validPositiveUnitFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v <= 1
}
