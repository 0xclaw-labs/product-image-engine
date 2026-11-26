// Package fidelitygate implements the HUI-2232 product-image I2X engineering
// quality gate (subject fidelity, mask/alpha geometry, OCR preservation, and
// anti-echo-copy / anti-unverified-inferred-view enforcement) together with
// the platform-billing Hold -> Spend / Refund / Unknown recovery state machine.
//
// Zero second billing ledger: this package never stores wallet balances or
// mints local credits/points. All billing transitions strictly align with
// platform-billing using X-App-ID: product-image and principal_id identity.
package fidelitygate

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
	"strings"
)

const (
	// AppID is the canonical platform app identifier for product-image-engine.
	AppID = "product-image"

	DefaultEvaluatorVersion = "i2x-fidelity-eval/v1"
	DefaultThresholdVersion = "i2x-thresholds/v1"

	// Claim modes distinguish strict factual product representation from
	// non-factual creative exploration.
	ClaimFactualFidelity     = "factual_fidelity"
	ClaimCreativeExploration = "creative_exploration"

	// View sources distinguish photographed views from AI-inferred unseen angles
	// (e.g. inferred back-view / side-view without photographic ground truth).
	ViewSourceCaptured   = "captured"
	ViewSourceAIInferred = "ai_inferred"

	// SampleOriginReal marks real product inputs. Synthetic/fake/fixture inputs
	// are rejected fail-closed by the production I2X quality gate.
	SampleOriginReal = "real"

	// Default metric thresholds for factual I2X product background replacement.
	DefaultMinMaskIoU              = 0.92
	DefaultMaxForegroundPixelDrift = 0.08
	DefaultMinOCRSimilarity        = 0.95

	// Minimum background pixel change fraction required to prove the background
	// was actually replaced rather than echoed.
	minBackgroundChangedRatio = 0.10
	// Minimum per-channel delta (0..255) for a background pixel to count as changed.
	minBackgroundPixelDelta = 8
	// Alpha threshold (>= 128) defining protected subject foreground pixels.
	maskForegroundAlphaThreshold = 128
)

// GateVerdict is the structured outcome of EvaluateI2XQualityGate.
type GateVerdict string

const (
	VerdictPass         GateVerdict = "pass"
	VerdictFail         GateVerdict = "fail"
	VerdictInvalidInput GateVerdict = "invalid_input"
)

// RepairAction prescribes the deterministic next engineering action when a
// candidate is rejected by the quality gate.
type RepairAction string

const (
	RepairActionNone                     RepairAction = "none"
	RepairActionRejectAndReupload        RepairAction = "reject_and_reupload"
	RepairActionRetryBackgroundSwap      RepairAction = "retry_background_swap"
	RepairActionRelockSubjectMask        RepairAction = "relock_subject_mask"
	RepairActionRestoreBrandText         RepairAction = "restore_brand_text"
	RepairActionRequireMultiAngleCapture RepairAction = "require_multi_angle_capture"
	RepairActionManualReviewOrRegenerate RepairAction = "manual_review_or_regenerate"
)

// Machine-readable failure reason codes returned in GateDecision.FailureReasons.
const (
	ReasonInvalidAppID              = "invalid_app_id"
	ReasonMissingPrincipalID        = "missing_principal_id"
	ReasonInvalidGateConfig         = "invalid_gate_config"
	ReasonFakeSampleRejected        = "fake_sample_rejected"
	ReasonMissingSourceSHA256       = "missing_or_invalid_source_sha256"
	ReasonMissingCandidateSHA256    = "missing_or_invalid_candidate_sha256"
	ReasonMissingEvaluatorVersion   = "missing_evaluator_version"
	ReasonMissingThresholdVersion   = "missing_threshold_version"
	ReasonThresholdVersionMismatch  = "threshold_version_mismatch"
	ReasonInvalidMetricRange        = "invalid_metric_range"
	ReasonEchoCopyDetected          = "echo_copy_detected"
	ReasonMaskIoUBelowThreshold     = "mask_iou_below_threshold"
	ReasonForegroundDriftExceeded   = "foreground_pixel_drift_exceeded"
	ReasonAlphaIntegrityFailed      = "alpha_integrity_failed"
	ReasonLogoOCRBelowThreshold     = "logo_ocr_similarity_below_threshold"
	ReasonUnverifiedInferredFactual = "unverified_ai_inferred_view_claimed_as_factual"
	ReasonInvalidClaimMode          = "invalid_claim_mode"
	ReasonInvalidViewSource         = "invalid_view_source"
)

