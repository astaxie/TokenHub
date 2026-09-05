import { useState } from "react";
import type { ApiContext, AppData } from "../core/types";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection } from "../shared/ui";

type Rates = Record<"input" | "cache_read" | "cache_write" | "cache_write_5m" | "cache_write_1h" | "output", string>;
type Period = { name: string; timezone: string; weekdays: number[]; start_time: string; end_time: string; rates: Rates };
type Card = { id?: string; revision?: number; kind: string; target: string; source: string; currency: string; effective_from?: string; rates: Rates; periods: Period[] };
type Preview = { snapshot: { period?: string }; charge: { amount: string; currency: string; usd?: string } };
const emptyRates = (): Rates => ({ input: "", cache_read: "", cache_write: "", cache_write_5m: "", cache_write_1h: "", output: "" });
const rateLabels: [keyof Rates, string][] = [["input", "普通输入"], ["cache_read", "缓存读取"], ["cache_write", "其他缓存写入"], ["cache_write_5m", "5 分钟缓存写入"], ["cache_write_1h", "1 小时缓存写入"], ["output", "输出"]];

function RateFields({ rates, update }: { rates: Rates; update: (key: keyof Rates, value: string) => void }) {
  return <div className="form-grid">{rateLabels.map(([key, label]) => <label key={key}>{tx(label)}<input inputMode="decimal" value={rates[key]} onChange={(event) => update(key, event.target.value)} /></label>)}</div>;
}

