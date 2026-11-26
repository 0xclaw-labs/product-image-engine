package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/i2v"
	"github.com/bianjiefilm/product-image-engine/server/internal/i2vcontract"
	"github.com/bianjiefilm/product-image-engine/server/internal/showcasevideo"
	"github.com/bianjiefilm/product-image-engine/server/internal/store"
)

func (s *Server) handleShowcaseVideoCapabilities(w http.ResponseWriter, r *http.Request) {
	qual := i2vcontract.DefaultNoAdapterDecision()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":               true,
		"image_required":        true,
		"camera_moves":          []string{showcasevideo.Move360, showcasevideo.MoveScene},
		"billing_label":         showcasevideo.BillingPendingLabel,
		"charged":               false,
		"billing_passed":        false,
		"production_authorized": false,
		"show_video":            false,
		"play_still":            false,
		"video_url":             "",
		"delivery_readiness":    showcasevideo.ReadinessInternal,
		"honesty":               showcasevideo.HonestyNotice,
		// HUI-1702 MX-OPS: no usable image-conditioned video adapter today.
		// UNKNOWN is explicit; T2V is never substituted.
		"i2v_capability":           i2v.Probe(""),
		"i2v_qualification":        qual,
		"basic_image_path_blocked": false,
		"execution_policy": map[string]any{
			"allowed_preferences":                i2vcontract.AllowedExecutionPreferences(),
			"arbitrary_priority_allowed":         false,
			"free_quota_auto_charge":             false,
			"free_tier_restricted_to_public_lane": true,
			"candidate_destination":              i2vcontract.CandidateDestinationMerchantLibrary,
			"auto_publish_allowed":               false,
			"auto_reward_allowed":                false,
		},
	})
}

