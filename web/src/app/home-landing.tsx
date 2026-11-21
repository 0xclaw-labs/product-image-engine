"use client";

// 产品首页(公共/营销入口):案例卡 + 上传/开始入口。
// 会话裁决保留既有语义:探针确认已登录 → 回创作入口 /start;401 → 匿名落地;
// 网络/服务异常 → 可见错误态与重试,不静默吞掉。
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import {
  ArrowRight,
  FolderOpen,
  ImagePlus,
  Loader2,
  RefreshCw,
} from "lucide-react";

import { fetchWithRefresh } from "@/lib/api-client";
import { loggedInHome } from "@/lib/first-image";
import { Button } from "@/components/shadcn/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/shadcn/card";
import { Badge } from "@/components/shadcn/badge";
import { Alert, AlertDescription, AlertTitle } from "@/components/shadcn/alert";
import { Skeleton } from "@/components/shadcn/skeleton";
import { Separator } from "@/components/shadcn/separator";

type ProbeState = "loading" | "anon" | "error" | "ready";

type CaseStudy = {
  id: string;
  badge: string;
  title: string;
  description: string;
};

const LOGIN_NEXT = encodeURIComponent(loggedInHome());
const CASE_STUDIES_URL = "/content/case-studies.json";

export default function HomeLanding() {
  const router = useRouter();
  const [probe, setProbe] = useState<ProbeState>("loading");
  // studies 为 null 表示目录尚未加载,[] 表示真实空目录,二者分别走骨架与空态。
  const [studies, setStudies] = useState<CaseStudy[] | null>(null);
  const [catalogError, setCatalogError] = useState("");

  const loadStudies = useCallback(async () => {
    setCatalogError("");
    try {
      const res = await fetch(CASE_STUDIES_URL, { signal: AbortSignal.timeout(10000) });
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const body = (await res.json().catch(() => null)) as CaseStudy[] | null;
      setStudies(Array.isArray(body) ? body : []);
    } catch {
      setStudies(null);
      setCatalogError("案例方向暂时加载失败，请稍后重试。");
    }
  }, []);

  useEffect(() => {
    void loadStudies();
  }, [loadStudies]);

  const probeSession = useCallback(async () => {
    setProbe("loading");
    try {
      const res = await fetchWithRefresh("/api/auth/session", {
        signal: AbortSignal.timeout(10000),
      });
      if (res.ok) {
        setProbe("ready");
        router.replace(loggedInHome());
      } else if (res.status === 401) {
        setProbe("anon");
      } else {
        setProbe("error");
      }
    } catch {
      setProbe("error");
    }
  }, [router]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        await probeSession();
      } catch {
        if (!cancelled) setProbe("error");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [probeSession]);

  const probing = probe === "loading";

  return (
    <div className="grid gap-6">
      <Card data-region="hero">
        <CardHeader>
          <Badge variant="secondary">产品图</Badge>
          <CardTitle className="mt-2 text-2xl">先看商品，再决定改哪里</CardTitle>
          <CardDescription>
            从一张商品照片开始，选定场景与尺寸，确认费用后生成。商品本身保持不动；
            这里还没有成片，浏览不会生成图片。
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-3">
          <Button
            type="button"
            disabled={probing}
            onClick={() => router.push(`/login?next=${LOGIN_NEXT}`)}
          >
            <ImagePlus aria-hidden="true" />
            上传商品照片开始
          </Button>
          <Button type="button" variant="outline" disabled={probing} asChild>
            <Link href="/projects">
              <FolderOpen aria-hidden="true" />
              继续已有工程
            </Link>
          </Button>
          {probing ? (
            <p role="status" className="muted text-sm">
              正在检查登录状态…
            </p>
          ) : null}
          {probe === "ready" ? (
            <p role="status" className="muted text-sm">
              已登录，正在进入工作台…
            </p>
          ) : null}
        </CardContent>
      </Card>

      {probe === "error" ? (
        <Alert variant="destructive" role="alert">
          <AlertTitle>暂时无法确认登录状态</AlertTitle>
          <AlertDescription className="flex flex-wrap items-center gap-3">
            网络或服务暂时不可达，仍可浏览下面的方向；进入制作前会再次检查登录。
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={() => void probeSession()}
            >
              <RefreshCw aria-hidden="true" />
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      <section aria-label="可做方向">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="text-base font-semibold">案例方向</h2>
          <Separator orientation="vertical" className="h-5" />
          <Link className="link text-sm" href="/start">
            查看制作流程 <ArrowRight aria-hidden="true" />
          </Link>
        </div>
        {catalogError ? (
          <Alert variant="destructive" role="alert">
            <AlertDescription className="flex flex-wrap items-center gap-3">
              {catalogError}
              <Button type="button" size="sm" variant="outline" onClick={() => void loadStudies()}>
                重试
              </Button>
            </AlertDescription>
          </Alert>
        ) : studies === null ? (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3" aria-label="加载中">
            {[0, 1, 2].map((i) => (
              <Card key={i} aria-hidden="true">
                <CardHeader>
                  <Skeleton className="h-4 w-16" />
                  <Skeleton className="h-5 w-32" />
                  <Skeleton className="h-4 w-full" />
                </CardHeader>
              </Card>
            ))}
          </div>
        ) : studies.length === 0 ? (
          <Card data-state="empty">
            <CardContent className="py-8 text-center text-sm text-muted-foreground">
              暂时没有可展示的案例方向。
            </CardContent>
          </Card>
        ) : (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {studies.map((item) => (
              <Card key={item.id} data-case={item.id}>
                <CardHeader>
                  <Badge variant="secondary" className="w-fit">
                    {item.badge}
                  </Badge>
                  <CardTitle className="text-base">{item.title}</CardTitle>
                  <CardDescription>{item.description}</CardDescription>
                </CardHeader>
                <CardContent>
                  <Button type="button" variant="ghost" size="sm" asChild>
                    <Link href={`/login?next=${LOGIN_NEXT}`}>
                      按这个方向开始
                      <ArrowRight aria-hidden="true" />
                    </Link>
                  </Button>
                </CardContent>
              </Card>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
