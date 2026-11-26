package fidelitygate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
)

// BillingState tracks the remote platform-billing lifecycle of a single hold.
// Zero second billing ledger: balances and accounting live exclusively in platform-billing.
type BillingState string

const (
	BillingInit     BillingState = "init"
	BillingHeld     BillingState = "held"
	BillingSpent    BillingState = "spent"
	BillingRefunded BillingState = "refunded"
	BillingUnknown  BillingState = "unknown"
)

// CandidateAdoptionState tracks whether a generated candidate image may be adopted.
// Invariant: CandidateAdopted is reachable ONLY after EvaluateI2XQualityGate passes
// AND platform-billing confirms Spend.
type CandidateAdoptionState string

const (
	CandidateNone        CandidateAdoptionState = "none"
	CandidatePendingGate CandidateAdoptionState = "pending_gate"
	CandidateRejected    CandidateAdoptionState = "rejected"
	CandidateAdopted     CandidateAdoptionState = "adopted"
)

// PendingBillingAction identifies which platform-billing RPC became ambiguous on timeout/loss.
type PendingBillingAction string

const (
	ActionNone   PendingBillingAction = "none"
	ActionHold   PendingBillingAction = "hold"
	ActionSpend  PendingBillingAction = "spend"
	ActionRefund PendingBillingAction = "refund"
)

// GateFailureBillingPolicy explicitly governs settlement when the quality gate rejects
// a candidate AFTER the upstream image provider has already generated an image.
type GateFailureBillingPolicy string

const (
	// GateFailurePolicySettleProviderSpend settles real provider spend when the
	// provider charged for generation (or refunds if provider did not charge).
	// Candidate is NEVER marked adopted.
	GateFailurePolicySettleProviderSpend GateFailureBillingPolicy = "settle_provider_spend"

	// GateFailurePolicyRefundPlatformAbsorbed explicitly refunds the user's hold
	// under platform-absorbed quality failure policy. Candidate is NEVER marked adopted.
	GateFailurePolicyRefundPlatformAbsorbed GateFailureBillingPolicy = "refund_platform_absorbed"
)

var (
	ErrNilBillingClient         = errors.New("fidelitygate: platform billing client is nil")
	ErrInvalidBillingScope      = errors.New("fidelitygate: invalid billing scope (requires X-App-ID=product-image, principal_id, request_key)")
	ErrInvalidHoldAmount        = errors.New("fidelitygate: hold amount_minor must be positive")
	ErrInvalidBillingPolicy     = errors.New("fidelitygate: explicit gate failure billing policy is required")
	ErrRecordNotFound           = errors.New("fidelitygate: billing recovery record not found")
	ErrReconcileRequired        = errors.New("fidelitygate: billing state is unknown; must reconcile by original hold_id/request_key before retry or release")
	ErrBillingUnknown           = errors.New("fidelitygate: billing transition outcome unknown due to timeout or lost response")
	ErrAlreadySettled           = errors.New("fidelitygate: billing hold already reached terminal state")
	ErrInvalidStateTransition   = errors.New("fidelitygate: invalid billing state transition")
	ErrCandidateAlreadyRejected = errors.New("fidelitygate: candidate already rejected by quality gate; cannot adopt on same request_key")
	ErrTransportAmbiguous       = errors.New("fidelitygate: ambiguous transport error (timeout or lost response)")
)

// BillingScope binds every platform-billing call to X-App-ID: product-image,
// platform principal_id (never a second user ID), and caller request_key.
type BillingScope struct {
	AppID       string `json:"app_id"`
	PrincipalID string `json:"principal_id"`
	RequestKey  string `json:"request_key"`
}

// Validate enforces strict X-App-ID: product-image and principal_id binding.
func (s BillingScope) Validate() error {
	if strings.TrimSpace(s.AppID) != AppID ||
		strings.TrimSpace(s.PrincipalID) == "" ||
		strings.TrimSpace(s.RequestKey) == "" {
		return ErrInvalidBillingScope
	}
	return nil
}

