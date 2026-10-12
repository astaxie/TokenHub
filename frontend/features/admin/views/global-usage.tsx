import { RefreshCw } from "lucide-react";
import { useEffect, useState, type ReactNode } from "react";
import { appRole } from "../core/navigation";
import type { AdminUser, ApiContext, AppData, UsageBreakdownRow } from "../core/types";
import type { UsageRange, UsageReport } from "../core/usage-report-types";
import { apiKeyAuditLabel, costCenterLabel, findProvider, projectName, providerResourceAuditLabel, usageMemberLabel } from "../domain/entities";
import { compactNumber, formatNumber } from "../domain/formatting";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { DataSection, SimpleTable } from "../shared/ui";
import { UsageReportChart, UsageReportMetrics } from "./usage-report-chart";

const ranges: UsageRange[] = ["today", "7d", "30d", "all"];

function rangeLabel(range: UsageRange) {
  return { today: tx("今日"), "7d": tx("7 天"), "30d": tx("30 天"), all: tx("全部") }[range];
}

type ReportState = { range: UsageRange; api: ApiContext; report?: UsageReport; error?: string };

export function GlobalUsageView({ api, data, user, renderReport }: {
  api: ApiContext;
  data: AppData;
  user: AdminUser;
  renderReport?: (data: AppData, label: string) => ReactNode;
}) {
  const [range, setRange] = useState<UsageRange>("today");
  const [result, setResult] = useState<ReportState>();
  const [refreshing, setRefreshing] = useState(true);
  const [revision, setRevision] = useState(0);
  const current = result?.range === range && result.api.baseURL === api.baseURL && result.api.adminToken === api.adminToken ? result : undefined;
  const report = current?.report;
  const error = current?.error;

  useEffect(() => {
    let active = true;
    let inFlight = false;
    const controller = new AbortController();
    async function load() {
      if (inFlight) return;
      inFlight = true;
      setRefreshing(true);
      try {
        const response = await adminFetch(api, `/api/admin/usage/report?range=${range}`, { signal: controller.signal });
        if (!response.ok) throw new Error(await readAdminError(response, tx("用量统计加载失败")));
        const payload = await response.json() as UsageReport;
        if (payload.range !== range) throw new Error(tx("用量统计加载失败"));
        if (active) setResult({ range, api, report: payload });
      } catch (reason) {
        if (active && !isAuthExpiredError(reason)) setResult({ range, api, error: reason instanceof Error ? reason.message : tx("用量统计加载失败") });
      } finally {
        inFlight = false;
        if (active) setRefreshing(false);
      }
    }
    void load();
    const interval = window.setInterval(() => void load(), 30_000);
    return () => { active = false; controller.abort(); window.clearInterval(interval); };
  }, [api, range, revision]);

  const scopedData = report ? { ...data, summary: report.summary, breakdown: report.breakdown, timeseries: report.timeseries } : undefined;
  return <section className="global-usage" aria-label={tx("全局用量")}>
    <div className="global-usage-toolbar">
      <div className="global-usage-ranges" role="group" aria-label={tx("用量时间范围")}>
        {ranges.map(value => <button key={value} type="button" aria-pressed={range === value} onClick={() => setRange(value)}>{rangeLabel(value)}</button>)}
      </div>
      <div className="global-usage-cost"><strong>{report ? usageMoney(report.summary.estimated_cost_usd) : "—"}</strong><span>{tx("估算成本")}</span></div>
      <button className="secondary-button" type="button" disabled={refreshing} onClick={() => setRevision(value => value + 1)}><RefreshCw size={15} />{tx("刷新")}</button>
    </div>
    <div className="global-usage-context">
      <span>{report ? usageWindowLabel(report) : rangeLabel(range)}</span>
      <span>{tx("每 30 秒刷新")}</span>
    </div>
    {error ? <div className="global-usage-error" role="alert"><p>{tx("用量统计加载失败")}</p><p>{error === tx("用量统计加载失败") ? null : error}</p><button className="secondary-button" type="button" onClick={() => setRevision(value => value + 1)}>{tx("重试")}</button></div> : null}
    {!error && !report ? <div className="empty" role="status">{tx("正在加载用量…")}</div> : null}
    {report && scopedData ? <>
      <div aria-busy={refreshing}>
        <UsageReportMetrics report={report} />
        <UsageReportChart report={report} />
        <UsageReportBreakdowns key={range} data={scopedData} user={user} report={report} />
      </div>
      {renderReport ? <details className="global-usage-details"><summary>{tx("部门与成员报告")}</summary>{renderReport(scopedData, rangeLabel(range))}</details> : null}
    </> : null}
  </section>;
}

