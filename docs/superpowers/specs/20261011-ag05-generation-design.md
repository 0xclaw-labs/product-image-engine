# AG05-GENERATION 设计：HUI-1700 光影场景 / HUI-1702 图生视频

日期：2026-10-11
范围票：HUI-1700（FEAT-0201 光影场景生成）、HUI-1702（FEAT-0203 图生视频）
基线：`origin/main` = `57afcb0`（PR #55 merge）。本地 `main` 6219124 不作基线：它落后 origin/main 163 个提交，并含一个仅本地提交 `chore(identity)`。

## 1. 分类与自批

- 分类：architectural（取重路径）。理由：本职责涉及供应、计费语义与跨群接口。
- 批准方式：本运行无人可回答。协调者以 AG05-GENERATION 身份自批，批准记录即本文件。跨群接口与计费语义的裁定请求已记入第 7 节，交 AG00。
- 本次不改产品行为代码。下文说明理由。

## 2. 基线（实读，2026-10-11）

工作树：`/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-ag05-generation`，分支 `codex/20261011-ag05-generation`，基线 `origin/main` `57afcb0`。
`EnterWorktree` 不可用：启动目录不是 git 仓，原生工具无法在仓外创建树。改用 `git worktree add` 手工路径，目录位于仓外，无需忽略规则。

工单现状（Linear 刷新，写作时）：

- HUI-1700：In Progress，`updatedAt` 2026-10-10T13:40Z。
- HUI-1702：In Progress，`updatedAt` 2026-10-10T13:54Z。

已合并的票面提交：

| 票 | PR | origin/main 中的提交 | 内容 |
|---|---|---|---|
| 1700 | #11 / #12 | f3d7d62 / f8e3f27 | 保真默认，失败不被创意绕过 |
| 1700 | #21 | a27999a（merge），5daa0fa | 无模型凭证时失败关闭 |
| 1700 | #50 | 8eb9c7c（merge），35250ad / eb48334 | 无凭证不调用、不扣费；创意静默入口共用产品轴失败判断 |
| 1702 | #16 | cdf829f（merge），0295f3e | 已有产品图可发起展示视频，无供应商时失败 |
| 1702 | #25 | bb98a07（merge），16f4719 | 图生视频夹具只记录未验证，不生成视频 |
| 1702 | #55 | 57afcb0（merge），a808b4c | 无图生视频供应商时不调用平台任务 |

旧分支 `ag05/hui-1700-light-honest`、`ag05/hui-1702-i2v-honest` 的工作树干净，提交均已在 origin/main，不再在途。本次不续接，只从 origin/main 新开分支。`hui-1700-creative-guard`（cd26c26）未合并，已被 f8e3f27 覆盖的内容，本次不取。

测试基线（均在 `57afcb0`）：

- `cd server && GOWORK=off go test -count=1 ./internal/lightscene/ ./internal/httpapi/ ./internal/config/ ./internal/store/ ./internal/showcasevideo/ ./internal/videofixture/ ./internal/imagemodel/`：7 个包全部 `ok`，退出码 0。
- `cd web && npx vitest run --pool=forks`：53 个文件、379 个用例通过，退出码 0。
- `cd web && npx tsc --noEmit`：退出码 0。

环境：本进程中 `PRODUCT_LIGHT_MODEL_CREDENTIAL`、`PRODUCT_LIGHT_MODEL_URL`、`PRODUCT_LIGHT_MODEL`、`PLATFORM_TASK_BASE_URL`、`FEATURE_LIGHT_SCENE`、`FEATURE_SHOWCASE_VIDEO` 均未设置。只检查了存在性，未读取任何值。

## 3. 票面要求与现状对照

HUI-1700（光影）：

| 票面要求 | 现状 | 证据 |
|---|---|---|
| 正常 UI 选光影 → 报价确认 → 真实生成 → 对照 → 选定/导出 | 未完成。无真实供应商时提交为 `failed`，不调用平台任务、不调用计费 | `TestLightSceneNoCredentialStaysClosed` 计数 `taskCalls=0`、`billCalls=0` |
| 冻结三授权商品样本的光照与主体标准 | 未完成。仓内 `fixtures/frozen-samples` 是合成样本，`Scope = frozen_synthetic_sample_only`，不是授权商品 | `server/internal/fidelitysamples/manifest.go` |
| 同一 Task/Asset/版本/价格记录，供 2232 引用 | 代码路径存在：指纹幂等、`ClaimLightSubmit` 单次认领、平台任务提交。无真实 task 证据 | `handleSubmitLightScene` |
| unknown/刷新/重试不重复调用或双扣 | 测试通过 | `TestLightSceneUnknownDoesNotRegenerate`、`TestLightSceneRefreshWithoutCredentialDoesNotCallSupplier` |
| 实际图像文件/哈希、效果对照、失败样例、真实成本 | 未完成 | 无真实生成 |

HUI-1702（图生视频）：

| 票面要求 | 现状 | 证据 |
|---|---|---|
| 先实测当前模型的图像条件、时长、比例、主体保持 | 未完成。仓内没有任何 I2V 模型、catalog 或 adapter。全仓无 `h3-fast` 引用 | 全仓 grep |
| UI 选图 → 报价 → 真实异步 Task → 播放/下载 | 未完成。`handleSubmitShowcaseVideo` 始终写 `failed`，不调平台任务 | `TestShowcaseVideoUnknownRefreshDoesNotRegenerate` 计数 `calls=0` |
| 整段视频解码、时序、主体结构检查 | 无视频可检 | `videofixture` 仅为夹具 |
| 离场/刷新/unknown/取消/重试恢复，不双扣 | 仅有失败关闭，无真实 task 恢复证据 | — |
| 视频先进候选素材，不外发 | 未实现，依赖真实视频 | — |

