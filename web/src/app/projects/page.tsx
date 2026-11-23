"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Alert } from "@/components/ui/alert";
import { Skeleton } from "@/components/ui/skeleton";
import { jsonBody, jsonHeaders } from "@/lib/api-client";
import { projectSourceLabel, projectStatusLabel } from "@/lib/project-labels";
import { Input } from "@/components/ui/input";
import { SelectField } from "@/components/ui/select-field";

interface Project {
  id: string;
  name: string;
  status: string;
  usage_kind: string;
  width_px: number;
  height_px: number;
  source_type: string;
  source_ref: string;
  updated_at: string;
}

// 项目列表:恢复/新建/打开工程。
export default function ProjectsPage() {
  const router = useRouter();
  const [projects, setProjects] = useState<Project[] | null>(null);
  const [error, setError] = useState("");
  const [showCreate, setShowCreate] = useState(false);
  const [form, setForm] = useState({
    name: "",
    usage_kind: "电商主图",
    width_px: 800,
    height_px: 800,
    source_type: "standalone",
    source_ref: "",
  });
  const [creating, setCreating] = useState(false);

  const reload = useCallback(async () => {
    let res: Response;
    try {
      res = await fetch("/api/projects");
    } catch {
      setError("网络不可用,暂时拿不到工程列表,请稍后重试。");
      return;
    }
    const data = await res.json().catch(() => null);
    if (!res.ok) {
      setError(data?.error?.message ?? "加载失败");
      return;
    }
    setError("");
    setProjects((data?.projects ?? []) as Project[]);
  }, []);

  useEffect(() => {
    (async () => {
      let session: Response;
      try {
        session = await fetch("/api/auth/session");
      } catch {
        // 网络不可达不等于未登录:给诚实错误态和恢复动作,不偷偷跳登录。
        setError("网络不可用,暂时无法确认登录状态,请稍后重试。");
        return;
      }
      if (session.status === 401) {
        router.replace("/login");
        return;
      }
      await reload();
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setCreating(true);
    setError("");
    try {
      const res = await fetch("/api/projects", {
        method: "POST",
        headers: jsonHeaders,
        body: jsonBody(form),
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setError(data?.error?.message ?? "创建失败");
        return;
      }
      setShowCreate(false);
      await reload();
      const id = data?.project?.id;
      if (id) router.push(`/projects/${id}`);
    } catch {
      setError("网络连接失败，工程未创建，请检查网络后重试。");
    } finally {
      setCreating(false);
    }
  }

  return (
    <div className="list-workspace" data-layout="list" data-page-pattern="list">
      <div className="card" data-region="header">
        <div className="row row-center">
          <h1 className="row-title card-heading">我的制作工程</h1>
          <Link className="link" href="/start">开始做产品图</Link>
          <Link className="link" href="/batches">批量</Link>
          <Link className="link" href="/handoff">
            接受跨应用交接
          </Link>
          <Button onClick={() => setShowCreate((v) => !v)}>
            {showCreate ? "收起" : "新建工程"}
          </Button>
        </div>
        {showCreate ? (
          <form onSubmit={create} className="mt-12">
            <div className="media-actions">
              <Button type="submit" disabled={creating}>
                {creating ? "创建中…" : "创建"}
              </Button>
            </div>
            <div className="row">
              <div>
                <label htmlFor="proj-create-name">工程名称</label>
                <Input
                  id="proj-create-name"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  required
                />
              </div>
              <div>
                <label htmlFor="proj-create-usage">用途</label>
                <Input
                  id="proj-create-usage"
                  value={form.usage_kind}
                  onChange={(e) =>
                    setForm({ ...form, usage_kind: e.target.value })
                  }
                />
              </div>
            </div>
            <div className="row">
              <div>
                <label htmlFor="proj-create-width">宽(px)</label>
                <input
                  id="proj-create-width"
                  type="number"
                  min={1}
                  max={16384}
                  value={form.width_px}
                  onChange={(e) =>
                    setForm({ ...form, width_px: Number(e.target.value) })
                  }
                />
              </div>
              <div>
                <label htmlFor="proj-create-height">高(px)</label>
                <input
                  id="proj-create-height"
                  type="number"
                  min={1}
                  max={16384}
                  value={form.height_px}
                  onChange={(e) =>
                    setForm({ ...form, height_px: Number(e.target.value) })
                  }
                />
              </div>
              <div>
                <label htmlFor="proj-create-source">来源类型</label>
                <SelectField
                  id="proj-create-source"
                  name="proj-create-source"
                  value={form.source_type}
                  onChange={(v) =>
                    setForm({ ...form, source_type: v })
                  }
                  options={[
                    { value: "standalone", label: "独立制作(无订单)" },
                    { value: "order", label: "挂接订单" },
                    { value: "campaign", label: "挂接活动" },
                  ]}
                />
              </div>
              {form.source_type !== "standalone" ? (
                <div>
                  <label htmlFor="proj-create-ref">来源引用(source_ref)</label>
                  <input
                    id="proj-create-ref"
                    value={form.source_ref}
                    onChange={(e) =>
                      setForm({ ...form, source_ref: e.target.value })
                    }
                    placeholder="来源私有引用,必填"
                  />
                </div>
              ) : null}
            </div>
          </form>
        ) : null}
        {error ? (
          <div data-state="error" className="mt-3">
            <Alert tone="danger" role="alert">
              <div className="flex flex-wrap items-center gap-3">
                {error}
                <Button type="button" size="sm" variant="outline" onClick={() => void reload()}>
                  重试
                </Button>
              </div>
            </Alert>
          </div>
        ) : null}
      </div>

      <div className="card" data-region="collection">
        {projects === null ? (
          <div data-state="loading" role="status" className="grid gap-3">
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </div>
        ) : projects.length === 0 ? (
          <span className="muted" data-state="empty">
            还没有工程。<Link className="link" href="/start">去开始做产品图</Link>
          </span>
        ) : (
          <>
            <ul className="asset-strip" aria-label="工程">
              {projects.map((p) => (
                <li key={p.id}>
                  <Link className="link" href={`/projects/${p.id}`}>
                    {p.name}
                  </Link>
                  <div><Badge variant="secondary">{projectStatusLabel(p.status)}</Badge></div>
                  <div>{p.usage_kind || "—"}</div>
                  <div>{p.width_px}×{p.height_px}</div>
                  <div>
                    {p.source_type ? (
                      <Badge variant="outline">
                        {projectSourceLabel(p.source_type)}
                        {p.source_ref ? ` · ${p.source_ref}` : ""}
                      </Badge>
                    ) : (
                      "—"
                    )}
                  </div>
                  <div className="muted">{p.updated_at || "时间未知"}</div>
                </li>
              ))}
            </ul>
            <details data-technical="true">
              <summary>工程技术详情</summary>
              <table>
                <thead>
                  <tr>
                    <th scope="col">名称</th>
                    <th scope="col">状态</th>
                    <th scope="col">用途</th>
                    <th scope="col">尺寸</th>
                    <th scope="col">来源</th>
                    <th scope="col">更新时间</th>
                  </tr>
                </thead>
                <tbody>
                  {projects.map((p) => (
                    <tr key={p.id}>
                      <td>{p.name}</td>
                      <td>{p.status || "状态未知"}</td>
                      <td>{p.usage_kind || "—"}</td>
                      <td className="tabular-nums">{p.width_px}×{p.height_px}</td>
                      <td>{p.source_type || "—"}{p.source_ref ? ` · ${p.source_ref}` : ""}</td>
                      <td className="muted">{p.updated_at}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </details>
          </>
        )}
      </div>
    </div>
  );
}
