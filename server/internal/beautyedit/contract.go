// Package beautyedit implements HUI-3902 restricted beauty-portrait & makeup
// editing verification, independent human portrait authorization checks,
// adapter capability honesty (full_redraw vs pixel_locked_mask), multi-dimension
// non-target region preservation gating, version/revision protection, and
// candidate-only downstream handoff for shuhai / Bot / matrix consumers.
//
// Strict ecosystem boundaries:
//   - Zero second image engine, zero digital-human cloning service.
//   - Zero second billing ledger (integrates directly with fidelitygate + platform-billing).
//   - Zero social publishing and zero CRM/consultation reception.
//   - Product-image permission NEVER auto-expands to real-human portrait editing.
package beautyedit

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// EditMode enumerates the strictly verified beauty-portrait restricted editing modes.
// Unknown or unverified modes fail closed and are NEVER surfaced as supported controls.
type EditMode string

const (
	// ModeFidelityBackgroundEdit preserves the person's identity, skin texture,
	// and real makeup while modifying ONLY background or composition framing.
	// Eligible for UsageRealServiceProof when all multi-dimension checks pass.
	ModeFidelityBackgroundEdit EditMode = "fidelity_background_edit"

	// ModeCreativeMakeupDemo modifies makeup regions for creative illustration
	// or consultation demo ONLY. Always tagged usage_type=creative_demo and
	// is_real_service_proof=false; strictly forbidden from real service proof export.
	ModeCreativeMakeupDemo EditMode = "creative_makeup_demo"
)

// UsageType explicitly separates real-service photographic evidence from
// creative demonstration assets and unmodified originals.
type UsageType string

const (
	// UsageOriginalSource marks the unmodified authorized original portrait asset.
	UsageOriginalSource UsageType = "original_source"

	// UsageRealServiceProof marks a fidelity_background_edit output whose face
	// identity, skin texture, and real makeup passed non-target preservation QA.
	UsageRealServiceProof UsageType = "real_service_proof"

	// UsageCreativeDemo marks a creative_makeup_demo output. It can NEVER be
	// used or exported as real service proof.
	UsageCreativeDemo UsageType = "creative_demo"
)

// RegionKind classifies anatomical/semantic regions for declared allowed edits
// versus strictly preserved regions.
type RegionKind string

const (
	RegionFaceIdentity RegionKind = "face_identity"
	RegionSkinTexture  RegionKind = "skin_texture"
	RegionRealMakeup   RegionKind = "real_makeup"
	RegionHairClothing RegionKind = "hair_clothing"
	RegionBackground   RegionKind = "background"
	RegionComposition  RegionKind = "composition_framing"
	RegionMakeupRegion RegionKind = "makeup_region"
	RegionEyeMakeup    RegionKind = "eye_makeup"
	RegionLipMakeup    RegionKind = "lip_makeup"
	RegionBlushContour RegionKind = "blush_contour"
)

// AdapterEditCapability honestly declares how an underlying image-edit adapter
// operates. Providers that redraw the entire canvas MUST declare full_redraw
// and MUST NOT claim pixel-locked local editing.
type AdapterEditCapability string

const (
	// AdapterCapPixelLockedMask indicates the adapter enforces mask-locked local
	// editing preserving unmasked pixels directly.
	AdapterCapPixelLockedMask AdapterEditCapability = "pixel_locked_mask"

	// AdapterCapFullRedraw indicates the provider redraws the whole image even
	// when prompted for a local change; post-edit non-target region preservation
	// verification is MANDATORY.
	AdapterCapFullRedraw AdapterEditCapability = "full_redraw"
)

// PortraitUsageScope defines what a human portrait subject authorization permits.
type PortraitUsageScope string

