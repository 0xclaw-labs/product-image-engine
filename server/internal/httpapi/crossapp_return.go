package httpapi

// HUI-1745 跨应用原生工程续接与不可变候选回流/回执 HTTP 接口。
//
// 本文件采用纯增量接线，保持既有 /api/v1/handoffs/*、/api/v1/projects/{id}/outputs/{outputId}/receipt、
// /api/v1/projects/{id}/campaign-restore、/api/v1/projects/{id}/campaign-selection 路由及
// 既有 49/49 订单回流与默认登记表闭环基线 100% 不变。

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/product-image-engine/server/internal/crossappreturn"
	"github.com/bianjiefilm/product-image-engine/server/internal/fidelity"
	"github.com/bianjiefilm/product-image-engine/server/internal/handoff"
	si "github.com/bianjiefilm/product-image-engine/server/internal/sourceimage"
	"github.com/bianjiefilm/product-image-engine/server/internal/store"
)

func (s *Server) crossAppCoordinator() *crossappreturn.Coordinator {
	s.crossAppMu.Lock()
	defer s.crossAppMu.Unlock()
	if s.CrossApp == nil {
		s.CrossApp = crossappreturn.NewCoordinator()
	}
	return s.CrossApp
}

func (s *Server) crossAppDeliveryOpts() crossappreturn.DeliveryOptions {
	appID := strings.TrimSpace(s.Cfg.AppID)
	if appID == "" {
		appID = "product-image"
	}
	return crossappreturn.DeliveryOptions{
		SourceApp: appID,
		Registry:  s.Registry,
		ReceiptSecretForApp: func(targetApp string) string {
			return s.Cfg.ReceiptKeyFor(targetApp)
		},
		BearerTokenForApp: func(targetApp string) string {
			if targetApp == "orders" {
				return s.Cfg.OrderInternalToken
			}
			return ""
		},
		HTTPClient: s.ImageHTTP,
	}
}

func writeCrossAppErr(w http.ResponseWriter, err error, record *crossappreturn.DeliveryRecord) {
	if ce, ok := crossappreturn.AsContractError(err); ok {
		errObj := map[string]string{
			"code":    ce.Code,
			"message": ce.Message,
		}
		if ce.RecoveryAction != "" {
			errObj["recovery_action"] = ce.RecoveryAction
		}
		body := map[string]any{
			"error":        errObj,
			"side_effects": ce.SideEffects,
		}
		if record != nil && record.ReceiptID != "" {
			body["record"] = record
		}
		writeJSON(w, ce.HTTPStatus, body)
		return
	}
	writeErr(w, http.StatusInternalServerError, "internal", "服务内部错误")
}

