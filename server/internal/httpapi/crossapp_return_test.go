package httpapi

import (
	"encoding/json"
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
	"github.com/bianjiefilm/product-image-engine/server/internal/config"
	"github.com/bianjiefilm/product-image-engine/server/internal/crossappreturn"
	"github.com/bianjiefilm/product-image-engine/server/internal/receiptdoc"
)

type crossAppReceiverStub struct {
	srv        *httptest.Server
	mu         sync.Mutex
	payloads   []crossappreturn.CandidateReturnPayload
	rawBodies  [][]byte
	headers    []http.Header
	calls      atomic.Int64
	failStatus atomic.Int32
	failBody   string
	delay      time.Duration
}

func newCrossAppReceiverStub(t *testing.T, secret string) *crossAppReceiverStub {
	t.Helper()
	rx := &crossAppReceiverStub{}
	rx.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rx.delay > 0 {
			time.Sleep(rx.delay)
		}
		rx.calls.Add(1)
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if code := rx.failStatus.Swap(0); code != 0 {
			w.WriteHeader(int(code))
			_, _ = w.Write([]byte(rx.failBody))
			return
		}
		var payload crossappreturn.CandidateReturnPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := receiptdoc.FrameVerify(
			[]byte(secret),
			r.Header.Get(receiptdoc.HeaderTimestamp),
			r.Header.Get(receiptdoc.HeaderSignature),
			"product-image",
			payload.TargetApp,
			payload.ReceiptID,
			raw,
			time.Now().UTC(),
		); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_hmac"}`))
			return
		}
		rx.mu.Lock()
		rx.payloads = append(rx.payloads, payload)
		rx.rawBodies = append(rx.rawBodies, append([]byte(nil), raw...))
		rx.headers = append(rx.headers, r.Header.Clone())
		rx.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"candidate_id":"touch_cand_%s","status":"accepted_candidate"}`, payload.OutputID)
	}))
	t.Cleanup(rx.srv.Close)
	return rx
}

func (rx *crossAppReceiverStub) count() int {
	rx.mu.Lock()
	defer rx.mu.Unlock()
	return len(rx.payloads)
}

func mustCrossAppManifest(t *testing.T, ordersURL, touchURL string) *appregistry.Manifest {
	t.Helper()
	manifest := fmt.Sprintf(`{
		"manifest_version": "app-registry/v1",
		"apps": [
			{"app_id": "orders", "display_name": "订单", "enabled": true,
			 "supported_source_kinds": ["order"],
			 "capabilities": [{"name": "order.handoff", "menu_visible": true, "requires_billing": false}],
			 "launch_targets": [{"target_id": "ti-orders-web", "kind": "launch", "url": "https://guanlan-order.example.invalid/launch"}],
			 "receipt_targets": [{"target_id": "rc-orders-main", "kind": "receipt", "url": %q}]},
			{"app_id": "touch-engine", "display_name": "碰一碰内容包", "enabled": true,
			 "supported_source_kinds": ["campaign", "standalone"],
			 "capabilities": [{"name": "content.pack", "menu_visible": true, "requires_billing": false}],
			 "launch_targets": [{"target_id": "ti-campaign-web", "kind": "launch", "url": "https://touch-engine.example.invalid/launch"}],
			 "receipt_targets": [{"target_id": "rc-touch-content-pack", "kind": "receipt", "url": %q}]}
		]
	}`, ordersURL, touchURL)
	m, err := appregistry.LoadManifest([]byte(manifest))
	if err != nil {
		t.Fatalf("加载跨应用登记表失败: %v", err)
	}
	return m
}

func TestCrossAppContinueNativeOrderCampaignAndBotNoRebuild(t *testing.T) {
	var taskCalls atomic.Int64
	taskStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		taskCalls.Add(1)
		_, _ = w.Write([]byte(`{"task_id":"tk_bad","status":"queued"}`))
	}))
	defer taskStub.Close()

	f := newFixture(t, func(c *config.Config) {
		c.GenerationEnabled = true
		c.TaskBaseURL = taskStub.URL
		c.TaskToken = "task-tok"
	})
	_, pair := f.login(t, "cont@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant("cont@x.com")

	// 1. Create an order-bound project and a campaign-bound project via handoff.
	ordDoc := handoffDoc("h-cont-ord-1", "orders", "order", tenant, "src-cont-ord", "bind-cont-ord-1", "rev-1", "brief-1")
	st, ordAcc := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(ordDoc, "主图"))
	if st != 200 {
		t.Fatalf("order accept failed: %d %v", st, ordAcc)
	}
	ordProjID := ordAcc["project"].(map[string]any)["id"].(string)

	campDoc := handoffDoc("h-cont-camp-1", "touch-engine", "campaign", tenant, "src-cont-camp", "bind-cont-camp-1", "rev-1", "brief-1")
	st, campAcc := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(campDoc, "主图"))
	if st != 200 {
		t.Fatalf("campaign accept failed: %d %v", st, campAcc)
	}
	campProjID := campAcc["project"].(map[string]any)["id"].(string)

	// Record initial project count to prove continuation NEVER builds a second project.
	st, projListBefore := f.do(t, "GET", "/api/v1/projects", tok, nil)
	if st != 200 {
		t.Fatalf("list projects failed: %d", st)
	}
	initialProjects := len(projListBefore["projects"].([]any))

	// 2. Continue order-bound project natively.
	st, ordCont := f.do(t, "POST", "/api/v1/crossapp/continue", tok, map[string]any{
		"project_id":           ordProjID,
		"task_id":              "task_ord_native_1",
		"pinned_asset_version": "ver_ord_v1",
		"actor_role":           "merchant",
		"entry_trigger":        "merchant_continue",
	})
	if st != 200 {
		t.Fatalf("order continue failed: %d %v", st, ordCont)
	}
	cont1 := ordCont["continuation"].(map[string]any)
	if cont1["project_id"] != ordProjID || cont1["task_id"] != "task_ord_native_1" || cont1["order_id"] != "100234" {
		t.Fatalf("unexpected order continuation: %v", cont1)
	}

	// 3. Bot/notification return continues the EXACT same order project without rebuilding.
	st, botCont := f.do(t, "POST", "/api/v1/crossapp/continue", tok, map[string]any{
		"source_kind":             "bot",
		"project_id":              ordProjID,
		"task_id":                 "task_ord_native_1",
		"pinned_asset_version":    "ver_ord_v1",
		"requested_asset_version": "ver_ord_v1",
		"actor_role":              "bot_service",
		"entry_trigger":           "bot_return",
	})
	if st != 200 {
		t.Fatalf("bot continue failed: %d %v", st, botCont)
	}
	cont2 := botCont["continuation"].(map[string]any)
	if cont2["project_id"] != ordProjID || cont2["continued_count"] != float64(2) {
		t.Fatalf("bot continue must resume same project: %v", cont2)
	}
	se := botCont["side_effects"].(map[string]any)
	if se["project_rebuilt"] != false || se["generation_triggered"] != false || se["billing_triggered"] != false {
		t.Fatalf("continuation must have zero side effects: %v", se)
	}

	// 4. Campaign/no-order continuation succeeds without order_id.
	st, campCont := f.do(t, "POST", "/api/v1/crossapp/continue", tok, map[string]any{
		"project_id":           campProjID,
		"task_id":              "task_camp_native_1",
		"pinned_asset_version": "ver_camp_v1",
	})
	if st != 200 {
		t.Fatalf("campaign continue failed: %d %v", st, campCont)
	}
	cCont := campCont["continuation"].(map[string]any)
	if cCont["campaign_id"] != "camp-2026-091" || cCont["order_id"] != nil {
		t.Fatalf("campaign continue should have campaign_id and no order_id: %v", cCont)
	}

	// 5. Negative: campaign forging order_id to bypass auth -> 400 fake_order_forbidden.
	st, fakeOrd := f.do(t, "POST", "/api/v1/crossapp/continue", tok, map[string]any{
		"project_id":           campProjID,
		"order_id":             "fake_ord_777",
		"pinned_asset_version": "ver_camp_v1",
	})
	if st != 400 {
		t.Fatalf("fake order on campaign must be 400, got %d %v", st, fakeOrd)
	}
	if code, _ := errCodeOf(fakeOrd); code != crossappreturn.CodeFakeOrderForbidden {
		t.Fatalf("expected fake_order_forbidden, got %v", fakeOrd)
	}

	// 6. Negative: silently swapping asset_version with "latest" -> 409 latest_version_swap_forbidden.
	st, swapResp := f.do(t, "POST", "/api/v1/crossapp/continue", tok, map[string]any{
		"project_id":              ordProjID,
		"pinned_asset_version":    "ver_ord_v1",
		"requested_asset_version": "latest",
	})
	if st != 409 {
		t.Fatalf("latest version swap must be 409, got %d %v", st, swapResp)
	}
	if code, _ := errCodeOf(swapResp); code != crossappreturn.CodeLatestSwapForbidden {
		t.Fatalf("expected latest_version_swap_forbidden, got %v", swapResp)
	}

	// 7. Negative: visitor touch/view NEVER initiates generation or continuation mutation.
	st, visResp := f.do(t, "POST", "/api/v1/crossapp/continue", tok, map[string]any{
		"project_id":           campProjID,
		"pinned_asset_version": "ver_camp_v1",
		"actor_role":           "visitor",
		"entry_trigger":        "visitor_touch",
		"request_generation":   true,
	})
	if st != 403 {
		t.Fatalf("visitor touch must be 403, got %d %v", st, visResp)
	}
	if code, _ := errCodeOf(visResp); code != crossappreturn.CodeVisitorGenerationBlocked {
		t.Fatalf("expected visitor_generation_forbidden, got %v", visResp)
	}

	// 8. Negative: cross-tenant continuation rejected.
	_, pairOther := f.login(t, "other-cont@x.com", "right-pass")
	tokOther, _ := pairOther["access_token"].(string)
	st, crossResp := f.do(t, "POST", "/api/v1/crossapp/continue", tokOther, map[string]any{
		"project_id":           ordProjID,
		"pinned_asset_version": "ver_ord_v1",
	})
	if st != 404 {
		t.Fatalf("cross-tenant continuation must be 404, got %d %v", st, crossResp)
	}

	// Verify ZERO second projects were built and ZERO generation tasks were called!
	st, projListAfter := f.do(t, "GET", "/api/v1/projects", tok, nil)
	if st != 200 || len(projListAfter["projects"].([]any)) != initialProjects {
		t.Fatalf("continuation must never rebuild or create a second project: before=%d after=%d", initialProjects, len(projListAfter["projects"].([]any)))
	}
	if taskCalls.Load() != 0 {
		t.Fatalf("continuation & visitor rejection must trigger zero generation tasks, got %d", taskCalls.Load())
	}
}

func TestCrossAppCandidateReturnTouchAndOrderHTTPIntegration(t *testing.T) {
	var taskCalls, billingCalls atomic.Int64
	taskStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		taskCalls.Add(1)
		_, _ = w.Write([]byte(`{"task_id":"tk_unexpected","status":"queued"}`))
	}))
	defer taskStub.Close()
	billingStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		billingCalls.Add(1)
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer billingStub.Close()

	eco := newEcoStub(t)
	touchRx := newCrossAppReceiverStub(t, testSecret)
	orderRx := newCrossAppReceiverStub(t, testSecret)

	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
		c.ReceiptKeys = "orders:" + testSecret + ";touch-engine:" + testSecret
		c.OrderInternalToken = "order-peer-tok"
		c.GenerationEnabled = true
		c.TaskBaseURL = taskStub.URL
		c.TaskToken = "task-tok"
		c.BillingEnabled = true
		c.BillingBaseURL = billingStub.URL
		c.BillingToken = "bill-tok"
	})
	attachRegistry(t, f, mustCrossAppManifest(t, orderRx.srv.URL+"/orders/receipt", touchRx.srv.URL+"/touch/content-pack"))

	_, pair := f.login(t, "ret@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant("ret@x.com")

	// ---- Campaign -> Touch HUI-3553 content pack receiver ----
	campDoc := handoffDoc("h-ret-camp-1", "touch-engine", "campaign", tenant, "src-ret-camp", "bind-ret-camp-1", "rev-1", "brief-1")
	st, campAcc := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(campDoc, "主图"))
	if st != 200 {
		t.Fatalf("campaign accept: %d %v", st, campAcc)
	}
	campProjID := campAcc["project"].(map[string]any)["id"].(string)

	st, outResp := f.do(t, "POST", "/api/v1/projects/"+campProjID+"/outputs", tok, map[string]any{
		"file_name": "hero.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 4, 4, 21)),
	})
	if st != 201 {
		t.Fatalf("register output: %d %v", st, outResp)
	}
	outID := outResp["output"].(map[string]any)["id"].(string)

	// Unselected campaign output must still fail closed with 409 version_not_selected.
	st, unsel := f.do(t, "POST", "/api/v1/projects/"+campProjID+"/outputs/"+outID+"/candidate-return", tok, map[string]any{
		"brand_id":      "brand_milk_tea",
		"asset_version": "ver_hero_1",
	})
	if st != 409 {
		t.Fatalf("unselected campaign output must be 409, got %d %v", st, unsel)
	}
	if code, _ := errCodeOf(unsel); code != "version_not_selected" {
		t.Fatalf("expected version_not_selected, got %v", unsel)
	}

	// Explicitly select output for campaign.
	st, _ = f.do(t, "POST", "/api/v1/projects/"+campProjID+"/campaign-selection", tok, map[string]any{"output_id": outID})
	if st != 201 {
		t.Fatalf("campaign selection failed: %d", st)
	}

	// Deliver immutable candidate return to registered Touch content pack receiver.
	st, retResp := f.do(t, "POST", "/api/v1/projects/"+campProjID+"/outputs/"+outID+"/candidate-return", tok, map[string]any{
		"brand_id":         "brand_milk_tea",
		"task_id":          "task_camp_701",
		"asset_version":    "ver_hero_1",
		"usage_scope":      "touch_content_pack",
		"destination_kind": "touch_content_pack",
	})
	if st != 201 {
		t.Fatalf("campaign candidate return must be 201, got %d %v", st, retResp)
	}
	rec := retResp["record"].(map[string]any)
	receiptID := rec["receipt_id"].(string)
	if rec["status"] != crossappreturn.StatusDelivered || touchRx.count() != 1 {
		t.Fatalf("expected delivered receipt on touch receiver: rec=%v count=%d", rec, touchRx.count())
	}
	got := touchRx.payloads[0]
	if got.TenantID != tenant || got.BrandID != "brand_milk_tea" || got.CampaignID != "camp-2026-091" ||
		got.OrderID != "" || got.ProjectID != campProjID || got.TaskID != "task_camp_701" ||
		got.OutputID != outID || got.AssetVersion != "ver_hero_1" || got.UsageScope != "touch_content_pack" ||
		got.SourceVersion != "rev-1:brief-1" || got.ReceiptID != receiptID {
		t.Fatalf("delivered payload mismatch: %+v", got)
	}
	if ce := crossappreturn.AuditRawEventBytes(touchRx.rawBodies[0]); ce != nil {
		t.Fatalf("raw event failed plaintext/secret audit: %v", ce)
	}

	// Duplicate delivery -> idempotent 200, exact same receipt_id, zero extra delivery.
	st, dupResp := f.do(t, "POST", "/api/v1/projects/"+campProjID+"/outputs/"+outID+"/candidate-return", tok, map[string]any{
		"brand_id":         "brand_milk_tea",
		"task_id":          "task_camp_701",
		"asset_version":    "ver_hero_1",
		"usage_scope":      "touch_content_pack",
		"destination_kind": "touch_content_pack",
	})
	if st != 200 || dupResp["duplicate"] != true || touchRx.calls.Load() != 1 {
		t.Fatalf("duplicate return must be idempotent 200 with 1 total HTTP call: st=%d resp=%v calls=%d", st, dupResp, touchRx.calls.Load())
	}

	// Explicit merchant adoption.
	st, adoptResp := f.do(t, "POST", "/api/v1/projects/"+campProjID+"/outputs/"+outID+"/candidate-return/adopt", tok, map[string]any{
		"receipt_id": receiptID,
	})
	if st != 200 || adoptResp["record"].(map[string]any)["adopted"] != true {
		t.Fatalf("adopt failed: %d %v", st, adoptResp)
	}

	// Read-only queries return exact state with zero side effects.
	st, listResp := f.do(t, "GET", "/api/v1/projects/"+campProjID+"/candidate-returns", tok, nil)
	if st != 200 || len(listResp["returns"].([]any)) != 1 {
		t.Fatalf("list candidate returns failed: %d %v", st, listResp)
	}
	st, getResp := f.do(t, "GET", "/api/v1/candidate-returns/"+receiptID, tok, nil)
	if st != 200 || getResp["record"].(map[string]any)["status"] != crossappreturn.StatusAdopted {
		t.Fatalf("get candidate return failed: %d %v", st, getResp)
	}

	// ---- Order -> registered orders receiver ----
	ordDoc := handoffDoc("h-ret-ord-1", "orders", "order", tenant, "src-ret-ord", "bind-ret-ord-1", "rev-1", "brief-1")
	st, ordAcc := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(ordDoc, "主图"))
	if st != 200 {
		t.Fatalf("order accept: %d %v", st, ordAcc)
	}
	ordProjID := ordAcc["project"].(map[string]any)["id"].(string)
	st, ordOut := f.do(t, "POST", "/api/v1/projects/"+ordProjID+"/outputs", tok, map[string]any{
		"file_name": "ord.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 4, 4, 33)),
	})
	if st != 201 {
		t.Fatalf("order register output: %d %v", st, ordOut)
	}
	ordOutID := ordOut["output"].(map[string]any)["id"].(string)

	st, ordRet := f.do(t, "POST", "/api/v1/projects/"+ordProjID+"/outputs/"+ordOutID+"/candidate-return", tok, map[string]any{
		"brand_id":      "brand_milk_tea",
		"asset_version": "ver_ord_1",
	})
	if st != 201 || orderRx.count() != 1 {
		t.Fatalf("order candidate return failed: %d %v count=%d", st, ordRet, orderRx.count())
	}
	if orderRx.payloads[0].OrderID != "100234" || orderRx.payloads[0].CampaignID != "" {
		t.Fatalf("unexpected order return payload: %+v", orderRx.payloads[0])
	}
	if orderRx.headers[0].Get("Authorization") != "Bearer order-peer-tok" {
		t.Fatalf("expected bearer token on order receiver, got %q", orderRx.headers[0].Get("Authorization"))
	}

	if taskCalls.Load() != 0 || billingCalls.Load() != 0 {
		t.Fatalf("candidate return & adoption must trigger zero generation/billing: tasks=%d billing=%d", taskCalls.Load(), billingCalls.Load())
	}
}

func TestCrossAppCandidateReturnUnregisteredTarget409NeverFakesSuccess(t *testing.T) {
	eco := newEcoStub(t)
	// Default manifest intentionally has touch-engine receipt_targets: [].
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
		c.ReceiptKeys = "orders:" + testSecret + ";touch-engine:" + testSecret
	})

	_, pair := f.login(t, "unreg@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant("unreg@x.com")

	campDoc := handoffDoc("h-unreg-1", "touch-engine", "campaign", tenant, "src-unreg", "bind-unreg-1", "rev-1", "brief-1")
	st, acc := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(campDoc, "主图"))
	if st != 200 {
		t.Fatalf("accept failed: %d %v", st, acc)
	}
	projID := acc["project"].(map[string]any)["id"].(string)
	st, outResp := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs", tok, map[string]any{
		"file_name": "u.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 2, 2, 5)),
	})
	if st != 201 {
		t.Fatalf("register output failed: %d", st)
	}
	outID := outResp["output"].(map[string]any)["id"].(string)
	st, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})
	if st != 201 {
		t.Fatalf("campaign selection failed: %d", st)
	}

	st, denied := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/candidate-return", tok, map[string]any{
		"brand_id":      "brand_1",
		"asset_version": "ver_1",
	})
	if st != 409 {
		t.Fatalf("unregistered target must return explicit 409, got %d %v", st, denied)
	}
	if code, _ := errCodeOf(denied); code != crossappreturn.CodeTargetNotRegistered {
		t.Fatalf("expected error code target_not_registered, got %v", denied)
	}
	errObj := denied["error"].(map[string]any)
	if errObj["recovery_action"] != crossappreturn.RecoveryRegisterTarget {
		t.Fatalf("expected recovery_action %s, got %v", crossappreturn.RecoveryRegisterTarget, errObj)
	}
}

func TestCrossAppCandidateReturnStaleRevokedExpiredAndRetryGuards(t *testing.T) {
	var taskCalls atomic.Int64
	taskStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		taskCalls.Add(1)
		_, _ = w.Write([]byte(`{"task_id":"tk_bad","status":"queued"}`))
	}))
	defer taskStub.Close()

	eco := newEcoStub(t)
	touchRx := newCrossAppReceiverStub(t, testSecret)
	orderRx := newCrossAppReceiverStub(t, testSecret)

	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
		c.ReceiptKeys = "orders:" + testSecret + ";touch-engine:" + testSecret
		c.GenerationEnabled = true
		c.TaskBaseURL = taskStub.URL
		c.TaskToken = "task-tok"
	})
	attachRegistry(t, f, mustCrossAppManifest(t, orderRx.srv.URL+"/orders/receipt", touchRx.srv.URL+"/touch/content-pack"))

	_, pair := f.login(t, "guards@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant("guards@x.com")

	// Standalone project for clean guard assertions.
	projID := createProject(t, f, tok)
	registerOut := func(name string, seed byte) string {
		t.Helper()
		st, o := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs", tok, map[string]any{
			"file_name": name, "content_type": "image/png", "data_b64": b64(pngBytes(t, 2, 2, seed)),
		})
		if st != 201 {
			t.Fatalf("register %s failed: %d %v", name, st, o)
		}
		return o["output"].(map[string]any)["id"].(string)
	}

	outV2 := registerOut("v2.png", 41)
	outV1Late := registerOut("v1.png", 42)
	outRetry := registerOut("retry.png", 43)

	// 1. Raw image plaintext rejected fail-closed.
	st, plainErr := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outV2+"/candidate-return", tok, map[string]any{
		"source_kind":       "campaign",
		"campaign_id":       "camp_guard_1",
		"target_app":        "touch-engine",
		"raw_image_payload": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUg==",
	})
	if st != 400 {
		t.Fatalf("plaintext image must be 400, got %d %v", st, plainErr)
	}
	if code, _ := errCodeOf(plainErr); code != crossappreturn.CodePlaintextImageForbidden {
		t.Fatalf("expected plaintext_image_forbidden, got %v", plainErr)
	}

	// 2. Revoked permission rejected fail-closed with recovery path.
	st, revErr := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outV2+"/candidate-return", tok, map[string]any{
		"source_kind": "campaign",
		"campaign_id": "camp_guard_1",
		"target_app":  "touch-engine",
		"permission":  map[string]any{"grant_id": "g_rev", "status": "revoked"},
	})
	if st != 403 {
		t.Fatalf("revoked permission must be 403, got %d %v", st, revErr)
	}
	if code, _ := errCodeOf(revErr); code != crossappreturn.CodeRevoked {
		t.Fatalf("expected revoked, got %v", revErr)
	}

	// 3. Expired signed URL rejected fail-closed with 410 url_expired + recovery path.
	st, expErr := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outV2+"/candidate-return", tok, map[string]any{
		"source_kind":           "campaign",
		"campaign_id":           "camp_guard_1",
		"target_app":            "touch-engine",
		"signed_access_url":     "https://cdn.example.invalid/v2.png",
		"signed_url_expires_at": time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339),
	})
	if st != 410 {
		t.Fatalf("expired signed URL must be 410, got %d %v", st, expErr)
	}
	if code, _ := errCodeOf(expErr); code != crossappreturn.CodeURLExpired {
		t.Fatalf("expected url_expired, got %v", expErr)
	}

	// 4. Deliver v2 & adopt it; then late-arriving v1 MUST fail with 409 stale_source_version.
	st, v2Resp := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outV2+"/candidate-return", tok, map[string]any{
		"source_kind":        "campaign",
		"campaign_id":        "camp_guard_1",
		"target_app":         "touch-engine",
		"asset_version":      "ver_v2",
		"source_version":     "v2",
		"source_version_seq": 2,
	})
	if st != 201 {
		t.Fatalf("v2 return failed: %d %v", st, v2Resp)
	}
	v2ReceiptID := v2Resp["record"].(map[string]any)["receipt_id"].(string)
	st, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outV2+"/candidate-return/adopt", tok, map[string]any{
		"receipt_id": v2ReceiptID,
	})
	if st != 200 {
		t.Fatalf("adopt v2 failed: %d", st)
	}

	st, staleResp := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outV1Late+"/candidate-return", tok, map[string]any{
		"source_kind":        "campaign",
		"campaign_id":        "camp_guard_1",
		"target_app":         "touch-engine",
		"asset_version":      "ver_v1",
		"source_version":     "v1",
		"source_version_seq": 1,
	})
	if st != 409 {
		t.Fatalf("late stale v1 must be 409, got %d %v", st, staleResp)
	}
	if code, _ := errCodeOf(staleResp); code != crossappreturn.CodeStaleSourceVersion {
		t.Fatalf("expected stale_source_version, got %v", staleResp)
	}

	// 5. Transient 500 -> failed receipt -> retry succeeds with same receipt_id & zero regeneration.
	touchRx.failStatus.Store(http.StatusInternalServerError)
	touchRx.failBody = `{"error":"down"}`
	st, failResp := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outRetry+"/candidate-return", tok, map[string]any{
		"source_kind":        "campaign",
		"campaign_id":        "camp_retry_1",
		"target_app":         "touch-engine",
		"asset_version":      "ver_retry_1",
		"source_version":     "v1",
		"source_version_seq": 1,
	})
	if st != 502 {
		t.Fatalf("transient 500 must surface 502 with failed record, got %d %v", st, failResp)
	}
	retryReceiptID := failResp["record"].(map[string]any)["receipt_id"].(string)

	// Visitor cannot trigger retry.
	st, visRetry := f.do(t, "POST", "/api/v1/candidate-returns/"+retryReceiptID+"/retry", tok, map[string]any{
		"actor_role":    "visitor",
		"entry_trigger": "visitor_view",
	})
	if st != 403 {
		t.Fatalf("visitor retry must be 403, got %d %v", st, visRetry)
	}

	// Cross-tenant cannot read or retry receipt.
	_, pairOther := f.login(t, "cross@x.com", "right-pass")
	tokOther, _ := pairOther["access_token"].(string)
	if st, _ := f.do(t, "GET", "/api/v1/candidate-returns/"+retryReceiptID, tokOther, nil); st != 404 {
		t.Fatalf("cross-tenant get receipt must be 404, got %d", st)
	}
	if st, _ := f.do(t, "POST", "/api/v1/candidate-returns/"+retryReceiptID+"/retry", tokOther, nil); st != 404 {
		t.Fatalf("cross-tenant retry receipt must be 404, got %d", st)
	}

	// Merchant retry succeeds with same receipt_id and zero generation.
	st, okRetry := f.do(t, "POST", "/api/v1/candidate-returns/"+retryReceiptID+"/retry", tok, nil)
	if st != 200 || okRetry["retried"] != true || okRetry["record"].(map[string]any)["status"] != crossappreturn.StatusDelivered {
		t.Fatalf("retry should succeed: %d %v", st, okRetry)
	}

	// Revocation after delivery blocks adopt fail-closed!
	_ = tenant
	f.srv.CrossApp.RevokeAsset(tenant, okRetry["record"].(map[string]any)["payload"].(map[string]any)["asset_ref"].(string))
	st, revAdopt := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outRetry+"/candidate-return/adopt", tok, map[string]any{
		"receipt_id": retryReceiptID,
	})
	if st != 403 {
		t.Fatalf("revoked asset adopt must be 403, got %d %v", st, revAdopt)
	}
	if code, _ := errCodeOf(revAdopt); code != crossappreturn.CodeRevoked {
		t.Fatalf("expected revoked on adopt, got %v", revAdopt)
	}

	if taskCalls.Load() != 0 {
		t.Fatalf("guard/retry paths must trigger zero generation tasks, got %d", taskCalls.Load())
	}
}

func TestCrossAppCandidateReturnConcurrentHTTPRace(t *testing.T) {
	eco := newEcoStub(t)
	touchRx := newCrossAppReceiverStub(t, testSecret)
	touchRx.delay = 20 * time.Millisecond
	orderRx := newCrossAppReceiverStub(t, testSecret)

	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
		c.ReceiptKeys = "orders:" + testSecret + ";touch-engine:" + testSecret
	})
	attachRegistry(t, f, mustCrossAppManifest(t, orderRx.srv.URL+"/orders/receipt", touchRx.srv.URL+"/touch/content-pack"))

	_, pair := f.login(t, "race@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	projID := createProject(t, f, tok)

	st, outResp := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs", tok, map[string]any{
		"file_name": "race.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 2, 2, 99)),
	})
	if st != 201 {
		t.Fatalf("register output failed: %d", st)
	}
	outID := outResp["output"].(map[string]any)["id"].(string)

	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan string, workers)
	ids := make(chan string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/candidate-return", tok, map[string]any{
				"source_kind":        "campaign",
				"campaign_id":        "camp_http_race",
				"target_app":         "touch-engine",
				"asset_version":      "ver_race_http",
				"source_version":     "v1",
				"source_version_seq": 1,
			})
			if code != 200 && code != 201 {
				errs <- fmt.Sprintf("unexpected status %d: %v", code, body)
				return
			}
			rec, _ := body["record"].(map[string]any)
			id, _ := rec["receipt_id"].(string)
			if strings.TrimSpace(id) == "" {
				errs <- fmt.Sprintf("missing receipt_id: %v", body)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)

	for e := range errs {
		t.Fatalf("concurrent HTTP candidate-return error: %s", e)
	}
	var first string
	for id := range ids {
		if first == "" {
			first = id
		} else if id != first {
			t.Fatalf("divergent receipt_ids under concurrency: %s vs %s", first, id)
		}
	}
	if touchRx.calls.Load() != 1 {
		t.Fatalf("12 concurrent HTTP candidate-returns must coalesce to exactly 1 receiver delivery, got %d", touchRx.calls.Load())
	}
}
