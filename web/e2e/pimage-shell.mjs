// FIXTURE-STACK browser journey for the HUI-2627 / HUI-3902 shell slice.
// Everything here is labelled `fixture`: it drives the real Next app against
// e2e/fixture-api.mjs. It proves UI states and keyboard/touch/overflow facts,
// never real generation, billing or provider acceptance.
import { execFileSync, spawn } from "node:child_process";
import fs from "node:fs";
import path from "node:path";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { chromium } from "playwright-core";

const here = path.dirname(fileURLToPath(import.meta.url));
const webDir = path.resolve(here, "..");
const WEB = Number(process.env.E2E_WEB_PORT ?? 32331);
const API = Number(process.env.FIXTURE_API_PORT ?? 32334);
const required = (name) => {
  const v = process.env[name];
  if (!v) {
    console.error(`${name} is required`);
    process.exit(2);
  }
  return v;
};
const OUT = required("E2E_OUT");
const PROFILE = required("E2E_PROFILE");
const SAMPLES = required("FIXTURE_SAMPLES_DIR");
const CHROME = process.env.E2E_CHROME ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const BASE = `http://localhost:${WEB}`;
const VIEWPORTS = { desktop: { width: 1280, height: 800 }, mobile: { width: 390, height: 844 } };
const children = [];
const results = [];

function start(cmd, args, env, name) {
  const child = spawn(cmd, args, { cwd: webDir, env: { ...process.env, ...env }, stdio: ["ignore", "pipe", "pipe"] });
  child.stdout.on("data", (d) => process.env.E2E_VERBOSE && process.stdout.write(`[${name}] ${d}`));
  child.stderr.on("data", (d) => process.env.E2E_VERBOSE && process.stderr.write(`[${name}] ${d}`));
  children.push(child);
  return child;
}
async function waitFor(url, ms = 90000) {
  const end = Date.now() + ms;
  for (;;) {
    try {
      const r = await fetch(url);
      if (r.status < 500) return;
    } catch {}
    if (Date.now() > end) throw new Error(`timeout waiting for ${url}`);
    await new Promise((r) => setTimeout(r, 300));
  }
}
const api = (p, body) => fetch(`http://127.0.0.1:${API}${p}`, body ? { method: "POST", body: JSON.stringify(body) } : {}).then((r) => r.json());
const shot = (page, name, vp) => page.screenshot({ path: path.join(OUT, `fixture-pimage-${name}-${vp}.png`), fullPage: true });
const noHorizontalScroll = (page) => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth + 1);

async function open(name, vp) {
  const dir = path.join(PROFILE, `${name}-${vp}`);
  fs.rmSync(dir, { recursive: true, force: true });
  const context = await chromium.launchPersistentContext(dir, {
    executablePath: CHROME,
    headless: true,
    viewport: VIEWPORTS[vp],
    args: ["--no-first-run", "--disable-extensions", "--disable-sync"],
  });
  await context.addCookies([{ name: "pia_access", value: "fixture-access", url: BASE }]);
  const page = context.pages()[0] ?? (await context.newPage());
  if (process.env.E2E_DEBUG) {
    page.on("console", (msg) => console.log(`[console:${msg.type()}] ${msg.text()}`));
    page.on("pageerror", (error) => console.log(`[pageerror] ${error.message}`));
    page.on("requestfailed", (request) => console.log(`[requestfailed] ${request.url()} ${request.failure()?.errorText}`));
    page.on("response", (response) => {
      if (response.url().includes("source-image-runs") || response.url().includes("revisions")) {
        console.log(`[response] ${response.status()} ${response.request().method()} ${response.url()}`);
      }
    });
  }
  return { context, page };
}

async function touchAudit(page) {
  return page.evaluate(() => {
    const nodes = [...document.querySelectorAll(
      '[data-page-pattern] button:not([disabled]), [data-page-pattern] input:not([type="checkbox"]):not([disabled]), [data-page-pattern] select:not([disabled]), [data-page-pattern] textarea:not([disabled])'
    )];
    return nodes
      .filter((el) => {
        const r = el.getBoundingClientRect();
        return r.width > 0 && r.height > 0;
      })
      .map((el) => {
        const r = el.getBoundingClientRect();
        return { tag: el.tagName.toLowerCase(), text: (el.textContent || el.getAttribute("aria-label") || "").trim().slice(0, 40), w: Math.round(r.width), h: Math.round(r.height) };
      })
      .filter((x) => x.w < 44 || x.h < 44);
  });
}

