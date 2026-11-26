package crossappreturn

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/appregistry"
	"github.com/bianjiefilm/product-image-engine/server/internal/receiptdoc"
)

// TargetResolver resolves a registered receipt target URL from app-registry/v1.
// *appregistry.Manifest satisfies this interface directly.
type TargetResolver interface {
	ResolveTarget(appID, targetID, kind string) (string, error)
	FirstReceiptTarget(appID string) (appregistry.Target, error)
}

// DeliveryOptions configures outbound HTTP delivery for a single return/retry.
type DeliveryOptions struct {
	SourceApp           string
	Registry            TargetResolver
	ReceiptSecretForApp func(appID string) string
	BearerTokenForApp   func(appID string) string
	HTTPClient          *http.Client
	KeyID               string
}

// ReturnOutcome is the structured result of ReturnCandidate or RetryDelivery.
type ReturnOutcome struct {
	Record      DeliveryRecord  `json:"record"`
	Duplicate   bool            `json:"duplicate"`
	Retried     bool            `json:"retried"`
	SideEffects SideEffectAudit `json:"side_effects"`
}

// AdoptRequest records an explicit merchant/receiver adoption of a delivered
// candidate so late-arriving stale source_version deliveries can never overwrite it.
type AdoptRequest struct {
	TenantID     string `json:"tenant_id"`
	ProjectID    string `json:"project_id"`
	OutputID     string `json:"output_id"`
	ReceiptID    string `json:"receipt_id"`
	AdoptedBy    string `json:"adopted_by"`
	ActorRole    string `json:"actor_role,omitempty"`
	EntryTrigger string `json:"entry_trigger,omitempty"`
}

// AdoptOutcome is the structured result of AdoptCandidate.
type AdoptOutcome struct {
	Record      DeliveryRecord       `json:"record"`
	Adopted     AdoptedVersionRecord `json:"adopted"`
	Duplicate   bool                 `json:"duplicate"`
	SideEffects SideEffectAudit      `json:"side_effects"`
}

// RetryRequest requests an idempotent delivery retry for an existing receipt_id
// without triggering generation, billing, publish, or coupon issuance.
type RetryRequest struct {
	TenantID           string `json:"tenant_id"`
	ReceiptID          string `json:"receipt_id"`
	SignedAccessURL    string `json:"signed_access_url,omitempty"`
	SignedURLExpiresAt string `json:"signed_url_expires_at,omitempty"`
	ActorRole          string `json:"actor_role,omitempty"`
	EntryTrigger       string `json:"entry_trigger,omitempty"`
}

// Coordinator coordinates HUI-1745 native project continuation, immutable
// candidate/output return delivery, target registration verification,
// deduplication, stale source_version protection, revocation, and signed-URL
// expiry checks. All exported methods are safe for concurrent use.
type Coordinator struct {
	mu               sync.RWMutex
	now              func() time.Time
	continuations    map[string]ContinuationRecord   // key: tenantID + "\x1f" + projectID
	deliveriesByID   map[string]DeliveryRecord       // key: receiptID
	deliveriesByKey  map[string]string               // key: dedupeKey -> receiptID
	adoptedByContext map[string]AdoptedVersionRecord // key: tenantID + "\x1f" + targetApp + "\x1f" + contextKey
	revokedAssets    map[string]bool                 // key: tenantID + "\x1f" + assetRef
	revokedGrants    map[string]bool                 // key: tenantID + "\x1f" + grantID
	inFlight         map[string]chan struct{}        // key: receiptID
}

// NewCoordinator constructs a Coordinator with UTC clock.
func NewCoordinator() *Coordinator {
	return &Coordinator{
		now:              func() time.Time { return time.Now().UTC() },
		continuations:    make(map[string]ContinuationRecord),
		deliveriesByID:   make(map[string]DeliveryRecord),
		deliveriesByKey:  make(map[string]string),
		adoptedByContext: make(map[string]AdoptedVersionRecord),
		revokedAssets:    make(map[string]bool),
		revokedGrants:    make(map[string]bool),
		inFlight:         make(map[string]chan struct{}),
	}
}

// SetClock overrides the clock used for expiry and timestamps (primarily for tests).
func (c *Coordinator) SetClock(fn func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fn == nil {
		c.now = func() time.Time { return time.Now().UTC() }
		return
	}
	c.now = fn
}

func (c *Coordinator) currentTime() time.Time {
	c.mu.RLock()
	fn := c.now
	c.mu.RUnlock()
	if fn == nil {
		return time.Now().UTC()
	}
	return fn().UTC()
}

// RevokeAsset marks an asset_ref as revoked for a tenant so all subsequent
// continuations, returns, and retries fail closed with 403 revoked.
func (c *Coordinator) RevokeAsset(tenantID, assetRef string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revokedAssets[scopedKey(tenantID, assetRef)] = true
}

// RevokeGrant marks a permission grant_id as revoked for a tenant so all
// subsequent continuations, returns, and retries fail closed with 403 revoked.
func (c *Coordinator) RevokeGrant(tenantID, grantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.revokedGrants[scopedKey(tenantID, grantID)] = true
}

// IsRevoked reports whether the asset_ref or grant_id is revoked for tenantID.
func (c *Coordinator) IsRevoked(tenantID, assetRef, grantID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.isRevokedLocked(tenantID, assetRef, grantID)
}

func (c *Coordinator) isRevokedLocked(tenantID, assetRef, grantID string) bool {
	if strings.TrimSpace(assetRef) != "" && c.revokedAssets[scopedKey(tenantID, assetRef)] {
		return true
	}
	if strings.TrimSpace(grantID) != "" && c.revokedGrants[scopedKey(tenantID, grantID)] {
		return true
	}
	return false
}

