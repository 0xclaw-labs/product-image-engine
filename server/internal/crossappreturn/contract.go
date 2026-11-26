// Package crossappreturn implements the HUI-1745 cross-application native project
// continuation and immutable candidate/output return & receipt contract.
//
// Core invariants:
//   - Native project continuation resumes the original project_id / task_id for
//     order, campaign (no-order), standalone, bot, and notification entries
//     without rebuilding a second project, without creating fake orders to bypass
//     auth, and without silently swapping a pinned asset_version with "latest".
//   - Candidate/output return delivers an immutable structured payload containing
//     (tenant_id, brand_id, campaign_id / order_id, project_id, task_id, output_id,
//     asset_version, usage_scope, permission, source_version, receipt_id) to a
//     registered target receiver without raw image plaintext in events and without
//     manual download/re-upload.
//   - Unregistered targets fail closed with HTTP 409 (target_not_registered) and
//     never fake or sign success.
//   - Duplicate receipt delivery is strictly idempotent (same candidate/receipt_id,
//     zero regeneration, zero billing, zero auto-publish, zero coupon).
//   - Late-arriving stale source_version never overwrites a newer adopted version.
//   - Revoked permission (403 revoked) and expired signed URL (410 url_expired)
//     fail closed with explicit recovery actions; retries never trigger image
//     generation, billing, publish, or coupon issuance.
//   - Visitor touch/view never initiates product-image generation.
package crossappreturn

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	ContractVersion = "crossapp-candidate-return/v1"

	// Source kinds supported for native continuation and candidate return.
	SourceOrder        = "order"
	SourceCampaign     = "campaign"
	SourceStandalone   = "standalone"
	SourceBot          = "bot"
	SourceNotification = "notification"

	// Receiver destination kinds.
	DestinationTouchContentPack = "touch_content_pack" // Touch HUI-3553 content pack receiver
	DestinationOrderCandidate   = "order_candidate"    // Order fulfillment receiver
	DestinationMatrixDraft      = "matrix_draft"       // Matrix draft receiver
	DestinationBotCallback      = "bot_callback"       // Cloud Bot notification receiver

	// Actor roles and entry triggers.
	RoleMerchant   = "merchant"
	RoleOperator   = "operator"
	RoleBotService = "bot_service"
	RoleVisitor    = "visitor"

	TriggerMerchantContinue   = "merchant_continue"
	TriggerBotReturn          = "bot_return"
	TriggerNotificationReturn = "notification_return"
	TriggerVisitorTouch       = "visitor_touch"
	TriggerVisitorView        = "visitor_view"

	// Permission grant statuses.
	PermissionActive  = "active"
	PermissionRevoked = "revoked"
	PermissionExpired = "expired"

	// Candidate landing mode (never auto-publishes, never auto-coupons, never auto-bills).
	LandingModeCandidateOnly = "candidate_only"

	// Delivery statuses.
	StatusPendingDelivery = "pending_delivery"
	StatusDelivered       = "delivered"
	StatusDeliveryFailed  = "delivery_failed"
	StatusAdopted         = "adopted"

	// Machine-readable recovery actions (never regenerate/bill/publish/coupon).
	RecoveryRegisterTarget      = "register_target_in_app_registry"
	RecoveryReauthorizeGrant    = "reauthorize_asset_permission"
	RecoveryRefreshSignedURL    = "refresh_signed_url_same_version"
	RecoveryAdoptCurrentVersion = "adopt_or_select_current_source_version"
	RecoveryRetryDeliveryOnly   = "retry_candidate_delivery_without_regeneration"
	RecoveryMerchantEntryOnly   = "use_authenticated_merchant_entry"
)

