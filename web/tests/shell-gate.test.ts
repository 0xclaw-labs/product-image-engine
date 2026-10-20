import { execFileSync } from "node:child_process";
import path from "node:path";
import { describe, expect, it } from "vitest";

const web = process.cwd();
function run(script: string) {
  const out = execFileSync(process.execPath, [path.join("scripts", script)], {
    cwd: web,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
  return JSON.parse(out) as { ok: boolean; failures?: unknown[] | number };
}

describe("deterministic shell gates", () => {
  it("token contrast gate recomputes the adopted pairs and passes", () => {
    const out = run("gates/token-contrast.mjs");
    expect(out.ok).toBe(true);
    expect(out.failures).toBe(0);
  });

  it("keyboard/touch static gate passes on the touched surfaces", () => {
    const out = run("gates/keyboard-touch.mjs");
    expect(out.ok).toBe(true);
    expect(out.failures).toEqual([]);
  });
});