// ContinueProject continues an existing native product-image project without
// creating a second project, without fabricating an order to bypass auth, and
// without silently swapping asset_version with "latest".
func (c *Coordinator) ContinueProject(req ContinuationRequest) (ContinuationRecord, error) {
	now := c.currentTime()

	// 1. Visitor touch/view NEVER initiates generation or mutates continuation state.
	if IsVisitorContext(req.ActorRole, req.EntryTrigger) {
		return ContinuationRecord{}, newContractErr(
			http.StatusForbidden,
			CodeVisitorGenerationBlocked,
			"访客触碰/浏览只允许读取已采用内容，严禁发起产品图生成或工程续接写入",
			RecoveryMerchantEntryOnly,
			ErrVisitorGenerationBlocked,
		)
	}

	// 2. Validate source_kind and forbid fake order auth bypass.
	if ce := ValidateSourceKindAndContext(req.SourceKind, req.OrderID, req.CampaignID, req.FakeOrderBypass); ce != nil {
		return ContinuationRecord{}, ce
	}

	// 3. Required native continuation fields.
	for name, val := range map[string]string{
		"tenant_id":            req.TenantID,
		"brand_id":             req.BrandID,
		"source_app":           req.SourceApp,
		"project_id":           req.ProjectID,
		"pinned_asset_version": req.PinnedAssetVersion,
		"usage_scope":          req.UsageScope,
		"source_version":       req.SourceVersion,
	} {
		if !validTokenString(val, 256) {
			return ContinuationRecord{}, newContractErr(
				http.StatusBadRequest,
				CodeInvalidRequest,
				name+" 必填且须 1–256 非空白字符",
				"",
				ErrInvalidRequest,
			)
		}
	}

	// 4. Reject floating "latest" asset_version or silent version swap.
	if IsForbiddenFloatingVersion(req.PinnedAssetVersion) {
		return ContinuationRecord{}, newContractErr(
			http.StatusConflict,
			CodeLatestSwapForbidden,
			"续接原工程禁止使用 latest/floating 别名替代固定 asset_version",
			RecoveryAdoptCurrentVersion,
			ErrLatestSwapForbidden,
		)
	}
	if reqVer := strings.TrimSpace(req.RequestedAssetVersion); reqVer != "" {
		if IsForbiddenFloatingVersion(reqVer) || reqVer != strings.TrimSpace(req.PinnedAssetVersion) {
			return ContinuationRecord{}, newContractErr(
				http.StatusConflict,
				CodeLatestSwapForbidden,
				fmt.Sprintf("禁止以 %q 偷换原固定 asset_version %q（原工程续接必须保持版本不可变）", reqVer, req.PinnedAssetVersion),
				RecoveryAdoptCurrentVersion,
				ErrLatestSwapForbidden,
			)
		}
	}

	// 5. Permission and signed URL fail-closed guards.
	if ce := ValidatePermissionGrant(now, req.Permission); ce != nil {
		return ContinuationRecord{}, ce
	}
	if ce := ValidateSignedURLWindow(now, req.SignedAccessURL, req.SignedURLExpiresAt); ce != nil {
		return ContinuationRecord{}, ce
	}

	seq := ParseSourceVersionSeq(req.SourceVersion, req.SourceVersionSeq)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isRevokedLocked(req.TenantID, req.PinnedAssetRef, req.Permission.GrantID) {
		return ContinuationRecord{}, newContractErr(
			http.StatusForbidden,
			CodeRevoked,
			"该素材或授权已被撤销，续接已按 fail-closed 拒绝；请重新获取有效授权（不会触发生成或扣费）",
			RecoveryReauthorizeGrant,
			ErrRevoked,
		)
	}

	// Guard against cross-tenant continuation of an already-pinned project_id.
	for _, existing := range c.continuations {
		if existing.ProjectID == req.ProjectID && existing.TenantID != req.TenantID {
			return ContinuationRecord{}, newContractErr(
				http.StatusForbidden,
				CodeTenantMismatch,
				"目标工程不属于当前租户，禁止跨租户续接",
				"",
				ErrTenantMismatch,
			)
		}
	}

	key := scopedKey(req.TenantID, req.ProjectID)
	if existing, ok := c.continuations[key]; ok {
		// Original project_id is preserved; verify task_id, order_id, campaign_id,
		// and pinned asset_version are not silently replaced.
		if existing.TaskID != "" && strings.TrimSpace(req.TaskID) != "" && existing.TaskID != strings.TrimSpace(req.TaskID) {
			return ContinuationRecord{}, newContractErr(
				http.StatusConflict,
				CodeProjectContinuationFailed,
				fmt.Sprintf("原工程已绑定 task_id %q，禁止替换为 %q 或重建第二工程", existing.TaskID, req.TaskID),
				"",
				ErrProjectMismatch,
			)
		}
		if existing.OrderID != "" && strings.TrimSpace(req.OrderID) != "" && existing.OrderID != strings.TrimSpace(req.OrderID) {
			return ContinuationRecord{}, newContractErr(
				http.StatusConflict,
				CodeProjectContinuationFailed,
				fmt.Sprintf("原工程已绑定 order_id %q，与传入 order_id %q 冲突", existing.OrderID, req.OrderID),
				"",
				ErrProjectMismatch,
			)
		}
		if existing.CampaignID != "" && strings.TrimSpace(req.CampaignID) != "" && existing.CampaignID != strings.TrimSpace(req.CampaignID) {
			return ContinuationRecord{}, newContractErr(
				http.StatusConflict,
				CodeProjectContinuationFailed,
				fmt.Sprintf("原工程已绑定 campaign_id %q，与传入 campaign_id %q 冲突", existing.CampaignID, req.CampaignID),
				"",
				ErrProjectMismatch,
			)
		}
		if existing.AssetVersion != strings.TrimSpace(req.PinnedAssetVersion) {
			return ContinuationRecord{}, newContractErr(
				http.StatusConflict,
				CodeLatestSwapForbidden,
				fmt.Sprintf("原工程已固定 asset_version %q，禁止静默替换为 %q", existing.AssetVersion, req.PinnedAssetVersion),
				RecoveryAdoptCurrentVersion,
				ErrLatestSwapForbidden,
			)
		}
		// Never allow a stale source_version to downgrade the pinned continuation source_version.
		if seq < existing.SourceVersionSeq {
			return ContinuationRecord{}, newContractErr(
				http.StatusConflict,
				CodeStaleSourceVersion,
				fmt.Sprintf("晚到旧版本 source_version=%s(seq=%d) 不得覆盖当前版本 %s(seq=%d)",
					req.SourceVersion, seq, existing.SourceVersion, existing.SourceVersionSeq),
				RecoveryAdoptCurrentVersion,
				ErrStaleSourceVersion,
			)
		}
		if seq > existing.SourceVersionSeq {
			existing.SourceVersion = strings.TrimSpace(req.SourceVersion)
			existing.SourceVersionSeq = seq
		}
		existing.ContinuedCount++
		existing.ResumedExisting = true
		existing.UpdatedAt = now
		existing.SideEffects = ZeroSideEffects()
		c.continuations[key] = existing
		return cloneContinuation(existing), nil
	}

	contID := deterministicContinuationID(req.TenantID, req.ProjectID)
	rec := ContinuationRecord{
		ContinuationID:   contID,
		TenantID:         strings.TrimSpace(req.TenantID),
		BrandID:          strings.TrimSpace(req.BrandID),
		SourceKind:       strings.TrimSpace(req.SourceKind),
		SourceApp:        strings.TrimSpace(req.SourceApp),
		OrderID:          strings.TrimSpace(req.OrderID),
		CampaignID:       strings.TrimSpace(req.CampaignID),
		ProjectID:        strings.TrimSpace(req.ProjectID),
		TaskID:           strings.TrimSpace(req.TaskID),
		AssetRef:         strings.TrimSpace(req.PinnedAssetRef),
		AssetVersion:     strings.TrimSpace(req.PinnedAssetVersion),
		UsageScope:       strings.TrimSpace(req.UsageScope),
		Permission:       clonePermission(req.Permission),
		SourceVersion:    strings.TrimSpace(req.SourceVersion),
		SourceVersionSeq: seq,
		ContinuedCount:   1,
		ResumedExisting:  true,
		SideEffects:      ZeroSideEffects(),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if rec.Permission.Status == "" {
		rec.Permission.Status = PermissionActive
	}
	if rec.Permission.UsageScope == "" {
		rec.Permission.UsageScope = rec.UsageScope
	}
	c.continuations[key] = rec
	return cloneContinuation(rec), nil
}

// GetContinuation returns the pinned continuation record for (tenantID, projectID).
func (c *Coordinator) GetContinuation(tenantID, projectID string) (ContinuationRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.continuations[scopedKey(tenantID, projectID)]
	if !ok {
		return ContinuationRecord{}, false
	}
	return cloneContinuation(rec), true
}

// ReturnCandidate validates and delivers an immutable candidate/output return
// payload to a registered target receiver via real HTTP POST with §8 HMAC
// framing.
func (c *Coordinator) ReturnCandidate(ctx context.Context, req CandidateReturnRequest, opts DeliveryOptions) (ReturnOutcome, error) {
	now := c.currentTime()

	// 1. Visitor touch/view NEVER initiates generation or candidate return mutation.
	if IsVisitorContext(req.ActorRole, req.EntryTrigger) {
		return ReturnOutcome{}, newContractErr(
			http.StatusForbidden,
			CodeVisitorGenerationBlocked,
			"访客触碰/浏览禁止发起产品图生成或候选回流写入",
			RecoveryMerchantEntryOnly,
			ErrVisitorGenerationBlocked,
		)
	}

	// 2. Reject raw image plaintext explicitly passed by caller.
	if strings.TrimSpace(req.RawImagePayload) != "" {
		return ReturnOutcome{}, newContractErr(
			http.StatusBadRequest,
			CodePlaintextImageForbidden,
			"跨应用候选回流事件严禁携带图片明文，请仅传递已登记的不可变 asset_ref/asset_version 引用",
			"",
			ErrPlaintextImageForbidden,
		)
	}

	// 3. Validate source_kind & reject fake order bypass.
	if ce := ValidateSourceKindAndContext(req.SourceKind, req.OrderID, req.CampaignID, req.FakeOrderBypass); ce != nil {
		return ReturnOutcome{}, ce
	}

	// 4. Reject floating "latest" asset_version.
	if IsForbiddenFloatingVersion(req.AssetVersion) {
		return ReturnOutcome{}, newContractErr(
			http.StatusConflict,
			CodeLatestSwapForbidden,
			"候选回流禁止使用 latest/floating 别名替代不可变 asset_version",
			RecoveryAdoptCurrentVersion,
			ErrLatestSwapForbidden,
		)
	}

	// 5. Permission & signed URL fail-closed checks (zero side effects).
	if ce := ValidatePermissionGrant(now, req.Permission); ce != nil {
		return ReturnOutcome{}, ce
	}
	if ce := ValidateSignedURLWindow(now, req.SignedAccessURL, req.SignedURLExpiresAt); ce != nil {
		return ReturnOutcome{}, ce
	}

	// 6. Resolve registered target URL fail-closed (409 target_not_registered, NEVER fake success).
	targetApp := strings.TrimSpace(req.TargetApp)
	if targetApp == "" {
		return ReturnOutcome{}, newContractErr(http.StatusBadRequest, CodeInvalidRequest, "target_app 必填", "", ErrInvalidRequest)
	}
	resolvedTargetID, targetURL, targetErr := resolveRegisteredReceiptTarget(opts.Registry, targetApp, strings.TrimSpace(req.TargetID))
	if targetErr != nil {
		return ReturnOutcome{}, targetErr
	}

	destKind := strings.TrimSpace(req.DestinationKind)
	if destKind == "" {
		destKind = defaultDestinationKind(req.SourceKind, targetApp)
	}
	seq := ParseSourceVersionSeq(req.SourceVersion, req.SourceVersionSeq)
	contextKey := ContextKey(req.SourceKind, req.OrderID, req.CampaignID, req.ProjectID)
	dedupeKey := DedupeKeyFor(req.TenantID, targetApp, resolvedTargetID, contextKey, req.ProjectID, req.OutputID, req.AssetVersion, req.SourceVersion)

	receiptID := strings.TrimSpace(req.ReceiptID)
	if receiptID == "" {
		receiptID = DeterministicReceiptID(req.TenantID, targetApp, resolvedTargetID, contextKey, req.ProjectID, req.OutputID, req.AssetVersion, req.SourceVersion)
	}

	perm := clonePermission(req.Permission)
	if perm.Status == "" {
		perm.Status = PermissionActive
	}
	if perm.UsageScope == "" {
		perm.UsageScope = strings.TrimSpace(req.UsageScope)
	}
	if perm.Scopes == nil {
		perm.Scopes = []string{}
	}

	payload := CandidateReturnPayload{
		ContractVersion:        ContractVersion,
		ReceiptID:              receiptID,
		TenantID:               strings.TrimSpace(req.TenantID),
		BrandID:                strings.TrimSpace(req.BrandID),
		SourceKind:             strings.TrimSpace(req.SourceKind),
		OrderID:                strings.TrimSpace(req.OrderID),
		CampaignID:             strings.TrimSpace(req.CampaignID),
		ProjectID:              strings.TrimSpace(req.ProjectID),
		TaskID:                 strings.TrimSpace(req.TaskID),
		OutputID:               strings.TrimSpace(req.OutputID),
		AssetRef:               strings.TrimSpace(req.AssetRef),
		AssetVersion:           strings.TrimSpace(req.AssetVersion),
		AssetSHA256:            strings.TrimSpace(req.AssetSHA256),
		AssetSizeBytes:         req.AssetSizeBytes,
		MediaType:              strings.TrimSpace(req.MediaType),
		UsageScope:             strings.TrimSpace(req.UsageScope),
		Permission:             perm,
		SourceVersion:          strings.TrimSpace(req.SourceVersion),
		SourceVersionSeq:       seq,
		TargetApp:              targetApp,
		TargetID:               resolvedTargetID,
		DestinationKind:        destKind,
		LandingMode:            LandingModeCandidateOnly,
		ManualReuploadRequired: false,
		AutoPublished:          false,
		AutoCouponIssued:       false,
		AutoBilled:             false,
		OccurredAt:             now.Format("2006-01-02T15:04:05Z"),
	}
	if ce := payload.Validate(); ce != nil {
		return ReturnOutcome{}, ce
	}

	// Deterministic fingerprint excludes wall-clock occurred_at so identical
	// retries produce the exact same fingerprint even across seconds.
	fpPayload := payload
	fpPayload.OccurredAt = "1970-01-01T00:00:00Z"
	fpBytes, err := json.Marshal(fpPayload)
	if err != nil {
		return ReturnOutcome{}, newContractErr(http.StatusInternalServerError, CodeInvalidRequest, "回执指纹序列化失败", "", err)
	}
	fp := payloadFingerprint(fpBytes)

	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return ReturnOutcome{}, newContractErr(http.StatusInternalServerError, CodeInvalidRequest, "回执序列化失败", "", err)
	}
	if ce := AuditRawEventBytes(rawPayload); ce != nil {
		return ReturnOutcome{}, ce
	}

	// 7. Critical section: check revocation, stale adopted watermark, dedupe/conflict, and in-flight coalescing.
	for {
		c.mu.Lock()
		if c.isRevokedLocked(payload.TenantID, payload.AssetRef, payload.Permission.GrantID) {
			c.mu.Unlock()
			return ReturnOutcome{}, newContractErr(
				http.StatusForbidden,
				CodeRevoked,
				"素材或授权已被撤销，回流已按 fail-closed 拒绝；请重新授权后再操作（不触发生成、扣费、发布或发券）",
				RecoveryReauthorizeGrant,
				ErrRevoked,
			)
		}

		// Stale source_version guard: late-arriving stale source_version NEVER overwrites a newer adopted version.
		adoptedKey := adoptedContextKey(payload.TenantID, targetApp, contextKey)
		if adopted, hasAdopted := c.adoptedByContext[adoptedKey]; hasAdopted {
			if payload.SourceVersionSeq < adopted.SourceVersionSeq {
				c.mu.Unlock()
				return ReturnOutcome{}, newContractErr(
					http.StatusConflict,
					CodeStaleSourceVersion,
					fmt.Sprintf("晚到旧版本 source_version=%s(seq=%d) 不得覆盖已采用的新版本 %s(seq=%d, receipt=%s)",
						payload.SourceVersion, payload.SourceVersionSeq, adopted.SourceVersion, adopted.SourceVersionSeq, adopted.ReceiptID),
					RecoveryAdoptCurrentVersion,
					ErrStaleSourceVersion,
				)
			}
		}

		// Existing receipt lookup by dedupeKey first, then explicit receiptID.
		existingID, byKey := c.deliveriesByKey[dedupeKey]
		if byKey {
			if req.ReceiptID != "" && existingID != receiptID {
				// Same candidate tuple returned under a different explicit receipt_id:
				// converge idempotently to the original receipt without duplicating delivery.
				receiptID = existingID
			}
			if waitCh, busy := c.inFlight[existingID]; busy {
				c.mu.Unlock()
				select {
				case <-waitCh:
					continue
				case <-ctx.Done():
					return ReturnOutcome{}, ctx.Err()
				}
			}
			existing := c.deliveriesByID[existingID]
			if existing.Status == StatusDelivered || existing.Status == StatusAdopted {
				c.mu.Unlock()
				return ReturnOutcome{
					Record:      cloneDelivery(existing),
					Duplicate:   true,
					Retried:     false,
					SideEffects: ZeroSideEffects(),
				}, nil
			}
			// Previously failed delivery for same tuple: retry delivery with immutable original payload.
			doneCh := make(chan struct{})
			c.inFlight[existingID] = doneCh
			rawToSend := append([]byte(nil), existing.immutablePayloadRaw...)
			recCopy := cloneDelivery(existing)
			c.mu.Unlock()
			return c.executeHTTPDelivery(ctx, recCopy, targetURL, rawToSend, opts, doneCh, true)
		}

		if existing, byID := c.deliveriesByID[receiptID]; byID {
			if waitCh, busy := c.inFlight[receiptID]; busy {
				c.mu.Unlock()
				select {
				case <-waitCh:
					continue
				case <-ctx.Done():
					return ReturnOutcome{}, ctx.Err()
				}
			}
			if existing.Payload.TenantID != payload.TenantID {
				c.mu.Unlock()
				return ReturnOutcome{}, newContractErr(
					http.StatusForbidden,
					CodeTenantMismatch,
					"receipt_id 属于其他租户，拒绝覆盖或重放",
					"",
					ErrTenantMismatch,
				)
			}
			if existing.PayloadFingerprint != fp {
				c.mu.Unlock()
				return ReturnOutcome{}, newContractErr(
					http.StatusConflict,
					CodeReceiptPayloadConflict,
					"同 receipt_id 已存在不同载荷，保留原回执绝不覆盖",
					"",
					ErrReceiptConflict,
				)
			}
			if existing.Status == StatusDelivered || existing.Status == StatusAdopted {
				c.mu.Unlock()
				return ReturnOutcome{
					Record:      cloneDelivery(existing),
					Duplicate:   true,
					Retried:     false,
					SideEffects: ZeroSideEffects(),
				}, nil
			}
			doneCh := make(chan struct{})
			c.inFlight[receiptID] = doneCh
			rawToSend := append([]byte(nil), existing.immutablePayloadRaw...)
			recCopy := cloneDelivery(existing)
			c.mu.Unlock()
			return c.executeHTTPDelivery(ctx, recCopy, targetURL, rawToSend, opts, doneCh, true)
		}

		// Verify signing secret exists before persisting & attempting HTTP delivery (fail-closed).
		if secret := lookupSecret(opts.ReceiptSecretForApp, targetApp); secret == "" {
			c.mu.Unlock()
			return ReturnOutcome{}, newContractErr(
				http.StatusServiceUnavailable,
				CodeReceiptKeyMissing,
				fmt.Sprintf("未配置目标应用 %q 的回执签名密钥，拒绝伪造成功", targetApp),
				"",
				ErrReceiptKeyMissing,
			)
		}

		// Reserve new immutable delivery record and in-flight barrier.
		rec := DeliveryRecord{
			ReceiptID:           receiptID,
			DedupeKey:           dedupeKey,
			PayloadFingerprint:  fp,
			Payload:             payload,
			TargetURL:           targetURL,
			Status:              StatusPendingDelivery,
			Attempts:            0,
			SideEffects:         ZeroSideEffects(),
			CreatedAt:           now,
			UpdatedAt:           now,
			immutablePayloadRaw: rawPayload,
		}
		c.deliveriesByID[receiptID] = rec
		c.deliveriesByKey[dedupeKey] = receiptID
		doneCh := make(chan struct{})
		c.inFlight[receiptID] = doneCh
		c.mu.Unlock()

		return c.executeHTTPDelivery(ctx, cloneDelivery(rec), targetURL, rawPayload, opts, doneCh, false)
	}
}

