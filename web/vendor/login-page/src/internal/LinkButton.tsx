"use client";

import type { ReactNode } from "react";

import { Button as ShadcnButton } from "@/components/shadcn/button";

export type LinkButtonProps = {
  className?: string;
  disabled?: boolean;
  type?: "button" | "submit";
  children: ReactNode;
  onClick?: () => void;
};

/** 文字链按钮：底层为 shadcn/ui Button(ghost)，链接样式由宿主 CSS Module 类名传入。 */
export function LinkButton({
  className,
  disabled,
  type = "button",
  children,
  onClick,
}: LinkButtonProps) {
  return (
    <ShadcnButton
      type={type}
      variant="ghost"
      size="sm"
      className={className}
      disabled={disabled}
      onClick={onClick}
    >
      {children}
    </ShadcnButton>
  );
}
