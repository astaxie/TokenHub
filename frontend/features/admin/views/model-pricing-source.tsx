import { Link2 } from "lucide-react";
import type { AppData, Model, ModelPricingPeriod, ProviderModel } from "../core/types";
import { eligiblePricingSources, pricingSourceLabel } from "../domain/model-pricing-source";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";

export function ModelPricingSource({ data, values, initialModel, onChange }: { data: AppData; values: Record<string, string>; initialModel?: Model; onChange: (updates: Record<string, string>) => void }) {
  const mode = values.pricing_mode || "custom";
  const sources = eligiblePricingSources(data);
  const selected = sources.find(source => source.id === values.pricing_provider_model_id);
  const retainingDisabledSource = values.status === "disabled" && initialModel?.metadata?.pricing_mode === "provider" && mode === "provider" &&
    values.pricing_provider_model_id === initialModel.metadata.pricing_provider_model_id && values.modality === initialModel.modality;
  return <section className="model-pricing-source" aria-label={tx("对外定价方式")}>
    <label className="field"><span>{tx("对外定价方式")}</span><select value={mode} onChange={event => onChange({ pricing_mode: event.target.value })}>
      <option value="custom">{tx("独立定价")}</option><option value="provider">{tx("跟随指定上游定价")}</option>
    </select></label>
    {mode === "provider" ? <>
      <p className="model-pricing-source-help">{tx("所有路由统一采用所选上游模型的价格和时段，不随实际调用线路切换。上游修改仅影响新请求，进行中的请求保持原价格。")}</p>
      <label className="field"><span>{tx("定价来源模型")}</span><select required={!retainingDisabledSource} value={selected?.id ?? ""} onChange={event => onChange({ pricing_provider_model_id: event.target.value })}>
        <option value="">{tx("请选择已确认成本的上游模型")}</option>
        {sources.map(source => <option key={source.id} value={source.id}>{pricingSourceLabel(source, data)}</option>)}
      </select><small>{tx("仅列出已启用且成本已确认的聊天模型，明确确认的零价也可使用。")}</small></label>
      {!selected ? <p className="inline-notice error" role="alert">{tx(retainingDisabledSource ? "可以停用模型并保留原定价来源；重新启用前需要恢复有效来源。" : values.pricing_provider_model_id ? "原定价来源已不可用或成本未确认。请选择有效来源，或切回独立定价。" : "请选择定价来源；没有可选项时，请先在供应商页面保存并确认模型成本。")}</p> : <ModelPricingSourcePreview source={selected} />}
      <p className="model-pricing-source-help">{tx("独立价格已保留，切回独立定价后可继续编辑。")}</p>
    </> : <p className="model-pricing-source-help">{tx("所有路由使用下方设置的独立价格，上游成本变化不会自动改变对外价格。")}</p>}
  </section>;
}

function rate(value: number | undefined) {
  return value == null ? tx("继承基础价") : new Intl.NumberFormat(languageLocale(), { style: "currency", currency: "USD", maximumFractionDigits: 6 }).format(value);
}

function scheduleTime(period: ModelPricingPeriod) {
  const start = period.start_time?.trim();
  const end = period.end_time?.trim();
  if (!start && !end) return tx("全天");
  return `${start} – ${end}`;
}

function scheduleDays(period: ModelPricingPeriod) {
  if (!period.weekdays?.length) return tx("每天");
  const formatter = new Intl.DateTimeFormat(languageLocale(), { weekday: "short", timeZone: "UTC" });
  return period.weekdays.map(day => formatter.format(new Date(Date.UTC(2026, 0, 4 + day)))).join(" · ");
}

function effectiveDate(value: string | undefined, timeZone: string) {
  const date = value?.trim();
  if (!date) return "—";
  return new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "short", timeZone: timeZone.trim() || "UTC" }).format(new Date(date));
}

