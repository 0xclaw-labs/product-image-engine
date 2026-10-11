import { describe, expect, it } from "vitest";
import {
  MatrixDraft,
  draftHonestLabel,
  matrixLicenseRequired,
} from "../src/lib/matrix-draft";

describe("matrixLicenseRequired", () => {
  it("license_ref 破坏性必填", () => {
    expect(matrixLicenseRequired(null)).toBe(true);
    expect(matrixLicenseRequired(undefined)).toBe(true);
    expect(matrixLicenseRequired("")).toBe(true);
    expect(matrixLicenseRequired("   ")).toBe(true);
    expect(matrixLicenseRequired("lic-1")).toBe(false);
  });
});

describe("draftHonestLabel", () => {
  const base: MatrixDraft = { draft_id: "md_1", published: false, charged: false };

  it("草稿只陈述事实,不说已发布", () => {
    expect(draftHonestLabel(base)).toContain("未发布、未扣费");
    expect(draftHonestLabel(base)).toContain("md_1");
    expect(draftHonestLabel({ ...base, idempotent_replay: true })).toContain("幂等重放");
  });

  it("出现 published/charged=true 直接拒绝展示(不说谎)", () => {
    expect(() => draftHonestLabel({ ...base, published: true })).toThrow();
    expect(() => draftHonestLabel({ ...base, charged: true })).toThrow();
  });
});