func (s *Server) handleQualifyShowcaseVideo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelID                       string   `json:"model_id"`
		ProviderID                    string   `json:"provider_id"`
		AdapterID                     string   `json:"adapter_id"`
		AdapterRegistered             bool     `json:"adapter_registered"`
		Mode                          string   `json:"mode"`
		ImageConditioned              bool     `json:"image_conditioned"`
		EmpiricalImageBindingVerified bool     `json:"empirical_image_binding_verified"`
		VerifiedEvidenceRef           string   `json:"verified_evidence_ref"`
		IsFixture                     bool     `json:"is_fixture"`
		IsPlaceholder                 bool     `json:"is_placeholder"`
		EndpointURL                   string   `json:"endpoint_url"`
		DurationSeconds               int      `json:"duration_seconds"`
		AspectRatio                   string   `json:"aspect_ratio"`
		CapturedViewCount             int      `json:"captured_view_count"`
		CapturedAngles                []string `json:"captured_angles"`
		HasCapturedBackView           bool     `json:"has_captured_back_view"`
		ClaimTrue360                  bool     `json:"claim_true_360"`
		ClaimBackViewProven           bool     `json:"claim_back_view_proven"`
		ExecutionPreference           string   `json:"execution_preference"`
		PriorityNumber                *int     `json:"priority_number"`
		Priority                      *int     `json:"priority"`
		CustomPriority                string   `json:"custom_priority"`
		PayerType                     string   `json:"payer_type"`
		BillingAccountRef             string   `json:"billing_account_ref"`
		QuoteID                       string   `json:"quote_id"`
		PricingVersion                string   `json:"pricing_version"`
		EstimatedCostFen              int64    `json:"estimated_cost_fen"`
		FreeQuotaGrantID              string   `json:"free_quota_grant_id"`
		FreeQuotaExpiresAt            string   `json:"free_quota_expires_at"`
		FreeQuotaRemaining            int      `json:"free_quota_remaining"`
		AutoConvertToPaid             bool     `json:"auto_convert_to_paid"`
		TargetPoolClass               string   `json:"target_pool_class"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是 JSON")
		return
	}

	view := i2vcontract.ViewProvenance{
		CapturedViewCount:   req.CapturedViewCount,
		CapturedAngles:      req.CapturedAngles,
		HasCapturedBackView: req.HasCapturedBackView,
		ClaimTrue360:        req.ClaimTrue360,
		ClaimBackViewProven: req.ClaimBackViewProven,
	}

	// Enforce execution preference, arbitrary priority rejection, and free-tier boundaries
	// whenever any policy or billing field is supplied.
	prio := req.PriorityNumber
	if prio == nil {
		prio = req.Priority
	}
	if prio != nil || strings.TrimSpace(req.CustomPriority) != "" ||
		strings.TrimSpace(req.ExecutionPreference) != "" || strings.TrimSpace(req.PayerType) != "" ||
		req.AutoConvertToPaid || strings.TrimSpace(req.TargetPoolClass) != "" {
		pref := i2vcontract.ExecutionPreference(strings.TrimSpace(req.ExecutionPreference))
		if pref == "" {
			pref = i2vcontract.PrefImmediate
		}
		payer := i2vcontract.PayerType(strings.TrimSpace(req.PayerType))
		if payer == "" {
			payer = i2vcontract.PayerPersonal
		}
		var expiresAt *time.Time
		if rawExp := strings.TrimSpace(req.FreeQuotaExpiresAt); rawExp != "" {
			parsed, err := time.Parse(time.RFC3339, rawExp)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "invalid_request", "free_quota_expires_at 必须是 RFC3339 时间")
				return
			}
			expiresAt = &parsed
		}
		policyReq := i2vcontract.ExecutionPolicyRequest{
			SelectedPreference: pref,
			PriorityNumber:     prio,
			CustomPriority:     req.CustomPriority,
			TargetPoolClass:    i2vcontract.PoolClass(strings.TrimSpace(req.TargetPoolClass)),
		}
		cost := i2vcontract.CostAttribution{
			PayerType:          payer,
			BillingAccountRef:  req.BillingAccountRef,
			QuoteID:            req.QuoteID,
			PricingVersion:     req.PricingVersion,
			EstimatedCostFen:   req.EstimatedCostFen,
			FreeQuotaGrantID:   req.FreeQuotaGrantID,
			FreeQuotaExpiresAt: expiresAt,
			FreeQuotaRemaining: req.FreeQuotaRemaining,
			AutoConvertToPaid:  req.AutoConvertToPaid,
		}
		// Only require quote fields when payer/cost check was explicitly requested.
		if strings.TrimSpace(req.PayerType) == "" && payer == i2vcontract.PayerPersonal &&
			strings.TrimSpace(req.QuoteID) == "" && req.EstimatedCostFen == 0 {
			cost.QuoteID = "preflight"
			cost.PricingVersion = "preflight"
			cost.EstimatedCostFen = 1
			cost.BillingAccountRef = "preflight_account"
		}
		if blockCode, err := i2vcontract.ValidateExecutionAndBillingPolicy(policyReq, cost, time.Now().UTC()); err != nil {
			viewAssess, _ := i2vcontract.EvaluateViewProvenance(view)
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"qualification": i2vcontract.QualificationDecision{
					Status:                i2vcontract.QualificationBlocked,
					Eligible:              false,
					BlockCode:             blockCode,
					Reason:                err.Error(),
					ModelID:               strings.TrimSpace(req.ModelID),
					ProviderID:            strings.TrimSpace(req.ProviderID),
					ProviderCalls:         0,
					BasicImagePathBlocked: false,
					ViewAssessment:        viewAssess,
					QualityLimitations:    viewAssess.Limitations,
					Notice:                err.Error(),
				},
				"basic_image_path_blocked": false,
				"provider_calls":           0,
			})
			return
		}
	}

	// Server-side fail-closed: this deployment has no registered real I2V adapter,
	// so client claims of adapter_registered=true cannot bypass server truth.
	model := i2vcontract.ModelQualification{
		ModelID:                       req.ModelID,
		ProviderID:                    req.ProviderID,
		AdapterID:                     req.AdapterID,
		AdapterRegistered:             false,
		Mode:                          req.Mode,
		ImageConditioned:              req.ImageConditioned,
		EmpiricalImageBindingVerified: false,
		VerifiedEvidenceRef:           req.VerifiedEvidenceRef,
		IsFixture:                     req.IsFixture,
		IsPlaceholder:                 req.IsPlaceholder,
		EndpointURL:                   req.EndpointURL,
		TargetPoolClass:               i2vcontract.PoolClass(strings.TrimSpace(req.TargetPoolClass)),
	}
	decision, err := i2vcontract.EvaluateModelQualification(model, req.DurationSeconds, req.AspectRatio, view)
	status := http.StatusOK
	if err != nil {
		status = http.StatusUnprocessableEntity
	}
	payload := map[string]any{
		"qualification":            decision,
		"basic_image_path_blocked": false,
		"provider_calls":           0,
		"candidate_destination":    i2vcontract.CandidateDestinationMerchantLibrary,
		"auto_publish_allowed":     false,
		"auto_reward_allowed":      false,
	}
	if auditErr := i2vcontract.AuditNoInternalTokenLeak(payload); auditErr != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "响应安全校验失败")
		return
	}
	writeJSON(w, status, payload)
}

func (s *Server) handleCreateShowcaseVideo(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.showcaseProject(w, r)
	if !ok {
		return
	}
	var req struct {
		ProductImageID      string   `json:"product_image_id"`
		CameraMove          string   `json:"camera_move"`
		QuotedAmount        string   `json:"quoted_amount"`
		ExecutionPreference string   `json:"execution_preference"`
		PriorityNumber      *int     `json:"priority_number"`
		Priority            *int     `json:"priority"`
		CustomPriority      string   `json:"custom_priority"`
		ModelID             string   `json:"model_id"`
		ModelMode           string   `json:"model_mode"`
		CapturedViewCount   int      `json:"captured_view_count"`
		CapturedAngles      []string `json:"captured_angles"`
		HasCapturedBackView bool     `json:"has_captured_back_view"`
		ClaimTrue360        bool     `json:"claim_true_360"`
		ClaimBackViewProven bool     `json:"claim_back_view_proven"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是 JSON")
		return
	}
	// Reject arbitrary numeric/custom queue priority immediately.
	if req.PriorityNumber != nil || req.Priority != nil || strings.TrimSpace(req.CustomPriority) != "" {
		writeErr(w, http.StatusBadRequest, "arbitrary_priority_forbidden", i2vcontract.ErrArbitraryPriorityForbidden.Error())
		return
	}
	// Enforce server-allowed execution preference when specified.
	if prefRaw := strings.TrimSpace(req.ExecutionPreference); prefRaw != "" {
		pref := i2vcontract.ExecutionPreference(prefRaw)
		if pref != i2vcontract.PrefImmediate && pref != i2vcontract.PrefNight && pref != i2vcontract.PrefIdle {
			writeErr(w, http.StatusBadRequest, "invalid_execution_preference", i2vcontract.ErrInvalidExecutionPreference.Error())
			return
		}
	}
	// Reject single-image dishonest true-360 / proven-back-view claims.
	if _, err := i2vcontract.EvaluateViewProvenance(i2vcontract.ViewProvenance{
		CapturedViewCount:   req.CapturedViewCount,
		CapturedAngles:      req.CapturedAngles,
		HasCapturedBackView: req.HasCapturedBackView,
		ClaimTrue360:        req.ClaimTrue360,
		ClaimBackViewProven: req.ClaimBackViewProven,
		CameraMove:          req.CameraMove,
	}); errors.Is(err, i2vcontract.ErrTrue360ClaimForbidden) {
		writeErr(w, http.StatusBadRequest, "true_360_claim_forbidden", err.Error())
		return
	}
	// Reject explicit T2V masquerading attempts (e.g. h3-fast or mode=text_to_video).
	if strings.TrimSpace(req.ModelID) != "" || strings.TrimSpace(req.ModelMode) != "" {
		if _, err := i2vcontract.EvaluateModelQualification(
			i2vcontract.ModelQualification{
				ModelID: req.ModelID,
				Mode:    req.ModelMode,
			},
			5,
			"9:16",
			i2vcontract.ViewProvenance{CapturedViewCount: 1},
		); errors.Is(err, i2vcontract.ErrT2VMasqueradeForbidden) {
			writeErr(w, http.StatusUnprocessableEntity, "t2v_masquerade_forbidden", err.Error())
			return
		}
	}
	q, err := showcasevideo.Open(showcasevideo.OpenInput{
		TenantID: proj.TenantID, ProjectID: proj.ID, ImageID: req.ProductImageID, CameraMove: req.CameraMove,
		BillingConnected: false, QuotedAmount: "",
	})
	if errors.Is(err, showcasevideo.ErrImageRequired) {
		writeErr(w, http.StatusBadRequest, "image_required", err.Error())
		return
	}
	if errors.Is(err, showcasevideo.ErrMoveNotAllowed) {
		writeErr(w, http.StatusBadRequest, "camera_move_not_allowed", err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	kind, err := s.productImageKind(r.Context(), proj, q.ImageID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "image_not_found", "没有这张产品图，不能发起展示视频")
		return
	}
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if existing, ferr := s.St.FindShowcaseVideoByFingerprint(r.Context(), proj.TenantID, q.Fingerprint); ferr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(existing, true)})
		return
	} else if !errors.Is(ferr, store.ErrNotFound) {
		writeStoreErr(w, ferr)
		return
	}
	job, err := s.St.InsertShowcaseVideoJob(r.Context(), store.ShowcaseVideoJob{
		TenantID: proj.TenantID, ProjectID: proj.ID, ProductImageID: q.ImageID, ImageKind: kind,
		CameraMove: q.CameraMove, Fingerprint: q.Fingerprint, QuoteStatus: q.QuoteStatus,
		BillingLabel: q.BillingLabel, JobStatus: showcasevideo.StatusQuoted, Quality: showcasevideo.QualityUnknown,
		Pending: []string{"没有真实展示视频"},
	})
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"job": s.showcaseVideoView(job, false)})
}

