# HUI-2627 PImage Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把产品图应用收敛到 GoBoost 编辑器语言 + public-ai 语义 token，并修通“选图→简报→报价→确认→等待→大图比较→选定→采用版本→回退→导出/回传”的真实普通用户流程。

**Architecture:** 页面层新增一个作品台壳（素材栏/Stage/检查器/版本轨），复用既有 SourceImagePanel、RevisionRail、ConsumePanel 的业务事实；Go server 只新增一个“把已选定候选写进 revision ledger 并 adopt”的桥和受校验的统一充值 URL 配置，不新建第二套版本/钱包/任务系统。

**Tech Stack:** Next.js 15 App Router (React 19)、CSS Modules + public-ai `--pn-*` tokens、Go 1.27 net/http + SQLite、Vitest、playwright-core、Node gate 脚本。

**Spec:** `docs/superpowers/specs/2026-10-21-hui-2627-pimage-shell-design.md`

## Global Constraints

- 唯一设计系统：public-ai semantic tokens v1，`product-image-web` profile，`work.light/dark`。
- 唯一版本权威：revision ledger；`select` ≠ `adopt`；不新建版本表。
- 唯一钱包/任务/素材权威：platform-billing / platform-task / platform-upload；不建第二套。
- 所有真实能力不足的路径保持 fail-closed 与诚实文案；不伪造生成/编辑/计费成功。
- 真实 provider/generation/billing/deployed env 必须记 NOT_VERIFIED。
- 不修改 public-ai 仓、不改 git remote、不 push、不部署、不发 Linear。
- 页面在 1280 与 390 视口不得横向滚动；390 新控件命中 ≥44px；文本对比度 ≥4.5、非文本 ≥3。
- fixture 证据一律标 `fixture`；不改 fixture 让状态变绿。

## Review Focus

1. 大图（1024×1280 及更大）在 compare/media_result 中是否撑破两栏或产生横向滚动。
2. 离开等待页再回来，是否恢复的是**正在生成的那次**请求而不是旧成功记录。
3. 选定候选后点“采用”是否真的改变了 adopted 指针，且回退后 `revision content` 与导出指向同一版本。
4. 额度不足时是否还可能渲染出 `/billing-center` 死链；billing URL 未配置时是否仍可完成“返回后重新报价”。
5. 上传第二张合法照片后，报价用的 input 是否就是刚上传的那张。

---

### Task 1: 采用 producer token 产物 + 锁

**Files:**
- Create: `web/scripts/tokens/adopt-painuo-tokens.mjs`
- Create: `web/scripts/tokens/verify-painuo-tokens.mjs`
- Create: `web/src/styles/generated/painuo/v1/{tokens.css,tokens.scss,resolved.json,token-table.md,lint-allowlist.json,aliases.css,aliases.scss,painuo-tokens.lock.json}`
- Create: `web/tests/painuo-tokens.test.ts`
- Modify: `web/src/app/layout.tsx`
- Modify: `web/package.json` scripts `tokens:adopt`, `tokens:verify`, `gate:shell`

**Interfaces:**
- `adopt-painuo-tokens.mjs` reads `PAINUO_TOKENS_SOURCE` (default `../../public-ai`) and writes the generated root; lock shape `painuo-token-lock/v1` with exact producer fields.
- `verify-painuo-tokens.mjs` exits non-zero on hash drift / missing profile artifact.
- `painuo-tokens.test.ts` imports the lock JSON and asserts `consumerId`, `defaultSurface=work.light`, `defaultTheme=light`, file hashes equal bytes on disk, and that `tokens.css` contains all four `work.light` blocks.

**Steps:**
- [ ] Write test asserting lock/artifacts; run → fails because files absent.
- [ ] Write adoption script: verify source `tokens.json` raw SHA `0321ed8e...`, artifact manifest hashes, copy 6 base artifacts + 2 profile aliases, write lock with `sourceCommit=58a2b760842b33449785016a5257979c21aeab86`, `sourceCanonicalSha256=89ddf356...`, `aliasesDeprecatedUntil` = compatibility object.
- [ ] Run `node web/scripts/tokens/adopt-painuo-tokens.mjs`; run test → pass.
- [ ] Wire `import "@/styles/generated/painuo/v1/tokens.css"` and aliases before `./globals.css`; set `<html lang="zh-CN" data-pn-surface="work.light" data-pn-theme="dark">`.
- [ ] Commit.

