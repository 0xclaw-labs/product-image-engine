// HUI-1702 MX-OPS 增量：展示视频生产等待/费用/候选展示。
//
// 只读展示，不改变原有诚实语义：没有真实视频、没有可播放地址、不播放静图。
// 队列只来自权威 job_queue_view；没有投影就 UNKNOWN。费用只展示承担方，
// 不建钱包、不预扣。I2V 能力没有 adapter 时为 UNKNOWN，不用 T2V 冒充。

import { presentQueue, readJobQueueView, type MerchantQueue } from "@/lib/mx-ops/queue";
import { costBearingLabel, type PayerKind } from "@/lib/mx-ops/execution";

export interface ShowcaseProductionJob {
  id?: string;
  job_status?: string;
  quote_status?: string;
  billing_label?: string;
  job_queue_view?: unknown;
  i2v_capability?: { status?: string; notice?: string; missing?: string[] } | null;
  payer_kind?: string;
  quoted_amount_minor?: number | null;
  candidate_asset_ref?: string | null;
}

export interface ShowcaseProductionView {
  queue: MerchantQueue | null;
  waitingText: string;
  capability: "UNKNOWN" | "configured";
  capabilityNotice: string;
  costBearing: string;
  quoteText: string;
  candidate: { ref: string | null; adopted: false; hint: string };
  canPlayVideo: false;
  notice: string;
}

const PAYERS: PayerKind[] = ["user", "platform_subsidy", "internal", "campaign"];

function normalizePayer(value: string | undefined): PayerKind | null {
  return PAYERS.find((item) => item === value) ?? null;
}

function formatMinor(minor: number | null | undefined): string {
  if (minor === null || minor === undefined || !Number.isFinite(minor)) return "";
  const absolute = Math.abs(Math.round(minor));
  return `¥${Math.floor(absolute / 100)}.${String(absolute % 100).padStart(2, "0")}`;
}

export function presentShowcaseProduction(job: ShowcaseProductionJob): ShowcaseProductionView {
  const queue = presentQueue(readJobQueueView(job.job_queue_view));
  const capabilityStatus: "UNKNOWN" | "configured" =
    job.i2v_capability?.status === "configured" ? "configured" : "UNKNOWN";
  const payer = normalizePayer(job.payer_kind);
  const quoted = formatMinor(job.quoted_amount_minor);
  const candidateRef = (job.candidate_asset_ref ?? "").trim();
  const quoteText =
    quoted === ""
      ? job.billing_label || "计费待确认"
      : `${job.billing_label || "报价已确认"}（${quoted}）`;
  return {
    queue,
    waitingText: queue
      ? queue.phase === "waiting"
        ? `${queue.waitLabel}；预估 ${queue.etaText}`
        : `${queue.phaseLabel}；${queue.waitLabel}`
      : "排队状态未知：没有拿到权威队列投影，不提供估算时间。",
    capability: capabilityStatus,
    capabilityNotice:
      capabilityStatus === "UNKNOWN"
        ? "图生视频能力未验证：没有可用的图片条件视频适配器。这里不会用文生视频或静图冒充，也不会产生真实视频或费用。"
        : job.i2v_capability?.notice || "图生视频适配器已登记；真实产物仍以联调窗口验收为准。",
    costBearing: payer === null ? "费用承担未提供" : costBearingLabel(payer),
    quoteText,
    candidate: {
      ref: candidateRef === "" ? null : candidateRef,
      adopted: false,
      hint:
        candidateRef === ""
          ? "还没有候选素材。"
          : "成片只进入商家候选素材；发布/正式奖励另批准，不会自动外发。",
    },
    canPlayVideo: false,
    notice:
      "队列完成只回到候选素材；刷新/离场回到原任务，不重新提交、不重复扣费。",
  };
}
