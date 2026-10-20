// HUI-2227 残差② / HUI-2627：GET /api/eco-nav/launches — 生态导航发射映射与
// 当前应用身份（只读、零业务逻辑）。launch URL 真源=vendored public-ai
// production-manifest.json；当前应用身份/就绪态真源=eco-nav/v1 nav-manifest.json。
// 生产表没有的行诚实缺失，客户端不伪造跳转地址。
import { NextResponse } from "next/server";

import { currentApp, registeredLaunchRows } from "@/lib/eco-nav/registry";

export async function GET() {
  return NextResponse.json({ current: currentApp(), launches: registeredLaunchRows() });
}
