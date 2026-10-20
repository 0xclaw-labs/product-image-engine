# HUI-2627 / HUI-3902 产品图 Shell 设计 — GoBoost 设计语言 + 语义 Token + 真实出图流

日期：2026-10-21
作者：`pimage-shell`（派诺生态跨生态收敛轮工程 subagent）
范围：`product-image-engine`（Next.js `web/` + Go `server/` + SQLite）
状态：本地实现设计；真实供应商/生成/计费/部署仍 **NOT_VERIFIED**

---

## 1. 意图与目标

把产品图从“后端表单集合”收敛为 GoBoost 语言的**作品台**：大图优先、编辑器壳层、语义角色配色；
同时修掉真实出图主流程里会打断普通用户的实际缺陷（不是加一条并行通道绕过它们）。

目标：

1. 采用 public-ai `semantic-tokens/v1` 的 `product-image-web` consumer profile（生成产物 + profile aliases + 锁定哈希），页面不再自造颜色/字号/间距角色。
2. 采用 `page-patterns/v1` 的 `creation`、`media_result`、`compare`、`review`、`list`、`workspace` 定义，用 `data-page-pattern` / `data-region` 标注并测试。
3. 把 `/projects/[id]` 与 `/start` 做成 GoBoost 式作品台：左素材/简报、中大幅画面、右检查/费用、底部版本轨；表单退到渐进披露。
4. 修真实缺陷：候选选定后无版本/无回退、原图-候选无大幅对照、上传后引用旧输入、离开等待页回来恢复错记录、额度不足跳到不存在的 `/billing-center`、导航当前应用与降级态硬编码。
5. 加确定性 token 对比度/键盘/触控门脚本并真实运行；不伪造 PASS。
6. 只使用既有资产/任务/钱包/版本权威：不建第二设计系统、第二素材库、第二钱包、第二任务系统。

非目标：

- 不接真实生成供应商；不把冻结样本结果说成任意商品图可用。
- 不实现真实计费/充值 UI；不创建第二 Billing Center。
- 不改 public-ai 仓、不推远端、不发 Linear、不部署。
- 不重做 HUI-2596 的局部修改协议；只把它接进统一作品台与版本账本。

反目标（技术可用但产品失败）：

- 结果看起来像“已采用”但其实只是一个候选选中状态。
- 额度不足时用户被送到 404 或被要求私下充值。
- 大图把比较区撑破、移动端横向滚动。
- 用 fixture 截图冒充真实生成验收。

---

## 2. Brainstorm Q/A（无人类在场，按任务书与仓库证据作答）

> 本轮的 Lead 已预答任务书；以下每个问题都从任务书 + 仓库证据作答，并落成 `Ruling:`。
> 凡是需要业主/平台 owner 才能回答的，明确记 `OPEN`，不偷偷假定。

**Q1 这次是“换皮”还是“换工作方式”？**
A：换工作方式。任务书要求“Big artwork first, not backend form chrome”，且 HUI-2627 的真实流程已存在（冻结样本换背景），缺的是作品台和闭环。只换颜色会把表单墙刷成深色，问题仍在。
`Ruling R1：` `/projects/[id]` 与 `/start` 重构为三栏作品台 + 大 Stage；简报/技术字段进入渐进披露；列表/登录保持简单页面。页面结构变化，业务事实与 fail-closed 文案不削弱。

**Q2 视觉语言取 GoBoost 的什么？**
A：取编辑器语言：暗色分层（sunken/base/panel/raised）、细描边、低阴影、52px 顶栏、左侧工作区栏、中央媒体视口、右侧检查器、底部版本轨、媒体缩略图圆角封顶、克制动效。不复制 GoBoost 的业务组件或画布引擎。
`Ruling R2：` 采用 public-ai `work.light/dark` 语义角色并全局暗色；用 token 的 spacing/radius/type/state/elevation 建立产品图自己的 `ui/` 原语扩展。不新建第二套 token 文件。