## 4. 决定与理由（协调者自答）

- **D1 基线**：`origin/main` `57afcb0`。本地 main 不是正式基线。
- **D2 不续接旧树**：旧 ag05 分支已全部合并，续接只会把旧基线带入新工作。
- **D3 不新建光影或 I2V 供应适配器，不写视频客户端**：没有凭证、URL 与模型。I2V 适配器是跨群接口，属于 2295 与 Catalog/Adapter owner，按规则交 AG00 裁定。`h3-fast` 只允许用于已实证的 t2v 档，不能冒充图生视频。
- **D4 本次不改产品行为代码**：票面未完成项全部依赖真实供给。已有的失败关闭路径和计数测试已经覆盖"无凭证不调用、不扣费"。在没有真实供给时修改行为或 UI 文案，要么是伪造，要么越界到 AG05 消费者体验的验收范围。
- **D5 不冻结三授权样本标准**：授权样本不在仓内。在没有样本的情况下写出"标准"，就是伪造验收依据。缺口登记在第 5 节。
- **D6 不接触凭证**：不购买、不索取、不读取凭证值，包括 `渠道凭证-密钥.txt` 与任何 env 文件。
- **D7 分支交接形式**：提交本设计与计划文档，推送分支，开 draft PR。不合并，不部署，不 force-push。
- **D8 独立审查**：本会话没有可派出的非作者子代理，Spec 与 Standards 审查记 NOT_RUN，升级 AG00，由 AG00 派出 AG11-SPEC 与 AG11-STANDARDS。
- **D9 Linear 回写**：本运行是两票的唯一更新者。只做增量评论（分支、完整 HEAD、PR、测试命令与结果、剩余项与解除条件），写前刷新，写后回读。不改状态、负责人、标签、依赖或关系，不重开 Done。
- **D10 票面引用修正**：票面 1702 写的拒绝理由 `no_actual_i2v_supply` 在仓内不存在。实际文案是 `没有真实视频供应商，未执行生成`（`showcasevideo.DecideSubmit`）。以代码为准。票面引用待修正，记入 remaining。
- **D11 凭证门控语义待裁定**：光影提交门控 `LightRealModelConfigured` 只检查 `PRODUCT_LIGHT_MODEL_CREDENTIAL` 是否非空，不检查 URL。URL 只在 `engwalk/live.go` 与凭证成对使用。提交走平台 task 服务，handler 不直接使用 URL。需要确认：真实供应商密钥是否由平台持有，本服务的凭证变量是否只是存在性开关。记入第 7 节。

## 5. 供给缺口登记

| 缺口 | 影响票 | 解除条件 | 责任方 |
|---|---|---|---|
| 光影图像供应：平台 task 后端的授权供应商接入，凭证与 URL 的真实配置 | 1700 | 授权供应商接入平台 task，并完成一次真实生成 | 2295 凭证供给 owner（本角色不购买、不索取） |
| 三授权商品样本及授权引用 | 1700 | 业务方提供授权样本与 license 引用；据此冻结光照与主体标准 | 业务方，经 AG00 |
| I2V 模型与 adapter：图像条件、时长、比例、主体保持的实测，接入视频 adapter | 1702 | 实测通过，视频 adapter 接入平台任务，计费与恢复契约齐备 | Catalog/Adapter owner，待 AG00 指定 |
| 真实计费：扣费回执 | 1700、1702 | 计费接通，`charged` 有真实回执 | 计费 owner（票 1991 线） |
| 真实生成证据：UI 报价 → 真实生成 → 对照 → trace | 1700、1702 | 授权环境下一次真实正例，且费用与产物可追溯 | QA/浏览器角色，需授权 |
| 独立代码审查 | 1700、1702 | AG00 派出两名非作者审查者 | AG00 |

## 6. 边界与禁止

- 不部署，不写生产，不改生产数据库。
- 不调用计费扣款，不提交真实视频生成，不使用 `h3-fast`。
- 不购买、不索取、不读取凭证值。
- 不把夹具、包测试、CI、健康页写成真实生成证据。
- 不冒签满意、采用或真人验收。

## 7. 需 AG00 裁定的事项（escalations）

1. I2V adapter 的接缝与负责人：谁拥有 Catalog/Adapter 与 2295 的图生视频接入。
2. 光影凭证语义（D11）：`PRODUCT_LIGHT_MODEL_CREDENTIAL` 在产品服务中是存在性开关还是真实密钥；URL 配对是否必须进入提交门控。
3. 三授权商品样本的来源与授权引用。
4. 独立审查：派出 AG11-SPEC 与 AG11-STANDARDS 审查 draft PR 的 HEAD。
5. 票面 1702 的拒绝理由引用需要修正（D10）。

## 8. 自审（spec self-review）

- 占位检查：无 TBD、TODO，未完成项都标了责任方与解除条件。
- 一致性：第 2 节与第 3 节的测试证据一致；D3 与第 5 节的 I2V 责任方一致。
- 范围：本文件只做设计与证据登记，不引入产品行为改动。
- 歧义：D10 与 D11 已明确为"以代码为准"与"交 AG00 裁定"，不留两种解释。
