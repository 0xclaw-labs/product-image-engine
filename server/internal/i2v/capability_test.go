package i2v

// capability_test.go: HUI-1702 MX-OPS 证据测试 — 没有 image-conditioned video
// adapter 时能力必须是 UNKNOWN，且绝不以 T2V 冒充、绝不拨号/生成。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProbeReturnsUnknownWithoutImageConditionedAdapter(t *testing.T) {
	cap := Probe("h3-fast")
	if cap.Status != StatusUnknown {
		t.Fatalf("status = %q, want UNKNOWN", cap.Status)
	}
	if cap.ImageConditioned {
		t.Fatalf("must not claim image conditioning without an adapter")
	}
	if cap.AdapterReady || cap.RealVideo || cap.ProviderCalls != 0 {
		t.Fatalf("no adapter => adapterReady=false realVideo=false providerCalls=0, got %+v", cap)
	}
	for _, want := range []string{
		"image_conditioned_video_provider",
		"adapter_submit",
		"adapter_poll",
		"adapter_fetch",
		"artifact_hash",
		"subject_consistency_gate",
		"cost_line",
	} {
		if !contains(cap.Missing, want) {
			t.Fatalf("missing seam %q not reported: %v", want, cap.Missing)
		}
	}
	if len(cap.Evidence) == 0 {
		t.Fatalf("capability must carry the code evidence it is based on")
	}
}

func TestSubmitRejectsMissingImageAndMissingAdapter(t *testing.T) {
	if _, err := Submit(context.Background(), nil, Request{ModelID: "h3-fast"}); !errors.Is(err, ErrNotImageConditioned) {
		t.Fatalf("text-only request must fail with ErrNotImageConditioned, got %v", err)
	}
	req := validRequest()
	if _, err := Submit(context.Background(), nil, req); !errors.Is(err, ErrAdapterUnavailable) {
		t.Fatalf("no adapter must fail with ErrAdapterUnavailable, got %v", err)
	}
}

func TestValidateRequestChecksImageConditioningContract(t *testing.T) {
	req := validRequest()
	for name, mutate := range map[string]func(*Request){
		"empty image":    func(r *Request) { r.ImageRef = "" },
		"bad sha":        func(r *Request) { r.ImageSHA256 = "xyz" },
		"empty model":    func(r *Request) { r.ModelID = "" },
		"bad duration":   func(r *Request) { r.DurationSeconds = 0 },
		"empty aspect":   func(r *Request) { r.AspectRatio = "" },
		"empty prompt":   func(r *Request) { r.Prompt = "" },
		"text only mode": func(r *Request) { r.Mode = ModeTextToVideo },
	} {
		t.Run(name, func(t *testing.T) {
			bad := req
			mutate(&bad)
			if err := ValidateRequest(bad); err == nil {
				t.Fatalf("expected rejection")
			}
		})
	}
	if err := ValidateRequest(validRequest()); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
}

func TestFakeAdapterOnlyProvesTheSeamNotRealVideo(t *testing.T) {
	fake := &fakeAdapter{}
	ref, err := Submit(context.Background(), fake, validRequest())
	if err != nil {
		t.Fatalf("submit with adapter: %v", err)
	}
	if ref.ID == "" {
		t.Fatalf("adapter seam must return a job ref")
	}
	if fake.submits != 1 {
		t.Fatalf("fake adapter submits = %d", fake.submits)
	}
	if Probe("").RealVideo {
		t.Fatalf("probe never claims a real video")
	}
}

func validRequest() Request {
	return Request{
		ModelID:         "h3-fast",
		Mode:            ModeImageToVideo,
		ImageRef:        "las_1",
		ImageSHA256:     strings.Repeat("a", 64),
		Prompt:          "360 度展示",
		DurationSeconds: 5,
		AspectRatio:     "9:16",
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

type fakeAdapter struct{ submits int }

func (f *fakeAdapter) Submit(_ context.Context, _ Request) (JobRef, error) {
	f.submits++
	return JobRef{ID: "fake_job_1"}, nil
}

func (f *fakeAdapter) Poll(_ context.Context, _ JobRef) (State, error) {
	return State{Status: "unknown"}, nil
}

func (f *fakeAdapter) Fetch(_ context.Context, _ JobRef) (Artifact, error) {
	return Artifact{}, ErrAdapterUnavailable
}