**Q3 Token 怎么接？**
A：public-ai 已有 `product-image-web` profile，默认 `work.light/light`，支持 `work.light/dark`；`generated/` 有精确 CSS/JSON/锁清单。任务书要求 pin contract hash。
`Ruling R3：` 使用 producer 生成的**逐字节产物**：`tokens.css`、`tokens.scss`、`resolved.json`、`token-table.md`、`lint-allowlist.json`、`profiles/product-image-web/aliases.css|scss`；目标目录 `web/src/styles/generated/painuo/v1/`；写 `painuo-token-lock/v1` 锁（sourceCommit、canonical SHA、每个文件 SHA、compatibility）。页面只引用 `--pn-*` 与 profile aliases。

**Q4 为什么不用 producer 的 `sync.py --apply`？**
A：实测 `python3 scripts/semantic-tokens/sync.py --target-root … --consumer product-image-web --apply` 返回 `origin_invalid`。原因：profile/ producer 期望 `github.com/bianjiefilm/*`，两个 checkout 的实际 origin 都是 `github.com/0xclaw-labs/*`（仓库迁移后的既成事实）。改 origin 属于远端身份变更，本轮禁止。
`Ruling R4：` 不改 remote、不伪造 producer 运行。写一个**只复制 + 校验哈希**的 adoption 脚本，按 `sync.py` 的 lock 字段形状生成锁，并在 evidence 明确记录 `producer_sync=BLOCKED(origin_invalid)`。这是身份不匹配的受控降级，不是换一套 token 源。

**Q5 主题选亮还是暗？**
A：GoBoost 参考实现是暗色编辑器；产品图 profile 支持 `work.light/dark`，且该组合的 99 条静态对比义务在 producer `resolved.json` 中全部 pass（本仓 gate 会独立复算）。
`Ruling R5：` 全局 `data-pn-surface="work.light" data-pn-theme="dark"`。浅色仍是合同内能力，但本轮不做主题切换 UI（避免新增未验收状态）。

**Q6 “候选选定”与“采用版本”到底谁说了算？**
A：现在 `source-image-runs/{run}/select` 只把 run 标为 selected，不进入任何版本账本；`RevisionRail` 的采用/回退只认 revision ledger。用户选完候选后没有“当前采用版本”，回退无从谈起——这是断链，不是文案问题。
`Ruling R6：` **唯一版本权威 = revision ledger**。候选选定（select）仍是“候选选择”，不冒充采用；新增显式 `POST …/source-image-runs/{runId}/adopt`，把已选定且通过限定检查的合成图写入 ledger 为 `Vn` 并移动 adopted 指针。回退沿用既有 rollback。不建第二版本表。

**Q7 局部修改现在能做什么？**
A：HUI-2596 的真实供应商局部编辑是 UNKNOWN；当前只能做确定性底板 + 锁回主体，并要求用户上传原图/锁区 PNG；`PlanRevision` 在供应商关闭时 `degraded=true` 且不执行。
`Ruling R7：` 不伪造“局部编辑”成功。作品台保留既有“只改这里”入口，明示“页面上还不能直接圈选”“不会整张重画/不扣费”；只有在真实执行成功后才出现新版本。真实供应商局部编辑记 NOT_VERIFIED。

**Q8 额度不足应该去哪？**
A：后端 `topup` handoff 只返回 `entry=billing_center`、`return_target`、`quote_ref`，没有 URL；前端却拼了本地 `/billing-center?...`，而本应用没有该路由 → 死链 404。
`Ruling R8：` 不建本地 Billing Center、不建第二钱包。新增受校验的 `PRODUCT_BILLING_CENTER_URL`（https、无凭据、非 loopback）：配置了才渲染“前往统一充值”外链并带 `return_target`+`quote_ref`；未配置时明确显示“本环境未登记统一充值页地址”并给出可复制的报价引用，绝不渲染死链。真实充值仍 NOT_VERIFIED。

