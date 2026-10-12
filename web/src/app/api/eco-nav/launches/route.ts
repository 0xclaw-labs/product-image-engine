// HUI-2227 残差②：GET /api/eco-nav/launches — 生态导航发射映射（只读、零业务
// 逻辑）。真源=vendored public-ai production-manifest.json（见 lib/eco-nav/
// registry/SOURCE.txt）；生产表没有的行诚实缺失，客户端不伪造跳转地址。
import { NextResponse } from "next/server";

import { registeredLaunchRows } from "@/lib/eco-nav/registry";

export async function GET() {
  return NextResponse.json({ launches: registeredLaunchRows() });
}
