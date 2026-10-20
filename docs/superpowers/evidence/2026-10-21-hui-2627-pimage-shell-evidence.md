# HUI-2627 / HUI-3902 产品图 Shell 证据 — 2026-10-21

分支：`eco/hui-2627-pimage-shell`
工作树：`/Volumes/MacintoshHD/派诺生态/wt/product-image-engine/mb-eco-pimage`
规格：`docs/superpowers/specs/2026-10-21-hui-2627-pimage-shell-design.md`
计划：`docs/superpowers/plans/2026-10-21-hui-2627-pimage-shell.md`

结论先行：**本地工程验收 PASS（Go 全量 + web 全量 + 构建 + 确定性 gate + fixture 浏览器双视口旅程）；
真实平台/供应商/计费/部署 NOT_VERIFIED。** 所有浏览器证据层 = `fixture`，不是真实出图或真实付款验收。

---

## 1. 命令与真实结果

| # | 命令 | 结果 |
|---|---|---|
| 1 | `cd server && go test ./...` | **PASS**，全部包 ok，0 fail（本次运行 2026-10-21 03:0x，`httpapi` / `sourceflow` / `platelock` / `store` 等全绿） |
| 2 | `cd web && npm test` | **PASS**，64 test files / **448 tests**，0 fail |
| 3 | `cd web && npm run build` | **PASS**，Next.js production build exit 0（`/projects/[id]` 22.9 kB page，`/start` 6.47 kB page） |
| 4 | `cd web && npm run gate:shell` | **PASS**，exit 0：token lock/contrast + keyboard/touch 静态门（输出 `gate-output.json`） |
| 5 | `cd web && node e2e/pimage-shell.mjs`（fixture 栈） | **PASS** desktop 1280×800 + mobile 390×844；结果 `fixture/pimage-shell-run.json` |
| 6 | `cd web && node scripts/tokens/verify-painuo-tokens.mjs` | **PASS**：consumer=`product-image-web`，tokenVersion=`1.0.0`，7 artifacts 哈希一致，`work.light/dark` 99 条 producer 对比义务全 pass |

### Token 采用读数

- 目标：`web/src/styles/generated/painuo/v1/`，**8 个文件**（7 个 producer 产物 + `painuo-tokens.lock.json`）。
- 合同：`semantic-tokens/v1`，`sourceCanonicalSha256=89ddf356439b8c7997884fc0a2d8066089570f441bb993f248c2d4ebf6c87d72`，`sourceCommit=58a2b760842b33449785016a5257979c21aeab86`，`tokenVersion=1.0.0`。
- Profile：`product-image-web`，`defaultSurface=work.light`、`defaultTheme=light`；本轮全局使用 `work.light/dark`（GoBoost 编辑器语言），浅色仍是合同能力但本轮无切换 UI。
- `tokens.css` 1,033 行 / `aliases.css` 19 行；`resolved.json` 794 KB；`token-table.md` 113 KB。
- 对比：`work.light/dark` × `product-image-web` 共 **99** 条静态义务，producer 结果全 pass；consumer gate 独立复算 **24** 组实际使用 pair，文本 ≥4.5、非文本 ≥3，0 fail。
- 裸 hex：`globals.css` / `ui.module.css` / `eco-nav.module.css` 均 **0** 个六位 hex；`finish-hygiene.test.ts` 全树裸 hex=0 保持通过。
- Producer `sync.py`：**BLOCKED(origin_invalid)**。两个 checkout 的 origin 是 `github.com/0xclaw-labs/*`，profile 期望 `github.com/bianjiefilm/*`。未改 remote；改用 `web/scripts/tokens/adopt-painuo-tokens.mjs` 逐字节复制 + `painuo-token-lock/v1` 锁 + `verify-painuo-tokens.mjs` 哈希门。锁的形状按 producer `sync.py` 的 `_lock` 字段生成。

### Gate 输出（`gate-output.json`）