var (
	ErrInvalidImageInput = errors.New("fidelitygate: invalid png or mismatched dimensions")
	ErrDegenerateMask    = errors.New("fidelitygate: mask has no foreground or no background")
)

// SubjectFidelityReport captures deterministic engineering measurements comparing
// a source product image against a generated I2X candidate.
type SubjectFidelityReport struct {
	SourceSHA256         string  `json:"source_sha256"`
	CandidateSHA256      string  `json:"candidate_sha256"`
	MaskIoU              float64 `json:"mask_iou"`
	ForegroundPixelDrift float64 `json:"foreground_pixel_drift"`
	LogoOCRSimilarity    float64 `json:"logo_ocr_similarity"`
	BackgroundReplaced   bool    `json:"background_replaced"`
	AlphaIntegrityOK     bool    `json:"alpha_integrity_ok"`
	EvaluatorVersion     string  `json:"evaluator_version"`
	ThresholdVersion     string  `json:"threshold_version"`
}

// I2XGateRequest wraps a SubjectFidelityReport with platform identity scope and
// factual-view provenance required for fail-closed gating.
type I2XGateRequest struct {
	AppID                string                `json:"app_id"`
	PrincipalID          string                `json:"principal_id"`
	RequestKey           string                `json:"request_key"`
	ClaimMode            string                `json:"claim_mode"`
	ViewSource           string                `json:"view_source"`
	InferredViewVerified bool                  `json:"inferred_view_verified"`
	SampleOrigin         string                `json:"sample_origin"`
	Report               SubjectFidelityReport `json:"report"`
}

// GateConfig configures deterministic thresholds for EvaluateI2XQualityGate.
// Zero-value GateConfig fails closed to prevent accidental threshold bypass.
type GateConfig struct {
	ThresholdVersion        string  `json:"threshold_version"`
	MinMaskIoU              float64 `json:"min_mask_iou"`
	MaxForegroundPixelDrift float64 `json:"max_foreground_pixel_drift"`
	MinOCRSimilarity        float64 `json:"min_ocr_similarity"`
	RequireAlphaIntegrity   bool    `json:"require_alpha_integrity"`
	AllowUnverifiedInferred bool    `json:"allow_unverified_inferred"`
	DisallowSameHash        bool    `json:"disallow_same_hash"`
}

// DefaultGateConfig returns the baseline strict I2X quality gate thresholds.
func DefaultGateConfig() GateConfig {
	return GateConfig{
		ThresholdVersion:        DefaultThresholdVersion,
		MinMaskIoU:              DefaultMinMaskIoU,
		MaxForegroundPixelDrift: DefaultMaxForegroundPixelDrift,
		MinOCRSimilarity:        DefaultMinOCRSimilarity,
		RequireAlphaIntegrity:   true,
		AllowUnverifiedInferred: false,
		DisallowSameHash:        true,
	}
}

// GateDecision is the structured fail-closed result of EvaluateI2XQualityGate.
type GateDecision struct {
	Passed           bool         `json:"passed"`
	Verdict          GateVerdict  `json:"verdict"`
	FailureReasons   []string     `json:"failure_reasons"`
	RepairAction     RepairAction `json:"repair_action"`
	EvaluatorVersion string       `json:"evaluator_version,omitempty"`
	ThresholdVersion string       `json:"threshold_version,omitempty"`
}

// CanAdopt returns true only when the gate strictly passed with zero failure reasons.
func (d GateDecision) CanAdopt() bool {
	return d.Passed && d.Verdict == VerdictPass && len(d.FailureReasons) == 0
}

