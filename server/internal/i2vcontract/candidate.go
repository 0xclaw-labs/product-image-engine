package i2vcontract

import (
	"errors"
	"math"
	"strings"
)

const (
	DefaultVideoEvaluatorVersion = "i2v-video-eval/v1"
	DefaultVideoThresholdVersion = "i2v-video-thresholds/v1"

	// CandidateDestinationMerchantLibrary is the sole allowed landing zone for
	// generated product video artifacts. Publishing or reward disbursement requires
	// separate explicit merchant confirmation & downstream approval.
	CandidateDestinationMerchantLibrary = "merchant_candidate_library"
)

// VideoGateVerdict is the deterministic outcome of EvaluateVideoFidelity.
type VideoGateVerdict string

const (
	VideoVerdictPass       VideoGateVerdict = "pass"
	VideoVerdictFail       VideoGateVerdict = "fail"
	VideoVerdictUnverified VideoGateVerdict = "unverified"
)

// Machine-readable video fidelity failure reasons.
const (
	VideoReasonDecodeFailed           = "video_decode_failed"
	VideoReasonStillOrPlaceholder     = "still_image_or_placeholder_video_rejected"
	VideoReasonInvalidFileHashOrSize  = "missing_or_invalid_video_sha256_or_empty_bytes"
	VideoReasonInvalidDurationOrDims  = "invalid_duration_fps_or_dimensions"
	VideoReasonTemporalDiscontinuity  = "temporal_continuity_below_threshold"
	VideoReasonFrozenFrameAnomaly     = "frozen_frame_anomaly_detected"
	VideoReasonSubjectStructureDrift  = "subject_structure_score_below_threshold"
	VideoReasonLogoIntegrityDrift     = "logo_integrity_score_below_threshold"
	VideoReasonTextOCRDrift           = "text_ocr_similarity_below_threshold"
	VideoReasonMaskIoUBelowThreshold  = "mask_iou_mean_below_threshold"
	VideoReasonDishonestTrue360Claim  = "single_image_dishonest_true_360_claim"
	VideoReasonInvalidThresholdConfig = "invalid_video_fidelity_threshold_config"
)

var (
	ErrAutoPublishForbidden = errors.New("i2vcontract: product video candidate cannot auto-publish; explicit merchant approval is required")
	ErrAutoRewardForbidden  = errors.New("i2vcontract: product video candidate cannot trigger rewards automatically")
	ErrVideoFidelityFailed  = errors.New("i2vcontract: generated video failed decode/temporal/subject fidelity gate")
)

// VideoFidelityThresholds configures deterministic thresholds for whole-clip
// decode, temporal continuity, frozen-frame, and subject/logo preservation checks.
type VideoFidelityThresholds struct {
	EvaluatorVersion         string  `json:"evaluator_version"`
	ThresholdVersion         string  `json:"threshold_version"`
	MinTemporalContinuity    float64 `json:"min_temporal_continuity"`
	MaxFrozenFrameStreak     int     `json:"max_frozen_frame_streak"`
	MaxFrozenFrameRatio      float64 `json:"max_frozen_frame_ratio"`
	MinSubjectStructureScore float64 `json:"min_subject_structure_score"`
	MinLogoIntegrityScore    float64 `json:"min_logo_integrity_score"`
	MinTextOCRSimilarity     float64 `json:"min_text_ocr_similarity"`
	MinMaskIoUMean           float64 `json:"min_mask_iou_mean"`
	MinDurationSeconds       float64 `json:"min_duration_seconds"`
	MinFPS                   float64 `json:"min_fps"`
}

// DefaultVideoFidelityThresholds returns the strict baseline thresholds for HUI-1702.
func DefaultVideoFidelityThresholds() VideoFidelityThresholds {
	return VideoFidelityThresholds{
		EvaluatorVersion:         DefaultVideoEvaluatorVersion,
		ThresholdVersion:         DefaultVideoThresholdVersion,
		MinTemporalContinuity:    0.85,
		MaxFrozenFrameStreak:     4,
		MaxFrozenFrameRatio:      0.10,
		MinSubjectStructureScore: 0.90,
		MinLogoIntegrityScore:    0.92,
		MinTextOCRSimilarity:     0.92,
		MinMaskIoUMean:           0.88,
		MinDurationSeconds:       1.0,
		MinFPS:                   8.0,
	}
}

