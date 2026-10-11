package matrixdraftclient

// wire 单测:httptest 只验证本客户端的请求编解码与错误解析是否逐字符合目的端
// 契约(public-ai@d8facafc)。它不是目的端替身,也不冒充集成;对真实服务的
// 集成由 httpapi 的 livetest(env MATRIX_DRAFT_TEST_BASE_URL)与本机走查证据承担。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testHash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func requestBody(t *testing.T, r *http.Request) (map[string]string, map[string]any) {
	t.Helper()
	if got := r.Header.Get(tenantHeader); got == "" {
		t.Fatal("必须带 X-Matrix-Tenant 头")
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body 必须是 JSON: %v", err)
	}
	return map[string]string{tenantHeader: r.Header.Get(tenantHeader)}, body
}

func TestCreateSendsContractWire(t *testing.T) {
	var seenPath, seenMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath, seenMethod = r.URL.Path, r.Method
		_, body := requestBody(t, r)
		if body["schema_version"] != SchemaVersion {
			t.Fatalf("schema_version 必须是目的端契约值: %v", body["schema_version"])
		}
		if body["source_app"] != "product-image" || body["content_type"] != "image" {
			t.Fatalf("source_app/content_type 由契约固定: %v", body)
		}
		if body["return_target"].(map[string]any)["app"] != "product-image" {
			t.Fatalf("return_target.app 必须=source_app: %v", body["return_target"])
		}
		_ = json.NewEncoder(w).Encode(Draft{ID: "dft_1", SchemaVersion: DraftSchema, Status: "draft"})
	}))
	defer srv.Close()

	exp := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	c := &Client{BaseURL: srv.URL}
	d, err := c.Create(context.Background(), Input{
		SourceProjectID: "src-p-1", CampaignID: "camp-1", Revision: 2,
		AssetID: "asset_stub_abc", AssetHash: testHash, GrantRef: "bind-1",
		TenantID: "acct_t1", BrandID: "brand_x", PayerID: "acct_t1",
		LicenseRef: "lic-1", LicenseExpiresAt: &exp, ReturnPath: "/projects/p-1",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if seenPath != "/v1/matrix-drafts" || seenMethod != http.MethodPost {
		t.Fatalf("create 必须是 POST /v1/matrix-drafts: %s %s", seenMethod, seenPath)
	}
	if d.ID != "dft_1" || d.SchemaVersion != DraftSchema {
		t.Fatalf("draft 解析: %+v", d)
	}
}

func TestCreateLicenseExpiryRFC3339(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, body := requestBody(t, r)
		lic, _ := body["license_expires_at"].(string)
		if lic == "" {
			t.Fatal("license_expires_at 必须以 RFC3339 字符串上线")
		}
		if _, err := time.Parse(time.RFC3339, lic); err != nil {
			t.Fatalf("license_expires_at 必须 RFC3339: %v", err)
		}
		_ = json.NewEncoder(w).Encode(Draft{ID: "dft_2", SchemaVersion: DraftSchema})
	}))
	defer srv.Close()
	exp := time.Date(2027, 1, 2, 3, 4, 5, 123000000, time.FixedZone("X", 8*3600))
	if _, err := (&Client{BaseURL: srv.URL}).Create(context.Background(), Input{
		SourceProjectID: "p", Revision: 1, AssetID: "a", AssetHash: testHash,
		GrantRef: "g", TenantID: "t", BrandID: "b", PayerID: "p1",
		LicenseRef: "lic", LicenseExpiresAt: &exp, ReturnPath: "/r",
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
}

func TestErrorSurfaceMapping(t *testing.T) {
	cases := []struct {
		status int
		code   string
	}{
		{http.StatusForbidden, "wrong_tenant"},
		{http.StatusForbidden, "grant_expired"},
		{http.StatusForbidden, "grant_revoked"},
		{http.StatusForbidden, "grant_unknown"},
		{http.StatusForbidden, "license_expired"},
		{http.StatusConflict, "wrong_type"},
		{http.StatusConflict, "old_version"},
		{http.StatusConflict, "hash_conflict"},
		{http.StatusConflict, "idempotency_conflict"},
		{http.StatusBadRequest, "bad_request"},
		{http.StatusInternalServerError, "store_failed"},
		{http.StatusNotFound, "not_found"},
	}
	statusByCode := map[string]int{
		"wrong_tenant": 403, "grant_expired": 403, "grant_revoked": 403, "grant_unknown": 403,
		"license_expired": 403, "wrong_type": 409, "old_version": 409, "hash_conflict": 409,
		"idempotency_conflict": 409, "bad_request": 400, "store_failed": 500, "not_found": 404,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// 测试把要模拟的错误码编码在路径前缀里(仅存在于本测试桩)。
		code := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/v1/matrix-drafts")
		w.WriteHeader(statusByCode[code])
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": "x"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range cases {
		_, err := (&Client{BaseURL: srv.URL + "/" + tc.code}).Create(context.Background(), Input{
			SourceProjectID: "p", Revision: 1, AssetID: "a", AssetHash: testHash,
			GrantRef: "g", TenantID: "t", BrandID: "b", PayerID: "p1", LicenseRef: "lic", ReturnPath: "/r",
		})
		api, ok := err.(*APIError)
		if !ok {
			t.Fatalf("%s: 应解析成 *APIError: %v", tc.code, err)
		}
		if api.Code != tc.code || api.Status != tc.status {
			t.Fatalf("%s: got %s/%d want %s/%d", tc.code, api.Code, api.Status, tc.code, tc.status)
		}
	}
}

func TestGetAndReceiptCarryTenantHeader(t *testing.T) {
	mux := http.NewServeMux()
	seen := map[string]string{}
	mux.HandleFunc("GET /v1/matrix-drafts/dft_9", func(w http.ResponseWriter, r *http.Request) {
		seen["get"] = r.Header.Get(tenantHeader)
		_ = json.NewEncoder(w).Encode(Draft{ID: "dft_9", SchemaVersion: DraftSchema, LicenseRef: "lic-1"})
	})
	mux.HandleFunc("GET /v1/matrix-drafts/dft_9/receipt", func(w http.ResponseWriter, r *http.Request) {
		seen["receipt"] = r.Header.Get(tenantHeader)
		_ = json.NewEncoder(w).Encode(Draft{ID: "dft_9", SchemaVersion: ReceiptSchema})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	d, err := c.Get(context.Background(), "acct_t1", "dft_9")
	if err != nil || d.ID != "dft_9" || d.LicenseRef != "lic-1" {
		t.Fatalf("get: %v %+v", err, d)
	}
	rc, err := c.Receipt(context.Background(), "acct_t1", "dft_9")
	if err != nil || rc.SchemaVersion != ReceiptSchema {
		t.Fatalf("receipt: %v %+v", err, rc)
	}
	if seen["get"] != "acct_t1" || seen["receipt"] != "acct_t1" {
		t.Fatalf("回查必须带租户头: %v", seen)
	}
}

func TestUnexpectedBodyIsErrorNotFakeDraft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
	}))
	defer srv.Close()
	_, err := (&Client{BaseURL: srv.URL}).Get(context.Background(), "t", "x")
	api, ok := err.(*APIError)
	if !ok || api.Code != "unexpected_response" || !strings.Contains(api.Message, "hello") {
		t.Fatalf("非契约响应必须显式报错: %v", err)
	}
}
