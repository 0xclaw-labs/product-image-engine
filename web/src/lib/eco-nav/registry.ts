import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

/**
 * HUI-2227 残差②：生态导航登记真源。
 * 唯一来源是 vendored public-ai production-manifest.json（逐字节，见同目录
 * SOURCE.txt）。登记地址只有通过与 public-ai internal/econavmanifest emittable
 * 相同的检查才发射：https、无凭据/端口/query/fragment、非空路径、host 非
 * loopback/invalid。生产表没有的行诚实缺失（不出现在映射里）。
 */

type LaunchTargetRow = { target_id?: string; kind?: string; url?: string };
type RegistryApp = { app_id?: string; display_name?: string; enabled?: boolean; launch_targets?: LaunchTargetRow[] };
type RegistryFile = { apps?: RegistryApp[] };

const registryDir = join(dirname(fileURLToPath(import.meta.url)), "registry");

function readRegistry(name: string): RegistryFile {
  return JSON.parse(readFileSync(join(registryDir, name), "utf8")) as RegistryFile;
}

export function emittable(raw: string): boolean {
  let url: URL;
  try {
    url = new URL(raw);
  } catch {
    return false;
  }
  if (url.protocol !== "https:" || url.username || url.password || url.port || url.search || url.hash || url.pathname === "") {
    return false;
  }
  const host = url.hostname.toLowerCase();
  if (host === "localhost" || host === "127.0.0.1" || host === "::1" || host === "invalid" || host.endsWith(".invalid")) {
    return false;
  }
  return true;
}

export type NavState = "available" | "degraded" | "unavailable";
export interface CurrentAppFact {
  appId: string;
  display: string;
  navState: NavState;
}

const FALLBACK_CURRENT_APP: CurrentAppFact = { appId: "product-image", display: "产品图", navState: "unavailable" };
const NAV_STATES = new Set(["available", "degraded", "unavailable"]);

/** 当前应用身份/就绪态来自 eco-nav/v1 nav-manifest；缺失/非法时 fail-closed。 */
export function currentApp(): CurrentAppFact {
  try {
    const file = JSON.parse(readFileSync(join(registryDir, "nav-manifest.json"), "utf8")) as {
      manifest_version?: string;
      apps?: { app_id?: string; default_display_name?: string; nav_state?: string }[];
    };
    if (file.manifest_version !== "eco-nav-manifest/v1") return FALLBACK_CURRENT_APP;
    const row = (file.apps ?? []).find((app) => app.app_id === "product-image");
    if (!row || typeof row.default_display_name !== "string" || !row.default_display_name.trim()) return FALLBACK_CURRENT_APP;
    const navState = typeof row.nav_state === "string" && NAV_STATES.has(row.nav_state) ? (row.nav_state as NavState) : "unavailable";
    return { appId: "product-image", display: row.default_display_name, navState };
  } catch {
    return FALLBACK_CURRENT_APP;
  }
}

export interface LaunchRow {
  app_id: string;
  display_name: string;
  target_id: string;
  url: string;
}

/** 生产表 → 可发射 launch 行（含展示名；无真实地址的行缺失，不冒充）。 */
export function registeredLaunchRows(): LaunchRow[] {
  const file = readRegistry("production-manifest.json");
  const out: LaunchRow[] = [];
  for (const app of file.apps ?? []) {
    if (app.enabled === false) continue;
    for (const target of app.launch_targets ?? []) {
      if (target.kind !== "launch" || !target.target_id || !target.url) continue;
      if (!emittable(target.url)) continue;
      out.push({
        app_id: app.app_id ?? target.target_id,
        display_name: app.display_name ?? target.target_id,
        target_id: target.target_id,
        url: target.url,
      });
    }
  }
  return out.sort((a, b) => a.app_id.localeCompare(b.app_id));
}

/** 生产表 → target_id → 可发射 URL（无真实地址的行缺失，不冒充）。 */
export function registeredLaunchMap(): Record<string, string> {
  const out: Record<string, string> = {};
  for (const row of registeredLaunchRows()) {
    if (row.target_id in out) continue; // 重复登记不覆盖，首行为准（上游保证唯一）
    out[row.target_id] = row.url;
  }
  return out;
}
