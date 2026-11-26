package i2vcontract

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ExecutionPreference enumerates the only server-permitted execution preferences.
// Arbitrary numeric priorities are strictly forbidden.
type ExecutionPreference string

const (
	PrefImmediate ExecutionPreference = "immediate"
	PrefNight     ExecutionPreference = "night"
	PrefIdle      ExecutionPreference = "idle"
)

// AllowedExecutionPreferences returns the canonical server-permitted preferences.
func AllowedExecutionPreferences() []ExecutionPreference {
	return []ExecutionPreference{PrefImmediate, PrefNight, PrefIdle}
}

// PayerType identifies who bears the cost of an I2V task.
type PayerType string

const (
	PayerPersonal  PayerType = "personal"
	PayerOrg       PayerType = "org"
	PayerFreeGrant PayerType = "free_grant"
)

// QueueState is the user-safe projection of an asynchronous task's queue lifecycle.
type QueueState string

const (
	QueueStateQueued        QueueState = "queued"
	QueueStateWaitingWindow QueueState = "waiting_window"
	QueueStateDispatching   QueueState = "dispatching"
	QueueStateRunning       QueueState = "running"
	QueueStateEnhancing     QueueState = "enhancing"
	QueueStateDownloading   QueueState = "downloading"
	QueueStateCompleted     QueueState = "completed"
	QueueStateFailed        QueueState = "failed"
	QueueStateBlocked       QueueState = "blocked"
	QueueStateCancelled     QueueState = "cancelled"
	QueueStateUnknown       QueueState = "unknown"
)

// TaskStage identifies the discrete execution stage for stage-scoped counters & retries.
type TaskStage string

const (
	StageGenerate TaskStage = "generate"
	StageEnhance  TaskStage = "enhance"
	StageDownload TaskStage = "download"
	StageVerify   TaskStage = "verify"
)

var (
	ErrInvalidProductRef             = errors.New("i2vcontract: fixed product reference requires asset_id, version_id, valid sha256, and authorization grant")
	ErrProductReferenceTampered      = errors.New("i2vcontract: product reference was cleared or mutated across queue wait")
	ErrInvalidExecutionPreference    = errors.New("i2vcontract: execution_preference must be one of server-allowed immediate/night/idle")
	ErrArbitraryPriorityForbidden    = errors.New("i2vcontract: arbitrary numeric/custom queue priority is forbidden")
	ErrInvalidCostAttribution        = errors.New("i2vcontract: invalid payer_type or billing attribution")
	ErrFreeQuotaExpiredNoAutoCharge  = errors.New("i2vcontract: free quota expired or exhausted; auto-charging paid balance is forbidden")
	ErrFreeAutoChargeForbidden       = errors.New("i2vcontract: free_grant tasks must never enable auto_convert_to_paid")
	ErrFreeTierInternalCardForbidden = errors.New("i2vcontract: free-tier eligibility must never access internal R&D GPU cards")
	ErrBackendCostWalletMisuse       = errors.New("i2vcontract: backend cost wallet must never be presented as customer balance")
	ErrInternalTokenExposure         = errors.New("i2vcontract: internal pool/autodl/runtime token exposure is forbidden")
	ErrAuthorizationRevoked          = errors.New("i2vcontract: product asset authorization is revoked, expired, or out of scope")
	ErrSignedURLInvalidOrExpired     = errors.New("i2vcontract: renewed signed URL is empty, placeholder, or expired")
)

var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// SubjectConstraint locks product geometry and brand identifiers across generation.
type SubjectConstraint struct {
	MaskRef             string         `json:"mask_ref,omitempty"`
	PreserveStructure   bool           `json:"preserve_structure"`
	PreserveLogoAndText bool           `json:"preserve_logo_and_text"`
	View                ViewProvenance `json:"view"`
}

