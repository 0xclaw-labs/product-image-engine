package beautyedit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bianjiefilm/product-image-engine/server/internal/fidelitygate"
)

// --- Test doubles ---

type stubBillingClient struct {
	mu          sync.Mutex
	holdCalls   int
	spendCalls  int
	refundCalls int
}

func (s *stubBillingClient) Hold(_ context.Context, req fidelitygate.HoldRequest) (fidelitygate.HoldResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.holdCalls++
	return fidelitygate.HoldResponse{HoldID: fmt.Sprintf("hold-%s-%d", req.Scope.RequestKey, s.holdCalls)}, nil
}

func (s *stubBillingClient) Spend(_ context.Context, req fidelitygate.SpendRequest) (fidelitygate.SpendResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spendCalls++
	return fidelitygate.SpendResponse{SpendID: fmt.Sprintf("spend-%s-%d", req.HoldID, s.spendCalls)}, nil
}

func (s *stubBillingClient) Refund(_ context.Context, req fidelitygate.RefundRequest) (fidelitygate.RefundResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refundCalls++
	return fidelitygate.RefundResponse{RefundID: fmt.Sprintf("refund-%s-%d", req.HoldID, s.refundCalls)}, nil
}

func (s *stubBillingClient) LookupHold(_ context.Context, req fidelitygate.LookupHoldRequest) (fidelitygate.LookupHoldResponse, error) {
	return fidelitygate.LookupHoldResponse{HoldID: req.HoldID, State: fidelitygate.BillingHeld}, nil
}

func (s *stubBillingClient) counts() (holds, spends, refunds int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.holdCalls, s.spendCalls, s.refundCalls
}

type stubEditProvider struct {
	calls atomic.Int64
	fn    func(ctx context.Context, req ProviderEditRequest) (ProviderEditResponse, error)
}

func (p *stubEditProvider) Edit(ctx context.Context, req ProviderEditRequest) (ProviderEditResponse, error) {
	p.calls.Add(1)
	return p.fn(ctx, req)
}

type stubAuthLookup struct {
	records map[string]PortraitAuthRecord
}

func (l *stubAuthLookup) LookupPortraitAuth(_ context.Context, principalID, ref string) (PortraitAuthRecord, error) {
	rec, ok := l.records[ref]
	if !ok {
		return PortraitAuthRecord{}, fmt.Errorf("auth ref %q not found", ref)
	}
	if rec.PrincipalID == "" {
		rec.PrincipalID = principalID
	}
	return rec, nil
}

// --- PNG fixture helpers ---

// makePortraitPNG renders a 32x32 deterministic synthetic portrait test canvas:
//   - y in [0,15], x in [8,23]: face/skin region
//   - y in [8,13], x in [11,20]: real makeup region (inside face)
//   - everything else: background region
func makePortraitPNG(bgColor, faceColor, makeupColor color.NRGBA) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			c := bgColor
			if x >= 8 && x <= 23 && y >= 0 && y <= 15 {
				c = faceColor
			}
			if x >= 11 && x <= 20 && y >= 8 && y <= 13 {
				c = makeupColor
			}
			img.SetNRGBA(x, y, c)
		}
	}
	return encodePNG(img)
}

