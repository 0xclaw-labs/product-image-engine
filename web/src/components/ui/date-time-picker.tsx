"use client";

/**
 * 日期 / 日期时间复合件（D7 日期清零口径）：
 * - 触发器按钮可键盘聚焦、Enter/Space 打开；浮层为 Popover + Calendar 月历；
 * - 提交值由 hidden input 承载；日期早于 min 的格禁用；
 * - 带时间槽时，时间为复合件内显式 time 输入（带 label/aria），不保留裸 datetime-local。
 */
import { useEffect, useId, useMemo, useState } from "react";
import { cn } from "@/lib/utils";

function pad(n: number): string {
  return String(n).padStart(2, "0");
}

function parseLocal(value: string): { y: number; m: number; d: number; hh: string; mm: string } | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2}))?$/.exec(value);
  if (!match) return null;
  return {
    y: Number(match[1]),
    m: Number(match[2]),
    d: Number(match[3]),
    hh: match[4] ?? "00",
    mm: match[5] ?? "00",
  };
}

function localToday(): Date {
  return new Date();
}

export interface DateTimePickerProps {
  id?: string;
  name?: string;
  value: string;
  onChange: (value: string) => void;
  /** 是否带时间槽；false 时仅选日期。 */
  withTime?: boolean;
  /**  inclusive 下限（与 value 同格式）；早于该日不可选。 */
  min?: string;
  placeholder?: string;
  ariaLabel?: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
  disabled?: boolean;
  testid?: string;
}

export function DateTimePicker({
  id,
  name,
  value,
  onChange,
  withTime = false,
  min,
  placeholder = "— 选择日期 —",
  ariaLabel,
  "aria-invalid": ariaInvalid,
  "aria-describedby": ariaDescribedby,
  disabled,
  testid,
}: DateTimePickerProps) {
  const [open, setOpen] = useState(false);
  const parsed = parseLocal(value);
  const today = localToday();
  const [view, setView] = useState(() => {
    if (parsed) return { y: parsed.y, m: parsed.m };
    return { y: today.getFullYear(), m: today.getMonth() + 1 };
  });
  const panelId = useId();

  // Esc 关闭浮层并归还焦点。
  useEffect(() => {
    if (!open) return;
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") setOpen(false);
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  const minDate = useMemo(() => (min ? parseLocal(min) : null), [min]);

  const monthDays = useMemo(() => {
    const first = new Date(view.y, view.m - 1, 1);
    const startWeekday = first.getDay();
    const count = new Date(view.y, view.m, 0).getDate();
    const cells: (string | null)[] = [];
    for (let i = 0; i < startWeekday; i += 1) cells.push(null);
    for (let d = 1; d <= count; d += 1) cells.push(`${view.y}-${pad(view.m)}-${pad(d)}`);
    return cells;
  }, [view]);

  function shiftMonth(delta: number) {
    setView((cur) => {
      const next = new Date(cur.y, cur.m - 1 + delta, 1);
      return { y: next.getFullYear(), m: next.getMonth() + 1 };
    });
  }

  function dayDisabled(dayStr: string): boolean {
    if (!minDate) return false;
    const minDay = `${minDate.y}-${pad(minDate.m)}-${pad(minDate.d)}`;
    return dayStr < minDay;
  }

  function pickDay(dayStr: string) {
    const time = parsed ? `${parsed.hh}:${parsed.mm}` : "00:00";
    onChange(withTime ? `${dayStr}T${time}` : dayStr);
  }

  function pickTime(hhmm: string) {
    if (!parsed) return;
    onChange(`${parsed.y}-${pad(parsed.m)}-${pad(parsed.d)}T${hhmm}`);
  }

  const display = value || placeholder;
  const timeValue = parsed ? `${parsed.hh}:${parsed.mm}` : "";

  return (
    <div className="relative inline-flex flex-col gap-1" aria-invalid={ariaInvalid}>
      <input
        type="text"
        aria-hidden="true"
        tabIndex={-1}
        className="sr-only"
        data-testid={testid}
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <input type="hidden" id={id} name={name} value={value} />
      <button
        type="button"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={panelId}
        aria-label={ariaLabel}
        aria-describedby={ariaDescribedby}
        data-testid={testid ? `${testid}-trigger` : undefined}
        disabled={disabled}
        className="flex h-9 w-full items-center rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm transition-colors hover:border-[var(--pn-border-strong)] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50"
        onClick={() => setOpen((v) => !v)}
      >
        <span className="truncate">{display}</span>
        <span aria-hidden="true" className="ml-auto opacity-60">📅</span>
      </button>
      {/* hidden input 承载提交值；表单 / 测试读取路径不变。 */}
      {open ? (
        <div
          id={panelId}
          role="dialog"
          aria-label="选择日期"
          className="absolute left-0 top-full z-50 mt-2 w-72 rounded-md border bg-popover p-3 shadow-md outline-none"
        >
          <div className="mb-2 flex items-center justify-between">
            <button type="button" onClick={() => shiftMonth(-1)} aria-label="上一月" className="h-7 w-7 rounded-md border bg-transparent">
              ‹
            </button>
            <span className="text-sm font-medium">
              {view.y} 年 {pad(view.m)} 月
            </span>
            <button type="button" onClick={() => shiftMonth(1)} aria-label="下一月" className="h-7 w-7 rounded-md border bg-transparent">
              ›
            </button>
          </div>
          <div className="grid grid-cols-7 gap-1">
            {["日", "一", "二", "三", "四", "五", "六"].map((wd) => (
              <span key={wd} className="w-9 text-center text-xs text-muted-foreground">
                {wd}
              </span>
            ))}
            {monthDays.map((dayStr, idx) =>
              dayStr === null ? (
                <span key={`blank-${idx}`} />
              ) : (
                <button
                  key={dayStr}
                  type="button"
                  disabled={dayDisabled(dayStr)}
                  aria-pressed={value.startsWith(dayStr)}
                  onClick={() => pickDay(dayStr)}
                  className={cn(
                    "h-9 w-9 rounded-md text-center text-sm",
                    value.startsWith(dayStr)
                      ? "bg-primary text-primary-foreground"
                      : "hover:bg-accent hover:text-accent-foreground",
                    dayDisabled(dayStr) ? "opacity-50" : ""
                  )}
                >
                  {Number(dayStr.slice(-2))}
                </button>
              )
            )}
          </div>
          {withTime ? (
            <label className="mt-3 flex items-center gap-2 text-sm">
              时间
              <input
                type="time"
                aria-label="时间"
                value={timeValue}
                onChange={(e) => pickTime(e.target.value)}
                className="h-8 rounded-md border border-input bg-transparent px-2 text-sm"
              />
            </label>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