// handleCrossAppContinue POST /api/v1/crossapp/continue
// 原生工程续接：订单 / 活动(无订单) / 独立 / Bot / 通知返回直接续接既有 project_id/task_id，
// 不新建第二工程、不伪造订单绕权限、不以 latest 偷换固定 asset_version、访客触碰零触发生成。
func (s *Server) handleCrossAppContinue(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tenant := p.Tenant()

	var req crossappreturn.ContinuationRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是合法 JSON")
		return
	}
	// 访客触碰/浏览优先 fail-closed，绝不触发生成或工程续接写入。
	if crossappreturn.IsVisitorContext(req.ActorRole, req.EntryTrigger) {
		writeCrossAppErr(w, &crossappreturn.ContractError{
			HTTPStatus:     http.StatusForbidden,
			Code:           crossappreturn.CodeVisitorGenerationBlocked,
			Message:        "访客触碰/浏览只允许读取已采用内容，严禁发起产品图生成或工程续接",
			RecoveryAction: crossappreturn.RecoveryMerchantEntryOnly,
			SideEffects:    crossappreturn.ZeroSideEffects(),
		}, nil)
		return
	}
	if strings.TrimSpace(req.TenantID) != "" && strings.TrimSpace(req.TenantID) != tenant {
		writeCrossAppErr(w, &crossappreturn.ContractError{
			HTTPStatus:  http.StatusForbidden,
			Code:        crossappreturn.CodeTenantMismatch,
			Message:     "请求租户与当前已认证账号不匹配，拒绝跨租户续接",
			SideEffects: crossappreturn.ZeroSideEffects(),
		}, nil)
		return
	}
	req.TenantID = tenant

	projID := strings.TrimSpace(req.ProjectID)
	if projID == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "project_id 必填（原工程续接不新建工程）")
		return
	}

	ctx := r.Context()
	proj, err := s.St.GetProject(ctx, tenant, projID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}

	var boundPtr *store.SourceBinding
	if binding, berr := s.St.GetBindingByProject(ctx, tenant, projID); berr == nil {
		boundPtr = &binding
		if snap, serr := s.St.GetSnapshot(ctx, tenant, binding.CurrentSnapshotID); serr == nil {
			var sc handoff.SourceContext
			_ = json.Unmarshal([]byte(snap.ProfileJSON), &sc)
			if strings.TrimSpace(req.SourceApp) == "" {
				req.SourceApp = snap.SourceApp
			}
			if strings.TrimSpace(req.SourceKind) == "" {
				req.SourceKind = snap.SourceKind
			}
			// 权威来源核对：已绑定工程为非订单来源时禁止伪造 order_id；为订单来源时必须匹配原 order_ref。
			if snap.SourceKind == crossappreturn.SourceOrder {
				if strings.TrimSpace(req.OrderID) == "" {
					req.OrderID = sc.OrderRef
				} else if sc.OrderRef != "" && strings.TrimSpace(req.OrderID) != sc.OrderRef {
					writeCrossAppErr(w, &crossappreturn.ContractError{
						HTTPStatus:  http.StatusConflict,
						Code:        crossappreturn.CodeProjectContinuationFailed,
						Message:     "传入 order_id 与原工程已绑定订单不一致，拒绝串单续接",
						SideEffects: crossappreturn.ZeroSideEffects(),
					}, nil)
					return
				}
			} else if snap.SourceKind == crossappreturn.SourceCampaign {
				if strings.TrimSpace(req.OrderID) != "" {
					writeCrossAppErr(w, &crossappreturn.ContractError{
						HTTPStatus:     http.StatusBadRequest,
						Code:           crossappreturn.CodeFakeOrderForbidden,
						Message:        "活动工程禁止伪造 order_id 绕过授权校验",
						RecoveryAction: crossappreturn.RecoveryReauthorizeGrant,
						SideEffects:    crossappreturn.ZeroSideEffects(),
					}, nil)
					return
				}
				if strings.TrimSpace(req.CampaignID) == "" {
					req.CampaignID = sc.CampaignRef
				} else if sc.CampaignRef != "" && strings.TrimSpace(req.CampaignID) != sc.CampaignRef {
					writeCrossAppErr(w, &crossappreturn.ContractError{
						HTTPStatus:  http.StatusConflict,
						Code:        crossappreturn.CodeProjectContinuationFailed,
						Message:     "传入 campaign_id 与原工程已绑定活动不一致，拒绝串活动续接",
						SideEffects: crossappreturn.ZeroSideEffects(),
					}, nil)
					return
				}
			}
			if strings.TrimSpace(req.SourceVersion) == "" {
				req.SourceVersion = combineSourceVersion(snap.SourceRevision, snap.BriefVersion)
			}
			if req.SourceVersionSeq <= 0 {
				if ord, oerr := snapshotOrdinal(s, ctx, tenant, binding.ID, snap.ID); oerr == nil && ord > 0 {
					req.SourceVersionSeq = int64(ord)
				}
			}
			if len(req.Permission.Scopes) == 0 && len(sc.Scopes) > 0 {
				req.Permission.Scopes = append([]string(nil), sc.Scopes...)
			}
			if strings.TrimSpace(req.Permission.PayerPrincipalID) == "" {
				req.Permission.PayerPrincipalID = snap.PrincipalID
			}
		}
	} else if !errors.Is(berr, store.ErrNotFound) {
		writeStoreErr(w, berr)
		return
	}

	if strings.TrimSpace(req.SourceKind) == "" {
		if proj.SourceType != "" {
			req.SourceKind = proj.SourceType
		} else {
			req.SourceKind = crossappreturn.SourceStandalone
		}
	}
	if strings.TrimSpace(req.SourceApp) == "" {
		req.SourceApp = s.Cfg.AppID
	}
	if strings.TrimSpace(req.BrandID) == "" {
		req.BrandID = brandStandIn(tenant)
	}
	if strings.TrimSpace(req.UsageScope) == "" {
		if strings.TrimSpace(proj.UsageKind) != "" {
			req.UsageScope = proj.UsageKind
		} else {
			req.UsageScope = "product_image"
		}
	}
	if strings.TrimSpace(req.Permission.GrantID) == "" {
		req.Permission.GrantID = grantStandIn(tenant)
	}
	if strings.TrimSpace(req.Permission.UsageScope) == "" {
		req.Permission.UsageScope = req.UsageScope
	}
	if strings.TrimSpace(req.SourceVersion) == "" {
		req.SourceVersion = "v1"
	}

	cont, err := s.crossAppCoordinator().ContinueProject(req)
	if err != nil {
		writeCrossAppErr(w, err, nil)
		return
	}
	resp := map[string]any{
		"continuation": cont,
		"project":      proj,
		"side_effects": cont.SideEffects,
	}
	if boundPtr != nil {
		resp["binding"] = boundPtr
	}
	writeJSON(w, http.StatusOK, resp)
}