### Task 2: 语义 token 迁移（globals + ui + eco-nav）

**Files:**
- Modify: `web/src/app/globals.css`
- Modify: `web/src/components/ui/ui.module.css`
- Modify: `web/src/components/eco-nav/eco-nav.module.css`
- Create: `web/src/components/ui/workspace.module.css`
- Test: `web/tests/token-roles.test.ts` (new)
- Test: `web/tests/finish-hygiene.test.ts` (must stay green)

**Interfaces:**
- App aliases: `--bg=var(--pn-bg-page)`, `--card=var(--pn-bg-surface)`, `--border=var(--pn-border-default)`, `--text=var(--pn-text-primary)`, `--muted=var(--pn-text-muted)`, `--accent=var(--pn-brand-text)`, `--focus=var(--pn-border-focus)`, state aliases to `--pn-status-*`.
- `token-roles.test.ts` asserts every app alias resolves to a `--pn-*` role and no `#hex` in CSS files.

- [ ] Write failing test for aliases/no-hex.
- [ ] Replace globals `:root` palette with aliases; convert spacing/radius/typography/controls to `--pn-*`; add reduced-motion guard.
- [ ] Replace hex in `ui.module.css` and `eco-nav.module.css` with tokens; keep class names stable.
- [ ] Run web tests + build; commit.

### Task 3: 作品台壳与 page-pattern 标注

**Files:**
- Create: `web/src/components/workspace/WorkspaceShell.tsx`
- Create: `web/src/components/workspace/WorkspaceShell.module.css`
- Create: `web/src/components/workspace/Stage.tsx`
- Create: `web/src/components/workspace/Stage.module.css`
- Modify: `web/src/app/projects/[id]/page.tsx`
- Modify: `web/src/app/start/ui.tsx`
- Modify: `web/src/components/plate-lock/PlateLockScreen.tsx` (stage slots only; keep behavior)
- Modify: `web/src/components/revision/RevisionRail.tsx` (render inside inspector slot)
- Test: `web/tests/workspace-pattern.test.tsx` (new)

**Interfaces:**
- `WorkspaceShell({ title, status, materials, stage, inspector, versions, headerActions })` renders `data-page-pattern="workspace"` + regions.
- `Stage({ pattern="media_result"|"compare", original, candidate, tabs, children })` renders `data-page-pattern`, bounded `comparison_views`, large `<img>` with `width/height`, full-screen dialog trigger.
- Existing business components stay; project detail moves cards into slots.

- [ ] Write failing SSR test: project workspace markup contains `data-page-pattern="workspace"`, `data-region="current_work"`, `media_result`, `comparison_views`, one `<h1>`.
- [ ] Implement shell + stage CSS with tokens; no fixed widths; 3 columns ≥1100, 1 column <1100.
- [ ] Move project detail materials/brief into left rail, plate stage into center, revision/checks into right rail, versions into bottom track. Preserve all existing data-testid / text contracts.
- [ ] `/start` wraps StartScreen in workspace/creation pattern.
- [ ] Tests + build; commit.

### Task 4: 原图-候选大幅比较 + 全屏查看

**Files:**
- Modify: `web/src/components/workspace/Stage.tsx|module.css`
- Modify: `web/src/components/plate-lock/PlateLockScreen.tsx`
- Test: `web/tests/compare-stage.test.tsx` (new)

**Interfaces:**
- `CompareDialog({ open, left, right, title, onClose, returnFocus })` native `<dialog>` or `role="dialog"` with Esc/close/focus restore; images use `object-fit:contain` and `max-width:100%`.
- Stage shows original (`/api/projects/{id}/inputs/{inputId}/content`) and candidate (`sourcePath(.../content)`) side by side; labels “原图 / 候选（未采用）”; full-screen buttons.

- [ ] Test SSR: compare region has two labelled images, candidate label says 未采用 when not adopted, no `style=`.
- [ ] Test dialog focus return in a DOM-ish test or browser gate.
- [ ] Implement; ensure zero horizontal overflow at 390 via CSS.
- [ ] Commit.

### Task 5: 候选 → 采用版本桥（Go + BFF + UI）

