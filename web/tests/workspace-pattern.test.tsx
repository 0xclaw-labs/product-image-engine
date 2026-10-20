import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { WorkspaceShell } from "@/components/workspace/WorkspaceShell";
import { Stage } from "@/components/workspace/Stage";

const shell = renderToStaticMarkup(
  createElement(
    WorkspaceShell,
    {
      title: "春季纸盒主图",
      subtitle: "来源：独立制作",
      status: createElement("span", null, "进行中"),
      backHref: "/projects",
      versions: createElement("p", null, "V1 已采用"),
      children: createElement("p", null, "作品台内容"),
    }
  )
);

describe("workspace page pattern", () => {
  it("has one page h1 and the required workspace regions", () => {
    expect((shell.match(/<h1/g) ?? []).length).toBe(1);
    expect(shell).toContain("春季纸盒主图");
    expect(shell).toContain('data-page-pattern="workspace"');
    expect(shell).toContain('data-region="header"');
    expect(shell).toContain('data-region="status"');
    expect(shell).toContain('data-region="current_work"');
    expect(shell).toContain('data-region="recent_context"');
    expect(shell).toContain("V1 已采用");
    expect(shell).toContain('href="/projects"');
  });

  it("the project detail and start surfaces are wired to the patterns", () => {
    const detail = readFileSync(path.join(process.cwd(), "src/app/projects/[id]/page.tsx"), "utf8");
    expect(detail).toContain("<WorkspaceShell");
    expect(detail).toContain("versions={<RevisionRail");
    expect(detail).toContain('data-region="next_steps"');
    const start = readFileSync(path.join(process.cwd(), "src/components/first-image/StartScreen.tsx"), "utf8");
    expect(start).toContain('data-page-pattern="creation"');
    expect(start).toContain('data-region="progress_context"');
    expect(start).toContain('data-region="review_summary"');
  });

  it("the stage exposes original and candidate as labelled, comparable images", () => {
    const html = renderToStaticMarkup(
      createElement(Stage, {
        pattern: "compare",
        original: { src: "/api/projects/p1/inputs/pin_1/content", alt: "商品原图：纸盒", label: "原图", width: 1024, height: 1280 },
        candidate: { src: "/api/projects/p1/source-image-runs/sir_1/content", alt: "换背景后的商品图", label: "候选（限定范围，未采用）", width: 1024, height: 1280 },
        candidateBadge: "通过商品检查（限定范围）",
        candidateTone: "ok",
      })
    );
    expect(html).toContain('data-page-pattern="compare"');
    expect(html).toContain('data-region="comparison_views"');
    expect(html).toContain('data-testid="stage-original"');
    expect(html).toContain('data-testid="stage-candidate"');
    expect(html).toContain('alt="商品原图：纸盒"');
    expect(html).toContain('alt="换背景后的商品图"');
    expect(html).toContain("未采用");
    expect(html).toContain('aria-label="全屏查看原图"');
    expect(html).toContain('aria-label="全屏查看候选"');
    expect(html).toContain('role="group"');
    expect(html).toContain("只看候选");
  });

  it("without a candidate it shows an empty stage, never a substitute image", () => {
    const html = renderToStaticMarkup(
      createElement(Stage, {
        empty: createElement("p", null, "结果会显示在这里。还没有成片时，不会用别的图代替。"),
      })
    );
    expect(html).toContain("不会用别的图代替");
    expect(html).not.toContain("<img");
    expect(html).toContain('data-page-pattern="media_result"');
  });
});