**Q9 导航当前应用能不能写死？**
A：`EcoTopNav` 的应用切换已从 vendored production-manifest 取真实 launch URL（无硬编码域名、失败闭合）；但当前应用 `产品图` 与 `nav_state` 是常量，注册表里的 `product-image` 行（nav-manifest）没被使用。
`Ruling R9：` 保留 production-manifest 作为 launch URL 权威；另 vendored `eco-nav/v1/nav-manifest.json` 作为**当前应用身份/就绪态**权威，`nav_state=degraded` 在顶栏以“内部预览”标注，不宣称生产就绪。生产表缺行时只显示当前应用，不伪造目标。

**Q10 哪些状态必须是大图/真实控件？**
A：creation（渐进步骤）、media_result（大图 + 候选上下文）、compare（原图-候选/版本对照 + 变更范围）、review（检查证据 + 选定/采用分离）、list（工程列表）、workspace（继续工作）。默认密度，移动端 touch。
`Ruling R10：` 六个 pattern 都加 `data-page-pattern` 与关键 `data-region`；移动端 <768px 单栏、主操作可达、无横向溢出。`compare` 的 `comparison_views` 是有界滚动区，大图用 contain + 全屏查看对话框，不缩成缩略图冒充比较。

**Q11 真实流程在这里能跑到哪一步？**
A：本机没有 platform-identity/upload/task/billing（18101–18104 未监听），也没有真实供应商凭证；完整真实栈不可得。仓库有既存 fixture 栈（真实 Next + 真 Chrome + `e2e/fixture-api.mjs`），可验证 UI 状态与交互，但不是生成/计费验收。
`Ruling R11：` 真实流程走查用 fixture 栈，全部标注 `fixture`；真实 provider/generation/billing/deployed env 记 NOT_VERIFIED。不修改 fixture 让状态变绿；新增 UI 所需的 fixture 端点只补“真实契约形状”的只读/零扣费行为，并写测试。

**Q12 对比度/键盘/触控怎么证明？**
A：不能只靠肉眼。producer `resolved.json` 有 99 条 `product-image-web` × `work.light/dark` 对比义务（全 pass），但 consumer 自己的页面组合仍需 gate；键盘焦点已有“报价后聚焦确认”先例。
`Ruling R12：` 新增确定性 gate：① vendored token 哈希/锁校验；② 独立复算我们实际使用的 token 对（文本 ≥4.5、非文本 ≥3）；③ 静态键盘契约（新交互必须原生 button/a/input/select，不用 div onClick）；④ 390px 下新控件命中 ≥44px、无固定宽度溢出；⑤ 浏览器 gate 验证 focus ring/Esc/滚动。任何失败修代码，不豁免除非有 WCAG 行内例外并书面记录。

---

## 3. 设计

### 3.1 信息架构

```
EcoTopNav（产品图 · 内部预览 · 付款主体 · 计费事实 · 应用/账号）
┌────────────────────────────────────────────────────────────────────┐
│ 作品台 /projects/[id]                                               │
│ ┌──────────────┬──────────────────────────────┬─────────────────┐ │
│ │ 素材 + 简报   │  Stage：原图 / 候选 / 比较     │ 检查器           │ │
│ │ 大缩略图      │  大幅 contain + 全屏查看       │ 报价/费用事实     │ │
│ │ 上传/选择     │  候选徽标（候选≠采用）          │ 商品检查/限制      │ │
│ │ 渐进披露      │  compare 视图切换/放大         │ 版本/采用/回退     │ │
│ └──────────────┴──────────────────────────────┴─────────────────┘ │
│ 底部版本轨：V1 … Vn（采用指针、来源、费用、回退）                     │
└────────────────────────────────────────────────────────────────────┘
```

