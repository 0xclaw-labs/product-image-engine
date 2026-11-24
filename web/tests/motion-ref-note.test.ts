import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

function noteSource(): string {
  return readFileSync(path.join(process.cwd(), "src/components/motion-ref/MotionRefNote.tsx"), "utf8");
}

describe("MotionRefNote 用户面字段下沉（R1）", () => {
  it("用户面不出现 asset_id / content_hash / revision_id / model_call_count 明文标签与值", () => {
    const src = noteSource();
    // 旧的明文展示行已移除；原始 ID 只进 data-* 属性与 dev 折叠详情。
    expect(src).not.toContain("资产 {row.asset_id}");
    expect(src).not.toContain("内容哈希 {row.content_hash}");
    expect(src).not.toContain("修订 {row.revision_id}");
    expect(src).not.toContain("模型调用 {row.model_call_count");
    expect(src).not.toContain("商品重生成");
  });

  it("行级 article 用 data-* 承载四个内部字段", () => {
    const src = noteSource();
    expect(src).toContain("data-asset-id={row.asset_id}");
    expect(src).toContain("data-content-hash={row.content_hash}");
    expect(src).toContain("data-revision-id={row.revision_id}");
    expect(src).toContain("data-model-call-count={row.model_call_count ?? 0}");
  });

  it("版本选择器默认当前版本，提交体仍携带 revision_id（BFF 契约不动）", () => {
    const src = noteSource();
    // 默认选中首条（当前版本），不覆盖用户已选。
    expect(src).toContain("rows[0]?.revision_id || \"\"");
    expect(src).toContain("name=\"revision_id\"");
    expect(src).toContain("body: JSON.stringify({ revision_id: revisionID.trim() })");
  });

  it("技术详情折叠区仅开发环境渲染，生产构建不输出内部文本节点", () => {
    const src = noteSource();
    expect(src).toContain('process.env.NODE_ENV !== "production"');
    expect(src).toContain('data-dev-only="true"');
  });
});