async function journey(page, vp) {
  await api("/__fixture/scenario", { name: "limited", reset: true });
  await page.goto(`${BASE}/start`);
  await page.getByRole("heading", { name: "开始做产品图" }).waitFor();
  assert.ok(await noHorizontalScroll(page), "start: horizontal scroll");
  await shot(page, "start-empty", vp);

  const frozen = path.join(SAMPLES, "originals", "carton-1024x1280.png");
  await page.locator("#photo-file").setInputFiles(frozen);
  await page.getByRole("button", { name: "上传并使用" }).click();
  await page.getByTestId("plate-form").waitFor({ timeout: 30000 });
  // The original appears in the stage before any paid action.
  await page.getByTestId("stage-original").waitFor({ timeout: 20000 });
  assert.ok(await page.getByTestId("stage-original").evaluate((img) => img.complete && img.naturalWidth > 0), "original really loads");

  await page.getByLabel("背景描述").fill("深蓝色渐变摄影棚背景，柔和顶光");
  // Keyboard path: Tab reaches the quote action and Enter submits it. This is
  // also what makes the browser treat the programmatic confirm focus as
  // :focus-visible, instead of a mouse-only focus move.
  await page.getByLabel("背景描述").focus();
  await page.keyboard.press("Tab");
  assert.equal(await page.evaluate(() => document.activeElement?.textContent), "获取报价", "Tab reaches the quote button");
  await page.keyboard.press("Enter");
  await page.getByTestId("plate-quote").waitFor({ timeout: 20000 });
  await page.getByText("¥0.25").first().waitFor();
  assert.ok(await noHorizontalScroll(page), "quote: horizontal scroll");
  await shot(page, "quote", vp);

  // Keyboard: the confirm button receives focus once the quote is usable.
  await page.waitForFunction(() => document.activeElement?.getAttribute("data-testid") === "plate-confirm", null, { timeout: 10000 });
  const ring = await page.evaluate(() => {
    const s = getComputedStyle(document.activeElement);
    return `${s.outlineStyle} ${s.outlineWidth}`;
  });
  assert.ok(ring.startsWith("solid") && !ring.endsWith(" 0px"), `visible focus ring: ${ring}`);

  // Leave-and-return must resume the same in-flight request.
  await api("/__fixture/scenario", { name: "slow" });
  await page.getByTestId("plate-confirm").click();
  // Wait for the confirm response itself: navigating before the POST settles
  // would abandon the request (and looked like a quote-only restore).
  await page.getByText("正在生成背景并检查商品").first().waitFor({ timeout: 30000 });
  await page.goto(`${BASE}/start?project=proj_fx`);
  await page.getByText("正在生成背景并检查商品").first().waitFor({ timeout: 30000 });
  assert.ok(await noHorizontalScroll(page), "waiting: horizontal scroll");
  await api("/__fixture/advance", {});
  if (process.env.E2E_DEBUG) {
    console.log("[advance]", JSON.stringify((await api("/__fixture/stats")).runs));
    await page.waitForTimeout(5000);
    console.log("[after5s]", JSON.stringify((await api("/__fixture/stats")).runs));
  }
  await page.locator('[data-testid="plate-result"][data-candidate="limited_candidate"]').waitFor({ timeout: 40000 });
  await api("/__fixture/scenario", { name: "limited" });

  assert.ok(await page.getByRole("img", { name: "换背景后的商品图" }).evaluate((img) => img.complete && img.naturalWidth > 0), "candidate really loads");
  assert.equal(await page.getByTestId("stage-candidate").count(), 1);
  assert.equal(await page.getByTestId("stage-original").count(), 1);
  assert.ok(await page.getByText("未采用").first().isVisible());
  // The local-edit capability is closed in this environment; the UI says so
  // up front instead of offering an action that 404s.
  await page.getByTestId("revision-edit-closed").waitFor({ timeout: 15000 });
  assert.ok(await noHorizontalScroll(page), "result: horizontal scroll");
  await shot(page, "result-compare", vp);

  // Full-screen comparison is a real dialog: focus lands on close, Esc returns.
  const zoom = page.getByTestId("stage-zoom-candidate");
  await zoom.click();
  await page.getByTestId("stage-dialog").waitFor();
  await page.waitForFunction(() => document.activeElement?.getAttribute("data-testid") === "stage-dialog-close", null, { timeout: 5000 });
  await page.keyboard.press("Escape");
  await page.getByTestId("stage-dialog").waitFor({ state: "detached" });
  assert.equal(await page.evaluate(() => document.activeElement?.getAttribute("data-testid")), "stage-zoom-candidate", "focus returns to the zoom trigger");
  await shot(page, "result", vp);

  // Candidate selection is not adoption; adoption is a separate explicit move.
  await page.getByTestId("plate-select").click();
  await page.getByTestId("plate-adopt").waitFor();
  await page.waitForFunction(() => {
    const b = document.querySelector('[data-testid="plate-adopt"]');
    return b && !b.hasAttribute("disabled");
  }, null, { timeout: 10000 });
  assert.ok(await page.getByText("选定只是候选；采用才会把当前版本指针移到这张图").first().isVisible());
  await page.getByTestId("plate-adopt").click();
  await page.getByText("已采用为当前版本。版本区可以回退。").waitFor({ timeout: 20000 });
  await page.locator("#revision").getByText("V3").first().waitFor({ timeout: 20000 });
  await page.locator("#revision").getByText("已采用").first().waitFor();
  await shot(page, "adopted", vp);

  // Version rollback moves the same adopted pointer back to V1.
  await page.locator("#revision").getByRole("button", { name: "回到 V1", exact: true }).click();
  await page.getByText("已回到 V1。").waitFor({ timeout: 20000 });
  await page.locator("#revision").getByText(/V1/).first().waitFor();
  await shot(page, "rolled-back", vp);

  // Export keeps an explicit version reference; downstream only returns a local ref.
  const exportHref = await page.getByTestId("plate-export").getAttribute("href");
  assert.match(exportHref ?? "", /\/source-image-runs\/sir_[A-Za-z0-9_-]+\/export$/);
  await page.locator("#revision").getByRole("button", { name: "送数字人" }).last().click();
  await page.getByText(/只返回了 V3 的本地引用|数字人、AiCut 和矩阵都还没收到/).first().waitFor({ timeout: 20000 });

  // The list and the professional workspace are real surfaces too.
  await page.goto(`${BASE}/projects`);
  await page.getByRole("heading", { name: "我的制作工程" }).waitFor();
  assert.ok(await noHorizontalScroll(page), "projects list: horizontal scroll");
  await shot(page, "projects-list", vp);
  await page.goto(`${BASE}/projects/proj_fx`);
  await page.locator('[data-page-pattern="workspace"]').waitFor({ timeout: 30000 });
  await page.getByRole("heading", { name: "个人商品图" }).waitFor();
  await page.locator('[data-plate-entry][data-ready="true"][aria-busy="false"]').waitFor({ timeout: 30000 });
  assert.ok(await noHorizontalScroll(page), "workspace: horizontal scroll");
  await shot(page, "workspace", vp);

  // Touch targets in the touched surfaces at 390px (inline text links are a
  // documented WCAG 2.5.8 exception and are not buttons/inputs).
  if (vp === "mobile") {
    const small = await touchAudit(page);
    assert.deepEqual(small, [], `touch targets under 44px: ${JSON.stringify(small)}`);
  }
}