func makeRegionMasks() (facePreservedMask, bgTargetMask, makeupMask, creativePreservedMask []byte) {
	faceMaskImg := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	bgMaskImg := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	makeupMaskImg := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	creativePresImg := image.NewNRGBA(image.Rect(0, 0, 32, 32))

	opaque := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	clear := color.NRGBA{R: 0, G: 0, B: 0, A: 0}

	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			inFace := x >= 8 && x <= 23 && y >= 0 && y <= 15
			inMakeup := x >= 11 && x <= 20 && y >= 8 && y <= 13

			if inFace {
				faceMaskImg.SetNRGBA(x, y, opaque)
				bgMaskImg.SetNRGBA(x, y, clear)
			} else {
				faceMaskImg.SetNRGBA(x, y, clear)
				bgMaskImg.SetNRGBA(x, y, opaque)
			}

			if inMakeup {
				makeupMaskImg.SetNRGBA(x, y, opaque)
				creativePresImg.SetNRGBA(x, y, clear)
			} else {
				makeupMaskImg.SetNRGBA(x, y, clear)
				creativePresImg.SetNRGBA(x, y, opaque)
			}
		}
	}
	return encodePNG(faceMaskImg), encodePNG(bgMaskImg), encodePNG(makeupMaskImg), encodePNG(creativePresImg)
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func newVerifiedRegistry(t *testing.T) *AdapterRegistry {
	t.Helper()
	reg := NewAdapterRegistry()
	// Adapter 1: full_redraw provider verified for BOTH fidelity_background_edit
	// and creative_makeup_demo, explicitly requiring post-edit region verification.
	err := reg.Register(AdapterSpec{
		ProviderID:                 "seedream-provider",
		ModelID:                    "seedream-edit-v2",
		ModelAdapterVersion:        "seedream-edit-v2@adapter-1.2",
		AdapterEditCapability:      AdapterCapFullRedraw,
		ClaimsPixelLockedLocalEdit: false,
		Verified:                   true,
		KnownLimitations:           []string{"full_canvas_redraw_requires_post_edit_face_and_makeup_check"},
		VerifiedModes: []AdapterModeProfile{
			{
				Mode:                   ModeFidelityBackgroundEdit,
				AllowedModifyRegions:   []RegionKind{RegionBackground, RegionComposition},
				PreservedRegions:       []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
				ReferenceConditions:    []string{"authorized_front_portrait", "clear_face_lighting"},
				KnownLimitations:       []string{"hair_strand_boundary_sensitive"},
				RequiresPostEditVerify: true,
			},
			{
				Mode:                   ModeCreativeMakeupDemo,
				AllowedModifyRegions:   []RegionKind{RegionMakeupRegion, RegionEyeMakeup, RegionLipMakeup, RegionBlushContour},
				PreservedRegions:       []RegionKind{RegionFaceIdentity, RegionHairClothing, RegionBackground},
				ReferenceConditions:    []string{"authorized_front_portrait", "creative_demo_disclosure_enabled"},
				KnownLimitations:       []string{"creative_illustration_only_not_real_service_proof"},
				RequiresPostEditVerify: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("register verified full_redraw adapter: %v", err)
	}

	// Adapter 2: pixel_locked_mask adapter verified ONLY for fidelity_background_edit.
	err = reg.Register(AdapterSpec{
		ProviderID:                 "inpaint-provider",
		ModelID:                    "mask-inpaint-v1",
		ModelAdapterVersion:        "mask-inpaint-v1@adapter-1.0",
		AdapterEditCapability:      AdapterCapPixelLockedMask,
		ClaimsPixelLockedLocalEdit: true,
		Verified:                   true,
		VerifiedModes: []AdapterModeProfile{
			{
				Mode:                   ModeFidelityBackgroundEdit,
				AllowedModifyRegions:   []RegionKind{RegionBackground},
				PreservedRegions:       []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
				ReferenceConditions:    []string{"binary_subject_mask"},
				KnownLimitations:       []string{"background_only_no_makeup_edit"},
				RequiresPostEditVerify: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("register verified pixel_locked adapter: %v", err)
	}
	return reg
}

func validPortraitAuth(principalID string, scope PortraitUsageScope, now time.Time) PortraitAuthRecord {
	return PortraitAuthRecord{
		PortraitAuthRef:            "pauth-ref-2026-001",
		PrincipalID:                principalID,
		SubjectID:                  "subject-model-88",
		IssuerService:              "public-ai-portrait-consent",
		UsageScope:                 scope,
		ProductImagePermissionOnly: false,
		GrantedAt:                  now.Add(-time.Hour),
		ExpiresAt:                  now.Add(24 * time.Hour),
		Revoked:                    false,
	}
}

func makePassingFidelityReport(srcHash, candHash string, nonTargetDrift, makeupDrift, targetChanged float64) BeautyEditQualityReport {
	return BeautyEditQualityReport{
		SourceSHA256:             srcHash,
		CandidateSHA256:          candHash,
		EvaluatorVersion:         DefaultBeautyEvaluatorVersion,
		ThresholdVersion:         DefaultBeautyThresholdVersion,
		OverallSimilarityPercent: 94.0,
		Identity: IdentityFeatureCheck{
			Evaluated:           true,
			FaceIdentityScore:   0.96,
			FacialGeometryDrift: 0.02,
		},
		RegionInvariance: RegionInvarianceCheck{
			Evaluated:                true,
			PostEditPixelVerified:    true,
			SkinTextureDrift:         0.015,
			MakeupRegionDrift:        makeupDrift,
			NonTargetPixelDrift:      nonTargetDrift,
			TargetRegionChangedRatio: targetChanged,
			UnapprovedRegionAltered:  false,
		},
		ScopeMatch: ScopeMatchCheck{
			Evaluated:                true,
			DeclaredAllowedRegions:   []RegionKind{RegionBackground, RegionComposition},
			DeclaredPreservedRegions: []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
			ObservedModifiedRegions:  []RegionKind{RegionBackground},
		},
		UsageDisclosure: UsageDisclosureCheck{
			Evaluated:          true,
			UsageType:          UsageRealServiceProof,
			IsRealServiceProof: true,
		},
	}
}

func makePassingCreativeReport(srcHash, candHash string, nonTargetDrift, makeupDrift, targetChanged float64) BeautyEditQualityReport {
	return BeautyEditQualityReport{
		SourceSHA256:             srcHash,
		CandidateSHA256:          candHash,
		EvaluatorVersion:         DefaultBeautyEvaluatorVersion,
		ThresholdVersion:         DefaultBeautyThresholdVersion,
		OverallSimilarityPercent: 86.0,
		Identity: IdentityFeatureCheck{
			Evaluated:           true,
			FaceIdentityScore:   0.93,
			FacialGeometryDrift: 0.03,
		},
		RegionInvariance: RegionInvarianceCheck{
			Evaluated:                true,
			PostEditPixelVerified:    true,
			SkinTextureDrift:         0.02,
			MakeupRegionDrift:        makeupDrift,
			NonTargetPixelDrift:      nonTargetDrift,
			TargetRegionChangedRatio: targetChanged,
			UnapprovedRegionAltered:  false,
		},
		ScopeMatch: ScopeMatchCheck{
			Evaluated:                true,
			DeclaredAllowedRegions:   []RegionKind{RegionMakeupRegion, RegionLipMakeup},
			DeclaredPreservedRegions: []RegionKind{RegionFaceIdentity, RegionHairClothing, RegionBackground},
			ObservedModifiedRegions:  []RegionKind{RegionMakeupRegion},
		},
		UsageDisclosure: UsageDisclosureCheck{
			Evaluated:               true,
			UsageType:               UsageCreativeDemo,
			IsRealServiceProof:      false,
			CreativeDisclosureLabel: DefaultCreativeDisclosureLabel,
		},
	}
}

// --- Tests ---

func TestPositiveFidelityBackgroundEditAndHandoff(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	srcPNG := makePortraitPNG(
		color.NRGBA{R: 220, G: 220, B: 220, A: 255}, // studio gray bg
		color.NRGBA{R: 235, G: 195, B: 170, A: 255}, // skin
		color.NRGBA{R: 185, G: 65, B: 90, A: 255},   // real makeup
	)
	candPNG := makePortraitPNG(
		color.NRGBA{R: 35, G: 55, B: 95, A: 255},    // replaced deep studio blue bg
		color.NRGBA{R: 235, G: 195, B: 170, A: 255}, // skin preserved identically
		color.NRGBA{R: 185, G: 65, B: 90, A: 255},   // real makeup preserved identically
	)
	facePresMask, bgTargetMask, makeupMask, _ := makeRegionMasks()

	meas, err := MeasurePNGPortraitRegions(srcPNG, candPNG, facePresMask, bgTargetMask, makeupMask)
	if err != nil {
		t.Fatalf("MeasurePNGPortraitRegions: %v", err)
	}
	if meas.NonTargetPixelDrift != 0 || meas.MakeupRegionDrift != 0 || meas.TargetRegionChangedRatio < 0.99 || meas.EchoCopyDetected {
		t.Fatalf("unexpected pixel measurement: %+v", meas)
	}

	billingClient := &stubBillingClient{}
	billingSM, err := fidelitygate.NewBillingRecoveryStateMachine(billingClient)
	if err != nil {
		t.Fatalf("NewBillingRecoveryStateMachine: %v", err)
	}
	reg := newVerifiedRegistry(t)
	provider := &stubEditProvider{
		fn: func(_ context.Context, req ProviderEditRequest) (ProviderEditResponse, error) {
			return ProviderEditResponse{
				TaskRef:            "task-fidelity-bg-001",
				OutputAssetVersion: "asset-ver-bg-001",
				PreviewRef:         "oss://preview/beauty/bg-001.png",
				OutputSHA256:       meas.CandidateSHA256,
				ProviderCharged:    true,
				ActualCostFen:      350,
				QualityReport:      makePassingFidelityReport(meas.SourceSHA256, meas.CandidateSHA256, meas.NonTargetPixelDrift, meas.MakeupRegionDrift, meas.TargetRegionChangedRatio),
			}, nil
		},
	}

	auth := validPortraitAuth("principal-mua-1", ScopePortraitRealServiceEdit, now)
	coord, err := NewCoordinator(CoordinatorConfig{
		Registry:   reg,
		Provider:   provider,
		AuthLookup: &stubAuthLookup{records: map[string]PortraitAuthRecord{auth.PortraitAuthRef: auth}},
		Billing:    billingSM,
		Now:        func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	sess, err := coord.CreateSession(CreateSessionRequest{
		SessionID:         "sess-fidelity-1",
		PrincipalID:       "principal-mua-1",
		InputAssetID:      "asset-portrait-1",
		InputAssetVersion: "asset-ver-orig-0",
		InputPreviewRef:   "oss://preview/beauty/orig-0.png",
		InputSHA256:       meas.SourceSHA256,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.CurrentRevision != 1 || sess.ActiveVersionID != "asset-ver-orig-0" {
		t.Fatalf("unexpected initial session: %+v", sess)
	}

	res, err := coord.SubmitEdit(context.Background(), SubmitEditRequest{
		CommandID:            "cmd-fidelity-001",
		SessionID:            "sess-fidelity-1",
		Scope:                fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-mua-1", RequestKey: "req-fidelity-001"},
		QuoteID:              "quote-001",
		PricingVersion:       "pricing-v1",
		EstimatedCostFen:     350,
		GateFailurePolicy:    fidelitygate.GateFailurePolicyRefundPlatformAbsorbed,
		Mode:                 ModeFidelityBackgroundEdit,
		UsageType:            UsageRealServiceProof,
		IsRealServiceProof:   true,
		ModelAdapterVersion:  "seedream-edit-v2@adapter-1.2",
		AllowedModifyRegions: []RegionKind{RegionBackground, RegionComposition},
		PreservedRegions:     []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
		PortraitAuthRef:      auth.PortraitAuthRef,
		SampleOrigin:         fidelitygate.SampleOriginReal,
		Prompt:               "Replace studio background with warm soft cream backdrop",
	})
	if err != nil {
		t.Fatalf("SubmitEdit fidelity failed: %v", err)
	}
	if !res.GateDecision.CanAdopt() || !res.GateDecision.CanClaimRealService {
		t.Fatalf("expected passing real-service gate decision, got %+v", res.GateDecision)
	}
	if res.CandidateVersion.AdapterEditCapability != AdapterCapFullRedraw {
		t.Fatalf("expected honest full_redraw capability, got %q", res.CandidateVersion.AdapterEditCapability)
	}
	if res.BillingState != fidelitygate.BillingSpent {
		t.Fatalf("expected billing spent, got %q", res.BillingState)
	}
	holds, spends, refunds := billingClient.counts()
	if holds != 1 || spends != 1 || refunds != 0 {
		t.Fatalf("unexpected billing counts: holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}

	// Source/candidate comparison before explicit adoption.
	cmp, err := coord.CompareVersions("principal-mua-1", "sess-fidelity-1", res.CandidateVersion.VersionID)
	if err != nil {
		t.Fatalf("CompareVersions: %v", err)
	}
	if !cmp.CanAdopt || cmp.ActiveVersionID != "asset-ver-orig-0" || cmp.OutputAssetVersion != "asset-ver-bg-001" {
		t.Fatalf("unexpected comparison before adopt: %+v", cmp)
	}

	// Explicit adoption bumps revision 1 -> 2.
	adopted, err := coord.AdoptVersion(AdoptVersionRequest{
		SessionID:          "sess-fidelity-1",
		PrincipalID:        "principal-mua-1",
		CandidateVersionID: "asset-ver-bg-001",
		ExpectedRevision:   1,
	})
	if err != nil {
		t.Fatalf("AdoptVersion: %v", err)
	}
	if adopted.CurrentRevision != 2 || adopted.ActiveVersionID != "asset-ver-bg-001" {
		t.Fatalf("unexpected adopted snapshot: %+v", adopted)
	}

	// Candidate handoff to shuhai with real_service_proof succeeds with all contract fields.
	pkg, err := coord.BuildHandoffPackage(HandoffRequest{
		SessionID:     "sess-fidelity-1",
		PrincipalID:   "principal-mua-1",
		ExportPurpose: ExportPurposeRealServiceProof,
		Consumer:      ConsumerShuhai,
	})
	if err != nil {
		t.Fatalf("BuildHandoffPackage: %v", err)
	}
	if !slices.Contains(pkg.VerifiedEditModes, ModeFidelityBackgroundEdit) ||
		pkg.InputAssetVersion != "asset-ver-orig-0" ||
		pkg.OutputAssetVersion != "asset-ver-bg-001" ||
		pkg.ModelAdapterVersion != "seedream-edit-v2@adapter-1.2" ||
		pkg.UsageType != UsageRealServiceProof ||
		!pkg.IsRealServiceProof ||
		pkg.TaskRef != "task-fidelity-bg-001" ||
		pkg.PreviewRef != "oss://preview/beauty/bg-001.png" ||
		pkg.SelectedResultRef != "oss://preview/beauty/bg-001.png" ||
		!pkg.AutoPublishDisabled || !pkg.CRMReceptionDisabled || !pkg.SecondBillingDisabled {
		t.Fatalf("incomplete handoff contract fields: %+v", pkg)
	}
	if !errors.Is(pkg.AttemptSocialPublish(), ErrSocialPublishOutOfScope) {
		t.Fatalf("expected ErrSocialPublishOutOfScope")
	}
	if !errors.Is(pkg.AttemptCRMReception(), ErrCRMReceptionOutOfScope) {
		t.Fatalf("expected ErrCRMReceptionOutOfScope")
	}
}

func TestPositiveCreativeMakeupDemoAndExportSeparation(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	srcPNG := makePortraitPNG(
		color.NRGBA{R: 220, G: 220, B: 220, A: 255},
		color.NRGBA{R: 235, G: 195, B: 170, A: 255},
		color.NRGBA{R: 185, G: 65, B: 90, A: 255},
	)
	candPNG := makePortraitPNG(
		color.NRGBA{R: 220, G: 220, B: 220, A: 255}, // bg preserved
		color.NRGBA{R: 235, G: 195, B: 170, A: 255}, // skin preserved
		color.NRGBA{R: 110, G: 25, B: 165, A: 255},  // creative purple editorial makeup demo
	)
	_, _, makeupMask, creativePresMask := makeRegionMasks()

	meas, err := MeasurePNGPortraitRegions(srcPNG, candPNG, creativePresMask, makeupMask, makeupMask)
	if err != nil {
		t.Fatalf("MeasurePNGPortraitRegions creative: %v", err)
	}
	if meas.NonTargetPixelDrift != 0 || meas.TargetRegionChangedRatio < 0.99 || meas.MakeupRegionDrift < 0.2 {
		t.Fatalf("unexpected creative pixel measurement: %+v", meas)
	}

	reg := newVerifiedRegistry(t)
	provider := &stubEditProvider{
		fn: func(_ context.Context, req ProviderEditRequest) (ProviderEditResponse, error) {
			return ProviderEditResponse{
				TaskRef:            "task-creative-makeup-001",
				OutputAssetVersion: "asset-ver-makeup-001",
				PreviewRef:         "oss://preview/beauty/makeup-demo-001.png",
				OutputSHA256:       meas.CandidateSHA256,
				ProviderCharged:    true,
				ActualCostFen:      400,
				QualityReport:      makePassingCreativeReport(meas.SourceSHA256, meas.CandidateSHA256, meas.NonTargetPixelDrift, meas.MakeupRegionDrift, meas.TargetRegionChangedRatio),
			}, nil
		},
	}

	auth := validPortraitAuth("principal-mua-2", ScopePortraitCreativeMakeupDemo, now)
	coord, err := NewCoordinator(CoordinatorConfig{
		Registry: reg,
		Provider: provider,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	_, err = coord.CreateSession(CreateSessionRequest{
		SessionID:         "sess-creative-1",
		PrincipalID:       "principal-mua-2",
		InputAssetID:      "asset-portrait-2",
		InputAssetVersion: "asset-ver-orig-2",
		InputPreviewRef:   "oss://preview/beauty/orig-2.png",
		InputSHA256:       meas.SourceSHA256,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Negative pre-flight check: creative_makeup_demo claiming is_real_service_proof=true MUST fail before provider call.
	_, err = coord.SubmitEdit(context.Background(), SubmitEditRequest{
		CommandID:               "cmd-creative-bad-claim",
		SessionID:               "sess-creative-1",
		Scope:                   fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-mua-2", RequestKey: "req-c-bad"},
		Mode:                    ModeCreativeMakeupDemo,
		UsageType:               UsageRealServiceProof,
		IsRealServiceProof:      true,
		CreativeDisclosureLabel: DefaultCreativeDisclosureLabel,
		ModelAdapterVersion:     "seedream-edit-v2@adapter-1.2",
		AllowedModifyRegions:    []RegionKind{RegionMakeupRegion, RegionLipMakeup},
		PreservedRegions:        []RegionKind{RegionFaceIdentity, RegionHairClothing, RegionBackground},
		PortraitAuth:            &auth,
		PortraitAuthRef:         auth.PortraitAuthRef,
		SampleOrigin:            fidelitygate.SampleOriginReal,
	})
	if !errors.Is(err, ErrCreativeDemoExportAsRealProofForbidden) {
		t.Fatalf("expected ErrCreativeDemoExportAsRealProofForbidden, got %v", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("expected 0 provider calls on blocked creative real-proof claim")
	}

	// Positive path: properly tagged creative_makeup_demo.
	res, err := coord.SubmitEdit(context.Background(), SubmitEditRequest{
		CommandID:               "cmd-creative-001",
		SessionID:               "sess-creative-1",
		Scope:                   fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-mua-2", RequestKey: "req-c-1"},
		Mode:                    ModeCreativeMakeupDemo,
		UsageType:               UsageCreativeDemo,
		IsRealServiceProof:      false,
		CreativeDisclosureLabel: DefaultCreativeDisclosureLabel,
		ModelAdapterVersion:     "seedream-edit-v2@adapter-1.2",
		AllowedModifyRegions:    []RegionKind{RegionMakeupRegion, RegionLipMakeup},
		PreservedRegions:        []RegionKind{RegionFaceIdentity, RegionHairClothing, RegionBackground},
		PortraitAuth:            &auth,
		PortraitAuthRef:         auth.PortraitAuthRef,
		SampleOrigin:            fidelitygate.SampleOriginReal,
	})
	if err != nil {
		t.Fatalf("SubmitEdit creative failed: %v", err)
	}
	if !res.GateDecision.CanAdopt() || res.GateDecision.CanClaimRealService {
		t.Fatalf("expected adoptable creative demo with CanClaimRealService=false, got %+v", res.GateDecision)
	}
	if res.CandidateVersion.UsageType != UsageCreativeDemo || res.CandidateVersion.IsRealServiceProof {
		t.Fatalf("creative version must be tagged usage_type=creative_demo, is_real_service_proof=false: %+v", res.CandidateVersion)
	}

	if _, err := coord.AdoptVersion(AdoptVersionRequest{
		SessionID:          "sess-creative-1",
		PrincipalID:        "principal-mua-2",
		CandidateVersionID: res.CandidateVersion.VersionID,
		ExpectedRevision:   1,
	}); err != nil {
		t.Fatalf("AdoptVersion creative: %v", err)
	}

	// Handoff as creative_demo succeeds for Bot consumer.
	pkg, err := coord.BuildHandoffPackage(HandoffRequest{
		SessionID:     "sess-creative-1",
		PrincipalID:   "principal-mua-2",
		ExportPurpose: ExportPurposeCreativeDemo,
		Consumer:      ConsumerBot,
	})
	if err != nil {
		t.Fatalf("BuildHandoffPackage creative_demo: %v", err)
	}
	if pkg.UsageType != UsageCreativeDemo || pkg.IsRealServiceProof || pkg.CreativeDisclosureLabel != DefaultCreativeDisclosureLabel {
		t.Fatalf("unexpected creative handoff metadata: %+v", pkg)
	}

	// Exporting creative_makeup_demo as real_service_proof MUST be strictly forbidden!
	_, err = coord.BuildHandoffPackage(HandoffRequest{
		SessionID:     "sess-creative-1",
		PrincipalID:   "principal-mua-2",
		ExportPurpose: ExportPurposeRealServiceProof,
		Consumer:      ConsumerMatrixCandidate,
	})
	if !errors.Is(err, ErrCreativeDemoExportAsRealProofForbidden) {
		t.Fatalf("expected ErrCreativeDemoExportAsRealProofForbidden when exporting creative demo as real proof, got %v", err)
	}
}

func TestIndependentPortraitAuthorizationEnforcement(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	principal := "principal-merchant-9"
	srcHash := sha256Hex([]byte("portrait-source-bytes"))

	billingClient := &stubBillingClient{}
	billingSM, _ := fidelitygate.NewBillingRecoveryStateMachine(billingClient)
	provider := &stubEditProvider{
		fn: func(_ context.Context, _ ProviderEditRequest) (ProviderEditResponse, error) {
			t.Fatalf("provider MUST NOT be called when portrait authorization fails")
			return ProviderEditResponse{}, nil
		},
	}
	coord, err := NewCoordinator(CoordinatorConfig{
		Registry: newVerifiedRegistry(t),
		Provider: provider,
		Billing:  billingSM,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	_, err = coord.CreateSession(CreateSessionRequest{
		SessionID:         "sess-auth-1",
		PrincipalID:       principal,
		InputAssetID:      "asset-p-9",
		InputAssetVersion: "ver-orig-9",
		InputPreviewRef:   "oss://preview/orig-9.png",
		InputSHA256:       srcHash,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	baseReq := func(cmd string, auth *PortraitAuthRecord, ref string) SubmitEditRequest {
		return SubmitEditRequest{
			CommandID:            cmd,
			SessionID:            "sess-auth-1",
			Scope:                fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: principal, RequestKey: "req-" + cmd},
			QuoteID:              "q-1",
			PricingVersion:       "p-v1",
			EstimatedCostFen:     300,
			GateFailurePolicy:    fidelitygate.GateFailurePolicyRefundPlatformAbsorbed,
			Mode:                 ModeFidelityBackgroundEdit,
			UsageType:            UsageRealServiceProof,
			IsRealServiceProof:   true,
			ModelAdapterVersion:  "seedream-edit-v2@adapter-1.2",
			AllowedModifyRegions: []RegionKind{RegionBackground},
			PreservedRegions:     []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
			PortraitAuthRef:      ref,
			PortraitAuth:         auth,
			SampleOrigin:         fidelitygate.SampleOriginReal,
		}
	}

	productOnly := validPortraitAuth(principal, ScopeProductImageOnly, now)
	productOnly.ProductImagePermissionOnly = true

	expired := validPortraitAuth(principal, ScopePortraitRealServiceEdit, now)
	expired.ExpiresAt = now.Add(-time.Minute)

	revoked := validPortraitAuth(principal, ScopePortraitRealServiceEdit, now)
	revoked.Revoked = true

	wrongScope := validPortraitAuth(principal, ScopePortraitCreativeMakeupDemo, now)

	copiedCred := validPortraitAuth(principal, ScopePortraitRealServiceEdit, now)
	copiedCred.EmbeddedCredential = "sk-live-long-lived-secret-token"

	cases := []struct {
		name    string
		auth    *PortraitAuthRecord
		ref     string
		wantErr error
	}{
		{"missing_portrait_auth", nil, "", ErrPortraitAuthRequired},
		{"product_image_permission_only", &productOnly, productOnly.PortraitAuthRef, ErrPortraitAuthRequired},
		{"expired_portrait_auth", &expired, expired.PortraitAuthRef, ErrPortraitAuthRequired},
		{"revoked_portrait_auth", &revoked, revoked.PortraitAuthRef, ErrPortraitAuthRequired},
		{"wrong_usage_scope_for_mode", &wrongScope, wrongScope.PortraitAuthRef, ErrPortraitAuthRequired},
		{"copied_long_lived_credential_field", &copiedCred, copiedCred.PortraitAuthRef, ErrLongLivedCredentialCopyForbidden},
		{"bearer_token_in_auth_ref", &wrongScope, "Bearer eyJhbGciOiJIUzI1NiJ9.secret", ErrLongLivedCredentialCopyForbidden},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := coord.SubmitEdit(context.Background(), baseReq(fmt.Sprintf("cmd-auth-%d", i), tc.auth, tc.ref))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: expected %v, got %v", tc.name, tc.wantErr, err)
			}
		})
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("expected 0 provider calls across all rejected auth cases, got %d", provider.calls.Load())
	}
	holds, spends, refunds := billingClient.counts()
	if holds != 0 || spends != 0 || refunds != 0 {
		t.Fatalf("expected 0 billing calls on rejected auth, got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}

func TestUnverifiedModeAndDishonestAdapterFailClosed(t *testing.T) {
	reg := newVerifiedRegistry(t)

	// 1. Registering full_redraw claiming pixel-lock MUST fail closed.
	err := reg.Register(AdapterSpec{
		ProviderID:                 "dishonest-provider",
		ModelID:                    "redraw-only-v1",
		ModelAdapterVersion:        "redraw-only-v1@adapter-1.0",
		AdapterEditCapability:      AdapterCapFullRedraw,
		ClaimsPixelLockedLocalEdit: true,
		Verified:                   true,
		VerifiedModes: []AdapterModeProfile{
			{
				Mode:                   ModeFidelityBackgroundEdit,
				AllowedModifyRegions:   []RegionKind{RegionBackground},
				PreservedRegions:       []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
				RequiresPostEditVerify: true,
			},
		},
	})
	if !errors.Is(err, ErrDishonestPixelLockClaim) {
		t.Fatalf("expected ErrDishonestPixelLockClaim, got %v", err)
	}

	// 2. Registering full_redraw without RequiresPostEditVerify MUST fail closed.
	err = reg.Register(AdapterSpec{
		ProviderID:                 "lazy-provider",
		ModelID:                    "redraw-no-verify-v1",
		ModelAdapterVersion:        "redraw-no-verify-v1@adapter-1.0",
		AdapterEditCapability:      AdapterCapFullRedraw,
		ClaimsPixelLockedLocalEdit: false,
		Verified:                   true,
		VerifiedModes: []AdapterModeProfile{
			{
				Mode:                   ModeFidelityBackgroundEdit,
				AllowedModifyRegions:   []RegionKind{RegionBackground},
				PreservedRegions:       []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
				RequiresPostEditVerify: false,
			},
		},
	})
	if !errors.Is(err, ErrEditModeNotVerified) {
		t.Fatalf("expected ErrEditModeNotVerified for full_redraw missing post-edit verify, got %v", err)
	}

	// 3. Unverified adapter must not advertise any VerifiedEditModes and must fail ResolveVerified.
	err = reg.Register(AdapterSpec{
		ProviderID:            "unverified-provider",
		ModelID:               "beta-v0",
		ModelAdapterVersion:   "beta-v0@adapter-0.1",
		AdapterEditCapability: AdapterCapFullRedraw,
		Verified:              false,
		VerifiedModes: []AdapterModeProfile{
			{
				Mode:                   ModeFidelityBackgroundEdit,
				AllowedModifyRegions:   []RegionKind{RegionBackground},
				PreservedRegions:       []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
				RequiresPostEditVerify: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("register unverified placeholder adapter: %v", err)
	}
	if modes := reg.VerifiedEditModes("beta-v0@adapter-0.1"); len(modes) != 0 {
		t.Fatalf("unverified adapter must expose 0 verified modes, got %v", modes)
	}
	if _, _, err := reg.ResolveVerified("beta-v0@adapter-0.1", ModeFidelityBackgroundEdit); !errors.Is(err, ErrEditModeNotVerified) {
		t.Fatalf("expected ErrEditModeNotVerified for unverified adapter, got %v", err)
	}

	// 4. Unknown mode and unverified (adapter, mode) combination fail closed.
	if _, _, err := reg.ResolveVerified("seedream-edit-v2@adapter-1.2", EditMode("magic_face_swap")); !errors.Is(err, ErrEditModeNotVerified) {
		t.Fatalf("expected ErrEditModeNotVerified for unknown mode, got %v", err)
	}
	if _, _, err := reg.ResolveVerified("mask-inpaint-v1@adapter-1.0", ModeCreativeMakeupDemo); !errors.Is(err, ErrEditModeNotVerified) {
		t.Fatalf("expected ErrEditModeNotVerified for adapter not verified for creative_makeup_demo, got %v", err)
	}

	// 5. Gate catches full_redraw missing PostEditPixelVerified or dishonestly claiming pixel lock.
	srcHash := sha256Hex([]byte("src"))
	candHash := sha256Hex([]byte("cand"))
	rep := makePassingFidelityReport(srcHash, candHash, 0.01, 0.01, 0.5)
	rep.RegionInvariance.PostEditPixelVerified = false

	dec := EvaluateBeautyEditGate(BeautyEditGateRequest{
		AppID:                      fidelitygate.AppID,
		PrincipalID:                "principal-1",
		Mode:                       ModeFidelityBackgroundEdit,
		AdapterEditCapability:      AdapterCapFullRedraw,
		ClaimsPixelLockedLocalEdit: true,
		SampleOrigin:               fidelitygate.SampleOriginReal,
		Report:                     rep,
	}, DefaultBeautyEditThresholds())

	if dec.CanAdopt() {
		t.Fatalf("expected gate failure for dishonest full_redraw without post-edit pixel verify")
	}
	if !slices.Contains(dec.FailureReasons, ReasonDishonestPixelLockClaim) {
		t.Fatalf("expected %s in failure reasons, got %v", ReasonDishonestPixelLockClaim, dec.FailureReasons)
	}
	if !slices.Contains(dec.FailureReasons, ReasonFullRedrawMissingPostEditVerify) {
		t.Fatalf("expected %s in failure reasons, got %v", ReasonFullRedrawMissingPostEditVerify, dec.FailureReasons)
	}
}

func TestNonTargetFaceAndMakeupDriftRejectedWithRealPNGPixels(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	srcPNG := makePortraitPNG(
		color.NRGBA{R: 220, G: 220, B: 220, A: 255},
		color.NRGBA{R: 235, G: 195, B: 170, A: 255},
		color.NRGBA{R: 185, G: 65, B: 90, A: 255},
	)
	// Candidate replaced background BUT ALSO altered real makeup and face skin!
	driftedPNG := makePortraitPNG(
		color.NRGBA{R: 40, G: 60, B: 110, A: 255},  // bg changed
		color.NRGBA{R: 190, G: 140, B: 120, A: 255}, // skin altered!
		color.NRGBA{R: 20, G: 210, B: 220, A: 255},  // real makeup altered drastically!
	)
	facePresMask, bgTargetMask, makeupMask, _ := makeRegionMasks()

	meas, err := MeasurePNGPortraitRegions(srcPNG, driftedPNG, facePresMask, bgTargetMask, makeupMask)
	if err != nil {
		t.Fatalf("MeasurePNGPortraitRegions drifted: %v", err)
	}
	if meas.MakeupRegionDrift <= DefaultMaxRealMakeupDrift || meas.NonTargetPixelDrift <= DefaultMaxNonTargetPixelDrift {
		t.Fatalf("expected significant non-target/makeup drift, got %+v", meas)
	}

	billingClient := &stubBillingClient{}
	billingSM, _ := fidelitygate.NewBillingRecoveryStateMachine(billingClient)
	provider := &stubEditProvider{
		fn: func(_ context.Context, _ ProviderEditRequest) (ProviderEditResponse, error) {
			rep := makePassingFidelityReport(meas.SourceSHA256, meas.CandidateSHA256, meas.NonTargetPixelDrift, meas.MakeupRegionDrift, meas.TargetRegionChangedRatio)
			rep.ScopeMatch.ObservedModifiedRegions = []RegionKind{RegionBackground, RegionRealMakeup}
			return ProviderEditResponse{
				TaskRef:            "task-drift-001",
				OutputAssetVersion: "asset-ver-drifted-001",
				PreviewRef:         "oss://preview/beauty/drifted-001.png",
				OutputSHA256:       meas.CandidateSHA256,
				ProviderCharged:    true,
				ActualCostFen:      350,
				QualityReport:      rep,
			}, nil
		},
	}

	auth := validPortraitAuth("principal-drift-1", ScopePortraitRealServiceEdit, now)
	coord, err := NewCoordinator(CoordinatorConfig{
		Registry: newVerifiedRegistry(t),
		Provider: provider,
		Billing:  billingSM,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	_, _ = coord.CreateSession(CreateSessionRequest{
		SessionID:         "sess-drift-1",
		PrincipalID:       "principal-drift-1",
		InputAssetID:      "asset-drift-1",
		InputAssetVersion: "ver-orig-d1",
		InputPreviewRef:   "oss://preview/orig-d1.png",
		InputSHA256:       meas.SourceSHA256,
	})

	res, err := coord.SubmitEdit(context.Background(), SubmitEditRequest{
		CommandID:            "cmd-drift-001",
		SessionID:            "sess-drift-1",
		Scope:                fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-drift-1", RequestKey: "req-drift-001"},
		QuoteID:              "q-drift",
		PricingVersion:       "p-v1",
		EstimatedCostFen:     350,
		GateFailurePolicy:    fidelitygate.GateFailurePolicyRefundPlatformAbsorbed,
		Mode:                 ModeFidelityBackgroundEdit,
		UsageType:            UsageRealServiceProof,
		IsRealServiceProof:   true,
		ModelAdapterVersion:  "seedream-edit-v2@adapter-1.2",
		AllowedModifyRegions: []RegionKind{RegionBackground},
		PreservedRegions:     []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
		PortraitAuth:         &auth,
		PortraitAuthRef:      auth.PortraitAuthRef,
		SampleOrigin:         fidelitygate.SampleOriginReal,
	})
	if err != nil {
		t.Fatalf("SubmitEdit drifted returned unexpected error: %v", err)
	}
	if res.GateDecision.CanAdopt() || res.GateDecision.CanClaimRealService {
		t.Fatalf("drifted face/makeup MUST fail gate: %+v", res.GateDecision)
	}
	for _, want := range []string{
		ReasonUnapprovedRealMakeupAlteration,
		ReasonNonTargetPixelDriftExceeded,
		ReasonObservedEditHitPreservedRegion,
	} {
		if !slices.Contains(res.GateDecision.FailureReasons, want) {
			t.Fatalf("expected failure reason %q in %v", want, res.GateDecision.FailureReasons)
		}
	}
	if res.BillingState != fidelitygate.BillingRefunded {
		t.Fatalf("expected platform-absorbed refund on gate failure, got %q", res.BillingState)
	}

	// Adopting a gate-rejected candidate MUST fail with ErrCandidateNotAdoptable.
	_, err = coord.AdoptVersion(AdoptVersionRequest{
		SessionID:          "sess-drift-1",
		PrincipalID:        "principal-drift-1",
		CandidateVersionID: res.CandidateVersion.VersionID,
		ExpectedRevision:   1,
	})
	if !errors.Is(err, ErrCandidateNotAdoptable) {
		t.Fatalf("expected ErrCandidateNotAdoptable, got %v", err)
	}
}

func TestRejectSimilarityOnlyCertification(t *testing.T) {
	srcHash := sha256Hex([]byte("portrait-a"))
	candHash := sha256Hex([]byte("portrait-b"))

	// Case 1: Only OverallSimilarityPercent provided, multi-dim checks unevaluated.
	simOnly := BeautyEditQualityReport{
		SourceSHA256:             srcHash,
		CandidateSHA256:          candHash,
		EvaluatorVersion:         DefaultBeautyEvaluatorVersion,
		ThresholdVersion:         DefaultBeautyThresholdVersion,
		OverallSimilarityPercent: 98.7,
	}
	if err := RejectSimilarityOnlyCertification(simOnly); !errors.Is(err, ErrSimilarityOnlyCertificationRejected) {
		t.Fatalf("expected ErrSimilarityOnlyCertificationRejected, got %v", err)
	}

	// Case 2: SimilarityOnlyCertified explicitly true even with high similarity.
	simFlagged := makePassingFidelityReport(srcHash, candHash, 0.01, 0.01, 0.6)
	simFlagged.SimilarityOnlyCertified = true
	if err := RejectSimilarityOnlyCertification(simFlagged); !errors.Is(err, ErrSimilarityOnlyCertificationRejected) {
		t.Fatalf("expected ErrSimilarityOnlyCertificationRejected when SimilarityOnlyCertified=true, got %v", err)
	}

	dec := EvaluateBeautyEditGate(BeautyEditGateRequest{
		AppID:                 fidelitygate.AppID,
		PrincipalID:           "principal-qa-1",
		Mode:                  ModeFidelityBackgroundEdit,
		AdapterEditCapability: AdapterCapPixelLockedMask,
		SampleOrigin:          fidelitygate.SampleOriginReal,
		Report:                simOnly,
	}, DefaultBeautyEditThresholds())
	if dec.CanAdopt() {
		t.Fatalf("similarity-only QA report MUST NOT pass gate")
	}
	if !slices.Contains(dec.FailureReasons, ReasonSimilarityOnlyRejected) {
		t.Fatalf("expected %s in failure reasons, got %v", ReasonSimilarityOnlyRejected, dec.FailureReasons)
	}
}

func TestTargetedLocalRedoVersionProtectionAndRollback(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	srcHash := sha256Hex([]byte("orig-portrait"))
	v1Hash := sha256Hex([]byte("cand-v1"))
	v2Hash := sha256Hex([]byte("cand-v2"))

	var seq atomic.Int64
	provider := &stubEditProvider{
		fn: func(_ context.Context, req ProviderEditRequest) (ProviderEditResponse, error) {
			n := seq.Add(1)
			h := v1Hash
			if n > 1 {
				h = v2Hash
			}
			rep := makePassingFidelityReport(srcHash, h, 0.01, 0.01, 0.7)
			rep.ScopeMatch.DeclaredAllowedRegions = slices.Clone(req.AllowedModifyRegions)
			rep.ScopeMatch.ObservedModifiedRegions = []RegionKind{RegionBackground}
			return ProviderEditResponse{
				TaskRef:            fmt.Sprintf("task-v%d", n),
				OutputAssetVersion: fmt.Sprintf("ver-cand-%d", n),
				PreviewRef:         fmt.Sprintf("oss://preview/cand-%d.png", n),
				OutputSHA256:       h,
				ProviderCharged:    true,
				ActualCostFen:      300,
				QualityReport:      rep,
			}, nil
		},
	}

	auth := validPortraitAuth("principal-ver-1", ScopePortraitRealServiceEdit, now)
	coord, err := NewCoordinator(CoordinatorConfig{
		Registry: newVerifiedRegistry(t),
		Provider: provider,
		Now:      func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}

	_, err = coord.CreateSession(CreateSessionRequest{
		SessionID:         "sess-ver-1",
		PrincipalID:       "principal-ver-1",
		InputAssetID:      "asset-v-1",
		InputAssetVersion: "ver-orig-0",
		InputPreviewRef:   "oss://preview/orig-0.png",
		InputSHA256:       srcHash,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// 1. Initial submit -> ver-cand-1.
	r1, err := coord.SubmitEdit(context.Background(), SubmitEditRequest{
		CommandID:            "cmd-v1",
		SessionID:            "sess-ver-1",
		Scope:                fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-ver-1", RequestKey: "req-v1"},
		Mode:                 ModeFidelityBackgroundEdit,
		UsageType:            UsageRealServiceProof,
		IsRealServiceProof:   true,
		ModelAdapterVersion:  "seedream-edit-v2@adapter-1.2",
		AllowedModifyRegions: []RegionKind{RegionBackground, RegionComposition},
		PreservedRegions:     []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
		PortraitAuth:         &auth,
		PortraitAuthRef:      auth.PortraitAuthRef,
		SampleOrigin:         fidelitygate.SampleOriginReal,
	})
	if err != nil || !r1.GateDecision.CanAdopt() {
		t.Fatalf("submit v1 failed: err=%v dec=%+v", err, r1.GateDecision)
	}

	// Adopt v1 (rev 1 -> 2).
	snap, err := coord.AdoptVersion(AdoptVersionRequest{
		SessionID:          "sess-ver-1",
		PrincipalID:        "principal-ver-1",
		CandidateVersionID: "ver-cand-1",
		ExpectedRevision:   1,
	})
	if err != nil || snap.CurrentRevision != 2 || snap.ActiveVersionID != "ver-cand-1" {
		t.Fatalf("adopt v1 failed: err=%v snap=%+v", err, snap)
	}

	// 2. Targeted local redo widening into real_makeup MUST fail closed (0 extra provider calls).
	callsBefore := provider.calls.Load()
	_, err = coord.SubmitLocalRedo(context.Background(), LocalRedoRequest{
		CommandID:           "cmd-redo-bad",
		SessionID:           "sess-ver-1",
		BaseVersionID:       "ver-cand-1",
		Scope:               fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-ver-1", RequestKey: "req-redo-bad"},
		Mode:                ModeFidelityBackgroundEdit,
		UsageType:           UsageRealServiceProof,
		IsRealServiceProof:  true,
		ModelAdapterVersion: "seedream-edit-v2@adapter-1.2",
		TargetRedoRegions:   []RegionKind{RegionRealMakeup},
		PortraitAuth:        &auth,
		PortraitAuthRef:     auth.PortraitAuthRef,
		SampleOrigin:        fidelitygate.SampleOriginReal,
		RedoReason:          "attempt_unapproved_makeup_touchup",
	})
	if !errors.Is(err, ErrInvalidRedoScope) {
		t.Fatalf("expected ErrInvalidRedoScope, got %v", err)
	}
	if provider.calls.Load() != callsBefore {
		t.Fatalf("bad redo must not call provider")
	}

	// 3. Valid targeted local redo restricted strictly to RegionBackground -> ver-cand-2.
	r2, err := coord.SubmitLocalRedo(context.Background(), LocalRedoRequest{
		CommandID:           "cmd-redo-bg",
		SessionID:           "sess-ver-1",
		BaseVersionID:       "ver-cand-1",
		Scope:               fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-ver-1", RequestKey: "req-redo-bg"},
		Mode:                ModeFidelityBackgroundEdit,
		UsageType:           UsageRealServiceProof,
		IsRealServiceProof:  true,
		ModelAdapterVersion: "seedream-edit-v2@adapter-1.2",
		TargetRedoRegions:   []RegionKind{RegionBackground},
		PortraitAuth:        &auth,
		PortraitAuthRef:     auth.PortraitAuthRef,
		SampleOrigin:        fidelitygate.SampleOriginReal,
		RedoReason:          "soften_background_lighting_edge",
	})
	if err != nil || !r2.GateDecision.CanAdopt() {
		t.Fatalf("valid local redo failed: err=%v dec=%+v", err, r2.GateDecision)
	}
	if r2.CandidateVersion.ParentVersionID != "ver-cand-1" || r2.CandidateVersion.RedoReason != "soften_background_lighting_edge" {
		t.Fatalf("expected redo lineage metadata, got %+v", r2.CandidateVersion)
	}

	// Adopt v2 (rev 2 -> 3).
	snap, err = coord.AdoptVersion(AdoptVersionRequest{
		SessionID:          "sess-ver-1",
		PrincipalID:        "principal-ver-1",
		CandidateVersionID: "ver-cand-2",
		ExpectedRevision:   2,
	})
	if err != nil || snap.CurrentRevision != 3 || snap.ActiveVersionID != "ver-cand-2" {
		t.Fatalf("adopt v2 failed: err=%v snap=%+v", err, snap)
	}

	// 4. Stale revision (ExpectedRevision=2) MUST NOT overwrite newer adopted version (rev 3)!
	_, err = coord.AdoptVersion(AdoptVersionRequest{
		SessionID:          "sess-ver-1",
		PrincipalID:        "principal-ver-1",
		CandidateVersionID: "ver-cand-1",
		ExpectedRevision:   2,
	})
	if !errors.Is(err, ErrStaleRevisionConflict) {
		t.Fatalf("expected ErrStaleRevisionConflict for stale adopt, got %v", err)
	}

	// 5. Rollback to original (rev 3 -> 4).
	rbSnap, err := coord.RollbackToOriginal(RollbackRequest{
		SessionID:        "sess-ver-1",
		PrincipalID:      "principal-ver-1",
		ExpectedRevision: 3,
		Reason:           "operator_prefers_original_studio_shot",
	})
	if err != nil {
		t.Fatalf("RollbackToOriginal: %v", err)
	}
	if rbSnap.CurrentRevision != 4 || rbSnap.ActiveVersionID != "ver-orig-0" || !rbSnap.RolledBackToOriginal || rbSnap.SelectedResultRef != "oss://preview/orig-0.png" {
		t.Fatalf("unexpected rollback snapshot: %+v", rbSnap)
	}

	// Stale revision after rollback (ExpectedRevision=3) MUST also fail!
	_, err = coord.AdoptVersion(AdoptVersionRequest{
		SessionID:          "sess-ver-1",
		PrincipalID:        "principal-ver-1",
		CandidateVersionID: "ver-cand-2",
		ExpectedRevision:   3,
	})
	if !errors.Is(err, ErrStaleRevisionConflict) {
		t.Fatalf("expected ErrStaleRevisionConflict after rollback, got %v", err)
	}
}

func TestIdempotentCommandReplayAndConflictZeroExtraCharge(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	srcHash := sha256Hex([]byte("idem-src"))
	candHash := sha256Hex([]byte("idem-cand"))

	billingClient := &stubBillingClient{}
	billingSM, _ := fidelitygate.NewBillingRecoveryStateMachine(billingClient)
	provider := &stubEditProvider{
		fn: func(_ context.Context, _ ProviderEditRequest) (ProviderEditResponse, error) {
			return ProviderEditResponse{
				TaskRef:            "task-idem-1",
				OutputAssetVersion: "ver-idem-1",
				PreviewRef:         "oss://preview/idem-1.png",
				OutputSHA256:       candHash,
				ProviderCharged:    true,
				ActualCostFen:      350,
				QualityReport:      makePassingFidelityReport(srcHash, candHash, 0.01, 0.01, 0.65),
			}, nil
		},
	}

	auth := validPortraitAuth("principal-idem-1", ScopePortraitRealServiceEdit, now)
	coord, _ := NewCoordinator(CoordinatorConfig{
		Registry: newVerifiedRegistry(t),
		Provider: provider,
		Billing:  billingSM,
		Now:      func() time.Time { return now },
	})
	_, _ = coord.CreateSession(CreateSessionRequest{
		SessionID:         "sess-idem-1",
		PrincipalID:       "principal-idem-1",
		InputAssetID:      "asset-idem-1",
		InputAssetVersion: "ver-orig-idem",
		InputPreviewRef:   "oss://preview/orig-idem.png",
		InputSHA256:       srcHash,
	})

	req := SubmitEditRequest{
		CommandID:            "cmd-idem-exact-001",
		SessionID:            "sess-idem-1",
		Scope:                fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-idem-1", RequestKey: "req-idem-001"},
		QuoteID:              "quote-idem",
		PricingVersion:       "pricing-v1",
		EstimatedCostFen:     350,
		GateFailurePolicy:    fidelitygate.GateFailurePolicyRefundPlatformAbsorbed,
		Mode:                 ModeFidelityBackgroundEdit,
		UsageType:            UsageRealServiceProof,
		IsRealServiceProof:   true,
		ModelAdapterVersion:  "seedream-edit-v2@adapter-1.2",
		AllowedModifyRegions: []RegionKind{RegionBackground, RegionComposition},
		PreservedRegions:     []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
		PortraitAuth:         &auth,
		PortraitAuthRef:      auth.PortraitAuthRef,
		SampleOrigin:         fidelitygate.SampleOriginReal,
		Prompt:               "soft beige studio backdrop",
	}

	first, err := coord.SubmitEdit(context.Background(), req)
	if err != nil {
		t.Fatalf("first submit failed: %v", err)
	}
	if first.IdempotentReplay {
		t.Fatalf("first submit must not be marked IdempotentReplay")
	}

	// Exact replay: returns identical version, IdempotentReplay=true, 0 extra provider/billing calls.
	second, err := coord.SubmitEdit(context.Background(), req)
	if err != nil {
		t.Fatalf("replay submit failed: %v", err)
	}
	if !second.IdempotentReplay || second.CandidateVersion.VersionID != first.CandidateVersion.VersionID || second.TaskRef != first.TaskRef {
		t.Fatalf("unexpected replay result: %+v", second)
	}

	// Conflicting payload with same command_id: rejected with ErrCommandIDConflict, 0 extra charge.
	conflictReq := req
	conflictReq.Prompt = "completely different neon background"
	_, err = coord.SubmitEdit(context.Background(), conflictReq)
	if !errors.Is(err, ErrCommandIDConflict) {
		t.Fatalf("expected ErrCommandIDConflict, got %v", err)
	}

	if provider.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 provider call, got %d", provider.calls.Load())
	}
	holds, spends, refunds := billingClient.counts()
	if holds != 1 || spends != 1 || refunds != 0 {
		t.Fatalf("expected exact 1 hold & 1 spend (0 extra charge), got holds=%d spends=%d refunds=%d", holds, spends, refunds)
	}
}

func TestConcurrentStressUnderRace(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	srcHash := sha256Hex([]byte("race-src"))
	cand1Hash := sha256Hex([]byte("race-cand-1"))
	cand2Hash := sha256Hex([]byte("race-cand-2"))

	billingClient := &stubBillingClient{}
	billingSM, _ := fidelitygate.NewBillingRecoveryStateMachine(billingClient)
	provider := &stubEditProvider{
		fn: func(_ context.Context, req ProviderEditRequest) (ProviderEditResponse, error) {
			// Small delay maximizes goroutine overlap on in-flight command_id dedup.
			time.Sleep(15 * time.Millisecond)
			h := cand1Hash
			ver := "ver-race-1"
			if req.CommandID == "cmd-race-cand-2" {
				h = cand2Hash
				ver = "ver-race-2"
			}
			return ProviderEditResponse{
				TaskRef:            "task-" + req.CommandID,
				OutputAssetVersion: ver,
				PreviewRef:         "oss://preview/" + ver + ".png",
				OutputSHA256:       h,
				ProviderCharged:    true,
				ActualCostFen:      350,
				QualityReport:      makePassingFidelityReport(srcHash, h, 0.01, 0.01, 0.65),
			}, nil
		},
	}

	auth := validPortraitAuth("principal-race-1", ScopePortraitBeautyAll, now)
	coord, _ := NewCoordinator(CoordinatorConfig{
		Registry: newVerifiedRegistry(t),
		Provider: provider,
		Billing:  billingSM,
		Now:      func() time.Time { return now },
	})
	_, _ = coord.CreateSession(CreateSessionRequest{
		SessionID:         "sess-race-1",
		PrincipalID:       "principal-race-1",
		InputAssetID:      "asset-race-1",
		InputAssetVersion: "ver-orig-race",
		InputPreviewRef:   "oss://preview/orig-race.png",
		InputSHA256:       srcHash,
	})

	// Phase A: 24 concurrent duplicate command_id submissions -> EXACTLY 1 provider call & 1 billing charge!
	const dupWorkers = 24
	var (
		wg            sync.WaitGroup
		freshCount    atomic.Int64
		replayCount   atomic.Int64
		errCount      atomic.Int64
	)
	dupReq := SubmitEditRequest{
		CommandID:            "cmd-race-cand-1",
		SessionID:            "sess-race-1",
		Scope:                fidelitygate.BillingScope{AppID: fidelitygate.AppID, PrincipalID: "principal-race-1", RequestKey: "req-race-1"},
		QuoteID:              "quote-race-1",
		PricingVersion:       "pricing-v1",
		EstimatedCostFen:     350,
		GateFailurePolicy:    fidelitygate.GateFailurePolicyRefundPlatformAbsorbed,
		Mode:                 ModeFidelityBackgroundEdit,
		UsageType:            UsageRealServiceProof,
		IsRealServiceProof:   true,
		ModelAdapterVersion:  "seedream-edit-v2@adapter-1.2",
		AllowedModifyRegions: []RegionKind{RegionBackground, RegionComposition},
		PreservedRegions:     []RegionKind{RegionFaceIdentity, RegionSkinTexture, RegionRealMakeup},
		PortraitAuth:         &auth,
		PortraitAuthRef:      auth.PortraitAuthRef,
		SampleOrigin:         fidelitygate.SampleOriginReal,
	}

	start := make(chan struct{})
	for i := 0; i < dupWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			res, err := coord.SubmitEdit(context.Background(), dupReq)
			if err != nil {
				errCount.Add(1)
				return
			}
			if res.IdempotentReplay {
				replayCount.Add(1)
			} else {
				freshCount.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if errCount.Load() != 0 || freshCount.Load() != 1 || replayCount.Load() != dupWorkers-1 {
		t.Fatalf("concurrent idempotent submit mismatch: errs=%d fresh=%d replay=%d",
			errCount.Load(), freshCount.Load(), replayCount.Load())
	}
	if provider.calls.Load() != 1 {
		t.Fatalf("expected exactly 1 provider call under 24x concurrent duplicate submit, got %d", provider.calls.Load())
	}
	holds, spends, refunds := billingClient.counts()
	if holds != 1 || spends != 1 || refunds != 0 {
		t.Fatalf("expected exact 1 hold & 1 spend under 24x concurrent duplicate submit, got holds=%d spends=%d refunds=%d",
			holds, spends, refunds)
	}

	// Create second candidate so concurrent AdoptVersion race has 2 valid competing targets.
	req2 := dupReq
	req2.CommandID = "cmd-race-cand-2"
	req2.Scope.RequestKey = "req-race-2"
	if _, err := coord.SubmitEdit(context.Background(), req2); err != nil {
		t.Fatalf("submit cand-2: %v", err)
	}

	// Phase B: 20 concurrent AdoptVersion calls targeting ExpectedRevision=1 alongside readers -> EXACTLY 1 winner!
	const adoptWorkers = 20
	var (
		adoptWinners atomic.Int64
		staleLosers  atomic.Int64
		otherErrs    atomic.Int64
	)
	adoptStart := make(chan struct{})
	for i := 0; i < adoptWorkers; i++ {
		wg.Add(1)
		target := "ver-race-1"
		if i%2 == 1 {
			target = "ver-race-2"
		}
		go func(ver string) {
			defer wg.Done()
			<-adoptStart
			_, err := coord.AdoptVersion(AdoptVersionRequest{
				SessionID:          "sess-race-1",
				PrincipalID:        "principal-race-1",
				CandidateVersionID: ver,
				ExpectedRevision:   1,
			})
			switch {
			case err == nil:
				adoptWinners.Add(1)
			case errors.Is(err, ErrStaleRevisionConflict):
				staleLosers.Add(1)
			default:
				otherErrs.Add(1)
			}
		}(target)
	}
	// Concurrent readers (CompareVersions, GetSession, BuildHandoffPackage).
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-adoptStart
			_, _ = coord.CompareVersions("principal-race-1", "sess-race-1", "ver-race-1")
			_, _ = coord.GetSession("principal-race-1", "sess-race-1")
			_, _ = coord.BuildHandoffPackage(HandoffRequest{
				SessionID:     "sess-race-1",
				PrincipalID:   "principal-race-1",
				ExportPurpose: ExportPurposeRealServiceProof,
				Consumer:      ConsumerShuhai,
			})
		}()
	}
	close(adoptStart)
	wg.Wait()

	if adoptWinners.Load() != 1 || staleLosers.Load() != adoptWorkers-1 || otherErrs.Load() != 0 {
		t.Fatalf("optimistic adopt race mismatch: winners=%d stale=%d otherErrs=%d",
			adoptWinners.Load(), staleLosers.Load(), otherErrs.Load())
	}
	finalSnap, err := coord.GetSession("principal-race-1", "sess-race-1")
	if err != nil || finalSnap.CurrentRevision != 2 {
		t.Fatalf("expected final revision 2, got err=%v snap=%+v", err, finalSnap)
	}
}

func TestCapabilityContractEchoCopyAndCreativeIdentityDrift(t *testing.T) {
	reg := newVerifiedRegistry(t)
	contract, err := reg.CapabilityContractFor("seedream-edit-v2@adapter-1.2", ModeFidelityBackgroundEdit)
	if err != nil {
		t.Fatalf("CapabilityContractFor: %v", err)
	}
	if contract.AdapterEditCapability != AdapterCapFullRedraw ||
		!contract.RequiresPostEditVerify ||
		len(contract.VerifiedEditModes) != 2 ||
		len(contract.AllowedModifyRegions) == 0 ||
		len(contract.PreservedRegions) == 0 ||
		len(contract.KnownLimitations) == 0 {
		t.Fatalf("incomplete capability contract: %+v", contract)
	}

	// Echo copy on identical PNGs must be detected and rejected by the gate.
	srcPNG := makePortraitPNG(
		color.NRGBA{R: 200, G: 200, B: 200, A: 255},
		color.NRGBA{R: 230, G: 190, B: 165, A: 255},
		color.NRGBA{R: 180, G: 60, B: 85, A: 255},
	)
	facePresMask, bgTargetMask, makeupMask, _ := makeRegionMasks()
	echoMeas, err := MeasurePNGPortraitRegions(srcPNG, srcPNG, facePresMask, bgTargetMask, makeupMask)
	if err != nil {
		t.Fatalf("MeasurePNGPortraitRegions echo: %v", err)
	}
	if !echoMeas.EchoCopyDetected || echoMeas.TargetRegionChangedRatio != 0 {
		t.Fatalf("expected echo copy detection on identical PNGs, got %+v", echoMeas)
	}
	echoDec := EvaluateBeautyEditGate(BeautyEditGateRequest{
		AppID:                 fidelitygate.AppID,
		PrincipalID:           "principal-echo",
		Mode:                  ModeFidelityBackgroundEdit,
		AdapterEditCapability: AdapterCapFullRedraw,
		SampleOrigin:          fidelitygate.SampleOriginReal,
		Report:                makePassingFidelityReport(echoMeas.SourceSHA256, echoMeas.CandidateSHA256, echoMeas.NonTargetPixelDrift, echoMeas.MakeupRegionDrift, echoMeas.TargetRegionChangedRatio),
	}, DefaultBeautyEditThresholds())
	if echoDec.CanAdopt() || !slices.Contains(echoDec.FailureReasons, ReasonEchoCopyDetected) || !slices.Contains(echoDec.FailureReasons, ReasonTargetRegionNotModified) {
		t.Fatalf("expected echo copy rejection, got %+v", echoDec)
	}

	// Creative makeup demo must STILL reject facial identity swap / background alteration.
	creativeRep := makePassingCreativeReport(sha256Hex([]byte("c-src")), sha256Hex([]byte("c-cand")), 0.25, 0.45, 0.85)
	creativeRep.Identity.FaceIdentityScore = 0.65
	creativeRep.ScopeMatch.ObservedModifiedRegions = []RegionKind{RegionMakeupRegion, RegionFaceIdentity, RegionBackground}
	creativeDec := EvaluateBeautyEditGate(BeautyEditGateRequest{
		AppID:                 fidelitygate.AppID,
		PrincipalID:           "principal-creative-drift",
		Mode:                  ModeCreativeMakeupDemo,
		AdapterEditCapability: AdapterCapFullRedraw,
		SampleOrigin:          fidelitygate.SampleOriginReal,
		Report:                creativeRep,
	}, DefaultBeautyEditThresholds())
	if creativeDec.CanAdopt() {
		t.Fatalf("creative demo with identity swap & background drift MUST fail gate")
	}
	for _, want := range []string{
		ReasonFaceIdentityDriftExceeded,
		ReasonNonTargetPixelDriftExceeded,
		ReasonObservedEditHitPreservedRegion,
		ReasonObservedEditOutsideAllowedScope,
	} {
		if !slices.Contains(creativeDec.FailureReasons, want) {
			t.Fatalf("expected %s in creative failure reasons, got %v", want, creativeDec.FailureReasons)
		}
	}
}