- token lock：7/7 文件哈希一致，`contrastRows=99`。
- contrast：24/24 pairs pass；最差文本 pair 仍高于 4.5（例如 `--pn-text-inverse` on `--pn-brand-accent` = 5.541），最差非文本 `--pn-border-strong` on `--pn-bg-surface` = 4.656。
- keyboard/touch 静态：8 个本轮文件，0 failure；检查 div/span onClick、dialog role/aria-modal/Esc/focus return、compare 有界滚动/minmax/单栏、44px 媒体规则、reduced-motion、stage 无固定 px 宽。
- 浏览器门（fixture，非静态）：Tab 到报价 → Enter；报价后确认焦点 + visible focus ring；全屏 dialog Esc 关闭且焦点回触发按钮；1280/390 均无横向滚动；390 新控件命中 ≥44px（见第 4 节）。

---

## 2. Token / page-pattern 映射

| 角色 | Token | 使用面 |
|---|---|---|
| 页面底 | `--pn-bg-page` | body / 页面 |
| 卡片/面板 | `--pn-bg-surface` | card / 检查器 |
| 浮层/控件 | `--pn-bg-raised` | 按钮、panel、thumbnail |
| 画布/舞台底 | `--pn-bg-sunken` / `--pn-bg-canvas` | Stage、离线条、技术区 |
| 主/次/弱文字 | `--pn-text-primary/secondary/muted` | 正文、标签、说明 |
| 品牌填充/文字 | `--pn-brand-accent` / `--pn-brand-ink` / `--pn-brand-text` | 主按钮、链接 |
| 状态 | `--pn-status-{success,warning,danger,info}-{fg,weak,solid}` | 检查结果、banner、chip |
| 焦点 | `--pn-border-focus` / `--pn-focus-width` / `--pn-focus-offset` | 全局 focus-visible |
| 间距/圆角/字号 | `--pn-spacing-*` / `--pn-radius-*` / `--pn-type-*` | 全布局 |
| 控制高度 | `--pn-control-height-{compact,default,touch}` | 按钮/输入/触控 |

Page patterns：

- `workspace`：`/projects/[id]` → `WorkspaceShell`，regions `header/status/current_work/recent_context`，一个 h1。
- `list`：`/projects` → `data-page-pattern="list"`，h1 + `header/collection`。
- `creation`：`/start` → `StartScreen`，`progress_context/input_sections/review_summary/media_preview`。
- `media_result`：`Stage` 根，`media_preview/comparison_views/candidate_context/result_actions`。
- `compare`：Stage 内 `comparison_views` 嵌套 `data-page-pattern="compare"`，原图/候选各有标签与全屏 dialog。
- `review`：plate 结果区 `data-page-pattern="review"`，`subject_preview/decision_controls`；检查器 `evidence_context`。

已 vendored：
- `web/src/lib/eco-nav/registry/nav-manifest.json`（public-ai `eco-nav/v1/nav-manifest.json`，sha256 `b401299f…`），记录在 `NAV-SOURCE.txt` / `nav-manifest.sha256`。
- launch URL 仍只用既有 `production-manifest.json`（真实域名，无硬编码客户端域名）。

---

## 3. 真实流程缺陷 → 根因 → 根修（含证据）