const (
	// ScopeProductImageOnly represents standard product-image entitlement.
	// It NEVER grants real-human portrait editing permission.
	ScopeProductImageOnly PortraitUsageScope = "product_image_only"

	// ScopePortraitRealServiceEdit authorizes fidelity background/composition edit
	// preserving identity and real makeup.
	ScopePortraitRealServiceEdit PortraitUsageScope = "portrait_real_service_edit"

	// ScopePortraitCreativeMakeupDemo authorizes creative makeup demonstration edits.
	ScopePortraitCreativeMakeupDemo PortraitUsageScope = "portrait_creative_makeup_demo"

	// ScopePortraitBeautyAll authorizes both verified beauty edit modes explicitly.
	ScopePortraitBeautyAll PortraitUsageScope = "portrait_beauty_all"
)

// ExportPurpose declares how a caller intends to export or hand off an asset.
type ExportPurpose string

const (
	ExportPurposeRealServiceProof ExportPurpose = "real_service_proof"
	ExportPurposeCreativeDemo     ExportPurpose = "creative_demo"
	ExportPurposeCandidateReview  ExportPurpose = "candidate_review"
)

// HandoffConsumer identifies the downstream candidate recipient.
type HandoffConsumer string

const (
	ConsumerShuhai          HandoffConsumer = "shuhai"
	ConsumerBot             HandoffConsumer = "bot"
	ConsumerMatrixCandidate HandoffConsumer = "matrix_candidate"
)

const (
	DefaultCreativeDisclosureLabel = "创意妆面示意（非原服务实拍效果）"
)

var (
	// ErrEditModeNotVerified fails closed whenever an edit mode or (provider, model,
	// adapter_version, mode) combination has not been explicitly verified.
	ErrEditModeNotVerified = errors.New("beautyedit: edit mode or model/adapter combination is not verified")

	// ErrDishonestPixelLockClaim rejects any full_redraw adapter pretending to
	// support pixel-locked local editing.
	ErrDishonestPixelLockClaim = errors.New("beautyedit: full_redraw adapter cannot claim pixel-locked local editing")

	// ErrPortraitAuthRequired rejects any request lacking valid, unexpired,
	// mode-scoped human portrait subject authorization (including product-image-only auth).
	ErrPortraitAuthRequired = errors.New("beautyedit: independent valid human portrait authorization is required")

	// ErrLongLivedCredentialCopyForbidden rejects attempts to embed or copy
	// long-lived credentials/tokens into portrait authorization references.
	ErrLongLivedCredentialCopyForbidden = errors.New("beautyedit: copying long-lived credentials into portrait authorization ref is forbidden")

	// ErrInvalidRegionDeclaration rejects conflicting, empty, or mode-violating
	// allowed_modify_regions / preserved_regions declarations.
	ErrInvalidRegionDeclaration = errors.New("beautyedit: invalid allowed_modify_regions or preserved_regions declaration")

	// ErrUsageModeMismatch rejects mismatched (edit_mode, usage_type, is_real_service_proof)
	// combinations such as claiming real_service_proof on creative_makeup_demo.
	ErrUsageModeMismatch = errors.New("beautyedit: usage_type or is_real_service_proof conflicts with edit_mode")

	// ErrCreativeDemoExportAsRealProofForbidden blocks creative_makeup_demo assets
	// from ever being exported or handed off as real service proof.
	ErrCreativeDemoExportAsRealProofForbidden = errors.New("beautyedit: creative_makeup_demo output is strictly forbidden from real_service_proof export")

	// ErrSocialPublishOutOfScope explicitly enforces that HUI-3902 does not implement
	// external social publishing.
	ErrSocialPublishOutOfScope = errors.New("beautyedit: direct social publishing is out of scope; candidate handoff only")

	// ErrCRMReceptionOutOfScope explicitly enforces that HUI-3902 does not implement
	// CRM or consultation reception.
	ErrCRMReceptionOutOfScope = errors.New("beautyedit: CRM consultation reception is out of scope")
)

// IsKnownVerifiedMode returns true only for the two canonical HUI-3902 modes.
func IsKnownVerifiedMode(mode EditMode) bool {
	return mode == ModeFidelityBackgroundEdit || mode == ModeCreativeMakeupDemo
}