type httpCandidateReturnReq struct {
	ReceiptID          string                         `json:"receipt_id,omitempty"`
	BrandID            string                         `json:"brand_id,omitempty"`
	SourceKind         string                         `json:"source_kind,omitempty"`
	OrderID            string                         `json:"order_id,omitempty"`
	CampaignID         string                         `json:"campaign_id,omitempty"`
	TaskID             string                         `json:"task_id,omitempty"`
	AssetVersion       string                         `json:"asset_version,omitempty"`
	UsageScope         string                         `json:"usage_scope,omitempty"`
	Permission         crossappreturn.PermissionGrant `json:"permission,omitempty"`
	SourceVersion      string                         `json:"source_version,omitempty"`
	SourceVersionSeq   int64                          `json:"source_version_seq,omitempty"`
	TargetApp          string                         `json:"target_app,omitempty"`
	TargetID           string                         `json:"target_id,omitempty"`
	DestinationKind    string                         `json:"destination_kind,omitempty"`
	SignedAccessURL    string                         `json:"signed_access_url,omitempty"`
	SignedURLExpiresAt string                         `json:"signed_url_expires_at,omitempty"`
	ActorRole          string                         `json:"actor_role,omitempty"`
	EntryTrigger       string                         `json:"entry_trigger,omitempty"`
	RawImagePayload    string                         `json:"raw_image_payload,omitempty"`
	FakeOrderBypass    bool                           `json:"fake_order_bypass,omitempty"`
}

