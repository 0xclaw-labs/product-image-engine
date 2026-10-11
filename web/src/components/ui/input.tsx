import type { InputHTMLAttributes } from "react";
import styles from "./ui.module.css";

// Input 是站点统一输入控件(与 Button 同思路):样式限定在本模块,
// 新代码一律用它,不再新增裸 <input>(finish-r1 卫生基线只许下降)。
export function Input({
  className,
  ...props
}: InputHTMLAttributes<HTMLInputElement>) {
  const names = [styles.input, className].filter(Boolean).join(" ");
  return <input className={names} {...props} />;
}
