package fidelitygate

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func validSHA(char byte) string {
	return strings.Repeat(string(char), 64)
}

func makeHealthyRequest() I2XGateRequest {
	return I2XGateRequest{
		AppID:        AppID,
		PrincipalID:  "usr_01HXY12345",
		RequestKey:   "req_bg_swap_001",
		ClaimMode:    ClaimFactualFidelity,
		ViewSource:   ViewSourceCaptured,
		SampleOrigin: SampleOriginReal,
		Report: SubjectFidelityReport{
			SourceSHA256:         validSHA('a'),
			CandidateSHA256:      validSHA('b'),
			MaskIoU:              0.98,
			ForegroundPixelDrift: 0.02,
			LogoOCRSimilarity:    0.99,
			BackgroundReplaced:   true,
			AlphaIntegrityOK:     true,
			EvaluatorVersion:     DefaultEvaluatorVersion,
			ThresholdVersion:     DefaultThresholdVersion,
		},
	}
}

func encodeTestPNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// buildProductScene creates a deterministic 32x32 product image where pixels inside
// [x0..x1, y0..y1) are the product subject and pixels outside are the background.
func buildProductScene(fg, bg color.NRGBA, x0, y0, x1, y1 int) (*image.NRGBA, *image.NRGBA) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	mask := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			if x >= x0 && x < x1 && y >= y0 && y < y1 {
				img.SetNRGBA(x, y, fg)
				mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
			} else {
				img.SetNRGBA(x, y, bg)
				mask.SetNRGBA(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 0})
			}
		}
	}
	return img, mask
}

func containsReason(reasons []string, target string) bool {
	for _, r := range reasons {
		if r == target {
			return true
		}
	}
	return false
}

func TestEvaluateI2XQualityGate_HealthyBackgroundSwapPasses(t *testing.T) {
	srcImg, srcMask := buildProductScene(
		color.NRGBA{R: 210, G: 40, B: 30, A: 255},
		color.NRGBA{R: 245, G: 245, B: 245, A: 255},
		8, 8, 24, 24,
	)
	candImg, candMask := buildProductScene(
		color.NRGBA{R: 210, G: 40, B: 30, A: 255},
		color.NRGBA{R: 35, G: 90, B: 140, A: 255}, // genuine new studio background
		8, 8, 24, 24,
	)

	report, err := MeasurePNGSubjectFidelity(
		encodeTestPNG(t, srcImg),
		encodeTestPNG(t, candImg),
		encodeTestPNG(t, srcMask),
		encodeTestPNG(t, candMask),
		0.99,
	)
	if err != nil {
		t.Fatalf("MeasurePNGSubjectFidelity failed: %v", err)
	}
	if !report.BackgroundReplaced {
		t.Fatalf("expected BackgroundReplaced=true for genuine background swap")
	}
	if report.MaskIoU != 1.0 {
		t.Fatalf("expected MaskIoU=1.0, got %v", report.MaskIoU)
	}
	if report.ForegroundPixelDrift != 0.0 {
		t.Fatalf("expected ForegroundPixelDrift=0, got %v", report.ForegroundPixelDrift)
	}

	req := I2XGateRequest{
		AppID:        AppID,
		PrincipalID:  "usr_real_01",
		RequestKey:   "req_swap_ok",
		ClaimMode:    ClaimFactualFidelity,
		ViewSource:   ViewSourceCaptured,
		SampleOrigin: SampleOriginReal,
		Report:       report,
	}
	dec := EvaluateI2XQualityGate(req, DefaultGateConfig())
	if !dec.Passed || dec.Verdict != VerdictPass || !dec.CanAdopt() {
		t.Fatalf("expected healthy background swap to pass, got: %+v", dec)
	}
	if dec.RepairAction != RepairActionNone {
		t.Fatalf("expected RepairActionNone, got %q", dec.RepairAction)
	}
	if len(dec.FailureReasons) != 0 {
		t.Fatalf("expected zero failure reasons, got %v", dec.FailureReasons)
	}
}