// AuthorizationRef persists the authorization proof bound to the product image.
// Raw secrets/tokens must never be stored here.
type AuthorizationRef struct {
	GrantID      string     `json:"grant_id"`
	AuthorizedBy string     `json:"authorized_by"`
	TenantID     string     `json:"tenant_id"`
	ProjectID    string     `json:"project_id"`
	Scope        string     `json:"scope,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Revoked      bool       `json:"revoked"`
}

// FixedProductRef is the immutable product identity persisted with the task so
// waiting in night/idle queues can never drop the reference or swap the product.
type FixedProductRef struct {
	AssetID       string            `json:"asset_id"`
	VersionID     string            `json:"version_id"`
	SHA256        string            `json:"sha256"`
	SKURef        string            `json:"sku_ref,omitempty"`
	SubjectLock   SubjectConstraint `json:"subject_lock"`
	Authorization AuthorizationRef  `json:"authorization"`
}

// CanonicalNormalized returns a whitespace-trimmed, lowercase-digest copy.
func (r FixedProductRef) CanonicalNormalized() FixedProductRef {
	cp := r
	cp.AssetID = strings.TrimSpace(r.AssetID)
	cp.VersionID = strings.TrimSpace(r.VersionID)
	cp.SHA256 = strings.ToLower(strings.TrimSpace(r.SHA256))
	cp.SKURef = strings.TrimSpace(r.SKURef)
	cp.SubjectLock.MaskRef = strings.TrimSpace(r.SubjectLock.MaskRef)
	if r.SubjectLock.View.CapturedAngles != nil {
		cp.SubjectLock.View.CapturedAngles = append([]string(nil), r.SubjectLock.View.CapturedAngles...)
	}
	cp.Authorization.GrantID = strings.TrimSpace(r.Authorization.GrantID)
	cp.Authorization.AuthorizedBy = strings.TrimSpace(r.Authorization.AuthorizedBy)
	cp.Authorization.TenantID = strings.TrimSpace(r.Authorization.TenantID)
	cp.Authorization.ProjectID = strings.TrimSpace(r.Authorization.ProjectID)
	cp.Authorization.Scope = strings.TrimSpace(r.Authorization.Scope)
	if r.Authorization.ExpiresAt != nil {
		t := r.Authorization.ExpiresAt.UTC()
		cp.Authorization.ExpiresAt = &t
	}
	return cp
}

// Validate enforces immutable product identity and active authorization before enqueueing.
func (r FixedProductRef) Validate(tenantID, projectID string, now time.Time) error {
	n := r.CanonicalNormalized()
	if n.AssetID == "" || n.VersionID == "" || !sha256HexPattern.MatchString(n.SHA256) {
		return ErrInvalidProductRef
	}
	if !n.SubjectLock.PreserveStructure || !n.SubjectLock.PreserveLogoAndText {
		return fmt.Errorf("%w: subject structure and logo/text preservation must be locked", ErrInvalidProductRef)
	}
	if n.Authorization.GrantID == "" || n.Authorization.AuthorizedBy == "" {
		return ErrInvalidProductRef
	}
	if n.Authorization.Revoked {
		return ErrAuthorizationRevoked
	}
	if n.Authorization.TenantID != "" && strings.TrimSpace(tenantID) != "" && n.Authorization.TenantID != strings.TrimSpace(tenantID) {
		return ErrAuthorizationRevoked
	}
	if n.Authorization.ProjectID != "" && strings.TrimSpace(projectID) != "" && n.Authorization.ProjectID != strings.TrimSpace(projectID) {
		return ErrAuthorizationRevoked
	}
	if n.Authorization.ExpiresAt != nil && !now.IsZero() && now.After(*n.Authorization.ExpiresAt) {
		return ErrAuthorizationRevoked
	}
	return nil
}

// MatchesImmutable verifies that a post-wait/post-renewal reference still points to
// the exact same product asset, version, digest, SKU, and subject lock.
func (r FixedProductRef) MatchesImmutable(other FixedProductRef) error {
	a := r.CanonicalNormalized()
	b := other.CanonicalNormalized()
	if b.AssetID == "" || b.VersionID == "" || b.SHA256 == "" {
		return fmt.Errorf("%w: reference was cleared", ErrProductReferenceTampered)
	}
	if a.AssetID != b.AssetID || a.VersionID != b.VersionID || a.SHA256 != b.SHA256 {
		return fmt.Errorf("%w: expected (%s,%s,%s), got (%s,%s,%s)",
			ErrProductReferenceTampered, a.AssetID, a.VersionID, a.SHA256, b.AssetID, b.VersionID, b.SHA256)
	}
	if a.SKURef != b.SKURef {
		return fmt.Errorf("%w: sku_ref changed from %q to %q", ErrProductReferenceTampered, a.SKURef, b.SKURef)
	}
	if a.SubjectLock.MaskRef != b.SubjectLock.MaskRef ||
		a.SubjectLock.PreserveStructure != b.SubjectLock.PreserveStructure ||
		a.SubjectLock.PreserveLogoAndText != b.SubjectLock.PreserveLogoAndText {
		return fmt.Errorf("%w: subject lock modified", ErrProductReferenceTampered)
	}
	return nil
}

// ExecutionPolicyRequest carries the user's selected server-allowed execution preference
// together with explicit anti-arbitrary-priority and server-side pool guard inputs.
type ExecutionPolicyRequest struct {
	AllowedPreferences []ExecutionPreference `json:"allowed_preferences,omitempty"`
	SelectedPreference ExecutionPreference   `json:"execution_preference"`
	PriorityNumber     *int                  `json:"priority_number,omitempty"`
	CustomPriority     string                `json:"custom_priority,omitempty"`
	DeferUntilWindow   bool                  `json:"defer_until_window,omitempty"`
	NextWindowAt       *time.Time            `json:"next_window_at,omitempty"`
	WaitReason         string                `json:"wait_reason,omitempty"`
	ETASeconds         *int64                `json:"eta_seconds,omitempty"`
	TargetPoolClass    PoolClass             `json:"-"` // strictly server-side, never user-exposed
}

// CostAttribution records who bears the cost and guards free-tier & billing boundaries.
type CostAttribution struct {
	PayerType          PayerType  `json:"payer_type"`
	BillingAccountRef  string     `json:"billing_account_ref,omitempty"`
	QuoteID            string     `json:"quote_id"`
	PricingVersion     string     `json:"pricing_version"`
	EstimatedCostFen   int64      `json:"estimated_cost_fen"`
	FreeQuotaGrantID   string     `json:"free_quota_grant_id,omitempty"`
	FreeQuotaExpiresAt *time.Time `json:"free_quota_expires_at,omitempty"`
	FreeQuotaRemaining int        `json:"free_quota_remaining,omitempty"`
	AutoConvertToPaid  bool       `json:"auto_convert_to_paid"`
}

// ValidateExecutionAndBillingPolicy enforces all HUI-1702 MX-OPS execution & cost guards:
//   - Only server-permitted preferences (immediate / night / idle) are accepted.
//   - Arbitrary priority numbers or custom priority strings are strictly rejected.
//   - Free tier NEVER accesses internal R&D GPU cards.
//   - Expired or exhausted free quota NEVER auto-charges paid balance.
//   - Backend cost wallets are NEVER accepted as customer billing balance.
func ValidateExecutionAndBillingPolicy(policy ExecutionPolicyRequest, cost CostAttribution, now time.Time) (string, error) {
	if policy.PriorityNumber != nil || strings.TrimSpace(policy.CustomPriority) != "" {
		return BlockArbitraryPriorityForbidden, ErrArbitraryPriorityForbidden
	}

	allowed := policy.AllowedPreferences
	if len(allowed) == 0 {
		allowed = AllowedExecutionPreferences()
	}
	if !isCanonicalPreference(policy.SelectedPreference) || !containsPreference(allowed, policy.SelectedPreference) {
		return BlockInvalidExecutionPreference, fmt.Errorf("%w: %q", ErrInvalidExecutionPreference, policy.SelectedPreference)
	}

	if isBackendCostWalletRef(cost.BillingAccountRef) {
		return BlockBackendCostWalletMisuse, ErrBackendCostWalletMisuse
	}

	switch cost.PayerType {
	case PayerFreeGrant:
		if cost.AutoConvertToPaid {
			return BlockFreeQuotaExpiredNoAutoCharge, ErrFreeAutoChargeForbidden
		}
		if policy.TargetPoolClass == PoolInternalRDCard {
			return BlockFreeTierInternalCardForbidden, ErrFreeTierInternalCardForbidden
		}
		if strings.TrimSpace(cost.FreeQuotaGrantID) == "" || cost.FreeQuotaRemaining <= 0 {
			return BlockFreeQuotaExpiredNoAutoCharge, ErrFreeQuotaExpiredNoAutoCharge
		}
		if cost.FreeQuotaExpiresAt != nil && !now.IsZero() && now.After(*cost.FreeQuotaExpiresAt) {
			return BlockFreeQuotaExpiredNoAutoCharge, ErrFreeQuotaExpiredNoAutoCharge
		}
	case PayerPersonal, PayerOrg:
		if strings.TrimSpace(cost.QuoteID) == "" || strings.TrimSpace(cost.PricingVersion) == "" || cost.EstimatedCostFen <= 0 {
			return BlockInvalidExecutionPreference, fmt.Errorf("%w: paid task requires quote_id, pricing_version, and positive estimated_cost_fen", ErrInvalidCostAttribution)
		}
		if strings.TrimSpace(cost.BillingAccountRef) == "" {
			return BlockInvalidExecutionPreference, fmt.Errorf("%w: billing_account_ref is required for paid payer", ErrInvalidCostAttribution)
		}
	default:
		return BlockInvalidExecutionPreference, fmt.Errorf("%w: unknown payer_type %q", ErrInvalidCostAttribution, cost.PayerType)
	}

	return "", nil
}

// StageCounter tracks per-stage counts for batch/project observability.
type StageCounter struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Running   int `json:"running"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Retried   int `json:"retried"`
}