// VideoFidelityMetrics carries measured full-clip decode, temporal, frozen-frame,
// and subject/brand fidelity evidence for a downloaded video artifact.
type VideoFidelityMetrics struct {
	VideoURL                string  `json:"video_url"`
	MediaType               string  `json:"media_type"`
	FileSHA256              string  `json:"file_sha256"`
	ByteSize                int64   `json:"byte_size"`
	DecodedOK               bool    `json:"decoded_ok"`
	Codec                   string  `json:"codec"`
	Width                   int     `json:"width"`
	Height                  int     `json:"height"`
	FPS                     float64 `json:"fps"`
	ActualDurationSeconds   float64 `json:"actual_duration_seconds"`
	TemporalContinuityScore float64 `json:"temporal_continuity_score"`
	MaxFrozenFrameStreak    int     `json:"max_frozen_frame_streak"`
	FrozenFrameRatio        float64 `json:"frozen_frame_ratio"`
	SubjectStructureScore   float64 `json:"subject_structure_score"`
	LogoIntegrityScore      float64 `json:"logo_integrity_score"`
	TextOCRSimilarity       float64 `json:"text_ocr_similarity"`
	MaskIoUMean             float64 `json:"mask_iou_mean"`
	ClaimsTrue360           bool    `json:"claims_true_360"`
}

// VideoFidelityAssessment is persisted on the candidate record and forwarded to
// downstream consumers (HUI-5196 / HUI-1745 / HUI-2232).
type VideoFidelityAssessment struct {
	Verdict               VideoGateVerdict     `json:"verdict"`
	Passed                bool                 `json:"passed"`
	DecodedOK             bool                 `json:"decoded_ok"`
	HasFrozenFrameAnomaly bool                 `json:"has_frozen_frame_anomaly"`
	BackViewUnproven      bool                 `json:"back_view_unproven"`
	CannotClaimTrue360    bool                 `json:"cannot_claim_true_360"`
	FailureReasons        []string             `json:"failure_reasons,omitempty"`
	QualityLimitations    []string             `json:"quality_limitations,omitempty"`
	EvaluatorVersion      string               `json:"evaluator_version"`
	ThresholdVersion      string               `json:"threshold_version"`
	Metrics               VideoFidelityMetrics `json:"metrics"`
}

