package i2vcontract

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

// --- Test doubles ---

type fakeBillingClient struct {
	mu           sync.Mutex
	holdCalls    int
	spendCalls   int
	refundCalls  int
	lookupCalls  int
	stateByHold  map[string]fidelitygate.BillingState
	nextSeq      int
	spendErr     error
	holdErr      error
}

func newFakeBillingClient() *fakeBillingClient {
	return &fakeBillingClient{stateByHold: make(map[string]fidelitygate.BillingState)}
}

func (b *fakeBillingClient) Hold(_ context.Context, _ fidelitygate.HoldRequest) (fidelitygate.HoldResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.holdCalls++
	if b.holdErr != nil {
		return fidelitygate.HoldResponse{}, b.holdErr
	}
	b.nextSeq++
	id := "hold_" + string(rune('0'+b.nextSeq))
	b.stateByHold[id] = fidelitygate.BillingHeld
	return fidelitygate.HoldResponse{HoldID: id}, nil
}

func (b *fakeBillingClient) Spend(_ context.Context, req fidelitygate.SpendRequest) (fidelitygate.SpendResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spendCalls++
	if b.spendErr != nil {
		return fidelitygate.SpendResponse{}, b.spendErr
	}
	b.stateByHold[req.HoldID] = fidelitygate.BillingSpent
	return fidelitygate.SpendResponse{SpendID: "spend_" + req.HoldID}, nil
}

func (b *fakeBillingClient) Refund(_ context.Context, req fidelitygate.RefundRequest) (fidelitygate.RefundResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refundCalls++
	b.stateByHold[req.HoldID] = fidelitygate.BillingRefunded
	return fidelitygate.RefundResponse{RefundID: "refund_" + req.HoldID}, nil
}

func (b *fakeBillingClient) LookupHold(_ context.Context, req fidelitygate.LookupHoldRequest) (fidelitygate.LookupHoldResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lookupCalls++
	st := b.stateByHold[req.HoldID]
	if st == "" {
		st = fidelitygate.BillingHeld
	}
	return fidelitygate.LookupHoldResponse{HoldID: req.HoldID, State: st}, nil
}

func (b *fakeBillingClient) counts() (holds, spends, refunds int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.holdCalls, b.spendCalls, b.refundCalls
}

type fakeVideoAdapter struct {
	qual          ModelQualification
	submitCalls   atomic.Int32
	pollCalls     atomic.Int32
	enhanceCalls  atomic.Int32
	downloadCalls atomic.Int32

	submitFn   func(ctx context.Context, req ProviderSubmitRequest) (ProviderSubmitResponse, error)
	pollFn     func(ctx context.Context, providerJobID string) (ProviderPollResponse, error)
	enhanceFn  func(ctx context.Context, req ProviderEnhanceRequest) (ProviderArtifactResponse, error)
	downloadFn func(ctx context.Context, req ProviderDownloadRequest) (VideoFidelityMetrics, error)
}

func (a *fakeVideoAdapter) Qualification() ModelQualification { return a.qual }

func (a *fakeVideoAdapter) SubmitI2V(ctx context.Context, req ProviderSubmitRequest) (ProviderSubmitResponse, error) {
	a.submitCalls.Add(1)
	if a.submitFn != nil {
		return a.submitFn(ctx, req)
	}
	return ProviderSubmitResponse{
		ProviderJobID:   "pjob_1",
		OutputAssetID:   "vid_asset_1",
		OutputVersionID: "vid_ver_1",
		RawVideoURL:     "https://cdn.example.com/signed/raw-video.mp4",
		ProviderCharged: true,
		ActualCostFen:   250,
	}, nil
}

func (a *fakeVideoAdapter) PollI2V(ctx context.Context, providerJobID string) (ProviderPollResponse, error) {
	a.pollCalls.Add(1)
	if a.pollFn != nil {
		return a.pollFn(ctx, providerJobID)
	}
	return ProviderPollResponse{
		Status:          QueueStateCompleted,
		OutputAssetID:   "vid_asset_1",
		OutputVersionID: "vid_ver_1",
		RawVideoURL:     "https://cdn.example.com/signed/raw-video.mp4",
		ProviderCharged: true,
		ActualCostFen:   250,
	}, nil
}

func (a *fakeVideoAdapter) EnhanceVideo(ctx context.Context, req ProviderEnhanceRequest) (ProviderArtifactResponse, error) {
	a.enhanceCalls.Add(1)
	if a.enhanceFn != nil {
		return a.enhanceFn(ctx, req)
	}
	return ProviderArtifactResponse{
		EnhancedVideoURL: "https://cdn.example.com/signed/enhanced-video.mp4",
		OutputAssetID:    req.OutputAssetID,
		OutputVersionID:  req.OutputVersionID + "_enh",
	}, nil
}

func (a *fakeVideoAdapter) DownloadAndInspect(ctx context.Context, req ProviderDownloadRequest) (VideoFidelityMetrics, error) {
	a.downloadCalls.Add(1)
	if a.downloadFn != nil {
		return a.downloadFn(ctx, req)
	}
	return validVideoMetrics(req.VideoURL), nil
}

type fakeRenewer struct {
	calls atomic.Int32
	fn    func(ctx context.Context, ref FixedProductRef) (RenewedAssetURL, error)
}

func (r *fakeRenewer) RenewSignedURL(ctx context.Context, ref FixedProductRef) (RenewedAssetURL, error) {
	r.calls.Add(1)
	return r.fn(ctx, ref)
}

type fakeAuthVerifier struct {
	calls atomic.Int32
	fn    func(ctx context.Context, scope TaskScope, ref FixedProductRef) (AuthorizationCheck, error)
}

