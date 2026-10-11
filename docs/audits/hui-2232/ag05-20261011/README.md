# HUI-2232 · AG05 工程基线与缺口地图（2026-10-11）

分支 `codex/20261011-ag05-pie-integrator`，基于 origin/main 57afcb0d5e4ace9f4f1e1c1827ae95541fce27ef。
本文件只记录本轮真实执行的工程基线，以及票面剩余项的 NOT_RUN 地图。它不是 HUI-2232 的验收结论，票保持 In Progress。

## 本轮真实执行（工程层）

| 项 | 命令 | 结果 |
| --- | --- | --- |
| Go 构建 | `GOWORK=off go build ./...`（server） | exit 0 |
| Go 测试基线 | `GOWORK=off go test -count=1 ./...` | exit 0，33 个包 ok，0 FAIL（日志 `../../hui-2627/ag05-20261011/go-test-baseline.log`） |
| Web 全量测试 | `npx vitest run` | 54 文件 / 381 测试通过 |
| Web 构建 | `npx next build` | exit 0 |
| Web 类型检查 | `npx tsc --noEmit` | exit 0 |

工程层只证明代码与单元/集成测试状态，不等同于真实供应商、真实计费或正式环境。

## 已存在、与票面相关的既有测试（引用，本轮未改）

- `server/internal/httpapi/campaign_forward_test.go`：`TestDefaultManifestKeepsCampaignReceiptClosed`、`TestCampaignRestoreRequiresExplicitSelection`（默认关闭 Campaign 回执；恢复需显式选择）。
- `server/internal/httpapi/entry_test.go`：`TestOrderAndCampaignRestoreAssetsAndExactReturn`。
- `server/internal/httpapi/handoff_test.go`：`TestAcceptHandoffThreeSources`、`TestAcceptHandoffNegatives`。
- `server/internal/httpapi/source_image_test.go`：`TestSourceUpstreamUnknownKeepsAuthorizedOriginalID`、`TestSourceContentExportReadOnlyFreshBillAndDelete`。

这些测试证明的是 fixture/契约层的 fail-closed 与恢复语义。

## 票面剩余项（本轮 NOT_RUN）

1. 普通 Campaign → 选图 → 修改/确认 → 真实适用输出 → 采用/回退/导出 → 重登恢复：需要真实 QA 栈与平台账号，本轮没有读取凭据，未执行。
2. B2 模式真实能力：1700 光影（真实图像编辑能力）、1702 图生视频（需确认支持图片条件的 I2V 供给；h3-fast 仅证明 t2v）、1699 创意/参考编辑：均未在本轮执行真实调用。
3. B5 供应商成本、平台补贴与用户账务分开记录：需要真实账本通道，本轮未执行。
4. 真实 Provider 保真样本（Logo、包装文字、结构、不同背景、电商尺寸）：本轮零供应商调用，未执行。
5. 正式 blind 与真人采用：不在工程范围内，票面已注明独列。
6. 2627 → 2388 送矩阵的真实目的端收到回执：依赖 HUI-2388 与真实 consumer，本轮未执行。

## 边界

- 本轮未触碰根 checkout，未部署，未调用生产服务。
- 本轮 HUI-2232 没有新增工程改动；所有 2232 相关改动在 HUI-2627 证据包中仅涉及页面按钮与普查账本。
