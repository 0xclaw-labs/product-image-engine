# AG05-GENERATION Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. No subagent tool is available in this session, so the native executor is used. The independent review step is not run here and is escalated to AG00.

**Goal:** 为 HUI-1700（光影场景）与 HUI-1702（图生视频）完成本运行可诚实完成的部分：验证零成本的失败关闭路径，补齐两个纯单元的输入守卫测试，登记供给缺口，并以 draft PR 交接分支。真实光影出图与真实图生视频不在本计划内，依赖外部供给。

**Architecture:** 不改产品行为。新增一个只读性质的测试文件，覆盖 `showcasevideo` 包中运镜归一化与租户隔离的指纹。其余步骤是对已有测试的重跑与证据记录，加上设计文档与计划文档的提交。

**Tech Stack:** Go 1.26（`server` 模块，需 `GOWORK=off`）、`net/http/httptest` 与已有 httpapi 测试夹具；Node 22、Next.js 15、vitest 3、TypeScript 5（`web`）；git、gh（draft PR）。

**Spec:** `docs/superpowers/specs/20261011-ag05-generation-design.md`

## Global Constraints

- 基线 `origin/main` = `57afcb0`。本地 `main` 6219124 不是基线。
- 视频生成只允许 H3-fast。本计划不提交任何视频生成，也不使用 H3-fast 冒充图生视频。
- 不部署，不写生产，不改生产数据库，不做 nginx 或服务重启。
- 不购买、不索取、不读取凭证值，包括 `PRODUCT_LIGHT_MODEL_CREDENTIAL`、`PLATFORM_TASK_TOKEN` 与 `渠道凭证-密钥.txt`。只检查存在性，不打印值。
- 不调用计费扣款。
- 夹具、包测试、CI、健康页不得写成真实生成证据。
- 不冒签满意、采用或真人验收。
- 不 force-push，不合并 PR，不删除分支或工作树。
- commit 结尾必须有一行 `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>`。PR 描述结尾必须有 `🤖 Generated with [Claude Code](https://claude.com/claude-code)`。

## Review Focus

以下五条是规格暗示、但现有测试没有直接钉住、且最可能伤到真实使用者的输入或失败模式，按可能性排序：

1. 光影凭证只配置了 `PRODUCT_LIGHT_MODEL_CREDENTIAL`、没有 `PRODUCT_LIGHT_MODEL_URL` 时，提交门控只看凭证是否非空。预期行为由 AG00 按 D11 裁定。在裁定前，本运行不加测试，记为 NOT_RUN。
2. 运镜字段带空白或大小写不同（如 `" 360 "`、`"SCENE"`），预期前者规范化后接受，后者明确拒绝，不能静默改写成其他运镜。由 Task 4 的测试钉住。
3. 同一工程与同一产品图在不同租户下，展示视频指纹必须不同，否则跨租户会共享失败记录或幂等结果。由 Task 4 的测试钉住。
4. 光影失败记录被后续平台成功回执改回成功，必须被拒绝。由已有 `TestLightSceneFailedRefreshDoesNotRelabel` 钉住，Task 3 运行它。
5. 展示视频的失败记录被供应商填上的输出编号或 `video_url` 改成成功，必须不生效。由已有 `TestShowcaseVideoSupplierAssetDoesNotVerifyFailure` 钉住，Task 3 运行它。

---

### Task 1: 确认基线与工作树状态

**Files:**
- 无代码改动。只读检查。

**Interfaces:**
- Consumes: 工作树 `/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-ag05-generation`，分支 `codex/20261011-ag05-generation`。
- Produces: 确认的 HEAD SHA 与 `git status` 结果，供后续 PR 与 Linear 回写引用。

- [ ] **Step 1: 确认 HEAD 与分支**

Run:
```bash
WT="/Volumes/MobileHD/Work/派诺生态/_worktrees/20261011-ag05-generation"
git -C "$WT" rev-parse --abbrev-ref HEAD
git -C "$WT" rev-parse HEAD
git -C "$WT" merge-base --is-ancestor origin/main HEAD && echo "base ok"
```
Expected: 分支 `codex/20261011-ag05-generation`，HEAD 在 `57afcb0` 之上（设计文档提交后 HEAD 会前进），`base ok`。

- [ ] **Step 2: 确认只有预期的未提交文件**

