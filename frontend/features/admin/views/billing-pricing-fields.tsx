import { cloneElement, isValidElement, useId, type ReactNode } from "react";
import { languageLocale, tx } from "../i18n/runtime";

export type Rates = Record<"input" | "cache_read" | "cache_write" | "cache_write_5m" | "cache_write_1h" | "output", string>;
export type Period = { name: string; timezone: string; weekdays: number[]; start_time: string; end_time: string; effective_from?: string; effective_until?: string; rates: Rates };
export type Card = { id?: string; revision?: number; kind: string; target: string; source: string; currency: string; effective_from?: string; rates: Rates; periods: Period[] };
export const emptyRates = (): Rates => ({ input: "", cache_read: "", cache_write: "", cache_write_5m: "", cache_write_1h: "", output: "" });
export const rateLabels: [keyof Rates, string][] = [["input", "普通输入"], ["cache_read", "缓存读取"], ["cache_write", "其他缓存写入"], ["cache_write_5m", "5 分钟缓存写入"], ["cache_write_1h", "1 小时缓存写入"], ["output", "输出"]];
export function Field({ label, children, hint }: { label: string; children: ReactNode; hint?: string }) {
  const id = useId();
  const control = isValidElement<{ id?: string; "aria-describedby"?: string }>(children) ? cloneElement(children, { id, "aria-describedby": hint ? `${id}-hint` : undefined }) : children;
  return <div className="field"><label htmlFor={id}>{tx(label)}</label>{control}{hint ? <small id={`${id}-hint`}>{tx(hint)}</small> : null}</div>;
}
export function RateFields({ rates, update, cache = false, inherit = true, inputOnly = false }: { rates: Rates; update: (key: keyof Rates, value: string) => void; cache?: boolean; inherit?: boolean; inputOnly?: boolean }) {
  return <div className="billing-form-grid">{rateLabels.filter(([key]) => !inputOnly || key === "input").filter(([key]) => cache ? key.startsWith("cache_") : !key.startsWith("cache_")).map(([key, label]) => <Field key={key} label={label}><input inputMode="decimal" placeholder={cache ? tx(inherit ? "留空按基础价格计算" : "未知单价请留空") : "0.00"} value={rates[key]} onChange={(event) => update(key, event.target.value)} /></Field>)}</div>;
}
export function completedRates(rates: Rates, inheritCache = true): Rates {
  if (!inheritCache) return { input: rates.input.trim(), output: rates.output.trim(), cache_read: rates.cache_read.trim(), cache_write: rates.cache_write.trim(), cache_write_5m: rates.cache_write_5m.trim(), cache_write_1h: rates.cache_write_1h.trim() };
  const write = rates.cache_write.trim() || rates.input.trim();
  return { input: rates.input.trim(), output: rates.output.trim(), cache_read: rates.cache_read.trim() || rates.input.trim(), cache_write: write, cache_write_5m: rates.cache_write_5m.trim() || write, cache_write_1h: rates.cache_write_1h.trim() || write };
}
export function localDateTime() {
  const now = new Date();
  return new Date(now.getTime() - now.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}
// Preserve the decimal amount rather than converting exact prices to a float.
export function decimalAmount(value: string) {
  if (!/^\d+(\.\d+)?$/.test(value)) return value;
  const [integer, fraction = ""] = value.split(".");
  const separator = new Intl.NumberFormat(languageLocale()).formatToParts(1.1).find((part) => part.type === "decimal")?.value ?? ".";
  return new Intl.NumberFormat(languageLocale()).format(BigInt(integer)) + separator + fraction.replace(/0+$/, "").padEnd(2, "0");
}
export function displayDate(value?: string) {
  if (!value || Number.isNaN(Date.parse(value))) return "—";
  return new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}