// EvaluateI2XQualityGate performs fail-closed evaluation of an I2X product image
// candidate against deterministic subject fidelity, geometry/alpha, OCR, echo-copy,
// and factual-view provenance rules.
func EvaluateI2XQualityGate(req I2XGateRequest, cfg GateConfig) GateDecision {
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

	// 1. Platform app scope & principal identity.
	if strings.TrimSpace(req.AppID) != AppID {
		inputInvalid = true
		addReason(ReasonInvalidAppID)
	}
	if strings.TrimSpace(req.PrincipalID) == "" {
		inputInvalid = true
		addReason(ReasonMissingPrincipalID)
	}

	// 2. Fail-closed config validation (zero-value config cannot pass).
	cfgThresholdVersion := strings.TrimSpace(cfg.ThresholdVersion)
	if cfgThresholdVersion == "" ||
		!validThresholdRatio(cfg.MinMaskIoU, true) ||
		!validThresholdRatio(cfg.MinOCRSimilarity, true) ||
		!validThresholdRatio(cfg.MaxForegroundPixelDrift, false) {
		inputInvalid = true
		addReason(ReasonInvalidGateConfig)
	}

	// 3. Reject fake/fixture/synthetic samples from passing the quality gate.
	sampleOrigin := strings.ToLower(strings.TrimSpace(req.SampleOrigin))
	if sampleOrigin != "" && sampleOrigin != SampleOriginReal {
		inputInvalid = true
		addReason(ReasonFakeSampleRejected)
	}

	// 4. Validate claim mode and view source provenance.
	claimMode := strings.ToLower(strings.TrimSpace(req.ClaimMode))
	if claimMode == "" {
		claimMode = ClaimFactualFidelity
	}
	if claimMode != ClaimFactualFidelity && claimMode != ClaimCreativeExploration {
		inputInvalid = true
		addReason(ReasonInvalidClaimMode)
	}

	viewSource := strings.ToLower(strings.TrimSpace(req.ViewSource))
	if viewSource == "" {
		viewSource = ViewSourceCaptured
	}
	if viewSource != ViewSourceCaptured && viewSource != ViewSourceAIInferred {
		inputInvalid = true
		addReason(ReasonInvalidViewSource)
	}

	// 5. Validate required SHA256 digests and evaluator/threshold versions.
	srcHash := strings.TrimSpace(req.Report.SourceSHA256)
	candHash := strings.TrimSpace(req.Report.CandidateSHA256)
	if !isValidSHA256Hex(srcHash) {
		inputInvalid = true
		addReason(ReasonMissingSourceSHA256)
	}
	if !isValidSHA256Hex(candHash) {
		inputInvalid = true
		addReason(ReasonMissingCandidateSHA256)
	}

	evalVer := strings.TrimSpace(req.Report.EvaluatorVersion)
	if evalVer == "" {
		inputInvalid = true
		addReason(ReasonMissingEvaluatorVersion)
	}
	threshVer := strings.TrimSpace(req.Report.ThresholdVersion)
	if threshVer == "" {
		inputInvalid = true
		addReason(ReasonMissingThresholdVersion)
	} else if cfgThresholdVersion != "" && threshVer != cfgThresholdVersion {
		inputInvalid = true
		addReason(ReasonThresholdVersionMismatch)
	}

	// 6. Validate deterministic metric numeric ranges (reject NaN/Inf/out-of-bounds).
	metricsValid := true
	if !validUnitFloat(req.Report.MaskIoU) ||
		!validUnitFloat(req.Report.ForegroundPixelDrift) ||
		!validUnitFloat(req.Report.LogoOCRSimilarity) {
		metricsValid = false
		inputInvalid = true
		addReason(ReasonInvalidMetricRange)
	}

	// 7. Echo-copy rejection: background must genuinely change and candidate hash
	// cannot match source hash when DisallowSameHash is enforced.
	if !req.Report.BackgroundReplaced {
		addReason(ReasonEchoCopyDetected)
	}
	if cfg.DisallowSameHash && isValidSHA256Hex(srcHash) && isValidSHA256Hex(candHash) && strings.EqualFold(srcHash, candHash) {
		addReason(ReasonEchoCopyDetected)
	}

	// 8. Geometry, foreground drift, alpha integrity, and OCR checks.
	if metricsValid && cfgThresholdVersion != "" && validThresholdRatio(cfg.MinMaskIoU, true) {
		if req.Report.MaskIoU < cfg.MinMaskIoU {
			addReason(ReasonMaskIoUBelowThreshold)
		}
	}
	if metricsValid && cfgThresholdVersion != "" && validThresholdRatio(cfg.MaxForegroundPixelDrift, false) {
		if req.Report.ForegroundPixelDrift > cfg.MaxForegroundPixelDrift {
			addReason(ReasonForegroundDriftExceeded)
		}
	}
	if cfg.RequireAlphaIntegrity && !req.Report.AlphaIntegrityOK {
		addReason(ReasonAlphaIntegrityFailed)
	}
	if metricsValid && cfgThresholdVersion != "" && validThresholdRatio(cfg.MinOCRSimilarity, true) {
		if req.Report.LogoOCRSimilarity < cfg.MinOCRSimilarity {
			addReason(ReasonLogoOCRBelowThreshold)
		}
	}

	// 9. Reject AI-inferred unverified back-view / occluded view tagged as factual fidelity.
	if claimMode == ClaimFactualFidelity &&
		viewSource == ViewSourceAIInferred &&
		!req.InferredViewVerified &&
		!cfg.AllowUnverifiedInferred {
		addReason(ReasonUnverifiedInferredFactual)
	}

	if len(reasons) == 0 {
		return GateDecision{
			Passed:           true,
			Verdict:          VerdictPass,
			FailureReasons:   []string{},
			RepairAction:     RepairActionNone,
			EvaluatorVersion: evalVer,
			ThresholdVersion: threshVer,
		}
	}

	verdict := VerdictFail
	if inputInvalid && onlyInputInvalidReasons(reasons) {
		verdict = VerdictInvalidInput
	}
	return GateDecision{
		Passed:           false,
		Verdict:          verdict,
		FailureReasons:   reasons,
		RepairAction:     chooseRepairAction(reasons),
		EvaluatorVersion: evalVer,
		ThresholdVersion: threshVer,
	}
}