Run:
```bash
git -C "$WT" status --short
```
Expected: 只出现 `docs/superpowers/specs/20261011-ag05-generation-design.md` 与 `docs/superpowers/plans/20261011-ag05-generation.md` 两个未跟踪文件。`web/node_modules` 是 gitignore 中的目录，不应出现。

---

### Task 2: 重跑测试基线并记录

**Files:**
- 无代码改动。

**Interfaces:**
- Consumes: Task 1 的工作树。
- Produces: 基线测试结果，供 PR 描述中的测试账目引用。

- [ ] **Step 1: Go 基线**

Run:
```bash
cd "$WT/server" && GOWORK=off go test -count=1 ./internal/lightscene/ ./internal/httpapi/ ./internal/config/ ./internal/store/ ./internal/showcasevideo/ ./internal/videofixture/ ./internal/imagemodel/
echo "GO_EXIT=$?"
```
Expected: 7 行 `ok`，`GO_EXIT=0`。此前在 `57afcb0` 已得到相同结果。

- [ ] **Step 2: Web 基线**

Run:
```bash
cd "$WT/web" && npx vitest run --pool=forks
echo "VITEST_EXIT=$?"
npx tsc --noEmit
echo "TSC_EXIT=$?"
```
Expected: `Test Files  53 passed (53)`，`Tests  379 passed (379)`，`VITEST_EXIT=0`，`TSC_EXIT=0`。

---

### Task 3: 零成本失败关闭的证据

**Files:**
- 无代码改动。运行已有测试。

**Interfaces:**
- Consumes: `server/internal/httpapi` 中已有的测试函数。
- Produces: 以函数名标识的 PASS 证据，供 Linear 与 PR 引用。

- [ ] **Step 1: 运行已有的零成本与失败不能改成功的测试**

Run:
```bash
cd "$WT/server" && GOWORK=off go test -count=1 -v -run 'TestLightSceneNoCredentialStaysClosed|TestLightSceneRefreshWithoutCredentialDoesNotCallSupplier|TestLightSceneFailedRefreshDoesNotRelabel|TestShowcaseVideoSubmitWithoutSupplierStaysFailed|TestShowcaseVideoUnknownRefreshDoesNotRegenerate|TestShowcaseVideoSupplierAssetDoesNotVerifyFailure' ./internal/httpapi/ | grep -E '^(--- |ok|FAIL|PASS)'
echo "EXIT=${pipestatus[1]:-$?}"
```
Expected: 六行 `--- PASS: <名字>`，最后 `ok ... ./internal/httpapi`，退出码 0。任何 `FAIL` 都要原样记录，不得删断言。

---

### Task 4: 补两个输入守卫的单元测试（Review Focus 2、3）

**Files:**
- Create: `server/internal/showcasevideo/input_guard_test.go`
- Test: 同一文件

**Interfaces:**
- Consumes: 包 `showcasevideo` 中的 `Open(OpenInput) (Quote, error)`、`Fingerprint(tenant, project, imageID, move string) string`、常量 `Move360`、`MoveScene`，错误 `ErrMoveNotAllowed`。
- Produces: 两个测试函数名，供 Review Focus 引用。不改变产品代码。

- [ ] **Step 1: 写测试文件（characterization，预期直接通过）**

Create `server/internal/showcasevideo/input_guard_test.go`:

```go
package showcasevideo

import "testing"

// 运镜字段的归一化与拒绝：前后空白可以去掉，大小写或其他值必须拒绝，
// 不能静默改写成其他运镜。
func TestOpenNormalizesWhitespaceAndRejectsOtherCasing(t *testing.T) {
	q, err := Open(OpenInput{TenantID: "tenant-a", ProjectID: "prj", ImageID: "img", CameraMove: " 360 "})
	if err != nil {
		t.Fatalf("空白包裹的 360 应被接受: err=%v", err)
	}
	if q.CameraMove != Move360 {
		t.Fatalf("运镜应规范化为 %q, got %q", Move360, q.CameraMove)
	}
	for _, move := range []string{"SCENE", "Scene", "orbit", ""} {
		if _, err := Open(OpenInput{TenantID: "tenant-a", ProjectID: "prj", ImageID: "img", CameraMove: move}); err != ErrMoveNotAllowed {
			t.Fatalf("运镜 %q 应被拒绝, got err=%v", move, err)
		}
	}
}

// 指纹必须绑定租户：同一工程与同一产品图在不同租户下不能共用记录。
// 同一组合的指纹必须稳定，刷新才能回到同一条记录。
func TestFingerprintIsTenantScopedAndStable(t *testing.T) {
	a := Fingerprint("tenant-a", "prj", "img", Move360)
	b := Fingerprint("tenant-b", "prj", "img", Move360)
	if a == b {
		t.Fatalf("不同租户的同一工程与产品图不得共用指纹")
	}
	if a != Fingerprint("tenant-a", "prj", "img", Move360) {
		t.Fatalf("同一组合的指纹必须稳定")
	}
}
```