// EvaluateVideoFidelity performs fail-closed full-clip verification across decode,
// temporal continuity, frozen-frame detection, subject/logo/OCR preservation, and
// single-image 360 honesty.
func EvaluateVideoFidelity(
	metrics VideoFidelityMetrics,
	view ViewProvenance,
	thr VideoFidelityThresholds,
) VideoFidelityAssessment {
	viewAssessment, viewErr := EvaluateViewProvenance(view)
	out := VideoFidelityAssessment{
		Verdict:            VideoVerdictFail,
		Passed:             false,
		DecodedOK:          metrics.DecodedOK,
		BackViewUnproven:   viewAssessment.BackViewUnproven,
		CannotClaimTrue360: viewAssessment.CannotClaimTrue360,
		QualityLimitations: append([]string(nil), viewAssessment.Limitations...),
		EvaluatorVersion:   strings.TrimSpace(thr.EvaluatorVersion),
		ThresholdVersion:   strings.TrimSpace(thr.ThresholdVersion),
		Metrics:            metrics,
	}

	reasons := make([]string, 0)
	if !thresholdsValid(thr) {
		reasons = append(reasons, VideoReasonInvalidThresholdConfig)
	}

	if viewErr != nil || (viewAssessment.CannotClaimTrue360 && metrics.ClaimsTrue360) {
		reasons = append(reasons, VideoReasonDishonestTrue360Claim)
	}

	mediaLower := strings.ToLower(strings.TrimSpace(metrics.MediaType))
	if strings.HasPrefix(mediaLower, "image/") ||
		!strings.HasPrefix(mediaLower, "video/") ||
		strings.TrimSpace(metrics.VideoURL) == "" ||
		isPlaceholderOrStillURL(metrics.VideoURL) {
		reasons = append(reasons, VideoReasonStillOrPlaceholder)
	}

	if !metrics.DecodedOK || strings.TrimSpace(metrics.Codec) == "" {
		reasons = append(reasons, VideoReasonDecodeFailed)
	}

	digest := strings.ToLower(strings.TrimSpace(metrics.FileSHA256))
	if !sha256HexPattern.MatchString(digest) || metrics.ByteSize <= 0 {
		reasons = append(reasons, VideoReasonInvalidFileHashOrSize)
	}

	if metrics.Width <= 0 || metrics.Height <= 0 ||
		!validFinite(metrics.ActualDurationSeconds) || metrics.ActualDurationSeconds < thr.MinDurationSeconds ||
		!validFinite(metrics.FPS) || metrics.FPS < thr.MinFPS {
		reasons = append(reasons, VideoReasonInvalidDurationOrDims)
	}

	if !validUnitScore(metrics.TemporalContinuityScore) || metrics.TemporalContinuityScore < thr.MinTemporalContinuity {
		reasons = append(reasons, VideoReasonTemporalDiscontinuity)
	}

	frozenAnomaly := metrics.MaxFrozenFrameStreak < 0 ||
		metrics.MaxFrozenFrameStreak > thr.MaxFrozenFrameStreak ||
		!validUnitScore(metrics.FrozenFrameRatio) ||
		metrics.FrozenFrameRatio > thr.MaxFrozenFrameRatio
	out.HasFrozenFrameAnomaly = frozenAnomaly
	if frozenAnomaly {
		reasons = append(reasons, VideoReasonFrozenFrameAnomaly)
	}

	if !validUnitScore(metrics.SubjectStructureScore) || metrics.SubjectStructureScore < thr.MinSubjectStructureScore {
		reasons = append(reasons, VideoReasonSubjectStructureDrift)
	}
	if !validUnitScore(metrics.LogoIntegrityScore) || metrics.LogoIntegrityScore < thr.MinLogoIntegrityScore {
		reasons = append(reasons, VideoReasonLogoIntegrityDrift)
	}
	if !validUnitScore(metrics.TextOCRSimilarity) || metrics.TextOCRSimilarity < thr.MinTextOCRSimilarity {
		reasons = append(reasons, VideoReasonTextOCRDrift)
	}
	if !validUnitScore(metrics.MaskIoUMean) || metrics.MaskIoUMean < thr.MinMaskIoUMean {
		reasons = append(reasons, VideoReasonMaskIoUBelowThreshold)
	}

	out.FailureReasons = reasons
	if len(reasons) == 0 {
		out.Verdict = VideoVerdictPass
		out.Passed = true
	}
	return out
}

// CandidateReturnRecord represents the merchant-candidate-only return artifact.
// AutoPublished and RewardTriggered are always false by construction.
type CandidateReturnRecord struct {
	Destination           string                  `json:"destination"`
	CandidateID           string                  `json:"candidate_id"`
	TaskID                string                  `json:"task_id"`
	TenantID              string                  `json:"tenant_id"`
	ProjectID             string                  `json:"project_id"`
	OutputAssetID         string                  `json:"output_asset_id"`
	OutputVersionID       string                  `json:"output_version_id"`
	OutputSHA256          string                  `json:"output_sha256"`
	ActualDurationSeconds float64                 `json:"actual_duration_seconds"`
	VideoURL              string                  `json:"video_url"`
	AutoPublished         bool                    `json:"auto_published"`
	RewardTriggered       bool                    `json:"reward_triggered"`
	MerchantConfirmed     bool                    `json:"merchant_confirmed"`
	BackViewUnproven      bool                    `json:"back_view_unproven"`
	CannotClaimTrue360    bool                    `json:"cannot_claim_true_360"`
	QualityLimitations    []string                `json:"quality_limitations,omitempty"`
	Fidelity              VideoFidelityAssessment `json:"fidelity"`
}

