// HUI-2627（AG05 2026-10-11）：登录页与工程列表的按钮必须走设计系统 Button。
// 2625 gate-r2 / fix2 复扫把 login、projects 的原生 <button> 记为 off_design_system 残留；
// 本测试钉住「页面源不再含原生 <button>，且从 components/ui/button 引入 Button」。
// 口径说明：uifinish-scan 工具不在本检出中，本测试是源码级防回潮，不替代正式复扫。
// 统一组件接入（2026-10-21）：/login 表单本体交给 @painuo/login-page，页面源
// 不再直接引 Button——改钉「挂 SharedLogin 且无裸控件」，按钮口径由组件契约
// 与 tests/login-page.test.tsx 承接；projects 页仍走本仓设计系统 Button。
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const DS_PAGES = ["src/app/projects/page.tsx"] as const;

describe("HUI-2627 登录/工程列表按钮走设计系统 Button", () => {
  it("src/app/login/page.tsx 表单本体是统一组件，页面源无裸控件", () => {
    const source = readFileSync(path.join(webRoot, "src/app/login/page.tsx"), "utf8");
    expect(source).toMatch(/<SharedLogin[\s>]/);
    expect(source).toMatch(/from "@\/components\/login\/SharedLogin"/);
    expect(source).not.toMatch(/<button[\s>]/);
    expect(source).not.toMatch(/<input[\s>]/);
    expect(source).not.toMatch(/<form[\s>]/);
  });

  for (const page of DS_PAGES) {
    it(`${page} 不含原生 <button>，并引入 Button`, () => {
      const source = readFileSync(path.join(webRoot, page), "utf8");
      expect(source).not.toMatch(/<button[\s>]/);
      expect(source).toMatch(/import \{ Button \} from "@\/components\/ui\/button";/);
      expect(source).toMatch(/<Button[\s>]/);
    });
  }

  // D25:主操作升级 variant="primary" 后,className="primary" 兜底写法不得回潮。
  it("D25 防回潮:两页不再用 className=\"primary\" 兜底", () => {
    for (const page of ["src/app/login/page.tsx", ...DS_PAGES]) {
      const source = readFileSync(path.join(webRoot, page), "utf8");
      expect(source, page).not.toContain('className="primary"');
    }
  });
});