// Machine-readable contract error codes.
const (
	CodeTargetNotRegistered       = "target_not_registered"
	CodeRevoked                   = "revoked"
	CodeURLExpired                = "url_expired"
	CodeStaleSourceVersion        = "stale_source_version"
	CodeVisitorGenerationBlocked  = "visitor_generation_forbidden"
	CodeLatestSwapForbidden       = "latest_version_swap_forbidden"
	CodeFakeOrderForbidden        = "fake_order_forbidden"
	CodePlaintextImageForbidden   = "plaintext_image_forbidden"
	CodeSecretLeakForbidden       = "internal_secret_leak_forbidden"
	CodeProjectContinuationFailed = "project_continuation_mismatch"
	CodeTenantMismatch            = "tenant_mismatch"
	CodeReceiptPayloadConflict    = "receipt_payload_conflict"
	CodeInvalidRequest            = "invalid_request"
	CodeNotFound                  = "not_found"
	CodeDeliveryFailed            = "receipt_delivery_failed"
	CodeReceiptKeyMissing         = "receipt_key_missing"
)

// Sentinel errors for errors.Is checks.
var (
	ErrTargetNotRegistered      = errors.New("crossappreturn: target not registered")
	ErrRevoked                  = errors.New("crossappreturn: permission revoked")
	ErrURLExpired               = errors.New("crossappreturn: signed url expired")
	ErrStaleSourceVersion       = errors.New("crossappreturn: stale source version cannot overwrite newer adopted version")
	ErrVisitorGenerationBlocked = errors.New("crossappreturn: visitor touch/view cannot initiate product-image generation")
	ErrLatestSwapForbidden      = errors.New("crossappreturn: asset_version cannot be silently swapped with latest")
	ErrFakeOrderForbidden       = errors.New("crossappreturn: non-order source cannot forge order_id to bypass auth")
	ErrPlaintextImageForbidden  = errors.New("crossappreturn: raw image plaintext is forbidden in cross-app events")
	ErrSecretLeakForbidden      = errors.New("crossappreturn: internal path or secret is forbidden in cross-app events")
	ErrProjectMismatch          = errors.New("crossappreturn: continuation project_id/task_id mismatch")
	ErrTenantMismatch           = errors.New("crossappreturn: tenant mismatch")
	ErrReceiptConflict          = errors.New("crossappreturn: receipt_id already exists with different payload")
	ErrInvalidRequest           = errors.New("crossappreturn: invalid request")
	ErrNotFound                 = errors.New("crossappreturn: not found")
	ErrDeliveryFailed           = errors.New("crossappreturn: receipt delivery failed")
	ErrReceiptKeyMissing        = errors.New("crossappreturn: receipt signing key missing")
)

// SideEffectAudit makes the zero-unintended-side-effect invariant explicit on
// every continuation, return, retry, and error response.
type SideEffectAudit struct {
	ProjectRebuilt         bool `json:"project_rebuilt"`
	GenerationTriggered    bool `json:"generation_triggered"`
	BillingTriggered       bool `json:"billing_triggered"`
	AutoPublished          bool `json:"auto_published"`
	CouponIssued           bool `json:"coupon_issued"`
	ManualReuploadRequired bool `json:"manual_reupload_required"`
}

// ZeroSideEffects returns the canonical zero-side-effect proof.
func ZeroSideEffects() SideEffectAudit {
	return SideEffectAudit{
		ProjectRebuilt:         false,
		GenerationTriggered:    false,
		BillingTriggered:       false,
		AutoPublished:          false,
		CouponIssued:           false,
		ManualReuploadRequired: false,
	}
}

// ContractError is a structured fail-closed domain error carrying HTTP status,
// machine-readable code, recovery path, and guaranteed zero side effects.
type ContractError struct {
	HTTPStatus     int             `json:"http_status"`
	Code           string          `json:"code"`
	Message        string          `json:"message"`
	RecoveryAction string          `json:"recovery_action,omitempty"`
	SideEffects    SideEffectAudit `json:"side_effects"`
	cause          error
}

func (e *ContractError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("crossappreturn [%d %s]: %s", e.HTTPStatus, e.Code, e.Message)
}