func (v *fakeAuthVerifier) VerifyAuthorization(ctx context.Context, scope TaskScope, ref FixedProductRef) (AuthorizationCheck, error) {
	v.calls.Add(1)
	return v.fn(ctx, scope, ref)
}

type timeoutNetErr struct{}

func (timeoutNetErr) Error() string   { return "i/o timeout" }
func (timeoutNetErr) Timeout() bool   { return true }
func (timeoutNetErr) Temporary() bool { return true }

var _ net.Error = timeoutNetErr{}

// --- Test builders ---

func qualifiedRealI2VModel() ModelQualification {
	return ModelQualification{
		ModelID:                       "xmodel-i2v-commerce-v1",
		ProviderID:                    "goboost-xmodel",
		AdapterID:                     "adapter-i2v-v1",
		AdapterRegistered:             true,
		Mode:                          ModeImageToVideo,
		ImageConditioned:              true,
		EmpiricalImageBindingVerified: true,
		VerifiedEvidenceRef:           "hui-2295-evidence-20261126",
		SupportedDurations:            []int{5, 10},
		SupportedAspectRatios:         []string{"9:16", "1:1", "16:9"},
		TargetPoolClass:               PoolPublicShared,
	}
}

func validProductRef(now time.Time) FixedProductRef {
	exp := now.Add(24 * time.Hour)
	return FixedProductRef{
		AssetID:   "asset_prod_001",
		VersionID: "ver_prod_v3",
		SHA256:    strings.Repeat("a", 64),
		SKURef:    "SKU-CUP-2026-RED",
		SubjectLock: SubjectConstraint{
			MaskRef:             "mask_001",
			PreserveStructure:   true,
			PreserveLogoAndText: true,
			View: ViewProvenance{
				CapturedViewCount:   1,
				CapturedAngles:      []string{"front"},
				HasCapturedBackView: false,
				CameraMove:          "360",
			},
		},
		Authorization: AuthorizationRef{
			GrantID:      "grant_001",
			AuthorizedBy: "merchant_owner_1",
			TenantID:     "tenant_a",
			ProjectID:    "proj_a",
			Scope:        "product_video_generation",
			ExpiresAt:    &exp,
			Revoked:      false,
		},
	}
}

func validVideoMetrics(videoURL string) VideoFidelityMetrics {
	if videoURL == "" {
		videoURL = "https://cdn.example.com/signed/final-video.mp4"
	}
	return VideoFidelityMetrics{
		VideoURL:                videoURL,
		MediaType:               "video/mp4",
		FileSHA256:              strings.Repeat("b", 64),
		ByteSize:                4_194_304,
		DecodedOK:               true,
		Codec:                   "h264",
		Width:                   1080,
		Height:                  1920,
		FPS:                     24,
		ActualDurationSeconds:   5.0,
		TemporalContinuityScore: 0.94,
		MaxFrozenFrameStreak:    1,
		FrozenFrameRatio:        0.02,
		SubjectStructureScore:   0.95,
		LogoIntegrityScore:      0.96,
		TextOCRSimilarity:       0.97,
		MaskIoUMean:             0.93,
		ClaimsTrue360:           false,
	}
}

func validSubmitRequest(now time.Time) SubmitRequest {
	return SubmitRequest{
		Scope: TaskScope{
			AppID:          fidelitygate.AppID,
			TenantID:       "tenant_a",
			PrincipalID:    "principal_a",
			ProjectID:      "proj_a",
			RequestKey:     "req_i2v_001",
			IdempotencyKey: "idem_i2v_001",
			TraceID:        "trace_i2v_001",
		},
		ProductRef:      validProductRef(now),
		SignedImageURL:  "https://cdn.example.com/signed/product-input.png?sig=abc",
		Prompt:          "缓慢环绕展示陶瓷保温杯，保持杯身标识与结构不变",
		CameraMove:      "360",
		DurationSeconds: 5,
		AspectRatio:     "9:16",
		EnhanceEnabled:  true,
		Policy: ExecutionPolicyRequest{
			AllowedPreferences: AllowedExecutionPreferences(),
			SelectedPreference: PrefImmediate,
		},
		Cost: CostAttribution{
			PayerType:         PayerPersonal,
			BillingAccountRef: "acct_personal_001",
			QuoteID:           "quote_001",
			PricingVersion:    "pricing_v1",
			EstimatedCostFen:  250,
		},
	}
}

// --- Tests ---