func TestEvaluateI2XQualityGate_EchoCopyFails(t *testing.T) {
	t.Run("measured byte-identical png echo copy", func(t *testing.T) {
		srcImg, srcMask := buildProductScene(
			color.NRGBA{R: 210, G: 40, B: 30, A: 255},
			color.NRGBA{R: 245, G: 245, B: 245, A: 255},
			8, 8, 24, 24,
		)
		srcBytes := encodeTestPNG(t, srcImg)
		maskBytes := encodeTestPNG(t, srcMask)

		report, err := MeasurePNGSubjectFidelity(srcBytes, srcBytes, maskBytes, maskBytes, 1.0)
		if err != nil {
			t.Fatalf("MeasurePNGSubjectFidelity: %v", err)
		}
		if report.BackgroundReplaced {
			t.Fatalf("echo copy must not set BackgroundReplaced=true")
		}

		req := makeHealthyRequest()
		req.Report = report
		dec := EvaluateI2XQualityGate(req, DefaultGateConfig())
		if dec.Passed || dec.CanAdopt() {
			t.Fatalf("echo copy must fail quality gate: %+v", dec)
		}
		if !containsReason(dec.FailureReasons, ReasonEchoCopyDetected) {
			t.Fatalf("expected %s in reasons, got %v", ReasonEchoCopyDetected, dec.FailureReasons)
		}
		if dec.RepairAction != RepairActionRetryBackgroundSwap {
			t.Fatalf("expected %s, got %s", RepairActionRetryBackgroundSwap, dec.RepairAction)
		}
	})

	t.Run("distinct hash but BackgroundReplaced=false", func(t *testing.T) {
		req := makeHealthyRequest()
		req.Report.BackgroundReplaced = false
		dec := EvaluateI2XQualityGate(req, DefaultGateConfig())
		if dec.Passed || dec.CanAdopt() {
			t.Fatalf("expected failure when BackgroundReplaced=false: %+v", dec)
		}
		if !containsReason(dec.FailureReasons, ReasonEchoCopyDetected) {
			t.Fatalf("expected %s, got %v", ReasonEchoCopyDetected, dec.FailureReasons)
		}
		if dec.RepairAction != RepairActionRetryBackgroundSwap {
			t.Fatalf("expected %s, got %s", RepairActionRetryBackgroundSwap, dec.RepairAction)
		}
	})
}

func TestEvaluateI2XQualityGate_GeometryMaskDriftFails(t *testing.T) {
	t.Run("shifted silhouette lowers MaskIoU", func(t *testing.T) {
		srcImg, srcMask := buildProductScene(
			color.NRGBA{R: 180, G: 50, B: 20, A: 255},
			color.NRGBA{R: 240, G: 240, B: 240, A: 255},
			6, 6, 22, 22,
		)
		// Subject silhouette shifted by 4 pixels -> severe geometry distortion.
		candImg, candMask := buildProductScene(
			color.NRGBA{R: 180, G: 50, B: 20, A: 255},
			color.NRGBA{R: 20, G: 110, B: 170, A: 255},
			10, 10, 26, 26,
		)

		report, err := MeasurePNGSubjectFidelity(
			encodeTestPNG(t, srcImg),
			encodeTestPNG(t, candImg),
			encodeTestPNG(t, srcMask),
			encodeTestPNG(t, candMask),
			0.99,
		)
		if err != nil {
			t.Fatalf("MeasurePNGSubjectFidelity: %v", err)
		}
		if report.MaskIoU >= DefaultMinMaskIoU {
			t.Fatalf("expected shifted mask IoU < %v, got %v", DefaultMinMaskIoU, report.MaskIoU)
		}

		req := makeHealthyRequest()
		req.Report = report
		dec := EvaluateI2XQualityGate(req, DefaultGateConfig())
		if dec.Passed || dec.CanAdopt() {
			t.Fatalf("expected geometry distortion to fail gate: %+v", dec)
		}
		if !containsReason(dec.FailureReasons, ReasonMaskIoUBelowThreshold) {
			t.Fatalf("expected %s, got %v", ReasonMaskIoUBelowThreshold, dec.FailureReasons)
		}
		if dec.RepairAction != RepairActionRelockSubjectMask {
			t.Fatalf("expected %s, got %s", RepairActionRelockSubjectMask, dec.RepairAction)
		}
	})

	t.Run("foreground pixel drift and alpha failure", func(t *testing.T) {
		req := makeHealthyRequest()
		req.Report.ForegroundPixelDrift = 0.25
		req.Report.AlphaIntegrityOK = false
		dec := EvaluateI2XQualityGate(req, DefaultGateConfig())
		if dec.Passed || dec.CanAdopt() {
			t.Fatalf("expected drift/alpha failure: %+v", dec)
		}
		if !containsReason(dec.FailureReasons, ReasonForegroundDriftExceeded) {
			t.Fatalf("expected %s, got %v", ReasonForegroundDriftExceeded, dec.FailureReasons)
		}
		if !containsReason(dec.FailureReasons, ReasonAlphaIntegrityFailed) {
			t.Fatalf("expected %s, got %v", ReasonAlphaIntegrityFailed, dec.FailureReasons)
		}
		if dec.RepairAction != RepairActionRelockSubjectMask {
			t.Fatalf("expected %s, got %s", RepairActionRelockSubjectMask, dec.RepairAction)
		}
	})
}

