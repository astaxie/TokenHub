import { useEffect, useRef, useState } from "react";
import { Download, Search } from "lucide-react";
import type { ApiContext, AppData } from "../core/types";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection } from "../shared/ui";
import { Field, decimalAmount, displayDate } from "./billing-pricing-fields";

type StatementRow = { id: string; request_id: string; occurred_at: string; provider_id?: string; provider_name?: string; resource_id?: string; resource_name?: string; model: string; project_id?: string; project_name?: string; amount_usd?: string; status: string; reason?: string; source: string };
type Statement = { kind: string; from: string; to: string; known_amount_usd: string; records: number; pending: number; legacy: number; complete: boolean; offset: number; limit: number; groups: { id: string; name: string; records: number; pending: number; known_amount_usd: string }[]; items: StatementRow[] };
function monthRange() {
  const now = new Date();
  const format = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
  return { from: format(new Date(now.getFullYear(), now.getMonth(), 1)), to: format(new Date(now.getFullYear(), now.getMonth() + 1, 0)) };
}
export function BillingPlatformStatements({ api, data }: { api: ApiContext; data: AppData }) {
  const [filters, setFilters] = useState(() => ({ ...monthRange(), kind: "provider", group_by: "provider", provider_id: "", project_id: "", model: "" }));
  const [result, setResult] = useState<Statement | null>(null);
  const [activeQuery, setActiveQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  function queryString(offset = 0) {
    const from = new Date(`${filters.from}T00:00:00`), until = new Date(`${filters.to}T00:00:00`);
    until.setDate(until.getDate() + 1);
    if (Number.isNaN(from.getTime()) || Number.isNaN(until.getTime()) || from >= until || until.getTime() - from.getTime() > 366 * 86400000) throw new Error(tx("请选择有效日期范围，最长 366 天。"));
    return new URLSearchParams({ ...filters, from: from.toISOString(), to: until.toISOString(), offset: String(offset), limit: "100" }).toString();
  }
  async function load(offset = 0) {
    const run = ++generation.current;
    setBusy(true); setError("");
    try {
      const query = queryString(offset);
      const response = await adminFetch(api, `/api/admin/billing/statements?${query}`);
      if (!response.ok) throw new Error(await readAdminError(response, "读取平台账单失败"));
      const body = await response.json();
      if (run === generation.current) { setResult(body); setActiveQuery(query); }
    } catch (caught) { if (run === generation.current) setError(caught instanceof Error ? caught.message : tx("读取平台账单失败")); }
    finally { if (run === generation.current) setBusy(false); }
  }
  const initialLoad = useRef(load);
  useEffect(() => { const state = generation; void initialLoad.current(); return () => { state.current++; }; }, []);
  function change(patch: Partial<typeof filters>) { generation.current++; setFilters({ ...filters, ...patch }); setResult(null); setActiveQuery(""); setError(""); setBusy(false); }
  async function download() {
    setExporting(true); setError("");
    try {
      const response = await adminFetch(api, `/api/admin/billing/statements?${activeQuery}&format=csv`);
      if (!response.ok) throw new Error(await readAdminError(response, "导出账单失败"));
      const url = URL.createObjectURL(await response.blob());
      const link = document.createElement("a"); link.href = url; link.download = `tokenhub-${result?.kind ?? "provider"}-statement.csv`; link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (caught) { setError(caught instanceof Error ? caught.message : tx("导出账单失败")); }
    finally { setExporting(false); }
  }
  const number = (value: number) => new Intl.NumberFormat(languageLocale()).format(value);
  return <div className="billing-panel">
    <DataSection title="平台账单"><p className="billing-hint">{tx("无需连接器即可查看平台记录的下游收费和上游成本估算。供应商实际账单仅用于可选对账。")}</p>
      <form className="billing-statement-filters" onSubmit={(event) => { event.preventDefault(); void load(); }}>
        <Field label="账单口径"><select value={filters.kind} onChange={(event) => change({ kind: event.target.value, group_by: event.target.value === "provider" ? "provider" : "project", provider_id: "" })}><option value="provider">{tx("上游成本估算")}</option><option value="tenant">{tx("下游收费")}</option></select></Field>
        <Field label="开始日期"><input type="date" value={filters.from} onChange={(event) => change({ from: event.target.value })} /></Field><Field label="结束日期"><input type="date" value={filters.to} onChange={(event) => change({ to: event.target.value })} /></Field>
        <Field label="汇总维度"><select value={filters.group_by} onChange={(event) => change({ group_by: event.target.value })}>{filters.kind === "provider" ? <><option value="provider">Provider</option><option value="resource">{tx("资源账号")}</option></> : null}<option value="model">{tx("模型")}</option><option value="project">{tx("项目")}</option></select></Field>
        {filters.kind === "provider" ? <Field label="Provider"><select value={filters.provider_id} onChange={(event) => change({ provider_id: event.target.value })}><option value="">{tx("全部")}</option>{data.providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select></Field> : null}
        <Field label="项目"><select value={filters.project_id} onChange={(event) => change({ project_id: event.target.value })}><option value="">{tx("全部")}</option>{data.projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}</select></Field>
        <Field label="模型 ID"><input value={filters.model} onChange={(event) => change({ model: event.target.value })} /></Field>
        <div className="billing-statement-actions"><button type="submit" className="primary-button" disabled={busy}><Search size={15} />{tx(busy ? "查询中…" : "查询账单")}</button><button type="button" className="secondary-button" disabled={!result || busy || exporting} onClick={() => void download()}><Download size={15} />{tx("导出 CSV")}</button></div>
      </form>
      {error ? <p role="alert" className="billing-notice billing-error">{error}</p> : null}
      {result ? <><div className="billing-statement-totals"><div><small>{tx("已知费用小计")}</small><strong>{decimalAmount(result.known_amount_usd)} USD</strong></div><div><small>{tx("记录数")}</small><strong>{number(result.records)}</strong></div><div><small>{tx("待核实记录")}</small><strong>{number(result.pending)}</strong></div></div>
        {!result.complete ? <p role="status" className="billing-notice">{tx("存在待核实或历史依据不足的记录，小计仅包含已知金额，不代表完整成本。")}</p> : null}
        <p className="billing-hint">{result.kind === "provider" ? tx("按上游调用尝试归集，包含重试；这些金额是平台估算，并非供应商确认金额。") : tx("按请求准入时间归集下游收费，不因平台重试重复计费。")}</p>
      </> : !busy ? <p className="billing-hint">{tx("选择范围后点击查询账单。")}</p> : null}
    </DataSection>
    {result ? <><DataSection title="费用汇总"><div className="table-wrap"><table><thead><tr><th>{tx("归属")}</th><th>{tx("记录数")}</th><th>{tx("待核实记录")}</th><th>{tx("已知费用小计")}</th></tr></thead><tbody>{result.groups.map((group) => <tr key={group.id}><td>{group.name || tx("未归属")}</td><td>{number(group.records)}</td><td>{number(group.pending)}</td><td>{decimalAmount(group.known_amount_usd)} USD</td></tr>)}</tbody></table>{!result.groups.length ? <p className="empty">{tx("此范围暂无账单记录")}</p> : null}</div></DataSection>
      <DataSection title="账单明细"><div className="table-wrap"><table><thead><tr><th>{tx("发生时间")}</th><th>{tx("模型")}</th><th>{tx("归属")}</th><th>{tx("金额")}</th><th>{tx("状态")}</th></tr></thead><tbody>{result.items.map((row) => <tr key={row.id}><td>{displayDate(row.occurred_at)}</td><td>{row.model || "—"}<small className="billing-row-id">{row.request_id}</small></td><td>{result.kind === "provider" ? row.provider_name || data.providers.find((provider) => provider.id === row.provider_id)?.name || row.provider_id || tx("未归属") : row.project_name || row.project_id || tx("未归属")}<small className="billing-row-id">{row.resource_name || row.resource_id}</small></td><td>{row.amount_usd !== undefined && row.amount_usd !== "" ? `${decimalAmount(row.amount_usd)} USD` : tx("待核实")}</td><td>{tx(row.status === "pending" ? "待核实" : row.status === "charged" ? "已计费" : "估算")}{row.source === "legacy_usage" ? <small className="billing-row-id">{tx("历史依据不足")}</small> : null}</td></tr>)}</tbody></table></div><div className="billing-section-toolbar"><button type="button" className="secondary-button" disabled={busy || result.offset === 0} onClick={() => void load(Math.max(0, result.offset - result.limit))}>{tx("上一页")}</button><span>{number(result.records ? result.offset + 1 : 0)}–{number(Math.min(result.offset + result.limit, result.records))} / {number(result.records)}</span><button type="button" className="secondary-button" disabled={busy || result.offset + result.limit >= result.records} onClick={() => void load(result.offset + result.limit)}>{tx("下一页")}</button></div></DataSection>
    </> : null}
  </div>;
}
