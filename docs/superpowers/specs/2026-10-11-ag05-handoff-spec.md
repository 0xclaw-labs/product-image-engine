# AG05-HANDOFF Spec（HUI-1745 / HUI-2388 / HUI-2746 残差）— 2026-10-11

实施者：AG05-HANDOFF（Root=AG00 指挥）。brainstorm 由 Root 裁定（R14a–R14e），本 spec 逐条引用。
现场：worktree `/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-agg/ag05-handoff`，分支 `codex/20261011-agg-ag05-handoff`，base=origin/main `57afcb0`（含 #51/#54/#55）。

## 0. 审计刷新（开工实测，非审计快照照抄）

- pie 根 checkout 停在 main=6219124（旧，与 origin/main 分叉）；本切片全部工作在独立 worktree，未触碰根 checkout。
- origin/main=57afcb0：#51（campaign 选择表/restore/选定）、#54（matrixhandoff `publishedContractsInjected=false`→`ErrDraftNotPersisted` 诚实拒绝）、#55 已合。
- `server/internal/matrixhandoff` 仍无任何 HTTP/UI mount（全仓 grep `matrixhandoff.` 零外部调用）——诚实拒绝面完整保留，本切片不动该包。
- 默认登记表 `default_manifest.json`：touch-engine `receipt_targets: []`（`TestDefaultManifestKeepsCampaignReceiptClosed` 钉死），orders 有 `rc-orders-main`。本切片不改该文件。
- public-ai：本机 checkout 在 b192d349（无 matrixdraft，且有人 WIP：sdk/go/motionref consumerlink 改动未提交——不触碰）。matrixdraft 在 `d8facafc`（origin/main 包含它；`git diff d8facafc origin/main -- internal/matrixdraft` 为空 → 与 origin/main 同一版本）。服务本体：`internal/matrixdraft`（store/http/facts）+ `internal/matrixdraft/cmd/matrixdraft`。
- sdk：tag `sdk/go/v0.4.0` → `6065fba6`（consumer-link 合并 PR #236）。pie server/go.mod 现依赖 `github.com/bianjiefilm/public-ai/sdk/go v0.1.1`，注释明示"正式依赖是已发布的 sdk/go tag"。

## 1. Root 裁定（逐题记入，2026-10-11 固定）

- **R14a（2388 目的端）**：目的端=public-ai 真实 matrixdraft 服务（组合版本 `d8facafc`）。本机 loopback scratchpad 跑真实服务；pie 源端选择→创建草稿→收据→回查全链对真实服务集成（非 fixture）。消费方须知：路径不得解码后跳转、撤权只挡新建/重放、license_ref 破坏性必填。公共 Registry/SSO 层属 AG01-2375（未完成）→正式 SSO 旅程 NOT_RUN+解除条件；本地旅程用 identity stand-in（scratchpad，测试 token chmod600，不入日志不入仓）。
- **R14b（1745 回执目标）**：Touch receipt target 注册属 AG07（本批不在）→源端实现活动入口→选定 output/version→receipt target 查找→未注册保持诚实 409 `receipt_target_unregistered`（已测行为不动）→精确回执写回+回查代码路径；正向腿 NOT_RUN+解除条件。
- **R14c（2746 残差）**：Motion 真实执行/渲染归 AG09（本批不在）→消费 sdk/go motionref v0.4.0（tag→6065fba6）consumer-link 契约做源端形状+测试，不伪造 Motion 执行；正向腿 NOT_RUN。2746 已 Done 不重开不改票态。
- **R14d（诚实拒绝面全部保留）**：`version_not_selected`、`ErrDraftNotPersisted`、`receipt_target_unregistered` 是正确行为；正向路径加在其旁。
- **R14e（原票硬要求逐条落测试）**：图片/封面能力类型匹配；质量与许可随版本保持；tenant/brand/payer/source campaign/revision/hash 保留；导航切换不自动创建发布项。

## 2. HUI-2388 设计：源端 BFF → 真实 matrixdraft

### 2.1 新包 `server/internal/matrixdraftclient`

对真实服务 `matrix-draft-request/v1` 契约的薄客户端（wire 契约照抄 `public-ai@d8facafc internal/matrixdraft`）：

