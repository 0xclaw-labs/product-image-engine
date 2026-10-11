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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/config"
	"github.com/bianjiefilm/product-image-engine/server/internal/matrixdraftclient"
	"github.com/bianjiefilm/product-image-engine/server/internal/store"
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

// TestMatrixDraftDestination2xxContractViolationFailsExplicitly 钉死透传面类型化校验:
// 目的端返回 2xx 但 body 非契约(matrixdraftclient 产出 Status<400 的 APIError)时,
// BFF 必须显式 502 matrix_draft_bad_response——不得把非错误状态码原样透传,
// 否则 HTTP 200+错误体会被 web 侧 res.ok 判成"草稿已在矩阵服务落库"的假成功(R-58 F-2)。
func TestMatrixDraftDestination2xxContractViolationFailsExplicitly(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	attachRegistry(t, f, mustRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))
	projID, outID, tok, _ := campaignProjectWithOutput(t, f, "md-2xx@x.com", "h-md-2xx-1")
	_, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})

	// 违约桩:200 + 契约外 body(无 draft_id/schema_version,解析不出 Draft)。
	hits := 0
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unexpected":"shape"}`))
	}))
	defer bad.Close()
	f.srv.MatrixDrafts = &matrixdraftclient.Client{BaseURL: bad.URL}
	f.rearm(t)

	st, body := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-1"})
	if st != 502 {
		t.Fatalf("目的端 2xx 契约外响应必须显式失败 502(不得透传 200 冒成功): %d %v", st, body)
	}
	if code, _ := errCodeOf(body); code != "matrix_draft_bad_response" {
		t.Fatalf("错误码应为 matrix_draft_bad_response: %v", body)
	}
	if hits != 1 {
		t.Fatalf("502 必须来自目的端违约透传面(应恰好触达一次): %d", hits)
	}
	// 不冒成功也不落账:失败路径不得写本端 ref,回查诚实 404。
	st, none := f.do(t, "GET", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok, nil)
	if st != 404 {
		t.Fatalf("失败路径不得登记 ref: %d %v", st, none)
	}
	if code, _ := errCodeOf(none); code != "no_matrix_draft" {
		t.Fatalf("错误码应为 no_matrix_draft: %v", none)
	}
}

// TestMatrixDraftRevisionRefusesToSwallowStoreErrors 钉死 revision 事实链:
// revision 是目的端幂等键的成分;快照时间线读取失败必须显式上抛给调用方,
// 不得静默降级为 revision=1 编造送出事实(R-58 F-3)。
func TestMatrixDraftRevisionRefusesToSwallowStoreErrors(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "rev.db"))
	if err != nil {
		t.Fatalf("打开临时库: %v", err)
	}
	st.Close() // 注入 store 瞬时故障:此后任何读取都失败
	s := &Server{St: st}
	rev, rerr := snapshotOrdinal(s, context.Background(), "tenant-x", "bind-x", "snap-x")
	if rerr == nil {
		t.Fatalf("store 错误必须上抛,不得静默降级: revision=%d", rev)
	}
}

// TestMatrixDraftLicenseExpirySameInstantDifferentNotation 同一时刻不同写法
// (+08:00 vs Z)不算许可事实改写:比较归一化成 UTC 时刻,不再 409;存储保留
// 首送原串;解析失败回退原串比较(真异事实仍 409)。
func TestMatrixDraftLicenseExpirySameInstantDifferentNotation(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	attachRegistry(t, f, mustRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))
	projID, outID, tok, _ := campaignProjectWithOutput(t, f, "md-exp@x.com", "h-md-exp-1")
	_, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})
	stub := newCountingStub(t)
	f.srv.MatrixDrafts = &matrixdraftclient.Client{BaseURL: stub.srv.URL}
	f.rearm(t)

	st, first := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-exp", "license_expires_at": "2030-01-01T08:00:00+08:00"})
	if st != 201 {
		t.Fatalf("首送应 201: %d %v", st, first)
	}
	ref1, _ := first["ref"].(map[string]any)

	// 同一时刻,Z 写法 → 幂等重放,不再 409。
	st, again := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-exp", "license_expires_at": "2030-01-01T00:00:00Z"})
	if st != 200 {
		t.Fatalf("同一时刻不同写法不得 409: %d %v", st, again)
	}
	ref2, _ := again["ref"].(map[string]any)
	if ref2["id"] != ref1["id"] {
		t.Fatalf("同事实幂等 ref 不变: %v vs %v", ref1, ref2)
	}
	// 原串存储:落账保留首送写法,不因比较归一化而改写。
	if ref2["license_expires_at"] != "2030-01-01T08:00:00+08:00" {
		t.Fatalf("必须保留首送原串: %v", ref2["license_expires_at"])
	}
	// 时刻不同属真异事实,仍 409。
	st, conflict := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-exp", "license_expires_at": "2030-01-02T00:00:00Z"})
	if st != 409 {
		t.Fatalf("时刻不同仍须 409: %d %v", st, conflict)
	}
	// 解析失败回退原串比较:已有 expiry 的事实,再送不带 expiry → 409(改写)。
	st, missing := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-exp"})
	if st != 409 {
		t.Fatalf("去掉 expiry 属事实改写,须 409: %d %v", st, missing)
	}
}