// AttemptAutoPublish always refuses automatic external publication on video completion.
func (c CandidateReturnRecord) AttemptAutoPublish() error {
	return ErrAutoPublishForbidden
}

// AttemptAutoReward always refuses automatic reward triggering on video completion.
func (c CandidateReturnRecord) AttemptAutoReward() error {
	return ErrAutoRewardForbidden
}

// BillingReceiptRef carries platform-billing trace references without maintaining
// any second wallet balance.
type BillingReceiptRef struct {
	PayerType       PayerType `json:"payer_type"`
	QuoteID         string    `json:"quote_id,omitempty"`
	PricingVersion  string    `json:"pricing_version,omitempty"`
	HoldID          string    `json:"hold_id,omitempty"`
	SpendID         string    `json:"spend_id,omitempty"`
	RefundID        string    `json:"refund_id,omitempty"`
	BillingState    string    `json:"billing_state"`
	ActualCostFen   int64     `json:"actual_cost_fen"`
	FreeQuotaGrant  string    `json:"free_quota_grant_id,omitempty"`
	AutoChargedPaid bool      `json:"auto_charged_paid"`
}

// DownstreamHandoffEnvelope is the traceable contract consumed by HUI-5196
// (motion director & video QA), HUI-1745 (cross-app return), and HUI-2232
// (product image I2X quality gate).
type DownstreamHandoffEnvelope struct {
	SchemaVersion         string                 `json:"schema_version"`
	TraceID               string                 `json:"trace_id"`
	TaskID                string                 `json:"task_id"`
	TaskFingerprint       string                 `json:"task_fingerprint"`
	FixedProductRef       FixedProductRef        `json:"fixed_product_ref"`
	ExecutionPreference   ExecutionPreference    `json:"execution_preference"`
	ModelID               string                 `json:"model_id"`
	ProviderID            string                 `json:"provider_id"`
	BasicImagePathBlocked bool                   `json:"basic_image_path_blocked"`
	BackViewUnproven      bool                   `json:"back_view_unproven"`
	CannotClaimTrue360    bool                   `json:"cannot_claim_true_360"`
	QualityLimitations    []string               `json:"quality_limitations,omitempty"`
	Billing               BillingReceiptRef      `json:"billing"`
	Candidate             *CandidateReturnRecord `json:"candidate,omitempty"`
}

func thresholdsValid(thr VideoFidelityThresholds) bool {
	if strings.TrimSpace(thr.EvaluatorVersion) == "" || strings.TrimSpace(thr.ThresholdVersion) == "" {
		return false
	}
	if !validUnitScore(thr.MinTemporalContinuity) || thr.MinTemporalContinuity <= 0 ||
		!validUnitScore(thr.MaxFrozenFrameRatio) ||
		!validUnitScore(thr.MinSubjectStructureScore) || thr.MinSubjectStructureScore <= 0 ||
		!validUnitScore(thr.MinLogoIntegrityScore) || thr.MinLogoIntegrityScore <= 0 ||
		!validUnitScore(thr.MinTextOCRSimilarity) || thr.MinTextOCRSimilarity <= 0 ||
		!validUnitScore(thr.MinMaskIoUMean) || thr.MinMaskIoUMean <= 0 {
		return false
	}
	if thr.MaxFrozenFrameStreak < 0 || !validFinite(thr.MinDurationSeconds) || thr.MinDurationSeconds <= 0 ||
		!validFinite(thr.MinFPS) || thr.MinFPS <= 0 {
		return false
	}
	return true
}

func validFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func validUnitScore(v float64) bool {
	return validFinite(v) && v >= 0 && v <= 1
}
