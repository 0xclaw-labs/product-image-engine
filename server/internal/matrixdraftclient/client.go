// Package matrixdraftclient 是产品图源端对真实矩阵持久草稿服务
// (public-ai internal/matrixdraft @ d8facafc, matrix-draft-request/v1) 的薄客户端。
//
// 契约要点(照抄目的端,不转述、不加宽):
//   - POST /v1/matrix-drafts,头 X-Matrix-Tenant 必须等于 body.tenant_id(否则 403 wrong_tenant);
//   - source_app 恒为 "product-image",目的端映射 content_type 恒为 "image";
//     其他 content_type(如 video/activity)被目的端 409 wrong_type 拒绝——图片能力类型匹配是目的端契约;
//   - license_ref 破坏性必填(目的端 idPattern 校验);license_expires_at 可选 RFC3339,过期 403 license_expired;
//   - 幂等键=sha256(source_app,source_project_id,campaign_id,revision,asset_hash,tenant_id):
//     同键同事实→200 重放同草稿;同键异事实→409 idempotency_conflict;
//   - return_target.app 必须=source_app,path 必须是站内绝对路径(不得解码后跳转);
//   - published/charged 恒 false,草稿不是发布。
//
// 本包不做租户判定、不生成事实;事实由 httpapi 源端动作从已持久状态收集。
package matrixdraftclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 目的端契约常量。
const (
	SchemaVersion  = "matrix-draft-request/v1"
	DraftSchema    = "matrix-draft/v1"
	ReceiptSchema  = "matrix-draft-receipt/v1"
	SourceApp      = "product-image"
	ContentType    = "image"
	tenantHeader   = "X-Matrix-Tenant"
	defaultTimeout = 15 * time.Second
)

// Draft 是目的端返回的持久草稿行(published/charged 恒 false)。
type Draft struct {
	ID               string       `json:"draft_id"`
	SchemaVersion    string       `json:"schema_version"`
	SourceApp        string       `json:"source_app"`
	SourceProjectID  string       `json:"source_project_id"`
	CampaignID       string       `json:"campaign_id"`
	Revision         int          `json:"revision"`
	AssetID          string       `json:"asset_id"`
	AssetHash        string       `json:"asset_hash"`
	ContentType      string       `json:"content_type"`
	GrantRef         string       `json:"grant_ref"`
	TenantID         string       `json:"tenant_id"`
	BrandID          string       `json:"brand_id"`
	PayerID          string       `json:"payer_id"`
	LicenseRef       string       `json:"license_ref"`
	LicenseExpiresAt *time.Time   `json:"license_expires_at"`
	Return           ReturnTarget `json:"return_target"`
	Status           string       `json:"status"`
	Published        bool         `json:"published"`
	Charged          bool         `json:"charged"`
	CreatedAt        time.Time    `json:"created_at"`
	IdempotentReplay bool         `json:"idempotent_replay"`
}

// ReturnTarget 指回源端;path 是站内绝对路径,不是 URL。
type ReturnTarget struct {
	App  string `json:"app"`
	Path string `json:"path"`
}

// Input 是一次 create 的事实集合(source_app/content_type 由契约固定,不开放)。
type Input struct {
	SourceProjectID  string
	CampaignID       string
	Revision         int
	AssetID          string
	AssetHash        string
	GrantRef         string
	TenantID         string
	BrandID          string
	PayerID          string
	LicenseRef       string
	LicenseExpiresAt *time.Time
	ReturnPath       string
}

type createBody struct {
	SchemaVersion    string       `json:"schema_version"`
	SourceApp        string       `json:"source_app"`
	SourceProjectID  string       `json:"source_project_id"`
	CampaignID       string       `json:"campaign_id"`
	Revision         int          `json:"revision"`
	AssetID          string       `json:"asset_id"`
	AssetHash        string       `json:"asset_hash"`
	ContentType      string       `json:"content_type"`
	GrantRef         string       `json:"grant_ref"`
	TenantID         string       `json:"tenant_id"`
	BrandID          string       `json:"brand_id"`
	PayerID          string       `json:"payer_id"`
	LicenseRef       string       `json:"license_ref"`
	LicenseExpiresAt *string      `json:"license_expires_at,omitempty"`
	Return           ReturnTarget `json:"return_target"`
}

// APIError 是目的端结构化错误 {"error":{code,message}}。
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("matrixdraft: %s (%d): %s", e.Code, e.Status, e.Message)
}

// Client 指向一个真实 matrixdraft 服务实例(loopback 部署)。
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: defaultTimeout}
}

// Create 创建或幂等重放一条草稿。201=新建;200=同键同事实重放(IdempotentReplay=true)。
func (c *Client) Create(ctx context.Context, in Input) (Draft, error) {
	body := createBody{
		SchemaVersion: SchemaVersion, SourceApp: SourceApp,
		SourceProjectID: in.SourceProjectID, CampaignID: in.CampaignID, Revision: in.Revision,
		AssetID: in.AssetID, AssetHash: in.AssetHash, ContentType: ContentType,
		GrantRef: in.GrantRef, TenantID: in.TenantID, BrandID: in.BrandID, PayerID: in.PayerID,
		LicenseRef: in.LicenseRef,
		Return:     ReturnTarget{App: SourceApp, Path: in.ReturnPath},
	}
	if in.LicenseExpiresAt != nil {
		u := in.LicenseExpiresAt.UTC()
		s := u.Format(time.RFC3339)
		body.LicenseExpiresAt = &s
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Draft{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.BaseURL, "/")+"/v1/matrix-drafts", bytes.NewReader(raw))
	if err != nil {
		return Draft{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(tenantHeader, in.TenantID)
	return c.doDraft(req)
}

// Get 按 draft_id 回查草稿(纯读;目的端 GET 不创建任何东西)。
func (c *Client) Get(ctx context.Context, tenant, draftID string) (Draft, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/")+"/v1/matrix-drafts/"+draftID, nil)
	if err != nil {
		return Draft{}, err
	}
	req.Header.Set(tenantHeader, tenant)
	return c.doDraft(req)
}

// Receipt 按 draft_id 回查收据(schema=matrix-draft-receipt/v1)。
func (c *Client) Receipt(ctx context.Context, tenant, draftID string) (Draft, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(c.BaseURL, "/")+"/v1/matrix-drafts/"+draftID+"/receipt", nil)
	if err != nil {
		return Draft{}, err
	}
	req.Header.Set(tenantHeader, tenant)
	return c.doDraft(req)
}

func (c *Client) doDraft(req *http.Request) (Draft, error) {
	res, err := c.httpClient().Do(req)
	if err != nil {
		return Draft{}, &APIError{Status: 0, Code: "unreachable", Message: err.Error()}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Draft{}, &APIError{Status: res.StatusCode, Code: "unreadable_body", Message: err.Error()}
	}
	if res.StatusCode >= 300 {
		return Draft{}, c.apiErr(res.StatusCode, raw)
	}
	var draft Draft
	if jerr := json.Unmarshal(raw, &draft); jerr != nil || draft.ID == "" || draft.SchemaVersion == "" {
		return Draft{}, c.apiErr(res.StatusCode, raw)
	}
	return draft, nil
}

func (c *Client) apiErr(status int, raw []byte) *APIError {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	if body.Error.Code == "" {
		body.Error.Code = "unexpected_response"
		body.Error.Message = strings.TrimSpace(string(raw))
	}
	return &APIError{Status: status, Code: body.Error.Code, Message: body.Error.Message}
}