func (s *Server) handleGetShowcaseVideoContract(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadShowcaseVideo(w, r)
	if !ok {
		return
	}
	assetID := job.ProductImageID
	versionID := job.ProductImageID
	sha256Hex := ""
	if job.ImageKind == "input" {
		if in, err := s.St.GetInput(r.Context(), job.TenantID, job.ProjectID, job.ProductImageID); err == nil {
			if strings.TrimSpace(in.PlatformAssetID) != "" {
				assetID = in.PlatformAssetID
			}
			versionID = in.ID
		}
	} else if job.ImageKind == "output" {
		if out, err := s.St.GetOutput(r.Context(), job.TenantID, job.ProductImageID); err == nil && out.ProjectID == job.ProjectID {
			if strings.TrimSpace(out.PlatformAssetID) != "" {
				assetID = out.PlatformAssetID
			}
			versionID = out.ID
			sha256Hex = out.ResultSHA256
		}
	}
	qual := i2vcontract.DefaultNoAdapterDecision()
	queueState := i2vcontract.QueueStateBlocked
	waitReason := i2vcontract.BlockNoRealI2VAdapter
	if job.JobStatus == showcasevideo.StatusQuoted {
		queueState = i2vcontract.QueueStateQueued
		waitReason = "awaiting_quote_confirmation_and_real_i2v_adapter"
	} else if job.JobStatus == showcasevideo.StatusFailed {
		queueState = i2vcontract.QueueStateFailed
		waitReason = i2vcontract.BlockNoRealI2VAdapter
	}
	payload := map[string]any{
		"task_id":                  job.ID,
		"project_id":               job.ProjectID,
		"fingerprint":              job.Fingerprint,
		"basic_image_path_blocked": false,
		"fixed_product_ref": map[string]any{
			"asset_id":   assetID,
			"version_id": versionID,
			"sha256":     sha256Hex,
			"image_kind": job.ImageKind,
		},
		"i2v_qualification": qual,
		"view_assessment":   qual.ViewAssessment,
		"queue_projection": i2vcontract.QueueProjection{
			JobID:               job.ID,
			QueueState:          queueState,
			WaitReason:          waitReason,
			NextWindowAt:        nil,
			ETASeconds:          nil,
			ActiveStage:         i2vcontract.StageGenerate,
			ExecutionPreference: i2vcontract.PrefImmediate,
			PayerType:           i2vcontract.PayerPersonal,
		},
		"candidate_return": map[string]any{
			"destination":        i2vcontract.CandidateDestinationMerchantLibrary,
			"auto_published":     false,
			"reward_triggered":   false,
			"merchant_confirmed": false,
		},
	}
	if err := i2vcontract.AuditNoInternalTokenLeak(payload); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "响应安全校验失败")
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleListShowcaseVideo(w http.ResponseWriter, r *http.Request) {
	proj, ok := s.showcaseProject(w, r)
	if !ok {
		return
	}
	jobs, err := s.St.ListShowcaseVideoJobs(r.Context(), proj.TenantID, proj.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "读取展示视频请求失败")
		return
	}
	views := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, s.showcaseVideoView(job, false))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs": views, "billing_label": showcasevideo.BillingPendingLabel,
		"show_video": false, "play_still": false, "video_url": "",
	})
}