// HoldRequest maps to platform-billing Hold RPC.
type HoldRequest struct {
	Scope          BillingScope `json:"scope"`
	IdempotencyKey string       `json:"idempotency_key"`
	Capability     string       `json:"capability"`
	PricingVersion string       `json:"pricing_version"`
	AmountMinor    int64        `json:"amount_minor"`
}

// HoldResponse returns the authoritative platform-billing hold_id.
type HoldResponse struct {
	HoldID string `json:"hold_id"`
}

// SpendRequest maps to platform-billing Spend RPC bound to an existing hold_id.
type SpendRequest struct {
	Scope          BillingScope `json:"scope"`
	HoldID         string       `json:"hold_id"`
	IdempotencyKey string       `json:"idempotency_key"`
	Reason         string       `json:"reason"`
}

// SpendResponse returns the authoritative platform-billing spend_id.
type SpendResponse struct {
	SpendID string `json:"spend_id"`
}

// RefundRequest maps to platform-billing Refund/Release RPC bound to an existing hold_id.
type RefundRequest struct {
	Scope          BillingScope `json:"scope"`
	HoldID         string       `json:"hold_id"`
	IdempotencyKey string       `json:"idempotency_key"`
	Reason         string       `json:"reason"`
}

// RefundResponse returns the authoritative platform-billing refund_id.
type RefundResponse struct {
	RefundID string `json:"refund_id"`
}

// LookupHoldRequest queries platform-billing authoritative state by original
// (principal_id, request_key, hold_id) during crash or timeout reconciliation.
type LookupHoldRequest struct {
	Scope  BillingScope `json:"scope"`
	HoldID string       `json:"hold_id"`
}

// LookupHoldResponse reports authoritative remote state from platform-billing.
type LookupHoldResponse struct {
	HoldID   string       `json:"hold_id"`
	State    BillingState `json:"state"`
	SpendID  string       `json:"spend_id,omitempty"`
	RefundID string       `json:"refund_id,omitempty"`
}

// PlatformBillingClient is the remote port to platform-billing.
// Implementations must send X-App-ID: product-image and per-app internal token.
type PlatformBillingClient interface {
	Hold(ctx context.Context, req HoldRequest) (HoldResponse, error)
	Spend(ctx context.Context, req SpendRequest) (SpendResponse, error)
	Refund(ctx context.Context, req RefundRequest) (RefundResponse, error)
	LookupHold(ctx context.Context, req LookupHoldRequest) (LookupHoldResponse, error)
}

// RecoveryRecord captures protocol coordination state for a single
// (principal_id, request_key, hold_id) lifecycle. It holds zero wallet balance.
type RecoveryRecord struct {
	AppID             string                 `json:"app_id"`
	PrincipalID       string                 `json:"principal_id"`
	RequestKey        string                 `json:"request_key"`
	HoldID            string                 `json:"hold_id,omitempty"`
	SpendID           string                 `json:"spend_id,omitempty"`
	RefundID          string                 `json:"refund_id,omitempty"`
	State             BillingState           `json:"state"`
	CandidateState    CandidateAdoptionState `json:"candidate_state"`
	PendingAction     PendingBillingAction   `json:"pending_action"`
	ReconcileRequired bool                   `json:"reconcile_required"`
	LastGateDecision  *GateDecision          `json:"last_gate_decision,omitempty"`
	LastError         string                 `json:"last_error,omitempty"`
}

func (r RecoveryRecord) clone() RecoveryRecord {
	cp := r
	if r.LastGateDecision != nil {
		d := *r.LastGateDecision
		if d.FailureReasons != nil {
			d.FailureReasons = append([]string(nil), d.FailureReasons...)
		}
		cp.LastGateDecision = &d
	}
	return cp
}

