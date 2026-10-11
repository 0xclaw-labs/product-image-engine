package httpapi

// HUI-2388 源端动作:把已选定的产品图版本送进真实矩阵持久草稿服务
// (public-ai matrixdraft @ d8facafc,loopback 部署)。
//
// 红线:
//   - 只有显式 POST 动作会创建草稿;GET/导航/查询零创建(目的端 GET 亦不创建);
//   - campaign 来源必须先明确选定该版(复用 version_not_selected 诚实拒绝,不改动);
//   - 事实保留:tenant/payer/brand/source_project/campaign/revision/asset hash 全部来自
//     服务端已持久状态,不由调用方改写;license facts 随版本写死(不一致 409,不改写);
//   - brand_id/grant_ref 是 AG01 brandregistry/grant 接入前的确定性 stand-in(spec §2.2),
//     组合版本 d8facafc 的目的端按 idPattern 接受,接入后替换,审计已记 NOT_RUN 解除条件;
//   - draft 不是发布:目的端 published/charged 恒 false;本端也不写任何"已发布"状态。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/matrixdraftclient"
	"github.com/bianjiefilm/product-image-engine/server/internal/platform"
	si "github.com/bianjiefilm/product-image-engine/server/internal/sourceimage"
	"github.com/bianjiefilm/product-image-engine/server/internal/store"
)

type matrixDraftRequest struct {
	LicenseRef       string `json:"license_ref"`
	LicenseExpiresAt string `json:"license_expires_at,omitempty"`
}

// handleCreateMatrixDraft POST /api/v1/projects/{id}/outputs/{outputId}/matrix-draft
func (s *Server) handleCreateMatrixDraft(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req matrixDraftRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是 JSON(license_ref 必填)")
		return
	}
	if req.LicenseRef == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "license_ref 必填(目的端破坏性必填,不代填)")
		return
	}
	var licExp *time.Time
	if req.LicenseExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.LicenseExpiresAt)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid_request", "license_expires_at 必须 RFC3339")
			return
		}
		u := t.UTC()
		licExp = &u
	}
	if s.MatrixDrafts == nil {
		writeErr(w, http.StatusServiceUnavailable, "matrix_draft_unconfigured",
			"矩阵持久草稿服务未配置(MATRIX_DRAFT_BASE_URL),拒绝创建,不伪造草稿")
		return
	}
	ctx := r.Context()
	projID, outputID := r.PathValue("id"), r.PathValue("outputId")
	out, facts, aerr := s.collectMatrixFacts(ctx, p, projID, outputID)
	if aerr != nil {
		aerr.write(w)
		return
	}
	// 许可随版本保持:同 output 已登记且 license 事实不一致 → 拒绝(先于目的端)。
	// license_expires_at 按 RFC3339 时刻比较(同一时刻不同写法 +08:00 vs Z 不算
	// 改写);落账沿用已登记原串,保留首次请求写法(原串存储)。
	putLicRef, putLicExp := req.LicenseRef, req.LicenseExpiresAt
	if existing, err := s.St.GetMatrixDraftRef(ctx, p.Tenant(), projID, outputID); err == nil {
		if existing.LicenseRef != req.LicenseRef || !licenseExpiresEqual(existing.LicenseExpiresAt, req.LicenseExpiresAt) {
			writeErr(w, http.StatusConflict, "matrix_draft_facts_conflict",
				"该输出版本已按其他许可事实送过矩阵;许可随版本保持,不得改写(如需新许可请新版本新输出)")
			return
		}
		putLicRef, putLicExp = existing.LicenseRef, existing.LicenseExpiresAt
	} else if !errors.Is(err, store.ErrNotFound) {
		writeStoreErr(w, err)
		return
	}
	in := matrixdraftclient.Input{
		SourceProjectID: facts.SourceProjectID, CampaignID: facts.CampaignID, Revision: facts.Revision,
		AssetID: out.PlatformAssetID, AssetHash: out.ResultSHA256, GrantRef: facts.GrantRef,
		TenantID: p.Tenant(), BrandID: facts.BrandID, PayerID: facts.PayerID,
		LicenseRef: req.LicenseRef, LicenseExpiresAt: licExp,
		ReturnPath: "/projects/" + projID,
	}
	draft, err := s.MatrixDrafts.Create(ctx, in)
	if err != nil {
		writeMatrixDraftErr(w, err)
		return
	}
	factsJSON, _ := json.Marshal(in)
	ref, _, err := s.St.PutMatrixDraftRef(ctx, store.MatrixDraftRef{
		TenantScope: p.Tenant(), ProjectID: projID, OutputID: outputID, DraftID: draft.ID,
		LicenseRef: putLicRef, LicenseExpiresAt: putLicExp,
		QualityVerdict: facts.QualityVerdict, BriefVersion: out.BriefVersion,
		SourceRevision: out.SourceRevision, AssetSHA256: out.ResultSHA256,
		FactsJSON: string(factsJSON),
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	status := http.StatusCreated
	if draft.IdempotentReplay {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"matrix_draft": draft, "ref": ref, "source_facts": facts})
}

