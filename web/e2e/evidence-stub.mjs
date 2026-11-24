// EVIDENCE ONLY. 本地回环身份/数据桩，供 R5 截图证据在无联调栈条件下渲染真实页面态。
// - 只监听 127.0.0.1；不调用任何生产/远端服务；凭据为合成值，零 PII。
// - 被现有 BFF 会话链路识别：Cookie `pia_access` 经 BFF 以 `Authorization: Bearer <token>`
//   转发，本桩只接受合成访问令牌 `fixture-access`；内部头 `X-Product-Internal-Token` 为
//   `fixture-token`。生产鉴权逻辑与 fail-close 行为零改动，本桩不进入生产构建路径。
//
// 启动：
//   node e2e/evidence-stub.mjs            # 默认 127.0.0.1:32325
//   EVIDENCE_STUB_PORT=32325 node e2e/evidence-stub.mjs
// Web 侧环境：
//   PRODUCT_SERVER_BASE_URL=http://127.0.0.1:32325 PRODUCT_SERVER_TOKEN=fixture-token
//   浏览器注入 Cookie: pia_access=fixture-access
import http from "node:http";

const PORT = Number(process.env.EVIDENCE_STUB_PORT ?? 32325);
const TOKEN = "fixture-token";
const ACCESS = "fixture-access";
const PROJECT = "proj_evidence";

const sha = (seed) => "sha256:" + seed.repeat(32);

const declarations = [
  {
    id: "mrev_evidence_1",
    asset_id: "asset-evidence-001",
    content_hash: sha("cd"),
    revision_id: "V1",
    digest: sha("ab"),
    subject_hash: sha("cd"),
    product_regeneration: false,
    model_call_count: 0,
    video_generated: false,
    verified: false,
    dynamic_ad: false,
    finished_copy: "还没生成成片。这条是已采用商品的 Motion 引用声明，不是动态广告。",
  },
  {
    id: "mrev_evidence_2",
    asset_id: "asset-evidence-002",
    content_hash: sha("ef"),
    revision_id: "V2",
    digest: sha("gh"),
    subject_hash: sha("ef"),
    product_regeneration: false,
    model_call_count: 0,
    video_generated: false,
    verified: false,
    dynamic_ad: false,
    finished_copy: "还没生成成片。V2 候选只做引用登记，不生成视频。",
  },
];

const json = (res, status, body) => {
  res.writeHead(status, { "Content-Type": "application/json", "Cache-Control": "no-store" });
  res.end(JSON.stringify(body));
};
const err = (res, status, code, message = code) => json(res, status, { error: { code, message } });

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url ?? "/", `http://127.0.0.1:${PORT}`);
  const p = url.pathname;

  if (req.headers["x-product-internal-token"] !== TOKEN) return err(res, 401, "unauthenticated");
  if (req.headers.authorization !== `Bearer ${ACCESS}`) return err(res, 401, "unauthenticated");

  if (p === "/api/v1/auth/session" && req.method === "GET") {
    return json(res, 200, {
      principal: { user_id: "usr_evidence", account_id: "acct_evidence", tenant_id: "acct_evidence" },
    });
  }

  if (p === "/api/v1/entry/personal" && req.method === "GET") {
    return json(res, 200, {
      sessions: [{ id: "sess_evidence", project_id: PROJECT, status: "quoted" }],
    });
  }

  if (p === `/api/v1/projects/${PROJECT}/first-image` && req.method === "GET") {
    return json(res, 200, { session: null });
  }

  if (p === `/api/v1/projects/${PROJECT}/motion-refs`) {
    if (req.method === "GET") return json(res, 200, { declarations });
    if (req.method === "POST") return json(res, 201, { created: true, declaration: declarations[0] });
  }

  if (p === "/api/v1/billing/balance" && req.method === "GET") {
    return json(res, 200, { balance_cny: "100.00", items: [], account_id: "acct_evidence" });
  }

  if (p === "/api/v1/capabilities" && req.method === "GET") {
    return json(res, 200, { selective_revision: false, bg_plate_lock: true });
  }

  // 兜底：未建模的只读探针返回空集合，避免页面 503/报错污染证据截图；不模拟写入成功。
  if (req.method === "GET") return json(res, 200, { sessions: [], inputs: [], runs: [], outputs: [], receipts: [] });
  return err(res, 404, "evidence_stub_unknown_route", "证据桩未建模该路径");
});

server.listen(PORT, "127.0.0.1", () => {
  console.log(`evidence stub listening on http://127.0.0.1:${PORT}`);
});
