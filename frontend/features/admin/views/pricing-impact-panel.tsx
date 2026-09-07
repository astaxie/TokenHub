import { useEffect, useRef, useState } from "react";
import type { ApiContext } from "../core/types";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { Field, decimalAmount, displayDate, type Card } from "./billing-pricing-fields";
import { pricingRequest } from "./pricing-analysis-api";

export type ImpactAmounts = { current_usd: string; candidate_usd: string; cost_usd: string; charge_delta_usd: string; current_margin_usd: string; candidate_margin_usd: string; current_margin_percent: string | null; candidate_margin_percent: string | null };
export type PricingImpact = { basis: string; from: string; to: string; cutoff: string; timezone: string; project_ids: string[]; requests: number; computable: number; recorded_charges_usd: string; unknown_charges: number; request_coverage_percent: string | null; known_charge_coverage_percent: string | null; overall: ImpactAmounts | null; computable_amounts: ImpactAmounts | null; excluded: Record<string, number>; untested_periods: string[]; loss_requests: number; risks: string[]; receipt: string; groups: { id: string; names: string[]; requests: number; loss_requests: number; amounts: ImpactAmounts }[] };
function period(days: number, timezone = Intl.DateTimeFormat().resolvedOptions().timeZone) {
  const parts = new Intl.DateTimeFormat("en-CA", { timeZone: timezone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(new Date());
  const value = (type: string) => Number(parts.find(part => part.type === type)?.value);
  const end = new Date(Date.UTC(value("year"), value("month") - 1, value("day"))); const start = new Date(end); start.setUTCDate(start.getUTCDate() - days);
  return { from: start.toISOString().slice(0, 10), to: end.toISOString().slice(0, 10) };
}

export function impactMoney(value: string) { return value.startsWith("-") ? `−${decimalAmount(value.slice(1))} USD` : `${decimalAmount(value)} USD`; }
const percent = (value: string | null) => value === null ? "—" : `${decimalAmount(value)}%`;
export function ImpactAmountsTable({ amounts }: { amounts: ImpactAmounts }) {
  return <div className="table-wrap"><table><thead><tr><th>{tx("计费口径")}</th><th>{tx("当前价重算")}</th><th>{tx("候选价重算")}</th></tr></thead><tbody>
    <tr><td>{tx("计费额")}</td><td>{impactMoney(amounts.current_usd)}</td><td>{impactMoney(amounts.candidate_usd)}</td></tr>
    <tr><td>{tx("上游成本估算")}</td><td>{impactMoney(amounts.cost_usd)}</td><td>{impactMoney(amounts.cost_usd)}</td></tr>
    <tr><td>{tx("预计毛利")}</td><td>{impactMoney(amounts.current_margin_usd)}</td><td>{impactMoney(amounts.candidate_margin_usd)}</td></tr>
    <tr><td>{tx("预计毛利率")}</td><td>{percent(amounts.current_margin_percent)}</td><td>{percent(amounts.candidate_margin_percent)}</td></tr>
    <tr><td>{tx("本次调价计费增量")}</td><td>—</td><td>{impactMoney(amounts.charge_delta_usd)}</td></tr>
  </tbody></table></div>;
}
export function PricingImpactPanel({ api, card, fingerprint, onResult }: { api: ApiContext; card: Card; fingerprint: string; onResult: (report: PricingImpact | null) => void }) {
  const validBasis = useRef("");
  const queryKey = JSON.stringify({ card, fingerprint });
  const [open, setOpen] = useState(false); const [range, setRange] = useState(() => ({ ...period(30), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone }));
  const [projects, setProjects] = useState<{ id: string; name: string }[]>([]); const [selected, setSelected] = useState<string[]>([]); const [projectError, setProjectError] = useState("");
  const [basis, setBasis] = useState("historical"); const [report, setReport] = useState<PricingImpact | null>(null); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [refresh, setRefresh] = useState(0);
  useEffect(() => { const abort = new AbortController(); void (async () => { try { const response = await adminFetch(api, "/api/admin/projects", { signal: abort.signal }); if (!response.ok) throw new Error(await readAdminError(response, tx("项目加载失败"))); const body = await response.json(); if (!abort.signal.aborted) setProjects(body.data); } catch (caught) { if (!abort.signal.aborted) setProjectError(caught instanceof Error ? caught.message : tx("项目加载失败")); } })(); return () => abort.abort(); }, [api]);
  useEffect(() => {
    const abort = new AbortController();
    if (!open) { if (validBasis.current !== queryKey) { onResult(null); setReport(null); } setBusy(false); return () => abort.abort(); }
    onResult(null); setReport(null); setError("");
    setBusy(true);
    const timer = setTimeout(() => { void (async () => { try {
      const value = await pricingRequest(api, "/api/admin/billing/model-pricing/impact", { card, fingerprint, basis, ...range, project_ids: selected }, abort.signal) as PricingImpact;
      if (!abort.signal.aborted) { validBasis.current = queryKey; setReport(value); onResult(value); }
    } catch (caught) { if (!abort.signal.aborted) setError(caught instanceof Error ? caught.message : tx("读取分析失败")); } finally { if (!abort.signal.aborted) setBusy(false); } })(); }, 500);
    return () => { clearTimeout(timer); abort.abort(); };
  }, [api, card, fingerprint, basis, range, selected, open, refresh, onResult, queryKey]);
  function clear() { onResult(null); setReport(null); }
  function selectPeriod(days: number) { try { const next = period(days, range.timezone); clear(); setRange({ ...next, timezone: range.timezone }); } catch { setError(tx("请填写有效的分析时区。")); } }
  const number = (value: number) => new Intl.NumberFormat(languageLocale()).format(value);
  return <details className="billing-disclosure pricing-impact-panel" onToggle={e => { if (e.target === e.currentTarget) setOpen(e.currentTarget.open); }}><summary>{tx("可选：历史收益分析")}</summary>
    <p>{tx("固定历史时间、用量、渠道与重试，仅比较价格规则；不是未来收益预测。")}</p>
    <div className="pricing-impact-filters"><Field label="成本口径"><select value={basis} onChange={e => { clear(); setBasis(e.target.value); }}><option value="historical">{tx("历史记录成本")}</option><option value="current_procurement">{tx("当前采购价情景")}</option></select></Field>
      <Field label="开始日期"><input type="date" value={range.from} onChange={e => { clear(); setRange({ ...range, from: e.target.value }); }} /></Field><Field label="截止日期（不含）"><input type="date" value={range.to} onChange={e => { clear(); setRange({ ...range, to: e.target.value }); }} /></Field><Field label="分析时区"><input value={range.timezone} onChange={e => { clear(); setRange({ ...range, timezone: e.target.value }); }} /></Field>
      <Field label="分析项目（不选为全部）"><select multiple value={selected} onChange={e => { clear(); setSelected(Array.from(e.target.selectedOptions, o => o.value)); }}>{projects.map(project => <option key={project.id} value={project.id}>{project.name}</option>)}</select></Field></div>
    {projectError ? <p role="alert">{projectError}</p> : null}
    <div className="billing-section-toolbar"><button type="button" className="secondary-button" onClick={() => selectPeriod(7)}>{tx("最近 7 个完整日")}</button><button type="button" className="secondary-button" onClick={() => selectPeriod(30)}>{tx("最近 30 个完整日")}</button><button type="button" className="secondary-button" disabled={busy} onClick={() => { clear(); setRefresh(v => v + 1); }}>{tx("重新分析")}</button></div>
    <p className="billing-hint">{tx("最多 93 天、10000 条账单明细；超限请缩小范围，不会抽样或截断。")}</p>
    {busy ? <p role="status">{tx("正在分析历史请求…")}</p> : null}{error ? <p role="alert" className="billing-notice billing-error">{error}</p> : null}
    {report ? <section aria-label={tx("收益分析结果")}><p>{tx("数据截止")}: {displayDate(report.cutoff, report.timezone)} · {report.timezone}</p><p>{tx("历史已记录计费额（已知部分）")}: {impactMoney(report.recorded_charges_usd)}</p>
      <div className="billing-statement-totals"><div><small>{tx("可计算请求 / 总请求")}</small><strong>{number(report.computable)} / {number(report.requests)}</strong></div><div><small>{tx("请求覆盖率")}</small><strong>{percent(report.request_coverage_percent)}</strong></div><div><small>{tx("已知计费额覆盖率")}</small><strong>{percent(report.known_charge_coverage_percent)}</strong></div></div>
      {report.unknown_charges ? <p>{tx("历史收费金额未知的记录")}: {number(report.unknown_charges)}</p> : null}
      {report.overall ? <ImpactAmountsTable amounts={report.overall} /> : <p role="status" className="billing-notice">{tx(report.requests ? "整体毛利无法确定；部分样本不能代表全部业务。" : "此范围没有历史请求，可直接调价或使用高级规则验证。")}</p>}
      {!report.overall && report.computable_amounts ? <details open><summary>{tx("仅可计算样本结果，不作全量外推")}</summary><ImpactAmountsTable amounts={report.computable_amounts} /></details> : null}
      {Object.entries(report.excluded).map(([reason, count]) => <p key={reason}>{tx(({ usage_incomplete: "用量或交付依据不足", cost_incomplete: "成本依据不足", calculation_unavailable: "无法计算" })[reason] ?? reason)}: {number(count)}</p>)}
      {report.loss_requests > 0 ? <p className="billing-notice billing-error">{tx("样本存在亏损请求或路径")}: {number(report.loss_requests)}</p> : null}
      {report.untested_periods.length ? <p className="billing-notice">{tx("以下新规则未被历史样本验证")}: {report.untested_periods.join(" / ")}</p> : null}
      {report.groups.length ? <details><summary>{tx("渠道组合与亏损风险")}</summary><p>{tx("跨渠道重试按组合归集；收入只计一次，成本包含全部尝试。")}</p><div className="table-wrap"><table><thead><tr><th>{tx("渠道组合")}</th><th>{tx("请求")}</th><th>{tx("候选价预计毛利")}</th><th>{tx("亏损请求")}</th></tr></thead><tbody>{report.groups.map(group => <tr key={group.id}><td>{group.names.join(" + ") || tx("未归属")}</td><td>{number(group.requests)}</td><td>{impactMoney(group.amounts.candidate_margin_usd)}</td><td>{number(group.loss_requests)}</td></tr>)}</tbody></table></div></details> : null}
      <p className="billing-hint">{tx("仅为模型服务计费额减上游调用成本的估算，不含运营成本，也不代表实收或供应商已核实。")}</p>
    </section> : null}
  </details>;
}