// Deterministic idempotency keys ensure retries never mutate keys and never double-charge.
func HoldIdempotencyKey(requestKey string) string {
	return AppID + ":hold:" + strings.TrimSpace(requestKey)
}

func SpendIdempotencyKey(requestKey, holdID string) string {
	return AppID + ":spend:" + strings.TrimSpace(requestKey) + ":" + strings.TrimSpace(holdID)
}

func RefundIdempotencyKey(requestKey, holdID string) string {
	return AppID + ":refund:" + strings.TrimSpace(requestKey) + ":" + strings.TrimSpace(holdID)
}

// IsAmbiguousTransportError reports whether an error leaves the remote platform-billing
// mutation outcome unknown (e.g. context deadline exceeded, network timeout, or lost response).
func IsAmbiguousTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrTransportAmbiguous) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

type recordKey struct {
	principalID string
	requestKey  string
}

type recordEntry struct {
	mu  sync.Mutex
	rec RecoveryRecord
}

// BillingRecoveryStateMachine coordinates Hold -> Spend / Refund / Unknown crash
// recovery bound strictly to (principal_id, request_key, hold_id) with per-key
// concurrency serialization so retries never double-charge or falsely adopt.
type BillingRecoveryStateMachine struct {
	client  PlatformBillingClient
	mu      sync.Mutex
	entries map[recordKey]*recordEntry
}

// NewBillingRecoveryStateMachine constructs a state machine backed by platform-billing.
func NewBillingRecoveryStateMachine(client PlatformBillingClient) (*BillingRecoveryStateMachine, error) {
	if client == nil {
		return nil, ErrNilBillingClient
	}
	return &BillingRecoveryStateMachine{
		client:  client,
		entries: make(map[recordKey]*recordEntry),
	}, nil
}

// RestoreRecord loads a persisted protocol coordination record (e.g. after a process
// crash while a hold was in BillingHeld or BillingUnknown) so Reconcile or completion
// can resume safely using the original (principal_id, request_key, hold_id).
func (m *BillingRecoveryStateMachine) RestoreRecord(rec RecoveryRecord) error {
	scope := BillingScope{
		AppID:       rec.AppID,
		PrincipalID: rec.PrincipalID,
		RequestKey:  rec.RequestKey,
	}
	if err := scope.Validate(); err != nil {
		return err
	}
	// Safety invariant: never restore a failed/non-passed gate as adopted.
	if rec.CandidateState == CandidateAdopted {
		if rec.State != BillingSpent || rec.LastGateDecision == nil || !rec.LastGateDecision.CanAdopt() {
			return ErrInvalidStateTransition
		}
	}
	if rec.State == BillingUnknown {
		rec.ReconcileRequired = true
	}
	key := recordKey{
		principalID: strings.TrimSpace(rec.PrincipalID),
		requestKey:  strings.TrimSpace(rec.RequestKey),
	}
	entry := m.getOrCreateEntry(key, scope)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	entry.rec = rec.clone()
	return nil
}

