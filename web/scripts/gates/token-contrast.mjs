#!/usr/bin/env node
// Deterministic contrast gate for the token pairs this shell actually uses.
// It independently recomputes WCAG ratios from the adopted CSS values (it does
// not trust the producer's report), and also cross-checks that the producer's
// own product-image-work obligations are all pass in resolved.json.
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const root = path.join(webDir, "src/styles/generated/painuo/v1");
const sha = (buf) => createHash("sha256").update(buf).digest("hex");

function fail(code, detail = "") {
  console.error(`token-contrast: ${code}${detail ? `: ${detail}` : ""}`);
  process.exit(1);
}
function channel(v) {
  const c = v / 255;
  return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
}
export function luminance(hex) {
  const m = /^#([0-9a-f]{6})$/.exec(hex);
  if (!m) throw new Error(`not a six-digit hex: ${hex}`);
  const n = parseInt(m[1], 16);
  return 0.2126 * channel((n >> 16) & 0xff) + 0.7152 * channel((n >> 8) & 0xff) + 0.0722 * channel(n & 0xff);
}
export function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const lockPath = path.join(root, "painuo-tokens.lock.json");
if (!existsSync(lockPath)) fail("lock_missing");
const lock = JSON.parse(readFileSync(lockPath, "utf8"));
for (const [target, digest] of Object.entries(lock.files)) {
  const file = path.join(webDir, "..", target);
  if (!existsSync(file) || sha(readFileSync(file)) !== digest) fail("artifact_drift", target);
}
const css = readFileSync(path.join(root, "tokens.css"), "utf8");
const block = /\[data-pn-surface="work\.light"\]\[data-pn-theme="dark"\]\s*\{([\s\S]*?)\n\}/.exec(css);
if (!block) fail("work_light_dark_block_missing");
const vars = {};
for (const match of block[1].matchAll(/--pn-([a-z0-9-]+):\s*(#[0-9a-f]{6})/g)) vars[match[1]] = match[2];

// Pairs this shell renders. Text >= 4.5, necessary non-text >= 3.
const PAIRS = [
  ["text-primary", "bg-page", 4.5, "body on page"],
  ["text-primary", "bg-surface", 4.5, "body on card"],
  ["text-primary", "bg-raised", 4.5, "body on raised control"],
  ["text-primary", "bg-canvas", 4.5, "body on stage"],
  ["text-secondary", "bg-page", 4.5, "label on page"],
  ["text-secondary", "bg-surface", 4.5, "label on card"],
  ["text-secondary", "bg-raised", 4.5, "label on raised"],
  ["text-muted", "bg-page", 4.5, "muted on page"],
  ["text-muted", "bg-surface", 4.5, "muted on card"],
  ["text-muted", "bg-raised", 4.5, "muted on raised"],
  ["brand-text", "bg-page", 4.5, "link on page"],
  ["brand-text", "bg-surface", 4.5, "link on card"],
  ["brand-text", "bg-raised", 4.5, "link on raised"],
  ["brand-ink", "brand-accent", 4.5, "primary button text"],
  ["brand-ink", "brand-hover", 4.5, "primary button hover"],
  ["status-success-fg", "status-success-weak", 4.5, "success chip"],
  ["status-danger-fg", "status-danger-weak", 4.5, "danger banner"],
  ["status-warning-fg", "status-warning-weak", 4.5, "warning banner"],
  ["status-info-fg", "status-info-weak", 4.5, "info banner"],
  ["border-focus", "bg-page", 3, "focus ring on page"],
  ["border-focus", "bg-surface", 3, "focus ring on card"],
  ["border-focus", "bg-canvas", 3, "focus ring on stage"],
  ["border-strong", "bg-surface", 3, "control border"],
  ["text-inverse", "brand-accent", 4.5, "inverse on accent"],
];

const rows = [];
let failed = 0;
for (const [fg, bg, min, usage] of PAIRS) {
  if (!vars[fg] || !vars[bg]) fail("token_missing", `${fg}/${bg}`);
  const ratio = contrast(vars[fg], vars[bg]);
  const pass = ratio >= min;
  if (!pass) failed++;
  rows.push({ fg: `--pn-${fg}`, bg: `--pn-${bg}`, usage, min, ratio: Number(ratio.toFixed(3)), pass });
}

const resolved = JSON.parse(readFileSync(path.join(root, "resolved.json"), "utf8"));
const obligations = resolved.contrast.rows.filter(
  (row) => row.profiles.includes("product-image-web") && row.surface === "work.light" && row.theme === "dark"
);
if (obligations.length !== 99) fail("producer_obligation_count", String(obligations.length));
const producerFailed = obligations.filter((row) => row.verdict !== "pass");
if (producerFailed.length) fail("producer_obligation_failed", producerFailed[0].pair);

const out = {
  ok: failed === 0,
  surface: "work.light",
  theme: "dark",
  tokenVersion: lock.tokenVersion,
  sourceCanonicalSha256: lock.sourceCanonicalSha256,
  pairs: rows,
  producerObligations: obligations.length,
  producerFailures: producerFailed.length,
  failures: failed,
};
console.log(JSON.stringify(out, null, 2));
if (failed) process.exit(1);
