package lightingscene

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

func testSHA256(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

type stubBillingClient struct {
	mu          sync.Mutex
	holdCalls   int
	spendCalls  int
	refundCalls int
	lookupCalls int
	holds       map[string]fidelitygate.LookupHoldResponse
	holdErr     error
	spendErr    error
	refundErr   error
}

func newStubBillingClient() *stubBillingClient {
	return &stubBillingClient{
		holds: make(map[string]fidelitygate.LookupHoldResponse),
	}
}

func (c *stubBillingClient) Hold(_ context.Context, req fidelitygate.HoldRequest) (fidelitygate.HoldResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holdCalls++
	if c.holdErr != nil {
		return fidelitygate.HoldResponse{}, c.holdErr
	}
	key := req.Scope.PrincipalID + ":" + req.Scope.RequestKey
	if existing, ok := c.holds[key]; ok {
		return fidelitygate.HoldResponse{HoldID: existing.HoldID}, nil
	}
	holdID := fmt.Sprintf("hold_%d", c.holdCalls)
	c.holds[key] = fidelitygate.LookupHoldResponse{
		HoldID: holdID,
		State:  fidelitygate.BillingHeld,
	}
	return fidelitygate.HoldResponse{HoldID: holdID}, nil
}

func (c *stubBillingClient) Spend(_ context.Context, req fidelitygate.SpendRequest) (fidelitygate.SpendResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spendCalls++
	if c.spendErr != nil {
		return fidelitygate.SpendResponse{}, c.spendErr
	}
	key := req.Scope.PrincipalID + ":" + req.Scope.RequestKey
	rec := c.holds[key]
	if rec.SpendID == "" {
		rec.SpendID = fmt.Sprintf("spend_%d", c.spendCalls)
	}
	rec.HoldID = req.HoldID
	rec.State = fidelitygate.BillingSpent
	c.holds[key] = rec
	return fidelitygate.SpendResponse{SpendID: rec.SpendID}, nil
}

func (c *stubBillingClient) Refund(_ context.Context, req fidelitygate.RefundRequest) (fidelitygate.RefundResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refundCalls++
	if c.refundErr != nil {
		return fidelitygate.RefundResponse{}, c.refundErr
	}
	key := req.Scope.PrincipalID + ":" + req.Scope.RequestKey
	rec := c.holds[key]
	if rec.RefundID == "" {
		rec.RefundID = fmt.Sprintf("refund_%d", c.refundCalls)
	}
	rec.HoldID = req.HoldID
	rec.State = fidelitygate.BillingRefunded
	c.holds[key] = rec
	return fidelitygate.RefundResponse{RefundID: rec.RefundID}, nil
}

func (c *stubBillingClient) LookupHold(_ context.Context, req fidelitygate.LookupHoldRequest) (fidelitygate.LookupHoldResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lookupCalls++
	key := req.Scope.PrincipalID + ":" + req.Scope.RequestKey
	if rec, ok := c.holds[key]; ok {
		return rec, nil
	}
	return fidelitygate.LookupHoldResponse{State: fidelitygate.BillingInit}, nil
}

func (c *stubBillingClient) counts() (holds, spends, refunds, lookups int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.holdCalls, c.spendCalls, c.refundCalls, c.lookupCalls
}

type stubRelightProvider struct {
	relightCalls atomic.Int64
	lookupCalls  atomic.Int64
	relightFn    func(ctx context.Context, req ProviderRelightRequest) (ProviderRelightResponse, error)
	lookupFn     func(ctx context.Context, taskID, idempotencyKey string) (ProviderLookupResponse, error)
}

func (p *stubRelightProvider) Relight(ctx context.Context, req ProviderRelightRequest) (ProviderRelightResponse, error) {
	p.relightCalls.Add(1)
	if p.relightFn != nil {
		return p.relightFn(ctx, req)
	}
	return ProviderRelightResponse{}, errors.New("unexpected relight call")
}

func (p *stubRelightProvider) LookupTask(ctx context.Context, taskID, idempotencyKey string) (ProviderLookupResponse, error) {
	p.lookupCalls.Add(1)
	if p.lookupFn != nil {
		return p.lookupFn(ctx, taskID, idempotencyKey)
	}
	return ProviderLookupResponse{Status: TaskStatusUnknown}, nil
}

func validEligibleModel() ModelDescriptor {
	return ModelDescriptor{
		ModelID:            "flux-kontext-relight-v2",
		ProviderID:         "volc-image-edit",
		Capabilities:       []ModelCapability{CapabilityImageEditRelight},
		SupportsImageInput: true,
		SupportsMaskLock:   true,
		SupportsRelighting: true,
	}
}

func validAnchor(seed string) SubjectAnchor {
	return SubjectAnchor{
		SubjectAssetID: "asset_subject_" + seed,
		SubjectSHA256:  testSHA256("source_" + seed),
		LogoRegions: []NormalizedRegion{
			{Label: "brand_logo", X: 0.25, Y: 0.20, Width: 0.30, Height: 0.12},
		},
		TextRegions: []NormalizedRegion{
			{Label: "net_weight", ExpectedText: "500ml", X: 0.25, Y: 0.65, Width: 0.35, Height: 0.10},
		},
		StructureHash: testSHA256("structure_" + seed),
	}
}

func validLightingSpec() LightingSpec {
	return LightingSpec{
		LightDirection:           "top_left_45",
		ContactShadowRequired:    true,
		EdgeMaterialPreservation: 0.92,
		ScenePreset:              "marble_counter_morning",
	}
}

func validRequest(seed string, sample LightingSampleClass) LightingSceneRequest {
	return LightingSceneRequest{
		Scope: fidelitygate.BillingScope{
			AppID:       fidelitygate.AppID,
			PrincipalID: "principal_merchant_1",
			RequestKey:  "req_" + seed,
		},
		IdempotencyKey:   "idem_" + seed,
		TaskID:           "task_" + seed,
		QuoteID:          "quote_" + seed,
		EstimatedCostFen: 350,
		PricingVersion:   "lighting-price/v1",
		Mode:             ModeFidelityLocked,
		Model:            validEligibleModel(),
		Credentials: CredentialQuotaStatus{
			CredentialConfigured: true,
			CredentialRef:        "vault:image-relight-prod",
			QuotaAuthorized:      true,
			AvailableQuotaFen:    10000,
			GrantSource:          "platform_wallet_grant",
		},
		Anchor:       validAnchor(seed),
		Lighting:     validLightingSpec(),
		SampleClass:  sample,
		SampleOrigin: fidelitygate.SampleOriginReal,
	}
}

func passingCandidate(anchor SubjectAnchor, seed string) LightingCandidateMetrics {
	return LightingCandidateMetrics{
		Report: fidelitygate.SubjectFidelityReport{
			SourceSHA256:         anchor.SubjectSHA256,
			CandidateSHA256:      testSHA256("candidate_" + seed),
			MaskIoU:              0.97,
			ForegroundPixelDrift: 0.03,
			LogoOCRSimilarity:    0.99,
			BackgroundReplaced:   true,
			AlphaIntegrityOK:     true,
			EvaluatorVersion:     fidelitygate.DefaultEvaluatorVersion,
			ThresholdVersion:     fidelitygate.DefaultThresholdVersion,
		},
		CandidateStructureHash:   anchor.StructureHash,
		StructureSimilarity:      0.98,
		TextRegionOCRSimilarity:  0.98,
		ContactShadowPresent:     true,
		ContactShadowScore:       0.91,
		EdgeMaterialScore:        0.95,
		BackgroundAestheticScore: 0.93,
		ViewSource:               fidelitygate.ViewSourceCaptured,
	}
}

func newTestLedger(t *testing.T, provider RelightProvider, policy fidelitygate.GateFailureBillingPolicy) (*LightingLedger, *stubBillingClient) {
	t.Helper()
	bc := newStubBillingClient()
	sm, err := fidelitygate.NewBillingRecoveryStateMachine(bc)
	if err != nil {
		t.Fatalf("NewBillingRecoveryStateMachine failed: %v", err)
	}
	ledger, err := NewLightingLedger(sm, provider, DefaultLightingFidelityConfig(), policy)
	if err != nil {
		t.Fatalf("NewLightingLedger failed: %v", err)
	}
	return ledger, bc
}

func TestMissingCredentialsFailClosedZeroCallsZeroCost(t *testing.T) {
	provider := &stubRelightProvider{}
	ledger, bc := newTestLedger(t, provider, fidelitygate.GateFailurePolicyRefundPlatformAbsorbed)

	req := validRequest("missing_cred", SampleLogoTextPackaging)
	req.Credentials.CredentialConfigured = false

	val, err := ValidateLightingSceneRequest(req)
	if !errors.Is(err, ErrBlockedMissingCredentials) || !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlockedMissingCredentials, got %v", err)
	}
	if val.Status != PreflightBlocked || val.BlockCode != "BLOCKED_MISSING_CREDENTIALS" {
		t.Fatalf("expected BLOCKED_MISSING_CREDENTIALS, got status=%s code=%s", val.Status, val.BlockCode)
	}

	receipt, err := ledger.Execute(context.Background(), req)
	if !errors.Is(err, ErrBlockedMissingCredentials) {
		t.Fatalf("expected Execute fail-closed ErrBlockedMissingCredentials, got %v", err)
	}
	if receipt.TaskStatus != TaskStatusBlocked {
		t.Fatalf("expected TaskStatusBlocked, got %s", receipt.TaskStatus)
	}
	if receipt.ActualCostFen != 0 {
		t.Fatalf("expected ActualCostFen=0, got %d", receipt.ActualCostFen)
	}
	if provider.relightCalls.Load() != 0 {
		t.Fatalf("expected 0 provider calls, got %d", provider.relightCalls.Load())
	}
	holds, spends, refunds, _ := bc.counts()
	if holds != 0 || spends != 0 || refunds != 0 {
		t.Fatalf("expected 0 billing calls, got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}

	// Also verify unauthorized/insufficient quota fails closed with 0 calls and 0 cost.
	quotaReq := validRequest("missing_quota", SampleLogoTextPackaging)
	quotaReq.Credentials.QuotaAuthorized = false
	quotaReceipt, qErr := ledger.Execute(context.Background(), quotaReq)
	if !errors.Is(qErr, ErrBlockedQuotaUnauthorized) {
		t.Fatalf("expected ErrBlockedQuotaUnauthorized, got %v", qErr)
	}
	if quotaReceipt.TaskStatus != TaskStatusBlocked || quotaReceipt.ActualCostFen != 0 {
		t.Fatalf("expected BLOCKED with 0 cost, got status=%s cost=%d", quotaReceipt.TaskStatus, quotaReceipt.ActualCostFen)
	}
	if provider.relightCalls.Load() != 0 {
		t.Fatalf("expected 0 provider calls after quota block, got %d", provider.relightCalls.Load())
	}
}

func TestModelEligibilityRejectsH3FastAndTextOnlyModels(t *testing.T) {
	provider := &stubRelightProvider{}
	ledger, bc := newTestLedger(t, provider, fidelitygate.GateFailurePolicyRefundPlatformAbsorbed)

	cases := []struct {
		name  string
		model ModelDescriptor
	}{
		{
			name: "h3-fast disguised with image flags",
			model: ModelDescriptor{
				ModelID:            "h3-fast",
				ProviderID:         "video-provider",
				Capabilities:       []ModelCapability{CapabilityImageEditRelight},
				SupportsImageInput: true,
				SupportsMaskLock:   true,
				SupportsRelighting: true,
			},
		},
		{
			name: "h3-fast t2v variant",
			model: ModelDescriptor{
				ModelID:      "vendor/h3-fast-v1",
				ProviderID:   "video-provider",
				Capabilities: []ModelCapability{CapabilityTextToVideo},
			},
		},
		{
			name: "text-only model",
			model: ModelDescriptor{
				ModelID:      "gpt-text-only-v1",
				ProviderID:   "llm-provider",
				Capabilities: []ModelCapability{CapabilityTextOnly},
			},
		},
		{
			name: "t2i-only model without image-edit relight",
			model: ModelDescriptor{
				ModelID:            "sdxl-t2i",
				ProviderID:         "image-provider",
				Capabilities:       []ModelCapability{CapabilityTextToImage},
				SupportsImageInput: false,
				SupportsRelighting: false,
			},
		},
		{
			name: "relight model lacking mask lock in fidelity_locked",
			model: ModelDescriptor{
				ModelID:            "creative-relight-nomask",
				ProviderID:         "image-provider",
				Capabilities:       []ModelCapability{CapabilityImageEditRelight},
				SupportsImageInput: true,
				SupportsMaskLock:   false,
				SupportsRelighting: true,
			},
		},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := validRequest(fmt.Sprintf("ineligible_%d", i), SampleLogoTextPackaging)
			req.Model = tc.model
			receipt, err := ledger.Execute(context.Background(), req)
			if !errors.Is(err, ErrModelNotImageEditEligible) {
				t.Fatalf("expected ErrModelNotImageEditEligible, got %v", err)
			}
			if receipt.ActualCostFen != 0 {
				t.Fatalf("expected 0 cost, got %d", receipt.ActualCostFen)
			}
		})
	}

	if provider.relightCalls.Load() != 0 {
		t.Fatalf("expected 0 provider calls across all ineligible models, got %d", provider.relightCalls.Load())
	}
	holds, spends, refunds, _ := bc.counts()
	if holds != 0 || spends != 0 || refunds != 0 {
		t.Fatalf("expected 0 billing calls, got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}

func TestFidelityFailureNeverSilentlySwitchesToCreativeMode(t *testing.T) {
	req := validRequest("fidelity_fail_no_switch", SampleLogoTextPackaging)
	brokenCandidate := passingCandidate(req.Anchor, "broken_logo")
	brokenCandidate.Report.LogoOCRSimilarity = 0.42
	brokenCandidate.TextRegionOCRSimilarity = 0.50
	brokenCandidate.BackgroundAestheticScore = 0.96

	provider := &stubRelightProvider{
		relightFn: func(_ context.Context, preq ProviderRelightRequest) (ProviderRelightResponse, error) {
			return ProviderRelightResponse{
				ProviderTaskID:  "ptask_broken_1",
				AssetID:         "asset_broken_1",
				VersionID:       "ver_broken_1",
				ProviderCharged: true,
				ActualCostFen:   350,
				Candidate:       brokenCandidate,
			}, nil
		},
	}
	ledger, bc := newTestLedger(t, provider, fidelitygate.GateFailurePolicyRefundPlatformAbsorbed)

	// 1. Fidelity-locked run fails on broken logo/text and MUST remain ModeFidelityLocked.
	receipt, err := ledger.Execute(context.Background(), req)
	if !errors.Is(err, ErrFidelityGateRejected) {
		t.Fatalf("expected ErrFidelityGateRejected, got %v", err)
	}
	if receipt.Mode != ModeFidelityLocked {
		t.Fatalf("fidelity failure MUST NOT switch mode; got %s", receipt.Mode)
	}
	if receipt.TaskStatus != TaskStatusFailed || receipt.CandidateState != fidelitygate.CandidateRejected {
		t.Fatalf("expected FAILED + CandidateRejected, got status=%s cand=%s", receipt.TaskStatus, receipt.CandidateState)
	}
	if receipt.Selected {
		t.Fatalf("rejected candidate must never be marked selected")
	}

	// Verify pure transition guard also rejects silent switch.
	nextMode, gErr := GuardModeTransition(ModeFidelityLocked, ModeCreativeExplicit, true, true, false)
	if !errors.Is(gErr, ErrSilentCreativeSwitchForbidden) || nextMode != ModeFidelityLocked {
		t.Fatalf("expected ErrSilentCreativeSwitchForbidden keeping fidelity_locked, got mode=%s err=%v", nextMode, gErr)
	}

	// 2. Attempt to silently switch to creative mode on same subject WITHOUT
	// ExplicitSwitchAfterFailure (even if caller omits PriorFidelityFailed=true).
	silentSwitchReq := validRequest("silent_creative_try", SampleLogoTextPackaging)
	silentSwitchReq.Anchor = req.Anchor // same subject that just failed fidelity
	silentSwitchReq.Mode = ModeCreativeExplicit
	silentSwitchReq.CreativeUserConfirmed = true
	silentSwitchReq.PriorFidelityFailed = false // caller omitted flag; ledger must still block!
	silentSwitchReq.ExplicitSwitchAfterFailure = false

	_, sErr := ledger.Execute(context.Background(), silentSwitchReq)
	if !errors.Is(sErr, ErrSilentCreativeSwitchForbidden) {
		t.Fatalf("expected server-side ErrSilentCreativeSwitchForbidden, got %v", sErr)
	}
	if provider.relightCalls.Load() != 1 {
		t.Fatalf("silent creative switch must not invoke provider; calls=%d", provider.relightCalls.Load())
	}

	// 3. Creative mode without explicit user confirmation is also rejected.
	unconfirmedCreative := validRequest("unconfirmed_creative", SampleLogoTextPackaging)
	unconfirmedCreative.Mode = ModeCreativeExplicit
	unconfirmedCreative.CreativeUserConfirmed = false
	if _, uErr := ledger.Execute(context.Background(), unconfirmedCreative); !errors.Is(uErr, ErrCreativeConfirmationRequired) {
		t.Fatalf("expected ErrCreativeConfirmationRequired, got %v", uErr)
	}

	// 4. Explicit user confirmation + explicit post-failure acknowledgment IS allowed,
	// and produces a receipt explicitly tagged ModeCreativeExplicit.
	explicitCreative := validRequest("explicit_creative_ok", SampleLogoTextPackaging)
	explicitCreative.Anchor = req.Anchor
	explicitCreative.Mode = ModeCreativeExplicit
	explicitCreative.CreativeUserConfirmed = true
	explicitCreative.PriorFidelityFailed = true
	explicitCreative.ExplicitSwitchAfterFailure = true
	provider.relightFn = func(_ context.Context, preq ProviderRelightRequest) (ProviderRelightResponse, error) {
		return ProviderRelightResponse{
			ProviderTaskID:  "ptask_creative_1",
			AssetID:         "asset_creative_1",
			VersionID:       "ver_creative_1",
			ProviderCharged: true,
			ActualCostFen:   350,
			Candidate:       passingCandidate(req.Anchor, "creative_ok"),
		}, nil
	}
	creativeRec, cErr := ledger.Execute(context.Background(), explicitCreative)
	if cErr != nil {
		t.Fatalf("expected explicit confirmed creative run to succeed, got %v", cErr)
	}
	if creativeRec.Mode != ModeCreativeExplicit {
		t.Fatalf("expected ModeCreativeExplicit, got %s", creativeRec.Mode)
	}
	holds, spends, refunds, _ := bc.counts()
	if holds != 2 || spends != 1 || refunds != 1 {
		t.Fatalf("unexpected billing counts holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}

func TestPrettyBackgroundWithBrokenProductRejectedAndThreeSampleStandardPasses(t *testing.T) {
	cfg := DefaultLightingFidelityConfig()

	// Part A: Pretty background (0.99) hiding broken logo/text MUST be rejected under ModeFidelityLocked.
	anchor := validAnchor("pretty_trap")
	brokenCand := passingCandidate(anchor, "pretty_trap_broken")
	brokenCand.Report.LogoOCRSimilarity = 0.61
	brokenCand.TextRegionOCRSimilarity = 0.58
	brokenCand.BackgroundAestheticScore = 0.99

	badRes := EvaluateLightingFidelity(LightingFidelityInput{
		Scope: fidelitygate.BillingScope{
			AppID:       fidelitygate.AppID,
			PrincipalID: "principal_1",
			RequestKey:  "req_pretty_trap",
		},
		Mode:         ModeFidelityLocked,
		SampleClass:  SampleLogoTextPackaging,
		SampleOrigin: fidelitygate.SampleOriginReal,
		Anchor:       anchor,
		Lighting:     validLightingSpec(),
		Candidate:    brokenCand,
	}, cfg)

	if badRes.Passed || badRes.Verdict != fidelitygate.VerdictFail {
		t.Fatalf("expected FAIL for pretty background hiding broken product, got passed=%v verdict=%s", badRes.Passed, badRes.Verdict)
	}
	if !badRes.PrettyBackgroundMaskingDefect {
		t.Fatalf("expected PrettyBackgroundMaskingDefect=true")
	}
	if !badRes.SilentCreativeSwitchBlocked || badRes.Mode != ModeFidelityLocked {
		t.Fatalf("expected SilentCreativeSwitchBlocked=true and ModeFidelityLocked, got blocked=%v mode=%s", badRes.SilentCreativeSwitchBlocked, badRes.Mode)
	}
	combined := badRes.CombinedGateDecision()
	if combined.CanAdopt() {
		t.Fatalf("combined gate decision must not allow adoption")
	}
	assertContainsReason(t, combined.FailureReasons, ReasonPrettyBackgroundHidingBrokenProduct)
	assertContainsReason(t, combined.FailureReasons, ReasonLogoRegionBroken)
	assertContainsReason(t, combined.FailureReasons, ReasonTextRegionBroken)

	// Missing contact shadow MUST also fail even when base subject metrics pass.
	noShadowCand := passingCandidate(anchor, "no_shadow")
	noShadowCand.ContactShadowPresent = false
	noShadowCand.ContactShadowScore = 0.20
	noShadowRes := EvaluateLightingFidelity(LightingFidelityInput{
		Scope:        fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal_1", RequestKey: "req_no_shadow"},
		Mode:         ModeFidelityLocked,
		SampleClass:  SampleComplexEdgeSilhouette,
		SampleOrigin: fidelitygate.SampleOriginReal,
		Anchor:       anchor,
		Lighting:     validLightingSpec(),
		Candidate:    noShadowCand,
	}, cfg)
	if noShadowRes.Passed || noShadowRes.CombinedGateDecision().CanAdopt() {
		t.Fatalf("expected missing contact shadow to be rejected")
	}
	assertContainsReason(t, noShadowRes.LightingFailureReasons, ReasonContactShadowMissingOrWeak)

	// Part B: Valid 3-sample relighting with contact shadow & preserved subject passes
	// and records unified traceable DerivationReceipts for all 3 samples.
	samples := ThreeSampleStandard()
	if len(samples) != 3 {
		t.Fatalf("expected 3 canonical lighting samples, got %d", len(samples))
	}

	provider := &stubRelightProvider{
		relightFn: func(_ context.Context, preq ProviderRelightRequest) (ProviderRelightResponse, error) {
			return ProviderRelightResponse{
				ProviderTaskID:  "ptask_" + preq.TaskID,
				AssetID:         "asset_relit_" + preq.TaskID,
				VersionID:       "v1_" + preq.TaskID,
				ProviderCharged: true,
				ActualCostFen:   350,
				Candidate:       passingCandidate(preq.Anchor, preq.TaskID),
			}, nil
		},
	}
	ledger, bc := newTestLedger(t, provider, fidelitygate.GateFailurePolicyRefundPlatformAbsorbed)

	batchInputs := make([]LightingFidelityInput, 0, len(samples))
	for _, sample := range samples {
		seed := "three_sample_" + string(sample)
		req := validRequest(seed, sample)
		receipt, err := ledger.Execute(context.Background(), req)
		if err != nil {
			t.Fatalf("sample %s Execute failed: %v", sample, err)
		}
		if err := receipt.ValidateTraceable(); err != nil {
			t.Fatalf("sample %s receipt not traceable: %v", sample, err)
		}
		// Verify unified JSON fields consumable by HUI-2232 and GoBoost.
		raw, err := json.Marshal(receipt)
		if err != nil {
			t.Fatalf("marshal receipt failed: %v", err)
		}
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("unmarshal receipt failed: %v", err)
		}
		for _, field := range []string{
			"task_id", "asset_id", "version_id", "model_id", "provider_id",
			"task_fingerprint", "quote_id", "actual_cost_fen", "grant_source",
			"mode", "selected",
		} {
			if _, ok := doc[field]; !ok {
				t.Fatalf("receipt JSON missing required field %q", field)
			}
		}

		batchInputs = append(batchInputs, LightingFidelityInput{
			Scope:        req.Scope,
			Mode:         req.Mode,
			SampleClass:  sample,
			SampleOrigin: req.SampleOrigin,
			Anchor:       req.Anchor,
			Lighting:     req.Lighting,
			Candidate:    passingCandidate(req.Anchor, req.TaskID),
		})
	}

	summary := EvaluateThreeSampleBatch(batchInputs, cfg)
	if !summary.AllPassed || len(summary.MissingSamples) != 0 || len(summary.FailedSamples) != 0 {
		t.Fatalf("expected all 3 lighting samples to pass, got %+v", summary)
	}
	holds, spends, refunds, _ := bc.counts()
	if holds != 3 || spends != 3 || refunds != 0 {
		t.Fatalf("expected 3 holds and 3 spends, got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}

func TestUnknownRefreshAndConcurrentRetryUnderRaceNeverDoubleCallDoubleChargeOrOverwriteSelected(t *testing.T) {
	// Step 1: Establish a user-selected v1 on subject_main.
	reqV1 := validRequest("subj_main_v1", SampleLogoTextPackaging)
	candV1 := passingCandidate(reqV1.Anchor, "v1")

	var lookupReady atomic.Bool
	var candV2 LightingCandidateMetrics

	provider := &stubRelightProvider{
		relightFn: func(_ context.Context, preq ProviderRelightRequest) (ProviderRelightResponse, error) {
			if preq.IdempotencyKey == reqV1.IdempotencyKey {
				return ProviderRelightResponse{
					ProviderTaskID:  "ptask_v1",
					AssetID:         "asset_v1_selected",
					VersionID:       "ver_v1_selected",
					ProviderCharged: true,
					ActualCostFen:   350,
					Candidate:       candV1,
				}, nil
			}
			// Simulate ambiguous transport timeout leaving task UNKNOWN.
			return ProviderRelightResponse{}, context.DeadlineExceeded
		},
		lookupFn: func(_ context.Context, taskID, idempotencyKey string) (ProviderLookupResponse, error) {
			if !lookupReady.Load() {
				return ProviderLookupResponse{Status: TaskStatusUnknown}, nil
			}
			return ProviderLookupResponse{
				Status:          TaskStatusSucceeded,
				AssetID:         "asset_v2_recovered",
				VersionID:       "ver_v2_recovered",
				ProviderCharged: true,
				ActualCostFen:   350,
				Candidate:       &candV2,
			}, nil
		},
	}

	ledger, bc := newTestLedger(t, provider, fidelitygate.GateFailurePolicyRefundPlatformAbsorbed)

	recV1, err := ledger.Execute(context.Background(), reqV1)
	if err != nil {
		t.Fatalf("v1 Execute failed: %v", err)
	}
	selectedV1, err := ledger.SelectVersion(reqV1.Scope.PrincipalID, reqV1.IdempotencyKey)
	if err != nil {
		t.Fatalf("SelectVersion v1 failed: %v", err)
	}
	if !selectedV1.Selected || selectedV1.VersionID != "ver_v1_selected" || selectedV1.AssetID != "asset_v1_selected" {
		t.Fatalf("unexpected selected v1: %+v", selectedV1)
	}

	// Step 2: Launch a second derivation (v2, new quote) on the SAME subject that
	// times out into UNKNOWN, while 24 goroutines concurrently retry Execute (both
	// with same idempotency_key AND fresh retry key with identical task_fingerprint)
	// and call Refresh under -race.
	reqV2 := validRequest("subj_main_v2", SampleReflectiveMaterial)
	reqV2.Anchor = reqV1.Anchor // same subject as selected v1!
	candV2 = passingCandidate(reqV2.Anchor, "v2")
	fpV2 := TaskFingerprint(reqV2)

	const workers = 24
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			r := reqV2
			if idx%2 == 1 {
				// Simulate browser refresh retry with fresh client idempotency token
				// but identical quote + subject + lighting => identical task_fingerprint.
				r.IdempotencyKey = fmt.Sprintf("idem_refresh_retry_%d", idx)
				r.Scope.RequestKey = fmt.Sprintf("req_refresh_retry_%d", idx)
			}
			_, _ = ledger.Execute(context.Background(), r)
			_, _ = ledger.RefreshByFingerprint(context.Background(), reqV2.Scope.PrincipalID, fpV2)
			// Also hammer v1 with concurrent retries & refreshes to verify selected v1 is never overwritten.
			_, _ = ledger.Execute(context.Background(), reqV1)
			_, _ = ledger.Refresh(context.Background(), reqV1.Scope.PrincipalID, reqV1.IdempotencyKey)
		}(i)
	}
	close(start)
	wg.Wait()

	// Exactly 2 Relight calls total: 1 for v1 + 1 for v2 (ZERO double calls!).
	if got := provider.relightCalls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 provider Relight calls (1 for v1, 1 for v2), got %d", got)
	}
	// Exactly 2 Holds total: 1 for v1 + 1 for v2; Spend still 1 (only v1).
	holds, spends, refunds, _ := bc.counts()
	if holds != 2 || spends != 1 || refunds != 0 {
		t.Fatalf("expected holds=2 spends=1 refunds=0 during UNKNOWN, got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}

	// Verify v2 is UNKNOWN and CANNOT be selected while UNKNOWN.
	v2Snap, ok := ledger.GetReceiptByFingerprint(reqV2.Scope.PrincipalID, fpV2)
	if !ok || v2Snap.TaskStatus != TaskStatusUnknown {
		t.Fatalf("expected v2 TaskStatusUnknown, got ok=%v status=%s", ok, v2Snap.TaskStatus)
	}
	if _, selErr := ledger.SelectVersion(reqV2.Scope.PrincipalID, v2Snap.IdempotencyKey); !errors.Is(selErr, ErrVersionNotSelectable) {
		t.Fatalf("expected ErrVersionNotSelectable for UNKNOWN task, got %v", selErr)
	}

	// Verify user-selected v1 was NEVER overwritten during v2 UNKNOWN storm!
	selAfterUnknown, ok := ledger.GetSelectedReceipt(reqV1.Scope.PrincipalID, reqV1.Anchor.SubjectAssetID)
	if !ok || !selAfterUnknown.Selected || selAfterUnknown.VersionID != "ver_v1_selected" || selAfterUnknown.AssetID != "asset_v1_selected" {
		t.Fatalf("selected v1 must not be overwritten during UNKNOWN storm; got %+v", selAfterUnknown)
	}

	// Step 3: Provider lookup now reports v2 completed. Run concurrent RefreshByFingerprint
	// under -race -> reconciles billing & settles Spend ONCE, zero extra Relight calls,
	// and STILL does not overwrite user-selected v1!
	lookupReady.Store(true)
	var refreshWG sync.WaitGroup
	refreshStart := make(chan struct{})
	for i := 0; i < 16; i++ {
		refreshWG.Add(1)
		go func() {
			defer refreshWG.Done()
			<-refreshStart
			_, _ = ledger.RefreshByFingerprint(context.Background(), reqV2.Scope.PrincipalID, fpV2)
		}()
	}
	close(refreshStart)
	refreshWG.Wait()

	// Relight MUST STILL be 2 (Refresh NEVER re-invokes provider.Relight).
	if got := provider.relightCalls.Load(); got != 2 {
		t.Fatalf("Refresh must never call provider.Relight; got %d", got)
	}
	// Holds=2, Spends=2 (exactly 1 spend for v1 + 1 spend for v2; ZERO double-charge!).
	holds, spends, refunds, _ = bc.counts()
	if holds != 2 || spends != 2 || refunds != 0 {
		t.Fatalf("expected holds=2 spends=2 refunds=0 after UNKNOWN reconcile, got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}

	v2Recovered, ok := ledger.GetReceiptByFingerprint(reqV2.Scope.PrincipalID, fpV2)
	if !ok || v2Recovered.TaskStatus != TaskStatusSucceeded || v2Recovered.VersionID != "ver_v2_recovered" {
		t.Fatalf("expected v2 recovered SUCCEEDED, got ok=%v rec=%+v", ok, v2Recovered)
	}
	if err := v2Recovered.ValidateTraceable(); err != nil {
		t.Fatalf("recovered v2 receipt must be traceable: %v", err)
	}
	if v2Recovered.Selected {
		t.Fatalf("recovered v2 must not auto-overwrite user-selected v1")
	}

	// User-selected version for subject MUST still be v1!
	selAfterRecovery, ok := ledger.GetSelectedReceipt(reqV1.Scope.PrincipalID, reqV1.Anchor.SubjectAssetID)
	if !ok || !selAfterRecovery.Selected || selAfterRecovery.VersionID != recV1.VersionID || selAfterRecovery.AssetID != recV1.AssetID {
		t.Fatalf("selected v1 must stay intact after v2 recovery; got %+v", selAfterRecovery)
	}

	// Also verify idempotency key conflict with different fingerprint fails closed with 0 calls.
	conflictReq := validRequest("conflict_fp", SampleComplexEdgeSilhouette)
	conflictReq.IdempotencyKey = reqV1.IdempotencyKey
	conflictReq.Scope.PrincipalID = reqV1.Scope.PrincipalID
	if _, cErr := ledger.Execute(context.Background(), conflictReq); !errors.Is(cErr, ErrIdempotencyKeyConflict) {
		t.Fatalf("expected ErrIdempotencyKeyConflict, got %v", cErr)
	}
	if provider.relightCalls.Load() != 2 {
		t.Fatalf("idempotency conflict must not invoke provider; got %d", provider.relightCalls.Load())
	}
}

func TestFidelityEdgeCasesAndFailClosedGuards(t *testing.T) {
	cfg := DefaultLightingFidelityConfig()
	anchor := validAnchor("edge_cases")
	scope := fidelitygate.BillingScope{
		AppID:       fidelitygate.AppID,
		PrincipalID: "principal_edge",
		RequestKey:  "req_edge",
	}

	// 1. Edge/material below LightingSpec.EdgeMaterialPreservation (0.96 > default 0.90).
	spec := validLightingSpec()
	spec.EdgeMaterialPreservation = 0.96
	cand := passingCandidate(anchor, "edge_degraded")
	cand.EdgeMaterialScore = 0.92
	res := EvaluateLightingFidelity(LightingFidelityInput{
		Scope:        scope,
		Mode:         ModeFidelityLocked,
		SampleClass:  SampleReflectiveMaterial,
		SampleOrigin: fidelitygate.SampleOriginReal,
		Anchor:       anchor,
		Lighting:     spec,
		Candidate:    cand,
	}, cfg)
	if res.Passed || res.CombinedGateDecision().CanAdopt() {
		t.Fatalf("expected degraded edge/material to fail")
	}
	assertContainsReason(t, res.LightingFailureReasons, ReasonEdgeMaterialDegraded)

	// 2. Subject anchor SHA-256 mismatch fails closed.
	mismatchCand := passingCandidate(anchor, "anchor_mismatch")
	mismatchCand.Report.SourceSHA256 = testSHA256("different_source_image")
	mismatchRes := EvaluateLightingFidelity(LightingFidelityInput{
		Scope:        scope,
		Mode:         ModeFidelityLocked,
		SampleClass:  SampleLogoTextPackaging,
		SampleOrigin: fidelitygate.SampleOriginReal,
		Anchor:       anchor,
		Lighting:     validLightingSpec(),
		Candidate:    mismatchCand,
	}, cfg)
	if mismatchRes.Passed {
		t.Fatalf("expected subject anchor SHA256 mismatch to fail")
	}
	assertContainsReason(t, mismatchRes.LightingFailureReasons, ReasonSubjectAnchorMismatch)

	// 3. Zero-value LightingFidelityConfig fails closed.
	zeroRes := EvaluateLightingFidelity(LightingFidelityInput{
		Scope:        scope,
		Mode:         ModeFidelityLocked,
		SampleClass:  SampleLogoTextPackaging,
		SampleOrigin: fidelitygate.SampleOriginReal,
		Anchor:       anchor,
		Lighting:     validLightingSpec(),
		Candidate:    passingCandidate(anchor, "zero_cfg"),
	}, LightingFidelityConfig{})
	if zeroRes.Passed || zeroRes.Verdict != fidelitygate.VerdictInvalidInput {
		t.Fatalf("expected zero-value config to fail closed as invalid_input, got passed=%v verdict=%s", zeroRes.Passed, zeroRes.Verdict)
	}
	assertContainsReason(t, zeroRes.LightingFailureReasons, ReasonInvalidLightingConfig)

	// 4. Synthetic/fixture SampleOrigin rejected fail-closed.
	fakeReq := validRequest("fake_sample", SampleLogoTextPackaging)
	fakeReq.SampleOrigin = "synthetic"
	if _, err := ValidateLightingSceneRequest(fakeReq); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected synthetic sample_origin to fail validation, got %v", err)
	}

	// 5. Refresh resolving UNKNOWN -> provider FAILED refunds hold with 0 cost and 0 extra Relight calls.
	reqFail := validRequest("unknown_to_failed", SampleComplexEdgeSilhouette)
	provider := &stubRelightProvider{
		relightFn: func(_ context.Context, _ ProviderRelightRequest) (ProviderRelightResponse, error) {
			return ProviderRelightResponse{}, context.DeadlineExceeded
		},
		lookupFn: func(_ context.Context, _, _ string) (ProviderLookupResponse, error) {
			return ProviderLookupResponse{Status: TaskStatusFailed}, nil
		},
	}
	ledger, bc := newTestLedger(t, provider, fidelitygate.GateFailurePolicyRefundPlatformAbsorbed)
	unkRec, err := ledger.Execute(context.Background(), reqFail)
	if !errors.Is(err, ErrTaskOutcomeUnknown) || unkRec.TaskStatus != TaskStatusUnknown {
		t.Fatalf("expected UNKNOWN after timeout, got rec=%+v err=%v", unkRec, err)
	}
	refRec, rErr := ledger.Refresh(context.Background(), reqFail.Scope.PrincipalID, reqFail.IdempotencyKey)
	if rErr == nil || refRec.TaskStatus != TaskStatusFailed {
		t.Fatalf("expected FAILED after refresh lookup, got rec=%+v err=%v", refRec, rErr)
	}
	if refRec.BillingState != fidelitygate.BillingRefunded || refRec.ActualCostFen != 0 {
		t.Fatalf("expected refunded 0 cost after provider failure on refresh, got state=%s cost=%d", refRec.BillingState, refRec.ActualCostFen)
	}
	if provider.relightCalls.Load() != 1 {
		t.Fatalf("expected 1 Relight call total, got %d", provider.relightCalls.Load())
	}
	holds, spends, refunds, _ := bc.counts()
	if holds != 1 || spends != 0 || refunds != 1 {
		t.Fatalf("expected holds=1 spends=0 refunds=1, got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}

func assertContainsReason(t *testing.T, reasons []string, want string) {
	t.Helper()
	for _, r := range reasons {
		if r == want {
			return
		}
	}
	t.Fatalf("expected failure reasons %v to contain %q", reasons, want)
}