- `Create(ctx, Input)` → `POST /v1/matrix-drafts`，头 `X-Matrix-Tenant: <tenant>` 必须等于 body.tenant_id。
- `Get(ctx, tenant, draftID)` / `Receipt(ctx, tenant, draftID)` → `GET /v1/matrix-drafts/{id}` 与 `{id}/receipt`（receipt schema=`matrix-draft-receipt/v1`）。
- 错误面映射（解析 `{"error":{code,message}}`）：`wrong_tenant`(403)、`grant_expired/grant_revoked/grant_unknown`(403)、`license_expired`(403)、`wrong_type`(409)、`old_version`(409)、`hash_conflict`(409)、`idempotency_conflict`(409)、`bad_request`(400)、`store_failed`(500)、`not_found`(404) → 类型化 `*APIError{Status,Code,Message}`。
- 契约常量：source_app 固定 `product-image`；服务端映射 content_type 恒为 `image`（图片能力类型；`video`/`activity` 会被目的端 409 wrong_type——保留该负例为能力类型匹配测试）。

### 2.2 BFF 端点（`server/internal/httpapi/matrix_draft.go`）

- `POST /api/v1/projects/{id}/outputs/{outputId}/matrix-draft`（显式动作；GET 永不创建）。
  门（在既有诚实拒绝之旁，不改它们）：
  1. 工程/输出存在且同租户；
  2. 输出必须是 PNG（能力类型=image）；
  3. campaign 来源：必须先选定该版（复用 `rejectUnselectedCampaignReceipt` → 409 `version_not_selected`，R14d 不动）；
  4. 不可用候选拒绝（与回执同口径 `unusable_candidate`）。
  服务端事实收集（保留性）：
  - `tenant_id`=principal 租户；`payer_id`=principal AccountID；`brand_id`=`brand-`+sha256(tenant)[:12]（**stand-in**：AG01 brandregistry 未接入；记录于审计，解除条件=AG01 品牌注册接入后替换）；
  - `source_project_id`=binding.SourceRef；`campaign_id`=profile.CampaignRef（campaign 来源）；`revision`=该绑定快照序（1 起，旧→新；保证 ≥1 且随来源版本单调）；
  - `asset_id`=output.PlatformAssetID；`asset_hash`=output.ResultSHA256；
  - `grant_ref`=binding ID（**stand-in**：AG01 grant 未接入；本地 grant 事实表 scratchpad 提供 active）；
  - `license_ref`/`license_expires_at`：请求体必填/可选（目的端破坏性必填；来源为对该版本的授权事实，由发起动作携带）。
  持久化：新迁移 `0025_matrix_draft_refs.sql`——`matrix_draft_refs(id, tenant_scope, project_id, output_id, draft_id, license_ref, license_expires_at, quality_verdict, brief_version, source_revision, asset_sha256, facts_json, created_at)`。
  **许可随版本保持**：同 output 重复送（同 version）→ license facts 必须一致；不一致 → 409 `matrix_draft_facts_conflict`（不静默改写）。同事实重复送 → 目的端幂等重放（200 `idempotent_replay`）→ BFF 透传并回读。
- `GET /api/v1/projects/{id}/outputs/{outputId}/matrix-draft`：有 ref 行 → 目的端 `GET` 草稿+receipt 回查，连同源端 facts 一并返回；无 ref 行 → 404 `no_matrix_draft`，且**不向目的端发任何 POST**（导航/查询零创建，R14e-4）。
- 质量随版本保持：ref 行记录 `quality_verdict`（有质量绑定→`limited`；无→`unknown`）+ brief_version + source_revision + hash，回查响应原样返回。

### 2.3 错误面实测表（对真实服务，全部经 BFF 走 HTTP）

| 面 | 预期 |
|---|---|
| 同键同事实重复 create | 目的端 200 + `idempotent_replay=true`，同 draft_id |
| 同键异参（换 license_ref） | pie 409 `matrix_draft_facts_conflict`（先于目的端）；目的端同键异事实独立验证 409 `idempotency_conflict` |
| license 过期（expires_at < now） | 目的端 403 `license_expired` |
| 错 return_target（app≠source_app / 非法 path） | 目的端 400 `bad_request` |
| 错租户（头≠body） | 目的端 403 `wrong_tenant` |
| 能力类型不匹配（content_type=video） | 目的端 409 `wrong_type`（image 才是 product-image 的合法类型） |

## 3. HUI-1745 设计：回执精确写回+回查代码路径