// CanonicalUsageForMode returns the required (UsageType, isRealServiceProof)
// contract for a verified edit mode.
func CanonicalUsageForMode(mode EditMode) (UsageType, bool, error) {
	switch mode {
	case ModeFidelityBackgroundEdit:
		return UsageRealServiceProof, true, nil
	case ModeCreativeMakeupDemo:
		return UsageCreativeDemo, false, nil
	default:
		return "", false, fmt.Errorf("%w: mode %q", ErrEditModeNotVerified, mode)
	}
}

// DefaultAllowedModifyRegions returns the canonical modifiable regions for a mode.
func DefaultAllowedModifyRegions(mode EditMode) ([]RegionKind, error) {
	switch mode {
	case ModeFidelityBackgroundEdit:
		return []RegionKind{RegionBackground, RegionComposition}, nil
	case ModeCreativeMakeupDemo:
		return []RegionKind{RegionMakeupRegion, RegionEyeMakeup, RegionLipMakeup, RegionBlushContour}, nil
	default:
		return nil, fmt.Errorf("%w: mode %q", ErrEditModeNotVerified, mode)
	}
}

// DefaultPreservedRegions returns the mandatory preserved regions for a mode.
func DefaultPreservedRegions(mode EditMode) ([]RegionKind, error) {
	switch mode {
	case ModeFidelityBackgroundEdit:
		return []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup}, nil
	case ModeCreativeMakeupDemo:
		return []RegionKind{RegionFaceIdentity, RegionHairClothing, RegionBackground}, nil
	default:
		return nil, fmt.Errorf("%w: mode %q", ErrEditModeNotVerified, mode)
	}
}

// ValidateRegionContract enforces mode-specific allowed_modify_regions and
// preserved_regions invariants with zero overlap.
func ValidateRegionContract(mode EditMode, allowed, preserved []RegionKind) error {
	if !IsKnownVerifiedMode(mode) {
		return fmt.Errorf("%w: mode %q", ErrEditModeNotVerified, mode)
	}
	if len(allowed) == 0 || len(preserved) == 0 {
		return fmt.Errorf("%w: both allowed_modify_regions and preserved_regions must be non-empty", ErrInvalidRegionDeclaration)
	}

	allowedSet := make(map[RegionKind]struct{}, len(allowed))
	for _, r := range allowed {
		if strings.TrimSpace(string(r)) == "" {
			return fmt.Errorf("%w: empty allowed region", ErrInvalidRegionDeclaration)
		}
		allowedSet[r] = struct{}{}
	}
	preservedSet := make(map[RegionKind]struct{}, len(preserved))
	for _, r := range preserved {
		if strings.TrimSpace(string(r)) == "" {
			return fmt.Errorf("%w: empty preserved region", ErrInvalidRegionDeclaration)
		}
		if _, conflict := allowedSet[r]; conflict {
			return fmt.Errorf("%w: region %q cannot be both allowed and preserved", ErrInvalidRegionDeclaration, r)
		}
		preservedSet[r] = struct{}{}
	}

	// Face identity MUST always be preserved in both beauty editing modes
	// (no person replacement or unauthorized cloning).
	if _, ok := preservedSet[RegionFaceIdentity]; !ok {
		return fmt.Errorf("%w: %s must be in preserved_regions", ErrInvalidRegionDeclaration, RegionFaceIdentity)
	}

	switch mode {
	case ModeFidelityBackgroundEdit:
		// Strict preservation of face identity, skin texture, and real makeup.
		for _, req := range []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup} {
			if _, ok := preservedSet[req]; !ok {
				return fmt.Errorf("%w: %s requires %s in preserved_regions", ErrInvalidRegionDeclaration, mode, req)
			}
		}
		// Only background and composition framing may be modified.
		for r := range allowedSet {
			if r != RegionBackground && r != RegionComposition {
				return fmt.Errorf("%w: %s forbids modifying %s", ErrInvalidRegionDeclaration, mode, r)
			}
		}
	case ModeCreativeMakeupDemo:
		// At least one makeup region must be declared modifiable, and face_identity
		// cannot be modified.
		hasMakeupTarget := false
		for r := range allowedSet {
			if isMakeupRegion(r) {
				hasMakeupTarget = true
			}
			if r == RegionFaceIdentity || r == RegionRealMakeup {
				return fmt.Errorf("%w: %s cannot modify %s directly", ErrInvalidRegionDeclaration, mode, r)
			}
		}
		if !hasMakeupTarget {
			return fmt.Errorf("%w: %s must declare at least one makeup target region", ErrInvalidRegionDeclaration, mode)
		}
	}
	return nil
}

