import { NextRequest, NextResponse } from "next/server";
import {
  accessTokenOf,
  callServer,
  toErrorResponse,
  UpstreamError,
} from "@/lib/server";

// GET /api/capabilities — app feature switches that make edit controls
// truthful. This is not a permission grant; writes still enforce their gates.
export async function GET(req: NextRequest) {
  try {
    const access = accessTokenOf(req);
    if (!access) throw new UpstreamError(401, "unauthenticated", "未登录");
    const data = await callServer("/api/v1/capabilities", { accessToken: access });
    return NextResponse.json(data);
  } catch (e) {
    return toErrorResponse(e);
  }
}