// ---- 漂移窗口 documentation-test:钉住行为,不改行为 -------------------------------

// TestMatrixDraftSnapshotDriftWindowDocumented 钉住"绑定新增快照后,同 output 同
// license 再送"的当前行为(只观察,不断言"应然")。
//
// 机理:目的端幂等键含 facts.Revision;Revision=snapshotOrdinal=output 快照在
// 绑定快照时间线(新→旧)里的位置 len-i(旧端=1)。向时间线头部追加新快照时,
// 旧快照的 i 与 len 同步 +1,序号稳定 → 同 output 同事实再送仍是同键,目的端
// 幂等重放同一草稿,源端 ref 原样不变。
//
// 消费方须知(若本测试失败即出现"目的端新草稿键 + 响应回旧 ref"的漂移窗口):
// 窗口期内同一响应里 matrix_draft(目的端新键)与 ref(源端旧登记)指向不同
// 草稿;权威登记以源端 ref.DraftID 为准,目的端新草稿成为孤儿键,不会被源端
// 回查引用;修复方向是"事实随版本保持"(revision 变化即 matrix_draft_facts_conflict),
// 属行为变更,不在本测试范围。
func TestMatrixDraftSnapshotDriftWindowDocumented(t *testing.T) {
	eco := newEcoStub(t)
	f := newFixture(t, func(c *config.Config) {
		c.UploadBaseURL = eco.srv.URL
		c.UploadToken = "up-tok"
	})
	attachRegistry(t, f, mustRegistry(t, eco.srv.URL+"/internal/v1/handoff/receipts"))
	projID, outID, tok, tenant := campaignProjectWithOutput(t, f, "md-drift@x.com", "h-md-drift-1")
	_, _ = f.do(t, "POST", "/api/v1/projects/"+projID+"/campaign-selection", tok, map[string]any{"output_id": outID})

	// 忠实目的端桩:幂等键含 revision——同 revision 重放同草稿,异 revision 新键。
	// (newCountingStub 的"第二次必 replay"对键变化不忠实,故专用。)
	var seenRev []int
	draftsByRev := map[int]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/matrix-drafts", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Revision int `json:"revision"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		seenRev = append(seenRev, body.Revision)
		id, replay := draftsByRev[body.Revision]
		if !replay {
			id = fmt.Sprintf("dft_rev_%d", body.Revision)
			draftsByRev[body.Revision] = id
		}
		_ = json.NewEncoder(w).Encode(matrixdraftclient.Draft{
			ID: id, SchemaVersion: matrixdraftclient.DraftSchema, IdempotentReplay: replay,
		})
	})
	stub := httptest.NewServer(mux)
	t.Cleanup(stub.Close)
	f.srv.MatrixDrafts = &matrixdraftclient.Client{BaseURL: stub.URL}
	f.rearm(t)

	// 首送:输出落在绑定首快照上 → revision=1 → 201 新草稿。
	st, first := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-drift"})
	if st != 201 {
		t.Fatalf("首送应 201: %d %v", st, first)
	}
	ref1, _ := first["ref"].(map[string]any)
	if ref1 == nil || ref1["draft_id"] != "dft_rev_1" {
		t.Fatalf("首送应登记 dft_rev_1: %v", first["ref"])
	}

	// 绑定新增快照:同来源工程新版交接(rev-2,新凭据——一次性兑换拒绝异内容
	// 重放同凭据)→ pending;采纳为当前快照。五元组 source_ref 不变 → 同一绑定。
	doc2 := handoffDoc("h-md-drift-2", "touch-engine", "campaign", tenant, "src-h-md-drift-1", "bind-h-md-drift-2", "rev-2", "brief-1")
	st, acc2 := f.do(t, "POST", "/api/v1/handoffs/accept", tok, acceptBody(doc2, "主图"))
	if st != 200 {
		t.Fatalf("新版交接应 200: %d %v", st, acc2)
	}
	pending, _ := acc2["pending_snapshot"].(map[string]any)
	if pending == nil {
		t.Fatalf("新版交接应给待采用快照: %v", acc2)
	}
	binding, _ := acc2["binding"].(map[string]any)
	st, adopted := f.do(t, "POST", "/api/v1/bindings/"+binding["id"].(string)+"/adopt", tok,
		map[string]any{"snapshot_id": pending["id"]})
	if st != 200 || adopted["adopted"] != true {
		t.Fatalf("采纳新快照应 200: %d %v", st, adopted)
	}

	// 同 output 同 license 再送:钉住当前行为(序号稳定 → 同键幂等重放,ref 不变)。
	st, again := f.do(t, "POST", "/api/v1/projects/"+projID+"/outputs/"+outID+"/matrix-draft", tok,
		map[string]any{"license_ref": "lic-drift"})
	if st != 200 {
		t.Fatalf("同 output 同 license 再送当前行为应 200 幂等: %d %v", st, again)
	}
	ad, _ := again["matrix_draft"].(map[string]any)
	if ad["draft_id"] != "dft_rev_1" || ad["idempotent_replay"] != true {
		t.Fatalf("漂移窗口:目的端换新草稿键而源端 ref 未动(见函数头消费方须知): %v", ad)
	}
	ref2, _ := again["ref"].(map[string]any)
	if ref2["id"] != ref1["id"] || ref2["draft_id"] != ref1["draft_id"] {
		t.Fatalf("源端登记必须原样不变: %v vs %v", ref1, ref2)
	}
	if len(seenRev) != 2 || seenRev[0] != 1 || seenRev[1] != 1 {
		t.Fatalf("同 output 两次送矩阵 revision 必须稳定: %v", seenRev)
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

	// 直连目的端 video 负例:客户端 ContentType 常量钉死为 image(不开放),
	// 客户端编不出 video——"其他 content_type 被目的端 409 wrong_type 拒绝"
	// 的契约面只能用原始 HTTP 绕过客户端直打目的端验证。目的端 validate()
	// 先查 content_type,故 stand-in grant/brand 值即可触发该面。
	videoBody := fmt.Sprintf(`{
		"schema_version": %q, "source_app": "product-image",
		"source_project_id": "src-video-neg-%s", "revision": 1,
		"asset_id": "asset-video-neg-%s", "asset_hash": "%s",
		"content_type": "video", "grant_ref": %q, "tenant_id": %q,
		"brand_id": %q, "payer_id": %q, "license_ref": "lic-video-neg",
		"return_target": {"app": "product-image", "path": "/projects/x"}
	}`, matrixdraftclient.SchemaVersion, runSuffix, runSuffix, strings.Repeat("a", 64),
		sf["grant_ref"], tenant, sf["brand_id"], tenant)
	vreq, verr := http.NewRequest(http.MethodPost, base+"/v1/matrix-drafts", strings.NewReader(videoBody))
	if verr != nil {
		t.Fatalf("构造 video 负例: %v", verr)
	}
	vreq.Header.Set("Content-Type", "application/json")
	vreq.Header.Set("X-Matrix-Tenant", tenant)
	vres, derr := (&http.Client{Timeout: 15 * time.Second}).Do(vreq)
	if derr != nil {
		t.Fatalf("直连目的端 video 负例: %v", derr)
	}
	defer vres.Body.Close()
	vraw, _ := io.ReadAll(io.LimitReader(vres.Body, 1<<20))
	if vres.StatusCode != http.StatusConflict {
		t.Fatalf("content_type=video 必须被目的端 409 拒绝: %d %s", vres.StatusCode, string(vraw))
	}
	var vbody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(vraw, &vbody)
	if vbody.Error.Code != "wrong_type" {
		t.Fatalf("video 负例错误码必须 wrong_type: %s", string(vraw))
	}
}