async function main() {
  fs.mkdirSync(OUT, { recursive: true });
  for (const f of fs.readdirSync(OUT)) if (f.startsWith("fixture-pimage-")) fs.rmSync(path.join(OUT, f));
  // Build is a precondition; never drive `next start` against stale output.
  execFileSync(path.join(webDir, "node_modules/.bin/next"), ["build"], { cwd: webDir, stdio: "ignore" });
  start("node", ["e2e/fixture-api.mjs"], { FIXTURE_API_PORT: String(API), FIXTURE_SAMPLES_DIR: SAMPLES }, "api");
  start(path.join(webDir, "node_modules/.bin/next"), ["start", "-p", String(WEB)], {
    PRODUCT_SERVER_BASE_URL: `http://127.0.0.1:${API}`,
    PRODUCT_SERVER_TOKEN: "fixture-token",
    NEXT_TELEMETRY_DISABLED: "1",
  }, "web");
  await waitFor(`http://127.0.0.1:${API}/__fixture/stats`);
  await waitFor(`${BASE}/login`);

  const viewports = process.env.E2E_VIEWPORTS ? process.env.E2E_VIEWPORTS.split(",") : ["desktop", "mobile"];
  for (const vp of viewports) {
    const { context, page } = await open("pimage-shell", vp);
    try {
      await journey(page, vp);
      results.push({ viewport: vp, layer: "fixture", ok: true });
      console.log(`PASS pimage-shell@${vp}`);
    } catch (error) {
      results.push({ viewport: vp, layer: "fixture", ok: false, error: String(error?.message ?? error) });
      console.log(`FAIL pimage-shell@${vp}: ${error?.message ?? error}`);
      try { await shot(page, `FAILED-${vp}`, vp); } catch {}
    } finally {
      await context.close();
    }
  }
  fs.writeFileSync(path.join(OUT, "pimage-shell-run.json"), JSON.stringify({ layer: "fixture", at: new Date().toISOString(), results }, null, 2));
  if (results.some((r) => !r.ok)) process.exitCode = 1;
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
}).finally(() => {
  for (const child of children) child.kill("SIGTERM");
});
