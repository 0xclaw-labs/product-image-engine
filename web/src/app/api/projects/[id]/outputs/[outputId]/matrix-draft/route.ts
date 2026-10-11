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
    return NextResponse.json(data, { status: 200 });
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