func TestMissingRealI2VAdapterFailsClosedWithoutBlockingBasicImages(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	bc := newFakeBillingClient()
	bm, err := fidelitygate.NewBillingRecoveryStateMachine(bc)
	if err != nil {
		t.Fatal(err)
	}
	coord, err := NewCoordinator(CoordinatorConfig{
		Adapter: nil,
		Billing: bm,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	snap, err := coord.Submit(context.Background(), validSubmitRequest(now))
	if !errors.Is(err, ErrNoRealI2VAdapter) {
		t.Fatalf("expected ErrNoRealI2VAdapter, got %v", err)
	}
	if snap.BlockCode != BlockNoRealI2VAdapter || snap.Qualification.Eligible {
		t.Fatalf("expected fail-closed BLOCKED_NO_REAL_I2V_ADAPTER, got %+v", snap)
	}
	if snap.BasicImagePathBlocked {
		t.Fatalf("missing I2V adapter must NEVER block basic product image path")
	}
	if snap.ProviderGenerateCalls != 0 || snap.Qualification.ProviderCalls != 0 {
		t.Fatalf("expected 0 provider calls, got %d", snap.ProviderGenerateCalls)
	}
	if len(snap.Qualification.MissingSeams) == 0 || snap.Qualification.AdapterSeamOwner == "" {
		t.Fatalf("must expose missing adapter seams and owner: %+v", snap.Qualification)
	}
	holds, spends, refunds := bc.counts()
	if holds != 0 || spends != 0 || refunds != 0 {
		t.Fatalf("must not touch billing without real adapter: holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}

func TestT2VMasqueradeAndFixtureRejectedFailClosed(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		model    ModelQualification
		wantErr  error
		wantCode string
	}{
		{
			name: "h3-fast posing as I2V",
			model: ModelQualification{
				ModelID:                       "h3-fast",
				ProviderID:                    "xmodel",
				AdapterID:                     "adapter-t2v",
				AdapterRegistered:             true,
				Mode:                          ModeImageToVideo,
				ImageConditioned:              true,
				EmpiricalImageBindingVerified: true,
				VerifiedEvidenceRef:           "fake-claim",
			},
			wantErr:  ErrT2VMasqueradeForbidden,
			wantCode: BlockT2VMasquerade,
		},
		{
			name: "explicit text_to_video mode",
			model: ModelQualification{
				ModelID:           "video-gen-v2",
				ProviderID:        "xmodel",
				AdapterID:         "adapter-v2",
				AdapterRegistered: true,
				Mode:              ModeTextToVideo,
			},
			wantErr:  ErrT2VMasqueradeForbidden,
			wantCode: BlockT2VMasquerade,
		},
		{
			name: "memory fixture masquerade",
			model: ModelQualification{
				ModelID:           "videofixture-v1",
				ProviderID:        "local",
				AdapterID:         "fixture",
				AdapterRegistered: true,
				Mode:              ModeImageToVideo,
				IsFixture:         true,
			},
			wantErr:  ErrFixtureOrPlaceholderForbidden,
			wantCode: BlockFixtureOrPlaceholder,
		},
		{
			name: "placeholder endpoint URL",
			model: ModelQualification{
				ModelID:                       "commerce-i2v",
				ProviderID:                    "xmodel",
				AdapterID:                     "adapter-1",
				AdapterRegistered:             true,
				Mode:                          ModeImageToVideo,
				ImageConditioned:              true,
				EmpiricalImageBindingVerified: true,
				VerifiedEvidenceRef:           "ev1",
				EndpointURL:                   "https://example.invalid/fake.mp4",
			},
			wantErr:  ErrFixtureOrPlaceholderForbidden,
			wantCode: BlockFixtureOrPlaceholder,
		},
		{
			name: "unverified image condition binding",
			model: ModelQualification{
				ModelID:                       "commerce-i2v-beta",
				ProviderID:                    "xmodel",
				AdapterID:                     "adapter-beta",
				AdapterRegistered:             true,
				Mode:                          ModeImageToVideo,
				ImageConditioned:              true,
				EmpiricalImageBindingVerified: false,
			},
			wantErr:  ErrUnverifiedImageBinding,
			wantCode: BlockUnverifiedImageBinding,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := &fakeVideoAdapter{qual: tc.model}
			bc := newFakeBillingClient()
			bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
			coord, _ := NewCoordinator(CoordinatorConfig{
				Adapter: adapter,
				Billing: bm,
				Now:     func() time.Time { return now },
			})
			snap, err := coord.Submit(context.Background(), validSubmitRequest(now))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err=%v, want %v", err, tc.wantErr)
			}
			if snap.BlockCode != tc.wantCode {
				t.Fatalf("block_code=%q, want %q", snap.BlockCode, tc.wantCode)
			}
			if snap.BasicImagePathBlocked {
				t.Fatalf("basic image path must remain unblocked")
			}
			if adapter.submitCalls.Load() != 0 {
				t.Fatalf("adapter must not be called on rejected model, calls=%d", adapter.submitCalls.Load())
			}
			holds, _, _ := bc.counts()
			if holds != 0 {
				t.Fatalf("billing hold must not occur, holds=%d", holds)
			}
		})
	}
}

func TestSingleImageCannotClaimTrue360AndFlagsBackViewUnproven(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	// 1. Single image claiming true 360 is hard-rejected.
	decision, err := EvaluateModelQualification(qualifiedRealI2VModel(), 5, "9:16", ViewProvenance{
		CapturedViewCount:   1,
		HasCapturedBackView: false,
		ClaimTrue360:        true,
	})
	if !errors.Is(err, ErrTrue360ClaimForbidden) {
		t.Fatalf("expected ErrTrue360ClaimForbidden, got %v", err)
	}
	if !decision.ViewAssessment.BackViewUnproven || !decision.ViewAssessment.CannotClaimTrue360 {
		t.Fatalf("expected BackViewUnproven=true & CannotClaimTrue360=true, got %+v", decision.ViewAssessment)
	}

	// 2. Single image without false claim is allowed (with real adapter) but explicitly preserves flags.
	decision, err = EvaluateModelQualification(qualifiedRealI2VModel(), 5, "9:16", ViewProvenance{
		CapturedViewCount:   1,
		HasCapturedBackView: false,
		ClaimTrue360:        false,
	})
	if err != nil || !decision.Eligible {
		t.Fatalf("expected qualified limited preview, err=%v decision=%+v", err, decision)
	}
	if !decision.ViewAssessment.BackViewUnproven || !decision.ViewAssessment.CannotClaimTrue360 {
		t.Fatalf("single image must keep BackViewUnproven=true and CannotClaimTrue360=true")
	}

	// 3. Multi-angle with captured back view clears BackViewUnproven.
	decisionMulti, err := EvaluateModelQualification(qualifiedRealI2VModel(), 5, "9:16", ViewProvenance{
		CapturedViewCount:   2,
		CapturedAngles:      []string{"front", "back"},
		HasCapturedBackView: true,
		ClaimTrue360:        true,
	})
	if err != nil || !decisionMulti.Eligible {
		t.Fatalf("multi-angle with captured back view should qualify: err=%v", err)
	}
	if decisionMulti.ViewAssessment.BackViewUnproven || decisionMulti.ViewAssessment.CannotClaimTrue360 {
		t.Fatalf("captured back view should not be marked unproven: %+v", decisionMulti.ViewAssessment)
	}

	// 4. Fidelity gate rejects video output claiming true 360 from single image.
	m := validVideoMetrics("")
	m.ClaimsTrue360 = true
	assess := EvaluateVideoFidelity(m, validProductRef(now).SubjectLock.View, DefaultVideoFidelityThresholds())
	if assess.Passed || assess.Verdict != VideoVerdictFail {
		t.Fatalf("video claiming true 360 from single image must fail fidelity gate: %+v", assess)
	}
}

func TestExecutionPolicyGuardsPriorityFreeQuotaAndInternalCards(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	expired := now.Add(-time.Minute)
	validExp := now.Add(time.Hour)
	prio := 99

	// Arbitrary numeric priority forbidden.
	if code, err := ValidateExecutionAndBillingPolicy(
		ExecutionPolicyRequest{SelectedPreference: PrefImmediate, PriorityNumber: &prio},
		CostAttribution{PayerType: PayerPersonal, BillingAccountRef: "acct_1", QuoteID: "q1", PricingVersion: "v1", EstimatedCostFen: 100},
		now,
	); !errors.Is(err, ErrArbitraryPriorityForbidden) || code != BlockArbitraryPriorityForbidden {
		t.Fatalf("arbitrary priority must fail: code=%s err=%v", code, err)
	}

	// Disallowed preference forbidden.
	if _, err := ValidateExecutionAndBillingPolicy(
		ExecutionPolicyRequest{AllowedPreferences: []ExecutionPreference{PrefNight, PrefIdle}, SelectedPreference: PrefImmediate},
		CostAttribution{PayerType: PayerPersonal, BillingAccountRef: "acct_1", QuoteID: "q1", PricingVersion: "v1", EstimatedCostFen: 100},
		now,
	); !errors.Is(err, ErrInvalidExecutionPreference) {
		t.Fatalf("disallowed preference must fail, got %v", err)
	}

	// Free tier accessing internal R&D GPU card forbidden.
	if code, err := ValidateExecutionAndBillingPolicy(
		ExecutionPolicyRequest{SelectedPreference: PrefIdle, TargetPoolClass: PoolInternalRDCard},
		CostAttribution{PayerType: PayerFreeGrant, FreeQuotaGrantID: "fg_1", FreeQuotaExpiresAt: &validExp, FreeQuotaRemaining: 2},
		now,
	); !errors.Is(err, ErrFreeTierInternalCardForbidden) || code != BlockFreeTierInternalCardForbidden {
		t.Fatalf("free tier internal R&D card must fail: code=%s err=%v", code, err)
	}

	// Expired free quota must NEVER auto-charge.
	if code, err := ValidateExecutionAndBillingPolicy(
		ExecutionPolicyRequest{SelectedPreference: PrefNight, TargetPoolClass: PoolNightIdleShared},
		CostAttribution{PayerType: PayerFreeGrant, FreeQuotaGrantID: "fg_1", FreeQuotaExpiresAt: &expired, FreeQuotaRemaining: 2},
		now,
	); !errors.Is(err, ErrFreeQuotaExpiredNoAutoCharge) || code != BlockFreeQuotaExpiredNoAutoCharge {
		t.Fatalf("expired free quota must fail closed without auto-charge: code=%s err=%v", code, err)
	}

	// Free tier with auto_convert_to_paid=true is explicitly rejected.
	if _, err := ValidateExecutionAndBillingPolicy(
		ExecutionPolicyRequest{SelectedPreference: PrefNight, TargetPoolClass: PoolNightIdleShared},
		CostAttribution{PayerType: PayerFreeGrant, FreeQuotaGrantID: "fg_1", FreeQuotaExpiresAt: &validExp, FreeQuotaRemaining: 2, AutoConvertToPaid: true},
		now,
	); !errors.Is(err, ErrFreeAutoChargeForbidden) {
		t.Fatalf("auto_convert_to_paid must be forbidden, got %v", err)
	}

	// Backend cost wallet cannot be treated as customer billing balance.
	if _, err := ValidateExecutionAndBillingPolicy(
		ExecutionPolicyRequest{SelectedPreference: PrefImmediate},
		CostAttribution{PayerType: PayerPersonal, BillingAccountRef: "platform_cost_wallet_prod", QuoteID: "q1", PricingVersion: "v1", EstimatedCostFen: 100},
		now,
	); !errors.Is(err, ErrBackendCostWalletMisuse) {
		t.Fatalf("backend cost wallet must be rejected, got %v", err)
	}

	// Internal token exposure audit detects leaks.
	if err := AuditNoInternalTokenLeak(map[string]any{"job_id": "i2v_1", "pool_token": "pool_tok_secret"}); !errors.Is(err, ErrInternalTokenExposure) {
		t.Fatalf("expected leak detection for pool_token, got %v", err)
	}
	if err := AuditNoInternalTokenLeak(map[string]any{"job_id": "i2v_1", "meta": map[string]any{"note": "autodl_key_123"}}); !errors.Is(err, ErrInternalTokenExposure) {
		t.Fatalf("expected leak detection for autodl value, got %v", err)
	}
}

func TestNightQueuePreDispatchRenewalAndRevocationAndTamperProtection(t *testing.T) {
	currentTime := time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)
	nextWin := time.Date(2026, 11, 27, 2, 0, 0, 0, time.UTC)

	t.Run("night wait renews expired URL and succeeds with immutable product ref", func(t *testing.T) {
		adapter := &fakeVideoAdapter{qual: qualifiedRealI2VModel()}
		bc := newFakeBillingClient()
		bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
		renewer := &fakeRenewer{
			fn: func(_ context.Context, ref FixedProductRef) (RenewedAssetURL, error) {
				return RenewedAssetURL{
					AssetID:   ref.AssetID,
					VersionID: ref.VersionID,
					SHA256:    ref.SHA256,
					SKURef:    ref.SKURef,
					SignedURL: "https://cdn.example.com/signed/renewed-at-0200.png?sig=fresh",
					ExpiresAt: currentTime.Add(12 * time.Hour),
				}, nil
			},
		}
		authVerifier := &fakeAuthVerifier{
			fn: func(_ context.Context, _ TaskScope, ref FixedProductRef) (AuthorizationCheck, error) {
				return AuthorizationCheck{Authorized: true, Ref: ref}, nil
			},
		}
		coord, _ := NewCoordinator(CoordinatorConfig{
			Adapter:      adapter,
			Billing:      bm,
			Renewer:      renewer,
			AuthVerifier: authVerifier,
			Now:          func() time.Time { return currentTime },
		})

		req := validSubmitRequest(currentTime)
		req.Policy.SelectedPreference = PrefNight
		req.Policy.DeferUntilWindow = true
		req.Policy.NextWindowAt = &nextWin
		req.Policy.ETASeconds = nil // nullable ETA

		queued, err := coord.Submit(context.Background(), req)
		if err != nil {
			t.Fatalf("enqueue night task failed: %v", err)
		}
		if queued.Queue.QueueState != QueueStateWaitingWindow || queued.Queue.ETASeconds != nil || queued.Queue.NextWindowAt == nil {
			t.Fatalf("unexpected night queue projection: %+v", queued.Queue)
		}
		if adapter.submitCalls.Load() != 0 {
			t.Fatalf("queued night task must not invoke provider before window")
		}
		if err := AuditNoInternalTokenLeak(queued); err != nil {
			t.Fatalf("queued snapshot leaked internal token: %v", err)
		}

		// Advance clock into night window and dispatch.
		currentTime = nextWin.Add(5 * time.Minute)
		done, err := coord.DispatchQueued(context.Background(), AccessScope{
			TenantID: "tenant_a", PrincipalID: "principal_a", ProjectID: "proj_a",
		}, queued.TaskID)
		if err != nil {
			t.Fatalf("dispatch queued failed: %v", err)
		}
		if done.Queue.QueueState != QueueStateCompleted || done.URLRenewalCount != 1 || done.AuthReverifyCount != 1 {
			t.Fatalf("expected completed with renewal=1 & auth=1, got %+v", done)
		}
		if done.FixedProductRef.AssetID != req.ProductRef.AssetID || done.FixedProductRef.SHA256 != req.ProductRef.SHA256 || done.FixedProductRef.SKURef != req.ProductRef.SKURef {
			t.Fatalf("fixed product ref mutated: %+v", done.FixedProductRef)
		}
		if done.Candidate == nil || done.Candidate.Destination != CandidateDestinationMerchantLibrary || done.Candidate.AutoPublished || done.Candidate.RewardTriggered {
			t.Fatalf("candidate must land only in merchant candidate library without auto publish/reward: %+v", done.Candidate)
		}
	})

	t.Run("revoked authorization before night dispatch blocks with 0 calls and 0 holds", func(t *testing.T) {
		now := time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)
		adapter := &fakeVideoAdapter{qual: qualifiedRealI2VModel()}
		bc := newFakeBillingClient()
		bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
		authVerifier := &fakeAuthVerifier{
			fn: func(_ context.Context, _ TaskScope, ref FixedProductRef) (AuthorizationCheck, error) {
				return AuthorizationCheck{Authorized: false, Reason: "merchant revoked asset grant", Ref: ref}, nil
			},
		}
		coord, _ := NewCoordinator(CoordinatorConfig{
			Adapter:      adapter,
			Billing:      bm,
			AuthVerifier: authVerifier,
			Now:          func() time.Time { return now },
		})

		req := validSubmitRequest(now)
		req.Policy.SelectedPreference = PrefNight
		req.Policy.DeferUntilWindow = true
		queued, err := coord.Submit(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}

		blocked, err := coord.DispatchQueued(context.Background(), AccessScope{
			TenantID: "tenant_a", PrincipalID: "principal_a", ProjectID: "proj_a",
		}, queued.TaskID)
		if !errors.Is(err, ErrAuthorizationRevoked) {
			t.Fatalf("expected ErrAuthorizationRevoked, got %v", err)
		}
		if blocked.BlockCode != BlockAuthorizationRevoked || adapter.submitCalls.Load() != 0 {
			t.Fatalf("expected blocked with 0 provider calls, got %+v calls=%d", blocked, adapter.submitCalls.Load())
		}
		holds, _, _ := bc.counts()
		if holds != 0 {
			t.Fatalf("revoked task must not hold funds, holds=%d", holds)
		}
	})

	t.Run("renewer attempting product swap is blocked fail-closed", func(t *testing.T) {
		now := time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)
		adapter := &fakeVideoAdapter{qual: qualifiedRealI2VModel()}
		bc := newFakeBillingClient()
		bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
		renewer := &fakeRenewer{
			fn: func(_ context.Context, ref FixedProductRef) (RenewedAssetURL, error) {
				return RenewedAssetURL{
					AssetID:   "asset_DIFFERENT_PRODUCT",
					VersionID: ref.VersionID,
					SHA256:    strings.Repeat("f", 64),
					SKURef:    "SKU-SWAPPED",
					SignedURL: "https://cdn.example.com/signed/swapped.png",
					ExpiresAt: now.Add(time.Hour),
				}, nil
			},
		}
		coord, _ := NewCoordinator(CoordinatorConfig{
			Adapter: adapter,
			Billing: bm,
			Renewer: renewer,
			Now:     func() time.Time { return now },
		})

		req := validSubmitRequest(now)
		req.Policy.SelectedPreference = PrefIdle
		req.Policy.DeferUntilWindow = true
		queued, err := coord.Submit(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		blocked, err := coord.DispatchQueued(context.Background(), AccessScope{
			TenantID: "tenant_a", PrincipalID: "principal_a", ProjectID: "proj_a",
		}, queued.TaskID)
		if !errors.Is(err, ErrProductReferenceTampered) {
			t.Fatalf("expected ErrProductReferenceTampered, got %v", err)
		}
		if blocked.BlockCode != BlockProductRefTampered || adapter.submitCalls.Load() != 0 {
			t.Fatalf("product swap must be blocked with 0 calls: %+v", blocked)
		}
	})

	t.Run("free quota expiring during night wait fails closed without auto-charging", func(t *testing.T) {
		clock := time.Date(2026, 11, 26, 18, 0, 0, 0, time.UTC)
		freeExp := clock.Add(2 * time.Hour)
		adapter := &fakeVideoAdapter{qual: qualifiedRealI2VModel()}
		bc := newFakeBillingClient()
		bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
		coord, _ := NewCoordinator(CoordinatorConfig{
			Adapter: adapter,
			Billing: bm,
			Now:     func() time.Time { return clock },
		})

		req := validSubmitRequest(clock)
		req.Policy.SelectedPreference = PrefNight
		req.Policy.DeferUntilWindow = true
		req.Policy.TargetPoolClass = PoolNightIdleShared
		req.Cost = CostAttribution{
			PayerType:          PayerFreeGrant,
			FreeQuotaGrantID:   "free_grant_001",
			FreeQuotaExpiresAt: &freeExp,
			FreeQuotaRemaining: 1,
			AutoConvertToPaid:  false,
		}
		queued, err := coord.Submit(context.Background(), req)
		if err != nil {
			t.Fatalf("enqueue free night task failed: %v", err)
		}

		// Advance past free quota expiration.
		clock = freeExp.Add(30 * time.Minute)
		blocked, err := coord.DispatchQueued(context.Background(), AccessScope{
			TenantID: "tenant_a", PrincipalID: "principal_a", ProjectID: "proj_a",
		}, queued.TaskID)
		if !errors.Is(err, ErrFreeQuotaExpiredNoAutoCharge) {
			t.Fatalf("expected ErrFreeQuotaExpiredNoAutoCharge, got %v", err)
		}
		if blocked.Billing.AutoChargedPaid || blocked.BlockCode != BlockFreeQuotaExpiredNoAutoCharge {
			t.Fatalf("must never auto-charge expired free quota: %+v", blocked)
		}
		holds, spends, _ := bc.counts()
		if holds != 0 || spends != 0 || adapter.submitCalls.Load() != 0 {
			t.Fatalf("expired free quota must have 0 holds/spends/calls: holds=%d spends=%d calls=%d", holds, spends, adapter.submitCalls.Load())
		}
	})
}

