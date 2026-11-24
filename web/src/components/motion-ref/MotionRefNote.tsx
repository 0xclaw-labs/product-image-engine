"use client";

import { useCallback, useEffect, useState } from "react";
import { dynamicAdLabel, isMotionDigest, NOT_FINISHED_COPY, type MotionDeclaration } from "@/lib/motion-ref";
import { SelectField } from "@/components/ui/select-field";

type ListBody = { declarations?: MotionDeclaration[]; error?: { message?: string } };
type OneBody = { declaration?: MotionDeclaration; error?: { message?: string } };

const isDev = process.env.NODE_ENV !== "production";

export function MotionRefNote({ projectId }: { projectId?: string }) {
  const [closed, setClosed] = useState<boolean | null>(null);
  const [rows, setRows] = useState<MotionDeclaration[]>([]);
  const [revisionID, setRevisionID] = useState("");
  const [notice, setNotice] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    if (!projectId) {
      setClosed(null);
      setRows([]);
      return;
    }
    const res = await fetch(`/api/projects/${projectId}/motion-refs`);
    const data = (await res.json().catch(() => null)) as ListBody | null;
    if (res.status === 404) {
      setClosed(true);
      setRows([]);
      return;
    }
    if (!res.ok) {
      setNotice(data?.error?.message ?? "引用没有读到");
      setClosed(false);
      return;
    }
    setClosed(false);
    setRows(Array.isArray(data?.declarations) ? data.declarations : []);
  }, [projectId]);

  useEffect(() => {
    void load();
  }, [load]);

  // 候选版本回填后：默认选中当前版本（首条），不覆盖用户已选。
  useEffect(() => {
    setRevisionID((cur) => cur || rows[0]?.revision_id || "");
  }, [rows]);

  async function declare() {
    if (!projectId || !revisionID.trim()) return;
    setBusy(true);
    setNotice("");
    try {
      const res = await fetch(`/api/projects/${projectId}/motion-refs`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ revision_id: revisionID.trim() }),
      });
      const data = (await res.json().catch(() => null)) as OneBody | null;
      if (!res.ok || !data?.declaration) {
        setNotice(data?.error?.message ?? NOT_FINISHED_COPY);
        return;
      }
      if (!acceptDeclaration(data.declaration)) {
        setNotice("主体不能重做。还没生成成片。");
        return;
      }
      await load();
    } finally {
      setBusy(false);
    }
  }

  async function record(name: "price" | "color", value: string, id: string) {
    if (!projectId || !value.trim()) return;
    setBusy(true);
    setNotice("");
    try {
      const res = await fetch(`/api/projects/${projectId}/motion-refs/${encodeURIComponent(id)}/parameters`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, value: value.trim() }),
      });
      const data = (await res.json().catch(() => null)) as OneBody | null;
      if (!res.ok || !data?.declaration || !acceptDeclaration(data.declaration)) {
        setNotice(data?.error?.message ?? "只记录参数。还没生成成片。");
        return;
      }
      await load();
    } finally {
      setBusy(false);
    }
  }

  return (
    <section id="motion-ref" className="card" aria-label="Motion 引用">
      <h2>Motion 引用</h2>
      <p className="muted">{NOT_FINISHED_COPY}</p>
      <p className="muted">不生成视频，不调用供应商，这里也没有 Motion 编辑器。</p>
      <p className="muted">批量重跑尚未开放：还不确定其他版本是否执行过，为避免重复扣费暂不批量重跑。</p>
      {!projectId ? <p>还没有工程。还没生成成片。</p> : null}
      {closed ? <p>这个环境还没有打开 Motion 引用。还没生成成片。</p> : null}
      {projectId && closed === false ? (
        <div>
          <label htmlFor="md-revision-version">采用版本</label>
          <SelectField
            id="md-revision-version"
            name="revision_id"
            value={revisionID}
            onChange={setRevisionID}
            placeholder="— 选择版本 —"
            options={rows.map((row, index) => ({
              value: row.revision_id ?? `revision-${index}`,
              label: `版本 ${row.revision_id ?? index + 1} · ${shortCopy(row)}`,
            }))}
            data-testid="motion-ref-revision-select"
          />
          <button type="button" disabled={busy} onClick={() => void declare()}>形成引用</button>
          {rows.map((row) => (
            <MotionRefRow key={row.id || row.revision_id} row={row} busy={busy} onRecord={record} />
          ))}
        </div>
      ) : null}
      {notice ? <p role="alert">{notice}</p> : null}
      {isDev ? (
        <details data-technical="true" data-dev-only="true">
          <summary>技术详情（仅开发环境可见）</summary>
          <ul>
            {rows.map((row) => (
              <li
                key={row.id || row.revision_id}
                data-asset-id={row.asset_id}
                data-content-hash={row.content_hash}
                data-revision-id={row.revision_id}
                data-model-call-count={row.model_call_count ?? 0}
              >
                digest：{row.digest ?? "—"}；asset_id：{row.asset_id ?? "—"}；content_hash：
                {row.content_hash ?? "—"}；revision_id：{row.revision_id ?? "—"}；model_call_count：
                {row.model_call_count ?? 0}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </section>
  );
}

function MotionRefRow({ row, busy, onRecord }: { row: MotionDeclaration; busy: boolean; onRecord: (name: "price" | "color", value: string, id: string) => Promise<void> }) {
  const [price, setPrice] = useState("");
  const [color, setColor] = useState("");
  const copy = safeCopy(row);
  return (
    <article
      data-asset-id={row.asset_id}
      data-content-hash={row.content_hash}
      data-revision-id={row.revision_id}
      data-model-call-count={row.model_call_count ?? 0}
    >
      <p>{copy}</p>
      <label>
        价格
        <input value={price} onChange={(e) => setPrice(e.target.value)} />
      </label>
      <label>
        颜色
        <input value={color} onChange={(e) => setColor(e.target.value)} />
      </label>
      <button type="button" disabled={busy || !row.id} onClick={() => void onRecord("price", price, row.id || "")}>只记录价格</button>
      <button type="button" disabled={busy || !row.id} onClick={() => void onRecord("color", color, row.id || "")}>只记录颜色</button>
    </article>
  );
}

function acceptDeclaration(row: MotionDeclaration): boolean {
  if (row.product_regeneration !== false || row.model_call_count !== 0) return false;
  if (row.subject_hash !== row.content_hash) return false;
  if (!row.digest || !isMotionDigest(row.digest)) return false;
  try {
    dynamicAdLabel(row);
  } catch {
    return false;
  }
  return true;
}

function safeCopy(row: MotionDeclaration): string {
  try {
    return dynamicAdLabel(row);
  } catch {
    return NOT_FINISHED_COPY;
  }
}

function shortCopy(row: MotionDeclaration): string {
  return safeCopy(row).slice(0, 12);
}
