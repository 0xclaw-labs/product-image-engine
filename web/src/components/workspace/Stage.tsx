"use client";

import { useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import styles from "./Stage.module.css";

export type StageImage = {
  src: string;
  alt: string;
  label: string;
  width?: number;
  height?: number;
  note?: string;
};

export type StageProps = {
  id?: string;
  pattern?: "media_result" | "compare";
  original?: StageImage | null;
  candidate?: StageImage | null;
  candidateBadge?: string;
  candidateTone?: "ok" | "warn" | "bad";
  selected?: boolean;
  adoptedLabel?: string;
  empty?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
};

/**
 * Large artwork stage. The candidate is allowed to be the dominant object;
 * the original is always one explicit switch away. The comparison view is a
 * named, bounded scroll region so a 4096px source or a tall portrait cannot
 * widen the page. Full-screen viewing is a real dialog with Escape and focus
 * return, not an image that pretends to be bigger.
 */
export function Stage({
  id = "stage",
  pattern = "media_result",
  original,
  candidate,
  candidateBadge,
  candidateTone = "warn",
  selected = false,
  adoptedLabel = "",
  empty,
  actions,
  children,
}: StageProps) {
  const [view, setView] = useState<"compare" | "candidate" | "original">(
    original && candidate ? "compare" : candidate ? "candidate" : "original"
  );
  const [zoom, setZoom] = useState<StageImage | null>(null);
  const zoomTrigger = useRef<HTMLElement | null>(null);
  const choseView = useRef(false);
  const hasBoth = !!original && !!candidate;

  // The candidate arrives asynchronously after the paid run derives. Until the
  // user explicitly picks a view mode, the stage must switch to the candidate/
  // comparison instead of staying on an original-only view.
  useEffect(() => {
    if (choseView.current) return;
    setView(hasBoth ? "compare" : candidate ? "candidate" : "original");
  }, [hasBoth, candidate]);

  function chooseView(next: "compare" | "candidate" | "original") {
    choseView.current = true;
    setView(next);
  }

  useEffect(() => {
    if (!zoom) return;
    const trigger = zoomTrigger.current;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setZoom(null);
      }
      if (event.key === "Tab") {
        // The dialog has one decision (close); keep Tab inside it.
        event.preventDefault();
        document.querySelector<HTMLButtonElement>('[data-testid="stage-dialog-close"]')?.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      trigger?.focus?.();
    };
  }, [zoom]);

  const canShowOriginal = view !== "candidate" && !!original;
  const canShowCandidate = view !== "original" && !!candidate;

  function openZoom(image: StageImage, event: React.MouseEvent<HTMLButtonElement>) {
    zoomTrigger.current = event.currentTarget;
    setZoom(image);
  }

  const figure = (image: StageImage, role: "original" | "candidate") => (
    <figure className={styles.figure} data-stage-figure={role}>
      <div className={styles.frame}>
        <img
          src={image.src}
          alt={image.alt}
          width={image.width}
          height={image.height}
          data-testid={role === "candidate" ? "stage-candidate" : "stage-original"}
          loading="eager"
          decoding="async"
        />
        <span className={styles.badge} data-tone={role === "candidate" ? candidateTone : "neutral"}>
          {image.label}
        </span>
        <Button
          type="button"
          size="sm"
          variant="secondary"
          className={styles.zoomBtn}
          onClick={(event) => openZoom(image, event)}
          aria-label={`全屏查看${role === "candidate" ? "候选" : "原图"}`}
          data-testid={`stage-zoom-${role}`}
        >
          全屏
        </Button>
      </div>
      <figcaption className={styles.caption}>
        <span>{image.label}</span>
        {image.note ? <span className={styles.note}>{image.note}</span> : null}
      </figcaption>
    </figure>
  );

  return (
    <section
      id={id}
      className={styles.stage}
      data-page-pattern="media_result"
      data-region="media_preview"
      aria-label="作品画面"
    >
      <div className={styles.bar}>
        <div className={styles.state}>
          <span className={styles.kicker}>{pattern === "compare" ? "对照" : "候选画面"}</span>
          {candidateBadge ? (
            <span className={styles.pill} data-tone={candidateTone}>
              {candidateBadge}
            </span>
          ) : null}
          {selected ? <span className={styles.pill} data-tone="ok">已选定 · 未采用</span> : null}
          {adoptedLabel ? <span className={styles.pill} data-tone="ok">已采用 {adoptedLabel}</span> : null}
        </div>
        {hasBoth ? (
          <div className={styles.switch} role="group" aria-label="查看方式">
            <Button type="button" size="sm" variant={view === "compare" ? "default" : "secondary"} aria-pressed={view === "compare"} onClick={() => chooseView("compare")} data-testid="stage-view-compare">并排</Button>
            <Button type="button" size="sm" variant={view === "candidate" ? "default" : "secondary"} aria-pressed={view === "candidate"} onClick={() => chooseView("candidate")} data-testid="stage-view-candidate">只看候选</Button>
            <Button type="button" size="sm" variant={view === "original" ? "default" : "secondary"} aria-pressed={view === "original"} onClick={() => chooseView("original")} data-testid="stage-view-original">只看原图</Button>
          </div>
        ) : null}
      </div>

      <div
        className={styles.views}
        data-view={hasBoth ? view : candidate ? "candidate" : "original"}
        data-page-pattern={pattern}
        data-region="comparison_views"
        data-testid="stage-compare"
      >
        {canShowOriginal && original ? figure(original, "original") : null}
        {canShowCandidate && candidate ? figure(candidate, "candidate") : null}
        {!candidate && !original ? <div className={styles.empty}>{empty ?? <p>结果会显示在这里。</p>}</div> : null}
      </div>

      {candidate ? (
        <p className={styles.context} data-region="candidate_context">
          {selected
            ? "已选定是候选事实，还不等于采用。采用会把当前版本指针移到这张图；之后可在版本区回退。"
            : "候选画面不等于已采用；检查、费用与版本状态以右侧事实为准。"}
        </p>
      ) : null}
      {actions ? <div className={styles.actions} data-region="result_actions">{actions}</div> : null}
      {children}

      {zoom ? (
        <div
          className={styles.lightbox}
          role="dialog"
          aria-modal="true"
          aria-label={`全屏查看${zoom.label}`}
          data-testid="stage-dialog"
        >
          <div className={styles.lightboxBar}>
            <span>{zoom.label}</span>
            <Button autoFocus type="button" size="sm" variant="secondary" onClick={() => setZoom(null)} data-testid="stage-dialog-close">关闭（Esc）</Button>
          </div>
          <div className={styles.lightboxBody}>
            <img src={zoom.src} alt={zoom.alt} className={styles.zoomImage} />
          </div>
          <p className={styles.lightboxHint}>按 Esc 或点关闭返回；大图在有界区域里滚动，不改变页面宽度。</p>
        </div>
      ) : null}
    </section>
  );
}
