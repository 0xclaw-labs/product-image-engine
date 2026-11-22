"use client";

// 跨应用续接入口(HUI-1745 I1):粘贴来源应用下发的交接文档 → 接受。
// 只创建/恢复工程,绝不触发生成;新版需求返回待采用差异,由用户确认后采用。
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";

import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Card } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/shadcn/alert";
import { Textarea } from "@/components/shadcn/textarea";
import { Skeleton } from "@/components/shadcn/skeleton";

interface SourceContext {
  source_kind?: string;
  source_app?: string;
  principal_id?: string;
  order_ref?: string;
  stage_ref?: string;
  campaign_ref?: string;
  brief_version?: string;
  scopes?: string[];
  assets?: { asset_ref?: string; media_type?: string }[];
  delivery_spec?: { description?: string; media_type?: string };
}
interface Binding {
  id: string;
  source_app: string;
  source_ref: string;
  purpose: string;
  current_snapshot_id: string;
}
interface Snapshot {
  id: string;
  handoff_id: string;
  brief_version: string;
  source_revision: string;
}
interface DiffSide {
  handoff_id: string;
  source_revision: string;
  brief_version: string;
}
interface Diff {
  old?: DiffSide;
  new?: DiffSide;
}

type SessionState = "loading" | "ready" | "error";