// handleCandidateReturn POST /api/v1/projects/{id}/outputs/{outputId}/candidate-return
// 不可变候选成果回流与真实回执投递：仅向应用登记表已登记目标真实投递；
// 未登记目标显式返回 409 target_not_registered（绝不代签成功）；
// 重复回流幂等、旧版本晚到不覆盖已采用新版、撤权(403 revoked)/链接过期(410 url_expired) fail-closed。
func (s *Server) handleCandidateReturn(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tenant := p.Tenant()
	ctx := r.Context()
	projID, outputID := r.PathValue("id"), r.PathValue("outputId")

	var req httpCandidateReturnReq
	if r.Body != nil {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是合法 JSON")
			return
		}
	}

	if crossappreturn.IsVisitorContext(req.ActorRole, req.EntryTrigger) {
		writeCrossAppErr(w, &crossappreturn.ContractError{
			HTTPStatus:     http.StatusForbidden,
			Code:           crossappreturn.CodeVisitorGenerationBlocked,
			Message:        "访客触碰/浏览禁止发起产品图生成或候选回流写入",
			RecoveryAction: crossappreturn.RecoveryMerchantEntryOnly,
			SideEffects:    crossappreturn.ZeroSideEffects(),
		}, nil)
		return
	}
	if strings.TrimSpace(req.RawImagePayload) != "" {
		writeCrossAppErr(w, &crossappreturn.ContractError{
			HTTPStatus:  http.StatusBadRequest,
			Code:        crossappreturn.CodePlaintextImageForbidden,
			Message:     "跨应用候选回流事件严禁携带图片明文，请仅传递已登记的不可变 asset_ref/asset_version 引用",
			SideEffects: crossappreturn.ZeroSideEffects(),
		}, nil)
		return
	}

	proj, err := s.St.GetProject(ctx, tenant, projID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	out, err := s.St.GetOutput(ctx, tenant, outputID)
	if err != nil || out.ProjectID != projID {
		writeErr(w, http.StatusNotFound, "not_found", "输出不存在(或不属于当前账号)")
		return
	}

	// 保真与质量门校验（与既有回执门保持一致，未保真候选绝不可回流）。
	quality, hasQuality, qerr := s.St.GetOutputQuality(ctx, tenant, outputID)
	if qerr != nil {
		writeStoreErr(w, qerr)
		return
	}
	if hasQuality && (quality.CandidateState != si.CandidateLimited || quality.ResultSHA256 != out.ResultSHA256) {
		rejectUnusableCandidate(w)
		return
	}
	if !hasQuality {
		if _, owned, oerr := s.St.PlateDerivationOwnsSHA(ctx, tenant, projID, out.ResultSHA256); oerr != nil {
			writeStoreErr(w, oerr)
			return
		} else if owned {
			rejectUnusableCandidate(w)
			return
		}
	}
	if reports, rerr := s.St.ListFidelityReportsByOutput(ctx, tenant, outputID); rerr != nil {
		writeStoreErr(w, rerr)
		return
	} else if block, code, msg := fidelity.BlocksVerifiedReceipt(fidelityReports(reports)); block {
		writeErr(w, http.StatusConflict, code, msg)
		return
	}

	sourceKind := strings.TrimSpace(req.SourceKind)
	orderID := strings.TrimSpace(req.OrderID)
	campaignID := strings.TrimSpace(req.CampaignID)
	targetApp := strings.TrimSpace(req.TargetApp)
	sourceVersion := strings.TrimSpace(req.SourceVersion)
	sourceVersionSeq := req.SourceVersionSeq
	perm := req.Permission

	// 从权威绑定/快照补齐服务端事实（不信任客户端伪造来源上下文）。
	if binding, berr := s.St.GetBindingByProject(ctx, tenant, projID); berr == nil {
		if targetApp == "" {
			targetApp = binding.SourceApp
		}
		var snap store.HandoffSnapshot
		var hasSnap bool
		if out.SnapshotID != "" {
			if sRow, serr := s.St.GetSnapshot(ctx, tenant, out.SnapshotID); serr == nil {
				snap = sRow
				hasSnap = true
			}
		}
		if !hasSnap && binding.CurrentSnapshotID != "" {
			if sRow, serr := s.St.GetSnapshot(ctx, tenant, binding.CurrentSnapshotID); serr == nil {
				snap = sRow
				hasSnap = true
			}
		}
		if hasSnap {
			// 若绑定存在更新快照，则旧快照下产出的候选回流按 stale_source_version fail-closed 拒绝。
			if out.SnapshotID != "" {
				if latest, lerr := s.St.ListSnapshotsForBinding(ctx, tenant, binding.ID); lerr == nil && len(latest) > 0 && latest[0].ID != out.SnapshotID {
					writeCrossAppErr(w, &crossappreturn.ContractError{
						HTTPStatus:     http.StatusConflict,
						Code:           crossappreturn.CodeStaleSourceVersion,
						Message:        "所选输出来自旧来源版本(" + combineSourceVersion(out.SourceRevision, out.BriefVersion) + ")，已存在更新版本，禁止覆盖",
						RecoveryAction: crossappreturn.RecoveryAdoptCurrentVersion,
						SideEffects:    crossappreturn.ZeroSideEffects(),
					}, nil)
					return
				}
				if binding.CurrentSnapshotID != "" && binding.CurrentSnapshotID != out.SnapshotID {
					writeCrossAppErr(w, &crossappreturn.ContractError{
						HTTPStatus:     http.StatusConflict,
						Code:           crossappreturn.CodeStaleSourceVersion,
						Message:        "所选输出来自旧来源版本，当前已采用新版本，禁止覆盖",
						RecoveryAction: crossappreturn.RecoveryAdoptCurrentVersion,
						SideEffects:    crossappreturn.ZeroSideEffects(),
					}, nil)
					return
				}
			}
			var sc handoff.SourceContext
			_ = json.Unmarshal([]byte(snap.ProfileJSON), &sc)
			if sourceKind == "" {
				sourceKind = snap.SourceKind
			}
			if snap.SourceKind == crossappreturn.SourceCampaign {
				// 活动来源必须先明确选定该输出版本。
				if aerr := s.rejectUnselectedCampaignReceipt(ctx, tenant, projID, out); aerr != nil {
					aerr.write(w)
					return
				}
				if orderID != "" {
					writeCrossAppErr(w, &crossappreturn.ContractError{
						HTTPStatus:     http.StatusBadRequest,
						Code:           crossappreturn.CodeFakeOrderForbidden,
						Message:        "活动回流禁止伪造 order_id 绕过授权校验",
						RecoveryAction: crossappreturn.RecoveryReauthorizeGrant,
						SideEffects:    crossappreturn.ZeroSideEffects(),
					}, nil)
					return
				}
				if campaignID == "" {
					campaignID = sc.CampaignRef
				}
			} else if snap.SourceKind == crossappreturn.SourceOrder {
				if orderID == "" {
					orderID = sc.OrderRef
				}
			}
			if sourceVersion == "" {
				sourceVersion = combineSourceVersion(snap.SourceRevision, snap.BriefVersion)
			}
			if sourceVersionSeq <= 0 {
				if ord, oerr := snapshotOrdinal(s, ctx, tenant, binding.ID, snap.ID); oerr == nil && ord > 0 {
					sourceVersionSeq = int64(ord)
				}
			}
			if len(perm.Scopes) == 0 && len(sc.Scopes) > 0 {
				perm.Scopes = append([]string(nil), sc.Scopes...)
			}
			if strings.TrimSpace(perm.PayerPrincipalID) == "" {
				perm.PayerPrincipalID = snap.PrincipalID
			}
		}
	} else if !errors.Is(berr, store.ErrNotFound) {
		writeStoreErr(w, berr)
		return
	}

	if sourceKind == "" {
		if proj.SourceType != "" {
			sourceKind = proj.SourceType
		} else {
			sourceKind = crossappreturn.SourceStandalone
		}
	}
	brandID := strings.TrimSpace(req.BrandID)
	if brandID == "" {
		brandID = brandStandIn(tenant)
	}
	taskID := strings.TrimSpace(req.TaskID)
	if taskID == "" {
		if hasQuality && quality.RunID != "" {
			taskID = quality.RunID
		} else if out.PlatformTaskID != "" {
			taskID = out.PlatformTaskID
		} else {
			taskID = "output-" + out.ID
		}
	}
	assetVersion := strings.TrimSpace(req.AssetVersion)
	if assetVersion == "" {
		shortSHA := out.ResultSHA256
		if len(shortSHA) > 12 {
			shortSHA = shortSHA[:12]
		}
		assetVersion = out.ID + ":" + shortSHA
	}
	usageScope := strings.TrimSpace(req.UsageScope)
	if usageScope == "" {
		if sourceKind == crossappreturn.SourceCampaign {
			usageScope = crossappreturn.DestinationTouchContentPack
		} else if strings.TrimSpace(proj.UsageKind) != "" {
			usageScope = proj.UsageKind
		} else {
			usageScope = "product_image"
		}
	}
	if strings.TrimSpace(perm.GrantID) == "" {
		perm.GrantID = grantStandIn(tenant)
	}
	if strings.TrimSpace(perm.Status) == "" {
		perm.Status = crossappreturn.PermissionActive
	}
	if strings.TrimSpace(perm.UsageScope) == "" {
		perm.UsageScope = usageScope
	}
	if strings.TrimSpace(perm.PayerPrincipalID) == "" {
		perm.PayerPrincipalID = p.AccountID
	}
	if sourceVersion == "" {
		sourceVersion = combineSourceVersion(out.SourceRevision, out.BriefVersion)
	}
	if sourceVersion == "" {
		sourceVersion = "v1"
	}

	outcome, err := s.crossAppCoordinator().ReturnCandidate(ctx, crossappreturn.CandidateReturnRequest{
		ReceiptID:          req.ReceiptID,
		TenantID:           tenant,
		BrandID:            brandID,
		SourceKind:         sourceKind,
		OrderID:            orderID,
		CampaignID:         campaignID,
		ProjectID:          projID,
		TaskID:             taskID,
		OutputID:           out.ID,
		AssetRef:           out.PlatformAssetID,
		AssetVersion:       assetVersion,
		AssetSHA256:        out.ResultSHA256,
		AssetSizeBytes:     out.ResultSize,
		MediaType:          out.MediaType,
		UsageScope:         usageScope,
		Permission:         perm,
		SourceVersion:      sourceVersion,
		SourceVersionSeq:   sourceVersionSeq,
		TargetApp:          targetApp,
		TargetID:           req.TargetID,
		DestinationKind:    req.DestinationKind,
		SignedAccessURL:    req.SignedAccessURL,
		SignedURLExpiresAt: req.SignedURLExpiresAt,
		ActorRole:          req.ActorRole,
		EntryTrigger:       req.EntryTrigger,
		RawImagePayload:    req.RawImagePayload,
		FakeOrderBypass:    req.FakeOrderBypass,
	}, s.crossAppDeliveryOpts())
	if err != nil {
		var recPtr *crossappreturn.DeliveryRecord
		if outcome.Record.ReceiptID != "" {
			recPtr = &outcome.Record
		}
		writeCrossAppErr(w, err, recPtr)
		return
	}

	status := http.StatusCreated
	if outcome.Duplicate {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{
		"record":       outcome.Record,
		"duplicate":    outcome.Duplicate,
		"retried":      outcome.Retried,
		"side_effects": outcome.SideEffects,
	})
}

