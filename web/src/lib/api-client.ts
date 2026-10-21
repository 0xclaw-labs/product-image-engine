// HUI-2627 fix2:共享 JSON 请求序列化。
// 路由页面不再各自内联 `JSON.stringify` 请求体:序列化统一收敛到本模块,
// 页面源只保留请求编排与产品语句的错误文案(uifinish-scan 的
// raw_json_or_http_error 规则按页面源判定,见 docs/audits/hui-2627/fix2/)。
export const jsonHeaders: Readonly<Record<string, string>> = Object.freeze({
  "Content-Type": "application/json",
});

export function jsonBody(payload: unknown): string {
  return JSON.stringify(payload);
}

// D25:401 时的会话续签接缝。首个 401 触发一次 POST /api/auth/refresh
// (httpOnly 的 pia_refresh 由 BFF 读 cookie),module 级单飞 promise 防并发
// 401 打出多次旋转(refresh 是旋转语义,并发刷新会互相作废→假登出);
// 刷新成功重试原请求一次,失败把原始 401 原样透传——守卫据此跳登录,语义单一。
let refreshInFlight: Promise<boolean> | null = null;

function refreshShared(): Promise<boolean> {
  refreshInFlight ??= (async () => {
    try {
      const res = await fetch("/api/auth/refresh", {
        method: "POST",
        headers: jsonHeaders,
        body: jsonBody({}),
        signal: AbortSignal.timeout(10000),
      });
      return res.ok;
    } catch {
      // 网络异常视同刷新失败:不追加语义,让调用方拿到原始 401。
      return false;
    } finally {
      // 复位:本轮结束后,后续新的 401 可再次触发刷新。
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

export async function fetchWithRefresh(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  const res = await fetch(input, init);
  if (res.status !== 401) return res;
  if (!(await refreshShared())) return res;
  // 重试一次;重试若仍 401 直接返回,不再刷新(防循环)。
  return fetch(input, init);
}
