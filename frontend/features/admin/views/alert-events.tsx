import { useEffect, useState } from "react";
import { type AlertEvent, type ApiContext, type AppData } from "../core/types";
import { enumValueLabel } from "../domain/labels";
import { formatNumber } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { alertEventConfig } from "../resources/governance-config";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { PaginationControls, type PaginationState } from "../shared/pagination";
import { DataSection } from "../shared/ui";
import { EntityTable } from "./settings-table";

type AlertPage = { data: AlertEvent[]; page: number; page_size: number; total: number };
const emptyPage: AlertPage = { data: [], page: 1, page_size: 50, total: 0 };

export function AlertEventsView({ api, data }: { api: ApiContext; data: AppData }) {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(50);
  const [severity, setSeverity] = useState("");
  const [project, setProject] = useState("");
  const [from, setFrom] = useState("");
  const [before, setBefore] = useState("");
  const [result, setResult] = useState<AlertPage>(emptyPage);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [revision, setRevision] = useState(0);
  const config = alertEventConfig();

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setResult(emptyPage);
    const query = new URLSearchParams({ page: String(page), page_size: String(pageSize) });
    if (severity) query.set("severity", severity);
    if (project) query.set("project_id", project);
    if (from) query.set("from", new Date(`${from}T00:00:00Z`).toISOString());
    if (before) query.set("before", new Date(`${before}T00:00:00Z`).toISOString());
    void adminFetch(api, `/api/admin/alerts?${query}`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("告警加载失败")));
        return await response.json() as AlertPage;
      })
      .then((value) => { if (!controller.signal.aborted) setResult(value); })
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setError(reason instanceof Error ? reason.message : tx("告警加载失败"));
      })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [api, page, pageSize, severity, project, from, before, revision]);

  function filter(set: (value: string) => void, value: string) { set(value); setPage(1); }
  const pagination: PaginationState = {
    page, pageSize, pageCount: Math.max(1, Math.ceil(result.total / pageSize)),
    startIndex: (page - 1) * pageSize, endIndex: Math.min(page * pageSize, result.total),
    setPage, setPageSize: (value) => { setPageSize(value); setPage(1); },
  };
  return (
    <DataSection title="告警事件">
      <div className="table-toolbar alert-history-filters">
        <label>{tx("级别")}<select value={severity} onChange={(event) => filter(setSeverity, event.target.value)} aria-label={tx("级别")}>
          <option value="">{tx("全部")}</option>
          {["info", "warning", "critical", "error"].map((value) => <option key={value} value={value}>{enumValueLabel(value)}</option>)}
        </select></label>
        <label>{tx("项目 ID")}<input value={project} onChange={(event) => filter(setProject, event.target.value)} aria-label={tx("项目 ID")} /></label>
        <label>{tx("开始日期（UTC）")}<input type="date" value={from} onChange={(event) => filter(setFrom, event.target.value)} /></label>
        <label>{tx("结束日期（UTC，不含）")}<input type="date" value={before} onChange={(event) => filter(setBefore, event.target.value)} /></label>
        <span>{tx("记录")} {formatNumber(result.total)}</span>
        <button type="button" className="secondary-button" onClick={() => setRevision((value) => value + 1)}>{tx("刷新")}</button>
      </div>
      {error ? <div role="alert" className="inline-notice error">{error}</div> : null}
      <EntityTable config={config} data={data} items={result.data} loading={loading} query="" onEdit={() => undefined} onDelete={() => undefined} onAction={(action, item) => {
        if (!action.run) return;
        void action.run(api, item, data).then(() => setRevision((value) => value + 1)).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : tx("告警发送失败")));
      }} />
      <PaginationControls pagination={pagination} totalItems={result.total} />
    </DataSection>
  );
}
