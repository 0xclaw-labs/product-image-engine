import type { HTMLAttributes } from "react";
import { Badge as ShadcnBadge } from "@/components/shadcn/badge";

// 兼容旧引用:底层为 shadcn/ui Badge;warn 为品牌 warning 语义变体。
export function Badge({
  variant = "secondary",
  ...props
}: HTMLAttributes<HTMLSpanElement> & {
  variant?: "default" | "secondary" | "destructive" | "warn" | "outline";
}) {
  return <ShadcnBadge variant={variant} {...props} />;
}