// RetryDelivery retries an existing candidate return receipt without creating a
// new candidate, without changing the immutable payload/receipt_id, and with
// zero regeneration, billing, publish, or coupon side effects.
func (c *Coordinator) RetryDelivery(ctx context.Context, req RetryRequest, opts DeliveryOptions) (ReturnOutcome, error) {
	now := c.currentTime()

	if IsVisitorContext(req.ActorRole, req.EntryTrigger) {
		return ReturnOutcome{}, newContractErr(
			http.StatusForbidden,
			CodeVisitorGenerationBlocked,
			"访客触碰/浏览禁止发起候选回流重试",
			RecoveryMerchantEntryOnly,
			ErrVisitorGenerationBlocked,
		)
	}
	if ce := ValidateSignedURLWindow(now, req.SignedAccessURL, req.SignedURLExpiresAt); ce != nil {
		return ReturnOutcome{}, ce
	}

	receiptID := strings.TrimSpace(req.ReceiptID)
	tenantID := strings.TrimSpace(req.TenantID)
	if receiptID == "" || tenantID == "" {
		return ReturnOutcome{}, newContractErr(http.StatusBadRequest, CodeInvalidRequest, "tenant_id 与 receipt_id 必填", "", ErrInvalidRequest)
	}

	for {
		c.mu.Lock()
		existing, ok := c.deliveriesByID[receiptID]
		if !ok || existing.Payload.TenantID != tenantID {
			c.mu.Unlock()
			return ReturnOutcome{}, newContractErr(http.StatusNotFound, CodeNotFound, "回执不存在或不属于当前租户", "", ErrNotFound)
		}
		if waitCh, busy := c.inFlight[receiptID]; busy {
			c.mu.Unlock()
			select {
			case <-waitCh:
				continue
			case <-ctx.Done():
				return ReturnOutcome{}, ctx.Err()
			}
		}

		// Re-check revocation & permission expiry fail-closed on retry.
		if ce := ValidatePermissionGrant(now, existing.Payload.Permission); ce != nil {
			c.mu.Unlock()
			return ReturnOutcome{}, ce
		}
		if c.isRevokedLocked(tenantID, existing.Payload.AssetRef, existing.Payload.Permission.GrantID) {
			c.mu.Unlock()
			return ReturnOutcome{}, newContractErr(
				http.StatusForbidden,
				CodeRevoked,
				"该候选回执关联的素材或授权已被撤销，禁止重试投递；请重新授权（不触发生成或扣费）",
				RecoveryReauthorizeGrant,
				ErrRevoked,
			)
		}

		// Re-check stale watermark so retrying an old failed receipt never overwrites a newer adopted version.
		contextKey := ContextKey(existing.Payload.SourceKind, existing.Payload.OrderID, existing.Payload.CampaignID, existing.Payload.ProjectID)
		adoptedKey := adoptedContextKey(tenantID, existing.Payload.TargetApp, contextKey)
		if adopted, hasAdopted := c.adoptedByContext[adoptedKey]; hasAdopted {
			if existing.Payload.SourceVersionSeq < adopted.SourceVersionSeq {
				c.mu.Unlock()
				return ReturnOutcome{}, newContractErr(
					http.StatusConflict,
					CodeStaleSourceVersion,
					fmt.Sprintf("旧版本回执 %s(seq=%d) 已落后于已采用版本 %s(seq=%d)，拒绝重试覆盖",
						existing.Payload.SourceVersion, existing.Payload.SourceVersionSeq, adopted.SourceVersion, adopted.SourceVersionSeq),
					RecoveryAdoptCurrentVersion,
					ErrStaleSourceVersion,
				)
			}
		}

		// Already delivered or adopted -> idempotent no-op, zero extra HTTP calls!
		if existing.Status == StatusDelivered || existing.Status == StatusAdopted {
			c.mu.Unlock()
			return ReturnOutcome{
				Record:      cloneDelivery(existing),
				Duplicate:   true,
				Retried:     false,
				SideEffects: ZeroSideEffects(),
			}, nil
		}

		// Re-verify target registration fail-closed on retry.
		_, targetURL, targetErr := resolveRegisteredReceiptTarget(opts.Registry, existing.Payload.TargetApp, existing.Payload.TargetID)
		if targetErr != nil {
			c.mu.Unlock()
			return ReturnOutcome{}, targetErr
		}
		if secret := lookupSecret(opts.ReceiptSecretForApp, existing.Payload.TargetApp); secret == "" {
			c.mu.Unlock()
			return ReturnOutcome{}, newContractErr(
				http.StatusServiceUnavailable,
				CodeReceiptKeyMissing,
				fmt.Sprintf("未配置目标应用 %q 的回执签名密钥", existing.Payload.TargetApp),
				"",
				ErrReceiptKeyMissing,
			)
		}

		doneCh := make(chan struct{})
		c.inFlight[receiptID] = doneCh
		rawToSend := append([]byte(nil), existing.immutablePayloadRaw...)
		recCopy := cloneDelivery(existing)
		c.mu.Unlock()

		return c.executeHTTPDelivery(ctx, recCopy, targetURL, rawToSend, opts, doneCh, true)
	}
}