func (s *Server) handleConfirmShowcaseVideo(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadShowcaseVideo(w, r)
	if !ok {
		return
	}
	if job.JobStatus != showcasevideo.StatusQuoted {
		writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(job, true)})
		return
	}
	job.QuoteStatus = showcasevideo.QuoteConfirmed
	if err := s.St.UpdateShowcaseVideoJob(r.Context(), job); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(job, false)})
}

func (s *Server) handleSubmitShowcaseVideo(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadShowcaseVideo(w, r)
	if !ok {
		return
	}
	if job.JobStatus == showcasevideo.StatusFailed || job.JobStatus == showcasevideo.StatusUnknown {
		writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(job, true)})
		return
	}
	if job.QuoteStatus != showcasevideo.QuoteConfirmed {
		writeErr(w, http.StatusConflict, "quote_blocked", showcasevideo.ErrQuoteUnconfirmed.Error())
		return
	}
	if job.JobStatus != showcasevideo.StatusQuoted {
		writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(job, true)})
		return
	}
	claimed, err := s.St.ClaimShowcaseVideoSubmit(r.Context(), job.TenantID, job.ID)
	if err != nil {
		writeStoreErr(w, err)
		return
	}
	if !claimed {
		latest, lerr := s.St.GetShowcaseVideoJob(r.Context(), job.TenantID, job.ProjectID, job.ID)
		if lerr != nil {
			writeStoreErr(w, lerr)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(latest, true)})
		return
	}
	job.JobStatus = showcasevideo.StatusSubmitting
	rec, err := showcasevideo.DecideSubmit(showcasevideo.SubmitInput{
		Quote: showcasevideo.Quote{
			ImageID: job.ProductImageID, CameraMove: job.CameraMove,
			Fingerprint: job.Fingerprint, BillingLabel: job.BillingLabel,
			QuoteStatus: job.QuoteStatus,
		},
	})
	if err != nil {
		writeErr(w, http.StatusConflict, "quote_blocked", err.Error())
		return
	}
	if rec.CallSupplier {
		rec.Status = showcasevideo.StatusFailed
	}
	job.JobStatus = rec.Status
	if job.JobStatus != showcasevideo.StatusFailed {
		job.JobStatus = showcasevideo.StatusFailed
	}
	job.Quality = rec.Quality
	job.PlatformTaskID = ""
	job.OutputAssetID = ""
	for _, item := range rec.Pending {
		job.Pending = appendPendingShowcase(job.Pending, item)
	}
	if err := s.St.UpdateShowcaseVideoJob(r.Context(), job); err != nil {
		writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(job, false)})
}