func TestEvaluateI2XQualityGate_OCRTextGarbleFails(t *testing.T) {
	req := makeHealthyRequest()
	req.Report.LogoOCRSimilarity = 0.62 // garbled brand/ingredient label
	dec := EvaluateI2XQualityGate(req, DefaultGateConfig())
	if dec.Passed || dec.CanAdopt() {
		t.Fatalf("expected OCR garble to fail gate: %+v", dec)
	}
	if !containsReason(dec.FailureReasons, ReasonLogoOCRBelowThreshold) {
		t.Fatalf("expected %s, got %v", ReasonLogoOCRBelowThreshold, dec.FailureReasons)
	}
	if dec.RepairAction != RepairActionRestoreBrandText {
		t.Fatalf("expected %s, got %s", RepairActionRestoreBrandText, dec.RepairAction)
	}
}

func TestEvaluateI2XQualityGate_AIInferredBackViewFactualFidelityFails(t *testing.T) {
	req := makeHealthyRequest()
	req.ClaimMode = ClaimFactualFidelity
	req.ViewSource = ViewSourceAIInferred
	req.InferredViewVerified = false

	dec := EvaluateI2XQualityGate(req, DefaultGateConfig())
	if dec.Passed || dec.CanAdopt() {
		t.Fatalf("unverified AI-inferred back-view must fail factual fidelity gate: %+v", dec)
	}
	if !containsReason(dec.FailureReasons, ReasonUnverifiedInferredFactual) {
		t.Fatalf("expected %s, got %v", ReasonUnverifiedInferredFactual, dec.FailureReasons)
	}
	if dec.RepairAction != RepairActionRequireMultiAngleCapture {
		t.Fatalf("expected %s, got %s", RepairActionRequireMultiAngleCapture, dec.RepairAction)
	}

	// Once verified by real multi-angle capture/review, provenance check passes.
	req.InferredViewVerified = true
	verifiedDec := EvaluateI2XQualityGate(req, DefaultGateConfig())
	if !verifiedDec.Passed || !verifiedDec.CanAdopt() {
		t.Fatalf("verified inferred view should pass when all metrics pass: %+v", verifiedDec)
	}
}

func TestEvaluateI2XQualityGate_FailClosedInvalidInputs(t *testing.T) {
	cases := []struct {
		name       string
		mutateReq  func(*I2XGateRequest)
		mutateCfg  func(*GateConfig)
		wantReason string
	}{
		{
			name:       "missing source sha256",
			mutateReq:  func(r *I2XGateRequest) { r.Report.SourceSHA256 = "" },
			wantReason: ReasonMissingSourceSHA256,
		},
		{
			name:       "malformed candidate sha256",
			mutateReq:  func(r *I2XGateRequest) { r.Report.CandidateSHA256 = "not-a-sha256" },
			wantReason: ReasonMissingCandidateSHA256,
		},
		{
			name:       "wrong X-App-ID",
			mutateReq:  func(r *I2XGateRequest) { r.AppID = "other-app" },
			wantReason: ReasonInvalidAppID,
		},
		{
			name:       "missing principal_id",
			mutateReq:  func(r *I2XGateRequest) { r.PrincipalID = "   " },
			wantReason: ReasonMissingPrincipalID,
		},
		{
			name:       "fake sample rejected",
			mutateReq:  func(r *I2XGateRequest) { r.SampleOrigin = "fixture" },
			wantReason: ReasonFakeSampleRejected,
		},
		{
			name:       "zero-value gate config fails closed",
			mutateCfg:  func(c *GateConfig) { *c = GateConfig{} },
			wantReason: ReasonInvalidGateConfig,
		},
		{
			name:       "NaN metric fails closed",
			mutateReq:  func(r *I2XGateRequest) { r.Report.MaskIoU = math.NaN() },
			wantReason: ReasonInvalidMetricRange,
		},
		{
			name:       "threshold version mismatch fails closed",
			mutateReq:  func(r *I2XGateRequest) { r.Report.ThresholdVersion = "i2x-thresholds/v0" },
			wantReason: ReasonThresholdVersionMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := makeHealthyRequest()
			cfg := DefaultGateConfig()
			if tc.mutateReq != nil {
				tc.mutateReq(&req)
			}
			if tc.mutateCfg != nil {
				tc.mutateCfg(&cfg)
			}
			dec := EvaluateI2XQualityGate(req, cfg)
			if dec.Passed || dec.CanAdopt() {
				t.Fatalf("expected fail-closed rejection, got: %+v", dec)
			}
			if !containsReason(dec.FailureReasons, tc.wantReason) {
				t.Fatalf("expected reason %s in %v", tc.wantReason, dec.FailureReasons)
			}
			if dec.RepairAction != RepairActionRejectAndReupload {
				t.Fatalf("expected %s, got %s", RepairActionRejectAndReupload, dec.RepairAction)
			}
		})
	}
}