func TestConcurrentIdempotentSubmitPreventsDoubleGenerationAndDoubleCharge(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	adapter := &fakeVideoAdapter{qual: qualifiedRealI2VModel()}
	bc := newFakeBillingClient()
	bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
	coord, _ := NewCoordinator(CoordinatorConfig{
		Adapter: adapter,
		Billing: bm,
		Now:     func() time.Time { return now },
	})

	req := validSubmitRequest(now)
	const workers = 12
	var wg sync.WaitGroup
	taskIDs := make([]string, workers)
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			snap, err := coord.Submit(context.Background(), req)
			taskIDs[idx] = snap.TaskID
			errs[idx] = err
		}(i)
	}
	wg.Wait()

	firstID := taskIDs[0]
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d failed: %v", i, errs[i])
		}
		if taskIDs[i] != firstID {
			t.Fatalf("worker %d got task_id=%q, want %q", i, taskIDs[i], firstID)
		}
	}
	if adapter.submitCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 provider generate call, got %d", adapter.submitCalls.Load())
	}
	holds, spends, refunds := bc.counts()
	if holds != 1 || spends != 1 || refunds != 0 {
		t.Fatalf("expected holds=1 spends=1 refunds=0, got %d/%d/%d", holds, spends, refunds)
	}

	// Conflicting payload on same idempotency key must be rejected.
	conflictReq := req
	conflictReq.Prompt = "完全不同的提示词"
	if _, err := coord.Submit(context.Background(), conflictReq); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("expected ErrIdempotencyConflict, got %v", err)
	}
}