// AdoptCandidate explicitly marks a delivered candidate as adopted by a merchant
// or operator, locking the monotonic source_version watermark so late-arriving
// stale versions can never overwrite it. Never auto-publishes or issues coupons.
func (c *Coordinator) AdoptCandidate(req AdoptRequest) (AdoptOutcome, error) {
	now := c.currentTime()

	if IsVisitorContext(req.ActorRole, req.EntryTrigger) {
		return AdoptOutcome{}, newContractErr(
			http.StatusForbidden,
			CodeVisitorGenerationBlocked,
			"访客无权采用候选内容，仅限商家/操作员明确采用",
			RecoveryMerchantEntryOnly,
			ErrVisitorGenerationBlocked,
		)
	}
	tenantID := strings.TrimSpace(req.TenantID)
	receiptID := strings.TrimSpace(req.ReceiptID)
	adoptedBy := strings.TrimSpace(req.AdoptedBy)
	if tenantID == "" || receiptID == "" || adoptedBy == "" {
		return AdoptOutcome{}, newContractErr(http.StatusBadRequest, CodeInvalidRequest, "tenant_id、receipt_id、adopted_by 必填", "", ErrInvalidRequest)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	rec, ok := c.deliveriesByID[receiptID]
	if !ok || rec.Payload.TenantID != tenantID {
		return AdoptOutcome{}, newContractErr(http.StatusNotFound, CodeNotFound, "回执不存在或不属于当前租户", "", ErrNotFound)
	}
	if req.ProjectID != "" && rec.Payload.ProjectID != strings.TrimSpace(req.ProjectID) {
		return AdoptOutcome{}, newContractErr(http.StatusNotFound, CodeNotFound, "回执不属于指定工程", "", ErrNotFound)
	}
	if req.OutputID != "" && rec.Payload.OutputID != strings.TrimSpace(req.OutputID) {
		return AdoptOutcome{}, newContractErr(http.StatusNotFound, CodeNotFound, "回执不属于指定输出", "", ErrNotFound)
	}
	if c.isRevokedLocked(tenantID, rec.Payload.AssetRef, rec.Payload.Permission.GrantID) {
		return AdoptOutcome{}, newContractErr(
			http.StatusForbidden,
			CodeRevoked,
			"该候选关联的素材或授权已被撤销，无法采用；请重新授权",
			RecoveryReauthorizeGrant,
			ErrRevoked,
		)
	}
	if rec.Status != StatusDelivered && rec.Status != StatusAdopted {
		return AdoptOutcome{}, newContractErr(
			http.StatusConflict,
			CodeDeliveryFailed,
			"目标接收端尚未实际收到该候选回执，不可标为已采用",
			RecoveryRetryDeliveryOnly,
			ErrDeliveryFailed,
		)
	}

	contextKey := ContextKey(rec.Payload.SourceKind, rec.Payload.OrderID, rec.Payload.CampaignID, rec.Payload.ProjectID)
	adoptedKey := adoptedContextKey(tenantID, rec.Payload.TargetApp, contextKey)

	if prev, exists := c.adoptedByContext[adoptedKey]; exists {
		if prev.ReceiptID == rec.ReceiptID {
			return AdoptOutcome{
				Record:      cloneDelivery(rec),
				Adopted:     prev,
				Duplicate:   true,
				SideEffects: ZeroSideEffects(),
			}, nil
		}
		if rec.Payload.SourceVersionSeq < prev.SourceVersionSeq {
			return AdoptOutcome{}, newContractErr(
				http.StatusConflict,
				CodeStaleSourceVersion,
				fmt.Sprintf("旧版本 source_version=%s(seq=%d) 不得覆盖已采用的新版本 %s(seq=%d)",
					rec.Payload.SourceVersion, rec.Payload.SourceVersionSeq, prev.SourceVersion, prev.SourceVersionSeq),
				RecoveryAdoptCurrentVersion,
				ErrStaleSourceVersion,
			)
		}
	}

	adoptedAt := now
	rec.Status = StatusAdopted
	rec.Adopted = true
	rec.AdoptedAt = &adoptedAt
	rec.UpdatedAt = now
	rec.SideEffects = ZeroSideEffects()
	c.deliveriesByID[receiptID] = rec

	adopted := AdoptedVersionRecord{
		TenantID:         tenantID,
		TargetApp:        rec.Payload.TargetApp,
		ContextKey:       contextKey,
		ReceiptID:        rec.ReceiptID,
		ProjectID:        rec.Payload.ProjectID,
		OutputID:         rec.Payload.OutputID,
		AssetVersion:     rec.Payload.AssetVersion,
		SourceVersion:    rec.Payload.SourceVersion,
		SourceVersionSeq: rec.Payload.SourceVersionSeq,
		AdoptedBy:        adoptedBy,
		AdoptedAt:        adoptedAt,
	}
	c.adoptedByContext[adoptedKey] = adopted

	return AdoptOutcome{
		Record:      cloneDelivery(rec),
		Adopted:     adopted,
		Duplicate:   false,
		SideEffects: ZeroSideEffects(),
	}, nil
}

// GetDelivery returns an immutable snapshot of a delivery record for tenantID.
func (c *Coordinator) GetDelivery(tenantID, receiptID string) (DeliveryRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rec, ok := c.deliveriesByID[strings.TrimSpace(receiptID)]
	if !ok || rec.Payload.TenantID != strings.TrimSpace(tenantID) {
		return DeliveryRecord{}, false
	}
	return cloneDelivery(rec), true
}

// ListDeliveriesByProject returns all delivery records for (tenantID, projectID),
// ordered newest-first, with zero side effects.
func (c *Coordinator) ListDeliveriesByProject(tenantID, projectID string) []DeliveryRecord {
	c.mu.RLock()
	defer c.mu.RUnlock()
	tID := strings.TrimSpace(tenantID)
	pID := strings.TrimSpace(projectID)
	out := make([]DeliveryRecord, 0)
	for _, rec := range c.deliveriesByID {
		if rec.Payload.TenantID == tID && rec.Payload.ProjectID == pID {
			out = append(out, cloneDelivery(rec))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ReceiptID > out[j].ReceiptID
	})
	return out
}

// GetAdoptedVersion returns the currently adopted version for a target context.
func (c *Coordinator) GetAdoptedVersion(tenantID, targetApp, contextKey string) (AdoptedVersionRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.adoptedByContext[adoptedContextKey(tenantID, targetApp, contextKey)]
	return v, ok
}

func (c *Coordinator) executeHTTPDelivery(
	ctx context.Context,
	rec DeliveryRecord,
	targetURL string,
	rawPayload []byte,
	opts DeliveryOptions,
	doneCh chan struct{},
	isRetry bool,
) (ReturnOutcome, error) {
	defer func() {
		c.mu.Lock()
		delete(c.inFlight, rec.ReceiptID)
		close(doneCh)
		c.mu.Unlock()
	}()

	now := c.currentTime()
	sourceApp := strings.TrimSpace(opts.SourceApp)
	if sourceApp == "" {
		sourceApp = "product-image"
	}
	keyID := strings.TrimSpace(opts.KeyID)
	if keyID == "" {
		keyID = "product-image-receipt-key-1"
	}
	secret := lookupSecret(opts.ReceiptSecretForApp, rec.Payload.TargetApp)
	attemptNum := rec.Attempts + 1

	sig := receiptdoc.FrameSign([]byte(secret), now.Unix(), sourceApp, rec.Payload.TargetApp, rec.ReceiptID, rawPayload)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(rawPayload))
	if err != nil {
		updated := c.settleDelivery(rec.ReceiptID, StatusDeliveryFailed, 0, CodeDeliveryFailed, "回执请求构造失败: "+err.Error(), "", "")
		return ReturnOutcome{Record: updated, Retried: isRetry, SideEffects: ZeroSideEffects()},
			newContractErr(http.StatusBadGateway, CodeDeliveryFailed, "回执请求构造失败", RecoveryRetryDeliveryOnly, ErrDeliveryFailed)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(receiptdoc.HeaderKeyID, keyID)
	httpReq.Header.Set(receiptdoc.HeaderTimestamp, strconv.FormatInt(now.Unix(), 10))
	httpReq.Header.Set(receiptdoc.HeaderSignature, sig)
	httpReq.Header.Set(receiptdoc.HeaderAttempt, strconv.Itoa(attemptNum))
	if bearer := lookupSecret(opts.BearerTokenForApp, rec.Payload.TargetApp); bearer != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearer)
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		updated := c.settleDelivery(rec.ReceiptID, StatusDeliveryFailed, 0, CodeDeliveryFailed, "回执投递网络失败: "+err.Error(), "", "")
		return ReturnOutcome{Record: updated, Retried: isRetry, SideEffects: ZeroSideEffects()},
			newContractErr(http.StatusBadGateway, CodeDeliveryFailed, "接收端不可达，已保留不可变回执供重试（不触发生成或扣费）", RecoveryRetryDeliveryOnly, ErrDeliveryFailed)
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var ack struct {
			CandidateID string `json:"candidate_id"`
			AckID       string `json:"ack_id"`
			Status      string `json:"status"`
		}
		_ = json.Unmarshal(bodyBytes, &ack)
		ackID := strings.TrimSpace(ack.CandidateID)
		if ackID == "" {
			ackID = strings.TrimSpace(ack.AckID)
		}
		ackStatus := strings.TrimSpace(ack.Status)
		if ackStatus == "" {
			ackStatus = "accepted_candidate"
		}
		updated := c.settleDelivery(rec.ReceiptID, StatusDelivered, resp.StatusCode, "", "", ackID, ackStatus)
		return ReturnOutcome{
			Record:      updated,
			Duplicate:   false,
			Retried:     isRetry,
			SideEffects: ZeroSideEffects(),
		}, nil
	}

	// Classify receiver rejection fail-closed.
	bodyLower := strings.ToLower(string(bodyBytes))
	switch {
	case resp.StatusCode == http.StatusForbidden && (strings.Contains(bodyLower, "revoked") || strings.Contains(bodyLower, "permission")):
		updated := c.settleDelivery(rec.ReceiptID, StatusDeliveryFailed, resp.StatusCode, CodeRevoked, "接收端拒绝：权限已撤销 (403)", "", "")
		return ReturnOutcome{Record: updated, Retried: isRetry, SideEffects: ZeroSideEffects()},
			newContractErr(http.StatusForbidden, CodeRevoked, "接收端确认资产或权限已撤销，拒绝回流（不触发生成或扣费）", RecoveryReauthorizeGrant, ErrRevoked)
	case resp.StatusCode == http.StatusGone && (strings.Contains(bodyLower, "expired") || strings.Contains(bodyLower, "url")):
		updated := c.settleDelivery(rec.ReceiptID, StatusDeliveryFailed, resp.StatusCode, CodeURLExpired, "接收端拒绝：签名链接已过期 (410)", "", "")
		return ReturnOutcome{Record: updated, Retried: isRetry, SideEffects: ZeroSideEffects()},
			newContractErr(http.StatusGone, CodeURLExpired, "接收端拒绝：签名访问链接已过期，请针对同版本续签链接后重试（不触发生成或扣费）", RecoveryRefreshSignedURL, ErrURLExpired)
	case resp.StatusCode == http.StatusConflict && strings.Contains(bodyLower, "stale"):
		updated := c.settleDelivery(rec.ReceiptID, StatusDeliveryFailed, resp.StatusCode, CodeStaleSourceVersion, "接收端拒绝：晚到旧版本不得覆盖已采用新版本 (409)", "", "")
		return ReturnOutcome{Record: updated, Retried: isRetry, SideEffects: ZeroSideEffects()},
			newContractErr(http.StatusConflict, CodeStaleSourceVersion, "接收端拒绝：晚到旧版本 source_version 不得覆盖已采用新版本", RecoveryAdoptCurrentVersion, ErrStaleSourceVersion)
	default:
		msg := fmt.Sprintf("接收端拒绝回执: HTTP %d", resp.StatusCode)
		updated := c.settleDelivery(rec.ReceiptID, StatusDeliveryFailed, resp.StatusCode, CodeDeliveryFailed, msg, "", "")
		return ReturnOutcome{Record: updated, Retried: isRetry, SideEffects: ZeroSideEffects()},
			newContractErr(http.StatusBadGateway, CodeDeliveryFailed, msg+"（已保留不可变回执供重试，不触发生成或扣费）", RecoveryRetryDeliveryOnly, ErrDeliveryFailed)
	}
}

