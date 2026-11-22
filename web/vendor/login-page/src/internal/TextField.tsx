"use client";

import { useState, type ChangeEvent } from "react";

import { Input as ShadcnInput } from "@/components/shadcn/input";

import styles from "./ui.module.css";

export type TextFieldProps = {
  label: string;
  value: string;
  /** 受控回调拿纯值（与 Segmented.onChange 对称），事件封装留在组件内。 */
  onChange: (value: string) => void;
  type?: "text" | "email" | "password";
  inputMode?: "text" | "numeric" | "email" | "tel";
  maxLength?: number;
  autoComplete?: string;
  autoFocus?: boolean;
  disabled?: boolean;
  invalid?: boolean;
  /** 错误文案行（role=alert）；出现时输入框同时 aria-invalid。 */
  error?: string;
};

let autoSeq = 0;

/** Field.label + 输入框合体；底层为 shadcn/ui Input，label 配对与错误态契约保留。 */
export function TextField({
  label,
  value,
  onChange,
  type,
  inputMode,
  maxLength,
  autoComplete,
  autoFocus,
  disabled,
  invalid,
  error,
}: TextFieldProps) {
  // 实例内稳定 id（label htmlFor 配对用）；harness 驱动下不依赖 useId。
  const [inputId] = useState(() => `lpg-field-${(autoSeq += 1)}`);
  const bad = invalid ?? Boolean(error);

  return (
    <div className={styles.field}>
      <label className={styles.label} htmlFor={inputId}>
        {label}
      </label>
      <div className={styles.wrap}>
        <ShadcnInput
          id={inputId}
          type={type}
          value={value}
          onChange={(e: ChangeEvent<HTMLInputElement>) => onChange(e.currentTarget.value)}
          inputMode={inputMode}
          maxLength={maxLength}
          autoComplete={autoComplete}
          autoFocus={autoFocus}
          disabled={disabled}
          className={bad ? styles.invalid : undefined}
          aria-invalid={bad || undefined}
        />
      </div>
      {error ? (
        <p className={styles.error} role="alert">
          {error}
        </p>
      ) : null}
    </div>
  );
}