// --- Billing Recovery State Machine test double (models remote platform-billing) ---

type fakePlatformBilling struct {
	mu sync.Mutex

	holdCalls   int
	spendCalls  int
	refundCalls int
	lookupCalls int

	holdsByReqKey map[string]*remoteHold
	holdsByID     map[string]*remoteHold

	// Fault injection hooks (executed under lock).
	failNextHoldAfterCommit    bool
	failNextHoldBeforeCommit   bool
	failNextSpendAfterCommit   bool
	failNextSpendBeforeCommit  bool
	failNextRefundAfterCommit  bool
	failNextRefundBeforeCommit bool
	failNextLookup             bool
}

type remoteHold struct {
	holdID     string
	requestKey string
	principal  string
	state      BillingState
	spendID    string
	refundID   string
	spendKey   string
	refundKey  string
}

func newFakePlatformBilling() *fakePlatformBilling {
	return &fakePlatformBilling{
		holdsByReqKey: make(map[string]*remoteHold),
		holdsByID:     make(map[string]*remoteHold),
	}
}

func (f *fakePlatformBilling) Hold(_ context.Context, req HoldRequest) (HoldResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if req.Scope.AppID != AppID || req.Scope.PrincipalID == "" {
		return HoldResponse{}, errors.New("unauthorized app_id or principal_id")
	}
	f.holdCalls++
	if f.failNextHoldBeforeCommit {
		f.failNextHoldBeforeCommit = false
		return HoldResponse{}, context.DeadlineExceeded
	}
	rk := req.Scope.PrincipalID + "|" + req.Scope.RequestKey
	h, exists := f.holdsByReqKey[rk]
	if !exists {
		id := "hold_" + req.Scope.RequestKey
		h = &remoteHold{
			holdID:     id,
			requestKey: req.Scope.RequestKey,
			principal:  req.Scope.PrincipalID,
			state:      BillingHeld,
		}
		f.holdsByReqKey[rk] = h
		f.holdsByID[id] = h
	}
	if f.failNextHoldAfterCommit {
		f.failNextHoldAfterCommit = false
		return HoldResponse{}, ErrTransportAmbiguous
	}
	return HoldResponse{HoldID: h.holdID}, nil
}

func (f *fakePlatformBilling) Spend(_ context.Context, req SpendRequest) (SpendResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spendCalls++
	if f.failNextSpendBeforeCommit {
		f.failNextSpendBeforeCommit = false
		return SpendResponse{}, context.DeadlineExceeded
	}
	h, ok := f.holdsByID[req.HoldID]
	if !ok {
		return SpendResponse{}, errors.New("hold not found")
	}
	if h.state == BillingRefunded {
		return SpendResponse{}, errors.New("hold already refunded")
	}
	if h.state == BillingSpent {
		if h.spendKey != req.IdempotencyKey {
			return SpendResponse{}, errors.New("double charge with different idempotency key")
		}
		return SpendResponse{SpendID: h.spendID}, nil
	}
	h.state = BillingSpent
	h.spendID = "spend_" + h.holdID
	h.spendKey = req.IdempotencyKey
	if f.failNextSpendAfterCommit {
		f.failNextSpendAfterCommit = false
		return SpendResponse{}, ErrTransportAmbiguous
	}
	return SpendResponse{SpendID: h.spendID}, nil
}

