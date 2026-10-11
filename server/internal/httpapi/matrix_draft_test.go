package httpapi

// HUI-2388 源端动作测试。
// 两层:
//  1. 常驻:门序与零创建(version_not_selected 诚实面、未配置 503、许可事实不可改写、
//     GET/导航零创建——目的端桩只记录,任何到达的 create 都是失败);
//  2. livetest(env MATRIX_DRAFT_TEST_BASE_URL 指向真实 public-ai matrixdraft 服务
//     @ d8facafc 才跑,否则 t.Skip):创建→幂等重放→回查 receipt→license 过期 403。
//     真实服务集成不靠 fixture;本文件的目的端桩只用于"绝不能被碰到"的断言。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/config"
	"github.com/bianjiefilm/product-image-engine/server/internal/matrixdraftclient"
)

// countingStub 记录每一次请求;creates 里的任何条目都是测试失败证据。
type countingStub struct {
	srv     *httptest.Server
	creates int
	reads   int
}

func newCountingStub(t *testing.T) *countingStub {
	s := &countingStub{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/matrix-drafts", func(w http.ResponseWriter, r *http.Request) {
		s.creates++
		// 第二次以后的 create 模拟目的端幂等语义(真实服务才是权威;桩只为断言 BFF 行为)。
		_ = json.NewEncoder(w).Encode(matrixdraftclient.Draft{
			ID: "dft_stub", SchemaVersion: matrixdraftclient.DraftSchema, IdempotentReplay: s.creates > 1,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		s.reads++
		_ = json.NewEncoder(w).Encode(matrixdraftclient.Draft{ID: "dft_stub", SchemaVersion: matrixdraftclient.ReceiptSchema})
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// campaignProjectWithOutput 走真实 HTTP 门:接受活动交接→登记输出。返回 projID/outputID/token/tenant。
func campaignProjectWithOutput(t *testing.T, f *fixture, email, handoffID string) (string, string, string, string) {
	t.Helper()
	_, pair := f.login(t, email, "right-pass")
	tok, _ := pair["access_token"].(string)
	tenant := deriveTenant(email)
	doc := handoffDoc(handoffID, "touch-engine", "campaign", tenant, "src-"+handoffID, "bind-"+handoffID, "rev-1", "brief-1")
	st, accepted := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(doc, "主图"))
	if st != 200 {
		t.Fatalf("活动接受应 200: %d %v", st, accepted)
	}
	proj, _ := accepted["project"].(map[string]any)
	projID, _ := proj["id"].(string)
	st, registered := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs", tok, map[string]any{
		"file_name": "chosen.png", "content_type": "image/png", "data_b64": b64(pngBytes(t, 2, 2, 5)),
	})
	if st != 201 {
		t.Fatalf("登记成果应 201: %d %v", st, registered)
	}
	out, _ := registered["output"].(map[string]any)
	outID, _ := out["id"].(string)
	return projID, outID, tok, tenant
}

func TestMatrixDraftUnconfiguredFailsClosed(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	projID, outID, tok, _ := campaignProjectWithOutput(t, f, "md-nc@x.com", "h-md-nc-1")
	st, body := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-1"})
	if st != 503 {
		t.Fatalf("未配置矩阵服务必须 fail-closed 503: %d %v", st, body)
	}
	if code, _ := errCodeOf(body); code != "matrix_draft_unconfigured" {
		t.Fatalf("错误码: %v", body)
	}
}

func TestMatrixDraftCampaignRequiresSelectionAndGETNeverCreates(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	attachRegistry(t, f, mustRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))
	projID, outID, tok, _ := campaignProjectWithOutput(t, f, "md-sel@x.com", "h-md-sel-1")

	// 未选定 → version_not_selected(诚实拒绝面,R14d 不动);目的端桩一次都不许被碰。
	stub := newCountingStub(t)
	f.srv.MatrixDrafts = &matrixdraftclient.Client{BaseURL: stub.srv.URL}
	f.rearm(t)
	st, denied := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-1"})
	if st != 409 {
		t.Fatalf("未选定必须 409: %d %v", st, denied)
	}
	if code, _ := errCodeOf(denied); code != "version_not_selected" {
		t.Fatalf("错误码应为 version_not_selected: %v", denied)
	}
	if stub.creates != 0 {
		t.Fatalf("拒绝路径不得触达目的端: %d", stub.creates)
	}
	// GET 回查在无登记时 404 no_matrix_draft,且对目的端零请求(导航零创建,R14e-4)。
	st, none := f.do(t, "GET", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok, nil)
	if st != 404 {
		t.Fatalf("无登记回查应 404: %d %v", st, none)
	}
	if code, _ := errCodeOf(none); code != "no_matrix_draft" {
		t.Fatalf("错误码应为 no_matrix_draft: %v", none)
	}
	if stub.creates != 0 || stub.reads != 0 {
		t.Fatalf("查询零创建零触达: creates=%d reads=%d", stub.creates, stub.reads)
	}
}

