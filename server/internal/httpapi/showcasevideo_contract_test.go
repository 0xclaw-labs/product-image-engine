package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/config"
	"github.com/bianjiefilm/product-image-engine/server/internal/i2vcontract"
)

func TestShowcaseVideoQualificationHTTPGuardrails(t *testing.T) {
	f := newFixture(t, func(c *config.Config) { c.ShowcaseVideoEnabled = true })
	_, tok := f.loginOK(t)

	// 1. Capabilities endpoint carries honest qualification & execution policy with zero internal token leaks.
	st, capBody := f.do(t, "GET", "/api/v1/showcase-videos/capabilities", tok, nil)
	if st != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%#v", st, capBody)
	}
	if err := i2vcontract.AuditNoInternalTokenLeak(capBody); err != nil {
		t.Fatalf("capabilities leaked internal token: %v", err)
	}
	if capBody["basic_image_path_blocked"] != false {
		t.Fatalf("basic_image_path_blocked must be false: %#v", capBody)
	}
	qualMap, ok := capBody["i2v_qualification"].(map[string]any)
	if !ok || qualMap["eligible"] != false || qualMap["block_code"] != i2vcontract.BlockNoRealI2VAdapter {
		t.Fatalf("unexpected i2v_qualification in capabilities: %#v", capBody["i2v_qualification"])
	}

	// 2. Qualify endpoint rejects T2V masquerading (h3-fast).
	st, body := f.do(t, "POST", "/api/v1/showcase-videos/qualify", tok, map[string]any{
		"model_id":         "h3-fast",
		"provider_id":      "xmodel",
		"mode":             "image_to_video",
		"duration_seconds": 5,
		"aspect_ratio":     "9:16",
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("h3-fast masquerade must be 422, got %d %#v", st, body)
	}
	q, _ := body["qualification"].(map[string]any)
	if q["block_code"] != i2vcontract.BlockT2VMasquerade || body["basic_image_path_blocked"] != false || body["provider_calls"] != float64(0) {
		t.Fatalf("unexpected h3-fast rejection payload: %#v", body)
	}

	// 3. Qualify endpoint rejects single-image true-360 claim.
	st, body = f.do(t, "POST", "/api/v1/showcase-videos/qualify", tok, map[string]any{
		"model_id":            "xmodel-i2v-v1",
		"provider_id":         "xmodel",
		"mode":                "image_to_video",
		"duration_seconds":    5,
		"aspect_ratio":        "9:16",
		"captured_view_count": 1,
		"claim_true_360":      true,
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("single-image true-360 claim must be 422, got %d %#v", st, body)
	}
	q, _ = body["qualification"].(map[string]any)
	if q["block_code"] != i2vcontract.BlockUnprovenTrue360Claim {
		t.Fatalf("expected BLOCKED_UNPROVEN_TRUE_360_CLAIM, got %#v", q)
	}

	// 4. Qualify endpoint rejects arbitrary numeric priority.
	st, body = f.do(t, "POST", "/api/v1/showcase-videos/qualify", tok, map[string]any{
		"model_id":             "xmodel-i2v-v1",
		"execution_preference": "immediate",
		"priority_number":      100,
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("arbitrary priority must be 422, got %d %#v", st, body)
	}
	q, _ = body["qualification"].(map[string]any)
	if q["block_code"] != i2vcontract.BlockArbitraryPriorityForbidden {
		t.Fatalf("expected BLOCKED_ARBITRARY_PRIORITY_FORBIDDEN, got %#v", q)
	}

	// 5. Qualify endpoint blocks free tier targeting internal R&D GPU cards.
	futureExp := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	st, body = f.do(t, "POST", "/api/v1/showcase-videos/qualify", tok, map[string]any{
		"model_id":              "xmodel-i2v-v1",
		"execution_preference":  "idle",
		"payer_type":            "free_grant",
		"free_quota_grant_id":   "fg_1",
		"free_quota_expires_at": futureExp,
		"free_quota_remaining":  3,
		"target_pool_class":     "internal_rd_card",
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("free tier internal R&D card must be 422, got %d %#v", st, body)
	}
	q, _ = body["qualification"].(map[string]any)
	if q["block_code"] != i2vcontract.BlockFreeTierInternalCardForbidden {
		t.Fatalf("expected BLOCKED_FREE_TIER_INTERNAL_RD_CARD_FORBIDDEN, got %#v", q)
	}

	// 6. Qualify endpoint blocks expired free quota without auto-charging.
	pastExp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	st, body = f.do(t, "POST", "/api/v1/showcase-videos/qualify", tok, map[string]any{
		"model_id":              "xmodel-i2v-v1",
		"execution_preference":  "night",
		"payer_type":            "free_grant",
		"free_quota_grant_id":   "fg_1",
		"free_quota_expires_at": pastExp,
		"free_quota_remaining":  1,
		"auto_convert_to_paid":  false,
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("expired free quota must be 422, got %d %#v", st, body)
	}
	q, _ = body["qualification"].(map[string]any)
	if q["block_code"] != i2vcontract.BlockFreeQuotaExpiredNoAutoCharge {
		t.Fatalf("expected BLOCKED_FREE_QUOTA_EXPIRED_NO_AUTO_CHARGE, got %#v", q)
	}

	// 7. Honest no-real-adapter response even if client claims adapter_registered=true.
	st, body = f.do(t, "POST", "/api/v1/showcase-videos/qualify", tok, map[string]any{
		"model_id":                         "xmodel-i2v-v1",
		"provider_id":                      "xmodel",
		"adapter_id":                       "adapter-v1",
		"adapter_registered":               true,
		"mode":                             "image_to_video",
		"image_conditioned":                true,
		"empirical_image_binding_verified": true,
		"verified_evidence_ref":            "client-spoof",
		"duration_seconds":                 5,
		"aspect_ratio":                     "9:16",
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("client cannot spoof server adapter readiness, got %d %#v", st, body)
	}
	q, _ = body["qualification"].(map[string]any)
	if q["block_code"] != i2vcontract.BlockNoRealI2VAdapter || body["basic_image_path_blocked"] != false {
		t.Fatalf("expected BLOCKED_NO_REAL_I2V_ADAPTER and unblocked basic path: %#v", body)
	}
	if err := i2vcontract.AuditNoInternalTokenLeak(body); err != nil {
		t.Fatalf("qualify response leaked internal token: %v", err)
	}
}

func TestShowcaseVideoCreateAndContractCrossTenantIsolation(t *testing.T) {
	f := newFixture(t, func(c *config.Config) { c.ShowcaseVideoEnabled = true })
	_, tokA := f.loginOK(t)
	proj, imageID := f.projectWithInput(t, tokA)

	// Reject arbitrary priority number on create.
	st, _ := f.do(t, "POST", "/api/v1/projects/"+proj+"/showcase-videos", tokA, map[string]any{
		"product_image_id": imageID, "camera_move": "360", "priority_number": 5,
	})
	if st != http.StatusBadRequest {
		t.Fatalf("arbitrary priority on create must be 400, got %d", st)
	}

	// Reject invalid execution preference on create.
	st, _ = f.do(t, "POST", "/api/v1/projects/"+proj+"/showcase-videos", tokA, map[string]any{
		"product_image_id": imageID, "camera_move": "360", "execution_preference": "vip_fast_lane",
	})
	if st != http.StatusBadRequest {
		t.Fatalf("invalid execution_preference must be 400, got %d", st)
	}

	// Reject T2V masquerade on create.
	st, _ = f.do(t, "POST", "/api/v1/projects/"+proj+"/showcase-videos", tokA, map[string]any{
		"product_image_id": imageID, "camera_move": "360", "model_id": "h3-fast",
	})
	if st != http.StatusUnprocessableEntity {
		t.Fatalf("h3-fast masquerade on create must be 422, got %d", st)
	}

	// Reject single-image true 360 claim on create.
	st, _ = f.do(t, "POST", "/api/v1/projects/"+proj+"/showcase-videos", tokA, map[string]any{
		"product_image_id": imageID, "camera_move": "360", "claim_true_360": true,
	})
	if st != http.StatusBadRequest {
		t.Fatalf("single-image true-360 claim on create must be 400, got %d", st)
	}

	// Valid create with night execution preference.
	st, created := f.do(t, "POST", "/api/v1/projects/"+proj+"/showcase-videos", tokA, map[string]any{
		"product_image_id": imageID, "camera_move": "360", "execution_preference": "night",
	})
	if st != http.StatusCreated {
		t.Fatalf("valid create failed: %d %#v", st, created)
	}
	job, _ := created["job"].(map[string]any)
	jobID, _ := job["id"].(string)
	if job["basic_image_path_blocked"] != false {
		t.Fatalf("basic_image_path_blocked must stay false")
	}

	// Fetch contract view for owner tenant.
	st, contractBody := f.do(t, "GET", "/api/v1/projects/"+proj+"/showcase-videos/"+jobID+"/contract", tokA, nil)
	if st != http.StatusOK {
		t.Fatalf("get contract failed: %d %#v", st, contractBody)
	}
	if err := i2vcontract.AuditNoInternalTokenLeak(contractBody); err != nil {
		t.Fatalf("contract response leaked internal token: %v", err)
	}
	viewAssess, _ := contractBody["view_assessment"].(map[string]any)
	if viewAssess["back_view_unproven"] != true || viewAssess["cannot_claim_true_360"] != true {
		t.Fatalf("single-image contract must expose back_view_unproven & cannot_claim_true_360: %#v", viewAssess)
	}
	qProj, _ := contractBody["queue_projection"].(map[string]any)
	if qProj["eta_seconds"] != nil {
		t.Fatalf("unknown ETA must stay null, got %#v", qProj["eta_seconds"])
	}
	candRet, _ := contractBody["candidate_return"].(map[string]any)
	if candRet["destination"] != i2vcontract.CandidateDestinationMerchantLibrary ||
		candRet["auto_published"] != false || candRet["reward_triggered"] != false {
		t.Fatalf("candidate return must stay merchant-only with no auto publish/reward: %#v", candRet)
	}

	// Cross-tenant access to contract endpoint must be rejected (404).
	stLoginB, loginB := f.login(t, "other-tenant@x.com", "right-pass")
	if stLoginB != http.StatusOK {
		t.Fatalf("login tenant B failed: %d %#v", stLoginB, loginB)
	}
	tokB, _ := loginB["access_token"].(string)
	stCross, _ := f.do(t, "GET", "/api/v1/projects/"+proj+"/showcase-videos/"+jobID+"/contract", tokB, nil)
	if stCross != http.StatusNotFound {
		t.Fatalf("cross-tenant contract read must return 404, got %d", stCross)
	}
}