export function BillingRateCards({ api, data }: { api: ApiContext; data: AppData }) {
  const [card, setCard] = useState<Card>({ kind: "tenant", target: "", source: "", currency: "USD", rates: emptyRates(), periods: [] });
  const [at, setAt] = useState(() => new Date().toISOString());
  const [usage, setUsage] = useState({ prompt_tokens: "1000000", cached_input_tokens: "800000", completion_tokens: "10000" });
  const [fx, setFX] = useState("");
  const [previewCard, setPreviewCard] = useState("");
  const [preview, setPreview] = useState<Preview | null>(null);
  const [cards, setCards] = useState<Card[]>([]);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [requestID, setRequestID] = useState("");
  const [evidence, setEvidence] = useState<unknown>(null);
  async function request(path: string, body?: unknown) {
    const response = await adminFetch(api, path, body ? { method: "POST", body: JSON.stringify(body) } : undefined);
    if (!response.ok) throw new Error(await readAdminError(response, "计费操作失败"));
    return response.json();
  }
  async function act(action: "preview" | "publish" | "list" | "evidence") {
    setBusy(true); setError(""); setMessage("");
    try {
      if (action === "preview") {
        if (card.periods.some((period) => period.weekdays.length === 0)) throw new Error(tx("每个时段至少选择一天"));
        const counts = Object.fromEntries(Object.entries(usage).map(([key, value]) => [key, Number(value)]));
        if (Object.values(counts).some((value) => !Number.isSafeInteger(value) || value < 0)) throw new Error(tx("Token 数必须是非负安全整数"));
        setPreview(await request("/api/admin/billing/preview", { card, at, usage: counts, exchange_rate: fx }));
        setPreviewCard(JSON.stringify(card));
      } else if (action === "publish") {
        const result = await request("/api/admin/billing/rate-cards", card);
        setCards((current) => [result.data, ...current]);
        setMessage(tx("影子价目已发布，新请求将保留此版本的计价证据。"));
      } else if (action === "list") {
        setCards((await request("/api/admin/billing/rate-cards")).data);
      } else {
        setEvidence(await request(`/api/admin/billing/evidence/${encodeURIComponent(requestID)}`));
      }
    } catch (caught) { setError(caught instanceof Error ? caught.message : tx("计费操作失败")); }
    finally { setBusy(false); }
  }
  function updatePeriod(index: number, patch: Partial<Period>) { setCard((current) => ({ ...current, periods: current.periods.map((period, i) => i === index ? { ...period, ...patch } : period) })); setPreview(null); }
  return <DataSection title="精确计价与影子核对">
    <p>{tx("影子价目用于核对，不改变当前收费与预算。单价单位为原币/百万 Token；填写 0 表示免费，时段留空表示继承。")}</p>
    <form onSubmit={(event) => { event.preventDefault(); void act("preview"); }} onChange={() => setPreview(null)}>
      <div className="form-grid">
        <label>{tx("价目用途")}<select value={card.kind} onChange={(event) => setCard({ ...card, kind: event.target.value, target: "", currency: "USD" })}><option value="tenant">{tx("租户费用")}</option><option value="provider">{tx("上游成本")}</option></select></label>
        <label>{tx("计价对象")}<select required value={card.target} onChange={(event) => setCard({ ...card, target: event.target.value })}><option value="">{tx("请选择")}</option>{card.kind === "tenant" ? data.models.map((model) => <option key={model.name} value={model.name}>{model.name}</option>) : data.providerModels.map((model) => <option key={model.id} value={`${model.provider_id}:${model.upstream_model}`}>{model.provider_id} / {model.upstream_model}</option>)}</select></label>
        <label>{tx("币种")}<input required pattern="[A-Z]{3}" value={card.currency} readOnly={card.kind === "tenant"} onChange={(event) => setCard({ ...card, currency: event.target.value.toUpperCase() })} /></label>
        <label>{tx("价格依据")}<input required value={card.source} onChange={(event) => setCard({ ...card, source: event.target.value })} /></label>
      </div>
      <RateFields rates={card.rates} update={(key, value) => setCard({ ...card, rates: { ...card.rates, [key]: value } })} />
      {card.periods.map((period, index) => <fieldset key={index}>
        <legend>{tx("时段覆盖")}</legend>
        <div className="form-grid">
          <label>{tx("名称")}<input value={period.name} onChange={(event) => updatePeriod(index, { name: event.target.value })} /></label>
          <label>{tx("时区")}<input value={period.timezone} onChange={(event) => updatePeriod(index, { timezone: event.target.value })} /></label>
          <label>{tx("开始时间")}<input type="time" value={period.start_time} onChange={(event) => updatePeriod(index, { start_time: event.target.value })} /></label>
          <label>{tx("结束时间")}<input type="time" value={period.end_time} onChange={(event) => updatePeriod(index, { end_time: event.target.value })} /></label>
        </div>
        <div>{["周日", "周一", "周二", "周三", "周四", "周五", "周六"].map((day, number) => <label key={day}><input type="checkbox" checked={period.weekdays.includes(number)} onChange={(event) => updatePeriod(index, { weekdays: event.target.checked ? [...period.weekdays, number] : period.weekdays.filter((value) => value !== number) })} />{tx(day)}</label>)}</div>
        <RateFields rates={period.rates} update={(key, value) => updatePeriod(index, { rates: { ...period.rates, [key]: value } })} />
        <button type="button" onClick={() => setCard({ ...card, periods: card.periods.filter((_, i) => i !== index) })}>{tx("删除时段")}</button>
      </fieldset>)}
      <button type="button" onClick={() => setCard({ ...card, periods: [...card.periods, { name: `period-${card.periods.length + 1}`, timezone: "Asia/Shanghai", weekdays: [1, 2, 3, 4, 5], start_time: "09:00", end_time: "12:00", rates: emptyRates() }] })}>{tx("添加时段")}</button>
      <div className="form-grid">
        <label>{tx("预览时刻（RFC3339）")}<input required value={at} onChange={(event) => setAt(event.target.value)} /></label>
        <label>{tx("总输入 Token")}<input inputMode="numeric" value={usage.prompt_tokens} onChange={(event) => setUsage({ ...usage, prompt_tokens: event.target.value })} /></label>
        <label>{tx("缓存读取 Token")}<input inputMode="numeric" value={usage.cached_input_tokens} onChange={(event) => setUsage({ ...usage, cached_input_tokens: event.target.value })} /></label>
        <label>{tx("输出 Token")}<input inputMode="numeric" value={usage.completion_tokens} onChange={(event) => setUsage({ ...usage, completion_tokens: event.target.value })} /></label>
        <label>{tx("汇率：1 原币兑 USD")}<input inputMode="decimal" value={fx} onChange={(event) => setFX(event.target.value)} /></label>
      </div>
      <button disabled={busy} type="submit">{tx("预览费用")}</button>
      <button disabled={busy || !preview || previewCard !== JSON.stringify(card)} type="button" onClick={() => void act("publish")}>{tx("发布影子价目")}</button>
    </form>
    {preview ? <output><p>{preview.charge.amount} {preview.charge.currency}</p><p>{preview.charge.usd ? `${preview.charge.usd} USD` : tx("USD 折算待定")}</p><p>{preview.snapshot.period || tx("默认价格")}</p></output> : null}
    {error ? <p role="alert">{error}</p> : null}{message ? <p role="status">{message}</p> : null}
    <button type="button" disabled={busy} onClick={() => void act("list")}>{tx("读取已发布版本")}</button>
    <ul>{cards.map((item) => <li key={item.id}>{item.target} · {item.id} · {item.effective_from ? new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "medium" }).format(new Date(item.effective_from)) : ""}</li>)}</ul>
    <label>{tx("请求 ID")}<input value={requestID} onChange={(event) => setRequestID(event.target.value)} /></label>
    <button type="button" disabled={busy || !requestID.trim()} onClick={() => void act("evidence")}>{tx("读取计费证据")}</button>
    {evidence ? <pre>{JSON.stringify(evidence, null, 2)}</pre> : null}
  </DataSection>;
}