func TestMatrixDraftLicenseFactsAreWriteOncePerVersion(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	attachRegistry(t, f, mustRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))
	projID, outID, tok, _ := campaignProjectWithOutput(t, f, "md-lic@x.com", "h-md-lic-1")
	_, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})

	stub := newCountingStub(t)
	f.srv.MatrixDrafts = &matrixdraftclient.Client{BaseURL: stub.srv.URL}
	f.rearm(t)
	st, first := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-1"})
	if st != 201 {
		t.Fatalf("首次送矩阵应 201: %d %v", st, first)
	}
	// 同一版本换许可事实 → 409,不改写;目的端不收第二次 create。
	st, conflict := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-2"})
	if st != 409 {
		t.Fatalf("许可事实改写必须 409: %d %v", st, conflict)
	}
	if code, _ := errCodeOf(conflict); code != "matrix_draft_facts_conflict" {
		t.Fatalf("错误码: %v", conflict)
	}
	if stub.creates != 1 {
		t.Fatalf("冲突路径不得再触达目的端: %d", stub.creates)
	}
	// 同事实重复送 → 幂等(目的端重放),ref 不变。
	st, again := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-1"})
	if st != 200 {
		t.Fatalf("同事实重复送应 200: %d %v", st, again)
	}
	// license_expires_at 变化同属许可事实,一并拒绝。
	st, expConflict := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-1", "license_expires_at": time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)})
	if st != 409 {
		t.Fatalf("许可有效期改写必须 409: %d %v", st, expConflict)
	}
}

func TestMatrixDraftMissingLicenseRefRejected(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	attachRegistry(t, f, mustRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))
	projID, outID, tok, _ := campaignProjectWithOutput(t, f, "md-noref@x.com", "h-md-noref-1")
	_, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})
	stub := newCountingStub(t)
	f.srv.MatrixDrafts = &matrixdraftclient.Client{BaseURL: stub.srv.URL}
	f.rearm(t)
	st, body := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok, map[string]any{})
	if st != 400 {
		t.Fatalf("license_ref 缺失必须 400: %d %v", st, body)
	}
	if stub.creates != 0 {
		t.Fatalf("缺 license_ref 不得触达目的端: %d", stub.creates)
	}
}

// ---- livetest:对真实 matrixdraft 服务(@d8facafc) ---------------------------------