- [ ] **Step 2: 运行新测试**

Run:
```bash
cd "$WT/server" && GOWORK=off go test -count=1 -v -run 'TestOpenNormalizesWhitespaceAndRejectsOtherCasing|TestFingerprintIsTenantScopedAndStable' ./internal/showcasevideo/
```
Expected: 两个 `--- PASS`，`ok`。如果 FAIL，原样记录为发现，不改产品代码，交 AG00 裁定。

- [ ] **Step 3: 格式检查**

Run:
```bash
cd "$WT/server" && gofmt -l internal/showcasevideo/input_guard_test.go; echo "GOFMT_EXIT=$?"
```
Expected: 无输出，`GOFMT_EXIT=0`。

- [ ] **Step 4: 提交测试文件**

```bash
cd "$WT"
git add server/internal/showcasevideo/input_guard_test.go
git commit -m "test(hui-1702): 展示视频运镜归一化与指纹租户隔离的守卫测试

Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>"
```

---

### Task 5: 核对 D11 的代码事实（Review Focus 1，只记录，不加测试）

**Files:**
- 无代码改动。

**Interfaces:**
- Consumes: `server/internal/config/config.go`、`server/internal/httpapi/lightscene.go`、`server/internal/engwalk/live.go`。
- Produces: 行号级证据，交 AG00 裁定 D11。

- [ ] **Step 1: 用 grep 核对门控语义**

Run:
```bash
cd "$WT/server" && grep -n -E "LightRealModelConfigured|LightModelURL" internal/config/config.go internal/httpapi/lightscene.go internal/engwalk/live.go
```
Expected 包含：`config.go` 中 `LightRealModelConfigured` 只检查 `LightModelCredential`；`httpapi/lightscene.go` 中两处 `LightRealModelConfigured` 门控；`engwalk/live.go` 中凭证与 URL 成对出现。`LightModelURL` 不出现在 `httpapi/lightscene.go` 的提交路径中。

- [ ] **Step 2: 记为 NOT_RUN**

Review Focus 1 的测试在 AG00 裁定 D11 前不加入。结论记为 NOT_RUN，并列入 escalations。

---

### Task 6: 提交设计与计划，推送分支，开 draft PR

**Files:**
- Modify: 无。提交 `docs/superpowers/specs/20261011-ag05-generation-design.md` 与 `docs/superpowers/plans/20261011-ag05-generation.md`。

**Interfaces:**
- Consumes: Task 4 的测试提交。
- Produces: 远端分支与 draft PR URL，供 Task 8 的 Linear 回写引用。

- [ ] **Step 1: 提交文档**

```bash
cd "$WT"
git add docs/superpowers/specs/20261011-ag05-generation-design.md docs/superpowers/plans/20261011-ag05-generation.md
git commit -m "docs(hui-1700,hui-1702): AG05-GENERATION 设计与计划，登记光影与图生视频的供给缺口

Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>"
git rev-parse HEAD
```

- [ ] **Step 2: 推送分支（显式 refspec，不推 main）**

```bash
cd "$WT"
git push -u origin HEAD:codex/20261011-ag05-generation
```
Expected: 远端出现 `codex/20261011-ag05-generation`，没有任何对 `main` 的推送。

- [ ] **Step 3: 开 draft PR**

```bash
cd "$WT"
gh pr create --draft --base main --head codex/20261011-ag05-generation \
  --title "docs(hui-1700,hui-1702): AG05-GENERATION 设计、计划与输入守卫测试" \
  --body "$(cat <<'EOF'
## 范围

- HUI-1700 光影场景与 HUI-1702 图生视频的设计与计划文档，登记供给缺口与解除条件。
- 两个只读性质的输入守卫测试（运镜归一化、指纹租户隔离），不改产品行为。

## 基线

origin/main 57afcb0。

## 测试账目

- go test 7 个包：ok（基线与 Task 3 的已有测试）。
- web vitest：53 个文件、379 个用例通过；tsc --noEmit 通过。
- 新增测试：TestOpenNormalizesWhitespaceAndRejectsOtherCasing、TestFingerprintIsTenantScopedAndStable。

## NOT_RUN 与未完成

- 真实光影出图、真实图生视频、真实计费、真实生成证据：NOT_RUN，依赖外部供给（见设计文档第 5 节）。
- 独立 Spec 与 Standards 审查：NOT_RUN。本会话无非作者子代理，已升级 AG00。
- 凭证门控语义（D11）：待 AG00 裁定，本 PR 不改行为。

## 说明

本 PR 不合并，不部署。两张票保持 In Progress，不标 Done。

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```
Expected: 返回 draft PR URL。记录该 URL 与完整 HEAD。