func (e *ContractError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newContractErr(status int, code, msg, recovery string, cause error) *ContractError {
	return &ContractError{
		HTTPStatus:     status,
		Code:           code,
		Message:        msg,
		RecoveryAction: recovery,
		SideEffects:    ZeroSideEffects(),
		cause:          cause,
	}
}

// AsContractError extracts *ContractError if present.
func AsContractError(err error) (*ContractError, bool) {
	var ce *ContractError
	if errors.As(err, &ce) {
		return ce, true
	}
	return nil, false
}

// PermissionGrant describes the bounded asset usage permission attached to a
// continuation or returned candidate.
type PermissionGrant struct {
	GrantID          string   `json:"grant_id"`
	Status           string   `json:"status"`
	UsageScope       string   `json:"usage_scope"`
	Scopes           []string `json:"scopes"`
	PayerPrincipalID string   `json:"payer_principal_id,omitempty"`
	ExpiresAt        string   `json:"expires_at,omitempty"`
}

// ContinuationRequest represents a request to continue an existing native
// product-image project from an order, campaign (no-order), standalone, bot,
// or notification return entry.
type ContinuationRequest struct {
	TenantID              string          `json:"tenant_id"`
	BrandID               string          `json:"brand_id"`
	SourceKind            string          `json:"source_kind"`
	SourceApp             string          `json:"source_app"`
	OrderID               string          `json:"order_id,omitempty"`
	CampaignID            string          `json:"campaign_id,omitempty"`
	ProjectID             string          `json:"project_id"`
	TaskID                string          `json:"task_id,omitempty"`
	PinnedAssetRef        string          `json:"pinned_asset_ref,omitempty"`
	PinnedAssetVersion    string          `json:"pinned_asset_version"`
	RequestedAssetVersion string          `json:"requested_asset_version,omitempty"`
	UsageScope            string          `json:"usage_scope"`
	Permission            PermissionGrant `json:"permission"`
	SignedAccessURL       string          `json:"signed_access_url,omitempty"`
	SignedURLExpiresAt    string          `json:"signed_url_expires_at,omitempty"`
	SourceVersion         string          `json:"source_version"`
	SourceVersionSeq      int64           `json:"source_version_seq,omitempty"`
	ActorRole             string          `json:"actor_role,omitempty"`
	EntryTrigger          string          `json:"entry_trigger,omitempty"`
	FakeOrderBypass       bool            `json:"fake_order_bypass,omitempty"`
	RequestGeneration     bool            `json:"request_generation,omitempty"`
}

// ContinuationRecord is the immutable continuation binding returned after
// validating a continuation entry.
type ContinuationRecord struct {
	ContinuationID   string          `json:"continuation_id"`
	TenantID         string          `json:"tenant_id"`
	BrandID          string          `json:"brand_id"`
	SourceKind       string          `json:"source_kind"`
	SourceApp        string          `json:"source_app"`
	OrderID          string          `json:"order_id,omitempty"`
	CampaignID       string          `json:"campaign_id,omitempty"`
	ProjectID        string          `json:"project_id"`
	TaskID           string          `json:"task_id,omitempty"`
	AssetRef         string          `json:"asset_ref,omitempty"`
	AssetVersion     string          `json:"asset_version"`
	UsageScope       string          `json:"usage_scope"`
	Permission       PermissionGrant `json:"permission"`
	SourceVersion    string          `json:"source_version"`
	SourceVersionSeq int64           `json:"source_version_seq"`
	ContinuedCount   int             `json:"continued_count"`
	ResumedExisting  bool            `json:"resumed_existing"`
	SideEffects      SideEffectAudit `json:"side_effects"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// CandidateReturnPayload is the immutable contract payload delivered to a
// registered target receiver (e.g., Touch HUI-3553 content pack receiver or
// order receiver). It never carries raw image plaintext or long-lived secrets.
type CandidateReturnPayload struct {
	ContractVersion        string          `json:"contract_version"`
	ReceiptID              string          `json:"receipt_id"`
	TenantID               string          `json:"tenant_id"`
	BrandID                string          `json:"brand_id"`
	SourceKind             string          `json:"source_kind"`
	OrderID                string          `json:"order_id,omitempty"`
	CampaignID             string          `json:"campaign_id,omitempty"`
	ProjectID              string          `json:"project_id"`
	TaskID                 string          `json:"task_id"`
	OutputID               string          `json:"output_id"`
	AssetRef               string          `json:"asset_ref"`
	AssetVersion           string          `json:"asset_version"`
	AssetSHA256            string          `json:"asset_sha256"`
	AssetSizeBytes         int64           `json:"asset_size_bytes"`
	MediaType              string          `json:"media_type"`
	UsageScope             string          `json:"usage_scope"`
	Permission             PermissionGrant `json:"permission"`
	SourceVersion          string          `json:"source_version"`
	SourceVersionSeq       int64           `json:"source_version_seq"`
	TargetApp              string          `json:"target_app"`
	TargetID               string          `json:"target_id"`
	DestinationKind        string          `json:"destination_kind"`
	LandingMode            string          `json:"landing_mode"`
	ManualReuploadRequired bool            `json:"manual_reupload_required"`
	AutoPublished          bool            `json:"auto_published"`
	AutoCouponIssued       bool            `json:"auto_coupon_issued"`
	AutoBilled             bool            `json:"auto_billed"`
	OccurredAt             string          `json:"occurred_at"`
}

// CandidateReturnRequest is the caller input to initiate an immutable candidate
// return & receipt delivery.
type CandidateReturnRequest struct {
	ReceiptID          string          `json:"receipt_id,omitempty"`
	TenantID           string          `json:"tenant_id"`
	BrandID            string          `json:"brand_id"`
	SourceKind         string          `json:"source_kind"`
	OrderID            string          `json:"order_id,omitempty"`
	CampaignID         string          `json:"campaign_id,omitempty"`
	ProjectID          string          `json:"project_id"`
	TaskID             string          `json:"task_id"`
	OutputID           string          `json:"output_id"`
	AssetRef           string          `json:"asset_ref"`
	AssetVersion       string          `json:"asset_version"`
	AssetSHA256        string          `json:"asset_sha256"`
	AssetSizeBytes     int64           `json:"asset_size_bytes"`
	MediaType          string          `json:"media_type"`
	UsageScope         string          `json:"usage_scope"`
	Permission         PermissionGrant `json:"permission"`
	SourceVersion      string          `json:"source_version"`
	SourceVersionSeq   int64           `json:"source_version_seq,omitempty"`
	TargetApp          string          `json:"target_app"`
	TargetID           string          `json:"target_id,omitempty"`
	DestinationKind    string          `json:"destination_kind,omitempty"`
	SignedAccessURL    string          `json:"signed_access_url,omitempty"`
	SignedURLExpiresAt string          `json:"signed_url_expires_at,omitempty"`
	ActorRole          string          `json:"actor_role,omitempty"`
	EntryTrigger       string          `json:"entry_trigger,omitempty"`
	RawImagePayload    string          `json:"raw_image_payload,omitempty"`
	FakeOrderBypass    bool            `json:"fake_order_bypass,omitempty"`
}

// DeliveryRecord records the immutable candidate return delivery & receipt
// lifecycle (pending_delivery -> delivered | delivery_failed -> adopted).
type DeliveryRecord struct {
	ReceiptID           string                 `json:"receipt_id"`
	DedupeKey           string                 `json:"dedupe_key"`
	PayloadFingerprint  string                 `json:"payload_fingerprint"`
	Payload             CandidateReturnPayload `json:"payload"`
	TargetURL           string                 `json:"target_url"`
	Status              string                 `json:"status"`
	Attempts            int                    `json:"attempts"`
	LastHTTPStatus      int                    `json:"last_http_status,omitempty"`
	LastErrorCode       string                 `json:"last_error_code,omitempty"`
	LastError           string                 `json:"last_error,omitempty"`
	ReceiverAckID       string                 `json:"receiver_ack_id,omitempty"`
	ReceiverStatus      string                 `json:"receiver_status,omitempty"`
	Adopted             bool                   `json:"adopted"`
	AdoptedAt           *time.Time             `json:"adopted_at,omitempty"`
	SideEffects         SideEffectAudit        `json:"side_effects"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
	immutablePayloadRaw []byte
}

// AdoptedVersionRecord pins the explicitly adopted version for a target context
// so late-arriving stale source_version deliveries can never overwrite it.
type AdoptedVersionRecord struct {
	TenantID         string    `json:"tenant_id"`
	TargetApp        string    `json:"target_app"`
	ContextKey       string    `json:"context_key"`
	ReceiptID        string    `json:"receipt_id"`
	ProjectID        string    `json:"project_id"`
	OutputID         string    `json:"output_id"`
	AssetVersion     string    `json:"asset_version"`
	SourceVersion    string    `json:"source_version"`
	SourceVersionSeq int64     `json:"source_version_seq"`
	AdoptedBy        string    `json:"adopted_by"`
	AdoptedAt        time.Time `json:"adopted_at"`
}

var (
	sha256LowerRe    = regexp.MustCompile(`^[a-f0-9]{64}$`)
	trailingDigitsRe = regexp.MustCompile(`(\d+)$`)
)

// IsVisitorContext reports whether the request originates from a visitor touch
// or view trigger, which is strictly forbidden from initiating generation or
// mutating production state.
func IsVisitorContext(actorRole, entryTrigger string) bool {
	role := strings.ToLower(strings.TrimSpace(actorRole))
	trigger := strings.ToLower(strings.TrimSpace(entryTrigger))
	return role == RoleVisitor || trigger == TriggerVisitorTouch || trigger == TriggerVisitorView
}

// IsForbiddenFloatingVersion rejects floating aliases ("latest", "current",
// "head", "*") so callers can never silently swap an immutable asset_version.
func IsForbiddenFloatingVersion(v string) bool {
	norm := strings.ToLower(strings.TrimSpace(v))
	switch norm {
	case "latest", "current", "head", "*", "auto", "newest":
		return true
	default:
		return false
	}
}

// ParseSourceVersionSeq resolves a positive monotonic sequence number for stale
// source_version protection. Explicit positive seq always takes precedence;
// otherwise a trailing numeric version suffix (e.g. "v2", "rev-3", "brief-10")
// is extracted, falling back to 1.
func ParseSourceVersionSeq(sourceVersion string, explicitSeq int64) int64 {
	if explicitSeq > 0 {
		return explicitSeq
	}
	m := trailingDigitsRe.FindStringSubmatch(strings.TrimSpace(sourceVersion))
	if len(m) == 2 {
		if n, err := strconv.ParseInt(m[1], 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 1
}

// ContextKey returns the stable target business context key: campaign_id for
// campaign, order_id for order, or project_id for standalone/bot without
// campaign/order.
func ContextKey(sourceKind, orderID, campaignID, projectID string) string {
	switch strings.TrimSpace(sourceKind) {
	case SourceCampaign:
		return "campaign:" + strings.TrimSpace(campaignID)
	case SourceOrder:
		return "order:" + strings.TrimSpace(orderID)
	default:
		if strings.TrimSpace(campaignID) != "" {
			return "campaign:" + strings.TrimSpace(campaignID)
		}
		if strings.TrimSpace(orderID) != "" {
			return "order:" + strings.TrimSpace(orderID)
		}
		return "project:" + strings.TrimSpace(projectID)
	}
}

// ValidateSignedURLWindow fails closed when a signed access URL is present or
// an expiration timestamp is supplied and has expired or cannot be parsed.
func ValidateSignedURLWindow(now time.Time, signedURL, expiresAtRaw string) *ContractError {
	urlTrim := strings.TrimSpace(signedURL)
	expTrim := strings.TrimSpace(expiresAtRaw)
	if urlTrim == "" && expTrim == "" {
		return nil
	}
	if urlTrim != "" && expTrim == "" {
		return newContractErr(
			http.StatusGone,
			CodeURLExpired,
			"签名访问链接缺少有效期，按安全策略拒绝；请针对同一 asset_version 重新换取短期签名链接（不重生成、不扣费）",
			RecoveryRefreshSignedURL,
			ErrURLExpired,
		)
	}
	exp, err := parseStrictRFC3339(expTrim)
	if err != nil || !now.UTC().Before(exp) {
		return newContractErr(
			http.StatusGone,
			CodeURLExpired,
			"签名访问链接已过期或时间无效；请针对同一不可变 asset_version 续签链接，重试不会触发图片生成、扣费、发布或发券",
			RecoveryRefreshSignedURL,
			ErrURLExpired,
		)
	}
	return nil
}

// ValidatePermissionGrant enforces active permission status and optional expiry.
func ValidatePermissionGrant(now time.Time, g PermissionGrant) *ContractError {
	status := strings.ToLower(strings.TrimSpace(g.Status))
	if status == "" {
		status = PermissionActive
	}
	switch status {
	case PermissionRevoked:
		return newContractErr(
			http.StatusForbidden,
			CodeRevoked,
			"素材或用途授权已被撤销，已按 fail-closed 拒绝；请重新获取有效授权后再继续（不触发生成、扣费、发布或发券）",
			RecoveryReauthorizeGrant,
			ErrRevoked,
		)
	case PermissionExpired:
		return newContractErr(
			http.StatusForbidden,
			CodeRevoked,
			"素材或用途授权已失效，请重新授权后再继续（不触发生成、扣费、发布或发券）",
			RecoveryReauthorizeGrant,
			ErrRevoked,
		)
	case PermissionActive:
	default:
		return newContractErr(
			http.StatusBadRequest,
			CodeInvalidRequest,
			fmt.Sprintf("不支持的权限状态 %q", g.Status),
			RecoveryReauthorizeGrant,
			ErrInvalidRequest,
		)
	}
	if strings.TrimSpace(g.GrantID) == "" {
		return newContractErr(
			http.StatusBadRequest,
			CodeInvalidRequest,
			"permission.grant_id 必填",
			RecoveryReauthorizeGrant,
			ErrInvalidRequest,
		)
	}
	if expRaw := strings.TrimSpace(g.ExpiresAt); expRaw != "" {
		exp, err := parseStrictRFC3339(expRaw)
		if err != nil || !now.UTC().Before(exp) {
			return newContractErr(
				http.StatusForbidden,
				CodeRevoked,
				"素材或用途授权已过期，请重新授权后再继续（不触发生成、扣费、发布或发券）",
				RecoveryReauthorizeGrant,
				ErrRevoked,
			)
		}
	}
	return nil
}

// ValidateSourceKindAndContext validates source_kind and ensures non-order
// entries never fabricate an order_id to bypass auth.
func ValidateSourceKindAndContext(sourceKind, orderID, campaignID string, fakeOrderBypass bool) *ContractError {
	if fakeOrderBypass {
		return newContractErr(
			http.StatusBadRequest,
			CodeFakeOrderForbidden,
			"禁止伪造订单绕过授权校验；活动/独立/Bot 入口必须使用各自受限授权续接原工程",
			RecoveryReauthorizeGrant,
			ErrFakeOrderForbidden,
		)
	}
	kind := strings.TrimSpace(sourceKind)
	ord := strings.TrimSpace(orderID)
	camp := strings.TrimSpace(campaignID)
	switch kind {
	case SourceOrder:
		if ord == "" {
			return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "order 来源必须提供 order_id", "", ErrInvalidRequest)
		}
	case SourceCampaign:
		if ord != "" {
			return newContractErr(
				http.StatusBadRequest,
				CodeFakeOrderForbidden,
				"campaign 来源禁止伪造或携带 order_id 绕过授权校验",
				RecoveryReauthorizeGrant,
				ErrFakeOrderForbidden,
			)
		}
		if camp == "" {
			return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "campaign 来源必须提供 campaign_id", "", ErrInvalidRequest)
		}
	case SourceStandalone:
		if ord != "" {
			return newContractErr(
				http.StatusBadRequest,
				CodeFakeOrderForbidden,
				"standalone 来源禁止伪造或携带 order_id 绕过授权校验",
				RecoveryReauthorizeGrant,
				ErrFakeOrderForbidden,
			)
		}
	case SourceBot, SourceNotification:
		// Bot / notification return can continue an existing order, campaign, or standalone project,
		// but cannot simultaneously claim both an order_id and a campaign_id.
		if ord != "" && camp != "" {
			return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "bot/notification 续接不可同时携带 order_id 与 campaign_id", "", ErrInvalidRequest)
		}
	default:
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, fmt.Sprintf("不支持的 source_kind %q", sourceKind), "", ErrInvalidRequest)
	}
	return nil
}

