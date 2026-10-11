# AG05 产品图集成计划（2026-10-11）

依据 `docs/superpowers/specs/20261011-ag05-pie-integrator-design.md`。每步给出可验证结果。

## 阶段 A：基线（真实命令）

1. `cd web && npm ci`，记录退出码。
2. `cd server && go mod download`，记录退出码；`go build ./...`，`go test ./...`（无缓存）。记录每个包的 ok/FAIL。
3. `cd web && npx vitest run`，记录通过数。
4. `cd web && npx next build`，记录退出码。

若基线 FAIL：保留原输出，不改断言，记入 HUI-2232 基线表。

## 阶段 B：HUI-2627 残留修复（TDD）

5. 红：`web/tests/ds-button-residue.test.ts` 运行，确认 login 与 projects 两条失败。
6. 绿：`login/page.tsx` 的 `<button className="primary" type="submit">` 改为 `<Button type="submit" className="primary">`，并引入 `@/components/ui/button`。
7. 绿：`projects/page.tsx` 的「新建工程」「创建」两处 `<button>` 改为 `<Button>`，并引入。
8. 运行 `ds-button-residue.test.ts`，确认全绿；运行全量 vitest，与阶段 A 对账。
9. `next build` 再跑一次。

## 阶段 C：HUI-2627 浏览器证据（fixture 层）

10. 运行 `web/e2e/hui-2627-finish-r1.mjs`（需要 `E2E_OUT`、`FIXTURE_SAMPLES_DIR`，依赖 Chrome 与 `playwright-core`，`axe-core` 需要存在）。
11. 检查输出 JSON 的断言：首图路径、结果卡 chips、比较、版本、导出、降级文案、无横向溢出，1440 与 390。
12. 视觉核对：截图检查 login 与 projects 的主按钮位置是否与修改前一致；如偏移，回退阶段 B。

## 阶段 D：收尾

13. 对 HUI-2232 写 NOT_RUN 地图（真实 Provider、计费、Campaign 正向、重登恢复、正式 blind）。
14. 提交：`git add` 明确列出的文件（不使用 `-A`），commit 信息末尾加 `Co-Authored-By: Claude Haiku 5.5 <noreply@anthropic.com>`。
15. `git push -u origin codex/20261011-ag05-pie-integrator`；`gh pr create --draft`，描述末尾加 Claude Code 署名行。
16. Linear：HUI-2232、HUI-2627 各写一条增量评论（写前读取最新评论，写后回读）。

## 不做

- 不改 HUI-2596 禁区（局部修改交互）。
- 不新增真实 Provider 调用。
- 不改根 checkout。
