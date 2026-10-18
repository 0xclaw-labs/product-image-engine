# MX-OPS handoff — HUI-1702 产品图展示视频增量

- Group / Ticket: G08 / HUI-1702（原 I2V 承诺保持 In Progress，本票不宣称 Done）
- Repo: 0xclaw-labs/product-image-engine
- Branch: `mx-ops/1702-i2v-policy-candidate`
- Base: `57390e64`（origin/main）
- Head: 本分支最新 commit（PR 记录）
- Worktree: `.worktrees/mx-ops-1702-product-image-engine`
- PR: [#68](https://github.com/0xclaw-labs/product-image-engine/pull/68)（OPEN，不合并/不部署）
- Contract: `mx-ops-c0/v1 @ 6ffc90ca`（`docs/mx-ops/1702/contract-version.md`；无 additive）

## Scope done（逐条对工单 2026-10-18 MX-OPS 增量）

- [x] **I2V 真实能力核查 = UNKNOWN**：`server/internal/videofixture` 是内存夹具（provider_calls=0、无字节）；`showcasevideo` 明确没有图生视频客户端；`engwalk` 只有 `text_image`；仓内无 image-conditioned video adapter。证据与缺口见 `docs/mx-ops/1702/i2v-capability.md`。
- [x] 新增 `server/internal/i2v` Adapter 接缝（Submit/Poll/Fetch + image-conditioned 校验 + artifact hash/主体保持/cost line 缺口清单）；`Probe()` 恒 UNKNOWN、`ProviderCalls=0`；无图片条件 → `ErrNotImageConditioned`；无 adapter → `ErrAdapterUnavailable`；**绝不以 T2V 冒充**。
- [x] 能力接口 `GET /api/v1/showcase-videos/capabilities` 增 `i2v_capability`（UNKNOWN + missing + provider_calls=0 + real_video=false）；原有 `show_video/play_still/video_url` 保持 false/""。
- [x] 队列/等待消费：`web/src/lib/mx-ops/queue.ts` 严格读 `job_queue_view`（null≠0、ETA 可空、internal_debug 删除）；`web/src/lib/showcase-video-production.ts` 展示 phase/等待原因/ETA/下一窗口/策略版本；无投影如实 UNKNOWN。
- [x] 执行偏好与费用承担：`web/src/lib/mx-ops/execution.ts` 只从允许模板∩权益∩窗口∩报价取交集；无数字 priority；免费≠internal；费用只展示，不建钱包（`计费待确认` 保留）。
- [x] 候选回流：`web/src/lib/mx-ops/return.ts` 同 key 重放/异 digest 冲突/晚到不覆盖；展示视频候选素材不自动采用/外发。
- [x] 保留原 In Progress、原 I2V 承诺与在飞归属；不改 Motion/GoBoost/公共服务；跨应用交接仍归 1745。

## Tests

- `cd web && npx vitest run` => **61 文件 / 432 用例全过**（基线 57/398；新增 4 个 mx-ops 测试文件）。
- `cd server && go test ./internal/i2v/ -count=1` => PASS（新增）。
- `cd server && go test -modfile=/tmp/pie.mod ./internal/httpapi/ -run 'ShowcaseVideoCapability' -count=1` => PASS（本机临时 replace 到本地 public-ai SDK；仅验证用，不入库）。
- **Go 基线失败（既有，与本票无关）**：`go build ./...` 失败于 `github.com/bianjiefilm/public-ai/sdk/go v0.4.0` tag 在远端已不存在（`internal/billassemble`、`internal/motionref` 引用）。未在本票修 go.mod/go.sum。

## Code review

- Standards: PASS（gofmt 干净；沿用仓内既有 map-view/源码契约测试惯例；合同类型原样副本）。
  Judgement notes：①`i2v` 只定义接缝、不实现 provider（符合「无 adapter 不伪造」）；②任务视图新增 `job_queue_view:null`/`i2v_capability` 是显式未知而非猜测；③同形内核为独立部署副本。
- Spec: PASS（I2V UNKNOWN + 缺 Adapter 接缝、队列/等待/费用/候选、无 T2V 冒充均有实现与测试）；缺口：真实 I2V/GPU/账务未跑（UNKNOWN），队列投影真实上游未接（显式 null）。

## Blocked / UNKNOWN

1. **真实 I2V：UNKNOWN（无 adapter）**。缺 image-conditioned video provider + Submit/Poll/Fetch + artifact hash + 主体保持 gate + cost line；负责人接缝 HUI-2295/Catalog/Adapter owner；真实联调 `UNKNOWN（未授权实机窗口）`。
2. 真实队列投影/执行策略/权益：未接；用合同 fixture 验证，真实接线 `UNKNOWN`。
3. 真实计费/费用归因：`NOT_RUN`；只展示合同金额/承担方。
4. 生成中离场/unknown/取消/恢复：复用既有 record/previousId 逻辑；fixture 层通过，真实窗口 `NOT_RUN`。
5. PIE Go 全量基线失败（SDK tag 缺失）为既有问题，已在 handoff 记录；若需全量 Go 验证，先由维护者恢复可解析的 SDK 版本。

## Real-integration status

未在真实 GPU/生产验证（UNKNOWN）。验收脚本见 `docs/mx-ops/1702/i2v-capability.md` §4：真实 image-conditioned 提交 → 整段解码/时序/主体/Logo 检查 → 任务/asset/版本/时长/hash/账务同 trace → 离场恢复/不双扣；由 HUI-3836 窗口执行。

## Linear receipt

Lead 统一回报；本代理不写 Linear。