func isMakeupRegion(r RegionKind) bool {
	switch r {
	case RegionMakeupRegion, RegionEyeMakeup, RegionLipMakeup, RegionBlushContour:
		return true
	default:
		return false
	}
}

// AdapterModeProfile defines the verified scope and limits of a model adapter
// for one specific EditMode.
type AdapterModeProfile struct {
	Mode                   EditMode     `json:"mode"`
	AllowedModifyRegions   []RegionKind `json:"allowed_modify_regions"`
	PreservedRegions       []RegionKind `json:"preserved_regions"`
	ReferenceConditions    []string     `json:"reference_conditions"`
	KnownLimitations       []string     `json:"known_limitations"`
	RequiresPostEditVerify bool         `json:"requires_post_edit_verify"`
}

func (p AdapterModeProfile) clone() AdapterModeProfile {
	cp := p
	cp.AllowedModifyRegions = slices.Clone(p.AllowedModifyRegions)
	cp.PreservedRegions = slices.Clone(p.PreservedRegions)
	cp.ReferenceConditions = slices.Clone(p.ReferenceConditions)
	cp.KnownLimitations = slices.Clone(p.KnownLimitations)
	return cp
}

// AdapterSpec describes a registered image-editing model/adapter combination
// and its verified beauty-portrait editing capabilities.
type AdapterSpec struct {
	ProviderID                 string                `json:"provider_id"`
	ModelID                    string                `json:"model_id"`
	ModelAdapterVersion        string                `json:"model_adapter_version"`
	AdapterEditCapability      AdapterEditCapability `json:"adapter_edit_capability"`
	ClaimsPixelLockedLocalEdit bool                  `json:"claims_pixel_locked_local_edit"`
	Verified                   bool                  `json:"verified"`
	VerifiedModes              []AdapterModeProfile  `json:"verified_modes"`
	KnownLimitations           []string              `json:"known_limitations,omitempty"`
}

func (s AdapterSpec) clone() AdapterSpec {
	cp := s
	cp.KnownLimitations = slices.Clone(s.KnownLimitations)
	if s.VerifiedModes != nil {
		cp.VerifiedModes = make([]AdapterModeProfile, len(s.VerifiedModes))
		for i, m := range s.VerifiedModes {
			cp.VerifiedModes[i] = m.clone()
		}
	}
	return cp
}