// BatchStageCounters separates image vs video and generate vs enhance vs download counts.
type BatchStageCounters struct {
	ImageGenerate StageCounter `json:"image_generate"`
	ImageEnhance  StageCounter `json:"image_enhance"`
	VideoGenerate StageCounter `json:"video_generate"`
	VideoEnhance  StageCounter `json:"video_enhance"`
	Download      StageCounter `json:"download"`
}

// QueueProjection is the user-safe job queue view projected from the coordinator/XModel job.
// It deliberately omits pool IDs, AutoDL fields, runtime tokens, and internal GPU metadata.
type QueueProjection struct {
	JobID               string              `json:"job_id"`
	QueueState          QueueState          `json:"queue_state"`
	WaitReason          string              `json:"wait_reason,omitempty"`
	NextWindowAt        *time.Time          `json:"next_window_at"`
	ETASeconds          *int64              `json:"eta_seconds"`
	ActiveStage         TaskStage           `json:"active_stage"`
	ExecutionPreference ExecutionPreference `json:"execution_preference"`
	PayerType           PayerType           `json:"payer_type"`
	StageCounters       BatchStageCounters  `json:"stage_counters"`
}

// forbiddenInternalKeyTokens lists internal infrastructure terms that must NEVER
// appear in user-facing JSON keys or token payloads.
var forbiddenInternalKeyTokens = []string{
	"pool_id",
	"pool_token",
	"autodl",
	"runtime_token",
	"internal_token",
	"internal_rd_card",
	"internal_card",
	"cost_wallet",
	"backend_cost_wallet",
	"platform_cost_pool",
	"gpu_node_token",
}

