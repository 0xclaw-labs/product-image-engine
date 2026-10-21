import Link from "next/link";
import type { ReactNode } from "react";
import styles from "./WorkspaceShell.module.css";

export type WorkspaceShellProps = {
  title: string;
  kicker?: string;
  subtitle?: string;
  status?: ReactNode;
  backHref?: string;
  backLabel?: string;
  headerActions?: ReactNode;
  versions?: ReactNode;
  children: ReactNode;
  className?: string;
};

/**
 * Workspace frame for the professional product-image surface. It owns the one
 * page h1, the current-work region, the version track and the workspace page
 * pattern markers. It deliberately does not own materials/stage/inspector:
 * those are the existing source/plate/revision facts rendered by their owners.
 */
export function WorkspaceShell({
  title,
  kicker = "产品图 · 作品台",
  subtitle,
  status,
  backHref,
  backLabel = "返回工程列表",
  headerActions,
  versions,
  children,
  className,
}: WorkspaceShellProps) {
  return (
    <div className={[styles.shell, className].filter(Boolean).join(" ")} data-page-pattern="workspace">
      <header className={styles.header} data-region="header">
        <div className={styles.titleBlock}>
          <p className={styles.kicker}>{kicker}</p>
          <h1 className={styles.title}>{title}</h1>
          {subtitle ? <p className={styles.subtitle}>{subtitle}</p> : null}
        </div>
        <div className={styles.headerSide}>
          {status ? <div className={styles.status} data-region="status">{status}</div> : null}
          <div className={styles.headerActions}>
            {headerActions}
            {backHref ? <Link className="link" href={backHref}>← {backLabel}</Link> : null}
          </div>
        </div>
      </header>
      <div className={styles.work} data-region="current_work">
        {children}
      </div>
      {versions ? (
        <section className={styles.versions} data-region="recent_context" aria-label="版本与回退">
          {versions}
        </section>
      ) : null}
    </div>
  );
}
