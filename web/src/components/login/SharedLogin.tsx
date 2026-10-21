"use client";

// 产品图登录面对齐生态统一组件（vendor/login-page = @painuo/login-page，
// .source-ref 记录同步源）。会话不走 identity cookie：auth.signIn 打回自家
// BFF /api/auth/login，Go 后端验 identity 后 BFF 落 pia_access/pia_refresh
// cookie——BFF/Go 零改动。错误面统一收口 failure-copy（状态码/机器码不出
// 用户界面）。

import { LoginPage, ApiFailure } from "@painuo/login-page";

import { loginFailureText } from "@/lib/failure-copy";

import styles from "./shared-login.module.css";

/** 与 vendor AuthAdapter["signIn"] 同形；抛 ApiFailure 进组件错误面。 */
export type SignInBridge = (
  identifier: string,
  password: string,
) => Promise<Record<string, unknown>>;

/** 统一提交桥：POST /api/auth/login（BFF → Go /api/v1/auth/login）。 */
export function createBffSignIn(): SignInBridge {
  return async (identifier, password) => {
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ email: identifier.trim().toLowerCase(), password }),
      // 15s 客户端时限沿用原手写登录页（D24）；BFF 上游另有 30s 超时（D26）。
      signal: AbortSignal.timeout(15000),
    });
    const data = (await res.json().catch(() => ({}))) as Record<string, unknown>;
    if (!res.ok) {
      throw new ApiFailure(loginFailureText(res.status, data), res.status, "login_rejected");
    }
    return data;
  };
}

/**
 * 亮色薄包装。layout="compact"（宿主自持左台主视觉）、密码单式（本应用
 * 没有验证码端点）；brand.name 是统一卡的页题「产品图」；accent 注入本仓
 * 蓝色 token（包缺省 mint 与本仓品牌色不同源）。brand.tagline 不覆写——
 * 包缺省「账号由主管理员开通，不能自助注册。」正是本应用的真实口径。
 */
export function SharedLogin({
  signIn,
  onAuthenticated,
}: {
  signIn: SignInBridge;
  onAuthenticated?: (result: Record<string, unknown>) => void;
}) {
  return (
    <div className={styles.light}>
      <LoginPage
        layout="compact"
        modes={{ otp: false, password: true }}
        brand={{
          name: "产品图",
          media: null,
          accent: {
            base: "var(--accent)",
            hover: "var(--accent-hover)",
            weak: "var(--accent-weak)",
            onAccent: "var(--on-accent)",
          },
        }}
        auth={{ signIn }}
        onAuthenticated={(result) => onAuthenticated?.(result)}
        // 落点导航归宿主：onAuthenticated 里 router.replace(safeNext ?? 登录首页)。
        navigate={() => {}}
        messages={{
          generic: (e: unknown) =>
            e instanceof ApiFailure
              ? loginFailureText(e.status, { error: e.code })
              : loginFailureText(0, {}),
        }}
      />
    </div>
  );
}