func (c *Coordinator) settleDelivery(
	receiptID, status string,
	httpStatus int,
	errCode, errMsg, ackID, ackStatus string,
) DeliveryRecord {
	now := c.currentTime()
	c.mu.Lock()
	defer c.mu.Unlock()
	rec := c.deliveriesByID[receiptID]
	rec.Status = status
	rec.Attempts++
	rec.LastHTTPStatus = httpStatus
	rec.LastErrorCode = errCode
	rec.LastError = errMsg
	if ackID != "" {
		rec.ReceiverAckID = ackID
	}
	if ackStatus != "" {
		rec.ReceiverStatus = ackStatus
	}
	rec.UpdatedAt = now
	rec.SideEffects = ZeroSideEffects()
	c.deliveriesByID[receiptID] = rec
	return cloneDelivery(rec)
}

func resolveRegisteredReceiptTarget(reg TargetResolver, targetApp, targetID string) (string, string, *ContractError) {
	if reg == nil {
		return "", "", newContractErr(
			http.StatusConflict,
			CodeTargetNotRegistered,
			fmt.Sprintf("目标应用 %q 未在应用登记表登记可用回执接收端，拒绝伪造或代签成功", targetApp),
			RecoveryRegisterTarget,
			ErrTargetNotRegistered,
		)
	}
	if targetID != "" {
		u, err := reg.ResolveTarget(targetApp, targetID, appregistry.KindReceipt)
		if err != nil {
			return "", "", newContractErr(
				http.StatusConflict,
				CodeTargetNotRegistered,
				fmt.Sprintf("目标 %s/%s 未登记为合法回执接收端（%v），拒绝伪造或代签成功", targetApp, targetID, err),
				RecoveryRegisterTarget,
				ErrTargetNotRegistered,
			)
		}
		return targetID, u, nil
	}
	tg, err := reg.FirstReceiptTarget(targetApp)
	if err != nil {
		return "", "", newContractErr(
			http.StatusConflict,
			CodeTargetNotRegistered,
			fmt.Sprintf("目标应用 %q 未登记回执接收端（%v），拒绝伪造或代签成功", targetApp, err),
			RecoveryRegisterTarget,
			ErrTargetNotRegistered,
		)
	}
	return tg.TargetID, tg.URL, nil
}