// AuditNoPlaintextImageOrSecret inspects any serialized payload or struct to
// enforce the HUI-1745 security redline: no raw image plaintext (data URI,
// base64 PNG/JPEG) and no internal filesystem paths or long-lived download
// secrets in cross-app events.
func AuditNoPlaintextImageOrSecret(v any) *ContractError {
	raw, err := json.Marshal(v)
	if err != nil {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "事件载荷序列化失败", "", ErrInvalidRequest)
	}
	return AuditRawEventBytes(raw)
}

// AuditRawEventBytes scans raw JSON event bytes for forbidden image plaintext
// or internal secret/path markers.
func AuditRawEventBytes(raw []byte) *ContractError {
	s := string(raw)
	lower := strings.ToLower(s)
	for _, marker := range []string{
		"data:image/",
		"ivborw0kggo", // base64 PNG magic
		"/9j/4aaq",    // base64 JPEG magic
		"raw_image_payload",
		"\"data_b64\"",
		"\"image_bytes\"",
	} {
		if strings.Contains(lower, marker) {
			return newContractErr(
				http.StatusBadRequest,
				CodePlaintextImageForbidden,
				"跨应用回流事件禁止携带图片明文或 Base64 字节，请仅传递不可变 asset_ref/asset_version 引用",
				"",
				ErrPlaintextImageForbidden,
			)
		}
	}
	for _, banned := range []string{
		"/var/lib/",
		"file://",
		"signature=",
		"x-oss-signature",
		"accesskeysecret",
		"aws_secret",
		"presigned_url",
		"client_secret",
	} {
		if strings.Contains(lower, banned) {
			return newContractErr(
				http.StatusBadRequest,
				CodeSecretLeakForbidden,
				"跨应用回流事件禁止携带内部路径或长期下载密钥",
				"",
				ErrSecretLeakForbidden,
			)
		}
	}
	return nil
}

