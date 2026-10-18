// Package i2v defines the image-conditioned video adapter seam for HUI-1702.
//
// Evidence at 2026-10-19 (repo @ 57390e6):
//   - internal/videofixture is an in-memory fixture: provider_calls=0,
//     generation_triggered=false, no video bytes, status "真实视频未验证";
//   - internal/showcasevideo states it has no image-to-video client; without a
//     real supplier the submit decision is failed/unknown and never calls;
//   - internal/engwalk only knows ModeTextImage ("text_image");
//   - no image-conditioned video provider/adapter exists anywhere in the repo.
//
// Therefore Resolve/Probe report UNKNOWN + the exact missing seam. Nothing in
// this package dials a provider, submits a task, generates bytes or charges.
// A text-to-video (T2V) model must NOT be used as a substitute: camera moves
// and subject preservation are image-conditioned promises, and h3-fast is only
// evidenced for T2V material elsewhere.
package i2v

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// StatusUnknown is the only honest status while no adapter is registered.
	StatusUnknown = "UNKNOWN"
	// StatusConfigured means an adapter is registered (still not a real-video
	// proof; real integration is verified in HUI-3836).
	StatusConfigured = "configured"

	ModeImageToVideo = "image_to_video"
	ModeTextToVideo  = "text_to_video"
)

var (
	ErrNotImageConditioned = errors.New("i2v: 没有图片条件，不能发起图生视频（不得用 T2V 冒充）")
	ErrAdapterUnavailable  = errors.New("i2v: 没有可用的图生视频适配器")
	ErrInvalidRequest      = errors.New("i2v: 请求不完整")
)

// MissingSeams is the exact adapter seam this ticket still needs.
func MissingSeams() []string {
	return []string{
		"image_conditioned_video_provider",
		"adapter_submit",
		"adapter_poll",
		"adapter_fetch",
		"artifact_hash",
		"subject_consistency_gate",
		"cost_line",
	}
}

// Evidence lists the code facts this UNKNOWN conclusion is based on.
func Evidence() []string {
	return []string{
		"server/internal/videofixture: memory fixture, provider_calls=0, no video bytes",
		"server/internal/showcasevideo: no image-to-video client; failed/unknown without a supplier",
		"server/internal/engwalk: text_image only",
		"no image-conditioned video adapter registered",
	}
}

// Capability is the machine-readable answer for the UI/HTTP layer.
type Capability struct {
	ModelID          string   `json:"model_id"`
	Status           string   `json:"status"`
	ImageConditioned bool     `json:"image_conditioned"`
	AdapterReady     bool     `json:"adapter_ready"`
	RealVideo        bool     `json:"real_video"`
	ProviderCalls    int      `json:"provider_calls"`
	Missing          []string `json:"missing"`
	Evidence         []string `json:"evidence"`
	Notice           string   `json:"notice"`
}

const Notice = "没有可用的图生视频适配器：本能力保持 UNKNOWN，不用文生视频或静图冒充。"

// Probe answers the capability for one model. Today it is always UNKNOWN
// because the repo has no image-conditioned adapter; it never calls out.
func Probe(modelID string) Capability {
	return Capability{
		ModelID:          strings.TrimSpace(modelID),
		Status:           StatusUnknown,
		ImageConditioned: false,
		AdapterReady:     false,
		RealVideo:        false,
		ProviderCalls:    0,
		Missing:          MissingSeams(),
		Evidence:         Evidence(),
		Notice:           Notice,
	}
}

// Request is one image-conditioned video request. ImageRef + ImageSHA256 are
// mandatory: without them this is a T2V request and must be refused.
type Request struct {
	ModelID         string
	Mode            string
	ImageRef        string
	ImageSHA256     string
	Prompt          string
	DurationSeconds int
	AspectRatio     string
}

// JobRef identifies a submitted provider job (once an adapter exists).
type JobRef struct{ ID string }

// State is a poll result. Unknown stays unknown; never coerced to success.
type State struct {
	Status       string
	Progress     int
	FailureClass string
}

// Artifact is a decoded provider result. A hash is required before any
// downstream "real video" claim.
type Artifact struct {
	VideoURL        string
	SHA256          string
	DurationSeconds int
	Width           int
	Height          int
}

// Adapter is the missing seam. A future owner implements:
//   - Submit: image ref + prompt + duration/aspect → provider job;
//   - Poll: honest provider state (unknown is unknown);
//   - Fetch: bytes/URL + sha256 + real duration for decode/consistency checks.
type Adapter interface {
	Submit(ctx context.Context, req Request) (JobRef, error)
	Poll(ctx context.Context, ref JobRef) (State, error)
	Fetch(ctx context.Context, ref JobRef) (Artifact, error)
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidateRequest enforces the image-conditioning contract before any call.
func ValidateRequest(req Request) error {
	if strings.TrimSpace(req.ModelID) == "" {
		return fmt.Errorf("%w: model_id", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Mode) != ModeImageToVideo {
		return ErrNotImageConditioned
	}
	if strings.TrimSpace(req.ImageRef) == "" {
		return ErrNotImageConditioned
	}
	digest := strings.ToLower(strings.TrimSpace(req.ImageSHA256))
	if !sha256Hex.MatchString(digest) {
		return fmt.Errorf("%w: image_sha256", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return fmt.Errorf("%w: prompt", ErrInvalidRequest)
	}
	if req.DurationSeconds <= 0 {
		return fmt.Errorf("%w: duration_seconds", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.AspectRatio) == "" {
		return fmt.Errorf("%w: aspect_ratio", ErrInvalidRequest)
	}
	return nil
}

// Submit validates the request and delegates to a registered adapter. A nil
// adapter is the honest no-supplier case; this function never falls back to
// another mode and never makes an HTTP call itself.
func Submit(ctx context.Context, adapter Adapter, req Request) (JobRef, error) {
	if err := ValidateRequest(req); err != nil {
		return JobRef{}, err
	}
	if adapter == nil {
		return JobRef{}, ErrAdapterUnavailable
	}
	return adapter.Submit(ctx, req)
}