`/start` 是同一作品台的“新建”形态：没有工程时先选照片；有工程时显示 Stage + 继续。`/projects` 是 `list` pattern，保留工程卡/表与“继续做图”。

### 3.2 Token 采用

目标目录（profile `generatedRoot`）：

```
web/src/styles/generated/painuo/v1/
  tokens.css
  tokens.scss
  resolved.json
  token-table.md
  lint-allowlist.json
  aliases.css
  aliases.scss
  painuo-tokens.lock.json
```

- `layout.tsx` 引入 `tokens.css` → `aliases.css` → `globals.css`，`<html data-pn-surface="work.light" data-pn-theme="dark">`。
- `globals.css` 只保留 reset/基线/少量布局工具；颜色全部映射到 `--pn-*`：
  - `--bg` → `--pn-bg-page`；`--card` → `--pn-bg-surface`；`--border` → `--pn-border-default`；
  - `--text` → `--pn-text-primary`；`--muted` → `--pn-text-muted`；`--accent` → `--pn-brand-accent`（链接文本用 `--pn-brand-text`）；
  - 状态四色 → `--pn-status-*`；focus → `--pn-border-focus` / `--pn-focus-*`；
  - 间距/圆角/字号/控制高度 → `--pn-spacing-*` / `--pn-radius-*` / `--pn-type-*` / `--pn-control-height-*`。
- `ui.module.css` / `eco-nav.module.css` / 页面 CSS Module 同样迁移。禁止裸 hex（现有 finish-hygiene 测试）。
- 新增 `web/scripts/tokens/adopt-painuo-tokens.mjs`（复制+校验+写锁）和 `verify-painuo-tokens.mjs`（gate 用）。
- 锁字段严格按 `painuo-token-lock/v1`：`tokenVersion/sourceCommit/sourceCanonicalSha256/consumerId/defaultSurface/defaultTheme/files/aliasesDeprecatedUntil`；`files` 的键是 consumer `target` 路径，值是文件 SHA256。

### 3.3 Page pattern 映射

| pattern | 页面/区域 | 关键 data 标记 | 状态义务 |
|---|---|---|---|
| `workspace` | `/projects/[id]` 壳 | `data-page-pattern="workspace"`；`data-region="current_work|next_steps|recent_context|status|empty|loading|error"` | 继续工作优先；不自动起任务 |
| `list` | `/projects` | `data-page-pattern="list"`；`data-region="filter_context|collection|pagination"` | 空/过滤/错误都解释；collection 有界滚动 |
| `creation` | `/start` 新建态 | `data-page-pattern="creation"`；`data-region="progress_context|input_sections|review_summary"` | 一次一步；提交前复核费用/影响 |
| `media_result` | Stage 结果 | `data-page-pattern="media_result"`；`data-region="media_preview|candidate_context|result_actions"` | 预览≠采用；版本/覆盖未知显式 |
| `compare` | 原图-候选 / 版本对照 | `data-page-pattern="compare"`；`data-region="version_context|comparison_views|change_summary"` | 变更范围明确；不隐藏增量费用 |
| `review` | 检查器/采用区 | `data-page-pattern="review"`；`data-region="subject_preview|evidence_context|decision_controls"` | 决定绑定当前版本；技术 PASS ≠ 全质量 |

### 3.4 真实流程与状态模型

```
选照片/选合法来源
  → 简报（背景方向，渐进）
  → 报价（金额/付款人/到期）          [quoted]
  → 明确确认（唯一花钱按钮）           [confirmed]
  → 等待（可离开；回来按 run 恢复+轮询） [task_linked / pending]
  → 大图结果 + 原图-候选对照 + 检查     [derived: limited_candidate / not_usable_candidate]
  → 选定候选（select，不是采用）        [selected]
  → 采用为版本（adopt，写 revision ledger + 指针） [Vn adopted]
  → 局部修改（真实能力不足时 degraded，不落版本） [Vn+1 candidate]
  → 回退（rollback 指针）
  → 导出（ZIP：图片+检查+快照）/ 回传来源（精确 return target + quote/版本引用）
```

