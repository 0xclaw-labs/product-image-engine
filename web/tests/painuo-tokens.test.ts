import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

const root = path.join(process.cwd(), "src/styles/generated/painuo/v1");
const read = (name: string) => readFileSync(path.join(root, name));
const sha = (buf: Buffer) => createHash("sha256").update(buf).digest("hex");

const lock = JSON.parse(readFileSync(path.join(root, "painuo-tokens.lock.json"), "utf8"));

describe("public-ai semantic token adoption (product-image-web)", () => {
  it("pins the producer contract and every adopted artifact byte-for-byte", () => {
    expect(lock.schemaVersion).toBe("painuo-token-lock/v1");
    expect(lock.consumerId).toBe("product-image-web");
    expect(lock.defaultSurface).toBe("work.light");
    expect(lock.defaultTheme).toBe("light");
    expect(lock.tokenVersion).toBe("1.0.0");
    expect(lock.sourceCanonicalSha256).toBe(
      "89ddf356439b8c7997884fc0a2d8066089570f441bb993f248c2d4ebf6c87d72"
    );
    expect(lock.sourceCommit).toMatch(/^[0-9a-f]{40}$/);
    expect(Object.keys(lock.files)).toHaveLength(7);
    for (const [target, digest] of Object.entries(lock.files)) {
      expect(sha(read(path.basename(target))), target).toBe(digest);
    }
  });

  it("resolves the product-image profile surface/theme blocks and aliases", () => {
    const css = read("tokens.css").toString("utf8");
    expect(css).toContain('[data-pn-surface="work.light"][data-pn-theme="dark"]');
    expect(css).toContain('[data-pn-surface="work.light"][data-pn-theme="light"]');
    expect(css).toContain("--pn-brand-text:");
    expect(css).toContain("--pn-bg-canvas:");
    const aliases = read("aliases.css").toString("utf8");
    expect(aliases).toContain("--accent: var(--pn-brand-accent);");
    expect(aliases).toContain("--bg: var(--pn-bg-page);");
    expect(aliases).toContain("profile product-image-web");
  });

  it("the adopted resolved.json carries all work.light/dark obligations as pass", () => {
    const resolved = JSON.parse(read("resolved.json").toString("utf8"));
    expect(resolved.sourceCanonicalSha256).toBe(lock.sourceCanonicalSha256);
    const rows = resolved.contrast.rows.filter(
      (row: { profiles: string[]; surface: string; theme: string }) =>
        row.profiles.includes("product-image-web") &&
        row.surface === "work.light" &&
        row.theme === "dark"
    );
    expect(rows).toHaveLength(99);
    expect(rows.filter((row: { verdict: string }) => row.verdict !== "pass")).toEqual([]);
  });
});