// Validate validates all required fields and invariants on CandidateReturnPayload.
func (p *CandidateReturnPayload) Validate() *ContractError {
	if p.ContractVersion != ContractVersion {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "contract_version 必须为 "+ContractVersion, "", ErrInvalidRequest)
	}
	if ce := ValidateSourceKindAndContext(p.SourceKind, p.OrderID, p.CampaignID, false); ce != nil {
		return ce
	}
	required := map[string]string{
		"receipt_id":       p.ReceiptID,
		"tenant_id":        p.TenantID,
		"brand_id":         p.BrandID,
		"project_id":       p.ProjectID,
		"task_id":          p.TaskID,
		"output_id":        p.OutputID,
		"asset_ref":        p.AssetRef,
		"asset_version":    p.AssetVersion,
		"media_type":       p.MediaType,
		"usage_scope":      p.UsageScope,
		"source_version":   p.SourceVersion,
		"target_app":       p.TargetApp,
		"target_id":        p.TargetID,
		"destination_kind": p.DestinationKind,
		"occurred_at":      p.OccurredAt,
	}
	for name, val := range required {
		if !validTokenString(val, 256) {
			return newContractErr(http.StatusBadRequest, CodeInvalidRequest, name+" 必填且须 1–256 非空白字符", "", ErrInvalidRequest)
		}
	}
	if IsForbiddenFloatingVersion(p.AssetVersion) {
		return newContractErr(
			http.StatusConflict,
			CodeLatestSwapForbidden,
			"禁止使用 latest/floating 别名替代不可变 asset_version",
			RecoveryAdoptCurrentVersion,
			ErrLatestSwapForbidden,
		)
	}
	if !sha256LowerRe.MatchString(p.AssetSHA256) {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "asset_sha256 必须为 64 位小写 hex", "", ErrInvalidRequest)
	}
	if p.AssetSizeBytes <= 0 {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "asset_size_bytes 必须为正整数", "", ErrInvalidRequest)
	}
	if p.SourceVersionSeq < 1 {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "source_version_seq 必须为正整数", "", ErrInvalidRequest)
	}
	if p.LandingMode != LandingModeCandidateOnly {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "landing_mode 必须为 candidate_only（严禁自动发布或自动验收）", "", ErrInvalidRequest)
	}
	if p.ManualReuploadRequired || p.AutoPublished || p.AutoCouponIssued || p.AutoBilled {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "候选回流禁止要求手工下载重传，且禁止自动发布、自动发券或自动扣费", "", ErrInvalidRequest)
	}
	if _, err := parseStrictRFC3339(p.OccurredAt); err != nil {
		return newContractErr(http.StatusBadRequest, CodeInvalidRequest, "occurred_at 必须为有效 RFC3339 UTC 时间", "", ErrInvalidRequest)
	}
	return AuditNoPlaintextImageOrSecret(p)
}