状态事实全部来自服务端：报价、扣费、候选判定、检查轴、版本指针。前端只做投影，不本地推断。

### 3.5 缺陷 → 根因 → 修法（本轮要修）

| # | 真实缺陷（普通用户会遇到） | 根因 | 根修 |
|---|---|---|---|
| D1 | 余额不足点击充值 → `/billing-center?...` 404 | 前端拼了本地不存在路由；后端 handoff 无 URL | 服务端受校验 `PRODUCT_BILLING_CENTER_URL` 进入 handoff；UI 配置才给外链，否则显式“未登记”+可复制报价引用；不建本地钱包页 |
| D2 | 选定候选后无“采用版本”，无法回退 | `select` 只改 run 标志；revision ledger 不知道候选 | 新增 `POST /api/v1/projects/{id}/source-image-runs/{runId}/adopt`：校验 selected + limited + charged，把合成图写入 revision ledger 并 adopt；UI“采用为当前版本”；复用 rollback |
| D3 | 出图流没有原图-候选大幅对照 | PlateLock 只显示候选 `<figure>`；RevisionRail 只比版本 | Stage 增加原图/候选 compare（大图 contain + 全屏 dialog），显示版本/检查/变更范围；大图不撑破布局 |
| D4 | 上传新照片后报价仍用旧的“第一张支持照片” | `uploadAndAttach` 只返回 project；`quotePlate` 默认 `supported[0]` | 返回并回传新 input id；面板显式选中新 input（若在范围）；测试旧/新两张顺序 |
| D5 | 离开等待页回来恢复错记录 | `pickRecoveredRun` 先找任一 confirmed，可能命中旧成功记录，忽略更新 pending | 明确恢复优先级：最新 confirmed+pending > 最新 quoted > 最新；加表驱动测试 |
| D6 | 等待中刷新/返回后没有“还在生成”的可信状态 | 轮询只在 mounted 且 plate pending；离开即停 | 保留 mounted 轮询；恢复后重新进入轮询；Stage 显示服务端阶段与“不重复扣费”；`plate_run` 深链保留 |
| D7 | 候选/采用/已选定语义混在一起 | 文案与动作层级 | 统一文案：`选定这张候选` / `采用为当前版本` / `已采用 Vn`；`候选≠导出≠采用` 标签；review region 绑定版本 |
| D8 | 比较区大图/双栏在窄屏或超大图下溢出 | `.compare-pair img` 只有 max-height，无容器约束 | 容器 `min-width:0`、`aspect-ratio`、`object-fit:contain`、有界滚动；全屏 dialog；390px 单栏 |
| D9 | 当前应用/降级态硬编码；账号菜单没有统一账单入口 | `CURRENT_APP` 常量；无 nav_state | 读 vendored nav-manifest 的当前应用行；`degraded` 标“内部预览”；账号菜单在配置了 billing URL 时给统一账单入口 |
| D10 | 局部修改入口没有和当前候选/版本绑定 | RevisionRail 独立加载，无 base 版本上下文 | 作品台把 RevisionRail 放入 review/版本区并显示当前采用版本；执行前后对照；不伪造成功 |
| D11 | 错误恢复只有一句“待核实” | SourceImagePanel 通用 catch | 按 401/403/404/409/503 给明确恢复动作；报价过期→重新报价；额度不足→统一充值；候选不可用→查看原因/换背景 |

### 3.6 页面/状态矩阵（用于组件测试与 fixture 走查）

