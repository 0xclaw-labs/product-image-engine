import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import LoginPage from "@/app/login/page";

// renderToStaticMarkup 没有 app router 上下文,useRouter 需要 mock。
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: () => {} }),
}));

// 登录页换生态统一组件 @painuo/login-page(vendor/login-page v0.2.3)后的
// 组件级契约。行为分支(401/网络异常/深链回跳)由 BFF 测试与 e2e 覆盖;
// 这里钉住结构事实——密码单式无分段、字段在前提交在后、autocomplete、
// 品牌头与产品声音、错误面由 failure-copy 收口。
// 注:统一卡自带品牌头 h1(「产品图」),左台产品声音 h1(「先看商品…」)
// 并存,页内共两枚 h1——这是 vendor 版式契约(touch-engine 同构),D25 的
// 单 h1 纪律钉在其余五路由,不在本页强套。

function render(): string {
  return renderToStaticMarkup(createElement(LoginPage));
}

describe("登录页(统一组件后的结构契约)", () => {
  it("密码单式:无登录方式分段,唯一表单是账号密码,提交按钮文案为「登录」", () => {
    const html = render();
    expect(html).not.toContain("登录方式"); // Segmented 分段控件不渲染(modes 固定)
    expect(html).not.toContain("发送验证码");
    expect((html.match(/<form[\s>]/g) ?? []).length).toBe(1);
    expect(html).toContain("用户名");
    expect(html).toContain("密码");
    expect(html).toContain("登录");
  });

  it("字段在前、提交按钮在后(Tab/读屏顺序,组件契约延续 D24)", () => {
    const html = render();
    const usernameAt = html.indexOf('autoComplete="username"');
    const passwordAt = html.indexOf('autoComplete="current-password"');
    const submitAt = html.indexOf('type="submit"');
    expect(usernameAt).toBeGreaterThan(-1);
    expect(passwordAt).toBeGreaterThan(usernameAt);
    expect(submitAt).toBeGreaterThan(passwordAt);
  });

  it("邮箱/口令带 name 语义的 autocomplete(WCAG 1.3.5,密码管理器可识别)", () => {
    const html = render();
    // 组件标识字段是用户名语义(username),提交桥里做 trim+lowercase 进 email。
    expect(html).toContain('autoComplete="username"');
    expect(html).toContain('autoComplete="current-password"');
  });

  it("label 与控件 htmlFor/id 关联(lpg-field 配对)", () => {
    const html = render();
    const ids = [...html.matchAll(/<input[^>]*id="(lpg-field-\d+)"/g)].map((m) => m[1]);
    expect(ids.length).toBe(2);
    for (const id of ids) {
      expect(html).toContain(`for="${id}"`);
    }
  });

  it("统一卡品牌头:h1「产品图」+ 封闭注册 tagline(本应用真实口径)", () => {
    const html = render();
    expect(html).toContain("产品图");
    expect(html).toContain("账号由主管理员开通，不能自助注册。");
  });

  it("产品声音保留:左台主视觉、五步真实流程、不生成图片声明、身份派生声明", () => {
    const html = render();
    expect(html).toContain("先看商品，再决定改哪里");
    expect(html).toContain("上传或选择商品照片");
    expect(html).toContain("费用确认");
    expect(html).toContain("登录不会生成图片");
    expect(html).toContain("身份由平台统一派生，本应用不保存口令。");
  });

  it("页内 h1 恰两枚:左台产品声音 + 组件品牌头(vendor 版式契约,见文件头注)", () => {
    const html = render();
    expect(html.match(/<h1[ >]/g)?.length).toBe(2);
  });

  it("初始无错误面;错误渲染是组件内 role=alert(failure-copy 收口,不透传后端原文)", () => {
    const html = render();
    expect(html).not.toContain('role="alert"');
  });
});
