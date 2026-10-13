# HUI-2232 收口复验计划（2026-10-08，Gate Closure）

> **For agentic workers:** 本计划由收口子智能体内联执行（业主不在线，无审批等待）。
> 复验类任务用 checkbox 跟踪；如发现窄缺陷转 TDD 窄修。

**Goal:** 在当前 main（8b231ac）逐条实测 2026-09-28 Gate 五条 checklist，输出六层结论、链路断点清单与收口建议。

**Architecture:** 不重写生成引擎。以「当前 main gates 复跑 + 10-04 M1-M3 真实栈证据复核 + 远端 QA 栈最小探针 + 样本集内容级核验」四路证据交汇判定；每判定附可复跑命令与产物路径。

**Tech Stack:** Go 1.x（GOWORK=off）、Next.js web、Playwright driver（web/e2e）、Linear GraphQL 只读。

**Spec:** 票面 2026-09-28 当前 Gate 五条；docs/superpowers/plans/2026-10-04-hui-2232-real-acceptance.md（前置真实栈验收）。

## Global Constraints

- 主 checkout 只读；一切写在 worktree /Volumes/MobileHD/Work/派诺生态/_worktrees/hui-2232-close（branch hui-2232-close）。
- GOWORK=off；代理 http://127.0.0.1:7890；TMPDIR=/private/tmp；本地端口仅 32410-32419。
- Linear 只读；HUI-2627 不碰；HUI-2970/2974-2978/2987/2989/2990 只读。
- 不改写记录器现值；fixture 绿不关质量 Gate；密钥不入日志；QA 额度非现金；单票供应商花费 ≤¥50。
- N/A 与 UNKNOWN 不混同；每判定附证据路径。

---

### Task 1: 当前 main 工程门复跑 + 记录器现值核验

- [ ] `GOWORK=off go test ./...`（server，count=1）、`go vet ./...`、`go test -race ./internal/{engwalk,qualitygate,config,httpapi}/`
- [ ] web `npm test`/`tsc --noEmit`（如脚本存在）
- [ ] 记录器现值：grep engwalk `LivePassed`、qualitygate `generation_passed/billing_passed/production_authorized` 默认值与判定条件（只读源码+测试输出，不调用写路径）
- [ ] 产物：evidence/HUI-2232/engineering/gates-main-20261008.log

### Task 2: 冻结授权商品集内容级核验（checklist-1）

- [ ] 读 fixtures/frozen-samples/v1 manifest：每个样本覆盖条件、阈值、错误样例（负例）
- [ ] `server/cmd/samplegen` check 门复跑；SHA256 钉住复核
- [ ] 判定五条件覆盖：包装文字/Logo、明确结构、复杂材质、不同背景、电商尺寸——逐样本给可复验阈值
- [ ] 产物：evidence/HUI-2232/engineering/frozen-samples-audit.md

### Task 3: 10-04 真实栈证据完整性复核

- [ ] SHA256SUMS -c 校验 23MB 证据包
- [ ] 读 real-e2e/run-record、charge-ledger.jsonl（intent/observed 对账）、mode-matrix.md
- [ ] 判定 M1×3/M2/M3 证据是否支撑 checklist-2/3/4 的对应子项；记录 run id 与构建身份 8f3dd4c
- [ ] 产物：evidence/HUI-2232/billing/ledger-audit.md

### Task 4: 远端 QA 栈可达性探针 +（若可达）最小真实链复验

- [ ] 探测 120.26.69.202 四服务 healthz（经既有隧道手段或直连，只 GET）
- [ ] 可达：跑 /readyz 看 gates.plate_lock.usable；用既有 QA 登录态/或新 QA 用户最小复验一段链（上传→报价→确认→生成→质检→导出中至少报价+生成段），逐笔记账
- [ ] 不可达：如实标注，真实链结论引用 10-04 证据 + 构建身份一致性说明
- [ ] 产物：evidence/HUI-2232/service-provider/probe-20261008.log

### Task 5: 幂等/恢复复验（checklist-3 子项）

- [ ] fixture 层：REAL_DRY_RUN_FIXTURE=1 跑 web/e2e fixture-run（零费用）验证同键重复提交不二次调用
- [ ] 服务层：sourceflow recover/task 幂等测试显式运行并引用测试名
- [ ] 真实层：引用 10-04 attempt9 故障腿（charge=1×25）与 M3 四探针
- [ ] 产物：evidence/HUI-2232/billing/idempotency-recheck.md

### Task 6: 发布范围与 UI 文案诚实性（checklist-5）

- [ ] mode-matrix.md vs web limited-fidelity 面板文案逐条对照；确认无创意/视频越权宣称
- [ ] 产物：evidence/HUI-2232/browser/release-scope-audit.md

### Task 7: 六层结论 + 断点清单 + 收口建议（最终报告）

- [ ] 五条 checklist 判定、六层表、断点清单（坐标/缺口/解锁条件/归属票）、CLOSE vs KEEP 建议、花费台账
- [ ] 产物：evidence/HUI-2232/CONCLUSION-20261008.md；最终消息=完整报告