func (f *fakePlatformBilling) Refund(_ context.Context, req RefundRequest) (RefundResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refundCalls++
	if f.failNextRefundBeforeCommit {
		f.failNextRefundBeforeCommit = false
		return RefundResponse{}, context.DeadlineExceeded
	}
	h, ok := f.holdsByID[req.HoldID]
	if !ok {
		return RefundResponse{}, errors.New("hold not found")
	}
	if h.state == BillingSpent {
		return RefundResponse{}, errors.New("hold already spent")
	}
	if h.state == BillingRefunded {
		if h.refundKey != req.IdempotencyKey {
			return RefundResponse{}, errors.New("duplicate refund with different idempotency key")
		}
		return RefundResponse{RefundID: h.refundID}, nil
	}
	h.state = BillingRefunded
	h.refundID = "refund_" + h.holdID
	h.refundKey = req.IdempotencyKey
	if f.failNextRefundAfterCommit {
		f.failNextRefundAfterCommit = false
		return RefundResponse{}, ErrTransportAmbiguous
	}
	return RefundResponse{RefundID: h.refundID}, nil
}

func (f *fakePlatformBilling) LookupHold(_ context.Context, req LookupHoldRequest) (LookupHoldResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lookupCalls++
	if f.failNextLookup {
		f.failNextLookup = false
		return LookupHoldResponse{}, context.DeadlineExceeded
	}
	var h *remoteHold
	if req.HoldID != "" {
		h = f.holdsByID[req.HoldID]
	}
	if h == nil {
		h = f.holdsByReqKey[req.Scope.PrincipalID+"|"+req.Scope.RequestKey]
	}
	if h == nil {
		return LookupHoldResponse{State: BillingInit}, nil
	}
	return LookupHoldResponse{
		HoldID:   h.holdID,
		State:    h.state,
		SpendID:  h.spendID,
		RefundID: h.refundID,
	}, nil
}

func TestBillingRecovery_ProviderFailureBeforeDelivery_IdempotentFullRefund(t *testing.T) {
	ctx := context.Background()
	client := newFakePlatformBilling()
	sm, err := NewBillingRecoveryStateMachine(client)
	if err != nil {
		t.Fatalf("NewBillingRecoveryStateMachine: %v", err)
	}

	scope := BillingScope{AppID: AppID, PrincipalID: "usr_01", RequestKey: "req_prov_fail"}
	rec, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500)
	if err != nil {
		t.Fatalf("BeginHold: %v", err)
	}
	if rec.State != BillingHeld || rec.HoldID == "" {
		t.Fatalf("expected held state, got %+v", rec)
	}

	// Provider fails before delivering an image -> full refund.
	rec1, err := sm.HandleProviderFailureBeforeDelivery(ctx, scope, "provider_503")
	if err != nil {
		t.Fatalf("HandleProviderFailureBeforeDelivery: %v", err)
	}
	if rec1.State != BillingRefunded || rec1.RefundID == "" {
		t.Fatalf("expected BillingRefunded, got %+v", rec1)
	}
	if rec1.CandidateState == CandidateAdopted {
		t.Fatalf("provider failure must NEVER mark candidate adopted")
	}

	// Retry refund by same request_key -> idempotent, zero duplicate Refund RPC.
	rec2, err := sm.HandleProviderFailureBeforeDelivery(ctx, scope, "provider_503_retry")
	if err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	if rec2.State != BillingRefunded || rec2.RefundID != rec1.RefundID {
		t.Fatalf("expected identical refund record, got %+v", rec2)
	}
	if client.refundCalls != 1 || client.spendCalls != 0 {
		t.Fatalf("expected refundCalls=1 spendCalls=0, got refund=%d spend=%d", client.refundCalls, client.spendCalls)
	}
}

