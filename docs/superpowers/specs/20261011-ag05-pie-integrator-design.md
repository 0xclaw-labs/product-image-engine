# AG05 产品图集成与素材成品体验 — 设计（2026-10-11）

分支 `codex/20261011-ag05-pie-integrator`，基于 `origin/main` 57afcb0d5e4ace9f4f1e1c1827ae95541fce27ef。
负责票：HUI-2232（工程质量 Gate）、HUI-2627（UI-R5 I 媒体优先创作工作台）。

## 0. 现场事实（开工实读）

- 根 checkout `product-image-engine` 在本地 `main`（6219124，仅本地提交 chore(identity)，领先 origin/main 1 个提交，落后 163 个）；仅有未跟踪 `.DS_Store`。本次不触碰根 checkout。
- 正式 main 是 `origin/main` = 57afcb0，已合入 #50–#55（AG05 前序：1700 光影诚实化、1745 Campaign 回流、2627 残留收口、2232 source 复用/partial 拒绝、2388 草稿闸、1702 I2V 诚实化）。原 `.worktrees/ag05-*` 与 `ag05/*` 分支均已合并且无提交差异，不是在途作者树，不续接。
- 没有属于本票且干净的在途作者树（`_worktrees` 下 2627 相关树 finish-r1 / fix2 已合入 #48/#49）。按协议新建本树。
- `uifinish-scan`（2625/2619 detector）不在本检出（仓内无 `cmd/uifinish-scan` 或 `internal/uifinish`），正式路由复扫无法在本地运行。
- 无 `gh` 认证问题：`gh` 已登录 bianjiefilm。Go 模块依赖 `github.com/bianjiefilm/public-ai/sdk/go v0.1.1`，需要网络下载。

## 1. 头脑风暴：问题与决定（协调者自答，依据票面与仓内既有决定）

**Q1 本轮范围是什么？**
A：HUI-2627 做可在本地证明的部分：按票面 2026-10-09 更新，先修「login/projects 两处真残留」（login 主按钮、projects 「新建工程」与「创建」按钮的原生 `<button>`），再用 fixture 栈在 1440/390 跑首图→候选→比较→版本→导出链路，留存截图与断言。HUI-2232 做工程基线（Go/Web 全量测试、build）并列出真实 Provider、计费、Campaign 正向、重登恢复等剩余项的 NOT_RUN 地图；不在本轮伪造真实证据。

**Q2 基线用哪一个 main？**
A：`origin/main`（57afcb0）。根 checkout 的本地 identity 提交不属于正式 main，不纳入。

**Q3 2627 的 `form`、`input`、`table` 残留是否一并清？**
A：不清。票面与 fix2 D13/D22 已裁决：raw 控件全清零等于整面重写，会触 HUI-2596 禁区；`<form>` 是回车提交与 `required` 校验的语义载体，改成 DS 会改变行为。本轮只改票面点名的 `<button>` 残留，并以源码级测试防回潮。`unstyled_native_control` 与 `sourceTenantId:null` 误报维持记账。

**Q4 「正式 blind 复评」能否在本轮完成？**
A：不能。2625 正式 blind family review 需要非作者的独立盲走者与 detector 复扫，本会话无可派出的非作者子代理。记 NOT_RUN，写入 escalations，由 AG00 派发。

**Q5 是否调用真实 Provider 或真实 QA 栈？**
A：不调用。票面要求的真实栈 QA 凭据由 root 托管，本会话不读取、不打印任何凭证；H3 视频不在本轮范围。fixture 栈（`web/e2e/fixture-api.mjs`）只证明 UI 状态与交互，证据层标为 `fixture`，不等同验收。

**Q6 TDD 方式？**
A：先写源码级红测（`web/tests/ds-button-residue.test.ts`，登录与工程列表页源不得含原生 `<button>` 且须引入 `Button`），确认红，再替换为 `Button`，确认绿。全量 vitest、`next build` 作为回归。

**Q7 DS `Button` 会不会改变视觉？**
A：`Button` 叠加 `styles.btn` 模块类，`className="primary"` 仍保留，全站 `.primary` 样式不变。仍须在 1440/390 截图核对，若主视觉发生偏移则回退该改动。

**Q8 HUI-2232 本轮能否推进工程代码？**
A：本轮只做基线与缺口地图。工程基线（Go 包测试、Web 测试）若有红灯，记录为基线 FAIL，不删断言、不降门槛；新增工程改动须另行设计计划，不在本轮混入。

**Q9 独立代码审查如何完成？**
A：本会话没有可派出的全新非作者子代理（无 spawn 工具）。Spec 与 Standards 两轴审查记 NOT_RUN 并写入 escalations，不由作者自签。

**Q10 收尾动作是否包括合并、部署？**
A：不包括。按 finishing-branch 的「明确分支交接」：固定提交（含 Co-Authored-By 行），`git push -u origin` 该分支，`gh` 开 draft PR（描述以 Claude Code 署名行结尾）。不合并、不开 auto-merge、不部署、不 force-push、不删除 worktree 或分支。

## 2. 决定与理由汇总

| 编号 | 决定 | 理由 |
| --- | --- | --- |
| D-A1 | 基线 origin/main 57afcb0 | 正式 main；AG05 前序已合入 |
| D-A2 | 不续接 ag05/* 旧树 | 均已合并，无在途提交 |
| D-A3 | 只修 login/projects 原生 `<button>` | 票面点名的真残留；form/input/table 按 D13 记账 |
| D-A4 | 源码红测 + fixture 浏览器证据 | 正式 detector 不在检出；fixture 标注层级 |
| D-A5 | 不跑真实 Provider、不读凭据 | 凭据托管边界；零成本本轮 |
| D-A6 | 正式 blind 与代码审查记 NOT_RUN | 无非作者子代理可派；不自签 |
| D-A7 | 不合并、不部署 | 协议禁止；仅分支交接与 draft PR |
