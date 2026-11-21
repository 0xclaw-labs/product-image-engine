import {
  Button as ShadcnButton,
  type ButtonProps,
} from "@/components/shadcn/button";

type Variant = "default" | "outline" | "secondary";
type Size = "default" | "sm";

// 兼容旧引用:底层实现已迁移到 shadcn/ui 原语,variant/size 接口保持不变。
export function Button({
  variant = "default",
  size = "default",
  ...props
}: ButtonProps & { variant?: Variant; size?: Size }) {
  return <ShadcnButton variant={variant} size={size} {...props} />;
}