// handleGetMatrixDraft GET /api/v1/projects/{id}/outputs/{outputId}/matrix-draft
// 纯回查:本端不创建 ref,目的端 GET 也不创建草稿。导航与刷新走这里,零副作用。
func (s *Server) handleGetMatrixDraft(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	ctx := r.Context()
	projID, outputID := r.PathValue("id"), r.PathValue("outputId")
	if _, err := s.St.GetProject(ctx, p.Tenant(), projID); err != nil {
		writeStoreErr(w, err)
		return
	}
	ref, err := s.St.GetMatrixDraftRef(ctx, p.Tenant(), projID, outputID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "no_matrix_draft", "该输出版本尚未送矩阵(查询不创建草稿)")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if s.MatrixDrafts == nil {
		writeErr(w, http.StatusServiceUnavailable, "matrix_draft_unconfigured", "矩阵持久草稿服务未配置")
		return
	}
	draft, derr := s.MatrixDrafts.Get(ctx, p.Tenant(), ref.DraftID)
	if derr != nil {
		writeMatrixDraftErr(w, derr)
		return
	}
	receipt, rerr := s.MatrixDrafts.Receipt(ctx, p.Tenant(), ref.DraftID)
	if rerr != nil {
		writeMatrixDraftErr(w, rerr)
		return
	}
	var facts any
	_ = json.Unmarshal([]byte(ref.FactsJSON), &facts)
	writeJSON(w, http.StatusOK, map[string]any{
		"matrix_draft": draft, "receipt": receipt, "ref": ref, "source_facts": facts,
	})
}

