"use client";

import { Clock3, Plus, Trash2 } from "lucide-react";
import { newPricingPeriodDraft, type PricingPeriodDraft, type PricingPeriodRateKey } from "../domain/pricing-period-draft";
import { languageLocale, tx } from "../i18n/runtime";

export function PricingPeriodsEditor({ value, onChange, disabled = false }: {
  value: PricingPeriodDraft[];
  onChange: (value: PricingPeriodDraft[]) => void;
  disabled?: boolean;
}) {
  function update(index: number, patch: Partial<PricingPeriodDraft>) {
    onChange(value.map((period, position) => position === index ? { ...period, ...patch } : period));
  }
  const rates: { key: PricingPeriodRateKey; label: string }[] = [
    { key: "input_price_usd_per_1m", label: tx("输入（缓存未命中）USD/1M") },
    { key: "cache_read_price_usd_per_1m", label: tx("输入（缓存命中）USD/1M") },
    { key: "output_price_usd_per_1m", label: tx("输出 USD/1M") },
    { key: "cache_write_price_usd_per_1m", label: tx("缓存写成本 USD/1M") },
    { key: "cache_write_5m_price_usd_per_1m", label: tx("5 分钟缓存写成本 USD/1M") },
    { key: "cache_write_1h_price_usd_per_1m", label: tx("1 小时缓存写成本 USD/1M") },
  ];
  const days = [tx("周日"), tx("周一"), tx("周二"), tx("周三"), tx("周四"), tx("周五"), tx("周六")];
  function updateRate(index: number, key: PricingPeriodRateKey, input: HTMLInputElement) {
    const invalidRateKeys = (value[index].invalidRateKeys ?? []).filter(item => item !== key);
    if (input.validity.badInput) invalidRateKeys.push(key);
    update(index, { [key]: input.value, invalidRateKeys: invalidRateKeys.length ? invalidRateKeys : undefined });
  }
  function priceField(index: number, rate: typeof rates[number]) {
    // Native invalid text and an empty field both expose value="". Input events
    // also capture clearing invalid text when React sees no value change.
    const invalid = value[index].invalidRateKeys?.includes(rate.key) || undefined;
    return <label key={rate.key}><span>{rate.label}</span><input type="number" min="0" step="any" value={value[index][rate.key]} aria-invalid={invalid} placeholder={tx("继承基础价格")}
      onInput={event => updateRate(index, rate.key, event.currentTarget)} onChange={event => updateRate(index, rate.key, event.currentTarget)}
      onKeyDown={event => {
        // A remounted invalid field is already empty, so deleting it emits no input event.
        if (invalid && (event.key === "Backspace" || event.key === "Delete") && event.currentTarget.value === "" && !event.currentTarget.validity.badInput) updateRate(index, rate.key, event.currentTarget);
      }}
    /></label>;
  }
  return <fieldset className="pricing-periods-editor" disabled={disabled}>
    <div className="pricing-periods-toolbar"><div><strong><Clock3 size={16} />{tx("分时价格")}</strong><p>{tx("留空继承基础价格，0 表示免费。时段外使用基础价格。")}</p></div><button className="secondary-button" type="button" disabled={value.length >= 64} onClick={() => onChange([...value, newPricingPeriodDraft()])}><Plus size={14} />{tx("添加时段")}</button></div>
    {value.length === 0 ? <p className="pricing-periods-empty">{tx("尚未配置时段，全天使用基础价格。")}</p> : null}
    {value.map((period, index) => <section className="pricing-period-card" key={period.draftID} aria-label={tx("时段 {number}").replace("{number}", new Intl.NumberFormat(languageLocale()).format(index + 1))}>
      <div className="pricing-period-card-head"><strong>{tx("时段 {number}").replace("{number}", new Intl.NumberFormat(languageLocale()).format(index + 1))}</strong><button className="text-button" type="button" onClick={() => onChange(value.filter((_, position) => position !== index))}><Trash2 size={14} />{tx("删除时段")}</button></div>
      <div className="pricing-period-schedule">
        <label><span>{tx("时段名称")}</span><input value={period.name ?? ""} placeholder={tx("例如：夜间优惠")} onChange={event => update(index, { name: event.target.value })} /></label>
        <label><span>{tx("时区（IANA）")}</span><input value={period.timezone ?? ""} placeholder="UTC" onChange={event => update(index, { timezone: event.target.value })} /></label>
        <label><span>{tx("开始时间")}</span><input type="time" value={period.start_time ?? ""} onChange={event => update(index, { start_time: event.target.value })} /></label>
        <label><span>{tx("结束时间")}</span><input type="time" value={period.end_time ?? ""} onChange={event => update(index, { end_time: event.target.value })} /></label>
      </div>
      <div className="pricing-period-rates">{rates.slice(0, 3).map(rate => priceField(index, rate))}</div>
      <details className="pricing-period-advanced"><summary>{tx("星期、生效日期与缓存写入")}</summary>
        <div className="pricing-period-weekdays" role="group" aria-label={tx("适用星期")}>
          {days.map((day, dayIndex) => <label key={dayIndex}><input type="checkbox" checked={!period.weekdays?.length || period.weekdays.includes(dayIndex)} onChange={event => {
            const selected = period.weekdays?.length ? period.weekdays : [0, 1, 2, 3, 4, 5, 6];
            const next = event.target.checked ? [...selected, dayIndex].sort() : selected.filter(item => item !== dayIndex);
            if (next.length) update(index, { weekdays: next.length === 7 ? undefined : next });
          }} />{day}</label>)}
        </div>
        <div className="pricing-period-dates">
          <label><span>{tx("生效开始（RFC 3339，可选）")}</span><input value={period.effective_from ?? ""} placeholder="2026-11-10T00:00:00+08:00" onChange={event => update(index, { effective_from: event.target.value })} /></label>
          <label><span>{tx("生效结束（RFC 3339，可选）")}</span><input value={period.effective_until ?? ""} placeholder="2026-12-01T00:00:00+08:00" onChange={event => update(index, { effective_until: event.target.value })} /></label>
        </div>
        <div className="pricing-period-rates">{rates.slice(3).map(rate => priceField(index, rate))}</div>
      </details>
    </section>)}
    <p className="pricing-periods-help">{tx("供应商成本按每次上游尝试开始时间计价；包含开始、不含结束。跨午夜归开始日，开始与结束相同表示全天。时段不能重叠，最多 64 个。")}</p>
  </fieldset>;
}
