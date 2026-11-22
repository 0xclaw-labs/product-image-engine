"use client";

import type { ReactNode } from "react";

import { Button as ShadcnButton } from "@/components/shadcn/button";

import styles from "./ui.module.css";

export type ButtonVariant = "primary" | "secondary";

export type ButtonProps = {
  variant?: ButtonVariant;
  /** 通栏按钮：登录表单主操作占满一行。 */
  block?: boolean;
  loading?: boolean;
  disabled?: boolean;
  type?: "button" | "submit";
  children: ReactNode;
  onClick?: () => void;
};

/** primary=品牌实底、secondary=描边；底层为 shadcn/ui Button，loading 三点脉冲保留。 */
export function Button({
  variant = "secondary",
  block = false,
  loading = false,
  disabled,
  type = "button",
  children,
  onClick,
}: ButtonProps) {
  return (
    <ShadcnButton
      type={type}
      variant={variant === "primary" ? "default" : "outline"}
      className={block ? "w-full" : undefined}
      disabled={disabled ?? loading}
      aria-busy={loading || undefined}
      onClick={onClick}
    >
      {loading ? (
        <span className={styles.dots} aria-hidden="true">
          <i />
          <i />
          <i />
        </span>
      ) : null}
      <span className={styles.label}>{children}</span>
    </ShadcnButton>
  );
}
