import { describe, expect, it } from "vitest";
import {
  MatrixDraft,
  draftHonestLabel,
  matrixLicenseRequired,
  parseLicenseExpiry,
} from "../src/lib/matrix-draft";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { MatrixDraftPanel } from "../src/components/matrix-draft/Panel";

describe("matrixLicenseRequired", () => {
  it("license_ref 破坏性必填", () => {
    expect(matrixLicenseRequired(null)).toBe(true);
    expect(matrixLicenseRequired(undefined)).toBe(true);
    expect(matrixLicenseRequired("")).toBe(true);
    expect(matrixLicenseRequired("   ")).toBe(true);
    expect(matrixLicenseRequired("lic-1")).toBe(false);
  });
});

describe("parseLicenseExpiry", () => {
  it("空输入是可选项(返回空串,不带字段)", () => {
    expect(parseLicenseExpiry("")).toBe("");
    expect(parseLicenseExpiry("   ")).toBe("");
  });

  it("非法输入返回 null 而非抛出(new Date 异常就地接住,不产生 unhandled rejection)", () => {
    expect(parseLicenseExpiry("not-a-date")).toBeNull();
    expect(parseLicenseExpiry("2030-13-45T99:99")).toBeNull();
    expect(() => parseLicenseExpiry("2030-13-45T99:99")).not.toThrow();
  });

  it("合法输入解析成 ISO(UTC);显式时区偏移结果确定", () => {
    expect(parseLicenseExpiry("2030-01-01T08:00:00+08:00")).toBe("2030-01-01T00:00:00.000Z");
    expect(parseLicenseExpiry(" 2030-01-01T00:00:00Z ")).toBe("2030-01-01T00:00:00.000Z");
    // datetime-local 形式(无时区)按本地时区解析,只要求解析成功非空。
    expect(parseLicenseExpiry("2030-01-01T08:00")).not.toBeNull();
    expect(parseLicenseExpiry("2030-01-01T08:00")).not.toBe("");
  });
});

describe("MatrixDraftPanel", () => {
  it("面板静态渲染含许可必填与到期输入,不抛异常", () => {
    const html = renderToStaticMarkup(
      createElement(MatrixDraftPanel, { projectId: "proj_a", outputs: [{ id: "out_1", file_name: "a.png" }] })
    );
    expect(html).toContain("license 引用(必填)");
    expect(html).toContain("license 到期");
    expect(html).toContain("送矩阵(创建草稿)");
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
