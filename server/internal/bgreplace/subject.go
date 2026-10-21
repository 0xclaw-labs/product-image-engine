package bgreplace

// 人像/妆面受限编辑的能力边界（HUI-3902 走查）。
//
// 2026-10-21 真实取证（verify/industry-walk/artifacts/face_metrics_all.json）：
//   - doubao-seedream-4.0 / 5.0-pro、qwen-image-3.0 以原图作参考整图重绘，
//     OpenCV SFace 余弦相似度 0.06 / -0.01 / 0.10 / 0.09 / 0.16；
//     同人常用阈值约 0.363。身份、脸型、妆面均未保留。
//   - wanx2.1-imageedit + 人工遮罩（白=保留，黑=编辑）可以把背景换成纯色，
//     原人物身份保留（SFace 0.870）；但非目标区域仍有整体重绘/缩放：
//     人物区仅 13.99% 像素落在 ±8、MAE 23.0，不能声称像素锁定。
//   - 当前产品图适配器只把原图作为参考传给整图模型（imagemodel.Request.Refs），
//     不传遮罩；保真模式走平台任务，生产开关未开。故产品级人像保真未开放。
//
// 结论：人像保真在 provider 级「有可用组合、非像素锁定」，产品适配器未接入；
// 人像/妆面创意只能整图生成，必须明确「不保留身份、不代表真实服务效果」。
const (
	SubjectPortrait = "portrait"
	SubjectProduct  = "product"

	PortraitScopeNone             = "none"
	PortraitScopeProviderMaskOnly = "provider_mask_only"
	PortraitScopeWholeImage       = "whole_image"

	PortraitEvidenceRef = "verify/industry-walk/artifacts/face_metrics_all.json"
	PortraitEvidenceAt  = "2026-10-21"
)

// SubjectCapability 一项可对外声明的主体/模式能力。
type SubjectCapability struct {
	Subject string `json:"subject"`
	Mode    string `json:"mode"`
	// Supported 是产品适配器级结论（用户当前能否走通），不是 provider 是否有模型。
	Supported bool `json:"supported"`
	// ProviderSupported 只说明真实模型/组合能力，避免把 provider 可用误写成产品可用。
	ProviderSupported bool     `json:"provider_supported"`
	Scope             string   `json:"scope,omitempty"`
	PreservesIdentity bool     `json:"preserves_identity"`
	PixelLocked       bool     `json:"pixel_locked"`
	AllowedChanges    []string `json:"allowed_changes,omitempty"`
	MustPreserve      []string `json:"must_preserve,omitempty"`
	Limitations       []string `json:"limitations,omitempty"`
	TestedModels      []string `json:"tested_models,omitempty"`
	EvidenceRef       string   `json:"evidence_ref,omitempty"`
	EvidenceAt        string   `json:"evidence_at,omitempty"`
}

// PortraitCapabilities 固定返回人像两条路径的真实边界，字段不依赖运行时旋钮。
// creative 是否可执行仍由调用方另加 creative_available 开关；本函数不把它写成已通过。
func PortraitCapabilities() []SubjectCapability {
	return []SubjectCapability{
		{
			Subject: SubjectPortrait, Mode: string(ModeFidelity), Supported: false,
			ProviderSupported: true, Scope: PortraitScopeProviderMaskOnly, PreservesIdentity: true, PixelLocked: false,
			AllowedChanges: []string{"background", "composition"},
			MustPreserve:   []string{"face", "identity", "makeup", "skin", "hair", "pose"},
			Limitations: []string{
				"provider 可用：wanx2.1-imageedit + 人工遮罩可保身份改背景（SFace 0.870）",
				"非像素锁定：人物区仅 13.99% 像素在 ±8 内、MAE 23.0，不能声称局部像素不变",
				"产品适配器未接入遮罩：保真模式走平台任务且生产开关未开",
				"不开放用修图冒充真实到店效果，也不开放前后对比",
			},
			TestedModels: []string{
				"doubao-seedream-4-0-20260415",
				"doubao-seedream-5-0-pro-260628",
				"qwen-image-3.0",
				"wanx2.1-imageedit",
			},
			EvidenceRef: PortraitEvidenceRef, EvidenceAt: PortraitEvidenceAt,
		},
		{
			Subject: SubjectPortrait, Mode: string(ModeCreative), Supported: true,
			ProviderSupported: true, Scope: PortraitScopeWholeImage, PreservesIdentity: false, PixelLocked: false,
			AllowedChanges: []string{"makeup", "background", "composition"},
			MustPreserve:   []string{},
			Limitations: []string{
				"整图生成：生成人物与原人物不同，不能称为同一人",
				"仅作创意示意，不代表真实服务效果；发布须标 AI 生成",
				"是否可执行以 capabilities.creative_available 与生产开关为准",
			},
			TestedModels: []string{
				"doubao-seedream-5-0-pro-260628",
				"qwen-image-3.0",
			},
			EvidenceRef: PortraitEvidenceRef, EvidenceAt: PortraitEvidenceAt,
		},
	}
}