**Files:**
- Modify: `server/internal/httpapi/source_plate.go` (new `handleSourceAdopt`)
- Modify: `server/internal/httpapi/server.go` (route)
- Test: `server/internal/httpapi/source_plate_test.go`
- Create: `web/src/app/api/projects/[id]/source-image-runs/[runId]/adopt/route.ts`
- Modify: `web/src/lib/source-image.ts` (API `adopt`, controller method)
- Modify: `web/src/components/plate-lock/PlateLockScreen.tsx` (button)
- Modify: `web/src/components/source-image/Panel.tsx` (wire action)
- Test: `web/tests/source-image.test.ts`, `web/tests/plate-lock-screen.test.tsx`
- Test: `web/tests/source-image-bff.test.ts` (route exists)

**Interfaces:**
- `POST /api/v1/projects/{id}/source-image-runs/{runId}/adopt` → `{adopted_label, versions}`; 409 unless selected + limited + charged; writes composite into revision ledger as version action `adopt`, origin `plate_lock`, adopts it.
- `SourceImageAPI.adopt(project, run): Promise<SourceRunView>` (returns run; UI then reloads revisions).

- [ ] Go test: select then adopt ⇒ revision state has V1 adopted + content bytes equal composite; adopt before select ⇒ 409; not_usable ⇒ 409; unauthenticated ⇒ 401; no supplier call.
- [ ] Implement handler using `LoadRun(write=true)`, `LoadPlate`, `LoadRevisionLedger`, `PutCandidate` + `Adopt`, `SaveRevisionLedger`.
- [ ] Web API/controller + UI button `采用为当前版本` (disabled unless selected); notice “已采用 Vn”.
- [ ] Tests + commit.

### Task 6: 上传照片身份修复

**Files:**
- Modify: `web/src/components/plate-lock/PhotoUpload.tsx`
- Modify: `web/src/components/source-image/Panel.tsx`
- Modify: `web/src/components/plate-lock/PlateLockScreen.tsx`
- Test: `web/tests/plate-lock-upload.test.ts` (extend)
- Test: `web/tests/plate-lock-screen.test.tsx`

**Interfaces:**
- `uploadAndAttach(file, projectId, fetcher) => Promise<{projectId: string; inputId: string}>` (was string).
- `PhotoUpload.onAttached(projectId, inputId)`.
- Panel passes `onAttached` → sets project, `setInputId(inputId)`, reload; `PlateLockScreen` chooses explicit `inputId` if supported, else first.

- [ ] Test: upload response input id is returned and used; two supported inputs select the newly uploaded one.
- [ ] Implement; preserve error messages; commit.

### Task 7: 恢复顺序 + 等待轮询

**Files:**
- Modify: `web/src/lib/source-image.ts` (`pickRecoveredRun`)
- Modify: `web/src/components/source-image/Panel.tsx` (poll status line + plate_run URL)
- Test: `web/tests/source-image.test.ts`
- Test: `web/tests/source-image-mounts.test.tsx` if needed

**Interfaces:**
- `pickRecoveredRun` priority: newest confirmed non-deleted run with incomplete derivation/pending > newest non-deleted run > newest; deterministic by input order (server newest first), no reliance on sort.
- Stage shows `plateStatusLine(v)` and “离开页面不会重复扣费；回来会继续读取同一请求”.

- [ ] Test new ordering with older confirmed + newer pending; older confirmed + newer quoted.
- [ ] Implement + wire status text; commit.

### Task 8: 额度不足路由修复（Go config + BFF + UI）

**Files:**
- Modify: `server/internal/config/config.go` (`BillingCenterURL`, `BillingCenterEntry()`)
- Modify: `server/internal/httpapi/consume.go` (`topup` includes `billing_center_url`)
- Test: `server/internal/config/config_test.go`, `server/internal/httpapi/consume_test.go`
- Create: `web/src/app/api/billing/center/route.ts` (reads server capabilities? or quote response already has URL)
- Modify: `web/src/components/billing/ConsumePanel.tsx`
- Modify: `web/src/app/start/ui.tsx`
- Test: `web/tests/consume.test.tsx` (extend)

**Interfaces:**
- `PRODUCT_BILLING_CENTER_URL` must be `https://host[/path]`, no credentials/query/fragment, host not loopback; invalid ⇒ treated as unset (do not crash).
- `topup` JSON gains `billing_center_url` (string, `""` when unset).
- UI: if URL present → `<a href={url}?return_target=…&quote_ref=…>前往统一充值</a>`; absent → text “本环境未登记统一充值页地址” + `<code>{quote_ref}</code>`; never `/billing-center`.