| # | 缺陷（普通用户实际遇到） | 根因 | 修法 / 测试 / 证据 |
|---|---|---|---|
| D1 | 余额不足点击“统一充值” → 本地 `/billing-center?...` **404** | 前端拼了不存在的本地路由；后端 handoff 只有 entry/return_target/quote_ref，没有 URL | Go `PRODUCT_BILLING_CENTER_URL` 受校验（https/无凭据/无端口/无 query/fragment/非 loopback）；`topup` handoff 才携带 `billing_center_url`。UI 配置时外链，未配置时明确“本环境未登记统一充值页地址”+可复制报价引用；永不出现 `/billing-center`。测试：`config_test.go` 12 例 URL 策略；`consume_test.go` 未配置不编造、配置后 402 携带；`consume.test.tsx` 链接/无链接两态。 |
| D2 | 选定候选后没有“采用版本”，回退无从谈起 | `select` 只改 run 标志，revision ledger 不知道候选；select 与 adopt 混为一谈 | 新增 `POST /api/v1/projects/{id}/source-image-runs/{runId}/adopt`：要求 selected + limited + charged，把合成 PNG 写入 **唯一 revision ledger** 并移动 adopted 指针；同 run 幂等；不调用供应商、不扣费。UI “选定这张”与“采用为当前版本”分离；采用后版本区显示 V3 已采用，可回退。测试：`TestPlateAdoptIsExplicitIdempotentAndServable`（select 前 409、content 字节一致、重试不追加 V2、无 submit/charge）；web `plate-lock-screen.test.tsx` adopt 按钮态；fixture 浏览器 adopt→V3→rollback V1。 |
| D3 | 出图结果没有原图-候选大幅对照；大图易撑破布局 | 旧 PlateLock 只显示单张候选 `<figure>`；RevisionRail 只比版本 | 新增 `Stage`：原图/候选并排（`minmax(0,1fr)`、contain、有界滚动、移动端单栏），全屏 `role=dialog` + Esc + focus trap/return；390/1280 无横向滚动。测试：`workspace-pattern.test.tsx`；浏览器 compare/全屏/Esc。 |
| D4 | 上传第二张合法照片后，报价可能仍用旧的“第一张支持照片” | `uploadAndAttach` 只返回 project，不返回新 input；`quotePlate` 默认 `supported[0]` | 上传返回 `{projectId,inputId}`；`PhotoUpload.onAttached(projectId,inputId)`；`SourceImagePanel` 用 `preferredInputId` 显式选中；`chooseSupportedInput` 优先用户刚上传的 id。测试：`plate-lock-upload.test.ts` 返回 id；`chooseSupportedInput` 表驱动（新 id/空/缺失）。 |
| D5 | 离开等待页再回来，可能恢复旧成功记录而不是正在生成的请求 | `pickRecoveredRun` 先找任一 confirmed | 恢复优先级：最新 confirmed 且仍在途（plate pending / 无 output）→ 最新 confirmed → 最新记录；加表驱动测试（旧成功 + 新 pending → 新 pending；新 quoted + 旧成功 → 旧成功）。 |
| D6 | 等待中刷新/返回后状态不可信，或误以为要重新扣费 | 轮询只在 mounted 且 pending；恢复状态文案不完整 | 恢复后重新进入 3s 轮询；Stage 显示服务端阶段；文案明确“离开页面不会重复扣费；回来继续读取同一请求”；`plate_run`/`?project=` 深链保留。浏览器：confirm → 离开到 `/start?project=` → 恢复“正在生成…” → fixture advance → 结果。 |
| D7 | 候选/已选定/已采用语义混淆 | 文案和动作层级 | “选定这张”（候选）≠“采用为当前版本”（指针）≠“已采用 Vn”；采用说明固定为“选定只是候选；采用才会把当前版本指针移到这张图”；成功 notice 用 ok tone（修掉“已采用”显示成红色“没有完成”）。测试 + 截图 `adopted`。 |
| D8 | 比较区大图/双栏在窄屏或超大图下溢出 | 旧 `.compare-pair img` 只有 max-height，无 min-width/有界容器 | Stage 容器 `min-width:0`、`grid minmax(0,1fr)`、`max-height:min(62vh,660px)`、`overflow:auto`、`object-fit:contain`；720 单栏；430 触控 ≥44。静态 gate + 浏览器 `scrollWidth<=innerWidth+1`。 |
| D9 | 顶栏当前应用/就绪态硬编码；账号菜单没有计费事实 | `CURRENT_APP` 常量；无 nav_state | vendored `nav-manifest.json` 提供 `product-image` 的 display/nav_state；BFF `/api/eco-nav/launches` 返回 `{current,launches}`；`degraded` 显示“内部预览”；账号菜单/隐藏文本显示“计费事实由平台 billing 只读提供，无第二钱包”。测试：`eco-nav-registry.test.ts`（manifest sha256 + current + BFF）、`eco-nav-shell.test.tsx`（nav-state/billing）。 |
| D10 | 局部修改入口在未开放环境仍可点，点了才 404 | RevisionRail 不知道 `FEATURE_SELECTIVE_REVISION` | 新增 `GET /api/v1/capabilities`（`selective_revision`/`bg_plate_lock`，只读、非授权）+ BFF；RevisionRail 挂载即读，false 时 **先** 显示“局部修改在这个环境还没有打开”，版本仍可查看/采用/回退；首次点击的 404 只作为兜底，不再假装成功。测试：`capabilities_test.go`（只读、不授予写）、`bff.test.ts`（透传/401）、fixture 浏览器断言 `revision-edit-closed`。 |
| D11 | 错误恢复只有一句“待核实” | SourceImagePanel 通用 catch | 保留既有 401/403/404/409/503 映射与重试；新增“编辑未开放”与“充值未登记”的明确恢复动作；报价过期→重新报价；余额不足→统一充值/保留报价引用。部分长尾错误仍只显示安全兜底文案（见 NOT_VERIFIED/remaining）。 |

