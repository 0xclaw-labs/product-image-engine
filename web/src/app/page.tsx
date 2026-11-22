import { redirect } from "next/navigation";
import { cookies } from "next/headers";
import { loggedInHome } from "@/lib/first-image";
import { ACCESS_COOKIE, callServer } from "@/lib/server";
import HomeLanding from "./home-landing";

// 首页:已登录 → 开始做产品图;未登录 / 会话失效 → 公共入口壳
// (案例卡 + 上传/开始入口),由客户端探针如实补匿名 / 错误 / 加载态。
// 重定向语义保持:会话有效直回创作入口;进入受保护动作时再经 /login 守卫。
export default async function Home() {
  const jar = await cookies();
  const access = jar.get(ACCESS_COOKIE)?.value;
  if (access) {
    let valid = false;
    try {
      await callServer("/api/v1/auth/session", { accessToken: access });
      valid = true;
    } catch {
      // 会话失效或服务不可达:落到入口壳,错误面由客户端探针可见呈现。
      valid = false;
    }
    if (valid) redirect(loggedInHome());
  }
  return <HomeLanding />;
}