// handleAdoptCandidateReturn POST /api/v1/projects/{id}/outputs/{outputId}/candidate-return/adopt
// 商家/接收端明确采用已送达候选版本，锁定单调 source_version 水位（防止晚到旧版本覆盖）。
func (s *Server) handleAdoptCandidateReturn(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tenant := p.Tenant()
	projID, outputID := r.PathValue("id"), r.PathValue("outputId")

	var req struct {
		ReceiptID    string `json:"receipt_id"`
		AdoptedBy    string `json:"adopted_by,omitempty"`
		ActorRole    string `json:"actor_role,omitempty"`
		EntryTrigger string `json:"entry_trigger,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是 JSON 且含 receipt_id")
		return
	}
	if _, err := s.St.GetProject(r.Context(), tenant, projID); err != nil {
		writeStoreErr(w, err)
		return
	}
	out, err := s.St.GetOutput(r.Context(), tenant, outputID)
	if err != nil || out.ProjectID != projID {
		writeErr(w, http.StatusNotFound, "not_found", "输出不存在(或不属于当前账号)")
		return
	}
	adoptedBy := strings.TrimSpace(req.AdoptedBy)
	if adoptedBy == "" {
		adoptedBy = p.UserID
	}
	if adoptedBy == "" {
		adoptedBy = p.AccountID
	}
	res, err := s.crossAppCoordinator().AdoptCandidate(crossappreturn.AdoptRequest{
		TenantID:     tenant,
		ProjectID:    projID,
		OutputID:     outputID,
		ReceiptID:    req.ReceiptID,
		AdoptedBy:    adoptedBy,
		ActorRole:    req.ActorRole,
		EntryTrigger: req.EntryTrigger,
	})
	if err != nil {
		writeCrossAppErr(w, err, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record":       res.Record,
		"adopted":      res.Adopted,
		"duplicate":    res.Duplicate,
		"side_effects": res.SideEffects,
	})
}

// handleListCandidateReturns GET /api/v1/projects/{id}/candidate-returns
// 纯只读查询工程下的候选回流记录与续接绑定（零副作用）。
func (s *Server) handleListCandidateReturns(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	tenant := p.Tenant()
	projID := r.PathValue("id")
	if _, err := s.St.GetProject(r.Context(), tenant, projID); err != nil {
		writeStoreErr(w, err)
		return
	}
	coord := s.crossAppCoordinator()
	list := coord.ListDeliveriesByProject(tenant, projID)
	resp := map[string]any{
		"returns":      list,
		"side_effects": crossappreturn.ZeroSideEffects(),
	}
	if cont, ok := coord.GetContinuation(tenant, projID); ok {
		resp["continuation"] = cont
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleGetCandidateReturn GET /api/v1/candidate-returns/{receiptId}
// 按 receipt_id 回查不可变回执状态（租户隔离、零副作用）。
func (s *Server) handleGetCandidateReturn(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	rec, ok := s.crossAppCoordinator().GetDelivery(p.Tenant(), r.PathValue("receiptId"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "候选回流回执不存在(或不属于当前账号)")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record":       rec,
		"side_effects": crossappreturn.ZeroSideEffects(),
	})
}

// handleRetryCandidateReturn POST /api/v1/candidate-returns/{receiptId}/retry
// 幂等重传既有不可变候选回执：零重生成、零扣费、零自动发布、零发券；
// 重传前复核目标登记、权限撤销、签名链接有效期与新版本水位。
func (s *Server) handleRetryCandidateReturn(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	var req struct {
		SignedAccessURL    string `json:"signed_access_url,omitempty"`
		SignedURLExpiresAt string `json:"signed_url_expires_at,omitempty"`
		ActorRole          string `json:"actor_role,omitempty"`
		EntryTrigger       string `json:"entry_trigger,omitempty"`
	}
	if r.Body != nil {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是合法 JSON")
			return
		}
	}
	outcome, err := s.crossAppCoordinator().RetryDelivery(r.Context(), crossappreturn.RetryRequest{
		TenantID:           p.Tenant(),
		ReceiptID:          r.PathValue("receiptId"),
		SignedAccessURL:    req.SignedAccessURL,
		SignedURLExpiresAt: req.SignedURLExpiresAt,
		ActorRole:          req.ActorRole,
		EntryTrigger:       req.EntryTrigger,
	}, s.crossAppDeliveryOpts())
	if err != nil {
		var recPtr *crossappreturn.DeliveryRecord
		if outcome.Record.ReceiptID != "" {
			recPtr = &outcome.Record
		}
		writeCrossAppErr(w, err, recPtr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"record":       outcome.Record,
		"duplicate":    outcome.Duplicate,
		"retried":      outcome.Retried,
		"side_effects": outcome.SideEffects,
	})
}

func combineSourceVersion(rev, brief string) string {
	r := strings.TrimSpace(rev)
	b := strings.TrimSpace(brief)
	switch {
	case r != "" && b != "":
		return r + ":" + b
	case r != "":
		return r
	case b != "":
		return b
	default:
		return ""
	}
}