// AuditNoInternalTokenLeak marshals v to JSON and recursively verifies that no
// internal pool, AutoDL, runtime token, internal R&D card, or backend cost wallet
// identifiers leak into a user-facing response payload.
func AuditNoInternalTokenLeak(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("i2vcontract: audit marshal failed: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("i2vcontract: audit unmarshal failed: %w", err)
	}
	return walkAuditNode(decoded, "")
}

func walkAuditNode(node any, path string) error {
	switch typed := node.(type) {
	case map[string]any:
		for k, val := range typed {
			lowerKey := strings.ToLower(strings.TrimSpace(k))
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			for _, bad := range forbiddenInternalKeyTokens {
				if strings.Contains(lowerKey, bad) {
					return fmt.Errorf("%w: forbidden key %q", ErrInternalTokenExposure, childPath)
				}
			}
			if err := walkAuditNode(val, childPath); err != nil {
				return err
			}
		}
	case []any:
		for i, elem := range typed {
			if err := walkAuditNode(elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case string:
		lowerVal := strings.ToLower(strings.TrimSpace(typed))
		for _, prefix := range []string{"autodl_", "autodl-", "pool_tok_", "runtime_tok_", "rd_card_", "cost_wallet_"} {
			if strings.Contains(lowerVal, prefix) {
				return fmt.Errorf("%w: forbidden token value at %q", ErrInternalTokenExposure, path)
			}
		}
	}
	return nil
}

// RenewedAssetURL is returned by URLRenewer right before night/idle dispatch.
// AssetID, VersionID, SHA256, and SKURef MUST match the persisted FixedProductRef.
type RenewedAssetURL struct {
	AssetID   string    `json:"asset_id"`
	VersionID string    `json:"version_id"`
	SHA256    string    `json:"sha256"`
	SKURef    string    `json:"sku_ref,omitempty"`
	SignedURL string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Validate checks that the renewed URL is non-empty, non-placeholder, unexpired,
// and still bound to the exact immutable product reference.
func (u RenewedAssetURL) Validate(expected FixedProductRef, now time.Time) error {
	signed := strings.TrimSpace(u.SignedURL)
	if signed == "" || isPlaceholderURL(signed) {
		return ErrSignedURLInvalidOrExpired
	}
	if !now.IsZero() && !u.ExpiresAt.IsZero() && !u.ExpiresAt.After(now) {
		return ErrSignedURLInvalidOrExpired
	}
	exp := expected.CanonicalNormalized()
	if strings.TrimSpace(u.AssetID) != exp.AssetID ||
		strings.TrimSpace(u.VersionID) != exp.VersionID ||
		strings.ToLower(strings.TrimSpace(u.SHA256)) != exp.SHA256 ||
		strings.TrimSpace(u.SKURef) != exp.SKURef {
		return fmt.Errorf("%w: renewed URL identity mismatch", ErrProductReferenceTampered)
	}
	return nil
}

func isCanonicalPreference(pref ExecutionPreference) bool {
	return pref == PrefImmediate || pref == PrefNight || pref == PrefIdle
}

func containsPreference(list []ExecutionPreference, want ExecutionPreference) bool {
	for _, p := range list {
		if p == want {
			return true
		}
	}
	return false
}

func isBackendCostWalletRef(ref string) bool {
	norm := strings.ToLower(strings.TrimSpace(ref))
	if norm == "" {
		return false
	}
	for _, bad := range []string{"cost_wallet", "cost-wallet", "backend_wallet", "platform_cost_pool", "internal_cost"} {
		if strings.Contains(norm, bad) {
			return true
		}
	}
	return false
}
