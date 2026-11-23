"use client";

import * as React from "react";
import * as SelectPrimitive from "@radix-ui/react-select";
import { ChevronDown } from "lucide-react";

import { cn } from "@/lib/utils";
import { SelectContent, SelectItem } from "@/components/shadcn/select";
import styles from "./ui.module.css";

export interface SelectFieldOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SelectFieldProps {
  /** 控件 id,透传到触发器按钮;外部 `<label htmlFor>` 沿用同名 id。 */
  id?: string;
  /** 表单字段名;省略时回退到 id。同时用于隐藏 input 的 FormData 取值。 */
  name?: string;
  value: string;
  onChange: (value: string) => void;
  disabled?: boolean;
  options: SelectFieldOption[];
  /** 空值占位文案;value 不匹配任何 option 时展示。 */
  placeholder?: string;
  /** 显式可访问名;外部 label 已 htmlFor 配对时可省略。 */
  ariaLabel?: string;
  ariaDescribedby?: string;
  "data-testid"?: string;
  className?: string;
}

/**
 * Radix Select 受控封装。
 *
 * SSR 镜像方案:Radix `SelectValue` 在服务端/首帧不解析选中项文本,触发器会渲染成空。
 * 本组件在挂载前(`mounted=false`,SSR 与首帧一致)直接从 options 按 value 查 label,
 * 把选中项文案作为纯文本镜像输出到触发器;`useEffect` 挂载后再切到 `SelectValue`,
 * 避免 hydration 不一致与首帧闪空。服务端 HTML 中即可断言到选中项 label 文本。
 *
 * FormData:受控 React 场景之外,额外渲染一个 `type="hidden"` 的 input 承载
 * `name`/`value`,保证原生表单提交路径下字段值不丢失。
 */
export function SelectField({
  id,
  name,
  value,
  onChange,
  disabled,
  options,
  placeholder,
  ariaLabel,
  ariaDescribedby,
  "data-testid": testid,
  className,
}: SelectFieldProps) {
  const selected = options.find((o) => o.value === value);
  const mirror = selected ? selected.label : placeholder ?? "";
  const [mounted, setMounted] = React.useState(false);

  React.useEffect(() => {
    setMounted(true);
  }, []);

  const fieldName = name ?? id;

  return (
    <>
      <SelectPrimitive.Root
        value={value}
        onValueChange={onChange}
        disabled={disabled}
      >
        <SelectPrimitive.Trigger
          id={id}
          aria-label={ariaLabel}
          aria-describedby={ariaDescribedby}
          data-testid={testid}
          className={cn(styles.selectField, className)}
        >
          <span className={styles.selectFieldValue}>
            {mounted ? (
              <SelectPrimitive.Value placeholder={placeholder} />
            ) : mirror ? (
              mirror
            ) : (
              <span className={styles.selectFieldPlaceholder}>{placeholder}</span>
            )}
          </span>
          <SelectPrimitive.Icon asChild>
            <ChevronDown className={styles.selectFieldChevron} aria-hidden="true" />
          </SelectPrimitive.Icon>
        </SelectPrimitive.Trigger>
        <SelectContent position="popper">
          {placeholder ? (
            <SelectItem value="">
              <span className={styles.selectFieldPlaceholder}>{placeholder}</span>
            </SelectItem>
          ) : null}
          {options.map((option) => (
            <SelectItem
              key={option.value}
              value={option.value}
              disabled={option.disabled}
            >
              {option.label}
            </SelectItem>
          ))}
        </SelectContent>
      </SelectPrimitive.Root>
      {fieldName ? (
        <input
          type="hidden"
          name={fieldName}
          value={value}
          data-testid={testid ? `${testid}-hidden` : undefined}
        />
      ) : null}
    </>
  );
}