// Validate checks internal consistency and honesty invariants of AdapterSpec.
func (s AdapterSpec) Validate() error {
	if strings.TrimSpace(s.ProviderID) == "" ||
		strings.TrimSpace(s.ModelID) == "" ||
		strings.TrimSpace(s.ModelAdapterVersion) == "" {
		return fmt.Errorf("%w: provider_id, model_id, and model_adapter_version are required", ErrEditModeNotVerified)
	}
	switch s.AdapterEditCapability {
	case AdapterCapPixelLockedMask:
		// OK.
	case AdapterCapFullRedraw:
		if s.ClaimsPixelLockedLocalEdit {
			return fmt.Errorf("%w: adapter %s declares full_redraw yet claims pixel-locked local edit", ErrDishonestPixelLockClaim, s.ModelAdapterVersion)
		}
	default:
		return fmt.Errorf("%w: unknown adapter_edit_capability %q", ErrEditModeNotVerified, s.AdapterEditCapability)
	}

	seen := make(map[EditMode]struct{}, len(s.VerifiedModes))
	for _, prof := range s.VerifiedModes {
		if !IsKnownVerifiedMode(prof.Mode) {
			return fmt.Errorf("%w: mode %q is not a verified beauty edit mode", ErrEditModeNotVerified, prof.Mode)
		}
		if _, dup := seen[prof.Mode]; dup {
			return fmt.Errorf("%w: duplicate mode profile %q", ErrEditModeNotVerified, prof.Mode)
		}
		seen[prof.Mode] = struct{}{}
		if err := ValidateRegionContract(prof.Mode, prof.AllowedModifyRegions, prof.PreservedRegions); err != nil {
			return err
		}
		if s.AdapterEditCapability == AdapterCapFullRedraw && !prof.RequiresPostEditVerify {
			return fmt.Errorf("%w: full_redraw adapter %s must require post-edit non-target region verification for %s",
				ErrEditModeNotVerified, s.ModelAdapterVersion, prof.Mode)
		}
	}
	return nil
}

// CapabilityContract exposes the external capability metadata registered along
// existing product-image / public-ai Asset & Task contracts.
type CapabilityContract struct {
	ModelAdapterVersion    string                `json:"model_adapter_version"`
	ProviderID             string                `json:"provider_id"`
	ModelID                string                `json:"model_id"`
	AdapterEditCapability  AdapterEditCapability `json:"adapter_edit_capability"`
	VerifiedEditModes      []EditMode            `json:"verified_edit_modes"`
	AllowedModifyRegions   []RegionKind          `json:"allowed_modify_regions"`
	PreservedRegions       []RegionKind          `json:"preserved_regions"`
	ReferenceConditions    []string              `json:"reference_conditions"`
	KnownLimitations       []string              `json:"known_limitations"`
	RequiresPostEditVerify bool                  `json:"requires_post_edit_verify"`
}

// AdapterRegistry holds verified model/adapter combinations and fails closed on
// any unverified mode or unregistered adapter.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]AdapterSpec // key: model_adapter_version
}

// NewAdapterRegistry creates an empty fail-closed registry.
func NewAdapterRegistry() *AdapterRegistry {
	return &AdapterRegistry{
		adapters: make(map[string]AdapterSpec),
	}
}

// Register validates and registers an AdapterSpec. Dishonest full_redraw claims
// or invalid region declarations are rejected immediately.
func (r *AdapterRegistry) Register(spec AdapterSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[strings.TrimSpace(spec.ModelAdapterVersion)] = spec.clone()
	return nil
}

// ResolveVerified verifies that (modelAdapterVersion, mode) is registered,
// verified, and honestly configured. Fails closed with ErrEditModeNotVerified
// (or ErrDishonestPixelLockClaim).
func (r *AdapterRegistry) ResolveVerified(modelAdapterVersion string, mode EditMode) (AdapterSpec, AdapterModeProfile, error) {
	if r == nil {
		return AdapterSpec{}, AdapterModeProfile{}, fmt.Errorf("%w: adapter registry is nil", ErrEditModeNotVerified)
	}
	if !IsKnownVerifiedMode(mode) {
		return AdapterSpec{}, AdapterModeProfile{}, fmt.Errorf("%w: mode %q is unsupported", ErrEditModeNotVerified, mode)
	}
	key := strings.TrimSpace(modelAdapterVersion)
	if key == "" {
		return AdapterSpec{}, AdapterModeProfile{}, fmt.Errorf("%w: model_adapter_version is required", ErrEditModeNotVerified)
	}

	r.mu.RLock()
	spec, ok := r.adapters[key]
	r.mu.RUnlock()
	if !ok {
		return AdapterSpec{}, AdapterModeProfile{}, fmt.Errorf("%w: adapter %q is not registered", ErrEditModeNotVerified, key)
	}
	if err := spec.Validate(); err != nil {
		return AdapterSpec{}, AdapterModeProfile{}, err
	}
	if !spec.Verified {
		return AdapterSpec{}, AdapterModeProfile{}, fmt.Errorf("%w: adapter %q is not verified", ErrEditModeNotVerified, key)
	}
	for _, prof := range spec.VerifiedModes {
		if prof.Mode == mode {
			return spec.clone(), prof.clone(), nil
		}
	}
	return AdapterSpec{}, AdapterModeProfile{}, fmt.Errorf("%w: adapter %q has not verified mode %q", ErrEditModeNotVerified, key, mode)
}