func (s *Server) handleRefreshShowcaseVideo(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadShowcaseVideo(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": s.showcaseVideoView(job, true)})
}

func (s *Server) handleClaimShowcaseVideo(w http.ResponseWriter, r *http.Request) {
	job, ok := s.loadShowcaseVideo(w, r)
	if !ok {
		return
	}
	var req struct {
		Claim         string `json:"claim"`
		OutputAssetID string `json:"output_asset_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", "请求体必须是 JSON")
		return
	}
	rec := showcasevideo.Record{ID: job.ID, Status: job.JobStatus, Quality: job.Quality, BillingLabel: job.BillingLabel}
	noted := showcasevideo.NoteOutputID(rec, req.OutputAssetID)
	if _, err := showcasevideo.ApplyClientClaim(noted, req.Claim); err != nil {
		writeErr(w, http.StatusConflict, "relabel_forbidden", showcasevideo.ErrRelabelForbidden.Error())
		return
	}
	writeErr(w, http.StatusConflict, "relabel_forbidden", showcasevideo.ErrRelabelForbidden.Error())
}

func (s *Server) productImageKind(ctx context.Context, proj store.Project, imageID string) (string, error) {
	if _, err := s.St.GetInput(ctx, proj.TenantID, proj.ID, imageID); err == nil {
		return "input", nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return "", err
	}
	out, err := s.St.GetOutput(ctx, proj.TenantID, imageID)
	if err != nil {
		return "", err
	}
	if out.ProjectID != proj.ID {
		return "", store.ErrNotFound
	}
	return "output", nil
}

func (s *Server) loadShowcaseVideo(w http.ResponseWriter, r *http.Request) (store.ShowcaseVideoJob, bool) {
	proj, ok := s.showcaseProject(w, r)
	if !ok {
		return store.ShowcaseVideoJob{}, false
	}
	job, err := s.St.GetShowcaseVideoJob(r.Context(), proj.TenantID, proj.ID, r.PathValue("jobId"))
	if err != nil {
		writeStoreErr(w, err)
		return store.ShowcaseVideoJob{}, false
	}
	return job, true
}

func (s *Server) showcaseProject(w http.ResponseWriter, r *http.Request) (store.Project, bool) {
	p, _ := principalFrom(r.Context())
	proj, err := s.projectFor(r.Context(), p, r.PathValue("id"))
	if err != nil {
		writeStoreErr(w, err)
		return store.Project{}, false
	}
	return proj, true
}

func (s *Server) showcaseVideoView(job store.ShowcaseVideoJob, idempotent bool) map[string]any {
	if job.BillingLabel == "" {
		job.BillingLabel = showcasevideo.BillingPendingLabel
	}
	statusLabel := "待确认"
	if job.JobStatus == showcasevideo.StatusFailed {
		statusLabel = "失败"
	} else if job.JobStatus == showcasevideo.StatusQuoted {
		statusLabel = "待确认报价"
	}
	qual := i2vcontract.DefaultNoAdapterDecision()
	return map[string]any{
		"id": job.ID, "project_id": job.ProjectID, "product_image_id": job.ProductImageID,
		"image_kind": job.ImageKind, "camera_move": job.CameraMove, "image_required": true,
		"fingerprint": job.Fingerprint, "quote_status": job.QuoteStatus,
		"billing_label": job.BillingLabel, "job_status": job.JobStatus,
		"status_label": statusLabel, "quality": job.Quality,
		"platform_task_id": job.PlatformTaskID, "output_asset_id": "",
		"output_is_receipt": false, "show_video": false, "play_still": false, "video_url": "",
		"charged": false, "billing_passed": false, "production_authorized": false,
		"pending": job.Pending, "delivery_readiness": showcasevideo.ReadinessInternal,
		"honesty": showcasevideo.HonestyNotice, "idempotent": idempotent,
		// MX-OPS: the authoritative queue projection is not wired yet; explicit
		// null (unknown) beats a guessed ETA. i2v capability stays UNKNOWN.
		"job_queue_view":           nil,
		"i2v_capability":           i2v.Probe(""),
		"i2v_qualification":        qual,
		"view_provenance":          qual.ViewAssessment,
		"basic_image_path_blocked": false,
		"candidate_return": map[string]any{
			"destination":        i2vcontract.CandidateDestinationMerchantLibrary,
			"auto_published":     false,
			"reward_triggered":   false,
			"merchant_confirmed": false,
		},
	}
}

func appendPendingShowcase(items []string, item string) []string {
	for _, have := range items {
		if have == item {
			return items
		}
	}
	return append(items, item)
}