// MeasurePNGSubjectFidelity computes deterministic SubjectFidelityReport metrics
// directly from source/candidate PNG bytes and their corresponding subject alpha masks.
// logoOCRSimilarity must be supplied by the OCR evaluator in [0, 1].
func MeasurePNGSubjectFidelity(sourcePNG, candidatePNG, sourceMaskPNG, candidateMaskPNG []byte, logoOCRSimilarity float64) (SubjectFidelityReport, error) {
	if len(sourcePNG) == 0 || len(candidatePNG) == 0 || len(sourceMaskPNG) == 0 || len(candidateMaskPNG) == 0 {
		return SubjectFidelityReport{}, ErrInvalidImageInput
	}
	srcImg, err := png.Decode(bytes.NewReader(sourcePNG))
	if err != nil {
		return SubjectFidelityReport{}, fmt.Errorf("%w: decode source: %v", ErrInvalidImageInput, err)
	}
	candImg, err := png.Decode(bytes.NewReader(candidatePNG))
	if err != nil {
		return SubjectFidelityReport{}, fmt.Errorf("%w: decode candidate: %v", ErrInvalidImageInput, err)
	}
	srcMask, err := png.Decode(bytes.NewReader(sourceMaskPNG))
	if err != nil {
		return SubjectFidelityReport{}, fmt.Errorf("%w: decode source mask: %v", ErrInvalidImageInput, err)
	}
	candMask, err := png.Decode(bytes.NewReader(candidateMaskPNG))
	if err != nil {
		return SubjectFidelityReport{}, fmt.Errorf("%w: decode candidate mask: %v", ErrInvalidImageInput, err)
	}

	bounds := srcImg.Bounds()
	if bounds.Empty() ||
		bounds != candImg.Bounds() ||
		bounds != srcMask.Bounds() ||
		bounds != candMask.Bounds() {
		return SubjectFidelityReport{}, ErrInvalidImageInput
	}

	var (
		srcFGCount    int
		candFGCount   int
		intersection  int
		union         int
		bgTotal       int
		bgChanged     int
		fgDriftSum    float64
		candHasOpaque bool
	)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			sp := nrgbaAt(srcImg, x, y)
			cp := nrgbaAt(candImg, x, y)
			sm := nrgbaAt(srcMask, x, y).A >= maskForegroundAlphaThreshold
			cm := nrgbaAt(candMask, x, y).A >= maskForegroundAlphaThreshold

			if cp.A >= maskForegroundAlphaThreshold {
				candHasOpaque = true
			}
			if sm {
				srcFGCount++
			}
			if cm {
				candFGCount++
			}
			if sm && cm {
				intersection++
				fgDriftSum += pixelDrift(sp, cp)
			}
			if sm || cm {
				union++
			}
			if !sm {
				bgTotal++
				if pixelChannelMaxDelta(sp, cp) >= minBackgroundPixelDelta {
					bgChanged++
				}
			}
		}
	}

	if srcFGCount == 0 || bgTotal == 0 {
		return SubjectFidelityReport{}, ErrDegenerateMask
	}

	maskIoU := 0.0
	if union > 0 {
		maskIoU = float64(intersection) / float64(union)
	}
	fgDrift := 1.0
	if intersection > 0 {
		fgDrift = fgDriftSum / float64(intersection)
	}

	srcHash := sha256Hex(sourcePNG)
	candHash := sha256Hex(candidatePNG)
	bgChangedRatio := float64(bgChanged) / float64(bgTotal)
	bgReplaced := !strings.EqualFold(srcHash, candHash) && bgChangedRatio >= minBackgroundChangedRatio
	alphaIntegrityOK := candFGCount > 0 && candFGCount < bounds.Dx()*bounds.Dy() && candHasOpaque

	return SubjectFidelityReport{
		SourceSHA256:         srcHash,
		CandidateSHA256:      candHash,
		MaskIoU:              maskIoU,
		ForegroundPixelDrift: fgDrift,
		LogoOCRSimilarity:    logoOCRSimilarity,
		BackgroundReplaced:   bgReplaced,
		AlphaIntegrityOK:     alphaIntegrityOK,
		EvaluatorVersion:     DefaultEvaluatorVersion,
		ThresholdVersion:     DefaultThresholdVersion,
	}, nil
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

