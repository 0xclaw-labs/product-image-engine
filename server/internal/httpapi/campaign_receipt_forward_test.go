package httpapi

// HUI-1745 正向回执写回+回查代码路径测试(R14b)。
//
// 生产上 Touch receipt target 未登记(默认登记表 receipt_targets:[] 且被
// TestDefaultManifestKeepsCampaignReceiptClosed 钉死),选定后回执诚实 409
// receipt_target_unregistered——该面不变。本测试在 scratchpad 测试登记表里给
// touch-engine 注册 receipt target(指向进程内 eco 桩),证明"精确回执写回+回查"
// 代码路径真实完整:accept→登记→选定→回执 delivered→campaign-restore 读回→
// resend 幂等不重投。真实 Touch 接收端仍属 AG07,正向旅程 NOT_RUN。

import (
	"fmt"
	"testing"

	"github.com/bianjiefilm/product-image-engine/server/internal/appregistry"
	"github.com/bianjiefilm/product-image-engine/server/internal/config"
)

// mustTouchRegistry:orders 与 mustRegistry 相同;touch-engine 追加 receipt target(测试登记表)。
func mustTouchRegistry(t *testing.T, receiptURL string) *appregistry.Manifest {
	t.Helper()
	manifest := fmt.Sprintf(`{
		"manifest_version": "app-registry/v1",
		"apps": [
			{"app_id": "orders", "display_name": "订单", "enabled": true,
			 "supported_source_kinds": ["order"],
			 "capabilities": [{"name": "order.handoff", "menu_visible": true, "requires_billing": false}],
			 "launch_targets": [{"target_id": "ti-orders-web", "kind": "launch", "url": "https://guanlan-order.example.invalid/launch"}],
			 "receipt_targets": []},
			{"app_id": "touch-engine", "display_name": "活动", "enabled": true,
			 "supported_source_kinds": ["campaign", "standalone"],
			 "capabilities": [{"name": "image.generate", "menu_visible": true, "requires_billing": true}],
			 "launch_targets": [{"target_id": "ti-campaign-web", "kind": "launch", "url": "https://touch-engine.example.invalid/launch"}],
			 "receipt_targets": [{"target_id": "rc-touch-main", "kind": "receipt", "url": %q}]}
		]
	}`, receiptURL)
	m, err := appregistry.LoadManifest([]byte(manifest))
	if err != nil {
		t.Fatalf("测试登记表加载失败: %v", err)
	}
	return m
}

func TestCampaignReceiptForwardLegRegisteredTarget(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
		c.ReceiptKeys = "orders:" + testSecret + ";touch-engine:" + testSecret
	})
	// R14b:scratchpad 测试登记表给 touch-engine 注册 receipt target。
	attachRegistry(t, f, mustTouchRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))

	_, pair := f.login(t, "fwd@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant("fwd@x.com")

	doc := handoffDoc("h-camp-fwd-live", "touch-engine", "campaign", tenant, "src-camp-fwd", "bind-camp-fwd-live", "rev-1", "brief-1")
	st, accepted := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(doc, "主图"))
	if st != 200 {
		t.Fatalf("活动接受应 200: %d %v", st, accepted)
	}
	proj, _ := accepted["project"].(map[string]any)
	projID, _ := proj["id"].(string)
	binding, _ := accepted["binding"].(map[string]any)

	// 活动入口(launch):来源应用在登记表有 launch target → 解析出站内跳转事实。
	st, ret := f.do(t, "GET", "/api/v1/bindings/"+binding["id"].(string)+"/return-target", tok, nil)
	if st != 200 {
		t.Fatalf("登记了 launch target 的来源应解析返回地址: %d %v", st, ret)
	}
	if url, _ := ret["url"].(string); url == "" {
		t.Fatalf("返回地址缺失: %v", ret)
	}

	st, registered := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs", tok, map[string]any{
		"file_name": "chosen.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 2, 2, 11)),
	})
	if st != 201 {
		t.Fatalf("登记成果应 201: %d %v", st, registered)
	}
	out, _ := registered["output"].(map[string]any)
	outID, _ := out["id"].(string)

	// 未选定 → 诚实 409(面不动)。两个平级断言分别钉死状态码与错误码:
	// 嵌套条件在"409+错误码错"与"非409+错误码对"两类变异下都不失败(R-58 F-1)。
	st, denied := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/receipt", tok, nil)
	if st != 409 {
		t.Fatalf("未选定必须诚实 409: %d %v", st, denied)
	}
	if code, _ := errCodeOf(denied); code != "version_not_selected" {
		t.Fatalf("未选定错误码应为 version_not_selected: %v", denied)
	}
	// 明确选定这一版。
	st, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})
	if st != 201 {
		t.Fatalf("选定应 201: %d", st)
	}

	// 精确回执写回:选定后对已登记目标回执 → 201 delivered。
	st, sent := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/receipt", tok, nil)
	if st != 201 {
		t.Fatalf("登记目标下选定后回执应 201: %d %v", st, sent)
	}
	rec, _ := sent["receipt"].(map[string]any)
	if rec["status"] != "delivered" || rec["target_app"] != "touch-engine" {
		t.Fatalf("回执应精确投递到 touch-engine: %v", rec)
	}
	if eco.eventCount() != 1 {
		t.Fatalf("应恰好投递一次: %d", eco.eventCount())
	}

	// 回查:campaign-restore 读回该回执(同一账面,无第二事件)。
	st, restored := f.do(t, "GET", "/api/v1/projects/"+projID+"/campaign-restore", tok, nil)
	if st != 200 {
		t.Fatalf("活动恢复应 200: %d %v", st, restored)
	}
	rrec, _ := restored["receipt"].(map[string]any)
	if rrec == nil || rrec["output_id"] != outID || rrec["status"] != "delivered" || rrec["target_app"] != "touch-engine" {
		t.Fatalf("回查应读回已投递回执: %v", restored["receipt"])
	}
	ver, _ := restored["selected_version"].(map[string]any)
	if ver["output_id"] != outID {
		t.Fatalf("选定版本应保持: %v", ver)
	}

	// 重复提交回执 → 原样返回既有(不产生新事件)。
	st, again := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/receipt", tok, nil)
	if st != 200 || again["duplicate"] != true {
		t.Fatalf("重复回执应幂等: %d %v", st, again)
	}
	if eco.eventCount() != 1 {
		t.Fatalf("幂等重提不得再投递: %d", eco.eventCount())
	}

	// 投递帧内容:目标与 schema 正确(order-receipt/v1 承载,不含活动专有字段——面不回归)。
	eco.mu.Lock()
	ev := eco.events[0]
	eco.mu.Unlock()
	if ev["schema_version"] != "order-receipt/v1" || ev["target_app"] != "touch-engine" || ev["source_app"] != "product-image" {
		t.Fatalf("回执帧契约: %v", ev)
	}
}

