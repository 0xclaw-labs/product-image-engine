# AG05-HANDOFF 设计：选图送 Touch Matrix 与 Motion（2026-10-11）

- 负责票：HUI-1745（In Progress，macus）、HUI-2388（In Progress，macus）。HUI-2746 为接续残差，本轮未承接（见第 5 节）。
- 基线：`origin/main` = `57afcb0`。本地 `main` 6219124 是仅本地的 chore 提交，不作为基线。
- 工作树：`/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-ag05-handoff`，分支 `codex/20261011-ag05-handoff`。
- 批准：协调者自批（本次运行没有人类可回答问题）。批准只覆盖本文件列出的切片，不覆盖跨群接口或共享源码改动。

## 1. 决定与理由

### D1 HUI-1745：只补源端正向夹具证据，不登记假回执目标

- 现状（origin/main）：选定后 `POST .../outputs/{id}/receipt` 返回 409 `receipt_target_unregistered`，这是正确的失败关闭。`touch-engine` 在 `touchleads-manifest.json` 中 `receipt_targets: []`，且 `touch-engine` 代码库（`/Volumes/MobileHD/Work/派诺生态/touch-engine`，HEAD `63acba8`）没有任何 receipt 端点（`git grep -i receipt` 无结果）。
- 决定：不在生产登记表中写入任何 touch-engine receipt URL。真实 Touch 回执端点与登记由 AG07 供给。
- 本轮新增的测试 `TestCampaignSelectedReceiptDeliversExactlyOnceToFixtureTarget`（文件 `server/internal/httpapi/campaign_receipt_fixture_test.go`）只在测试进程中登记一个 loopback 桩作为 touch-engine receipt target，证明：活动交接 → 登记成果 → 选定 → 精确回执一次投递（`source_app=product-image`，`target_app=touch-engine`，同一 `event_id`，§8 HMAC 帧由桩验签）→ 重复回执幂等（不产生第二条事件）→ 列表回查为 `delivered`。
- 变异验证：把测试换成未登记登记表后，测试按预期失败，错误为 `409 receipt_target_unregistered`；恢复后通过。
- 理由：这是 Engineering 证据，不是 Touch 真实接收，也不是生产登记。源端代码不需要改动，因为正向路径已经是通用实现。

### D2 HUI-2388：不新增矩阵交接 HTTP 路由

- 现状：`matrixhandoff.Service.Handoff` 在 `publishedContractsInjected` 为假时返回 `ErrDraftNotPersisted`，不落库、不写草稿映射、不发 HTTP（失败关闭，未绕过）。仓库内没有任何 HTTP 路由调用 `matrixhandoff`。
- 决定：本轮不新增路由。
- 理由：构造 `SelectedImage` 需要 `BrandID`、`License`、`ExactSource`、`AIDisclosure`，但产品成果记录（`store.ProjectOutput`、`store.OutputQuality`）中没有这些字段（已 grep 确认）。路由若接受客户端传入的授权或品牌事实，就违反"授权版本保持"和租户隔离要求；路由若编造这些值，则是假证据。因此供给未齐，只能等 AG01（资产授权、品牌/租户事实）与 AG10（已发布草稿契约）。
- 本轮证据：`matrixhandoff` 包既有测试全部通过，确认未注入契约时的拒绝路径（含错误租户、撤权、过期、类型错误、needs_video 非成功等负例）。

### D3 审查

- Spec 与 Standards 两名非作者审查：NOT_RUN。本会话没有可派出的全新非作者子代理，已写入 escalations。
- 代理人不替审查者签字。

### D4 分支与交接

- 固定提交后推送 `codex/20261011-ag05-handoff`，开 draft PR。不合并、不 auto-merge、不部署、不 force-push、不删除 worktree 或分支。
- 提交信息结尾：`Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>`。PR 描述结尾：`🤖 Generated with [Claude Code](https://claude.com/claude-code)`。

## 2. 边界与不做的事

- 不改 `publishedContractsInjected`，不绕过 `ErrDraftNotPersisted`。
- 不写生产登记表，不设真实 receipt URL，不填 `PRODUCT_RECEIPT_KEYS` 或 `PRODUCT_ORDER_INTERNAL_TOKEN` 的真实值。
- 不触碰根 checkout（`/Volumes/MobileHD/Work/派诺生态/product-image-engine`，含 WIP 与 untracked `.DS_Store`）。
- 不做真实 Touch、真实 Matrix、真实 Provider、真实计费、真实 Motion 验收。

## 3. 验收与证据

- `GOWORK=off go test ./... -count=1`（server 目录）：全部包 ok。
- `GOWORK=off go vet ./internal/httpapi ./internal/matrixhandoff`：通过。
- `gofmt -l` 新测试文件：无输出。
- 新测试单独运行：PASS；变异（未登记目标）运行：FAIL 于 `409 receipt_target_unregistered`，证明测试有判别力。

## 4. 剩余项与解除条件

| 项 | 解除条件 | 责任 |
|---|---|---|
| HUI-1745 真实 Touch 回执目标登记 | Touch 提供真实 receipt 端点与 target_id，经 PRODUCT_REGISTRY_MANIFEST 登记（非 example.invalid），并通过真实 HTTP 回执回查 | AG07（Touch HTTP/权限）、AG01（登记部署） |
| HUI-1745 用户正常选择→Touch 目标校验/接收 | Touch 侧接收端与权限就绪，真实 HTTP 与 UI 双端走通 | AG07 |
| HUI-2388 源端 BFF/选择/launch | AG01 供给品牌/租户/授权事实（License、ExactSource、AIDisclosure），并确定 SelectedImage 来源字段 | AG01 |
| HUI-2388 真实草稿落库 | public-ai PR #289（AG10 草稿，HEAD a4b352f4）合入主线且 AG01 接入真实调用方身份；之后把 `publishedContractsInjected` 的注入路径接上并保留失败关闭 | AG10、AG01 |
| HUI-2388 Matrix 真实回查 | 真实草稿 API 可达后，源端按真实 HTTP 回查 plan/item 引用 | AG05 后续切片 |
| 独立 Spec / Standards 审查 | AG00 派出两名非作者审查者审查本分支 HEAD | AG00 |
| HUI-2746 Motion 变体与原选图回流 | AG09 模板与物化能力、权限就绪；本角色后续承接 | AG09、AG05-HANDOFF 后续轮 |

## 5. 未承接：HUI-2746

上游报告要求本角色承接 HUI-2746 残差（正常产品图作品 → Motion 变体 → 真实成品 → 原选图项目回流）。本轮未动，原因：Motion 变体需要 AG09 的真实模板与物化能力，当前 AG09 未合入 main，且没有可用的真实渲染证据。已列为 escalation。
