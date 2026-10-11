// HUI-2627（AG05 2026-10-11）：登录页与工程列表的按钮必须走设计系统 Button。
// 2625 gate-r2 / fix2 复扫把 login、projects 的原生 <button> 记为 off_design_system 残留；
// 本测试钉住「页面源不再含原生 <button>，且从 components/ui/button 引入 Button」。
// 口径说明：uifinish-scan 工具不在本检出中，本测试是源码级防回潮，不替代正式复扫。
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

const PAGES = ["src/app/login/page.tsx", "src/app/projects/page.tsx"] as const;

describe("HUI-2627 登录/工程列表按钮走设计系统 Button", () => {
  for (const page of PAGES) {
    it(`${page} 不含原生 <button>，并引入 Button`, () => {
      const source = readFileSync(path.join(webRoot, page), "utf8");
      expect(source).not.toMatch(/<button[\s>]/);
      expect(source).toMatch(/import \{ Button \} from "@\/components\/ui\/button";/);
      expect(source).toMatch(/<Button[\s>]/);
    });
  }
});