// Get returns a copy of the current protocol record for (principalID, requestKey).
func (m *BillingRecoveryStateMachine) Get(principalID, requestKey string) (RecoveryRecord, bool) {
	key := recordKey{
		principalID: strings.TrimSpace(principalID),
		requestKey:  strings.TrimSpace(requestKey),
	}
	m.mu.Lock()
	entry, ok := m.entries[key]
	m.mu.Unlock()
	if !ok {
		return RecoveryRecord{}, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.rec.clone(), true
}

// BeginHold places an idempotent platform-billing hold bound to (principal_id, request_key).
// Retries while already BillingHeld return the existing HoldID without re-calling Hold.
// Retries while BillingUnknown fail closed with ErrReconcileRequired.
// Retries after terminal Spend/Refund fail closed with ErrAlreadySettled.
func (m *BillingRecoveryStateMachine) BeginHold(
	ctx context.Context,
	scope BillingScope,
	capability string,
	pricingVersion string,
	amountMinor int64,
) (RecoveryRecord, error) {
	if err := scope.Validate(); err != nil {
		return RecoveryRecord{}, err
	}
	if strings.TrimSpace(capability) == "" || strings.TrimSpace(pricingVersion) == "" || amountMinor <= 0 {
		return RecoveryRecord{}, ErrInvalidHoldAmount
	}

	entry := m.getOrCreateEntry(recordKey{
		principalID: strings.TrimSpace(scope.PrincipalID),
		requestKey:  strings.TrimSpace(scope.RequestKey),
	}, scope)

	entry.mu.Lock()
	defer entry.mu.Unlock()

	switch entry.rec.State {
	case BillingHeld:
		return entry.rec.clone(), nil
	case BillingUnknown:
		return entry.rec.clone(), ErrReconcileRequired
	case BillingSpent, BillingRefunded:
		return entry.rec.clone(), ErrAlreadySettled
	case BillingInit:
		// proceed to Hold
	default:
		return entry.rec.clone(), ErrInvalidStateTransition
	}

	resp, err := m.client.Hold(ctx, HoldRequest{
		Scope:          scope,
		IdempotencyKey: HoldIdempotencyKey(scope.RequestKey),
		Capability:     strings.TrimSpace(capability),
		PricingVersion: strings.TrimSpace(pricingVersion),
		AmountMinor:    amountMinor,
	})
	if err != nil {
		entry.rec.LastError = err.Error()
		if IsAmbiguousTransportError(err) {
			entry.rec.State = BillingUnknown
			entry.rec.PendingAction = ActionHold
			entry.rec.ReconcileRequired = true
			entry.rec.CandidateState = CandidateNone
			return entry.rec.clone(), fmt.Errorf("%w: %v", ErrBillingUnknown, err)
		}
		return entry.rec.clone(), err
	}
	if strings.TrimSpace(resp.HoldID) == "" {
		entry.rec.State = BillingUnknown
		entry.rec.PendingAction = ActionHold
		entry.rec.ReconcileRequired = true
		entry.rec.CandidateState = CandidateNone
		entry.rec.LastError = "platform-billing hold returned empty hold_id"
		return entry.rec.clone(), ErrBillingUnknown
	}

	entry.rec.HoldID = strings.TrimSpace(resp.HoldID)
	entry.rec.State = BillingHeld
	entry.rec.CandidateState = CandidatePendingGate
	entry.rec.PendingAction = ActionNone
	entry.rec.ReconcileRequired = false
	entry.rec.LastError = ""
	return entry.rec.clone(), nil
}

// HandleProviderFailureBeforeDelivery issues an idempotent full Refund when the
// upstream image provider fails before delivering a candidate image.
func (m *BillingRecoveryStateMachine) HandleProviderFailureBeforeDelivery(
	ctx context.Context,
	scope BillingScope,
	failureReason string,
) (RecoveryRecord, error) {
	if err := scope.Validate(); err != nil {
		return RecoveryRecord{}, err
	}
	entry, ok := m.getEntry(scope)
	if !ok {
		return RecoveryRecord{}, ErrRecordNotFound
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	switch entry.rec.State {
	case BillingRefunded:
		return entry.rec.clone(), nil
	case BillingUnknown:
		return entry.rec.clone(), ErrReconcileRequired
	case BillingSpent:
		return entry.rec.clone(), ErrAlreadySettled
	case BillingHeld:
		if strings.TrimSpace(entry.rec.HoldID) == "" {
			return entry.rec.clone(), ErrInvalidStateTransition
		}
	default:
		return entry.rec.clone(), ErrInvalidStateTransition
	}

	reason := strings.TrimSpace(failureReason)
	if reason == "" {
		reason = "provider_failure_before_delivery"
	}

	// Never adopt a candidate on provider failure.
	entry.rec.CandidateState = CandidateNone

	resp, err := m.client.Refund(ctx, RefundRequest{
		Scope:          scope,
		HoldID:         entry.rec.HoldID,
		IdempotencyKey: RefundIdempotencyKey(scope.RequestKey, entry.rec.HoldID),
		Reason:         reason,
	})
	if err != nil {
		entry.rec.LastError = err.Error()
		if IsAmbiguousTransportError(err) {
			entry.rec.State = BillingUnknown
			entry.rec.PendingAction = ActionRefund
			entry.rec.ReconcileRequired = true
			return entry.rec.clone(), fmt.Errorf("%w: %v", ErrBillingUnknown, err)
		}
		return entry.rec.clone(), err
	}
	if strings.TrimSpace(resp.RefundID) == "" {
		entry.rec.State = BillingUnknown
		entry.rec.PendingAction = ActionRefund
		entry.rec.ReconcileRequired = true
		entry.rec.LastError = "platform-billing refund returned empty refund_id"
		return entry.rec.clone(), ErrBillingUnknown
	}

	entry.rec.State = BillingRefunded
	entry.rec.RefundID = strings.TrimSpace(resp.RefundID)
	entry.rec.PendingAction = ActionNone
	entry.rec.ReconcileRequired = false
	entry.rec.LastError = ""
	return entry.rec.clone(), nil
}

// SettleAfterQualityGate settles the hold after EvaluateI2XQualityGate finishes on a
// provider-generated image:
//   - If gate passed (decision.CanAdopt()): calls Spend once and marks CandidateAdopted
//     ONLY after platform-billing confirms Spend.
//   - If gate failed: NEVER marks CandidateAdopted (forces CandidateRejected), and either
//     settles real provider spend (if providerCharged && policy == GateFailurePolicySettleProviderSpend)
//     or refunds strictly per explicit policy. Retries are idempotent and NEVER double-charge.
func (m *BillingRecoveryStateMachine) SettleAfterQualityGate(
	ctx context.Context,
	scope BillingScope,
	decision GateDecision,
	providerCharged bool,
	policy GateFailureBillingPolicy,
) (RecoveryRecord, error) {
	if err := scope.Validate(); err != nil {
		return RecoveryRecord{}, err
	}
	entry, ok := m.getEntry(scope)
	if !ok {
		return RecoveryRecord{}, ErrRecordNotFound
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.rec.State == BillingUnknown {
		return entry.rec.clone(), ErrReconcileRequired
	}

	decisionCopy := decision
	if decision.FailureReasons != nil {
		decisionCopy.FailureReasons = append([]string(nil), decision.FailureReasons...)
	}

	// Path A: Quality gate PASSED.
	if decision.CanAdopt() {
		if entry.rec.CandidateState == CandidateRejected {
			return entry.rec.clone(), ErrCandidateAlreadyRejected
		}
		if entry.rec.State == BillingSpent {
			if entry.rec.CandidateState == CandidateAdopted {
				return entry.rec.clone(), nil
			}
			return entry.rec.clone(), ErrAlreadySettled
		}
		if entry.rec.State == BillingRefunded {
			return entry.rec.clone(), ErrAlreadySettled
		}
		if entry.rec.State != BillingHeld || strings.TrimSpace(entry.rec.HoldID) == "" {
			return entry.rec.clone(), ErrInvalidStateTransition
		}

		entry.rec.LastGateDecision = &decisionCopy
		resp, err := m.client.Spend(ctx, SpendRequest{
			Scope:          scope,
			HoldID:         entry.rec.HoldID,
			IdempotencyKey: SpendIdempotencyKey(scope.RequestKey, entry.rec.HoldID),
			Reason:         "i2x_quality_gate_passed",
		})
		if err != nil {
			entry.rec.LastError = err.Error()
			if IsAmbiguousTransportError(err) {
				entry.rec.State = BillingUnknown
				entry.rec.PendingAction = ActionSpend
				entry.rec.ReconcileRequired = true
				entry.rec.CandidateState = CandidatePendingGate
				return entry.rec.clone(), fmt.Errorf("%w: %v", ErrBillingUnknown, err)
			}
			return entry.rec.clone(), err
		}
		if strings.TrimSpace(resp.SpendID) == "" {
			entry.rec.State = BillingUnknown
			entry.rec.PendingAction = ActionSpend
			entry.rec.ReconcileRequired = true
			entry.rec.CandidateState = CandidatePendingGate
			entry.rec.LastError = "platform-billing spend returned empty spend_id"
			return entry.rec.clone(), ErrBillingUnknown
		}

		entry.rec.State = BillingSpent
		entry.rec.SpendID = strings.TrimSpace(resp.SpendID)
		entry.rec.CandidateState = CandidateAdopted
		entry.rec.PendingAction = ActionNone
		entry.rec.ReconcileRequired = false
		entry.rec.LastError = ""
		return entry.rec.clone(), nil
	}

	// Path B: Quality gate FAILED after provider generated an image.
	if policy != GateFailurePolicySettleProviderSpend && policy != GateFailurePolicyRefundPlatformAbsorbed {
		return entry.rec.clone(), ErrInvalidBillingPolicy
	}
	if entry.rec.CandidateState == CandidateAdopted {
		return entry.rec.clone(), ErrAlreadySettled
	}

	entry.rec.LastGateDecision = &decisionCopy
	entry.rec.CandidateState = CandidateRejected

	// Idempotent terminal returns: never double-charge or double-refund on retry.
	if entry.rec.State == BillingSpent || entry.rec.State == BillingRefunded {
		return entry.rec.clone(), nil
	}
	if entry.rec.State != BillingHeld || strings.TrimSpace(entry.rec.HoldID) == "" {
		return entry.rec.clone(), ErrInvalidStateTransition
	}

	shouldSpend := policy == GateFailurePolicySettleProviderSpend && providerCharged
	if shouldSpend {
		resp, err := m.client.Spend(ctx, SpendRequest{
			Scope:          scope,
			HoldID:         entry.rec.HoldID,
			IdempotencyKey: SpendIdempotencyKey(scope.RequestKey, entry.rec.HoldID),
			Reason:         "i2x_quality_gate_failed_provider_charged",
		})
		if err != nil {
			entry.rec.LastError = err.Error()
			if IsAmbiguousTransportError(err) {
				entry.rec.State = BillingUnknown
				entry.rec.PendingAction = ActionSpend
				entry.rec.ReconcileRequired = true
				return entry.rec.clone(), fmt.Errorf("%w: %v", ErrBillingUnknown, err)
			}
			return entry.rec.clone(), err
		}
		if strings.TrimSpace(resp.SpendID) == "" {
			entry.rec.State = BillingUnknown
			entry.rec.PendingAction = ActionSpend
			entry.rec.ReconcileRequired = true
			entry.rec.LastError = "platform-billing spend returned empty spend_id"
			return entry.rec.clone(), ErrBillingUnknown
		}
		entry.rec.State = BillingSpent
		entry.rec.SpendID = strings.TrimSpace(resp.SpendID)
		entry.rec.CandidateState = CandidateRejected
		entry.rec.PendingAction = ActionNone
		entry.rec.ReconcileRequired = false
		entry.rec.LastError = ""
		return entry.rec.clone(), nil
	}

	refundReason := "i2x_quality_gate_failed_platform_absorbed"
	if !providerCharged {
		refundReason = "i2x_quality_gate_failed_provider_uncharged"
	}
	resp, err := m.client.Refund(ctx, RefundRequest{
		Scope:          scope,
		HoldID:         entry.rec.HoldID,
		IdempotencyKey: RefundIdempotencyKey(scope.RequestKey, entry.rec.HoldID),
		Reason:         refundReason,
	})
	if err != nil {
		entry.rec.LastError = err.Error()
		if IsAmbiguousTransportError(err) {
			entry.rec.State = BillingUnknown
			entry.rec.PendingAction = ActionRefund
			entry.rec.ReconcileRequired = true
			return entry.rec.clone(), fmt.Errorf("%w: %v", ErrBillingUnknown, err)
		}
		return entry.rec.clone(), err
	}
	if strings.TrimSpace(resp.RefundID) == "" {
		entry.rec.State = BillingUnknown
		entry.rec.PendingAction = ActionRefund
		entry.rec.ReconcileRequired = true
		entry.rec.LastError = "platform-billing refund returned empty refund_id"
		return entry.rec.clone(), ErrBillingUnknown
	}
	entry.rec.State = BillingRefunded
	entry.rec.RefundID = strings.TrimSpace(resp.RefundID)
	entry.rec.CandidateState = CandidateRejected
	entry.rec.PendingAction = ActionNone
	entry.rec.ReconcileRequired = false
	entry.rec.LastError = ""
	return entry.rec.clone(), nil
}

// MarkTransportUnknown records an external timeout or lost response against an
// active hold so callers cannot retry or release before reconciling by original
// (principal_id, request_key, hold_id).
func (m *BillingRecoveryStateMachine) MarkTransportUnknown(
	scope BillingScope,
	pending PendingBillingAction,
	reason string,
) (RecoveryRecord, error) {
	if err := scope.Validate(); err != nil {
		return RecoveryRecord{}, err
	}
	entry, ok := m.getEntry(scope)
	if !ok {
		return RecoveryRecord{}, ErrRecordNotFound
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.rec.State == BillingSpent || entry.rec.State == BillingRefunded {
		return entry.rec.clone(), ErrAlreadySettled
	}
	entry.rec.State = BillingUnknown
	entry.rec.PendingAction = pending
	entry.rec.ReconcileRequired = true
	if entry.rec.CandidateState == CandidateAdopted {
		entry.rec.CandidateState = CandidatePendingGate
	}
	entry.rec.LastError = strings.TrimSpace(reason)
	return entry.rec.clone(), nil
}

// Reconcile resolves a BillingUnknown hold by querying platform-billing with the
// original (principal_id, request_key, hold_id). Only after authoritative reconciliation
// may a caller resume spend/refund or accept final adoption/rejection state.
func (m *BillingRecoveryStateMachine) Reconcile(ctx context.Context, scope BillingScope) (RecoveryRecord, error) {
	if err := scope.Validate(); err != nil {
		return RecoveryRecord{}, err
	}
	entry, ok := m.getEntry(scope)
	if !ok {
		return RecoveryRecord{}, ErrRecordNotFound
	}

	entry.mu.Lock()
	defer entry.mu.Unlock()

	resp, err := m.client.LookupHold(ctx, LookupHoldRequest{
		Scope:  scope,
		HoldID: entry.rec.HoldID,
	})
	if err != nil {
		entry.rec.State = BillingUnknown
		entry.rec.ReconcileRequired = true
		entry.rec.LastError = err.Error()
		return entry.rec.clone(), fmt.Errorf("%w: reconcile lookup failed: %v", ErrBillingUnknown, err)
	}

	if strings.TrimSpace(resp.HoldID) != "" {
		if entry.rec.HoldID != "" && entry.rec.HoldID != strings.TrimSpace(resp.HoldID) {
			entry.rec.State = BillingUnknown
			entry.rec.ReconcileRequired = true
			entry.rec.LastError = "platform-billing reconcile returned mismatched hold_id"
			return entry.rec.clone(), ErrBillingUnknown
		}
		entry.rec.HoldID = strings.TrimSpace(resp.HoldID)
	}

	switch resp.State {
	case BillingSpent:
		if strings.TrimSpace(resp.SpendID) == "" {
			entry.rec.State = BillingUnknown
			entry.rec.ReconcileRequired = true
			entry.rec.LastError = "platform-billing reconcile spent missing spend_id"
			return entry.rec.clone(), ErrBillingUnknown
		}
		entry.rec.State = BillingSpent
		entry.rec.SpendID = strings.TrimSpace(resp.SpendID)
		entry.rec.ReconcileRequired = false
		entry.rec.PendingAction = ActionNone
		entry.rec.LastError = ""
		if entry.rec.LastGateDecision != nil && entry.rec.LastGateDecision.CanAdopt() && entry.rec.CandidateState != CandidateRejected {
			entry.rec.CandidateState = CandidateAdopted
		} else if entry.rec.LastGateDecision != nil {
			entry.rec.CandidateState = CandidateRejected
		} else {
			entry.rec.CandidateState = CandidateNone
		}
		return entry.rec.clone(), nil

	case BillingRefunded:
		if strings.TrimSpace(resp.RefundID) == "" {
			entry.rec.State = BillingUnknown
			entry.rec.ReconcileRequired = true
			entry.rec.LastError = "platform-billing reconcile refunded missing refund_id"
			return entry.rec.clone(), ErrBillingUnknown
		}
		entry.rec.State = BillingRefunded
		entry.rec.RefundID = strings.TrimSpace(resp.RefundID)
		entry.rec.ReconcileRequired = false
		entry.rec.PendingAction = ActionNone
		entry.rec.LastError = ""
		if entry.rec.LastGateDecision != nil && !entry.rec.LastGateDecision.CanAdopt() {
			entry.rec.CandidateState = CandidateRejected
		} else {
			entry.rec.CandidateState = CandidateNone
		}
		return entry.rec.clone(), nil

	case BillingHeld:
		if strings.TrimSpace(entry.rec.HoldID) == "" {
			entry.rec.State = BillingUnknown
			entry.rec.ReconcileRequired = true
			entry.rec.LastError = "platform-billing reconcile held missing hold_id"
			return entry.rec.clone(), ErrBillingUnknown
		}
		entry.rec.State = BillingHeld
		entry.rec.ReconcileRequired = false
		entry.rec.LastError = ""
		if entry.rec.CandidateState == CandidateNone {
			entry.rec.CandidateState = CandidatePendingGate
		}
		return entry.rec.clone(), nil

	case BillingInit:
		// Platform confirmed the initial Hold never landed.
		if entry.rec.HoldID != "" {
			entry.rec.State = BillingUnknown
			entry.rec.ReconcileRequired = true
			entry.rec.LastError = "platform-billing returned init for known hold_id"
			return entry.rec.clone(), ErrBillingUnknown
		}
		entry.rec.State = BillingInit
		entry.rec.ReconcileRequired = false
		entry.rec.PendingAction = ActionNone
		entry.rec.CandidateState = CandidateNone
		entry.rec.LastError = ""
		return entry.rec.clone(), nil

	default:
		entry.rec.State = BillingUnknown
		entry.rec.ReconcileRequired = true
		entry.rec.LastError = fmt.Sprintf("platform-billing reconcile returned state %q", resp.State)
		return entry.rec.clone(), ErrBillingUnknown
	}
}

func (m *BillingRecoveryStateMachine) getEntry(scope BillingScope) (*recordEntry, bool) {
	key := recordKey{
		principalID: strings.TrimSpace(scope.PrincipalID),
		requestKey:  strings.TrimSpace(scope.RequestKey),
	}
	m.mu.Lock()
	entry, ok := m.entries[key]
	m.mu.Unlock()
	return entry, ok
}

func (m *BillingRecoveryStateMachine) getOrCreateEntry(key recordKey, scope BillingScope) *recordEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.entries[key]; ok {
		return existing
	}
	entry := &recordEntry{
		rec: RecoveryRecord{
			AppID:          strings.TrimSpace(scope.AppID),
			PrincipalID:    key.principalID,
			RequestKey:     key.requestKey,
			State:          BillingInit,
			CandidateState: CandidateNone,
			PendingAction:  ActionNone,
		},
	}
	m.entries[key] = entry
	return entry
}