- 已有活动入口（不动）：`POST /handoffs/accept`、`GET /campaign-restore`、`POST /campaign-selection`、`GET /bindings/{id}/return-target`（launch）。
- 新增**测试**（不改产品行为）：`campaign_receipt_forward_test.go` —— scratchpad 测试登记表给 touch-engine 注册 receipt target（指向 eco stub），走完整链：accept→登记 output→选定→`POST .../receipt` 201 delivered→`GET /campaign-restore` 读回 receipt{status:"delivered", target_app:"touch-engine"}→resend 幂等不重投。证明"精确回执写回+回查"代码路径真实存在。
- 正向腿 NOT_RUN：默认登记表 Touch 无 receipt target（`TestDefaultManifestKeepsCampaignReceiptClosed` 保持），生产上选定后回执仍诚实 409 `receipt_target_unregistered`。解除条件：AG07 注册真实 Touch receipt target + PRODUCT_RECEIPTS 配置。
- launch 面：默认登记表 touch-engine 有 launch target（enabled=false 样例域名 .invalid）；`return-target` 端点行为保持。

## 4. HUI-2746 残差：motionref consumer-link 源端形状

- `server/go.mod`：`github.com/bianjiefilm/public-ai/sdk/go v0.1.1 → v0.4.0`（tag→6065fba6）。
- 新文件 `server/internal/motionref/consumerlink.go`：
  - `ConsumerFacts{MotionProjectID, MotionRevisionID, OwnerTenantID, OwnerAccountID, OriginContextRef, Base}`；
  - `ConsumerLink(d Declaration, f ConsumerFacts) (string, error)`：
    - 门：`d.Intact()` 必须通过（诚实未完成声明；`video_generated/verified/dynamic_ad` 等任一为真即拒——不伪造 Motion 执行）；
    - `MotionProjectID` 为空即拒（`ErrNoMotionProject`：没有真实 Motion 工程就不出链，正向腿归 AG09）；
    - 组装 `sdkmotionref.Ref{ProjectID, RevisionID, Owner, OriginApp:"product-image", OriginContextRef, Permissions: 仅 view_render, EditableLevel: flattened, AllowedActions:["view_render"], Version:1}`；`Digest=sdkmotionref.ComputeDigest(ref)`（sdk 域，与 pie 声明 digest 不同域，显式不混用）；
    - `sdkmotionref.BuildConsumerHandoffLink(base, ref)` 出链 `<base>?motionRef=<token>`。
- 测试 `consumerlink_test.go`：形状（URL 前缀+参数名 `motionRef`；token 手工百分号解码回 5 存储键 project_id/revision_id/digest/origin_app/origin_context_ref 与 facts 一致；`ComputeDigest` 自洽）；负例（声明被篡改 digest 不绑定→sdk 拒、伪造 verified=true→拒、空 project id→拒、base 带 query→拒、http 非 loopback→拒）。
- 正向腿 NOT_RUN：无真实 Motion 工程（AG09 未在本批）；不出任何 HTTP 出链端点（避免假旅程）。解除条件：AG09 真实执行/渲染 + Motion Studio project id 可供给后，才可在 API 上出链。

## 5. 浏览器走查（R14a 本地旅程）

Playwright scratchpad（channel:'chrome'）：本地起真实 matrixdraft（127.0.0.1 随机口）+ pie server（127.0.0.1）+ web dev；identity stand-in（scratchpad 测试 token，chmod600，不入日志不入仓）。旅程：登录→活动工程（handoff accept 注入）→登记/选定 output→送矩阵→回查（draft facts+receipt）→回原位（campaign-restore/return-target）。1440 与 390 各一遍，截图+JSON 存 scratchpad，摘要入证据包。

## 6. 证据包

`docs/audits/hui-2388/`、`docs/audits/hui-1745/`、`docs/audits/ap-20261011/`：逐条 PASS/FAIL/NOT_RUN/UNKNOWN/UNSUPPORTED、确切命令、draft_id、hash、截图路径。 Linear：仅 HUI-1745/HUI-2388 各一条增量评论（写前刷新、写后回读，不改状态/负责人，不碰 2746）。

## 7. 允许文件窗口

改/新增：`server/internal/matrixdraftclient/*`、`server/internal/httpapi/matrix_draft*.go`、`server/internal/httpapi/campaign_receipt_forward_test.go`、`server/internal/httpapi/server.go`（仅路由+字段）、`server/internal/config/config.go`（仅新 env）、`server/cmd/server/main.go`（仅装配）、`server/internal/motionref/consumerlink*.go`、`server/internal/store/migrations/0025_*.sql`、`server/internal/store/matrixdraftrefs.go`、`server/go.mod|go.sum`、`web/src/lib/matrix-draft.ts`、`web/src/app/projects/`（送矩阵/回查 UI）、`docs/superpowers/`、`docs/audits/`。
禁改：`server/internal/matrixhandoff/**`、`server/internal/httpapi/campaign_forward.go`（测试行为钉死）、`server/internal/appregistry/default_manifest.json`、根 checkout、其他 worktree、public-ai 本机 checkout（含他人 WIP）。