// VerifiedEditModes returns ONLY the verified modes for an adapter so callers
// never render unverified UI buttons or falsely advertise unsupported modes.
func (r *AdapterRegistry) VerifiedEditModes(modelAdapterVersion string) []EditMode {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	spec, ok := r.adapters[strings.TrimSpace(modelAdapterVersion)]
	r.mu.RUnlock()
	if !ok || !spec.Verified {
		return nil
	}
	modes := make([]EditMode, 0, len(spec.VerifiedModes))
	for _, p := range spec.VerifiedModes {
		if IsKnownVerifiedMode(p.Mode) {
			modes = append(modes, p.Mode)
		}
	}
	return modes
}

// CapabilityContractFor builds the external capability contract for a verified
// (modelAdapterVersion, mode) pair.
func (r *AdapterRegistry) CapabilityContractFor(modelAdapterVersion string, mode EditMode) (CapabilityContract, error) {
	spec, prof, err := r.ResolveVerified(modelAdapterVersion, mode)
	if err != nil {
		return CapabilityContract{}, err
	}
	limitations := make([]string, 0, len(spec.KnownLimitations)+len(prof.KnownLimitations)+1)
	limitations = append(limitations, spec.KnownLimitations...)
	limitations = append(limitations, prof.KnownLimitations...)
	if spec.AdapterEditCapability == AdapterCapFullRedraw {
		limitations = append(limitations, "provider_full_redraw_only: pixel-locked local editing not supported; post-edit non-target region preservation check mandatory")
	}
	return CapabilityContract{
		ModelAdapterVersion:    spec.ModelAdapterVersion,
		ProviderID:             spec.ProviderID,
		ModelID:                spec.ModelID,
		AdapterEditCapability:  spec.AdapterEditCapability,
		VerifiedEditModes:      r.VerifiedEditModes(spec.ModelAdapterVersion),
		AllowedModifyRegions:   slices.Clone(prof.AllowedModifyRegions),
		PreservedRegions:       slices.Clone(prof.PreservedRegions),
		ReferenceConditions:    slices.Clone(prof.ReferenceConditions),
		KnownLimitations:       limitations,
		RequiresPostEditVerify: prof.RequiresPostEditVerify || spec.AdapterEditCapability == AdapterCapFullRedraw,
	}, nil
}

// PortraitAuthRecord is an ephemeral reference record queried from the original
// authorization service. It NEVER embeds long-lived credentials or raw tokens.
type PortraitAuthRecord struct {
	PortraitAuthRef            string             `json:"portrait_auth_ref"`
	PrincipalID                string             `json:"principal_id"`
	SubjectID                  string             `json:"subject_id"`
	IssuerService              string             `json:"issuer_service"`
	UsageScope                 PortraitUsageScope `json:"usage_scope"`
	AllowedModes               []EditMode         `json:"allowed_modes,omitempty"`
	ProductImagePermissionOnly bool               `json:"product_image_permission_only"`
	GrantedAt                  time.Time          `json:"granted_at"`
	ExpiresAt                  time.Time          `json:"expires_at"`
	Revoked                    bool               `json:"revoked"`
	// EmbeddedCredential MUST remain empty; populated values trigger
	// ErrLongLivedCredentialCopyForbidden fail-closed.
	EmbeddedCredential string `json:"embedded_credential,omitempty"`
}

