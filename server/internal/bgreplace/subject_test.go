package bgreplace

import "testing"

func TestPortraitCapabilitiesDeclareFidelityUnsupportedAtAdapterLevel(t *testing.T) {
	caps := PortraitCapabilities()
	if len(caps) != 2 {
		t.Fatalf("需要保真/创意两条声明, got %d", len(caps))
	}
	byMode := map[string]SubjectCapability{}
	for _, c := range caps {
		byMode[c.Mode] = c
		if c.Subject != SubjectPortrait || c.EvidenceRef == "" || c.EvidenceAt == "" {
			t.Fatalf("声明缺失主体或证据引用: %#v", c)
		}
	}
	fid := byMode[string(ModeFidelity)]
	if fid.Supported || !fid.ProviderSupported || fid.PreservesIdentity != true || fid.PixelLocked {
		t.Fatalf("产品级人像保真未接入；provider 可用但非像素锁定: %#v", fid)
	}
	if fid.Scope != PortraitScopeProviderMaskOnly {
		t.Fatalf("保真范围必须是 provider 遮罩级: %#v", fid)
	}
	if len(fid.MustPreserve) == 0 {
		t.Fatalf("保真必须声明应保留内容: %#v", fid)
	}
	cre := byMode[string(ModeCreative)]
	if !cre.Supported || !cre.ProviderSupported || cre.PreservesIdentity != false || cre.PixelLocked || cre.Scope != PortraitScopeWholeImage {
		t.Fatalf("创意妆面只能声明整图生成且不保身份: %#v", cre)
	}
}
