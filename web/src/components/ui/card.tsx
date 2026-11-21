import type { HTMLAttributes, ReactNode } from "react";
import {
  Card as ShadcnCard,
  CardHeader as ShadcnCardHeader,
  CardTitle as ShadcnCardTitle,
  CardDescription as ShadcnCardDescription,
} from "@/components/shadcn/card";

// 兼容旧引用:底层实现已迁移到 shadcn/ui Card 原语,子组件接口保持不变。
export function Card({ className, ...props }: HTMLAttributes<HTMLElement>) {
  return <ShadcnCard className={className} {...props} />;
}

export function CardHeader({ children }: { children: ReactNode }) {
  return <ShadcnCardHeader>{children}</ShadcnCardHeader>;
}

export function CardTitle({ children }: { children: ReactNode }) {
  return <ShadcnCardTitle>{children}</ShadcnCardTitle>;
}

export function CardDescription({ children }: { children: ReactNode }) {
  return <ShadcnCardDescription>{children}</ShadcnCardDescription>;
}
