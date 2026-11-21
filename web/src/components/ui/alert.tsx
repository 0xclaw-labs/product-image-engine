import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils";

type Tone = "info" | "ok" | "warn" | "danger";

const toneClass: Record<Tone, string> = {
  info: "border-[var(--pn-status-info-solid)] bg-[var(--pn-status-info-weak)] text-[var(--pn-status-info-fg)]",
  ok: "border-[var(--pn-status-success-solid)] bg-[var(--pn-status-success-weak)] text-[var(--pn-status-success-fg)]",
  warn: "border-[var(--pn-status-warning-solid)] bg-[var(--pn-status-warning-weak)] text-[var(--pn-status-warning-fg)]",
  danger:
    "border-[var(--pn-status-danger-solid)] bg-[var(--pn-status-danger-weak)] text-[var(--pn-status-danger-fg)]",
};

// Alert: danger and warn interrupt (role=alert); info and ok are polite status.
// 样式收敛到品牌语义令牌,不再经旧 CSS Module 皮肤。
export function Alert({
  tone = "info",
  title,
  children,
  role,
  ...props
}: {
  tone?: Tone;
  title?: string;
  children: ReactNode;
} & Omit<HTMLAttributes<HTMLDivElement>, "title">) {
  const resolvedRole =
    role ?? (tone === "danger" || tone === "warn" ? "alert" : "status");
  return (
    <div
      role={resolvedRole}
      data-tone={tone}
      className={cn(
        "grid gap-1 rounded-lg border px-4 py-3 text-sm leading-relaxed",
        toneClass[tone],
      )}
      {...props}
    >
      {title ? <strong className="font-medium">{title}</strong> : null}
      <div>{children}</div>
    </div>
  );
}
