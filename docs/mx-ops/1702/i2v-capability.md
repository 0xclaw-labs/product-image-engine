# HUI-1702 — 商品图 I2V 真实能力证据（MX-OPS 增量前置核查）

- 日期：2026-10-19
- 代码基线：`origin/main @ 57390e6`（worktree `mx-ops/1702-i2v-policy-candidate`）
- 结论：**UNKNOWN — 当前没有可用的 image-conditioned video（I2V）适配器。**
- 处置：如实申报 UNKNOWN + 缺的 Adapter 接缝；**不用 T2V 冒充**；原 I2V 承诺保持未完成。

## 1. 证据（仓内可复核）

| 证据 | 位置 | 事实 |
|---|---|---|
| 图生视频夹具 | `server/internal/videofixture/videofixture.go` | 版本化内存夹具；`provider_calls=0`、`generation_triggered=false`、无视频字节、`status=真实视频未验证`、`real_video=false` |
| 展示视频判定 | `server/internal/showcasevideo/showcasevideo.go` | 包注释明确「本包没有图生视频客户端」；无供应商时 `DecideSubmit` 只回 failed/unknown，不调用、不扣费；拒绝改成文生视频或平台任务 |
| 工程走查 | `server/internal/engwalk/engwalk.go` | 只有 `ModeTextImage = "text_image"`，无视频模态 |
| 适配器搜索 | `grep -rn -iE 'image.to.video\|i2v\|first_frame\|image_url\|image_condition\|init_image' server` | 仅命中 `videofixture` 的测试/文档字符串；无 provider/adapter 实现 |
| 模型/供应商目录 | `server/internal/matrixhandoff/catalog.go`（及 appregistry） | 无任何 image-conditioned video 商品/能力声明 |

复核命令：

```bash
cd server
grep -rn -iE 'image.to.video|i2v|first_frame|image_condition|init_image' . | grep -v _test.go
go test ./internal/i2v/ -count=1
```

## 2. 缺失的 Adapter 接缝

新增 `server/internal/i2v` 定义接缝（**只定义、不实现、不拨号**）：

```go
type Adapter interface {
    Submit(ctx context.Context, req Request) (JobRef, error)   // image_ref + sha256 + prompt + duration/aspect
    Poll(ctx context.Context, ref JobRef) (State, error)       // unknown 保持 unknown
    Fetch(ctx context.Context, ref JobRef) (Artifact, error)   // bytes/URL + sha256 + 真实时长
}
```

`i2v.Probe(modelID)` 当前固定返回：

```json
{
  "status": "UNKNOWN",
  "image_conditioned": false,
  "adapter_ready": false,
  "real_video": false,
  "provider_calls": 0,
  "missing": ["image_conditioned_video_provider","adapter_submit","adapter_poll","adapter_fetch","artifact_hash","subject_consistency_gate","cost_line"]
}
```

负责人接缝：HUI-2295（Catalog/Adapter owner）+ 视频供应商接入方。需先实测：图像条件、时长/比例、主体保持、成本行、失败恢复；只有实证组合才允许启用本模式。

## 3. 为什么不能用 T2V 冒充

- 票面要求「从已授权商品图 → 视频」，核心承诺是**同一商品/主体的连续性与标识保持**；T2V 从文字重新生成主体，无法证明与输入图同一主体。
- `h3-fast` 在本轮只按已实证 T2V 档用于独立视频素材/补拍，不能作为商品图 I2V 证据。
- `showcasevideo` 已明确拒绝把静图、占位 URL、输出编号当成视频；本增量延续该纪律。

## 4. 未完成轴与后续验收（UNKNOWN / NOT_RUN）

- [ ] 真实 I2V 供应商实测：提交 image-conditioned 任务 → 完整解码 → 时间连续/冻结帧/主体结构/Logo 变化检查 → 固定阈值与失败反例。
- [ ] 任务/asset/版本/真实时长/文件 hash/账务同 trace；离场/刷新/unknown/取消/恢复原任务；不重生成/双扣。
- [ ] 产物仅进商家候选素材；发布/奖励另批准。
- 以上全部 `UNKNOWN（未授权实机窗口）`，由 HUI-3836 窗口执行；本仓 fixture 全链路通过**不等价**于真实通过。

## 5. 本增量交付的诚实行为

- 能力接口 `GET /api/v1/showcase-videos/capabilities` 增 `i2v_capability`（UNKNOWN + missing + provider_calls=0）。
- 商家面板显示「图生视频能力未验证」，仍不出现静图/假视频 `<video>/<img>`。
- 新增测试锁死：无 adapter 时 `Submit` 返回 `ErrAdapterUnavailable`；无图片条件返回 `ErrNotImageConditioned`；任何路径 provider 调用为 0。
