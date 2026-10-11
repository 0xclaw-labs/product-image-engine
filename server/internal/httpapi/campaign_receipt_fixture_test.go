package httpapi

import (
	"fmt"
	"testing"

	"github.com/bianjiefilm/product-image-engine/server/internal/appregistry"
	"github.com/bianjiefilm/product-image-engine/server/internal/config"
)

// mustRegistryWithTouchReceipt 只存在于测试进程:把 touch-engine 的 receipt target
// 指向 loopback 夹具桩,用来证明"选定 → 精确回执 → 回查"的源端正向链路。
// 它不是生产登记表。生产 touch-engine 仍按 touchleads-manifest.json 为 receipt_targets=[],
// 真实 Touch 回执端点与登记由 AG07 供给后才能替换;本测试不代表真实 Touch 已接通。
func mustRegistryWithTouchReceipt(t *testing.T, touchReceiptURL string) *appregistry.Manifest {
	t.Helper()
	manifest := fmt.Sprintf(`{
		"manifest_version": "app-registry/v1",
		"apps": [
			{"app_id": "orders", "display_name": "订单", "enabled": true,
			 "supported_source_kinds": ["order"],
			 "capabilities": [{"name": "order.handoff", "menu_visible": true, "requires_billing": false}],
			 "launch_targets": [{"target_id": "ti-orders-web", "kind": "launch", "url": "https://guanlan-order.example.invalid/launch"}],
			 "receipt_targets": [{"target_id": "rc-orders-main", "kind": "receipt", "url": "https://guanlan-order.example.invalid/api/handoff/receipts"}]},
			{"app_id": "touch-engine", "display_name": "活动(测试夹具)", "enabled": false,
			 "supported_source_kinds": ["campaign", "standalone"],
			 "capabilities": [{"name": "image.generate", "menu_visible": false, "requires_billing": true}],
			 "launch_targets": [{"target_id": "ti-campaign-web", "kind": "launch", "url": "https://touch-engine.example.invalid/launch"}],
			 "receipt_targets": [{"target_id": "rc-touch-engine-fixture", "kind": "receipt", "url": %q}]}
		]
	}`, touchReceiptURL)
	m, err := appregistry.LoadManifest([]byte(manifest))
	if err != nil {
		t.Fatalf("测试登记表加载失败: %v", err)
	}
	return m
}

// TestCampaignSelectedReceiptDeliversExactlyOnceToFixtureTarget 是源端正向证据:
// 活动交接 → 登记成果 → 选定 → 精确回执一次投递 → 重复回执幂等 → 列表回查为 delivered。
// 只用于本地夹具(loopback 桩 + 测试专用登记表);不代表真实 Touch 接收或生产登记。
func TestCampaignSelectedReceiptDeliversExactlyOnceToFixtureTarget(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
		c.ReceiptKeys = "orders:" + testSecret + ";touch-engine:" + testSecret
		c.PhotoUploadEnabled = true
	})
	attachRegistry(t, f, mustRegistryWithTouchReceipt(t, eco.srv.URL+"/internal/v1/handoff/receipts"))

	_, pair := f.login(t, "jia@x.com", "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant("jia@x.com")

	doc := handoffDoc("h-camp-fixture-1", "touch-engine", "campaign", tenant, "src-camp-fixture", "bind-camp-fixture-1", "rev-1", "brief-1")
	st, accepted := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(doc, "主图"))
	if st != 200 {
		t.Fatalf("活动接受应 200: %d %v", st, accepted)
	}
	proj, _ := accepted["project"].(map[string]any)
	projID, _ := proj["id"].(string)

	st, registered := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs", tok, map[string]any{
		"file_name": "fixture.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 2, 2, 5)),
	})
	if st != 201 {
		t.Fatalf("登记成果应 201: %d %v", st, registered)
	}
	out, _ := registered["output"].(map[string]any)
	outID, _ := out["id"].(string)

	st, selected := f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})
	if st != 201 {
		t.Fatalf("选定应 201: %d %v", st, selected)
	}

	st, sent := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/receipt", tok, nil)
	if st != 201 {
		t.Fatalf("选定后已登记夹具目标应回执 201: %d %v", st, sent)
	}
	rec, _ := sent["receipt"].(map[string]any)
	eventID, _ := rec["event_id"].(string)
	if rec["status"] != "delivered" || eventID == "" || rec["target_app"] != "touch-engine" {
		t.Fatalf("回执应为 delivered 且目标为 touch-engine: %v", rec)
	}
	if eco.eventCount() != 1 {
		t.Fatalf("应恰好投递一次: %d", eco.eventCount())
	}
	eco.mu.Lock()
	got := eco.events[0]
	eco.mu.Unlock()
	if got["event_id"] != eventID || got["target_app"] != "touch-engine" || got["source_app"] != "product-image" {
		t.Fatalf("接收端事件应与源端回执精确一致: %v", got)
	}

	st, again := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/receipt", tok, nil)
	if st != 200 || again["duplicate"] != true {
		t.Fatalf("重复回执应幂等返回原回执: %d %v", st, again)
	}
	if eco.eventCount() != 1 {
		t.Fatalf("重复回执不得产生第二条事件: %d", eco.eventCount())
	}

	st, listed := f.do(t, "GET", "/api/v1/projects/"+projID+"/receipts", tok, nil)
	rows, _ := listed["receipts"].([]any)
	if st != 200 || len(rows) != 1 {
		t.Fatalf("回查应恰好一条回执: %d %v", st, listed)
	}
	row, _ := rows[0].(map[string]any)
	if row["event_id"] != eventID || row["status"] != "delivered" {
		t.Fatalf("回查应为同一 event_id 且 delivered: %v", row)
	}
}
