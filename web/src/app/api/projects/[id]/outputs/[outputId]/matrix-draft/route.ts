import { NextRequest, NextResponse } from "next/server";
import {
  accessTokenOf,
  callServer,
  toErrorResponse,
  UpstreamError,
} from "@/lib/server";

type Ctx = { params: Promise<{ id: string; outputId: string }> };

// POST /api/projects/[id]/outputs/[outputId]/matrix-draft — 送矩阵(显式动作)。
// 查询(导航/刷新)走 GET;GET 不创建草稿。
// 状态码与上游约定对齐(同 receipt 的 duplicate?200:201 模式):
// 目的端新建 201;同键同事实幂等重放 200。
export async function POST(req: NextRequest, ctx: Ctx) {
  try {
    const access = accessTokenOf(req);
    if (!access) throw new UpstreamError(401, "unauthenticated", "未登录");
    const { id, outputId } = await ctx.params;
    const body = await req.json().catch(() => ({}));
    const data = await callServer(
      `/api/v1/projects/${id}/outputs/${outputId}/matrix-draft`,
      { method: "POST", accessToken: access, body }
    );
    const replay =
      (data.matrix_draft as { idempotent_replay?: boolean } | undefined)
        ?.idempotent_replay === true;
    return NextResponse.json(data, { status: replay ? 200 : 201 });
  } catch (e) {
    return toErrorResponse(e);
  }
}

// GET /api/projects/[id]/outputs/[outputId]/matrix-draft — 纯回查,零创建。
export async function GET(req: NextRequest, ctx: Ctx) {
  try {
    const access = accessTokenOf(req);
    if (!access) throw new UpstreamError(401, "unauthenticated", "未登录");
    const { id, outputId } = await ctx.params;
    const data = await callServer(
      `/api/v1/projects/${id}/outputs/${outputId}/matrix-draft`,
      { accessToken: access }
    );
    return NextResponse.json(data, { status: 200 });
  } catch (e) {
    return toErrorResponse(e);
  }
}
