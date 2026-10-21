"use client";

import { useEffect, useRef } from "react";
import { useRouter } from "next/navigation";

import { fetchWithRefresh } from "@/lib/api-client";
import { firstScreen, loggedInHome } from "@/lib/first-image";
import { SharedLogin, createBffSignIn } from "@/components/login/SharedLogin";

// 登录页:左台保留产品声音(五步真实流程+诚实声明),表单本体换成生态统一
// 组件 @painuo/login-page(vendor/login-page,亮肤见 components/login/
// shared-login.module.css)。组件卡自带品牌头(h1「产品图」+封闭注册 tagline)。
// 会话探测与 ?next= 防御留在宿主:?next= 只收站内路径(以单/开头,拒绝 //
// 与外链),防 open redirect;登录提交由组件经 signIn 桥打 BFF。
const STEPS = firstScreen().steps;
const signIn = createBffSignIn();

function safeNext(raw: string | null): string | null {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//")) return null;
  return raw;
}

export default function LoginPage() {
  const router = useRouter();
  const nextRef = useRef<string | null>(null);

  // 已登录访问 /login 直接送回目标页;会话检查失败(未登录/平台不可达/
  // 网络不通)一律静默留在本页——登录页是这些失败的合法落点。
  // D25:401 先走 fetchWithRefresh 续签一次,refresh 有效则照样送回目标页。
  useEffect(() => {
    nextRef.current = safeNext(
      new URLSearchParams(window.location.search).get("next"),
    );
    let cancelled = false;
    (async () => {
      try {
        const res = await fetchWithRefresh("/api/auth/session", {
          signal: AbortSignal.timeout(10000),
        });
        if (!cancelled && res.ok) {
          router.replace(nextRef.current ?? loggedInHome());
        }
      } catch {
        // 未登录或不可达都留在登录页,不打扰。
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [router]);

  return (
    <div className="media-bench" data-layout="entry">
      <section className="media-stage" aria-label="登录后的流程">
        <div className="stage-empty" data-state="empty">
          <p className="stage-kicker">产品图</p>
          <h1>先看商品，再决定改哪里</h1>
          <p>登录之后从一张商品照片开始。商品本身保持不动。</p>
          <ol className="entry-steps">
            {STEPS.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
          <p className="muted">这里还没有成片。登录不会生成图片。</p>
        </div>
      </section>
      <aside className="media-rail">
        <SharedLogin
          signIn={signIn}
          onAuthenticated={() => {
            // 成功落点:深链优先,站内性由 safeNext 收口;组件内 navigate 已置空。
            router.replace(safeNext(nextRef.current) ?? loggedInHome());
          }}
        />
        <p className="muted mt-12 mb-0">
          身份由平台统一派生，本应用不保存口令。
        </p>
      </aside>
    </div>
  );
}