---

## 4. 浏览器状态矩阵（fixture 层）

| 表面/状态 | desktop 1280 | mobile 390 | 断言 |
|---|---|---|---|
| `/start` 空 | `fixture-pimage-start-empty-desktop.png` | `...-mobile.png` | h1、上传 CTA、无横向滚动 |
| 报价 | `quote-desktop` | `quote-mobile` | ¥0.25、付款人、Tab→报价、Enter、确认焦点 + focus ring |
| 结果对照 | `result-compare-desktop` | `result-compare-mobile` | 原图+候选均加载、候选未采用、检查限制、无横向滚动 |
| 结果（单图/操作） | `result-desktop` | `result-mobile` | 选定/采用分离、导出 href、编辑未开放说明 |
| 已采用 V3 | `adopted-desktop` | `adopted-mobile` | 版本区 V3 已采用、成功 notice 绿色 |
| 回退 V1 | `rolled-back-desktop` | `rolled-back-mobile` | 回到 V1 生效、指针唯一 |
| 工程列表 | 运行中截取 | 同 | `data-page-pattern=list`、h1、无横向滚动 |
| 工程作品台 | 运行中截取 | 同 | `data-page-pattern=workspace`、h1、regions、无横向滚动 |
| 全屏对照 | 运行中断言 | 同 | dialog role/aria-modal、Esc、焦点回触发按钮 |
| 触控 390 | — | 全量按钮/输入/选择/文本域 | 0 个 <44px（行内文字链接按 WCAG 2.5.8 例外不列入控件审计） |

`fixture/pimage-shell-run.json` 记录两个视口 `ok:true`；截图 SHA 由文件本身与 git 提供，不另造哈希。

---

## 5. 双轴 code review

### Standards（本仓库既有标准）

| 严重度 | 发现 | 处理 |
|---|---|---|
| Major | 修 D2 时若另建版本表会形成第二权威 | 已拒绝；只用 revision ledger，select≠adopt，回退复用 `Rollback`。测试锁定。 |
| Major | 修 D1 时若建本地 `/billing-center` 会形成第二钱包/假入口 | 已拒绝；只做受校验外链 + 未登记事实，无本地路由。 |
| Minor | `Stage` 全屏 dialog 最初没有 Tab 焦点陷阱 | 已加 Tab→关闭按钮；Esc + focus return 有浏览器断言。 |
| Minor | “已采用”成功提示被旧 `Alert tone="danger" title="没有完成"` 渲染成失败 | 已按 notice 前缀分 ok/danger；测试防回潮。 |
| Minor | producer lock 的 `aliasesDeprecatedUntil` 未在 consumer verify 中逐字段比对 | 已由 token 测试/verify 覆盖 canonical 与文件哈希；compatibility 字段随锁固定，未单独重构 producer 校验器（记录为可接受剩余）。 |
| Minor | 新增 `Button` 触控尺寸依赖 `ui.module.css` 480 媒体与 Stage 430 媒体两处 | gate 同时检查两处；390 浏览器实测 0 violation。 |

### Spec（任务书/Ruling）

