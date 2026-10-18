# HUI-1702 — C0 合同采用记录

- 合同：`mx-ops-c0/v1`
- 发布 commit：`6ffc90ca`（0xclaw-labs/public-ai，`docs/contracts/mx-ops-c0/v1/`；supersedes `45b32f4b` errata，本仓不消费 `config-command-response`，仅记录）
- 取用日期：2026-10-19
- 校验：37 artifact digest / 56 vectors 由 G01 与 Lead 复核；本仓只读消费下列对象。

## 采用对象与字段

| 对象 | 采用字段 | 用途 |
|---|---|---|
| `job_queue_view` | 全部公开字段（phase/stage/wait_reason/position/jobs_ahead/waiting_jobs_ahead/running_jobs/eta_seconds_min|max/next_eligible_at_unix/policy_source/policy_version/capacity_state/borrowed/as_of_unix）；`internal_debug` 公开投影整键删除 | 展示视频任务卡：等待/队列/可空 ETA/策略版本 |
| `execution_policy` | `template`、`time_window`、`dispatch`、`compute_permission`、`cost_bearing`、`service_level`、`enabled` | 执行偏好与费用承担展示（只交集展示，不做政策判断） |
| `execution_grant` | `cost_bearing`、`price_ref`、`entitlement`、`validity`（只读） | 费用/授权提示；不建钱包、不预扣 |
| `error_taxonomy` | `class`/`code`/`retryable`（映射展示文案） | 失败/等待文案 |

未采用：`model_offering` 写面、`compute_pool`、`config_command`/`config_command_response`（归 G01/G02）。**I2V 能力不属于 C0 合同对象**，见 `docs/mx-ops/1702/i2v-capability.md`（UNKNOWN）。

## 使用方式与边界

- TS 类型副本：`web/src/lib/mx-ops/contract.ts`（`6ffc90ca` 原样拷贝，头部标注 sha；不改语义）。
- fixture：`web/tests/fixtures/mx-ops/`（合同正例）。
- 读取规则：未知=`null`，`0` 是有效测量值；ETA 半未知/倒置判非法；`internal_debug` 不进入公开投影。
- 本仓不新增合同字段/枚举；无 additive 需求。
- 真实模型/GPU/账务：UNKNOWN（留 HUI-3836 窗口）。