- [ ] Go config tests: valid/invalid URL.
- [ ] Go handoff test: URL empty vs configured.
- [ ] Web component test: no `/billing-center` ever; configured/absent states.
- [ ] Implement; commit.

### Task 9: 导航当前应用 + 账号统一账单入口

**Files:**
- Create: `web/src/lib/eco-nav/registry/nav-manifest.json`
- Create: `web/src/lib/eco-nav/registry/NAV-SOURCE.txt`
- Modify: `web/src/lib/eco-nav/registry.ts` (`currentApp()`)
- Modify: `web/src/app/api/eco-nav/launches/route.ts` (return `current`)
- Modify: `web/src/lib/eco-nav.ts` (types)
- Modify: `web/src/components/AppShell.tsx`, `EcoTopNav.tsx`, `eco-nav.module.css`
- Test: `web/tests/eco-nav-registry.test.ts`, `eco-nav-shell.test.tsx`

**Interfaces:**
- `currentApp()` reads `nav-manifest.json` row `app_id=product-image`, returns `{appId, display, navState}`; missing/invalid ⇒ `{appId:"product-image", display:"产品图", navState:"unavailable"}` (no fake available).
- BFF returns `{current, launches}`; AppShell prefers server `current` else fallback.
- EcoTopNav shows “内部预览” when `navState=degraded` (title explains not production-ready).
- Account menu adds “账单与额度” only when `billingCenterUrl` prop is present.

- [ ] Tests for manifest parse/fail-closed + shell render.
- [ ] Implement vendoring exact nav-manifest bytes + pin; commit.

### Task 10: 确定性门脚本（tokens/contrast/keyboard/touch）

**Files:**
- Create: `web/scripts/gates/token-contrast.mjs`
- Create: `web/scripts/gates/keyboard-touch.mjs` (static + optional browser)
- Modify: `web/package.json` `gate:shell`
- Test: `web/tests/shell-gate.test.ts` (unit for contrast math)

**Interfaces:**
- `token-contrast.mjs`: verify lock, recompute WCAG for a declared list of `fg/bg` role pairs used in `globals.css`; print table; exit 1 on <4.5 text / <3 non-text. Also cross-check `resolved.json` rows for `product-image-web`/`work.light`/`dark` are all `pass`.
- `keyboard-touch.mjs`: scan touched TSX for div onClick (fail), ensure new interactive elements use button/a/input/select, ensure CSS mobile rules set 44px for new classes; optional `--browser` mode drives fixture app (focus/ESC/overflow).
- `npm run gate:shell` runs both; outputs JSON to stdout.

- [ ] Write failing test for contrast function (known pairs).
- [ ] Implement gate; run → record real output; fix any failures.
- [ ] Commit.

### Task 11: 全量验证 + 浏览器证据

**Files:**
- Create: `docs/superpowers/evidence/2026-10-21-hui-2627-pimage-shell-evidence.md`
- Create: `docs/superpowers/evidence/2026-10-21-hui-2627-pimage-shell/` (JSON + screenshots)
- Modify: `web/e2e/pimage-shell.mjs` (new focused fixture journey; do not alter existing assertions)

**Steps:**
- [ ] `cd server && go test ./...`; `cd web && npm test`; `npm run build`.
- [ ] `npm run gate:shell` capture output.
- [ ] Build fixture stack + run `node e2e/pimage-shell.mjs` (desktop 1280/mobile 390): upload frozen photo → brief → quote → confirm → navigate away/back → wait → compare dialog → select → adopt → rollback → export; screenshots + no-horizontal-scroll + touch checks.
- [ ] Record fixture-only boundary + NOT_VERIFIED list.
- [ ] Commit.

### Task 12: 双轴 code review + 修复 + 集成

**Steps:**
- [ ] Standards review: tokens/no second system, dead code, tests, error handling, a11y, Go conventions.
- [ ] Spec review: each Ruling/defect D1-D11 has code + test/evidence.
- [ ] Fix findings; rerun gates.
- [ ] Merge into local `mb/eco-20261021` under `.locks/product-image-engine.integrate.lock`; rerun gates on merged result; abort + report on conflict; checkout main; keep worktree.
- [ ] Final report to Lead.