| 要求 | 结论 | 证据 |
|---|---|---|
| GoBoost 语言 + 语义 token | PASS | 8 文件锁 + 0 裸 hex + 24 对比 pair |
| 六个 page patterns | PASS | 组件/静态测试 + 浏览器 DOM |
| 大图优先 / 非后端表单墙 | PASS（创建页仍保留上传/简报表单，但放在左栏；技术/闭源能力折叠） | 截图 + `WorkspaceShell` |
| 真实流程：上传→简报→报价→确认→等待→离开返回→对照→选定→采用→回退→导出/回传 | PASS（fixture 层）；真实 provider N/A | Go adopt 测试 + 浏览器旅程 |
| 局部修改保留非目标区域 | **PARTIAL**：既有 HUI-2596 确定性路径保留，未开放时前置说明；本轮未把 adopted plate 版本作为局部编辑底图（现有编辑器仍要求用户提供原图/锁区 PNG），fixture 中 `FEATURE_SELECTIVE_REVISION=0`，因此浏览器只验证了“未开放”态 | `capabilities` + RevisionRail + NOT_VERIFIED #5 |
| 真实额度不足路由 | PASS（配置/未配置两态）；真实充值 N/A | Go/TS 测试 |
| 无第二设计系统/素材库/钱包/任务 | PASS | code review + grep |
| 对比度/键盘/触控 gate | PASS | `gate:shell` + browser |
| 不伪造 provider/fixture | PASS（fixture 层明确；不把 fixture 当验收） | 本文件 + `pimage-shell-run.json` |

---

## 6. NOT_VERIFIED（明确未验证）

1. **真实生成供应商/模型**：本机没有真实 provider 凭据；`background_plate_lock` 仍限定冻结样本，`not_usable` 仍不可采用；真实任意商品图效果 NOT_VERIFIED。
2. **真实平台服务**：platform-identity/upload/task/billing（18101–18104）未在本机监听；真实 Go server + web + 平台联调未跑。
3. **真实计费/充值**：没有真实人民币扣款、充值、退款；Billing Center URL 未配置真实值；`PRODUCT_BILLING_CENTER_URL` 只做策略测试。
4. **部署环境**：未部署、未推远端、未改 Linear。
5. **真实局部编辑供应商/底图绑定**：HUI-2596 `PartialEdit=false`，真实供应商局部编辑 UNKNOWN；本轮只保证未开放时前置说明与不伪造。局部编辑 UI 仍未以 adopted plate 版本为底图，仍要求用户提供原图/锁区 PNG；fixture 中该能力关闭，只验证了未开放态。
6. **真实跨应用 return target**：fixture 没有订单/活动 binding；真实 return target/回执未联调。
7. **原生/设备/真机**：不适用 Web-only 验收；未跑 native。
8. **真人观感评审**：`human_usefulness=NOT_RUN`；fixture 截图是 UI 证据，不是审美/质量验收。
9. **Producer sync 原生命令**：因 `origin_invalid` 阻塞，使用受控 adoption 脚本；锁字节与 producer 产物一致，但不是 producer CLI 运行回执。

## 7. 运行备注

- fixture 端口：web `32331`，api `32334`（可用 `E2E_WEB_PORT`/`FIXTURE_API_PORT` 覆盖）；`E2E_PROFILE` 指向独立 Chrome profile。
- 浏览器：真实 headless Chrome；`fixture` 层。
- 截图/JSON：`docs/superpowers/evidence/2026-10-21-hui-2627-pimage-shell/`。
- 工作树保留；未 push。

## 8. Integration（本地合并后复跑）

- 锁：`/Volumes/MacintoshHD/派诺生态/.locks/product-image-engine.integrate.lock`（`shlock` 持锁；退出释放）。
- 合并：`git merge --no-ff eco/hui-2627-pimage-shell` → `mb/eco-20261021`，合并提交 `9267163822f20a6c2563e966d8112330d33cfdd9`。
- 合并树 vs feature HEAD：`git diff --stat` **空**（同一代码树）。
- 合并后复跑（真实输出）：
  - `cd server && go test ./...` → PASS（全部包 ok）。
  - `cd web && npm ci` → ok（锁文件无新增依赖）。
  - `cd web && npm test` → **64 files / 448 tests pass**。
  - `cd web && npm run gate:shell` → exit 0（原始输出 `merged-gate-output.json`）。
  - `cd web && npm run build` → exit 0。
- 合并后 `git checkout main`；`mb/eco-20261021` 与 feature 分支保留，工作树保留。
