// HUI-2388:BFF 路由三态(鉴权 401 / 透传 / 上游错误原样)。
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NextRequest } from "next/server";

import {
  GET as draftGET,
  POST as draftPOST,
} from "@/app/api/projects/[id]/outputs/[outputId]/matrix-draft/route";
import { POST as selectionPOST } from "@/app/api/projects/[id]/campaign-selection/route";
import { GET as restoreGET } from "@/app/api/projects/[id]/campaign-restore/route";

function req(url: string, init: NonNullable<ConstructorParameters<typeof NextRequest>[1]> = {}): NextRequest {
  return new NextRequest(`http://localhost:3000${url}`, init);
}

function authed(url: string, init: NonNullable<ConstructorParameters<typeof NextRequest>[1]> = {}): NextRequest {
  const headers = new Headers(init.headers);
  headers.set("cookie", "pia_access=good-token");
  return req(url, { ...init, headers });
}

function jsonInit(body: unknown): NonNullable<ConstructorParameters<typeof NextRequest>[1]> {
  return { method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify(body) };
}

const ctxOut = (id: string, outputId: string) => ({
  params: Promise.resolve({ id, outputId }),
});
const ctxProj = (id: string) => ({ params: Promise.resolve({ id }) });

beforeEach(() => vi.restoreAllMocks());
afterEach(() => vi.unstubAllGlobals());

describe("矩阵草稿 BFF", () => {
  it("未登录不触达上游", async () => {
    const fetchSpy = vi.fn();
    vi.stubGlobal("fetch", fetchSpy);
    const post = await draftPOST(req("/api/projects/p/outputs/o/matrix-draft", jsonInit({})), ctxOut("p", "o"));
    expect(post.status).toBe(401);
    const get = await draftGET(req("/api/projects/p/outputs/o/matrix-draft"), ctxOut("p", "o"));
    expect(get.status).toBe(401);
    const sel = await selectionPOST(req("/api/projects/p/campaign-selection", jsonInit({})), ctxProj("p"));
    expect(sel.status).toBe(401);
    const res = await restoreGET(req("/api/projects/p/campaign-restore"), ctxProj("p"));
    expect(res.status).toBe(401);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  it("GET 回查只读透传(不发 POST)", async () => {
    const fetchSpy = vi.fn(async (_url: string, init?: RequestInit) =>
      new Response(JSON.stringify({ matrix_draft: { draft_id: "md_1", published: false } }), {
        status: 200,
      })
    );
    vi.stubGlobal("fetch", fetchSpy);
    const res = await draftGET(authed("/api/projects/p1/outputs/o1/matrix-draft"), ctxOut("p1", "o1"));
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.matrix_draft.published).toBe(false);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    expect((fetchSpy.mock.calls[0] as unknown[])[1]?.method).toBe("GET");
  });

  it("POST 送矩阵透传(含 license 事实)", async () => {
    const fetchSpy = vi.fn(async () =>
      new Response(JSON.stringify({ matrix_draft: { draft_id: "md_2", idempotent_replay: false } }), { status: 201 })
    );
    vi.stubGlobal("fetch", fetchSpy);
    const res = await draftPOST(
      authed("/api/projects/p1/outputs/o1/matrix-draft", jsonInit({ license_ref: "lic-1" })),
      ctxOut("p1", "o1")
    );
    expect(res.status).toBe(200);
    const body = await res.json();
    expect(body.matrix_draft.draft_id).toBe("md_2");
    const [, init] = fetchSpy.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(String(init.body)).toContain("lic-1");
  });

  it("上游 409 idempotency_conflict 原样上浮", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(
          JSON.stringify({ error: { code: "matrix_draft_facts_conflict", message: "不得改写" } }),
          { status: 409 }
        )
      )
    );
    const res = await draftPOST(
      authed("/api/projects/p1/outputs/o1/matrix-draft", jsonInit({ license_ref: "lic-2" })),
      ctxOut("p1", "o1")
    );
    expect(res.status).toBe(409);
    const body = await res.json();
    expect(body.error.code).toBe("matrix_draft_facts_conflict");
  });

  it("活动选定与恢复透传", async () => {
    const fetchSpy = vi.fn(async () => new Response(JSON.stringify({ ok: true }), { status: 200 }));
    vi.stubGlobal("fetch", fetchSpy);
    const sel = await selectionPOST(
      authed("/api/projects/p1/campaign-selection", jsonInit({ output_id: "o1" })),
      ctxProj("p1")
    );
    expect(sel.status).toBe(200);
    const res = await restoreGET(authed("/api/projects/p1/campaign-restore"), ctxProj("p1"));
    expect(res.status).toBe(200);
    expect(fetchSpy).toHaveBeenCalledTimes(2);
  });
});
