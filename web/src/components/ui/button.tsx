import {
  Button as ShadcnButton,
  type ButtonProps,
} from "@/components/shadcn/button";

// 兼容旧引用:底层实现为 shadcn/ui 原语;variant/size 透传 Radix 全量变体,
// 调用面保持与 shadcn ButtonProps 一致。
export function Button({
  variant = "default",
  size = "default",
  ...props
}: ButtonProps) {
  return <ShadcnButton variant={variant} size={size} {...props} />;
}