export default function HandoffPage() {
  const router = useRouter();
  const [docText, setDocText] = useState("");
  const [purpose, setPurpose] = useState("主图");
  const [accepting, setAccepting] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<{
    resolution?: string;
    snapshot_resolution?: string;
    project?: { id: string; name: string; usage_kind: string };
    binding?: Binding;
    snapshot?: Snapshot;
    source_context?: SourceContext;
    pending_snapshot?: Snapshot;
    diff?: Diff;
  } | null>(null);
  const [adopting, setAdopting] = useState(false);
  const [adoptNotice, setAdoptNotice] = useState<{
    kind: "success" | "error";
    text: string;
  } | null>(null);
  const [sessionState, setSessionState] = useState<SessionState>("loading");

  const probeSession = useCallback(async () => {
    setSessionState("loading");
    try {
      const s = await fetch("/api/auth/session", {
        signal: AbortSignal.timeout(10000),
      });
      if (s.status === 401) {
        router.replace("/login");
        return;
      }
      setSessionState(s.ok ? "ready" : "error");
    } catch {
      setSessionState("error");
    }
  }, [router]);

  useEffect(() => {
    void probeSession();
  }, [probeSession]);

  async function accept(e: React.FormEvent) {
    e.preventDefault();
    setAccepting(true);
    setError("");
    setResult(null);
    setAdoptNotice(null);
    try {
      let doc: unknown;
      try {
        doc = JSON.parse(docText);
      } catch {
        setError("交接文档不是合法 JSON");
        return;
      }
      const res = await fetch("/api/handoffs/accept", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ handoff: doc, purpose }),
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setError(data?.error?.message ?? `接受失败(HTTP ${res.status})`);
        return;
      }
      setResult(data);
    } catch {
      setError("网络连接失败，请检查网络后重试。");
    } finally {
      setAccepting(false);
    }
  }

  async function adopt() {
    if (!result?.binding?.id || !result.pending_snapshot?.id) return;
    setAdopting(true);
    setAdoptNotice(null);
    try {
      const res = await fetch(`/api/bindings/${result.binding.id}/adopt`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ snapshot_id: result.pending_snapshot.id }),
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setAdoptNotice({
          kind: "error",
          text: data?.error?.message ?? `采用失败(HTTP ${res.status})`,
        });
        return;
      }
      setAdoptNotice({
        kind: "success",
        text: "已采用新版需求(人工编辑与已选输出保留)",
      });
      setResult({ ...result, pending_snapshot: undefined, diff: undefined });
    } catch {
      setAdoptNotice({
        kind: "error",
        text: "网络连接失败，请检查网络后重试。",
      });
    } finally {
      setAdopting(false);
    }
  }

  const ctx = result?.source_context;
  // 素材不丢失:只有带稳定 asset_ref 的条目才按「引用+类型」去重;
  // 缺少 asset_ref 的素材无法判定为同一对象,逐条保留,绝不合并不同素材。
  const seenRefs = new Set<string>();
  const uniqueAssets = (ctx?.assets ?? []).filter((a) => {
    if (!a.asset_ref) return true;
    const key = `${a.asset_ref}::${a.media_type ?? ""}`;
    if (seenRefs.has(key)) return false;
    seenRefs.add(key);
    return true;
  });

  return (
    <div>
      <Card className="card">
        <div className="row" style={{ alignItems: "center" }}>
          <h2 style={{ flex: 1, margin: 0 }}>接受跨应用交接</h2>
          <Link className="link" href="/projects">
            → 我的工程
          </Link>
        </div>
        <p className="muted" style={{ marginTop: 4 }}>
          粘贴来源应用下发的交接文档。接受只会创建或恢复工程，不会因此自动做图或扣费。
        </p>

        {sessionState === "error" ? (
          <Alert variant="destructive" role="alert" className="mt-3">
            <AlertDescription className="flex flex-wrap items-center gap-3">
              无法检查登录状态，请刷新或重试；仍不可用时回到工程列表继续。
              <Button type="button" size="sm" variant="outline" onClick={() => void probeSession()}>
                重试
              </Button>
            </AlertDescription>
          </Alert>
        ) : null}

        {sessionState === "loading" ? (
          <div role="status" className="mt-3">
            <Skeleton className="h-10 w-full" />
            <p className="muted mt-2">正在检查登录状态…</p>
          </div>
        ) : null}

        <form onSubmit={accept} style={{ marginTop: 12 }}>
          <div className="media-actions">
            <Button
              type="submit"
              disabled={accepting || sessionState !== "ready" || !docText.trim() || !purpose.trim()}
            >
              {accepting ? "接受中…" : "接受交接"}
            </Button>
          </div>
          <div>
            <label htmlFor="handoff-doc">交接文档</label>
            <Textarea
              id="handoff-doc"
              rows={10}
              value={docText}
              onChange={(e) => setDocText(e.target.value)}
              placeholder="把交接文档贴在这里"
              required
            />
          </div>
          <div className="row" style={{ marginTop: 8 }}>
            <div>
              <label htmlFor="handoff-purpose">用途</label>
              <Input
                id="handoff-purpose"
                value={purpose}
                onChange={(e) => setPurpose(e.target.value)}
                required
              />
            </div>
          </div>
        </form>
        {error ? (
          <Alert variant="destructive" role="alert" className="mt-3">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        ) : null}
      </Card>

      {result ? (
        <Card className="card">
          <h2>续接结果</h2>
          <p role="status" className="muted" aria-live="polite">
            交接文档已接受，工程已创建或恢复；此处只展示带入的素材与需求版本。
          </p>
          <p>
            {result.resolution ? <Badge variant="secondary">{result.resolution}</Badge> : null}{" "}
            {result.snapshot_resolution ? (
              <Badge variant="secondary">{result.snapshot_resolution}</Badge>
            ) : null}{" "}
            {result.project ? (
              <Link className="link" href={`/projects/${result.project.id}`}>
                打开工程:{result.project.name}
              </Link>
            ) : null}
          </p>
          <h3>来源上下文(只读展示;来源文本仅作数据,不参与任何执行)</h3>
          <dl>
            <div>
              <dt>来源</dt>
              <dd>
                {ctx?.source_kind || "来源未知"} · {ctx?.source_app || "—"}
                {ctx?.order_ref ? ` · 订单 ${ctx.order_ref}` : ""}
                {ctx?.stage_ref ? ` · 阶段 ${ctx.stage_ref}` : ""}
                {ctx?.campaign_ref ? ` · 活动 ${ctx.campaign_ref}` : ""}
              </dd>
            </div>
            <div>
              <dt>需求版本</dt>
              <dd>{result.snapshot?.brief_version || "—"}</dd>
            </div>
            <div>
              <dt>用途 / 交付</dt>
              <dd>
                {result.project?.usage_kind || "—"} · {ctx?.delivery_spec?.media_type || "—"}
              </dd>
            </div>
            <div>
              <dt>素材</dt>
              <dd>
                {uniqueAssets.length === 0
                  ? "—"
                  : uniqueAssets.map((a, i) => (
                      <Badge key={`${a.asset_ref ?? i}-${a.media_type ?? ""}`} variant="outline">
                        {a.asset_ref || "未命名素材"}
                      </Badge>
                    ))}
              </dd>
            </div>
            <div>
              <dt>授权范围</dt>
              <dd>{(ctx?.scopes ?? []).join(" / ") || "—"}</dd>
            </div>
          </dl>
          <details data-technical="true">
            <summary>来源技术详情</summary>
            <table>
              <tbody>
                <tr>
                  <th scope="row">来源</th>
                  <td>
                    {ctx?.source_kind} · {ctx?.source_app}
                    {ctx?.order_ref ? ` · 订单 ${ctx.order_ref}` : ""}
                    {ctx?.stage_ref ? ` · 阶段 ${ctx.stage_ref}` : ""}
                    {ctx?.campaign_ref ? ` · 活动 ${ctx.campaign_ref}` : ""}
                  </td>
                </tr>
                <tr>
                  <th scope="row">付款主体</th>
                  <td>{ctx?.principal_id || "未知"}</td>
                </tr>
                <tr>
                  <th scope="row">需求版本</th>
                  <td>{result.snapshot?.brief_version}</td>
                </tr>
                <tr>
                  <th scope="row">用途 / 交付</th>
                  <td>
                    {result.project?.usage_kind} · {ctx?.delivery_spec?.media_type}
                  </td>
                </tr>
                <tr>
                  <th scope="row">素材</th>
                  <td>{uniqueAssets.length}</td>
                </tr>
                <tr>
                  <th scope="row">授权范围</th>
                  <td>{(ctx?.scopes ?? []).join(" / ")}</td>
                </tr>
              </tbody>
            </table>
          </details>

          {result.pending_snapshot && result.diff ? (
            <div id="compare" style={{ marginTop: 12 }}>
              <h3>新版需求待采用</h3>
              <p className="muted">这里并排的是需求文字，不是成片。成片比较在工程里。</p>
              <div className="media-actions">
                <Button onClick={adopt} disabled={adopting}>
                  {adopting ? "采用中…" : "确认差异并采用新版"}
                </Button>
                {result.project ? <Link className="link" href={`/projects/${result.project.id}#compare`}>去工程里并排看图</Link> : null}
              </div>
              <ul className="asset-strip" aria-label="需求差异">
                <li>
                  <strong>交接单</strong>
                  <div>当前 {result.diff?.old?.handoff_id ?? "—"}</div>
                  <div>新版 {result.diff?.new?.handoff_id ?? "—"}</div>
                </li>
                <li>
                  <strong>来源版本</strong>
                  <div>当前 {result.diff?.old?.source_revision ?? "—"}</div>
                  <div>新版 {result.diff?.new?.source_revision ?? "—"}</div>
                </li>
                <li>
                  <strong>需求版本</strong>
                  <div>当前 {result.diff?.old?.brief_version ?? "—"}</div>
                  <div>新版 {result.diff?.new?.brief_version ?? "—"}</div>
                </li>
              </ul>
              <details data-technical="true">
                <summary>差异技术详情</summary>
                <table>
                  <thead>
                    <tr>
                      <th scope="col">项</th>
                      <th scope="col">当前已采用</th>
                      <th scope="col">新版(待确认)</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr>
                      <td>交接单</td>
                      <td>{result.diff?.old?.handoff_id ?? "—"}</td>
                      <td>{result.diff?.new?.handoff_id ?? "—"}</td>
                    </tr>
                    <tr>
                      <td>来源版本</td>
                      <td>{result.diff?.old?.source_revision ?? "—"}</td>
                      <td>{result.diff?.new?.source_revision ?? "—"}</td>
                    </tr>
                    <tr>
                      <td>需求版本</td>
                      <td>{result.diff?.old?.brief_version ?? "—"}</td>
                      <td>{result.diff?.new?.brief_version ?? "—"}</td>
                    </tr>
                  </tbody>
                </table>
              </details>
            </div>
          ) : null}
          {adoptNotice?.kind === "error" ? (
            <Alert variant="destructive" role="alert" className="mt-3">
              <AlertDescription>{adoptNotice.text}</AlertDescription>
            </Alert>
          ) : null}
          {adoptNotice?.kind === "success" ? (
            <p role="status" className="muted mt-3">{adoptNotice.text}</p>
          ) : null}
        </Card>
      ) : sessionState === "ready" ? (
        <p className="muted" data-state="empty">
          还没有续接结果。粘贴交接文档后，这里会展示带入的素材与需求版本。
        </p>
      ) : null}
    </div>
  );
}