// TestCampaignReceiptForwardUnregisteredTargetStaysHonest409 是夹具正向链路的变异臂:
// 与 TestCampaignSelectedReceiptDeliversExactlyOnceToFixtureTarget 同链
// (accept→登记成果→选定→回执),唯一变异是登记表保持生产形状
// (touch-engine receipt_targets=[],即 newFixture 默认装配的正式 DefaultManifest,
// 由 TestDefaultManifestKeepsCampaignReceiptClosed 钉死)。该链必须:
// 409 receipt_target_unregistered + 零投递。两个平级断言钉死状态与错误码,
// 任何把 fail-closed 面改成 2xx 假成功或换错误码的回归都会在此失败(R-58 F-1)。
func TestCampaignReceiptForwardUnregisteredTargetStaysHonest409(t *testing.T) {
	eco := newEcoStub(t)
	// 故意不 attach 测试登记表:默认登记表即生产形状(活动回执关闭)。
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
		c.ReceiptKeys = "orders:" + testSecret + ";touch-engine:" + testSecret
	})

	_, pair := f.login(t, "neg@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant("neg@x.com")

	doc := handoffDoc("h-camp-neg-1", "touch-engine", "campaign", tenant, "src-camp-neg", "bind-camp-neg-1", "rev-1", "brief-1")
	st, accepted := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(doc, "主图"))
	if st != 200 {
		t.Fatalf("活动接受应 200: %d %v", st, accepted)
	}
	proj, _ := accepted["project"].(map[string]any)
	projID, _ := proj["id"].(string)

	st, registered := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs", tok, map[string]any{
		"file_name": "neg.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 2, 2, 7)),
	})
	if st != 201 {
		t.Fatalf("登记成果应 201: %d %v", st, registered)
	}
	out, _ := registered["output"].(map[string]any)
	outID, _ := out["id"].(string)

	st, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})
	if st != 201 {
		t.Fatalf("选定应 201: %d", st)
	}

	// 未登记目标 → 必须 409 receipt_target_unregistered,且零投递。
	st, denied := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/receipt", tok, nil)
	if st != 409 {
		t.Fatalf("未登记目标必须诚实 409(不得 2xx 假成功): %d %v", st, denied)
	}
	if code, _ := errCodeOf(denied); code != "receipt_target_unregistered" {
		t.Fatalf("错误码应为 receipt_target_unregistered: %v", denied)
	}
	if eco.eventCount() != 0 {
		t.Fatalf("拒绝路径不得产生任何投递: %d", eco.eventCount())
	}
}