| 表面 | loading | empty | error | ready/候选 | 其他 |
|---|---|---|---|---|---|
| `/start` 无工程 | 读取器 skeleton | 选照片 CTA | 401→登录；503→配置缺失 | 上传/选择 | 上传中/失败/不在范围 |
| `/start` 有工程 | 恢复中 | 无输入 → 范围说明 | 读取失败+重试 | 简报+报价 | 报价过期/确认中/等待/结果 |
| `/projects/[id]` | 壳 skeleton | 无素材/无版本 | 工程未打开+返回 | 作品台 | 等待/生成中/候选/已采用/回退 |
| `/projects` | 列表 skeleton | 还没有工程 | 列表失败+重试 | 工程卡/表 | 来源/状态徽章 |
| compare | 图片加载占位 | 无可比版本 | 版本不可读 | 原图-候选 / 版本对照 | 全屏、放大、Esc |
| review/采用 | 版本读取中 | 还没有版本 | 采用失败 | 检查轴+限制+采用/回退 | 未选定/已选定/已采用 |
| 顶栏 | 计费待确认 | 无其他应用 | 注册表不可达→仅当前应用 | 当前应用+可跳应用 | 内部预览/返回来源 |

### 3.7 可访问性与门

- 唯一主操作：报价→确认→采用/导出分别独立；采用与选定不合并。
- 对比度：`work.light/dark` 下文本 ≥4.5、非文本 ≥3；producer 99 条义务全部 pass 作为交叉校验，gate 独立复算。
- 键盘：Tab 到报价/确认；报价可用后焦点落到确认（既有）；compare dialog Esc 关闭且焦点回触发按钮；切换 compare 视图不丢标签。
- 触控：390px 下新按钮/输入 ≥44px；不靠缩小字号。
- 动效：`prefers-reduced-motion` 下关闭 skeleton/过渡。
- 图片：`alt` 说明“原图/候选/版本”；大图不依赖颜色区分。
- 无横向滚动：1280/390 都断言 `scrollWidth <= innerWidth + 1`。

### 3.8 约束与边界

- 不改 `sourceflow` 的 fail-closed 事实链；adopt 只读已扣费、已派生、已选定的合成图。
- 不新建钱包/任务/资产表；billing 仍只读平台事实。
- 不把 fixture 结果写进生产路径。
- 不改 public-ai 仓。
- 真实 provider/generation/billing/deployed env：**NOT_VERIFIED**。

---

## 4. 决策记录（Ruling）

- `Ruling R1` 作品台重构，不是换皮。
- `Ruling R2` 全局 `work.light/dark`，GoBoost 编辑器语言，不复制业务组件。
- `Ruling R3` 逐字节采用 producer 产物 + conforming lock。
- `Ruling R4` producer `sync.py` 因 `origin_invalid` 不可用；adoption 脚本 + 锁 + evidence 记录，不改 remote。
- `Ruling R5` 暗色为唯一本轮主题；浅色保留合同能力，不做切换 UI。
- `Ruling R6` revision ledger 唯一版本权威；select ≠ adopt；新增显式 adopt 桥。
- `Ruling R7` 局部修改真实能力不足时 degraded，不伪造。
- `Ruling R8` 不建第二钱包；billing URL 受校验配置，未配置显式降级。
- `Ruling R9` production-manifest 管 launch URL，nav-manifest 管当前应用身份/状态。
- `Ruling R10` 六 pattern 标注 + 大图 compare + 有界滚动。
- `Ruling R11` fixture 只做 UI 证据；真实栈不可得，NOT_VERIFIED。
- `Ruling R12` 确定性门 + 浏览器门；失败修代码，不豁免。

## 5. OPEN（需要业主/平台 owner，不在本轮擅自决定）

- OPEN-1：真实生成供应商与凭据何时接入；本轮没有。
- OPEN-2：统一 Billing Center 的正式公网 URL 与 return target 白名单；本轮只做受校验配置位。
- OPEN-3：真实局部编辑供应商（HUI-2596 UNKNOWN）；本轮只保留 deterministic plate + 主体锁。
- OPEN-4：产品图在生态注册表的生产 launch URL（当前 production-manifest 缺 product-image 行）；本轮不伪造。
