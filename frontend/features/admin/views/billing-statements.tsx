"use client";

import { createPortal } from "react-dom";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { type ApiContext } from "../core/types";
import { formatStatementAmount, statementCSV, statementMonth, type StatementQuery, type StatementResult, type StatementSide } from "../domain/billing-statements";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection, SimpleTable } from "../shared/ui";

type StatementProps = { api: ApiContext; side?: StatementSide; model?: string; providerID?: string };

export function StatementLauncher(props: StatementProps) {
  const [open, setOpen] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  useEffect(() => { if (open) dialog.current?.showModal(); }, [open]);
  return <>
    <button className="text-button" type="button" onClick={() => setOpen(!open)}>{props.side === "provider" ? tx("上游费用对账单") : tx("下游费用对账单")}</button>
    {open ? createPortal(<dialog ref={dialog} className="statement-drawer" aria-label={tx("费用对账单")} onClose={() => setOpen(false)}><button className="button secondary" type="button" onClick={() => setOpen(false)}>{tx("关闭")}</button><BillingStatements {...props} /></dialog>, document.body) : null}
  </>;
}

export function BillingStatements({ api, side = "tenant", model = "", providerID = "" }: StatementProps) {
  const [query, setQuery] = useState<StatementQuery>(() => ({ side, ...statementMonth(), timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC", customer: "", project_ids: [], provider_id: providerID, resource_id: "", model }));
  const [projects, setProjects] = useState<{ id: string; name: string }[]>([]);
  const [result, setResult] = useState<StatementResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [projectError, setProjectError] = useState("");
  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const response = await adminFetch(api, "/api/admin/projects");
        if (!response.ok) throw new Error(await readAdminError(response, tx("项目加载失败")));
        const payload = await response.json() as { data: { id: string; name: string }[] };
        if (active) setProjects(payload.data);
      } catch (caught) { if (active) setProjectError(caught instanceof Error ? caught.message : tx("项目加载失败")); }
    })();
    return () => { active = false; };
  }, [api]);
  function change(patch: Partial<StatementQuery>) { setQuery(current => ({ ...current, ...patch })); setResult(null); setError(""); }
  async function preview(event: FormEvent) {
    event.preventDefault(); event.stopPropagation(); setBusy(true); setError(""); setResult(null);
    try {
      const response = await adminFetch(api, "/api/admin/billing/statements", { method: "POST", body: JSON.stringify(query) });
      if (!response.ok) throw new Error(await readAdminError(response, tx("对账单生成失败")));
      setResult(await response.json() as StatementResult);
    } catch (caught) { setError(caught instanceof Error ? caught.message : tx("对账单生成失败")); }
    finally { setBusy(false); }
  }
  function download() {
    if (!result) return;
    const url = URL.createObjectURL(new Blob([statementCSV(result)], { type: "text/csv;charset=utf-8" }));
    const anchor = document.createElement("a"); anchor.href = url; anchor.download = `tokenhub-${result.query.side}-${result.query.from}-${result.query.to}.csv`; anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
  const money = (amount: string | null, currency: string) => amount === null ? tx("未知") : formatStatementAmount(amount, currency, languageLocale());
  const number = (value: number) => new Intl.NumberFormat(languageLocale()).format(value);
  const date = (value: string, timezone: string) => {
    try { return new Intl.DateTimeFormat(languageLocale(), { dateStyle: "short", timeStyle: "short", timeZone: timezone }).format(new Date(value)); }
    catch { return value; }
  };
  return <DataSection title="费用对账单">
    <p>{tx("仅供费用核对，不代表确认或收付款。导出与当前预览使用同一份数据。")}</p>
    <form onSubmit={event => void preview(event)}>
      <fieldset disabled={busy} className="statement-fields">
        <label>{tx("对账单类型")}<select value={query.side} onChange={event => change({ side: event.target.value as StatementSide, customer: "", provider_id: "", resource_id: "", model: "", project_ids: [] })}>
          <option value="tenant">{tx("下游费用对账单")}</option><option value="provider">{tx("上游费用对账单")}</option><option value="margin">{tx("预计毛利汇总")}</option>
        </select></label>
        <label>{tx("开始日期")}<input required type="date" value={query.from} onChange={event => change({ from: event.target.value })} /></label>
        <label>{tx("结束日期（不含）")}<input required type="date" value={query.to} onChange={event => change({ to: event.target.value })} /></label>
        <label>{tx("账期时区")}<input required value={query.timezone} onChange={event => change({ timezone: event.target.value })} /></label>
        {query.side !== "provider" ? <>
          <label>{tx("客户名称")}<input required={query.side === "tenant"} maxLength={200} value={query.customer} onChange={event => change({ customer: event.target.value })} /></label>
          <label>{tx("客户项目（可多选）")}<select multiple required={query.side === "tenant"} value={query.project_ids} onChange={event => change({ project_ids: Array.from(event.target.selectedOptions, option => option.value) })}>
            {projects.map(project => <option key={project.id} value={project.id}>{project.name} ({project.id})</option>)}
          </select></label>
        </> : <>
          <label>{tx("Provider ID（留空为全部）")}<input value={query.provider_id} onChange={event => change({ provider_id: event.target.value })} /></label>
          <label>{tx("资源账号 ID（留空为全部）")}<input value={query.resource_id} onChange={event => change({ resource_id: event.target.value })} /></label>
        </>}
        <label>{query.side === "provider" ? tx("上游模型（留空为全部）") : tx("对外模型（留空为全部）")}<input value={query.model} onChange={event => change({ model: event.target.value })} /></label>
        <button className="button" type="submit">{busy ? tx("生成中") : tx("预览对账单")}</button>
      </fieldset>
    </form>
    {projectError && query.side !== "provider" ? <p role="alert">{projectError}</p> : null}
    {error ? <p role="alert">{error}</p> : null}
    {result ? <>
      <div className="statement-summary">
        <strong>{result.query.customer}</strong>
        <span>{date(result.from, result.query.timezone)} – {date(result.to, result.query.timezone)} ({result.query.timezone})</span>
        <span>{tx("生成时间")}: {date(result.generated_at, result.query.timezone)}</span>
        <span>{result.time_basis === "request_admission" ? tx("按请求开始时间归集；历史记录按完成时间归集") : tx("按上游尝试开始时间归集；供应商记录保留原始期间")}</span>
        <button className="button secondary" type="button" onClick={download}>{tx("导出当前预览 CSV")}</button>
      </div>
      {result.unknown_count || result.incomplete_count ? <p role="status">{tx("成本或历史依据不完整，不能据此确认利润。")}{" "}{tx("未知金额")}: {number(result.unknown_count)} · {tx("依据不足")}: {number(result.incomplete_count)}</p> : null}
      {query.side === "provider" ? <p>{tx("供应商金额与本地估算分列，不相加；跨期间记录保留全额，不自动分摊。")}</p> : null}
      <div className="statement-summary">{Object.entries(result.totals).map(([key, value]) => {
        const [source, currency] = key.split(":");
        return <span key={key}>{statementSource(source)} · {currency === "USD_equivalent" ? tx("折算 USD") : currency}: {money(value, currency === "USD_equivalent" ? "USD" : currency)}</span>;
      })}</div>
      {query.side === "margin" ? <p><strong>{tx("预计毛利")}: {money(result.estimated_margin_usd, "USD")}</strong>{" "}{tx("仅按请求对应的本地成本估算，不代表净利润或现金结余。")}</p> : null}
      <SimpleTable columns={[tx("金额来源"), tx("项目 / 模型"), tx("时间 / 时区"), tx("用量"), tx("金额"), tx("依据")]} paginationKey="billing-statements" rows={result.rows.map(row => [
        statementSource(row.source), <span key="subject">{row.project_id || row.provider_id}<small>{row.model}</small><small>{row.api_key_id || row.resource_id}</small></span>,
        <span key="time">{date(row.at, row.timezone || "UTC")}<small>{row.timezone}</small>{row.end_at ? <small>{date(row.end_at, row.timezone || "UTC")}</small> : null}</span>,
        row.source === "provider_billed" ? `${number(row.usage_quantity ?? 0)} ${row.usage_unit ?? ""}` : Object.entries(row.units).filter(([, value]) => value > 0).map(([kind, value]) => `${statementUnit(kind)}: ${number(value)}`).join(" · "),
        money(row.amount, row.currency), <details key="evidence"><summary>{statementStatus(row.status)}</summary><p>{statementReason(row.reason)}</p>{row.external_id ? <p>{row.external_id}</p> : null}{row.price ? <p>{row.price.version || row.price.source}</p> : null}{row.lines?.map(line => <p key={line.kind}>{statementUnit(line.kind)}: {number(line.units)} × {money(line.rate, row.currency)} / 1M = {money(line.amount, row.currency)}</p>)}</details>,
      ])} />
    </> : null}
  </DataSection>;
}

function statementSource(value: string) { return ({ tenant: tx("租户费用"), provider_estimate: tx("本地估算成本"), provider_billed: tx("供应商账单金额") })[value] ?? value; }
function statementUnit(value: string) { return ({ input: tx("输入"), output: tx("输出"), cache_read: tx("缓存读"), cache_write: tx("缓存写"), cache_write_5m: tx("5 分钟缓存写"), cache_write_1h: tx("1 小时缓存写") })[value] ?? value; }
function statementStatus(value: string) { return ({ estimated: tx("估算"), provider_billed: tx("供应商账单金额"), pending: tx("待核实"), legacy_incomplete: tx("历史依据不足"), period_overlap: tx("跨期间金额") })[value] ?? value; }
function statementReason(value?: string) {
  if (value === "recorded_tenant_charge") return tx("采用已记录费用和历史单价");
  if (value === "supplier_reported") return tx("供应商原始账单金额");
  if (value === "full_supplier_amount_not_prorated") return tx("保留供应商全额，未按所选期间分摊");
  if (value === "usage_presence_and_provider_time_basis_unverified") return tx("按历史价格估算，用量及供应商计时依据待核实");
  return tx("证据不完整，不能将未知金额视为零，也不能使用当前价格补算。");
}