func onlyInputInvalidReasons(reasons []string) bool {
	for _, r := range reasons {
		switch r {
		case ReasonInvalidAppID,
			ReasonMissingPrincipalID,
			ReasonInvalidGateConfig,
			ReasonFakeSampleRejected,
			ReasonMissingSourceSHA256,
			ReasonMissingCandidateSHA256,
			ReasonMissingEvaluatorVersion,
			ReasonMissingThresholdVersion,
			ReasonThresholdVersionMismatch,
			ReasonInvalidMetricRange,
			ReasonInvalidClaimMode,
			ReasonInvalidViewSource:
			continue
		default:
			return false
		}
	}
	return true
}

func chooseRepairAction(reasons []string) RepairAction {
	var (
		hasInput      bool
		hasInferred   bool
		hasEcho       bool
		hasGeometry   bool
		hasOCR        bool
		defectBuckets int
	)
	for _, r := range reasons {
		switch r {
		case ReasonInvalidAppID,
			ReasonMissingPrincipalID,
			ReasonInvalidGateConfig,
			ReasonFakeSampleRejected,
			ReasonMissingSourceSHA256,
			ReasonMissingCandidateSHA256,
			ReasonMissingEvaluatorVersion,
			ReasonMissingThresholdVersion,
			ReasonThresholdVersionMismatch,
			ReasonInvalidMetricRange,
			ReasonInvalidClaimMode,
			ReasonInvalidViewSource:
			hasInput = true
		case ReasonUnverifiedInferredFactual:
			hasInferred = true
		case ReasonEchoCopyDetected:
			hasEcho = true
		case ReasonMaskIoUBelowThreshold, ReasonForegroundDriftExceeded, ReasonAlphaIntegrityFailed:
			hasGeometry = true
		case ReasonLogoOCRBelowThreshold:
			hasOCR = true
		}
	}
	if hasInput {
		return RepairActionRejectAndReupload
	}
	if hasInferred {
		return RepairActionRequireMultiAngleCapture
	}
	if hasEcho {
		defectBuckets++
	}
	if hasGeometry {
		defectBuckets++
	}
	if hasOCR {
		defectBuckets++
	}
	if defectBuckets > 1 {
		return RepairActionManualReviewOrRegenerate
	}
	switch {
	case hasEcho:
		return RepairActionRetryBackgroundSwap
	case hasOCR:
		return RepairActionRestoreBrandText
	case hasGeometry:
		return RepairActionRelockSubjectMask
	default:
		return RepairActionManualReviewOrRegenerate
	}
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
