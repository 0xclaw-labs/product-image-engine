# AG05-HANDOFF Plan — 2026-10-11

对应 spec：`docs/superpowers/specs/2026-10-11-ag05-handoff-spec.md`。每步验证后小步提交。

1. **[完成] 侦察**：origin/main=57afcb0；matrixdraft@d8facafc 契约全读（http/store/facts/main + http_test 负例全集）；sdk v0.4.0 consumerlink/ref/digest/permission 全读；pie handoff/campaign_forward/motion/server.go/store 迁移机制全读；web 目录普查。
2. **spec+plan 提交**（本步）。
3. **2388 源端**：
   a. `matrixdraftclient` 包：wire 类型+Create/Get/Receipt+错误映射；wire 单测（httptest 只测协议编解码与错误解析，标注非目的端）。
   b. 迁移 0025 + store `matrixdraftrefs.go`（Save/Get/List + facts 冲突判定）。
   c. httpapi `matrix_draft.go`：POST（门+事实收集+先 pie 冲突判定后目的端 create+落 ref）+ GET（纯回查，零创建）+ 路由 + config env `MATRIX_DRAFT_BASE_URL` + main 装配。
   d. httpapi 集成测试 `matrix_draft_test.go`：livetest 门（env `MATRIX_DRAFT_TEST_BASE_URL` 指向真实服务则跑，否则 skip）+ 常驻单测（BFF 门序：version_not_selected/unusable/facts_conflict/no_matrix_draft 零创建）。
   e. 本机真实服务全错误面实测（scratchpad 脚本，curl 走 BFF），命令+响应入证据包。提交。
4. **1745 回执写回代码路径测试**：`campaign_receipt_forward_test.go`（scratchpad 登记表注册 touch receipt target→全链 delivered→campaign-restore 读回→resend 幂等）；默认登记表负例保持不动。提交。
5. **2746 残差**：go.mod bump v0.4.0（`go get`）；`motionref/consumerlink.go`+测试。提交。
6. **web UI**：`lib/matrix-draft.ts`（BFF 客户端）+ 工程页送矩阵/回查面板（显式按钮；查询不创建）。web 单测（vitest）不回退。提交。
7. **浏览器走查**：真实 matrixdraft+pie server+web；identity stand-in token（chmod600 scratchpad）；1440/390 双轮截图+JSON。提交（证据摘要）。
8. **证据包**：docs/audits/ 三份 + 命令记录。提交。
9. **全量回归**：`go test ./...`（server）+ `npm test`（web）逐字记录，与基线对照不回退。
10. **收口**：push、`gh pr create -R bianjiefilm/product-image-engine`（非 draft，证据索引）、Linear 两票增量评论（写前刷新/写后回读）。

回滚：每步独立 commit，`git revert <sha>` 即可；无 DB 破坏性迁移（0025 纯新增表）。
