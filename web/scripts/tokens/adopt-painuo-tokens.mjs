#!/usr/bin/env node
// Adopt public-ai semantic tokens for the product-image-web profile.
//
// The producer ships `scripts/semantic-tokens/sync.py`, but it refuses this
// checkout with `origin_invalid`: the frozen profile expects
// github.com/bianjiefilm/product-image-engine while this repository's actual
// origin is github.com/0xclaw-labs/product-image-engine. Changing a remote is
// out of scope, so this script performs the same manifest-owned copy with the
// same hash checks and writes the same `painuo-token-lock/v1` shape.
//
// It never regenerates tokens and never invents a palette: every byte comes
// from the producer's committed generated artifacts.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const repoDir = path.resolve(webDir, "..");
const defaultSource = path.resolve(repoDir, "../../../repos/public-ai");
const sourceRoot = path.resolve(process.env.PAINUO_TOKENS_SOURCE || defaultSource);
const contractDir = path.join(sourceRoot, "docs/contracts/semantic-tokens/v1");
const generatedDir = path.join(contractDir, "generated");
const targetRelative = "web/src/styles/generated/painuo/v1";
const targetDir = path.join(repoDir, targetRelative);

const EXPECTED = {
  tokenVersion: "1.0.0",
  rawTokensSha256: "0321ed8e0107e75076837cca45634e4a98ec7c96d2814fcb38dbe3eda4a14553",
  canonicalSha256: "89ddf356439b8c7997884fc0a2d8066089570f441bb993f248c2d4ebf6c87d72",
  profile: "product-image-web",
  defaultSurface: "work.light",
  defaultTheme: "light",
};

const sha = (buf) => createHash("sha256").update(buf).digest("hex");
const read = (p) => readFileSync(p);
function fail(code, detail = "") {
  console.error(`adopt-painuo-tokens: ${code}${detail ? `: ${detail}` : ""}`);
  process.exit(1);
}
function json(p) {
  return JSON.parse(readFileSync(p, "utf8"));
}

if (!existsSync(path.join(contractDir, "tokens.json"))) fail("source_missing", contractDir);
if (sha(read(path.join(contractDir, "tokens.json"))) !== EXPECTED.rawTokensSha256) {
  fail("source_raw_hash_mismatch");
}
const manifest = json(path.join(generatedDir, "artifact-manifest.json"));
if (manifest.sourceCanonicalSha256 !== EXPECTED.canonicalSha256) fail("canonical_hash_mismatch");
if (manifest.tokenVersion !== EXPECTED.tokenVersion) fail("token_version_mismatch");
const tokens = json(path.join(contractDir, "tokens.json"));
const profile = tokens.consumers?.[EXPECTED.profile];
if (!profile) fail("profile_missing");

const owned = profile.ownedFiles.map(({ source, target }) => ({ source, target }));
// The producer's ownedFiles names profile sources as profiles/<id>/aliases.css;
// its generated tree carries those exact bytes.
const copies = owned.map((row) => ({ source: row.source, target: path.basename(row.target) }));
for (const row of copies) {
  const actual = sha(read(path.join(generatedDir, row.source)));
  const expected = manifest.files[row.source];
  if (!expected) fail("artifact_not_manifested", row.source);
  if (actual !== expected) fail("artifact_hash_mismatch", row.source);
}

mkdirSync(targetDir, { recursive: true });
const copyBytes = (source, target) => {
  const bytes = read(path.join(generatedDir, source));
  writeFileSync(path.join(targetDir, target), bytes);
};

const lock = {
  schemaVersion: "painuo-token-lock/v1",
  tokenVersion: manifest.tokenVersion,
  // Probe fact: HEAD of the producer checkout used for this adoption.
  sourceCommit: (() => {
    try {
      return execFileSync("git", ["rev-parse", "HEAD"], { cwd: sourceRoot, encoding: "utf8" }).trim();
    } catch {
      return process.env.PAINUO_TOKENS_SOURCE_COMMIT;
    }
  })(),
  sourceCanonicalSha256: manifest.sourceCanonicalSha256,
  consumerId: EXPECTED.profile,
  defaultSurface: profile.defaultSurface,
  defaultTheme: profile.defaultTheme,
  files: {},
  aliasesDeprecatedUntil: tokens.compatibility,
};

if (!/^[0-9a-f]{40}$/.test(lock.sourceCommit || "")) {
  fail("source_commit_unavailable", "set PAINUO_TOKENS_SOURCE_COMMIT");
}

for (const row of copies) {
  copyBytes(row.source, row.target);
  lock.files[`${targetRelative}/${row.target}`] = sha(read(path.join(generatedDir, row.source)));
}
writeFileSync(path.join(targetDir, "painuo-tokens.lock.json"), JSON.stringify(lock, null, 2) + "\n");
console.log(JSON.stringify({
  adopted: EXPECTED.profile,
  tokenVersion: lock.tokenVersion,
  sourceCommit: lock.sourceCommit,
  sourceCanonicalSha256: lock.sourceCanonicalSha256,
  target: targetRelative,
  files: Object.keys(lock.files).length,
}, null, 2));
