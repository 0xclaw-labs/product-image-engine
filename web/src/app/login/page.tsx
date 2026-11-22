"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";

import { fetchWithRefresh } from "@/lib/api-client";
import { firstScreen, loggedInHome } from "@/lib/first-image";
import { SharedLogin, createBffSignIn } from "@/components/login/SharedLogin";
import { Alert, AlertDescription } from "@/components/shadcn/alert";
import { Button } from "@/components/ui/button";

// 登录页:左台保留产品声音(五步真实流程+诚实声明),表单本体是生态统一组件
// @painuo/login-page(vendor/login-page)。组件卡自带品牌头(h1「产品图」+封闭
// 注册 tagline)。会话探测与 ?next= 防御留在宿主:?next= 只收站内路径(以单/
// 开头,拒绝 // 与外链),防 open redirect;登录提交由组件经 signIn 桥打 BFF。
// 会话异常给可见错误面与重试,不静默;vendor 表单始终挂载,不被骨架/错误替换。
const STEPS = firstScreen().steps;
const signIn = createBffSignIn();

function safeNext(raw: string | null): string | null {
  if (!raw || !raw.startsWith("/") || raw.startsWith("//")) return null;
  return raw;
}

export default function LoginPage() {
  const router = useRouter();
  const nextRef = useRef<string | null>(null);
  const [sessionError, setSessionError] = useState("");
  const [probing, setProbing] = useState(true);

  const probeSession = useCallback(async () => {
    setSessionError("");
    setProbing(true);
    try {
      const res = await fetchWithRefresh("/api/auth/session", {
        signal: AbortSignal.timeout(10000),
      });
      if (res.ok) {
        router.replace(nextRef.current ?? loggedInHome());
      } else if (res.status !== 401) {
        setSessionError("暂时无法确认登录状态，仍可在下方尝试登录。");
      }
    } catch {
      setSessionError("网络连接失败，暂时无法确认登录状态，请检查网络后重试。");
    } finally {
      setProbing(false);
    }
  }, [router]);

  useEffect(() => {
    nextRef.current = safeNext(
      new URLSearchParams(window.location.search).get("next"),
    );
    void probeSession();
  }, [probeSession]);

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
        {sessionError ? (
          <Alert variant="destructive" role="alert" className="mb-3">
            <AlertDescription className="flex flex-wrap items-center gap-3">
              {sessionError}
              <Button type="button" size="sm" variant="outline" onClick={() => void probeSession()}>
                重试
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}
        {probing ? (
          <p role="status" className="muted mb-3 text-sm">
            正在检查登录状态…
          </p>
        ) : null}
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