func defaultDestinationKind(sourceKind, targetApp string) string {
	switch {
	case targetApp == "touch-engine" || sourceKind == SourceCampaign:
		return DestinationTouchContentPack
	case targetApp == "orders" || sourceKind == SourceOrder:
		return DestinationOrderCandidate
	case sourceKind == SourceBot || sourceKind == SourceNotification:
		return DestinationBotCallback
	default:
		return DestinationOrderCandidate
	}
}

func lookupSecret(fn func(string) string, appID string) string {
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn(appID))
}

func scopedKey(a, b string) string {
	return strings.TrimSpace(a) + "\x1f" + strings.TrimSpace(b)
}

func adoptedContextKey(tenantID, targetApp, contextKey string) string {
	return strings.TrimSpace(tenantID) + "\x1f" + strings.TrimSpace(targetApp) + "\x1f" + strings.TrimSpace(contextKey)
}

func deterministicContinuationID(tenantID, projectID string) string {
	sum := sha256.Sum256([]byte(scopedKey(tenantID, projectID)))
	return "cont_" + hex.EncodeToString(sum[:12])
}

func clonePermission(p PermissionGrant) PermissionGrant {
	cp := p
	if p.Scopes != nil {
		cp.Scopes = append([]string(nil), p.Scopes...)
	}
	return cp
}

func cloneContinuation(r ContinuationRecord) ContinuationRecord {
	cp := r
	cp.Permission = clonePermission(r.Permission)
	return cp
}

func cloneDelivery(r DeliveryRecord) DeliveryRecord {
	cp := r
	cp.Payload.Permission = clonePermission(r.Payload.Permission)
	if r.AdoptedAt != nil {
		t := *r.AdoptedAt
		cp.AdoptedAt = &t
	}
	if r.immutablePayloadRaw != nil {
		cp.immutablePayloadRaw = append([]byte(nil), r.immutablePayloadRaw...)
	}
	return cp
}
