// HUI-2388 源端 UI 事实:送矩阵(真实 matrixdraft 服务)与回查。
// 草稿不是发布:published/charged 恒 false;查询与导航零创建。

export interface MatrixDraftFacts {
  source_project_id?: string;
  campaign_id?: string;
  revision?: number;
  grant_ref?: string;
  brand_id?: string;
  payer_id?: string;
  quality_verdict?: string;
  source_kind?: string;
}

export interface MatrixDraft {
  draft_id?: string;
  schema_version?: string;
  source_app?: string;
  content_type?: string;
  asset_hash?: string;
  license_ref?: string;
  status?: string;
  published?: boolean;
  charged?: boolean;
  idempotent_replay?: boolean;
}

// draftHonestLabel 只陈述草稿事实,绝不说"已发布"。
export function draftHonestLabel(draft: MatrixDraft): string {
  if (draft.published === true || draft.charged === true) {
    // 目的端契约里不可能出现;出现即拒展(不说谎)。
    throw new Error("草稿不能是已发布或已扣费");
  }
  const replay = draft.idempotent_replay ? "幂等重放" : "新建";
  return `草稿 ${draft.draft_id ?? "—"}(${replay});未发布、未扣费`;
}

// matrixLicenseRequired license_ref 破坏性必填:空引用不得发起送矩阵。
export function matrixLicenseRequired(licenseRef: string | null | undefined): boolean {
  return licenseRef == null || licenseRef.trim() === "";
}
