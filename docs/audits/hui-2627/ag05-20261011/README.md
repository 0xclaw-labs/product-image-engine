# HUI-2627 · AG05 证据包（2026-10-11）

分支 `codex/20261011-ag05-pie-integrator`，基于 origin/main 57afcb0d5e4ace9f4f1e1c1827ae95541fce27ef。
设计与计划见 `docs/superpowers/specs/20261011-ag05-pie-integrator-design.md` 与 `docs/superpowers/plans/20261011-ag05-pie-integrator.md`。

## 证据层级

本包全部为 **fixture 层**（`web/e2e/fixture-api.mjs` 代替 Go API，本地 Chrome，零供应商调用，零扣费）。
它证明 UI 状态、交互与可访问性，不证明真实生成、真实计费或真人采用，不能作为验收证据。

## 本轮改动

- `web/src/app/login/page.tsx`：登录主按钮由原生 `<button>` 改为设计系统 `Button`（`className="primary"` 保留）。
- `web/src/app/projects/page.tsx`：「新建工程」「创建」两处原生 `<button>` 改为 `Button`。
- `web/src/lib/page-census.ts`、`web/tests/page-census.test.ts`：半成品普查账本同步收紧（/login 去掉 raw button；/projects 去掉 raw button）。
- `web/tests/finish-hygiene.test.ts`：raw_button 基线 45 → 42（只许下降的棘轮）。
- `web/tests/ds-button-residue.test.ts`：新增源码级红绿测试，先红（2 failed）后绿。

## 命令与结果（真实执行）

| 命令 | 结果 | 日志 |
| --- | --- | --- |
| `npm ci`（web） | exit 0，added 79 packages | 会话内 |
| `GOWORK=off go build ./...`（server） | exit 0 | `go-build.log` |
| `GOWORK=off go test -count=1 ./...`（server，改动前基线） | exit 0，33 个包 ok | `go-test-baseline.log` |
| `npx vitest run tests/ds-button-residue.test.ts`（改动前） | 2 failed（红，预期） | 会话内 |
| `npx vitest run`（全量，最终） | 54 文件 / 381 测试 全部通过 | 会话内 |
| `npx next build` | exit 0 | `next-build.log` |
| `npx tsc --noEmit` | exit 0 | `tsc.log` |
| `node e2e/hui-2627-finish-r1.mjs`（fixture 走查，1440/390 等五宽） | E2E_EXIT=0，`fails: 0`，ok 25 条 | `run-log.txt`（末尾为 JSON 汇总） |

走查断言摘要（fixture 层）：
- 登录主操作位于首个输入框之上（改动后仍成立）。
- 结果卡三个芯片可读：主体保真 / 通过商品检查（限定范围） / 已选定，尺寸 1024×1280。
- 八个代表页面 × 五个宽度矩阵完成（截图在 `browser/`）。
- axe 扫描：axe-results.jsonl 实收 15 行（页面×视口部分矩阵），全部 0 serious/critical；login 页 @390 腿本轮未采（沿用上轮 finish-r1 的如实标注），补齐前不以「八页 × 1440/390 全覆盖」口径声称。（R-PIE56 P2 口径修正，Root integrator）
- 降级文案诚实：flag 关闭、未传图、下游仅本地引用（「还没收到」），未出现「已送出」。
- HUI-2596 采用/回退往返交互未受影响。
- 视觉核对：`finish-matrix-login-390.png` 登录按钮全宽、位于输入框之上；`finish-matrix-projects-1440.png` 「新建工程」样式与改动前一致（蓝底白字）。

## 未完成与 NOT_RUN

- **detector 正式路由复扫（uifinish-scan）**：NOT_RUN。该工具不在本检出中（仓内无 `cmd/uifinish-scan`、无 `internal/uifinish`）。本轮只能做源码级防回潮，不能宣称 route-scan 结论。
- **2625 正式 blind family review**：NOT_RUN。需要非作者盲走者，本会话无法派出。
- **剩余记账残留（D13/D22 口径，未动）**：`<form>`（login、projects 创建表单）、`<input>`（unstyled_native_control）、`projects` 技术详情内的 `<table>`、`sourceTenantId: null` 字段名误报。整面清零会触 HUI-2596 禁区，不在本轮。
- **真实栈 QA 走查、真实供应商、真实扣费**：NOT_RUN（本轮零成本，凭据由 root 托管，未读取）。
- **分享与导出的真实下载内容**：fixture 层只校验 UI 与按钮状态，真实导出字节未在本轮取证。

## 校验和

`SHA256SUMS` 覆盖本目录除自身外的全部文件，供复核。