function UsageReportBreakdowns({ data, user, report }: { data: AppData; user: AdminUser; report: UsageReport }) {
  const role = appRole(user.role);
  const breakdown = report.breakdown;
  return <>
    <div className="two-column">
      <UsageTable title={tx("模型用量")} label={tx("模型")} rows={breakdown.models ?? []} paginationKey="usage-models" name={row => row.id} />
      <UsageTable title={tx("项目归因")} label={tx("项目")} rows={breakdown.projects ?? []} paginationKey="usage-projects" name={row => projectName(data, row.id)} />
    </div>
    <details className="global-usage-details"><summary>{tx("更多用量明细")}</summary><div className="two-column">
      {role === "team_leader" ? <UsageTable title={tx("成员用量")} label={tx("成员")} rows={breakdown.members ?? []} paginationKey="usage-members" name={row => usageMemberLabel(data, row.id)} /> : null}
      <UsageTable title={tx("API Key 用量")} label="API Key" rows={breakdown.api_keys ?? []} paginationKey="usage-api-keys" name={row => apiKeyAuditLabel(data, row.id)} />
      {role === "admin" ? <UsageTable title={tx("供应商用量")} label={tx("供应商")} rows={breakdown.providers ?? []} paginationKey="usage-providers" name={row => findProvider(data, row.id)?.name || row.id} /> : null}
      {role === "admin" ? <UsageTable title={tx("资源账号用量")} label={tx("资源账号")} rows={breakdown.provider_resources ?? []} paginationKey="usage-resources" name={row => providerResourceAuditLabel(data, row.id)} /> : null}
      {role !== "user" ? <UsageTable title={tx("成本中心用量")} label={tx("成本中心")} rows={breakdown.cost_centers ?? []} paginationKey="usage-cost-centers" name={row => costCenterLabel(data, row.id)} /> : null}
      <DataSection title="Token 类型"><SimpleTable columns={["类型", "Token"]} paginationKey="usage-token-types" rows={[
        [tx("输入（不含缓存）"), compactNumber(Math.max(0, report.summary.input_tokens - (report.summary.cached_input_tokens ?? 0) - (report.summary.cache_write_input_tokens ?? 0)))],
        [tx("缓存读"), compactNumber(report.summary.cached_input_tokens ?? 0)],
        [tx("缓存写"), compactNumber(report.summary.cache_write_input_tokens ?? 0)],
        [tx("输出"), compactNumber(report.summary.output_tokens)],
      ]} /></DataSection>
    </div></details>
  </>;
}

function UsageTable({ title, label, rows, name, paginationKey }: {
  title: string; label: string; rows: UsageBreakdownRow[]; name: (row: UsageBreakdownRow) => string; paginationKey: string;
}) {
  return <DataSection title={title}><SimpleTable columns={[label, "用量记录", "Token", "缓存读", "成本"]} paginationKey={paginationKey} rows={rows.map(row => [
    name(row), formatNumber(row.request_count), compactNumber(row.total_tokens), compactNumber(row.cached_input_tokens ?? 0), usageMoney(row.estimated_cost_usd),
  ])} /></DataSection>;
}

function usageMoney(value: number) {
  return new Intl.NumberFormat(languageLocale(), { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: 6 }).format(value);
}

function usageWindowLabel(report: UsageReport) {
  if (!report.window_start) return formatTranslationTemplate(tx("{range} · {timezone}"), { range: rangeLabel(report.range), timezone: report.timezone });
  const formatter = new Intl.DateTimeFormat(languageLocale(), { dateStyle: "medium", timeStyle: "short", timeZone: report.timezone });
  return formatTranslationTemplate(tx("{start} – {end} · {timezone}"), {
    start: formatter.format(new Date(report.window_start)), end: formatter.format(new Date(report.window_end)), timezone: report.timezone,
  });
}