func TestBillingRecovery_QualityGateFailureAfterGeneration_PolicyAndNoFalseAdoption(t *testing.T) {
	ctx := context.Background()
	failedGate := EvaluateI2XQualityGate(func() I2XGateRequest {
		r := makeHealthyRequest()
		r.Report.LogoOCRSimilarity = 0.50
		return r
	}(), DefaultGateConfig())
	if failedGate.CanAdopt() {
		t.Fatalf("precondition failed: gate should fail")
	}

	t.Run("settle real provider spend when charged without adoption and zero double-charge on retry", func(t *testing.T) {
		client := newFakePlatformBilling()
		sm, _ := NewBillingRecoveryStateMachine(client)
		scope := BillingScope{AppID: AppID, PrincipalID: "usr_02", RequestKey: "req_gate_fail_spend"}

		if _, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500); err != nil {
			t.Fatalf("BeginHold: %v", err)
		}

		// Missing policy must fail closed without touching platform-billing.
		if _, err := sm.SettleAfterQualityGate(ctx, scope, failedGate, true, ""); !errors.Is(err, ErrInvalidBillingPolicy) {
			t.Fatalf("expected ErrInvalidBillingPolicy, got %v", err)
		}
		if client.spendCalls != 0 || client.refundCalls != 0 {
			t.Fatalf("invalid policy must not call platform-billing")
		}

		// Explicit settle_provider_spend with providerCharged=true -> spends hold, marks CandidateRejected.
		rec, err := sm.SettleAfterQualityGate(ctx, scope, failedGate, true, GateFailurePolicySettleProviderSpend)
		if err != nil {
			t.Fatalf("SettleAfterQualityGate: %v", err)
		}
		if rec.State != BillingSpent || rec.SpendID == "" {
			t.Fatalf("expected BillingSpent, got %+v", rec)
		}
		if rec.CandidateState != CandidateRejected {
			t.Fatalf("failed gate candidate MUST be rejected, never adopted; got %q", rec.CandidateState)
		}

		// Retry same failed settlement -> idempotent, zero double-charge.
		recRetry, err := sm.SettleAfterQualityGate(ctx, scope, failedGate, true, GateFailurePolicySettleProviderSpend)
		if err != nil {
			t.Fatalf("idempotent retry: %v", err)
		}
		if recRetry.CandidateState != CandidateRejected || client.spendCalls != 1 {
			t.Fatalf("expected CandidateRejected and spendCalls=1, got state=%q spendCalls=%d", recRetry.CandidateState, client.spendCalls)
		}

		// Attempt to re-submit a passing gate on the same already-rejected request_key -> refused!
		passedGate := EvaluateI2XQualityGate(makeHealthyRequest(), DefaultGateConfig())
		if _, err := sm.SettleAfterQualityGate(ctx, scope, passedGate, true, GateFailurePolicySettleProviderSpend); !errors.Is(err, ErrCandidateAlreadyRejected) {
			t.Fatalf("expected ErrCandidateAlreadyRejected, got %v", err)
		}
	})

	t.Run("refund under explicit platform-absorbed policy without adoption", func(t *testing.T) {
		client := newFakePlatformBilling()
		sm, _ := NewBillingRecoveryStateMachine(client)
		scope := BillingScope{AppID: AppID, PrincipalID: "usr_03", RequestKey: "req_gate_fail_refund"}

		if _, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500); err != nil {
			t.Fatalf("BeginHold: %v", err)
		}
		rec, err := sm.SettleAfterQualityGate(ctx, scope, failedGate, true, GateFailurePolicyRefundPlatformAbsorbed)
		if err != nil {
			t.Fatalf("SettleAfterQualityGate: %v", err)
		}
		if rec.State != BillingRefunded || rec.CandidateState != CandidateRejected {
			t.Fatalf("expected refunded + rejected, got %+v", rec)
		}
		if client.refundCalls != 1 || client.spendCalls != 0 {
			t.Fatalf("expected refundCalls=1 spendCalls=0, got refund=%d spend=%d", client.refundCalls, client.spendCalls)
		}
	})

	t.Run("settle policy refunds when provider did not charge", func(t *testing.T) {
		client := newFakePlatformBilling()
		sm, _ := NewBillingRecoveryStateMachine(client)
		scope := BillingScope{AppID: AppID, PrincipalID: "usr_04", RequestKey: "req_gate_fail_uncharged"}

		if _, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500); err != nil {
			t.Fatalf("BeginHold: %v", err)
		}
		rec, err := sm.SettleAfterQualityGate(ctx, scope, failedGate, false, GateFailurePolicySettleProviderSpend)
		if err != nil {
			t.Fatalf("SettleAfterQualityGate: %v", err)
		}
		if rec.State != BillingRefunded || rec.CandidateState != CandidateRejected {
			t.Fatalf("expected refunded + rejected, got %+v", rec)
		}
	})
}

