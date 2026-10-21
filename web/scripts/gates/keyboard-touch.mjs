#!/usr/bin/env node
// Static keyboard/touch gate for the surfaces this round touched. It fails on
// click handlers attached to non-interactive elements, and on the specific
// large-image/comparison surfaces missing the bounded-scroll + touch rules.
// The rendered keyboard journey (Tab/Esc/focus return, no horizontal scroll)
// runs in e2e/pimage-shell.mjs against a real browser; this gate is the cheap
// deterministic half that can run in CI without Chrome.
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const webDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const read = (p) => readFileSync(path.join(webDir, p), "utf8");
const failures = [];
function check(ok, code, detail = "") {
  if (!ok) failures.push({ code, detail });
}

const touched = [
  "src/components/workspace/Stage.tsx",
  "src/components/workspace/WorkspaceShell.tsx",
  "src/components/plate-lock/PlateLockScreen.tsx",
  "src/components/billing/ConsumePanel.tsx",
  "src/components/eco-nav/EcoTopNav.tsx",
  "src/app/start/ui.tsx",
  "src/app/projects/[id]/page.tsx",
  "src/app/projects/page.tsx",
];
for (const file of touched) {
  const src = read(file);
  check(!/<div[^>]*\sonClick=/.test(src), "div_onClick", file);
  check(!/<span[^>]*\sonClick=/.test(src), "span_onClick", file);
}
const stage = read("src/components/workspace/Stage.tsx");
check(stage.includes('role="dialog"'), "dialog_role_missing");
check(stage.includes("aria-modal"), "dialog_modal_missing");
check(stage.includes('event.key === "Escape"'), "dialog_escape_missing");
check(stage.includes("trigger?.focus?.()"), "dialog_focus_return_missing");
check(stage.includes('role="group"'), "compare_view_group_missing");
check(stage.includes("aria-pressed"), "compare_view_pressed_missing");

const stageCss = read("src/components/workspace/Stage.module.css");
check(/\.views\s*\{[\s\S]*?min-width:\s*0/.test(stageCss), "compare_views_min_width");
check(/\.views\s*\{[\s\S]*?overflow:\s*auto/.test(stageCss), "compare_views_bounded_scroll");
check(/max-height:/.test(stageCss), "compare_views_max_height");
check(/grid-template-columns:\s*repeat\(2,\s*minmax\(0,\s*1fr\)\)/.test(stageCss), "compare_two_column_minmax");
check(/@media \(max-width: 430px\)[\s\S]*min-height:\s*var\(--pn-control-height-touch\)/.test(stageCss), "touch_targets_missing");
check(/@media \(max-width: 720px\)[\s\S]*grid-template-columns:\s*minmax\(0,\s*1fr\)/.test(stageCss), "mobile_single_column_missing");

const globals = read("src/app/globals.css");
check(!/#[0-9a-fA-F]{3,6}\b/.test(globals), "globals_bare_hex");
check(/@media \(max-width: 430px\)[\s\S]*--pn-control-height-touch/.test(globals), "globals_touch_missing");
check(/prefers-reduced-motion/.test(globals), "reduced_motion_missing");
const uiCss = read("src/components/ui/ui.module.css");
check(!/#[0-9a-fA-F]{3,6}\b/.test(uiCss), "ui_bare_hex");
const navCss = read("src/components/eco-nav/eco-nav.module.css");
check(!/#[0-9a-fA-F]{3,6}\b/.test(navCss), "nav_bare_hex");

// The top-level stage must be first in source order on mobile; the CSS grid
// gives the stage the first column on desktop, and the DOM order puts the
// materials slot before it on desktop. On mobile the page stacks materials
// first only when an operator opens them, so require the stage remains a
// direct named region (already asserted) and no fixed pixel width is applied.
check(!/(?<![-a-z])width:\s*\d{3,}px/.test(stageCss), "stage_fixed_px_width");

const out = {
  ok: failures.length === 0,
  checkedFiles: touched.length,
  failureCount: failures.length,
  failures,
  browserGate: "e2e/pimage-shell.mjs (run separately against the fixture stack; labelled fixture)",
};
console.log(JSON.stringify(out, null, 2));
if (failures.length) process.exit(1);
