import { Calculator, Check, ChevronRight, Clock3, Plus, ShieldCheck, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { ApiContext, AppData } from "../core/types";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { BillingPricingHistory } from "./billing-pricing-history";
import { type Card, type Period, Field, RateFields, decimalAmount, emptyRates, localDateTime, rateLabels } from "./billing-pricing-fields";

type Preview = { current?: Card; proposed?: Card; fingerprint?: string; changed?: boolean; snapshot: { period?: string }; charge: { amount: string; currency: string; usd?: string; lines: { kind: string; units: number; amount: string }[] } };
const initialUsage = { prompt_tokens: "1000", cached_input_tokens: "0", completion_tokens: "1000", cache_write_other_tokens: "0", cache_write_5m_input_tokens: "0", cache_write_1h_input_tokens: "0" };

export function BillingRateCards({ api, data, view = "pricing", onApplied }: { api: ApiContext; data: AppData; view?: "pricing" | "history"; onApplied?: () => Promise<void> }) {
  const [card, setCard] = useState<Card>({ kind: "tenant", target: "", source: "manual", currency: "USD", rates: emptyRates(), periods: [] });
  const [at, setAt] = useState(localDateTime);
  const [usage, setUsage] = useState(initialUsage);
  const [fx, setFX] = useState("");
  const [preview, setPreview] = useState<Preview | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [published, setPublished] = useState(false);
  const [revision, setRevision] = useState(0);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [loadingModel, setLoadingModel] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const selection = useRef(0);
  const applyID = useRef("");
  useEffect(() => { if (confirmOpen) dialog.current?.showModal(); else dialog.current?.close(); }, [confirmOpen]);
  function invalidate() { applyID.current = ""; setConfirmOpen(false); setPreview(null); setError(""); setMessage(""); setPublished(false); }
  function updateCard(next: Card) { setCard(next); invalidate(); }
  function updatePeriod(index: number, patch: Partial<Period>) { updateCard({ ...card, periods: card.periods.map((period, i) => i === index ? { ...period, ...patch } : period) }); }
  const embedding = card.kind === "tenant" && data.models.some((model) => model.name === card.target && model.modality === "embedding");
  const targets = card.kind === "tenant" ? data.models.filter((model) => !model.modality || ["chat", "embedding"].includes(model.modality)).map((model) => ({ id: model.name, label: model.name })) : data.providerModels.map((model) => ({ id: `${model.provider_id}:${model.upstream_model}`, label: `${data.providers.find((provider) => provider.id === model.provider_id)?.name ?? model.provider_id} / ${model.upstream_model}` }));
  async function selectTarget(target: string) {
    const run = ++selection.current;
    updateCard({ ...card, target, rates: emptyRates(), periods: [] });
    if (!target) return;
    if (card.kind === "provider") {
      const model = data.providerModels.find((item) => `${item.provider_id}:${item.upstream_model}` === target);
      if (model) setCard({ ...card, target, periods: [], rates: { input: (model.input_price_usd_per_1m || model.metadata?.input_price_configured === "true") ? String(model.input_price_usd_per_1m ?? 0) : "", output: (model.output_price_usd_per_1m || model.metadata?.output_price_configured === "true") ? String(model.output_price_usd_per_1m ?? 0) : "", cache_read: (model.cache_read_price_usd_per_1m || model.metadata?.cache_read_price_configured === "true") ? String(model.cache_read_price_usd_per_1m ?? 0) : "", cache_write: model.cache_write_price_configured ? String(model.cache_write_price_usd_per_1m ?? 0) : "", cache_write_5m: model.cache_write_5m_price_configured ? String(model.cache_write_5m_price_usd_per_1m ?? 0) : "", cache_write_1h: model.cache_write_1h_price_configured ? String(model.cache_write_1h_price_usd_per_1m ?? 0) : "" } });
      return;
    }
    setLoadingModel(true);
    try {
      const response = await adminFetch(api, `/api/admin/billing/models/${encodeURIComponent(target)}/pricing`);
      if (!response.ok) throw new Error(await readAdminError(response, "读取模型价格失败"));
      const result = await response.json();
      if (run === selection.current && data.models.some((model) => model.name === target && model.modality === "embedding")) setUsage({ ...initialUsage, completion_tokens: "0" });
      if (run === selection.current) setCard({ ...result.card, periods: (result.card.periods ?? []).map((period: Period) => ({ ...period, timezone: period.timezone || "UTC", weekdays: period.weekdays?.length ? period.weekdays : [0, 1, 2, 3, 4, 5, 6], start_time: period.start_time || "", end_time: period.end_time || "" })) });
    } catch (caught) { if (run === selection.current) setError(caught instanceof Error ? caught.message : tx("读取模型价格失败")); }
    finally { if (run === selection.current) setLoadingModel(false); }
  }
  async function request(path: string, body: unknown) {
    const response = await adminFetch(api, path, { method: "POST", body: JSON.stringify(body) });
    if (!response.ok) {
      const detail = await readAdminError(response, "计费操作失败");
      if (detail.includes("changed after preview")) { setPreview(null); setConfirmOpen(false); throw new Error(tx("模型价格已被修改，请重新试算并确认。")); }
      if (detail.includes("overlap")) throw new Error(tx("时段不能重叠，请调整开始和结束时间。"));
      if (detail.includes("price missing") || detail.includes("pricing_incomplete")) throw new Error(tx("试算用量涉及未知单价，请补充价格或将该项用量设为 0。"));
      if (detail.includes("start_time") || detail.includes("end_time")) throw new Error(tx("请填写完整的时段开始和结束时间。"));
      if (detail.includes("timezone")) throw new Error(tx("请填写有效时区，例如 Asia/Shanghai。"));
      throw new Error(detail);
    }
    return response.json();
  }
  async function act(action: "preview" | "apply") {
    setBusy(true); setError(""); setMessage("");
    try {
      const payload = { ...card, source: card.source.trim() || "model", rates: card.rates };
      if (!card.target) throw new Error(tx("请先选择要试算的模型。"));
      if (!/^[A-Z]{3}$/.test(card.currency)) throw new Error(tx("币种请填写三个大写字母，例如 USD 或 CNY。"));
      const decimal = /^(0|[1-9][0-9]{0,17})(\.[0-9]{1,12})?$/;
      if (card.kind === "tenant" && (!decimal.test(payload.rates.input) || !decimal.test(payload.rates.output))) throw new Error(tx("请填写普通输入和输出单价，免费请填 0。"));
      if (Object.values(payload.rates).some((rate) => rate !== "" && !decimal.test(rate)) || card.periods.some((period) => Object.values(period.rates).some((rate) => rate !== "" && !decimal.test(rate)))) throw new Error(tx("单价须为非负小数，最多 12 位小数。"));
      if (card.periods.some((period) => period.weekdays.length === 0)) throw new Error(tx("每个时段至少选择一天"));
      if (action === "preview") {
        applyID.current = ""; setPublished(false); setPreview(null);
        const counts = Object.fromEntries(Object.entries(usage).map(([key, value]) => [key, value.trim() === "" ? 0 : Number(value)]));
        if (Object.values(counts).some((value) => !Number.isSafeInteger(value) || value < 0)) throw new Error(tx("Token 数必须是非负安全整数"));
        const write = counts.cache_write_other_tokens + counts.cache_write_5m_input_tokens + counts.cache_write_1h_input_tokens;
        if (!Number.isSafeInteger(write) || counts.cached_input_tokens + write > counts.prompt_tokens) throw new Error(tx("缓存读取与写入之和不能超过总输入 Token。"));
        const requiredRates = [[payload.rates.input, counts.prompt_tokens - counts.cached_input_tokens - write], [payload.rates.cache_read, counts.cached_input_tokens], [payload.rates.cache_write, counts.cache_write_other_tokens], [payload.rates.cache_write_5m, counts.cache_write_5m_input_tokens], [payload.rates.cache_write_1h, counts.cache_write_1h_input_tokens], [payload.rates.output, counts.completion_tokens]] as const;
        if (card.kind === "provider" && card.periods.length === 0 && requiredRates.some(([rate, count]) => count > 0 && rate === "")) throw new Error(tx("试算用量涉及未知单价，请补充价格或将该项用量设为 0。"));
        const instant = new Date(at);
        if (Number.isNaN(instant.getTime())) throw new Error(tx("请选择有效的试算时间。"));
        if (card.currency !== "USD" && fx !== "" && (!decimal.test(fx) || !/[1-9]/.test(fx))) throw new Error(tx("汇率必须是大于 0 的小数。"));
        const { cache_write_other_tokens: _other, ...reported } = counts;
        void _other;
        setPreview(await request("/api/admin/billing/preview", { card: payload, at: instant.toISOString(), usage: { ...reported, cache_write_input_tokens: write }, exchange_rate: card.currency === "USD" ? "" : fx }));
      } else if (preview?.proposed && preview.fingerprint && confirmOpen && !published) {
        if (!applyID.current) applyID.current = crypto.randomUUID();
        await request("/api/admin/billing/model-pricing/apply", { card: preview.proposed, fingerprint: preview.fingerprint, request_id: applyID.current, confirmed: true });
        setPublished(true); setConfirmOpen(false); setRevision((current) => current + 1);
        setMessage(tx("模型价格已更新。新请求立即使用新价，在途请求保持原价。"));
        if (onApplied) await onApplied();
      }
    } catch (caught) { setError(caught instanceof Error ? caught.message : tx("计费操作失败")); }
    finally { setBusy(false); }
  }
  function addPeriod() {
    updateCard({ ...card, periods: [...card.periods, { name: formatTranslationTemplate(tx("时段 {number}"), { number: String(card.periods.length + 1) }), timezone: "Asia/Shanghai", weekdays: [1, 2, 3, 4, 5], start_time: "09:00", end_time: "12:00", rates: emptyRates() }] });
  }
  return <>
    <div hidden={view !== "pricing"} className="billing-pricing" role="tabpanel" id="billing-panel-pricing" aria-labelledby="billing-tab-pricing">
      <div className="billing-intro"><div><h2>{tx("试算后确认调整模型价格")}</h2><p>{tx("选一个模型，输入价格与用量，即可比较普通、缓存和峰谷时段的费用。")}</p></div><span className="billing-safe"><ShieldCheck size={15} />{tx("试算不改价，确认后生效")}</span></div>
      <div className="billing-workbench">
        <form className="billing-editor" onSubmit={(event) => { event.preventDefault(); void act("preview"); }} onChange={invalidate}>
          <fieldset disabled={busy || loadingModel} className="billing-form-content">
            <section className="billing-step">
              <div className="billing-step-heading"><span>1</span><h3>{tx("选择计价对象")}</h3></div>
              <div className="billing-form-grid"><Field label="价目用途"><select value={card.kind} onChange={(event) => updateCard({ ...card, kind: event.target.value, target: "", currency: "USD" })}><option value="tenant">{tx("租户费用")}</option><option value="provider">{tx("上游成本")}</option></select></Field><Field label="计价对象"><select value={card.target} onChange={(event) => void selectTarget(event.target.value)}><option value="">{tx("请选择模型")}</option>{targets.map((target) => <option key={target.id} value={target.id}>{target.label}</option>)}</select></Field></div>
              {targets.length === 0 ? <p className="billing-hint">{tx("暂无可选模型，请先在 AI 资源中配置模型。")}</p> : null}
              {card.kind === "provider" ? <Field label="币种"><input pattern="[A-Z]{3}" maxLength={3} value={card.currency} onChange={(event) => updateCard({ ...card, currency: event.target.value.toUpperCase() })} /></Field> : null}
            </section>
            <section className="billing-step">
              <div className="billing-step-heading"><span>2</span><h3>{tx("填写基础价格")}</h3><small>{card.currency} / {tx("百万 Token")}</small></div>
              {card.kind === "provider" ? <p className="billing-hint">{tx("上游单价未知可留空，试算时该项用量需为 0。")}</p> : null}
              <RateFields inputOnly={embedding} rates={card.rates} update={(key, value) => updateCard({ ...card, rates: { ...card.rates, [key]: value } })} />
              <details hidden={embedding} className="billing-disclosure"><summary>{tx("单独设置缓存价格")}<small>{tx("可选")}</small></summary><p className="billing-hint">{tx(card.kind === "tenant" ? "缓存价留空使用模型默认规则；填写 0 表示明确免费。" : "上游缓存价格未知时请留空；已知时填写单价，0 表示明确免费。")}</p><RateFields cache inherit={card.kind === "tenant"} rates={card.rates} update={(key, value) => updateCard({ ...card, rates: { ...card.rates, [key]: value } })} /></details>
              {card.kind === "tenant" ? <p className="billing-hint">{tx("选择模型后带入当前价格。空白缓存项保持默认规则，试算与实际收费使用同一计价逻辑。")}</p> : null}
              <details hidden={embedding} className="billing-disclosure"><summary><Clock3 size={16} />{tx("设置峰谷时段")}<small>{tx("可选")}</small></summary><p className="billing-hint">{tx("只填写不同于基础价格的项目。跨午夜按开始日计算，时段不能重叠。")}</p>
                {card.periods.map((period, index) => <fieldset className="billing-period" key={index}><legend>{period.name}</legend><div className="billing-form-grid"><Field label="名称"><input value={period.name} onChange={(event) => updatePeriod(index, { name: event.target.value })} /></Field><Field label="时区"><input value={period.timezone} onChange={(event) => updatePeriod(index, { timezone: event.target.value })} /></Field><Field label="开始时间"><input type="time" value={period.start_time} onChange={(event) => updatePeriod(index, { start_time: event.target.value })} /></Field><Field label="结束时间"><input type="time" value={period.end_time} onChange={(event) => updatePeriod(index, { end_time: event.target.value })} /></Field></div>
                  <div className="billing-weekdays" role="group" aria-label={tx("适用星期")}>{["周日", "周一", "周二", "周三", "周四", "周五", "周六"].map((day, number) => <label key={day}><input type="checkbox" checked={period.weekdays.includes(number)} onChange={(event) => updatePeriod(index, { weekdays: event.target.checked ? [...period.weekdays, number] : period.weekdays.filter((value) => value !== number) })} /><span>{tx(day)}</span></label>)}</div>
                  <RateFields rates={period.rates} update={(key, value) => updatePeriod(index, { rates: { ...period.rates, [key]: value } })} /><details className="billing-disclosure"><summary>{tx("时段缓存价格")}</summary><RateFields cache rates={period.rates} update={(key, value) => updatePeriod(index, { rates: { ...period.rates, [key]: value } })} /></details><button type="button" className="text-button billing-remove" onClick={() => updateCard({ ...card, periods: card.periods.filter((_, i) => i !== index) })}><Trash2 size={14} />{tx("删除时段")}</button>
                </fieldset>)}
                <button type="button" className="secondary-button" onClick={addPeriod}><Plus size={15} />{tx("添加时段")}</button>
              </details>
            </section>
            <section className="billing-step">
              <div className="billing-step-heading"><span>3</span><h3>{tx("输入试算用量")}</h3></div>
              <div className="billing-form-grid"><Field label="总输入 Token"><input inputMode="numeric" value={usage.prompt_tokens} onChange={(event) => setUsage({ ...usage, prompt_tokens: event.target.value })} /></Field><Field label="输出 Token"><input disabled={embedding} inputMode="numeric" value={usage.completion_tokens} onChange={(event) => setUsage({ ...usage, completion_tokens: event.target.value })} /></Field></div>
              <details hidden={embedding} className="billing-disclosure"><summary>{tx("缓存用量与试算时间")}<small>{tx("可选")}</small></summary><p className="billing-hint">{tx("总输入已包含缓存读取和写入，请勿重复累加。")}</p><div className="billing-form-grid">{([["cached_input_tokens", "缓存读取 Token"], ["cache_write_other_tokens", "其他缓存写入 Token"], ["cache_write_5m_input_tokens", "5 分钟缓存写入 Token"], ["cache_write_1h_input_tokens", "1 小时缓存写入 Token"]] as const).map(([key, label]) => <Field key={key} label={label}><input inputMode="numeric" value={usage[key]} onChange={(event) => setUsage({ ...usage, [key]: event.target.value })} /></Field>)}<Field label="试算时间" hint={formatTranslationTemplate(tx("当前时区：{timezone}"), { timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })}><input type="datetime-local" value={at} onChange={(event) => setAt(event.target.value)} /></Field></div></details>
              {card.currency !== "USD" ? <Field label="汇率：1 原币兑 USD" hint="仅用于本次试算；正式汇率版本需单独发布。"><input inputMode="decimal" placeholder={tx("可留空，先查看原币费用")} value={fx} onChange={(event) => setFX(event.target.value)} /></Field> : null}

              <button disabled={busy || loadingModel} className="primary-button billing-preview-button" type="submit"><Calculator size={17} />{tx(busy ? "正在计算…" : "试算费用")}</button>
            </section>
          </fieldset>
        </form>
        <aside className="billing-result" aria-label={tx("试算结果")}>
          <div className="billing-result-heading"><Calculator size={18} /><h3>{tx("试算结果")}</h3></div>
          {error && !confirmOpen ? <p className="billing-notice billing-error" role="alert">{error}</p> : null}
          {preview ? <><div className="billing-result-total"><small>{tx("本次预计费用")}</small><output>{decimalAmount(preview.charge.amount)} <span>{preview.charge.currency}</span></output>{preview.charge.currency !== "USD" ? <small>{preview.charge.usd ? `${decimalAmount(preview.charge.usd)} USD` : tx("USD 折算待定")}</small> : null}</div><p className="billing-period-badge"><Clock3 size={14} />{preview.snapshot.period || tx("默认价格")}</p><dl className="billing-cost-lines">{preview.charge.lines.map((line) => <div key={line.kind}><dt>{tx(rateLabels.find(([key]) => key === line.kind)?.[1] ?? line.kind)}<small>{new Intl.NumberFormat(languageLocale()).format(line.units)} Token</small></dt><dd>{decimalAmount(line.amount)}</dd></div>)}</dl>{card.kind === "tenant" ? <div className="billing-publish"><h4>{tx("将试算价格应用到模型")}</h4><p>{tx("下一步会展示新旧价格差异，确认后才会改变下游收费。")}</p><button className="secondary-button" disabled={busy || published || !preview.changed} type="button" onClick={() => setConfirmOpen(true)}>{published ? <Check size={16} /> : <ChevronRight size={16} />}{tx(published ? "已应用" : preview.changed ? "应用到模型价格" : "价格未改变")}</button></div> : <p className="billing-hint">{tx("上游成本试算仅供参考；调整下游收费请选择租户费用。")}</p>}</> : <div className="billing-result-empty"><Calculator size={34} /><h4>{tx("费用会显示在这里")}</h4><p>{tx("填好左侧价格和用量，点击「试算费用」。缓存和峰谷配置可按需展开。")}</p><span>{tx("试算不会保存或修改价格。")}</span></div>}
          {message ? <p className="billing-notice billing-success" role="status">{message}</p> : null}
          <a className="billing-actual-link" href="/models">{tx("查看模型目录")}<ChevronRight size={14} /></a>
        </aside>
      </div>
    </div>
    <dialog ref={dialog} className="billing-confirm-dialog" aria-labelledby="billing-confirm-title" onCancel={(event) => { if (busy) event.preventDefault(); else setConfirmOpen(false); }}>
      <h2 id="billing-confirm-title">{tx("确认调整模型价格")}</h2>{error && confirmOpen ? <p role="alert" className="billing-notice billing-error">{error}</p> : null}<p>{tx("请核对目标模型与价格变化。确认后新请求立即使用新价，在途请求不受影响。")}</p>
      <strong className="billing-confirm-model">{preview?.proposed?.target}</strong>
      <table><thead><tr><th>{tx("计价项目")}</th><th>{tx("当前价格")}</th><th>{tx("新价格")}</th></tr></thead><tbody>{rateLabels.map(([key, label]) => <tr key={key} data-changed={preview?.current?.rates[key] !== preview?.proposed?.rates[key]}><td>{tx(label)}</td><td>{preview?.current?.rates[key] || tx("模型默认规则")}</td><td>{preview?.proposed?.rates[key] || tx("模型默认规则")}</td></tr>)}</tbody></table>
      <p>{tx("单价单位：USD / 百万 Token")}</p><details><summary>{tx("核对分时规则")}</summary><div className="billing-confirm-periods">{([preview?.current, preview?.proposed] as const).map((item, index) => <div key={index}><strong>{tx(index === 0 ? "当前价格" : "新价格")}</strong>{item?.periods?.length ? item.periods.map((period, i) => <p key={i}>{period.name} · {period.timezone} · {period.start_time}–{period.end_time}<br />{period.weekdays?.length ? period.weekdays.map((day) => tx(["周日", "周一", "周二", "周三", "周四", "周五", "周六"][day])).join(" / ") : tx("每天")}<br />{period.effective_from || ""} {period.effective_until || ""}<br />{rateLabels.map(([key, label]) => `${tx(label)}: ${period.rates[key] || tx("模型默认规则")}`).join(" / ")}</p>) : <p>{tx("无分时规则")}</p>}</div>)}</div></details>
      <div className="billing-confirm-actions"><button type="button" className="secondary-button" disabled={busy} onClick={() => setConfirmOpen(false)}>{tx("取消")}</button><button type="button" className="primary-button" disabled={busy || !preview?.changed} onClick={() => void act("apply")}>{tx(busy ? "正在应用…" : "确认应用价格")}</button></div>
    </dialog>
    <div hidden={view !== "history"} role="tabpanel" id="billing-panel-history" aria-labelledby="billing-tab-history">{view === "history" ? <BillingPricingHistory api={api} data={data} revision={revision} /> : null}</div>
  </>;
}