export function ModelPricingSourcePreview({ source }: { source: ProviderModel }) {
  const cacheWrite = source.cache_write_price_configured || (source.cache_write_price_usd_per_1m ?? 0) > 0 ? source.cache_write_price_usd_per_1m ?? 0 : source.input_price_usd_per_1m ?? 0;
  const prices = [
    { label: "输入（缓存未命中）", value: source.input_price_usd_per_1m ?? 0 },
    { label: "输入（缓存命中）", value: source.cache_read_price_usd_per_1m ?? 0 },
    { label: "输出", value: source.output_price_usd_per_1m ?? 0 },
  ];
  return <div className="model-pricing-source-preview" aria-label={tx("来源价格预览")}>
    <div className="model-pricing-source-heading"><Link2 size={16} /><strong>{tx("来源价格预览")}</strong><span>{tx("USD / 1M tokens")}</span></div>
    <dl className="model-pricing-source-rates">{prices.map(item => <div key={item.label}><dt>{tx(item.label)}</dt><dd>{rate(item.value)}</dd></div>)}</dl>
    <details className="model-pricing-source-cache"><summary>{tx("缓存写入价格")}</summary><dl className="model-pricing-source-rates">
      <div><dt>{tx("缓存写入")}</dt><dd>{rate(cacheWrite)}</dd></div>
      <div><dt>{tx("5 分钟缓存写入")}</dt><dd>{rate(source.cache_write_5m_price_configured || (source.cache_write_5m_price_usd_per_1m ?? 0) > 0 ? source.cache_write_5m_price_usd_per_1m ?? 0 : cacheWrite)}</dd></div>
      <div><dt>{tx("1 小时缓存写入")}</dt><dd>{rate(source.cache_write_1h_price_configured || (source.cache_write_1h_price_usd_per_1m ?? 0) > 0 ? source.cache_write_1h_price_usd_per_1m ?? 0 : cacheWrite)}</dd></div>
    </dl></details>
    <div className="model-pricing-source-schedules"><strong>{tx("来源分时时段")}</strong>{source.pricing_periods?.length ? source.pricing_periods.map((period, index) => <div className="model-pricing-source-period" key={index}>
      <div><strong>{period.name || formatTranslationTemplate(tx("时段 {number}"), { number: new Intl.NumberFormat(languageLocale()).format(index + 1) })}</strong><span>{scheduleTime(period)} · {period.timezone?.trim() || "UTC"} · {scheduleDays(period)}</span></div>
      <dl className="model-pricing-source-rates"><div><dt>{tx("输入（缓存未命中）")}</dt><dd>{rate(period.input_price_usd_per_1m)}</dd></div><div><dt>{tx("输入（缓存命中）")}</dt><dd>{rate(period.cache_read_price_usd_per_1m)}</dd></div><div><dt>{tx("输出")}</dt><dd>{rate(period.output_price_usd_per_1m)}</dd></div></dl>
      {[period.cache_write_price_usd_per_1m, period.cache_write_5m_price_usd_per_1m, period.cache_write_1h_price_usd_per_1m].some(value => value != null) ? <dl className="model-pricing-source-rates"><div><dt>{tx("缓存写入")}</dt><dd>{rate(period.cache_write_price_usd_per_1m)}</dd></div><div><dt>{tx("5 分钟缓存写入")}</dt><dd>{rate(period.cache_write_5m_price_usd_per_1m)}</dd></div><div><dt>{tx("1 小时缓存写入")}</dt><dd>{rate(period.cache_write_1h_price_usd_per_1m)}</dd></div></dl> : null}
      {period.effective_from?.trim() || period.effective_until?.trim() ? <small>{tx("生效区间")}: {effectiveDate(period.effective_from, period.timezone || "UTC")} – {effectiveDate(period.effective_until, period.timezone || "UTC")}</small> : null}
    </div>) : <p>{tx("未配置分时时段，全天使用基础价。")}</p>}</div>
    <p className="model-pricing-source-help">{tx("按请求开始时间匹配时段；未覆盖的分项使用基础价。此处为当前配置预览。")}</p>
  </div>;
}