func TestMatrixDraftLiveRealService(t *testing.T) {
	base := os.Getenv("MATRIX_DRAFT_TEST_BASE_URL")
	if base == "" {
		t.Skip("MATRIX_DRAFT_TEST_BASE_URL 未设置:真实服务集成在本机走查中执行(见 docs/audits/hui-2388)")
	}
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	attachRegistry(t, f, mustRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))
	// 目的端 sqlite 跨运行持久:fingerprint 一次写死,故每次运行用新的来源键,
	// 既证明创建,也在同一次运行内证明同键同事实重放;重跑不与历史行冲突。
	runSuffix := fmt.Sprintf("%d", time.Now().UnixNano())
	handoff1 := "h-md-live-1-" + runSuffix
	projID, outID, tok, tenant := campaignProjectWithOutput(t, f, "md-live@x.com", handoff1)
	_, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})

	f.srv.MatrixDrafts = &matrixdraftclient.Client{BaseURL: base}
	f.rearm(t)

	// 许可有效期用固定远期时刻:live 重跑时同键同事实(目的端幂等重放),不因 now 漂移变成异参冲突。
	future := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	st, created := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-live-1", "license_expires_at": future})
	if st != 201 {
		t.Fatalf("真实服务创建应 201: %d %v", st, created)
	}
	draft, _ := created["matrix_draft"].(map[string]any)
	draftID, _ := draft["draft_id"].(string)
	if draftID == "" || draft["published"] != false || draft["charged"] != false {
		t.Fatalf("草稿事实: %v", draft)
	}
	if draft["tenant_id"] != tenant || draft["content_type"] != "image" || draft["source_app"] != "product-image" {
		t.Fatalf("草稿契约事实: %v", draft)
	}
	if draft["campaign_id"] != "camp-2026-091" {
		t.Fatalf("source campaign 必须保留: %v", draft["campaign_id"])
	}
	// 事实保留:回查响应里的 source_facts 与服务端一致。
	sf, _ := created["source_facts"].(map[string]any)
	if sf["source_project_id"] != "src-"+handoff1 || sf["quality_verdict"] != "unknown" {
		t.Fatalf("source_facts: %v", sf)
	}
	// 同键同事实 → 幂等重放同草稿。
	st, replay := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-live-1", "license_expires_at": future})
	if st != 200 {
		t.Fatalf("同事实重复送应 200: %d %v", st, replay)
	}
	rd, _ := replay["matrix_draft"].(map[string]any)
	if rd["draft_id"] != draftID || rd["idempotent_replay"] != true {
		t.Fatalf("同键同事实必须回同草稿: %v", rd)
	}
	// 同键异事实在目的端也是 409(用另一输出同 source 键直接验证目的端契约)。
	client := &matrixdraftclient.Client{BaseURL: base}
	_, cErr := client.Create(context.Background(), matrixdraftclient.Input{
		SourceProjectID: "src-" + handoff1, CampaignID: "camp-2026-091", Revision: 1,
		AssetID: draft["asset_id"].(string), AssetHash: draft["asset_hash"].(string),
		GrantRef: fmt.Sprintf("%v", sf["grant_ref"]), TenantID: tenant,
		BrandID: fmt.Sprintf("%v", sf["brand_id"]), PayerID: tenant,
		LicenseRef: "lic-other", ReturnPath: "/projects/x",
	})
	api, ok := cErr.(*matrixdraftclient.APIError)
	if !ok || api.Code != "idempotency_conflict" || api.Status != 409 {
		t.Fatalf("同键异事实应 409 idempotency_conflict: %v", cErr)
	}
	// 回查:GET 草稿 + receipt(schema=matrix-draft-receipt/v1)。
	st, back := f.do(t, "GET", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok, nil)
	if st != 200 {
		t.Fatalf("回查应 200: %d %v", st, back)
	}
	receipt, _ := back["receipt"].(map[string]any)
	if receipt["schema_version"] != "matrix-draft-receipt/v1" || receipt["draft_id"] != draftID {
		t.Fatalf("receipt 回查: %v", receipt)
	}
	backDraft, _ := back["matrix_draft"].(map[string]any)
	if backDraft["license_ref"] != "lic-live-1" || backDraft["asset_hash"] != draft["asset_hash"] {
		t.Fatalf("回查事实与送出事实必须一致: %v", backDraft)
	}
	// 许可过期 → 目的端 403 license_expired(经 BFF 原样上浮)。
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	projID2, outID2, tok2, _ := campaignProjectWithOutput(t, f, "md-live2@x.com", "h-md-live-2-"+runSuffix)
	_, _ = f.do(t, "POST", "/api/v1/projects/"+projID2+"/campaign-selection", tok2, map[string]any{"output_id": outID2})
	st, expired := f.do(t, "POST", "/api/v1/projects/"+projID2+"/outputs/"+outID2+"/matrix-draft", tok2,
		map[string]any{"license_ref": "lic-live-2", "license_expires_at": past})
	if st != 403 {
		t.Fatalf("过期许可应 403: %d %v", st, expired)
	}
	if code, _ := errCodeOf(expired); code != "license_expired" {
		t.Fatalf("错误码应为 license_expired: %v", expired)
	}
}
