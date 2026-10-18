package httpapi

// i2v_capability_test.go: HUI-1702 MX-OPS 证据 — 能力接口必须如实申报 UNKNOWN，
// 不宣称 image conditioning/真实视频，不产生 provider 调用。

import (
	"net/http"
	"testing"

	"github.com/bianjiefilm/product-image-engine/server/internal/config"
)

func TestShowcaseVideoCapabilityReportsI2VUnknown(t *testing.T) {
	f := newFixture(t, func(c *config.Config) { c.ShowcaseVideoEnabled = true })
	_, tok := f.loginOK(t)
	status, body := f.do(t, "GET", "/api/v1/showcase-videos/capabilities", tok, nil)
	if status != http.StatusOK {
		t.Fatalf("capabilities status = %d", status)
	}
	capability, ok := body["i2v_capability"].(map[string]any)
	if !ok {
		t.Fatalf("i2v_capability missing: %#v", body)
	}
	if capability["status"] != "UNKNOWN" {
		t.Fatalf("status = %v, want UNKNOWN", capability["status"])
	}
	if capability["image_conditioned"] != false || capability["adapter_ready"] != false {
		t.Fatalf("must not claim image conditioning before an adapter exists: %#v", capability)
	}
	if capability["real_video"] != false || capability["provider_calls"] != float64(0) {
		t.Fatalf("no provider calls/real video may be claimed: %#v", capability)
	}
	missing, _ := capability["missing"].([]any)
	if len(missing) == 0 {
		t.Fatalf("missing adapter seam must be enumerated")
	}
	if body["show_video"] != false || body["play_still"] != false || body["video_url"] != "" {
		t.Fatalf("capability endpoint must stay honest about no video")
	}
}