func TestStageScopedRetryOnlyRetriesEnhanceOrDownloadWithoutRegenerating(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var failEnhance atomic.Bool
	var failDownload atomic.Bool
	failEnhance.Store(true)
	failDownload.Store(true)

	adapter := &fakeVideoAdapter{
		qual: qualifiedRealI2VModel(),
		enhanceFn: func(_ context.Context, req ProviderEnhanceRequest) (ProviderArtifactResponse, error) {
			if failEnhance.Swap(false) {
				return ProviderArtifactResponse{}, errors.New("transient enhance worker crash")
			}
			return ProviderArtifactResponse{
				EnhancedVideoURL: "https://cdn.example.com/signed/enhanced-ok.mp4",
				OutputAssetID:    req.OutputAssetID,
				OutputVersionID:  req.OutputVersionID + "_enh",
			}, nil
		},
		downloadFn: func(_ context.Context, req ProviderDownloadRequest) (VideoFidelityMetrics, error) {
			if failDownload.Swap(false) {
				return VideoFidelityMetrics{}, errors.New("transient CDN download timeout")
			}
			return validVideoMetrics(req.VideoURL), nil
		},
	}

	bc := newFakeBillingClient()
	bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
	coord, _ := NewCoordinator(CoordinatorConfig{
		Adapter: adapter,
		Billing: bm,
		Now:     func() time.Time { return now },
	})

	// Record image stages separately to verify image/video + generate/enhance counters.
	coord.RecordImageStage("tenant_a", "proj_a", StageGenerate, true, false)
	coord.RecordImageStage("tenant_a", "proj_a", StageEnhance, true, false)

	req := validSubmitRequest(now)
	access := AccessScope{TenantID: "tenant_a", PrincipalID: "principal_a", ProjectID: "proj_a"}

	// First run: generate succeeds, enhance fails.
	snap1, err := coord.Submit(context.Background(), req)
	if err == nil || snap1.FailedStage != StageEnhance {
		t.Fatalf("expected enhance failure, err=%v failed_stage=%q", err, snap1.FailedStage)
	}
	if adapter.submitCalls.Load() != 1 || adapter.enhanceCalls.Load() != 1 {
		t.Fatalf("unexpected call counts after enhance fail: submit=%d enhance=%d", adapter.submitCalls.Load(), adapter.enhanceCalls.Load())
	}

	// Retrying StageGenerate must be forbidden since generate already succeeded.
	if _, err := coord.RetryStage(context.Background(), access, snap1.TaskID, StageGenerate); !errors.Is(err, ErrStageRetryNotAllowed) {
		t.Fatalf("retrying generate after success must fail: %v", err)
	}

	// Retry enhance: succeeds, then download fails transiently.
	snap2, err := coord.RetryStage(context.Background(), access, snap1.TaskID, StageEnhance)
	if err == nil || snap2.FailedStage != StageDownload {
		t.Fatalf("expected download failure after enhance retry, err=%v failed_stage=%q", err, snap2.FailedStage)
	}
	if adapter.submitCalls.Load() != 1 || adapter.enhanceCalls.Load() != 2 || adapter.downloadCalls.Load() != 1 {
		t.Fatalf("enhance retry must not regenerate: submit=%d enhance=%d download=%d",
			adapter.submitCalls.Load(), adapter.enhanceCalls.Load(), adapter.downloadCalls.Load())
	}

	// Retry download ONLY: succeeds and completes without regenerating or re-enhancing.
	snap3, err := coord.RetryStage(context.Background(), access, snap1.TaskID, StageDownload)
	if err != nil || snap3.Queue.QueueState != QueueStateCompleted {
		t.Fatalf("download retry should complete: err=%v state=%q", err, snap3.Queue.QueueState)
	}
	if adapter.submitCalls.Load() != 1 || adapter.enhanceCalls.Load() != 2 || adapter.downloadCalls.Load() != 2 {
		t.Fatalf("download retry must not rerun generate or enhance: submit=%d enhance=%d download=%d",
			adapter.submitCalls.Load(), adapter.enhanceCalls.Load(), adapter.downloadCalls.Load())
	}
	holds, spends, refunds := bc.counts()
	if holds != 1 || spends != 1 || refunds != 0 {
		t.Fatalf("stage retries must never double-charge: holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}

	cnt := coord.StageCounters("tenant_a", "proj_a")
	if cnt.ImageGenerate.Succeeded != 1 || cnt.ImageEnhance.Succeeded != 1 ||
		cnt.VideoGenerate.Succeeded != 1 || cnt.VideoEnhance.Retried != 1 || cnt.Download.Retried != 1 {
		t.Fatalf("unexpected separated stage counters: %+v", cnt)
	}
}

func TestUnknownOutcomeRefreshReconcilesReadOnlyWithoutRegenerating(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	adapter := &fakeVideoAdapter{
		qual: qualifiedRealI2VModel(),
		submitFn: func(_ context.Context, _ ProviderSubmitRequest) (ProviderSubmitResponse, error) {
			return ProviderSubmitResponse{ProviderJobID: "pjob_unknown_1"}, timeoutNetErr{}
		},
	}
	bc := newFakeBillingClient()
	bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
	coord, _ := NewCoordinator(CoordinatorConfig{
		Adapter: adapter,
		Billing: bm,
		Now:     func() time.Time { return now },
	})

	req := validSubmitRequest(now)
	req.EnhanceEnabled = false
	snap, err := coord.Submit(context.Background(), req)
	if !errors.Is(err, ErrTaskOutcomeUnknown) || snap.Queue.QueueState != QueueStateUnknown {
		t.Fatalf("expected QueueStateUnknown, err=%v state=%q", err, snap.Queue.QueueState)
	}

	// Refresh reconciles read-only via PollI2V without calling SubmitI2V again.
	recovered, err := coord.Refresh(context.Background(), AccessScope{
		TenantID: "tenant_a", PrincipalID: "principal_a", ProjectID: "proj_a",
	}, snap.TaskID)
	if err != nil || recovered.Queue.QueueState != QueueStateCompleted {
		t.Fatalf("refresh should recover completed task: err=%v state=%q", err, recovered.Queue.QueueState)
	}
	if adapter.submitCalls.Load() != 1 || adapter.pollCalls.Load() != 1 {
		t.Fatalf("expected submit=1 poll=1, got submit=%d poll=%d", adapter.submitCalls.Load(), adapter.pollCalls.Load())
	}
}

func TestCrossTenantIsolationAndCandidateNoAutoPublishAndDownstreamEnvelope(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	adapter := &fakeVideoAdapter{qual: qualifiedRealI2VModel()}
	bc := newFakeBillingClient()
	bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
	coord, _ := NewCoordinator(CoordinatorConfig{
		Adapter: adapter,
		Billing: bm,
		Now:     func() time.Time { return now },
	})

	snap, err := coord.Submit(context.Background(), validSubmitRequest(now))
	if err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	// Cross-tenant reads/refreshes/envelopes must fail closed.
	otherTenant := AccessScope{TenantID: "tenant_b", PrincipalID: "principal_b", ProjectID: "proj_a"}
	if _, err := coord.GetTask(otherTenant, snap.TaskID); !errors.Is(err, ErrCrossTenantForbidden) {
		t.Fatalf("cross-tenant GetTask must fail: %v", err)
	}
	if _, err := coord.Refresh(context.Background(), otherTenant, snap.TaskID); !errors.Is(err, ErrCrossTenantForbidden) {
		t.Fatalf("cross-tenant Refresh must fail: %v", err)
	}
	if _, err := coord.GetDownstreamEnvelope(otherTenant, snap.TaskID); !errors.Is(err, ErrCrossTenantForbidden) {
		t.Fatalf("cross-tenant GetDownstreamEnvelope must fail: %v", err)
	}

	// Candidate never auto-publishes or triggers rewards.
	if snap.Candidate == nil || snap.Candidate.Destination != CandidateDestinationMerchantLibrary ||
		snap.Candidate.AutoPublished || snap.Candidate.RewardTriggered || snap.Candidate.MerchantConfirmed {
		t.Fatalf("unexpected candidate return state: %+v", snap.Candidate)
	}
	if err := snap.Candidate.AttemptAutoPublish(); !errors.Is(err, ErrAutoPublishForbidden) {
		t.Fatalf("expected ErrAutoPublishForbidden, got %v", err)
	}
	if err := snap.Candidate.AttemptAutoReward(); !errors.Is(err, ErrAutoRewardForbidden) {
		t.Fatalf("expected ErrAutoRewardForbidden, got %v", err)
	}

	// Downstream envelope for HUI-5196 / HUI-1745 / HUI-2232 retains fidelity & limitations with 0 internal token leaks.
	env, err := coord.GetDownstreamEnvelope(AccessScope{
		TenantID: "tenant_a", PrincipalID: "principal_a", ProjectID: "proj_a",
	}, snap.TaskID)
	if err != nil {
		t.Fatalf("GetDownstreamEnvelope failed: %v", err)
	}
	if env.SchemaVersion != EnvelopeSchemaVersion || !env.BackViewUnproven || !env.CannotClaimTrue360 || env.BasicImagePathBlocked {
		t.Fatalf("unexpected downstream envelope: %+v", env)
	}
	if env.Candidate == nil || !env.Candidate.Fidelity.Passed || !env.Candidate.Fidelity.DecodedOK || env.Candidate.Fidelity.HasFrozenFrameAnomaly {
		t.Fatalf("downstream fidelity metadata missing or invalid: %+v", env.Candidate)
	}
	if err := AuditNoInternalTokenLeak(env); err != nil {
		t.Fatalf("downstream envelope leaked internal token: %v", err)
	}
}

func TestVideoFidelityGateRejectsFrozenFramesAndLogoDriftAndRefundsPolicy(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	adapter := &fakeVideoAdapter{
		qual: qualifiedRealI2VModel(),
		downloadFn: func(_ context.Context, req ProviderDownloadRequest) (VideoFidelityMetrics, error) {
			m := validVideoMetrics(req.VideoURL)
			m.MaxFrozenFrameStreak = 18
			m.FrozenFrameRatio = 0.45
			m.LogoIntegrityScore = 0.55
			return m, nil
		},
	}
	bc := newFakeBillingClient()
	bm, _ := fidelitygate.NewBillingRecoveryStateMachine(bc)
	coord, _ := NewCoordinator(CoordinatorConfig{
		Adapter:       adapter,
		Billing:       bm,
		FailurePolicy: fidelitygate.GateFailurePolicyRefundPlatformAbsorbed,
		Now:           func() time.Time { return now },
	})

	req := validSubmitRequest(now)
	req.EnhanceEnabled = false
	snap, err := coord.Submit(context.Background(), req)
	if !errors.Is(err, ErrVideoFidelityFailed) {
		t.Fatalf("expected ErrVideoFidelityFailed, got %v", err)
	}
	if snap.Candidate == nil || snap.Candidate.VideoURL != "" || snap.Candidate.Fidelity.Passed || !snap.Candidate.Fidelity.HasFrozenFrameAnomaly {
		t.Fatalf("rejected candidate must not expose playable video URL and must record frozen-frame anomaly: %+v", snap.Candidate)
	}
	holds, spends, refunds := bc.counts()
	if holds != 1 || spends != 0 || refunds != 1 {
		t.Fatalf("platform-absorbed gate failure should refund hold: holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}
