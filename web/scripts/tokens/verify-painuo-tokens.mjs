#!/usr/bin/env node
// Deterministic consumer-side token lock verification. This is the gate, not a
// promise: it fails on any byte drift, missing profile artifact, or lock shape
// mismatch. It does not contact the producer or the network.
import { createHash } from "node:crypto";
import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const repoDir = path.resolve(webDir, "..");
const root = path.join(repoDir, "web/src/styles/generated/painuo/v1");
const lockPath = path.join(root, "painuo-tokens.lock.json");
const sha = (buf) => createHash("sha256").update(buf).digest("hex");
const fail = (code, detail = "") => {
  console.error(`verify-painuo-tokens: ${code}${detail ? `: ${detail}` : ""}`);
  process.exit(1);
};
if (!existsSync(lockPath)) fail("lock_missing", lockPath);
const lock = JSON.parse(readFileSync(lockPath, "utf8"));
const FIELDS = ["schemaVersion", "tokenVersion", "sourceCommit", "sourceCanonicalSha256", "consumerId", "defaultSurface", "defaultTheme", "files", "aliasesDeprecatedUntil"];
if (JSON.stringify(Object.keys(lock).sort()) !== JSON.stringify([...FIELDS].sort())) fail("lock_fields", Object.keys(lock).join(","));
if (lock.schemaVersion !== "painuo-token-lock/v1") fail("lock_schema");
if (lock.consumerId !== "product-image-web") fail("lock_consumer");
if (lock.defaultSurface !== "work.light" || lock.defaultTheme !== "light") fail("lock_default");
if (!/^[0-9a-f]{40}$/.test(lock.sourceCommit)) fail("lock_source_commit");
if (!/^[0-9a-f]{64}$/.test(lock.sourceCanonicalSha256)) fail("lock_canonical");
if (!/^\d+\.\d+\.\d+$/.test(lock.tokenVersion)) fail("lock_token_version");
if (!lock.aliasesDeprecatedUntil || typeof lock.aliasesDeprecatedUntil !== "object") fail("lock_alias_rule");
for (const [target, digest] of Object.entries(lock.files)) {
  if (!/^[0-9a-f]{64}$/.test(digest)) fail("lock_file_hash", target);
  const file = path.join(repoDir, target);
  if (!existsSync(file)) fail("artifact_missing", target);
  if (sha(readFileSync(file)) !== digest) fail("artifact_drift", target);
}
const css = readFileSync(path.join(root, "tokens.css"), "utf8");
for (const sel of [
  '[data-pn-surface="work.light"][data-pn-theme="dark"]',
  '[data-pn-surface="work.light"][data-pn-theme="light"]',
]) {
  if (!css.includes(sel)) fail("theme_block_missing", sel);
}
const resolved = JSON.parse(readFileSync(path.join(root, "resolved.json"), "utf8"));
if (resolved.sourceCanonicalSha256 !== lock.sourceCanonicalSha256) fail("resolved_canonical_mismatch");
for (const pair of ["work.light/dark", "work.light/light"]) {
  const [surface, theme] = pair.split("/");
  const rows = resolved.contrast.rows.filter((row) => row.profiles.includes("product-image-web") && row.surface === surface && row.theme === theme);
  if (rows.length === 0) fail("contrast_obligations_missing", pair);
  const failed = rows.filter((row) => row.verdict !== "pass");
  if (failed.length > 0) fail("contrast_obligation_failed", `${pair}:${failed[0].pair}`);
}
console.log(JSON.stringify({
  ok: true,
  consumerId: lock.consumerId,
  tokenVersion: lock.tokenVersion,
  sourceCommit: lock.sourceCommit,
  sourceCanonicalSha256: lock.sourceCanonicalSha256,
  files: Object.keys(lock.files).length,
  contrastRows: resolved.contrast.rows.filter((row) => row.profiles.includes("product-image-web") && row.surface === "work.light" && row.theme === "dark").length,
}, null, 2));