// handleListMatrixDrafts GET /api/v1/projects/{id}/matrix-drafts
// 列出工程下已送矩阵的登记(纯读,零创建)。
func (s *Server) handleListMatrixDrafts(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	refs, err := s.St.ListMatrixDraftRefs(r.Context(), p.Tenant(), r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matrix_drafts": refs})
}

// matrixFacts 是送矩阵的服务端事实集(全部来自已持久状态或确定性派生)。
type matrixFacts struct {
	SourceProjectID string `json:"source_project_id"`
	CampaignID      string `json:"campaign_id,omitempty"`
	Revision        int    `json:"revision"`
	GrantRef        string `json:"grant_ref"`
	BrandID         string `json:"brand_id"`
	PayerID         string `json:"payer_id"`
	QualityVerdict  string `json:"quality_verdict"`
	SourceKind      string `json:"source_kind"`
}

// collectMatrixFacts 汇集目的端要求的事实并执行源端门序。
func (s *Server) collectMatrixFacts(ctx context.Context, p platform.Principal, projID, outputID string) (store.ProjectOutput, matrixFacts, *apiErr) {
	var zero store.ProjectOutput
	if _, err := s.St.GetProject(ctx, p.Tenant(), projID); err != nil {
		return zero, matrixFacts{}, &apiErr{http.StatusNotFound, "not_found", "资源不存在(或不属于当前账号)"}
	}
	out, err := s.St.GetOutput(ctx, p.Tenant(), outputID)
	if err != nil || out.ProjectID != projID {
		return zero, matrixFacts{}, &apiErr{http.StatusNotFound, "not_found", "输出不存在(或不属于当前账号)"}
	}
	// 能力类型匹配:矩阵的 product-image 通道只收图片(目的端 content_type=image 契约)。
	if out.MediaType != "image/png" {
		return zero, matrixFacts{}, &apiErr{http.StatusConflict, "wrong_content_type", "矩阵通道按契约只收图片(image),非图片不得送矩阵"}
	}
	facts := matrixFacts{SourceKind: "standalone", Revision: 1}
	// 来源事实:有绑定的工程用来源绑定;standalone 以本工程为源(proj_<hex> 合目的端 idPattern)。
	facts.SourceProjectID = projID
	facts.GrantRef = grantStandIn(p.Tenant())
	if binding, berr := s.St.GetBindingByProject(ctx, p.Tenant(), projID); berr == nil {
		facts.SourceProjectID = binding.SourceRef
		facts.SourceKind = "bound"
		if snap, serr := s.St.GetSnapshot(ctx, p.Tenant(), binding.CurrentSnapshotID); serr == nil {
			var profile struct {
				SourceKind  string `json:"source_kind"`
				CampaignRef string `json:"campaign_ref"`
			}
			if json.Unmarshal([]byte(snap.ProfileJSON), &profile) == nil && profile.SourceKind == "campaign" {
				facts.SourceKind = "campaign"
				facts.CampaignID = profile.CampaignRef
				// campaign 来源:必须先明确选定该版(R14d:诚实拒绝面不动)。
				if aerr := s.rejectUnselectedCampaignReceipt(ctx, p.Tenant(), projID, out); aerr != nil {
					return zero, matrixFacts{}, aerr
				}
			}
			rev, rerr := snapshotOrdinal(s, ctx, p.Tenant(), binding.ID, out.SnapshotID)
			if rerr != nil {
				return zero, matrixFacts{}, &apiErr{http.StatusInternalServerError, "internal", "服务内部错误"}
			}
			facts.Revision = rev
		}
	} else if !errors.Is(berr, store.ErrNotFound) {
		return zero, matrixFacts{}, &apiErr{http.StatusInternalServerError, "internal", "服务内部错误"}
	}
	// 质量随版本保持:有质量绑定且可用 → limited;否则 unknown(不在草稿里美化)。
	if q, ok, qerr := s.St.GetOutputQuality(ctx, p.Tenant(), outputID); qerr == nil && ok {
		if q.CandidateState != si.CandidateLimited || q.ResultSHA256 != out.ResultSHA256 {
			return zero, matrixFacts{}, &apiErr{http.StatusConflict, unusableCandidateCode, unusableCandidateMsg}
		}
		facts.QualityVerdict = "limited"
	} else if qerr != nil {
		return zero, matrixFacts{}, &apiErr{http.StatusInternalServerError, "internal", "服务内部错误"}
	} else {
		if _, owned, oerr := s.St.PlateDerivationOwnsSHA(ctx, p.Tenant(), projID, out.ResultSHA256); oerr == nil && owned {
			return zero, matrixFacts{}, &apiErr{http.StatusConflict, unusableCandidateCode, unusableCandidateMsg}
		}
		facts.QualityVerdict = "unknown"
	}
	facts.BrandID = brandStandIn(p.Tenant())
	facts.PayerID = p.AccountID
	return out, facts, nil
}

// licenseExpiresEqual 比较许可到期时刻:两端都按 RFC3339 解析成 UTC 后比时刻
// (同一时刻的不同写法,如 +08:00 vs Z,不算许可事实不一致);任一端解析失败
// 回退原串比较。存储仍保留各自请求原串,不做改写。
func licenseExpiresEqual(stored, incoming string) bool {
	if stored == incoming {
		return true
	}
	st, serr := time.Parse(time.RFC3339, stored)
	it, ierr := time.Parse(time.RFC3339, incoming)
	if serr != nil || ierr != nil {
		return false
	}
	return st.Equal(it)
}

// snapshotOrdinal 需求版本序(1 起,旧→新):把输出所在的快照放到绑定快照时间线的位置。
// 无快照上下文的旧输出(snapshotID 为空/时间线为空/不在时间线上)按第 1 版;
// 快照时间线读取失败则显式上抛——revision 是目的端幂等键成分,store 故障时
// 不得静默降级编造送出事实(R-58 F-3)。
func snapshotOrdinal(s *Server, ctx context.Context, tenant, bindingID, snapshotID string) (int, error) {
	if snapshotID == "" {
		return 1, nil
	}
	snaps, err := s.St.ListSnapshotsForBinding(ctx, tenant, bindingID)
	if err != nil {
		return 0, err
	}
	if len(snaps) == 0 {
		return 1, nil
	}
	// 列表新→旧;位置 i(0 起)的旧序 = len-i。
	for i, sn := range snaps {
		if sn.ID == snapshotID {
			return len(snaps) - i, nil
		}
	}
	return 1, nil
}

// brandStandIn AG01 品牌注册接入前的确定性 stand-in(同一租户恒同一值,可审计可替换)。
func brandStandIn(tenant string) string {
	sum := sha256.Sum256([]byte("brand:" + tenant))
	return "brand-" + hex.EncodeToString(sum[:])[:12]
}

// grantStandIn AG01 授权 grant 接入前的确定性 stand-in;目的端 GrantFacts 未登记时
// fail-closed 403 grant_unknown(撤权/未授权挡新建,正是消费方须知语义)。
func grantStandIn(tenant string) string {
	sum := sha256.Sum256([]byte("grant:" + tenant))
	return "grant-" + hex.EncodeToString(sum[:])[:12]
}

// writeMatrixDraftErr 把目的端结构化错误上浮(状态码+错误码不改口径)。
// 透传面类型化校验(R-58 F-2):目的端契约要求错误必须走 ≥400;客户端若拿到
// 2xx/3xx 却解析不出契约 body(以 Status<400 的 APIError 表达),说明目的端违约——
// 压成 502 显式失败,绝不把非错误状态码原样透传(HTTP 200+错误体会被 web 侧
// res.ok 判成"草稿已落库"的假成功)。
func writeMatrixDraftErr(w http.ResponseWriter, err error) {
	api, ok := err.(*matrixdraftclient.APIError)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "internal", "服务内部错误")
		return
	}
	if api.Status == 0 {
		writeErr(w, http.StatusServiceUnavailable, "matrix_draft_unreachable",
			"矩阵持久草稿服务不可达,已拒绝本次请求(不伪造草稿):"+api.Message)
		return
	}
	if api.Status < 400 {
		writeErr(w, http.StatusBadGateway, "matrix_draft_bad_response",
			"矩阵草稿服务返回契约外响应,已按失败处理(不冒成功):"+api.Message)
		return
	}
	writeErr(w, api.Status, api.Code, fmt.Sprintf("矩阵草稿服务拒绝:%s", api.Message))
}
