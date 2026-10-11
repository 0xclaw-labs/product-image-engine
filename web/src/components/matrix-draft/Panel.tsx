"use client";

// HUI-2388 源端面板:把已选定(活动)的产品图版本送进真实矩阵持久草稿服务,并回查。
// 诚实边界:草稿不是发布(published/charged 恒 false,界面上如实显示);
// 查询与导航零创建(GET 不发 POST);license_ref 必填;错误原样上浮(409/403/400)。

import { useState } from "react";
import {
  MatrixDraft,
  MatrixDraftFacts,
  draftHonestLabel,
  matrixLicenseRequired,
} from "@/lib/matrix-draft";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

export function MatrixDraftPanel({
  projectId,
  outputs,
}: {
  projectId: string;
  outputs: { id: string; file_name?: string }[];
}) {
  const [outputId, setOutputId] = useState<string>(outputs[0]?.id ?? "");
  const [licenseRef, setLicenseRef] = useState("");
  const [licenseExpiresAt, setLicenseExpiresAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [draft, setDraft] = useState<MatrixDraft | null>(null);
  const [facts, setFacts] = useState<MatrixDraftFacts | null>(null);
  const [receipt, setReceipt] = useState<MatrixDraft | null>(null);

  async function sendToMatrix() {
    if (!outputId) return;
    if (matrixLicenseRequired(licenseRef)) {
      setNotice("license_ref 必填(目的端破坏性必填,不代填)");
      return;
    }
    setBusy(true);
    setNotice("");
    try {
      const res = await fetch(
        `/api/projects/${projectId}/outputs/${outputId}/matrix-draft`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            license_ref: licenseRef.trim(),
            ...(licenseExpiresAt.trim()
              ? { license_expires_at: new Date(licenseExpiresAt.trim()).toISOString() }
              : {}),
          }),
        }
      );
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setNotice(data?.error?.message ?? `送矩阵失败(HTTP ${res.status})`);
        return;
      }
      setDraft(data?.matrix_draft ?? null);
      setFacts(data?.source_facts ?? null);
      setNotice(
        data?.matrix_draft?.idempotent_replay
          ? "同键同事实:目的端幂等重放同一草稿"
          : "草稿已在矩阵服务落库(未发布、未扣费)"
      );
    } finally {
      setBusy(false);
    }
  }

  async function readBack() {
    if (!outputId) return;
    setBusy(true);
    setNotice("");
    try {
      // 回查是 GET:不创建任何草稿;未送过 → 404 no_matrix_draft 如实显示。
      const res = await fetch(
        `/api/projects/${projectId}/outputs/${outputId}/matrix-draft`
      );
      const data = await res.json().catch(() => null);
      if (!res.ok) {
        setDraft(null);
        setFacts(null);
        setReceipt(null);
        setNotice(data?.error?.message ?? `回查失败(HTTP ${res.status})`);
        return;
      }
      setDraft(data?.matrix_draft ?? null);
      setFacts(data?.source_facts ?? null);
      setReceipt(data?.receipt ?? null);
      setNotice(data?.matrix_draft ? draftHonestLabel(data.matrix_draft) : "");
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="card" data-testid="matrix-draft-panel">
      <h2>送入矩阵(选定图片)</h2>
      <p className="muted">
        把已登记的成果作为草稿送入矩阵持久草稿服务。草稿不是发布,不扣费;
        活动来源须先明确选定该版本;license 引用必填,同一版本不得改写。
      </p>
      <div className="row">
        <div>
          <label htmlFor="md-output">成果</label>
          <select
            id="md-output"
            value={outputId}
            onChange={(e) => setOutputId(e.target.value)}
          >
            <option value="">— 选择 —</option>
            {outputs.map((o) => (
              <option key={o.id} value={o.id}>
                {o.file_name || o.id}
              </option>
            ))}
          </select>
        </div>
        <div>
          <label htmlFor="md-license">license 引用(必填)</label>
          <Input
            id="md-license"
            value={licenseRef}
            onChange={(e) => setLicenseRef(e.target.value)}
            placeholder="如 lic-2026-001"
          />
        </div>
        <div>
          <label htmlFor="md-expiry">license 到期(可选,本地时间)</label>
          <Input
            id="md-expiry"
            type="datetime-local"
            value={licenseExpiresAt}
            onChange={(e) => setLicenseExpiresAt(e.target.value)}
          />
        </div>
      </div>
      <div className="media-actions">
        <Button onClick={sendToMatrix} disabled={busy}>送矩阵(创建草稿)</Button>
        <Button variant="outline" onClick={readBack} disabled={busy}>矩阵回查(只读)</Button>
      </div>
      {notice ? (
        <p role="status" className="muted" data-testid="matrix-draft-notice">
          {notice}
        </p>
      ) : null}
      {draft ? (
        <details data-technical="true" open>
          <summary>草稿事实(源端保留 + 目的端回查)</summary>
          <ul>
            <li>draft_id:{draft.draft_id ?? "—"}</li>
            <li>schema:{draft.schema_version ?? "—"}</li>
            <li>published:{String(draft.published === true)}</li>
            <li>charged:{String(draft.charged === true)}</li>
            <li>license_ref:{draft.license_ref ?? "—"}</li>
            {facts ? (
              <li>
                源端事实:source_project={facts.source_project_id ?? "—"};
                campaign={facts.campaign_id ?? "—"};revision=
                {String(facts.revision ?? "—")};quality=
                {facts.quality_verdict ?? "—"};brand={facts.brand_id ?? "—"};
                payer={facts.payer_id ?? "—"}
              </li>
            ) : null}
            {receipt ? (
              <li>收据 schema:{receipt.schema_version ?? "—"}</li>
            ) : null}
          </ul>
        </details>
      ) : null}
    </div>
  );
}