func TestBillingRecovery_LostResponseAndCrashReconciliation(t *testing.T) {
	ctx := context.Background()
	passedGate := EvaluateI2XQualityGate(makeHealthyRequest(), DefaultGateConfig())
	failedGate := EvaluateI2XQualityGate(func() I2XGateRequest {
		r := makeHealthyRequest()
		r.Report.MaskIoU = 0.40
		return r
	}(), DefaultGateConfig())

	t.Run("lost Spend response after gate PASS reconciles to Spent+Adopted with zero double-charge", func(t *testing.T) {
		client := newFakePlatformBilling()
		sm, _ := NewBillingRecoveryStateMachine(client)
		scope := BillingScope{AppID: AppID, PrincipalID: "usr_crash_1", RequestKey: "req_lost_spend_pass"}

		held, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500)
		if err != nil {
			t.Fatalf("BeginHold: %v", err)
		}

		// Platform commits Spend, but response is lost on the wire.
		client.failNextSpendAfterCommit = true
		unknownRec, err := sm.SettleAfterQualityGate(ctx, scope, passedGate, true, GateFailurePolicySettleProviderSpend)
		if !errors.Is(err, ErrBillingUnknown) {
			t.Fatalf("expected ErrBillingUnknown, got %v", err)
		}
		if unknownRec.State != BillingUnknown || !unknownRec.ReconcileRequired {
			t.Fatalf("expected BillingUnknown + ReconcileRequired, got %+v", unknownRec)
		}
		if unknownRec.CandidateState == CandidateAdopted {
			t.Fatalf("MUST NOT mark candidate adopted while billing is unknown")
		}

		// Any retry before reconcile must be blocked with ErrReconcileRequired.
		if _, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500); !errors.Is(err, ErrReconcileRequired) {
			t.Fatalf("expected ErrReconcileRequired on BeginHold, got %v", err)
		}
		if _, err := sm.SettleAfterQualityGate(ctx, scope, passedGate, true, GateFailurePolicySettleProviderSpend); !errors.Is(err, ErrReconcileRequired) {
			t.Fatalf("expected ErrReconcileRequired on Settle, got %v", err)
		}
		if _, err := sm.HandleProviderFailureBeforeDelivery(ctx, scope, "retry"); !errors.Is(err, ErrReconcileRequired) {
			t.Fatalf("expected ErrReconcileRequired on Refund, got %v", err)
		}

		// Simulate process restart: restore persisted (principal_id, request_key, hold_id) into a fresh SM.
		restartedSM, _ := NewBillingRecoveryStateMachine(client)
		if err := restartedSM.RestoreRecord(unknownRec); err != nil {
			t.Fatalf("RestoreRecord: %v", err)
		}

		reconciled, err := restartedSM.Reconcile(ctx, scope)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.State != BillingSpent || reconciled.HoldID != held.HoldID || reconciled.SpendID == "" {
			t.Fatalf("expected reconciled BillingSpent with original hold_id, got %+v", reconciled)
		}
		if reconciled.CandidateState != CandidateAdopted || reconciled.ReconcileRequired {
			t.Fatalf("expected CandidateAdopted after reconciled spend on passed gate, got %+v", reconciled)
		}
		if client.spendCalls != 1 {
			t.Fatalf("expected exactly 1 spend call (zero double-charge), got %d", client.spendCalls)
		}
	})

	t.Run("lost Spend response after gate FAIL reconciles to Spent+Rejected (ZERO false adoption)", func(t *testing.T) {
		client := newFakePlatformBilling()
		sm, _ := NewBillingRecoveryStateMachine(client)
		scope := BillingScope{AppID: AppID, PrincipalID: "usr_crash_2", RequestKey: "req_lost_spend_fail"}

		if _, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500); err != nil {
			t.Fatalf("BeginHold: %v", err)
		}
		client.failNextSpendAfterCommit = true
		unknownRec, err := sm.SettleAfterQualityGate(ctx, scope, failedGate, true, GateFailurePolicySettleProviderSpend)
		if !errors.Is(err, ErrBillingUnknown) {
			t.Fatalf("expected ErrBillingUnknown, got %v", err)
		}
		if unknownRec.CandidateState != CandidateRejected {
			t.Fatalf("expected CandidateRejected, got %q", unknownRec.CandidateState)
		}

		reconciled, err := sm.Reconcile(ctx, scope)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.State != BillingSpent || reconciled.CandidateState != CandidateRejected {
			t.Fatalf("reconciled failed-gate spend MUST stay CandidateRejected, got %+v", reconciled)
		}
	})

	t.Run("lost Refund before commit reconciles back to Held and completes idempotent Refund on same hold_id", func(t *testing.T) {
		client := newFakePlatformBilling()
		sm, _ := NewBillingRecoveryStateMachine(client)
		scope := BillingScope{AppID: AppID, PrincipalID: "usr_crash_3", RequestKey: "req_lost_refund"}

		held, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500)
		if err != nil {
			t.Fatalf("BeginHold: %v", err)
		}
		client.failNextRefundBeforeCommit = true
		if _, err := sm.HandleProviderFailureBeforeDelivery(ctx, scope, "provider_timeout"); !errors.Is(err, ErrBillingUnknown) {
			t.Fatalf("expected ErrBillingUnknown, got %v", err)
		}

		// Reconcile sees hold is still BillingHeld on platform-billing.
		reconciled, err := sm.Reconcile(ctx, scope)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.State != BillingHeld || reconciled.HoldID != held.HoldID || reconciled.ReconcileRequired {
			t.Fatalf("expected reconciled BillingHeld, got %+v", reconciled)
		}

		// Now safely finish refund on exact same hold_id.
		refunded, err := sm.HandleProviderFailureBeforeDelivery(ctx, scope, "provider_timeout")
		if err != nil {
			t.Fatalf("retry refund after reconcile: %v", err)
		}
		if refunded.State != BillingRefunded || refunded.HoldID != held.HoldID || refunded.CandidateState == CandidateAdopted {
			t.Fatalf("expected BillingRefunded with original hold_id, got %+v", refunded)
		}
	})

	t.Run("lost Hold response after commit reconciles hold_id without creating a second hold", func(t *testing.T) {
		client := newFakePlatformBilling()
		sm, _ := NewBillingRecoveryStateMachine(client)
		scope := BillingScope{AppID: AppID, PrincipalID: "usr_crash_4", RequestKey: "req_lost_hold"}

		client.failNextHoldAfterCommit = true
		if _, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500); !errors.Is(err, ErrBillingUnknown) {
			t.Fatalf("expected ErrBillingUnknown, got %v", err)
		}

		reconciled, err := sm.Reconcile(ctx, scope)
		if err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		if reconciled.State != BillingHeld || reconciled.HoldID == "" {
			t.Fatalf("expected recovered HoldID and BillingHeld, got %+v", reconciled)
		}
		// Calling BeginHold again returns the exact recovered hold without a second Hold RPC.
		again, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500)
		if err != nil {
			t.Fatalf("BeginHold after reconcile: %v", err)
		}
		if again.HoldID != reconciled.HoldID || client.holdCalls != 1 {
			t.Fatalf("expected single hold call and identical hold_id, got calls=%d id=%s", client.holdCalls, again.HoldID)
		}
	})
}

