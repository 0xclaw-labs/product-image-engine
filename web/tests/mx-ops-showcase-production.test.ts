import { readFileSync } from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { presentShowcaseProduction } from "@/lib/showcase-video-production";

const fixtures = path.join(process.cwd(), "tests", "fixtures", "mx-ops");

function queueFixture(name: string): unknown {
  return JSON.parse(readFileSync(path.join(fixtures, name), "utf8"));
}

function job(patch: Record<string, unknown> = {}) {
  return {
    id: "vid_1",
    job_status: "quoted",
    quote_status: "confirmed",
    billing_label: "计费待确认",
    job_queue_view: queueFixture("job-queue-view.waiting.json"),
    i2v_capability: { status: "UNKNOWN", missing: ["image_conditioned_video_provider"] },
    payer_kind: "user",
    quoted_amount_minor: 300,
    candidate_asset_ref: null,
    ...patch,
  };
}

describe("展示视频生产等待（MX-OPS）", () => {
  it("有权威队列投影时显示 phase/等待原因/可空 ETA", () => {
    const view = presentShowcaseProduction(job());
    expect(view.queue?.phaseLabel).toBe("排队等待");
    expect(view.queue?.waitLabel).toBe("等待云端算力");
    expect(view.queue?.etaText).toBe("暂不可估");
    expect(view.waitingText).toContain("暂不可估");
  });

  it("没有队列投影时如实 UNKNOWN，不猜 ETA", () => {
    const view = presentShowcaseProduction(job({ job_queue_view: undefined }));
    expect(view.queue).toBeNull();
    expect(view.waitingText).toContain("未知");
    expect(view.waitingText).not.toContain("预计");
  });

  it("I2V 能力 UNKNOWN：明确说明，不产生真实视频/费用", () => {
    const view = presentShowcaseProduction(job());
    expect(view.capability).toBe("UNKNOWN");
    expect(view.capabilityNotice).toContain("图生视频能力未验证");
    expect(view.capabilityNotice).toContain("不会产生真实视频");
    expect(view.canPlayVideo).toBe(false);
  });

  it("费用只展示承担方与报价，不建第二钱包", () => {
    const view = presentShowcaseProduction(job());
    expect(view.costBearing).toContain("你承担");
    expect(view.quoteText).toContain("¥3.00");
    expect(view.costBearing).not.toContain("钱包余额");
    expect(presentShowcaseProduction(job({ payer_kind: "internal" })).costBearing).toContain("内部");
    expect(presentShowcaseProduction(job({ payer_kind: undefined })).costBearing).toBe("费用承担未提供");
  });

  it("候选素材不自动采用/外发", () => {
    const view = presentShowcaseProduction(job({ candidate_asset_ref: "asset_vid_9" }));
    expect(view.candidate.ref).toBe("asset_vid_9");
    expect(view.candidate.adopted).toBe(false);
    expect(view.candidate.hint).toContain("不会自动外发");
    expect(presentShowcaseProduction(job()).candidate.ref).toBeNull();
  });

  it("刷新/离场回到原任务、不重复扣费", () => {
    expect(presentShowcaseProduction(job()).notice).toContain("不重新提交");
    expect(presentShowcaseProduction(job()).notice).toContain("不重复扣费");
  });
});

describe("面板接入（源码契约）", () => {
  const panel = readFileSync(path.join(process.cwd(), "src/components/showcase-video/Panel.tsx"), "utf8");

  it("展示队列/能力/费用/候选，且仍不播放静图或假视频", () => {
    expect(panel).toContain("presentShowcaseProduction");
    expect(panel).toContain("i2v_capability");
    expect(panel).toContain("capabilityNotice");
    expect(panel).toContain("costBearing");
    expect(panel).toContain("candidate.hint");
    expect(panel).not.toContain("<video");
    expect(panel).not.toContain("<img");
    expect(panel).not.toMatch(/文生视频|text-to-video|textToVideo/);
  });
});
