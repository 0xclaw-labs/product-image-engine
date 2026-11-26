package crossappreturn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/appregistry"
	"github.com/bianjiefilm/product-image-engine/server/internal/receiptdoc"
)

const testReceiptSecret = "hui-1745-test-receipt-secret"

type testReceiver struct {
	srv            *httptest.Server
	mu             sync.Mutex
	received       []CandidateReturnPayload
	rawBodies      [][]byte
	headers        []http.Header
	calls          atomic.Int64
	failNextStatus atomic.Int32
	failNextBody   string
	delay          time.Duration
}

func newTestReceiver(t *testing.T, expectedSourceApp string) *testReceiver {
	t.Helper()
	tr := &testReceiver{}
	tr.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tr.delay > 0 {
			time.Sleep(tr.delay)
		}
		tr.calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if code := tr.failNextStatus.Swap(0); code != 0 {
			w.WriteHeader(int(code))
			_, _ = w.Write([]byte(tr.failNextBody))
			return
		}
		var payload CandidateReturnPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// Verify §8 HMAC frame signature using the exact receiptdoc contract.
		if err := receiptdoc.FrameVerify(
			[]byte(testReceiptSecret),
			r.Header.Get(receiptdoc.HeaderTimestamp),
			r.Header.Get(receiptdoc.HeaderSignature),
			expectedSourceApp,
			payload.TargetApp,
			payload.ReceiptID,
			raw,
			time.Now().UTC(),
		); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_signature"}`))
			return
		}
		tr.mu.Lock()
		tr.received = append(tr.received, payload)
		tr.rawBodies = append(tr.rawBodies, append([]byte(nil), raw...))
		tr.headers = append(tr.headers, r.Header.Clone())
		tr.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"candidate_id":"cand_%s","status":"accepted_candidate"}`, payload.OutputID)
	}))
	t.Cleanup(tr.srv.Close)
	return tr
}

func (tr *testReceiver) count() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return len(tr.received)
}

func mustManifest(t *testing.T, ordersURL, touchURL string) *appregistry.Manifest {
	t.Helper()
	touchTargets := "[]"
	if touchURL != "" {
		touchTargets = fmt.Sprintf(`[{"target_id":"rc-touch-content-pack","kind":"receipt","url":%q}]`, touchURL)
	}
	ordersTargets := "[]"
	if ordersURL != "" {
		ordersTargets = fmt.Sprintf(`[{"target_id":"rc-orders-main","kind":"receipt","url":%q}]`, ordersURL)
	}
	raw := fmt.Sprintf(`{
		"manifest_version": "app-registry/v1",
		"apps": [
			{
				"app_id": "orders",
				"display_name": "观澜接单",
				"enabled": true,
				"supported_source_kinds": ["order"],
				"capabilities": [{"name": "order.handoff", "menu_visible": true, "requires_billing": false}],
				"launch_targets": [{"target_id": "ti-orders-web", "kind": "launch", "url": "https://orders.example.invalid/launch"}],
				"receipt_targets": %s
			},
			{
				"app_id": "touch-engine",
				"display_name": "碰一碰内容包",
				"enabled": true,
				"supported_source_kinds": ["campaign", "standalone"],
				"capabilities": [{"name": "content.pack", "menu_visible": true, "requires_billing": false}],
				"launch_targets": [{"target_id": "ti-touch-web", "kind": "launch", "url": "https://touch.example.invalid/launch"}],
				"receipt_targets": %s
			}
		]
	}`, ordersTargets, touchTargets)
	m, err := appregistry.LoadManifest([]byte(raw))
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	return m
}

func deliveryOpts(reg TargetResolver) DeliveryOptions {
	return DeliveryOptions{
		SourceApp: "product-image",
		Registry:  reg,
		ReceiptSecretForApp: func(appID string) string {
			if appID == "orders" || appID == "touch-engine" {
				return testReceiptSecret
			}
			return ""
		},
		BearerTokenForApp: func(appID string) string {
			if appID == "orders" {
				return "order-peer-bearer"
			}
			return ""
		},
	}
}

func baseCampaignReturnReq(outputID, assetVersion, sourceVersion string, seq int64) CandidateReturnRequest {
	return CandidateReturnRequest{
		TenantID:       "tenant_shop_1",
		BrandID:        "brand_tea_01",
		SourceKind:     SourceCampaign,
		CampaignID:     "camp_2026_1126",
		ProjectID:      "proj_orig_001",
		TaskID:         "task_orig_001",
		OutputID:       outputID,
		AssetRef:       "asset_" + outputID,
		AssetVersion:   assetVersion,
		AssetSHA256:    strings.Repeat("a", 64),
		AssetSizeBytes: 4096,
		MediaType:      "image/png",
		UsageScope:     "touch_content_pack",
		Permission: PermissionGrant{
			GrantID:          "grant_camp_001",
			Status:           PermissionActive,
			UsageScope:       "touch_content_pack",
			Scopes:           []string{"project.resume", "asset.import", "receipt.write"},
			PayerPrincipalID: "principal_merchant_1",
		},
		SourceVersion:    sourceVersion,
		SourceVersionSeq: seq,
		TargetApp:        "touch-engine",
		TargetID:         "rc-touch-content-pack",
		DestinationKind:  DestinationTouchContentPack,
	}
}

func TestContinueProjectNativeWithoutRebuildingAndGuards(t *testing.T) {
	c := NewCoordinator()
	now := time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)
	c.SetClock(func() time.Time { return now })

	// 1. Order-bound native continuation resumes original project_id/task_id without rebuilding.
	ordReq := ContinuationRequest{
		TenantID:           "tenant_1",
		BrandID:            "brand_1",
		SourceKind:         SourceOrder,
		SourceApp:          "orders",
		OrderID:            "ord_1001",
		ProjectID:          "proj_ord_1",
		TaskID:             "task_ord_1",
		PinnedAssetRef:     "asset_1",
		PinnedAssetVersion: "ver_1",
		UsageScope:         "order_delivery",
		Permission:         PermissionGrant{GrantID: "g_ord_1", Status: PermissionActive, UsageScope: "order_delivery", Scopes: []string{"project.resume"}},
		SourceVersion:      "v1",
		SourceVersionSeq:   1,
		ActorRole:          RoleMerchant,
		EntryTrigger:       TriggerMerchantContinue,
	}
	rec1, err := c.ContinueProject(ordReq)
	if err != nil {
		t.Fatalf("order continuation failed: %v", err)
	}
	if rec1.ProjectID != "proj_ord_1" || rec1.TaskID != "task_ord_1" || rec1.SideEffects.ProjectRebuilt || rec1.SideEffects.GenerationTriggered {
		t.Fatalf("unexpected order continuation record: %+v", rec1)
	}

	// Bot/notification return continues the EXACT same project without creating a second project.
	botReq := ordReq
	botReq.SourceKind = SourceBot
	botReq.EntryTrigger = TriggerBotReturn
	botReq.ActorRole = RoleBotService
	botReq.RequestedAssetVersion = "ver_1"
	rec2, err := c.ContinueProject(botReq)
	if err != nil {
		t.Fatalf("bot continuation failed: %v", err)
	}
	if rec2.ContinuationID != rec1.ContinuationID || rec2.ProjectID != "proj_ord_1" || rec2.ContinuedCount != 2 || rec2.SideEffects.ProjectRebuilt {
		t.Fatalf("bot continuation must resume original project: %+v", rec2)
	}

	// 2. Campaign/no-order continuation succeeds without order_id.
	campReq := ContinuationRequest{
		TenantID:           "tenant_1",
		BrandID:            "brand_1",
		SourceKind:         SourceCampaign,
		SourceApp:          "touch-engine",
		CampaignID:         "camp_99",
		ProjectID:          "proj_camp_99",
		TaskID:             "task_camp_99",
		PinnedAssetRef:     "asset_camp_1",
		PinnedAssetVersion: "ver_camp_1",
		UsageScope:         "touch_content_pack",
		Permission:         PermissionGrant{GrantID: "g_camp_1", Status: PermissionActive, UsageScope: "touch_content_pack", Scopes: []string{"project.resume"}},
		SourceVersion:      "v1",
		SourceVersionSeq:   1,
		SignedAccessURL:    "https://cdn.example.invalid/assets/asset_camp_1?exp=1",
		SignedURLExpiresAt: now.Add(5 * time.Minute).Format(time.RFC3339),
	}
	campRec, err := c.ContinueProject(campReq)
	if err != nil {
		t.Fatalf("campaign continuation failed: %v", err)
	}
	if campRec.OrderID != "" || campRec.CampaignID != "camp_99" || campRec.ProjectID != "proj_camp_99" {
		t.Fatalf("unexpected campaign continuation record: %+v", campRec)
	}

	// 3. Negative: campaign forging order_id to bypass auth -> 400 fake_order_forbidden.
	fakeOrd := campReq
	fakeOrd.OrderID = "fake_ord_999"
	_, err = c.ContinueProject(fakeOrd)
	ce, ok := AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusBadRequest || ce.Code != CodeFakeOrderForbidden {
		t.Fatalf("expected 400 fake_order_forbidden, got %v", err)
	}

	// 4. Negative: silently swapping asset_version with "latest" -> 409 latest_version_swap_forbidden.
	swapReq := botReq
	swapReq.RequestedAssetVersion = "latest"
	_, err = c.ContinueProject(swapReq)
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusConflict || ce.Code != CodeLatestSwapForbidden {
		t.Fatalf("expected 409 latest_version_swap_forbidden, got %v", err)
	}

	// 5. Negative: cross-tenant continuation of existing project -> 403 tenant_mismatch.
	crossTenant := ordReq
	crossTenant.TenantID = "tenant_other"
	_, err = c.ContinueProject(crossTenant)
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusForbidden || ce.Code != CodeTenantMismatch {
		t.Fatalf("expected 403 tenant_mismatch, got %v", err)
	}

	// 6. Negative: visitor touch/view NEVER initiates generation or continuation mutation.
	for _, tc := range []struct {
		role, trigger string
	}{
		{RoleVisitor, TriggerVisitorTouch},
		{RoleMerchant, TriggerVisitorTouch},
		{"", TriggerVisitorView},
	} {
		vReq := campReq
		vReq.ActorRole = tc.role
		vReq.EntryTrigger = tc.trigger
		vReq.RequestGeneration = true
		_, err = c.ContinueProject(vReq)
		ce, ok = AsContractError(err)
		if !ok || ce.HTTPStatus != http.StatusForbidden || ce.Code != CodeVisitorGenerationBlocked || ce.SideEffects.GenerationTriggered {
			t.Fatalf("visitor (%s/%s) must be blocked with 403 visitor_generation_forbidden, got %v", tc.role, tc.trigger, err)
		}
	}

	// 7. Negative: expired signed URL -> 410 url_expired with recovery path.
	expReq := campReq
	expReq.SignedURLExpiresAt = now.Format(time.RFC3339) // boundary equal = expired
	_, err = c.ContinueProject(expReq)
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusGone || ce.Code != CodeURLExpired || ce.RecoveryAction != RecoveryRefreshSignedURL {
		t.Fatalf("expected 410 url_expired, got %v", err)
	}

	// 8. Negative: revoked permission -> 403 revoked with recovery path.
	c.RevokeGrant("tenant_1", "g_camp_1")
	_, err = c.ContinueProject(campReq)
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusForbidden || ce.Code != CodeRevoked || ce.RecoveryAction != RecoveryReauthorizeGrant {
		t.Fatalf("expected 403 revoked, got %v", err)
	}
}

func TestReturnCandidateRegisteredTargetsAndUnregistered409(t *testing.T) {
	ctx := context.Background()
	touchRx := newTestReceiver(t, "product-image")
	orderRx := newTestReceiver(t, "product-image")
	c := NewCoordinator()

	// 1. Unregistered target (default manifest where touch-engine has receipt_targets=[]) -> explicit 409 target_not_registered, NEVER fake success.
	defaultMan, err := appregistry.DefaultManifest()
	if err != nil {
		t.Fatalf("default manifest: %v", err)
	}
	reqUnreg := baseCampaignReturnReq("out_1", "ver_1", "v1", 1)
	reqUnreg.TargetID = "" // lookup first receipt target for touch-engine (empty in default manifest)
	_, err = c.ReturnCandidate(ctx, reqUnreg, deliveryOpts(defaultMan))
	ce, ok := AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusConflict || ce.Code != CodeTargetNotRegistered || !errors.Is(err, ErrTargetNotRegistered) {
		t.Fatalf("expected 409 target_not_registered for default manifest touch-engine, got %v", err)
	}
	if touchRx.calls.Load() != 0 {
		t.Fatalf("unregistered target must make zero HTTP calls, got %d", touchRx.calls.Load())
	}

	// Explicit unknown target_id must also return 409 target_not_registered.
	regMan := mustManifest(t, orderRx.srv.URL+"/orders/receipt", touchRx.srv.URL+"/touch/content-pack")
	reqBadTarget := baseCampaignReturnReq("out_1", "ver_1", "v1", 1)
	reqBadTarget.TargetID = "rc-unregistered-spoof"
	_, err = c.ReturnCandidate(ctx, reqBadTarget, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusConflict || ce.Code != CodeTargetNotRegistered {
		t.Fatalf("expected 409 target_not_registered for unknown target_id, got %v", err)
	}

	// 2. Registered Touch HUI-3553 receiver succeeds with full immutable payload & zero plaintext image.
	reqCamp := baseCampaignReturnReq("out_camp_1", "ver_camp_1", "v1", 1)
	out, err := c.ReturnCandidate(ctx, reqCamp, deliveryOpts(regMan))
	if err != nil {
		t.Fatalf("registered touch return failed: %v", err)
	}
	if out.Duplicate || out.Record.Status != StatusDelivered || out.Record.ReceiverAckID != "cand_out_camp_1" {
		t.Fatalf("unexpected return outcome: %+v", out)
	}
	if out.SideEffects != ZeroSideEffects() {
		t.Fatalf("expected zero side effects, got %+v", out.SideEffects)
	}
	if touchRx.count() != 1 {
		t.Fatalf("expected 1 delivery to touch receiver, got %d", touchRx.count())
	}
	got := touchRx.received[0]
	if got.TenantID != "tenant_shop_1" || got.BrandID != "brand_tea_01" || got.CampaignID != "camp_2026_1126" ||
		got.OrderID != "" || got.ProjectID != "proj_orig_001" || got.TaskID != "task_orig_001" ||
		got.OutputID != "out_camp_1" || got.AssetVersion != "ver_camp_1" || got.UsageScope != "touch_content_pack" ||
		got.Permission.GrantID != "grant_camp_001" || got.SourceVersion != "v1" || got.ReceiptID == "" {
		t.Fatalf("delivered payload missing required fields: %+v", got)
	}
	if got.ManualReuploadRequired || got.AutoPublished || got.AutoCouponIssued || got.AutoBilled || got.LandingMode != LandingModeCandidateOnly {
		t.Fatalf("delivered payload violated candidate-only invariants: %+v", got)
	}
	if ce := AuditRawEventBytes(touchRx.rawBodies[0]); ce != nil {
		t.Fatalf("delivered raw payload failed security audit: %v", ce)
	}

	// 3. Duplicate delivery is strictly idempotent (same receipt_id, zero extra HTTP call, zero side effects).
	dupOut, err := c.ReturnCandidate(ctx, reqCamp, deliveryOpts(regMan))
	if err != nil {
		t.Fatalf("duplicate return failed: %v", err)
	}
	if !dupOut.Duplicate || dupOut.Record.ReceiptID != out.Record.ReceiptID || touchRx.calls.Load() != 1 {
		t.Fatalf("duplicate delivery must be idempotent without second HTTP call: dup=%v calls=%d", dupOut.Duplicate, touchRx.calls.Load())
	}

	// 4. Order receiver also receives order_id + Bearer token + §8 HMAC frame.
	ordReq := CandidateReturnRequest{
		TenantID:       "tenant_shop_1",
		BrandID:        "brand_tea_01",
		SourceKind:     SourceOrder,
		OrderID:        "ord_2026_88",
		ProjectID:      "proj_ord_88",
		TaskID:         "task_ord_88",
		OutputID:       "out_ord_88",
		AssetRef:       "asset_ord_88",
		AssetVersion:   "ver_ord_1",
		AssetSHA256:    strings.Repeat("b", 64),
		AssetSizeBytes: 2048,
		MediaType:      "image/png",
		UsageScope:     "order_delivery",
		Permission:     PermissionGrant{GrantID: "grant_ord_88", Status: PermissionActive, UsageScope: "order_delivery", Scopes: []string{"receipt.write"}},
		SourceVersion:  "v1",
		TargetApp:      "orders",
	}
	ordOut, err := c.ReturnCandidate(ctx, ordReq, deliveryOpts(regMan))
	if err != nil {
		t.Fatalf("order return failed: %v", err)
	}
	if ordOut.Record.Status != StatusDelivered || orderRx.count() != 1 {
		t.Fatalf("order receiver did not record delivery: %+v count=%d", ordOut, orderRx.count())
	}
	if h := orderRx.headers[0].Get("Authorization"); h != "Bearer order-peer-bearer" {
		t.Fatalf("expected order bearer header, got %q", h)
	}
	if orderRx.received[0].OrderID != "ord_2026_88" || orderRx.received[0].CampaignID != "" {
		t.Fatalf("unexpected order payload: %+v", orderRx.received[0])
	}
}

func TestStaleSourceVersionNeverOverwritesNewerAdoptedVersion(t *testing.T) {
	ctx := context.Background()
	touchRx := newTestReceiver(t, "product-image")
	regMan := mustManifest(t, "", touchRx.srv.URL+"/touch/content-pack")
	c := NewCoordinator()

	// Deliver v2 and explicitly adopt it.
	v2Req := baseCampaignReturnReq("out_v2", "ver_v2", "v2", 2)
	v2Out, err := c.ReturnCandidate(ctx, v2Req, deliveryOpts(regMan))
	if err != nil {
		t.Fatalf("v2 return failed: %v", err)
	}
	adoptOut, err := c.AdoptCandidate(AdoptRequest{
		TenantID:  "tenant_shop_1",
		ProjectID: "proj_orig_001",
		OutputID:  "out_v2",
		ReceiptID: v2Out.Record.ReceiptID,
		AdoptedBy: "merchant_owner_1",
	})
	if err != nil {
		t.Fatalf("adopt v2 failed: %v", err)
	}
	if !adoptOut.Record.Adopted || adoptOut.Adopted.SourceVersionSeq != 2 {
		t.Fatalf("unexpected adopt result: %+v", adoptOut)
	}

	// Late-arriving v1 MUST be rejected with 409 stale_source_version and NEVER overwrite v2.
	v1Late := baseCampaignReturnReq("out_v1_late", "ver_v1", "v1", 1)
	_, err = c.ReturnCandidate(ctx, v1Late, deliveryOpts(regMan))
	ce, ok := AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusConflict || ce.Code != CodeStaleSourceVersion {
		t.Fatalf("expected 409 stale_source_version for late v1, got %v", err)
	}
	if touchRx.calls.Load() != 1 {
		t.Fatalf("late stale v1 must not deliver to receiver, calls=%d", touchRx.calls.Load())
	}

	adopted, ok := c.GetAdoptedVersion("tenant_shop_1", "touch-engine", "campaign:camp_2026_1126")
	if !ok || adopted.OutputID != "out_v2" || adopted.AssetVersion != "ver_v2" || adopted.SourceVersionSeq != 2 {
		t.Fatalf("adopted version must remain v2: %+v", adopted)
	}
}

func TestPlaintextImageRevokedExpiredAndRetryIdempotency(t *testing.T) {
	ctx := context.Background()
	touchRx := newTestReceiver(t, "product-image")
	regMan := mustManifest(t, "", touchRx.srv.URL+"/touch/content-pack")
	c := NewCoordinator()
	now := time.Now().UTC().Truncate(time.Second)
	c.SetClock(func() time.Time { return now })

	// 1. Raw image plaintext is rejected fail-closed.
	plainReq := baseCampaignReturnReq("out_plain", "ver_1", "v1", 1)
	plainReq.RawImagePayload = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAAB"
	_, err := c.ReturnCandidate(ctx, plainReq, deliveryOpts(regMan))
	ce, ok := AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusBadRequest || ce.Code != CodePlaintextImageForbidden {
		t.Fatalf("expected 400 plaintext_image_forbidden, got %v", err)
	}

	// 2. Transient 500 failure -> immutable receipt stored -> retry delivers exact same receipt_id & payload.
	touchRx.failNextStatus.Store(http.StatusInternalServerError)
	touchRx.failNextBody = `{"error":"temporary_unavailable"}`

	req := baseCampaignReturnReq("out_retry_1", "ver_r1", "v1", 1)
	failedOut, err := c.ReturnCandidate(ctx, req, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusBadGateway || ce.Code != CodeDeliveryFailed {
		t.Fatalf("expected 502 receipt_delivery_failed on transient receiver 500, got %v", err)
	}
	if failedOut.Record.Status != StatusDeliveryFailed || failedOut.Record.ReceiptID == "" || failedOut.Record.Attempts != 1 {
		t.Fatalf("expected failed delivery record with receipt_id preserved, got %+v", failedOut.Record)
	}

	// Retry with expired signed URL must fail closed (410 url_expired) without calling receiver!
	_, err = c.RetryDelivery(ctx, RetryRequest{
		TenantID:           req.TenantID,
		ReceiptID:          failedOut.Record.ReceiptID,
		SignedAccessURL:    "https://cdn.example.invalid/a.png",
		SignedURLExpiresAt: now.Add(-1 * time.Second).Format(time.RFC3339),
	}, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusGone || ce.Code != CodeURLExpired || ce.RecoveryAction != RecoveryRefreshSignedURL {
		t.Fatalf("expected 410 url_expired on retry with expired URL, got %v", err)
	}
	if touchRx.calls.Load() != 1 {
		t.Fatalf("expired URL retry must not call receiver, calls=%d", touchRx.calls.Load())
	}

	// Retry succeeds with refreshed valid URL; same receipt_id, attempt=2, zero side effects.
	retryOut, err := c.RetryDelivery(ctx, RetryRequest{
		TenantID:           req.TenantID,
		ReceiptID:          failedOut.Record.ReceiptID,
		SignedAccessURL:    "https://cdn.example.invalid/a.png",
		SignedURLExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339),
	}, deliveryOpts(regMan))
	if err != nil {
		t.Fatalf("retry delivery failed: %v", err)
	}
	if !retryOut.Retried || retryOut.Record.ReceiptID != failedOut.Record.ReceiptID || retryOut.Record.Status != StatusDelivered || retryOut.Record.Attempts != 2 {
		t.Fatalf("unexpected retry outcome: %+v", retryOut)
	}
	if touchRx.headers[0].Get(receiptdoc.HeaderAttempt) != "2" {
		t.Fatalf("expected attempt header 2, got %q", touchRx.headers[0].Get(receiptdoc.HeaderAttempt))
	}

	// Retrying an already-delivered receipt is idempotent no-op (zero extra HTTP call).
	noopOut, err := c.RetryDelivery(ctx, RetryRequest{
		TenantID:  req.TenantID,
		ReceiptID: failedOut.Record.ReceiptID,
	}, deliveryOpts(regMan))
	if err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if !noopOut.Duplicate || noopOut.Retried || touchRx.calls.Load() != 2 {
		t.Fatalf("already-delivered retry must be no-op: out=%+v calls=%d", noopOut, touchRx.calls.Load())
	}

	// 3. Revocation blocks retry of another failed receipt fail-closed.
	touchRx.failNextStatus.Store(http.StatusServiceUnavailable)
	touchRx.failNextBody = `{"error":"busy"}`
	reqRev := baseCampaignReturnReq("out_rev_1", "ver_rev_1", "v1", 1)
	reqRev.CampaignID = "camp_rev_99"
	failedRev, err := c.ReturnCandidate(ctx, reqRev, deliveryOpts(regMan))
	if err == nil {
		t.Fatal("expected 502 failure")
	}
	c.RevokeAsset(reqRev.TenantID, reqRev.AssetRef)
	_, err = c.RetryDelivery(ctx, RetryRequest{
		TenantID:  reqRev.TenantID,
		ReceiptID: failedRev.Record.ReceiptID,
	}, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusForbidden || ce.Code != CodeRevoked || ce.RecoveryAction != RecoveryReauthorizeGrant {
		t.Fatalf("expected 403 revoked on retry after revocation, got %v", err)
	}
}

func TestConcurrentDuplicateCandidateReturnCoalescesUnderRace(t *testing.T) {
	ctx := context.Background()
	touchRx := newTestReceiver(t, "product-image")
	touchRx.delay = 25 * time.Millisecond
	regMan := mustManifest(t, "", touchRx.srv.URL+"/touch/content-pack")
	c := NewCoordinator()

	req := baseCampaignReturnReq("out_race_1", "ver_race_1", "v1", 1)
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	receiptIDs := make(chan string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.ReturnCandidate(ctx, req, deliveryOpts(regMan))
			if err != nil {
				errs <- err
				return
			}
			receiptIDs <- res.Record.ReceiptID
		}()
	}
	wg.Wait()
	close(errs)
	close(receiptIDs)

	for err := range errs {
		t.Fatalf("concurrent return error: %v", err)
	}
	var firstID string
	for id := range receiptIDs {
		if firstID == "" {
			firstID = id
		} else if id != firstID {
			t.Fatalf("concurrent callers got divergent receipt_ids: %s vs %s", firstID, id)
		}
	}
	if touchRx.calls.Load() != 1 {
		t.Fatalf("20 concurrent identical candidate returns must coalesce to exactly 1 HTTP delivery, got %d", touchRx.calls.Load())
	}
}

func TestReceiverRejectionsAndPayloadConflictAndStaleRetryBlocked(t *testing.T) {
	ctx := context.Background()
	touchRx := newTestReceiver(t, "product-image")
	regMan := mustManifest(t, "", touchRx.srv.URL+"/touch/content-pack")
	c := NewCoordinator()

	// 1. Secret/internal path audit rejects /var/lib/ and signature=.
	badReq := baseCampaignReturnReq("out_sec", "ver_1", "v1", 1)
	badReq.AssetRef = "/var/lib/product-image/secret.png"
	_, err := c.ReturnCandidate(ctx, badReq, deliveryOpts(regMan))
	ce, ok := AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusBadRequest || ce.Code != CodeSecretLeakForbidden {
		t.Fatalf("expected 400 internal_secret_leak_forbidden, got %v", err)
	}

	// 2. Receiver 403 revoked -> 403 revoked with recovery action.
	touchRx.failNextStatus.Store(http.StatusForbidden)
	touchRx.failNextBody = `{"error":"asset_revoked"}`
	reqRev := baseCampaignReturnReq("out_rx_rev", "ver_1", "v1", 1)
	reqRev.CampaignID = "camp_rx_rev"
	_, err = c.ReturnCandidate(ctx, reqRev, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusForbidden || ce.Code != CodeRevoked || ce.RecoveryAction != RecoveryReauthorizeGrant {
		t.Fatalf("expected 403 revoked from receiver, got %v", err)
	}

	// 3. Receiver 410 url_expired -> 410 url_expired with recovery action.
	touchRx.failNextStatus.Store(http.StatusGone)
	touchRx.failNextBody = `{"error":"url_expired"}`
	reqExp := baseCampaignReturnReq("out_rx_exp", "ver_1", "v1", 1)
	reqExp.CampaignID = "camp_rx_exp"
	_, err = c.ReturnCandidate(ctx, reqExp, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusGone || ce.Code != CodeURLExpired || ce.RecoveryAction != RecoveryRefreshSignedURL {
		t.Fatalf("expected 410 url_expired from receiver, got %v", err)
	}

	// 4. Conflicting payload on same explicit receipt_id -> 409 receipt_payload_conflict (original preserved).
	reqA := baseCampaignReturnReq("out_conf_a", "ver_a", "v1", 1)
	reqA.CampaignID = "camp_conf"
	reqA.ReceiptID = "rcpt_explicit_001"
	resA, err := c.ReturnCandidate(ctx, reqA, deliveryOpts(regMan))
	if err != nil || resA.Record.Status != StatusDelivered {
		t.Fatalf("initial explicit receipt failed: %v", err)
	}
	reqB := baseCampaignReturnReq("out_conf_b", "ver_b", "v1", 1)
	reqB.CampaignID = "camp_conf"
	reqB.ReceiptID = "rcpt_explicit_001"
	_, err = c.ReturnCandidate(ctx, reqB, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusConflict || ce.Code != CodeReceiptPayloadConflict {
		t.Fatalf("expected 409 receipt_payload_conflict, got %v", err)
	}

	// 5. Failed v1 receipt cannot be retried after v2 is delivered and adopted.
	touchRx.failNextStatus.Store(http.StatusInternalServerError)
	touchRx.failNextBody = `{"error":"temporary"}`
	reqV1Fail := baseCampaignReturnReq("out_sr_v1", "ver_v1", "v1", 1)
	reqV1Fail.CampaignID = "camp_stale_retry"
	failV1, err := c.ReturnCandidate(ctx, reqV1Fail, deliveryOpts(regMan))
	if err == nil || failV1.Record.ReceiptID == "" {
		t.Fatalf("expected failed v1 record, got err=%v rec=%+v", err, failV1.Record)
	}

	reqV2OK := baseCampaignReturnReq("out_sr_v2", "ver_v2", "v2", 2)
	reqV2OK.CampaignID = "camp_stale_retry"
	okV2, err := c.ReturnCandidate(ctx, reqV2OK, deliveryOpts(regMan))
	if err != nil {
		t.Fatalf("v2 delivery failed: %v", err)
	}
	if _, err := c.AdoptCandidate(AdoptRequest{
		TenantID:  reqV2OK.TenantID,
		ProjectID: reqV2OK.ProjectID,
		OutputID:  reqV2OK.OutputID,
		ReceiptID: okV2.Record.ReceiptID,
		AdoptedBy: "merchant_1",
	}); err != nil {
		t.Fatalf("adopt v2 failed: %v", err)
	}

	_, err = c.RetryDelivery(ctx, RetryRequest{
		TenantID:  reqV1Fail.TenantID,
		ReceiptID: failV1.Record.ReceiptID,
	}, deliveryOpts(regMan))
	ce, ok = AsContractError(err)
	if !ok || ce.HTTPStatus != http.StatusConflict || ce.Code != CodeStaleSourceVersion {
		t.Fatalf("retrying stale v1 after v2 adopted must fail 409 stale_source_version, got %v", err)
	}
}
