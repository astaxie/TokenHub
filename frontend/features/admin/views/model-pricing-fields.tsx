import { Field, RateFields, emptyRates, type Card, type Period } from "./billing-pricing-fields";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";

function localInstant(value?: string) {
  if (!value) return "";
  const at = new Date(value); return Number.isNaN(at.getTime()) ? "" : new Date(at.getTime() - at.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
}
export function ModelPricingFields({ card, onChange, embedding }: { card: Card; onChange: (card: Card) => void; embedding: boolean }) {
  function period(index: number, patch: Partial<Period>) { onChange({ ...card, periods: card.periods.map((item, i) => i === index ? { ...item, ...patch } : item) }); }
  return <section className="billing-step">
    <h3>{tx("填写基础价格")}</h3><p className="billing-hint">{tx("单价单位：USD / 百万 Token")}</p>
    <RateFields inputOnly={embedding} rates={card.rates} update={(key, value) => onChange({ ...card, rates: { ...card.rates, [key]: value } })} />
    {!embedding ? <><details className="billing-disclosure"><summary>{tx("单独设置缓存价格")}</summary><p>{tx("空白缓存项保持模型默认规则，填写 0 表示明确免费。")}</p><RateFields cache rates={card.rates} update={(key, value) => onChange({ ...card, rates: { ...card.rates, [key]: value } })} /></details>
      <details className="billing-disclosure"><summary>{tx("设置峰谷时段")}</summary><p className="billing-hint">{tx("只填写不同于基础价格的项目。跨午夜按开始日计算，时段不能重叠。")}</p>
        {card.periods.map((item, index) => <fieldset className="billing-period" key={index}><legend>{item.name}</legend><div className="billing-form-grid">
          <Field label="名称"><input value={item.name} onChange={e => period(index, { name: e.target.value })} /></Field><Field label="时区"><input value={item.timezone} onChange={e => period(index, { timezone: e.target.value })} /></Field>
          <Field label="开始时间"><input type="time" value={item.start_time} onChange={e => period(index, { start_time: e.target.value })} /></Field><Field label="结束时间"><input type="time" value={item.end_time} onChange={e => period(index, { end_time: e.target.value })} /></Field>
        </div><div className="billing-weekdays" role="group" aria-label={tx("适用星期")}>{["周日", "周一", "周二", "周三", "周四", "周五", "周六"].map((day, n) => <label key={day}><input type="checkbox" checked={item.weekdays.includes(n)} onChange={e => period(index, { weekdays: e.target.checked ? [...item.weekdays, n] : item.weekdays.filter(d => d !== n) })} /><span>{tx(day)}</span></label>)}</div>
          <RateFields rates={item.rates} update={(key, value) => period(index, { rates: { ...item.rates, [key]: value } })} />
          <details className="billing-disclosure"><summary>{tx("时段缓存价格")}</summary><RateFields cache rates={item.rates} update={(key, value) => period(index, { rates: { ...item.rates, [key]: value } })} /></details>
          <details className="billing-disclosure"><summary>{tx("设置生效日期")}</summary><p>{tx("生效日期按当前浏览器时区输入，留空不限制日期。")}</p><div className="billing-form-grid"><Field label="生效起始"><input type="datetime-local" value={localInstant(item.effective_from)} onChange={e => period(index, { effective_from: e.target.value ? new Date(e.target.value).toISOString() : undefined })} /></Field><Field label="生效截止"><input type="datetime-local" value={localInstant(item.effective_until)} onChange={e => period(index, { effective_until: e.target.value ? new Date(e.target.value).toISOString() : undefined })} /></Field></div></details>
          <button type="button" className="text-button billing-remove" onClick={() => onChange({ ...card, periods: card.periods.filter((_, i) => i !== index) })}>{tx("删除时段")}</button>
        </fieldset>)}
        <button type="button" className="secondary-button" onClick={() => onChange({ ...card, periods: [...card.periods, { name: formatTranslationTemplate(tx("时段 {number}"), { number: new Intl.NumberFormat(languageLocale()).format(card.periods.length + 1) }), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, weekdays: [1, 2, 3, 4, 5], start_time: "09:00", end_time: "12:00", rates: emptyRates() }] })}>{tx("添加时段")}</button>
      </details></> : null}
  </section>;
}