func TestBillingRecovery_ConcurrentRetryStorm_ZeroDoubleChargeAndRaceFree(t *testing.T) {
	ctx := context.Background()
	client := newFakePlatformBilling()
	sm, err := NewBillingRecoveryStateMachine(client)
	if err != nil {
		t.Fatalf("NewBillingRecoveryStateMachine: %v", err)
	}

	passedGate := EvaluateI2XQualityGate(makeHealthyRequest(), DefaultGateConfig())
	scope := BillingScope{AppID: AppID, PrincipalID: "usr_race_01", RequestKey: "req_race_storm"}

	const workers = 32
	var wg sync.WaitGroup
	var holdErrors atomic.Int32
	var settleErrors atomic.Int32

	// Phase 1: 32 concurrent BeginHold retries on the same (principal_id, request_key).
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			rec, err := sm.BeginHold(ctx, scope, "bg_replace", "pricing-2026-09", 500)
			if err != nil || rec.State != BillingHeld || rec.HoldID == "" {
				holdErrors.Add(1)
			}
		}()
	}
	wg.Wait()
	if holdErrors.Load() != 0 {
		t.Fatalf("expected 0 hold errors across concurrent retries, got %d", holdErrors.Load())
	}

	// Phase 2: 32 concurrent SettleAfterQualityGate retries on the same hold.
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			rec, err := sm.SettleAfterQualityGate(ctx, scope, passedGate, true, GateFailurePolicySettleProviderSpend)
			if err != nil || rec.State != BillingSpent || rec.CandidateState != CandidateAdopted {
				settleErrors.Add(1)
			}
		}()
	}
	wg.Wait()
	if settleErrors.Load() != 0 {
		t.Fatalf("expected 0 settle errors across concurrent retries, got %d", settleErrors.Load())
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if client.holdCalls != 1 {
		t.Fatalf("expected exactly 1 Hold RPC under retry storm, got %d", client.holdCalls)
	}
	if client.spendCalls != 1 {
		t.Fatalf("expected exactly 1 Spend RPC under retry storm (zero double-charge), got %d", client.spendCalls)
	}
	if client.refundCalls != 0 {
		t.Fatalf("expected 0 Refund RPCs, got %d", client.refundCalls)
	}
}
