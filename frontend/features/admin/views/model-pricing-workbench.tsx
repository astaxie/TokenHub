import { pricingRequest } from "./pricing-analysis-api";
import { PricingImpactPanel, type PricingImpact } from "./pricing-impact-panel";
import { useEffect, useRef, useState } from "react";
import type { ApiContext, AppData, Model } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { type Card, emptyRates, rateLabels, displayDate, decimalAmount } from "./billing-pricing-fields";
import { ModelPricingFields } from "./model-pricing-fields";
import { ModelRuleSimulator } from "./model-rule-simulator";
import { BillingPricingHistory } from "./billing-pricing-history";

type Check = { current: Card; proposed: Card; fingerprint: string; changed: boolean; analysis?: PricingImpact };

export function ModelPricingWorkbench({ api, data, model, onApplied }: { api: ApiContext; data: AppData; model: Model; onApplied: () => Promise<void> | void }) {
  const [card, setCard] = useState<Card | null>(null); const [fingerprint, setFingerprint] = useState(""); const [reload, setReload] = useState(0);
  const [analysis, setAnalysis] = useState<PricingImpact | null>(null);
  const [check, setCheck] = useState<Check | null>(null); const [error, setError] = useState(""); const [message, setMessage] = useState(""); const [busy, setBusy] = useState(false);
  const [acknowledged, setAcknowledged] = useState(false); const [revision, setRevision] = useState(0);
  const dialog = useRef<HTMLDialogElement>(null); const requestID = useRef("");
  useEffect(() => {
    const abort = new AbortController(); setCard(null); setAnalysis(null); setFingerprint(""); setCheck(null); setError("");
    void (async () => { try {
      const response = await adminFetch(api, `/api/admin/billing/models/${encodeURIComponent(model.name)}/pricing`, { signal: abort.signal });
      if (!response.ok) throw new Error(await readAdminError(response, tx("读取模型价格失败")));
      const body = await response.json(); if (abort.signal.aborted) return;
      setFingerprint(body.fingerprint); setCard({ ...body.card, rates: { ...emptyRates(), ...body.card.rates }, periods: (body.card.periods ?? []).map((period: Card["periods"][number]) => ({ ...period, rates: { ...emptyRates(), ...period.rates }, weekdays: period.weekdays?.length ? period.weekdays : [0, 1, 2, 3, 4, 5, 6], timezone: period.timezone || "UTC", start_time: period.start_time || "", end_time: period.end_time || "" })) });
    } catch (caught) { if (!abort.signal.aborted) setError(caught instanceof Error ? caught.message : tx("读取模型价格失败")); } })();
    return () => abort.abort();
  }, [api, model.name, reload]);
  useEffect(() => { if (check) dialog.current?.showModal(); else dialog.current?.close(); }, [check]);
  async function confirm() {
    if (!card) return; setBusy(true); setError(""); setMessage("");
    try { const result = await pricingRequest(api, "/api/admin/billing/model-pricing/check", { card, fingerprint }) as Check;
      if (!result.changed) { setMessage(tx("价格未改变")); return; }
      requestID.current = crypto.randomUUID(); setAcknowledged(Boolean(analysis && analysis.risks.length === 0)); setCheck({ ...result, analysis: analysis ?? undefined });
    } catch (caught) { setError(caught instanceof Error ? caught.message : tx("计费操作失败")); } finally { setBusy(false); }
  }
  async function apply() {
    if (!check || !acknowledged) return; setBusy(true); setError("");
    try { await pricingRequest(api, "/api/admin/billing/model-pricing/apply", { card: check.proposed, fingerprint: check.fingerprint, request_id: requestID.current, confirmed: true, risk_acknowledged: true, analysis_receipt: check.analysis?.receipt });
      setCheck(null); setMessage(tx("模型价格已更新。新请求立即使用新价，在途请求保持原价。")); setRevision(v => v + 1); setReload(v => v + 1); await onApplied();
    } catch (caught) { setError(caught instanceof Error ? caught.message : tx("计费操作失败")); } finally { setBusy(false); }
  }
  function change(next: Card) { setCard(next); setAnalysis(null); setCheck(null); setMessage(""); setError(""); }
  return <div className="model-pricing-workbench">
    <header className="billing-intro"><div><h2>{tx("定价与收益")}</h2><p>{model.name}</p></div><button type="button" className="secondary-button" disabled={busy} onClick={() => setReload(v => v + 1)}>{tx("重新读取价格")}</button></header>
    <p>{tx("价格对该模型的所有适用项目生效；分析筛选不会缩小调价范围。")}</p>
    {error && !check ? <p role="alert" className="billing-notice billing-error">{error}</p> : null}{message ? <p role="status" className="billing-notice billing-success">{message}</p> : null}
    {card ? <><fieldset disabled={busy} className="billing-form-fieldset"><ModelPricingFields card={card} onChange={change} embedding={model.modality === "embedding"} /><PricingImpactPanel api={api} card={card} fingerprint={fingerprint} onResult={setAnalysis} /><ModelRuleSimulator api={api} card={card} embedding={model.modality === "embedding"} /></fieldset><button type="button" className="primary-button" disabled={busy} onClick={() => void confirm()}>{tx("核对并保存")}</button></> : !error ? <p>{tx("正在读取模型价格…")}</p> : null}
    <dialog className="billing-confirm-dialog" ref={dialog} aria-labelledby="model-pricing-confirm-title" onCancel={e => { if (busy) e.preventDefault(); else setCheck(null); }}>
      <h2 id="model-pricing-confirm-title">{tx("确认调整模型价格")}</h2><strong>{model.name}</strong><p>{tx("价格对该模型的所有适用项目生效；分析筛选不会缩小调价范围。")}</p>
      {error ? <p role="alert" className="billing-notice billing-error">{error}</p> : null}
      <table><thead><tr><th>{tx("计价项目")}</th><th>{tx("当前价格")}</th><th>{tx("新价格")}</th></tr></thead><tbody>{rateLabels.map(([key, label]) => <tr key={key} data-changed={check?.current.rates[key] !== check?.proposed.rates[key]}><td>{tx(label)}</td><td>{check?.current.rates[key] ? decimalAmount(check.current.rates[key]) : tx("模型默认规则")}</td><td>{check?.proposed.rates[key] ? decimalAmount(check.proposed.rates[key]) : tx("模型默认规则")}</td></tr>)}</tbody></table><p>{tx("单价单位：USD / 百万 Token")}</p>
      <details><summary>{tx("核对分时规则")}</summary><div className="billing-confirm-periods">{[check?.current, check?.proposed].map((value, i) => <div key={i}><strong>{tx(i ? "新价格" : "当前价格")}</strong>{value?.periods?.length ? value.periods.map((period, n) => <p key={n}>{period.name} · {period.timezone} · {period.start_time}–{period.end_time}<br />{period.weekdays?.map(d => tx(["周日", "周一", "周二", "周三", "周四", "周五", "周六"][d])).join(" / ")}<br />{period.effective_from ? displayDate(period.effective_from, period.timezone) : ""} {period.effective_until ? displayDate(period.effective_until, period.timezone) : ""}<br />{rateLabels.map(([key, label]) => `${tx(label)}: ${period.rates[key] ? decimalAmount(period.rates[key]) : tx("模型默认规则")}`).join(" / ")}</p>) : <p>{tx("无分时规则")}</p>}</div>)}</div></details>
      {check?.analysis ? <div className="billing-notice"><p>{tx(check.analysis.basis === "historical" ? "历史记录成本" : "当前采购价情景")} · {check.analysis.timezone}</p><p>{tx("数据截止")}: {displayDate(check.analysis.cutoff, check.analysis.timezone)}</p><p>{tx("分析范围")}: {check.analysis.project_ids.length ? check.analysis.project_ids.join(" / ") : tx("全部项目")}</p>{check.analysis.risks.length ? <p>{tx("分析存在亏损、缺失依据或未验证规则，请核对后确认。")}</p> : null}</div> : <p className="billing-notice">{tx("未进行影响分析，尚未验证毛利与成本覆盖。")}</p>}<label className="pricing-risk-ack"><input type="checkbox" checked={acknowledged} disabled={busy} onChange={e => setAcknowledged(e.target.checked)} />{tx("我已了解本次调价影响及风险")}</label>
      <div className="billing-confirm-actions"><button type="button" className="secondary-button" disabled={busy} onClick={() => setCheck(null)}>{tx("取消")}</button><button type="button" className="primary-button" disabled={busy || !acknowledged} onClick={() => void apply()}>{tx("确认应用价格")}</button></div>
    </dialog>
    <details className="billing-disclosure"><summary>{tx("实际调价记录")}</summary><BillingPricingHistory api={api} data={data} revision={revision} /></details>
  </div>;
}