func validTokenString(s string, max int) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	return utf8.RuneCountInString(s) <= max
}

func parseStrictRFC3339(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, err
	}
	if t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}, fmt.Errorf("year out of range")
	}
	return t.UTC(), nil
}

// DeterministicReceiptID builds a stable, collision-resistant receipt ID from
// the immutable return identity tuple so retries and duplicates always converge
// to the same receipt_id.
func DeterministicReceiptID(tenantID, targetApp, targetID, contextKey, projectID, outputID, assetVersion, sourceVersion string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(tenantID),
		strings.TrimSpace(targetApp),
		strings.TrimSpace(targetID),
		strings.TrimSpace(contextKey),
		strings.TrimSpace(projectID),
		strings.TrimSpace(outputID),
		strings.TrimSpace(assetVersion),
		strings.TrimSpace(sourceVersion),
	}, "\x1f")
	sum := sha256.Sum256([]byte(seed))
	return "rcpt_" + hex.EncodeToString(sum[:12])
}

// DedupeKeyFor builds the canonical deduplication key for candidate return.
func DedupeKeyFor(tenantID, targetApp, targetID, contextKey, projectID, outputID, assetVersion, sourceVersion string) string {
	seed := strings.Join([]string{
		strings.TrimSpace(tenantID),
		strings.TrimSpace(targetApp),
		strings.TrimSpace(targetID),
		strings.TrimSpace(contextKey),
		strings.TrimSpace(projectID),
		strings.TrimSpace(outputID),
		strings.TrimSpace(assetVersion),
		strings.TrimSpace(sourceVersion),
	}, "\x1f")
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func payloadFingerprint(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
