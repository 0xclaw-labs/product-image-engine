// ProductImage 表单可访问性加固回归(D-forms P1):
// - 字段 id 与 label htmlFor 配对;
// - 错误节点 id 与 aria-describedby/aria-invalid 锚点成对;
// - number 输入带 min/max;日期走 Popover+Calendar,hidden input 承载提交值。
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { MatrixDraftPanel } from "../src/components/matrix-draft/Panel";
import { SubjectFidelityPanel } from "../src/components/SubjectFidelityPanel";

function htmlOf(el: React.ReactElement) {
  return renderToStaticMarkup(el);
}

describe("矩阵草案面板可访问性", () => {
  it("成果/license/到期控件有稳定 id,label 配对,到期带 min", () => {
    const html = htmlOf(
      createElement(MatrixDraftPanel, {
        projectId: "proj_a",
        outputs: [{ id: "out_1", file_name: "a.png" }],
      })
    );
    for (const id of ["md-output", "md-license", "md-expiry"]) {
      expect(html).toContain(`id="${id}"`);
      expect(html).toContain(`for="${id}"`);
    }
    // 等价替换:原生 datetime-local 不再外露;触发器可访问、hidden input 承载提交值,
    // 早于 min 的日期在日历上禁用(禁选逻辑见 date-time-picker dayDisabled)。
    expect(html).toContain('data-testid="md-expiry-trigger"');
    expect(html).toContain('id="md-expiry"');
    expect(html).toContain('type="hidden"');
    const expirySource = readFileSync(
      path.join(process.cwd(), "src/components/ui/date-time-picker.tsx"),
      "utf8"
    );
    expect(expirySource).toContain("dayDisabled");
    expect(expirySource).toContain("disabled={dayDisabled(dayStr)}");
  });

  it("license 与到期错误锚点 id 存在,控件接 aria-describedby", () => {
    const source = readFileSync(
      path.join(process.cwd(), "src/components/matrix-draft/Panel.tsx"),
      "utf8"
    );
    for (const anchor of ["md-license-error", "md-expiry-error"]) {
      expect(source).toContain(`id="${anchor}"`);
    }
    expect(source).toContain('aria-describedby={licenseError ? "md-license-error" : undefined}');
    expect(source).toContain('aria-describedby={expiryError ? "md-expiry-error" : undefined}');
    expect(source).toContain('aria-invalid={!!licenseError}');
    expect(source).toContain('aria-invalid={!!expiryError}');
    // 校验失败提示以 alert 角色广播,不再是纯 status 文本。
    expect(source).toContain('role={noticeAlert ? "alert" : "status"}');
  });
});

describe("主体保真面板可访问性", () => {
  it("输入/选择组 id 与 label htmlFor 配对", () => {
    const html = htmlOf(createElement(SubjectFidelityPanel, { projectId: "proj_a" }));
    const ids = [
      "sf-mode",
      "sf-sample-class",
      "sf-input-ref",
      "sf-input-version",
      "sf-mask-ref",
      "sf-output-ref",
      "sf-check-product_count",
      "sf-check-logo_text",
      "sf-check-shape",
      "sf-check-key_texture",
      "sf-check-declared_color",
    ];
    for (const id of ids) {
      expect(html, id).toContain(`id="${id}"`);
      expect(html, id).toContain(`for="${id}"`);
    }
  });

  it("失败提示以 alert 角色广播", () => {
    const source = readFileSync(
      path.join(process.cwd(), "src/components/SubjectFidelityPanel.tsx"),
      "utf8"
    );
    expect(source).toContain('role={noticeAlert ? "alert" : "status"}');
  });
});

describe("工程详情页表单可访问性", () => {
  const source = readFileSync(
    path.join(process.cwd(), "src/app/projects/[id]/page.tsx"),
    "utf8"
  );

  it("宽高 number 输入带 1..16384 边界,挂接素材字段有 id 与 label 配对", () => {
    for (const id of [
      "proj-name",
      "proj-width",
      "proj-height",
      "proj-attach-asset-id",
      "proj-attach-snapshot-name",
      "proj-attach-size",
    ]) {
      expect(source, id).toContain(`id="${id}"`);
      expect(source, id).toContain(`htmlFor="${id}"`);
    }
    expect(source).toContain("min={1}");
    expect(source).toContain("max={MAX_DIMENSION}");
  });

  it("简报错误锚点 id 存在并接到控件 aria-describedby/aria-invalid", () => {
    for (const anchor of ["proj-name-error", "proj-width-error", "proj-height-error"]) {
      expect(source).toContain(`id="${anchor}"`);
    }
    expect(source).toContain('aria-describedby={fieldErrors.name ? "proj-name-error" : undefined}');
    expect(source).toContain('aria-describedby={fieldErrors.width ? "proj-width-error" : undefined}');
    expect(source).toContain('aria-describedby={fieldErrors.height ? "proj-height-error" : undefined}');
    expect(source).toContain('aria-invalid={!!fieldErrors.name}');
    expect(source).toContain('aria-invalid={!!fieldErrors.width}');
    expect(source).toContain('aria-invalid={!!fieldErrors.height}');
  });
});