---

### Task 7: 独立审查（NOT_RUN，升级 AG00）

**Files:**
- 无代码改动。

**Interfaces:**
- Consumes: Task 6 的 PR URL 与完整 HEAD。
- Produces: 对 AG00 的审查请求，内容为 PR URL、HEAD、设计文档路径。

- [ ] **Step 1: 记录未运行的审查**

审查需要两名非作者的全新子代理，分别做 Spec 与 Standards 审查，提示词取自 `AG11-SPEC-非作者完整规格审查.md` 与 `AG11-STANDARDS-非作者工程与恢复审查.md`。本会话没有派遣工具，结论记为 NOT_RUN。

- [ ] **Step 2: 写入 escalations**

写入的内容包括：PR URL、HEAD、设计文档路径、需要裁定的五项（设计文档第 7 节）。

---

### Task 8: Linear 增量回写（HUI-1700、HUI-1702）

**Files:**
- 无代码改动。

**Interfaces:**
- Consumes: Task 6 的 PR URL 与完整 HEAD，Task 3 的测试结果。
- Produces: 两条 Linear 评论，写后回读确认。

- [ ] **Step 1: 写前刷新**

用 `get_issue`（HUI-1700、HUI-1702）与 `list_comments` 读取最新状态，确认 `updatedAt` 与状态未变。如果有新的他人进展，先合并进正文再写。

- [ ] **Step 2: 为两票各写一条增量评论**

评论内容包括：分支 `codex/20261011-ag05-generation`，完整 HEAD，draft PR URL，测试命令与结果（见 Task 2 与 Task 3），NOT_RUN 项，剩余项与解除条件（引用设计文档第 5 节）。评论不改状态、负责人、标签、依赖或关系。

- [ ] **Step 3: 写后回读**

用 `list_comments` 回读两票，确认评论已存在且内容完整。把回读结果记入交接输出。

---

### Task 9: 最终核对与结构化交接

**Files:**
- 无代码改动。

**Interfaces:**
- Consumes: Task 1 至 Task 8 的全部证据。
- Produces: StructuredOutput，状态为 `partial`，并列出所有 NOT_RUN 与解除条件。

- [ ] **Step 1: 确认 HEAD 与远端一致**

```bash
cd "$WT" && git rev-parse HEAD && git ls-remote origin refs/heads/codex/20261011-ag05-generation
```
Expected: 两个 SHA 一致。

- [ ] **Step 2: 确认没有对 main 的写入**

```bash
cd "$WT" && git log --oneline -3 && git -C "$WT" rev-parse origin/main
```
Expected: origin/main 仍为 `57afcb0`。

- [ ] **Step 3: 返回结构化结果**

状态为 `partial`。`blockers` 与 `remaining` 逐项列出设计文档第 5 节的缺口与解除条件。不把"计划完成""PR 已开"当作产品完成。

---

## Self-Review

1. **Spec 覆盖**：设计文档第 3 节的每一条票面要求，都有对应的 Task 或被登记为 NOT_RUN 并给出解除条件。第 5 节的六项缺口都在 Task 9 的 blockers 中出现。
2. **占位检查**：没有 TBD、TODO。Task 5 的 NOT_RUN 是明确的结论，不是未写完的步骤。
3. **类型一致性**：测试中使用的 `Open`、`OpenInput`、`Fingerprint`、`Move360`、`ErrMoveNotAllowed` 与 `showcasevideo.go` 的签名一致。
4. **Review Focus**：五条都有对应的测试或 NOT_RUN 记录。第 1 条是唯一未加测试的，已在 Task 5 说明理由。

**Execution handoff：** 本会话没有子代理工具，协调者自选 native 执行（executing-plans），由协调者自批。独立审查另行升级 AG00。