// PortraitAuthLookup queries valid human portrait authorization references from
// the upstream/original authorization service without copying long-lived credentials.
type PortraitAuthLookup interface {
	LookupPortraitAuth(ctx context.Context, principalID, portraitAuthRef string) (PortraitAuthRecord, error)
}

// ValidatePortraitAuthorization enforces independent human portrait authorization:
//   - Rejects product-image-only permissions (never auto-expands to real humans).
//   - Rejects copied long-lived credentials/tokens.
//   - Rejects missing, revoked, expired, principal-mismatched, or scope-mismatched refs.
func ValidatePortraitAuthorization(auth PortraitAuthRecord, principalID string, mode EditMode, now time.Time) error {
	if !IsKnownVerifiedMode(mode) {
		return fmt.Errorf("%w: mode %q", ErrEditModeNotVerified, mode)
	}
	if strings.TrimSpace(auth.EmbeddedCredential) != "" || looksLikeSecretRef(auth.PortraitAuthRef) {
		return ErrLongLivedCredentialCopyForbidden
	}
	if auth.ProductImagePermissionOnly || auth.UsageScope == ScopeProductImageOnly {
		return fmt.Errorf("%w: product-image permission does not authorize real-human portrait editing", ErrPortraitAuthRequired)
	}
	if strings.TrimSpace(auth.PortraitAuthRef) == "" {
		return fmt.Errorf("%w: portrait_auth_ref is required", ErrPortraitAuthRequired)
	}
	if strings.TrimSpace(auth.SubjectID) == "" {
		return fmt.Errorf("%w: subject_id is required", ErrPortraitAuthRequired)
	}
	if strings.TrimSpace(auth.IssuerService) == "" {
		return fmt.Errorf("%w: issuer_service is required", ErrPortraitAuthRequired)
	}
	if strings.TrimSpace(principalID) == "" || strings.TrimSpace(auth.PrincipalID) != strings.TrimSpace(principalID) {
		return fmt.Errorf("%w: portrait authorization principal mismatch", ErrPortraitAuthRequired)
	}
	if auth.Revoked {
		return fmt.Errorf("%w: portrait authorization %q is revoked", ErrPortraitAuthRequired, auth.PortraitAuthRef)
	}
	if auth.ExpiresAt.IsZero() || !now.Before(auth.ExpiresAt) {
		return fmt.Errorf("%w: portrait authorization %q is missing expiry or expired", ErrPortraitAuthRequired, auth.PortraitAuthRef)
	}
	if !auth.GrantedAt.IsZero() && now.Before(auth.GrantedAt) {
		return fmt.Errorf("%w: portrait authorization %q is not yet effective", ErrPortraitAuthRequired, auth.PortraitAuthRef)
	}

	if !usageScopeAllowsMode(auth.UsageScope, mode) {
		return fmt.Errorf("%w: usage_scope %q does not authorize mode %q", ErrPortraitAuthRequired, auth.UsageScope, mode)
	}
	if len(auth.AllowedModes) > 0 && !slices.Contains(auth.AllowedModes, mode) {
		return fmt.Errorf("%w: mode %q is not in portrait authorization allowed_modes", ErrPortraitAuthRequired, mode)
	}
	return nil
}

func usageScopeAllowsMode(scope PortraitUsageScope, mode EditMode) bool {
	switch scope {
	case ScopePortraitBeautyAll:
		return mode == ModeFidelityBackgroundEdit || mode == ModeCreativeMakeupDemo
	case ScopePortraitRealServiceEdit:
		return mode == ModeFidelityBackgroundEdit
	case ScopePortraitCreativeMakeupDemo:
		return mode == ModeCreativeMakeupDemo
	default:
		return false
	}
}

func looksLikeSecretRef(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	if lower == "" {
		return false
	}
	for _, prefix := range []string{"bearer ", "sk-", "ak_", "secret_", "token="} {
		if strings.HasPrefix(lower, prefix) || strings.Contains(lower, "-----begin ") {
			return true
		}
	}
	return false
}
