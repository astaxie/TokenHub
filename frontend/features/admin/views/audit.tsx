import { Activity, AlertCircle, Check, Copy, Gauge, Search, ShieldCheck } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { canViewAdminAudit } from "../core/navigation";
import { type AdminUser, type ApiContext, type AppData, type RequestDetail, type RequestLogPage, type RequestLogPagination, type RequestLogSummary, type RequestPayloadLog } from "../core/types";
import { auditRequestPagePath, auditRequestTimeRangeParameters, type AuditRequestStatus, type AuditRequestTimeRange } from "../domain/audit-request-page";
import { copyText } from "../domain/clipboard";
import { apiKeyAuditLabel, projectName, providerAttemptLabel, providerAuditLabel, providerResourceAuditLabel } from "../domain/entities";
import { compactNumber, formatMoney, formatNumber, formatTime } from "../domain/formatting";
import { actionLabel, enumValueLabel, resourceTypeLabel } from "../domain/labels";
import { countWithUnit, routeAttemptCountText, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError } from "../resources/payloads";
import { DataSection, SimpleTable, StatusPill } from "../shared/ui";
import { PaginationControls, type PaginationState } from "../shared/pagination";

export function AuditView({ api, data, user }: { api: ApiContext; data: AppData; user: AdminUser }) {
  const [activeAuditTab, setActiveAuditTab] = useState<"requests" | "admin">("requests");
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<AuditRequestStatus>("all");
  const [timeRange, setTimeRange] = useState<AuditRequestTimeRange>("all");
  const [modelFilter, setModelFilter] = useState("");
  const [requestLogs, setRequestLogs] = useState<RequestLogPage["data"]>([]);
  const [requestPage, setRequestPage] = useState(1);
  const [requestPageSize, setRequestPageSize] = useState(20);
  const [requestPagination, setRequestPagination] = useState<RequestLogPagination>(() => emptyRequestLogPagination(1, 20));
  const [requestSummary, setRequestSummary] = useState<RequestLogSummary>(() => emptyRequestLogSummary());
  const [requestListLoading, setRequestListLoading] = useState(false);
  const [requestListError, setRequestListError] = useState("");
  const [selectedRequestID, setSelectedRequestID] = useState("");
  const [detail, setDetail] = useState<RequestDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const showAdminAudit = canViewAdminAudit(user);
  const modelOptions = useMemo(
    () => [...new Set(data.models.map((model) => model.name).filter(Boolean))].sort((left, right) => left.localeCompare(right)),
    [data.models],
  );

  useEffect(() => {
    if (!showAdminAudit && activeAuditTab === "admin") {
      setActiveAuditTab("requests");
    }
  }, [activeAuditTab, showAdminAudit]);

  useEffect(() => {
    if (activeAuditTab !== "requests") return;
    const controller = new AbortController();
    const delay = query.trim() ? 250 : 0;
    setRequestListLoading(true);
    setRequestListError("");
    setRequestLogs([]);
    setRequestPagination(emptyRequestLogPagination(requestPage, requestPageSize));
    setRequestSummary(emptyRequestLogSummary());
    const timeout = window.setTimeout(() => {
	  const requestTimeRange = auditRequestTimeRangeParameters(timeRange, new Date());
      adminFetch(api, auditRequestPagePath({
        page: requestPage,
        pageSize: requestPageSize,
        status: statusFilter,
        query,
		model: modelFilter,
		...requestTimeRange,
      }), { signal: controller.signal })
        .then(async (resp) => {
          if (!resp.ok) throw new Error(`request logs ${resp.status}`);
          return (await resp.json()) as RequestLogPage;
        })
        .then((payload) => {
          setRequestLogs(payload.data ?? []);
          setRequestPagination(payload.pagination);
          setRequestSummary(payload.summary);
          const lastPage = Math.max(1, payload.pagination.total_pages);
          if (requestPage > lastPage) setRequestPage(lastPage);
        })
        .catch((err) => {
          if (controller.signal.aborted || isAuthExpiredError(err)) return;
          setRequestListError(tx("连接失败"));
        })
        .finally(() => {
          if (!controller.signal.aborted) setRequestListLoading(false);
        });
    }, delay);
    return () => {
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [activeAuditTab, api, modelFilter, query, requestPage, requestPageSize, statusFilter, timeRange]);

  const requestLogPagination = useMemo<PaginationState>(() => {
    const pageCount = Math.max(1, requestPagination.total_pages);
    const page = Math.min(requestPage, pageCount);
    const startIndex = requestPagination.total === 0 ? 0 : (page - 1) * requestPageSize;
    return {
      page,
      pageSize: requestPageSize,
      pageCount,
      startIndex,
      endIndex: Math.min(startIndex + requestLogs.length, requestPagination.total),
      setPage: (nextPage) => setRequestPage(Math.min(Math.max(nextPage, 1), pageCount)),
      setPageSize: (nextPageSize) => {
        setRequestPageSize(nextPageSize);
        setRequestPage(1);
      },
    };
  }, [requestLogs.length, requestPage, requestPageSize, requestPagination.total, requestPagination.total_pages]);

  useEffect(() => {
    if (activeAuditTab !== "requests") return;
    if (requestLogs.length === 0) {
      setSelectedRequestID("");
      setDetail(null);
      return;
    }
    const selectedVisible = requestLogs.some((log) => log.request_id === selectedRequestID);
    if (!selectedRequestID || !selectedVisible) {
      setSelectedRequestID(requestLogs[0].request_id);
    }
  }, [activeAuditTab, requestLogs, selectedRequestID]);

  useEffect(() => {
    if (activeAuditTab !== "requests") return;
    if (!selectedRequestID) {
      setDetail(null);
      return;
    }
    let alive = true;
    setDetailLoading(true);
    setDetailError("");
    adminFetch(api, `/api/admin/audit/requests/${encodeURIComponent(selectedRequestID)}`)
      .then(async (resp) => {
        if (!resp.ok) throw new Error(`request detail ${resp.status}`);
        return (await resp.json()) as RequestDetail;
      })
      .then((payload) => {
        if (!alive) return;
        setDetail({
          log: payload.log,
          usage: payload.usage ?? [],
          attempts: payload.attempts ?? [],
          payload: payload.payload ?? null,
        });
      })
      .catch((err) => {
        if (isAuthExpiredError(err) || !alive) return;
        setDetail(null);
        setDetailError(err instanceof Error ? err.message : tx("请求详情加载失败"));
      })
      .finally(() => {
        if (alive) setDetailLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [activeAuditTab, api, selectedRequestID]);

  const requestStats = useMemo(() => {
    const successRate = requestSummary.all ? Math.round((requestSummary.ok / requestSummary.all) * 100) : 0;
    return {
      total: requestSummary.all,
      failures: requestSummary.error,
      averageLatency: requestSummary.average_latency_ms,
      successRate,
    };
  }, [requestSummary]);

  const filters = [
    { key: "all", label: `${tx("全部")} ${requestSummary.all}` },
    { key: "ok", label: `${tx("成功")} ${requestSummary.ok}` },
    { key: "error", label: `${tx("失败")} ${requestSummary.error}` },
  ] as const;
  const timeFilters = [
    { key: "all", label: tx("全部时间") },
    { key: "15m", label: tx("最近 15 分钟") },
    { key: "1h", label: tx("最近 1 小时") },
    { key: "24h", label: tx("最近 24 小时") },
  ] as const;

  return (
    <div className="audit-view">
      <div className="audit-tabs" role="tablist" aria-label={tx("日志类型")}>
        <button
          type="button"
          className={`audit-tab ${activeAuditTab === "requests" ? "active" : ""}`}
          onClick={() => setActiveAuditTab("requests")}
          role="tab"
          aria-selected={activeAuditTab === "requests"}
        >
          <Activity size={15} />
          <span>{tx("大模型请求历史")}</span>
          <strong>{formatNumber(requestSummary.all)}</strong>
        </button>
        {showAdminAudit ? (
          <button
            type="button"
            className={`audit-tab ${activeAuditTab === "admin" ? "active" : ""}`}
            onClick={() => setActiveAuditTab("admin")}
            role="tab"
            aria-selected={activeAuditTab === "admin"}
          >
            <ShieldCheck size={15} />
            <span>{tx("后台操作审计")}</span>
            <strong>{formatNumber(data.auditEvents.length)}</strong>
          </button>
        ) : null}
      </div>

      {activeAuditTab === "requests" || !showAdminAudit ? (
        <DataSection title="大模型请求历史">
          <div className="request-history">
            <div className="request-history-toolbar">
              <label className="request-search" aria-label={tx("搜索请求历史")}>
                <Search size={15} />
                <input
                  value={query}
                  onChange={(event) => {
                    setQuery(event.target.value);
                    setRequestPage(1);
                  }}
                  placeholder={tx("搜索请求 ID、模型、Provider、状态码")}
                />
              </label>
              <div className="request-filter-tabs" role="tablist" aria-label={tx("请求状态筛选")}>
                {filters.map((filter) => (
                  <button
                    key={filter.key}
                    type="button"
                    className={statusFilter === filter.key ? "active" : ""}
                    onClick={() => {
                      setStatusFilter(filter.key);
                      setRequestPage(1);
                    }}
                  >
                    {filter.label}
                  </button>
                ))}
              </div>
              <div className="request-filter-tabs" role="group" aria-label={tx("请求时间范围")}>
                {timeFilters.map((filter) => (
                  <button
                    key={filter.key}
                    type="button"
                    className={timeRange === filter.key ? "active" : ""}
					aria-pressed={timeRange === filter.key}
                    onClick={() => {
                      setTimeRange(filter.key);
                      setRequestPage(1);
                    }}
                  >
                    {filter.label}
                  </button>
                ))}
              </div>
              <select
                aria-label={tx("按模型筛选")}
                className="request-log-model-filter"
                value={modelFilter}
                onChange={(event) => {
                  setModelFilter(event.target.value);
                  setRequestPage(1);
                }}
              >
                <option value="">{tx("全部模型")}</option>
                {modelOptions.map((model) => <option key={model} value={model}>{model}</option>)}
              </select>
            </div>

            <div className="metrics request-metrics">
              <RequestMetric label="总请求" value={formatNumber(requestStats.total)} icon={Activity} />
              <RequestMetric label="成功率" value={`${requestStats.successRate}%`} icon={Check} />
              <RequestMetric label="失败请求" value={formatNumber(requestStats.failures)} icon={AlertCircle} />
              <RequestMetric label="平均延迟" value={`${requestStats.averageLatency}ms`} icon={Gauge} />
            </div>

            <div className="request-history-layout">
              <div className="request-list-panel" aria-busy={requestListLoading}>
                <div className="request-list-head">
                  <span>{tx("请求列表")}</span>
                  <strong>{countWithUnit(requestPagination.total, "条", "record", "件")}</strong>
                </div>
                {requestListError ? (
                  <div className="compact-empty">{requestListError}</div>
                ) : requestListLoading && requestLogs.length === 0 ? (
                  <div className="compact-empty">{tx("加载中")}...</div>
                ) : requestLogs.length === 0 ? (
                  <div className="compact-empty">{tx("没有匹配的请求记录")}</div>
                ) : (
                  <div className="request-list" role="list">
                    {requestLogs.map((log) => (
                      <button
                        key={log.request_id}
                        type="button"
                        className={`request-list-row ${selectedRequestID === log.request_id ? "active" : ""}`}
                        onClick={() => setSelectedRequestID(log.request_id)}
                      >
                        <span className="request-row-main">
                          <strong>{log.model || "-"}</strong>
                          <span>{log.request_id}</span>
                        </span>
                        <span className="request-row-meta">
                          <span>{providerAuditLabel(data, log)}</span>
                          <span>{formatTime(log.created_at)}</span>
                          {log.usage_record_count ? <span>{compactNumber(log.total_tokens ?? 0)} Token</span> : null}
                          {log.usage_record_count ? <span>${formatMoney(log.estimated_cost_usd ?? 0)}</span> : null}
                        </span>
                        <span className="request-row-tail">
                          <StatusPill status={log.status_code >= 400 ? "error" : "ok"} label={String(log.status_code || "-")} />
                          <span>{log.latency_ms || 0}ms</span>
                        </span>
                      </button>
                    ))}
                  </div>
                )}
                <PaginationControls pagination={requestLogPagination} totalItems={requestPagination.total} />
              </div>

              <RequestDetailPanel
                data={data}
                requestID={selectedRequestID}
                detail={detail?.log.request_id === selectedRequestID ? detail : null}
                loading={detailLoading}
                error={detailError}
                showProviderCost={showAdminAudit}
              />
            </div>
          </div>
        </DataSection>
      ) : (
        <DataSection title="后台操作审计">
          <SimpleTable
            columns={["时间", "操作人", "动作", "对象", "对象 ID", "状态", "来源 IP"]}
            paginationKey="admin-audit-events"
            rows={data.auditEvents.map((event) => [
              formatTime(event.created_at),
              event.actor_name || event.actor_user_id || "-",
              actionLabel(event.action),
              resourceTypeLabel(event.resource_type),
              event.resource_id || "-",
              <StatusPill key={event.id} status={event.status === "success" ? "ok" : "error"} label={enumValueLabel(event.status)} />,
              event.ip || "-",
            ])}
          />
        </DataSection>
      )}
    </div>
  );
}

function emptyRequestLogPagination(page: number, pageSize: number): RequestLogPagination {
  return {
    page,
    page_size: pageSize,
    total: 0,
    total_pages: 0,
  };
}

function emptyRequestLogSummary(): RequestLogSummary {
  return {
    all: 0,
    ok: 0,
    error: 0,
    average_latency_ms: 0,
  };
}

export function RequestDetailPanel({
  data,
  requestID,
  detail,
  loading,
  error,
  showProviderCost,
}: {
  data: AppData;
  requestID: string;
  detail: RequestDetail | null;
  loading: boolean;
  error: string;
  showProviderCost: boolean;
}) {
  const [copied, setCopied] = useState(false);

  if (!requestID) {
    return (
      <div className="request-detail-panel">
        <div className="compact-empty">{tx("暂无大模型请求记录")}</div>
      </div>
    );
  }

  if (loading && !detail) {
    return (
      <div className="request-detail-panel">
        <div className="compact-empty">{tx("正在加载请求详情...")}</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className="request-detail-panel">
        <div className="status-line error">{error}</div>
      </div>
    );
  }

  if (!detail) {
    return (
      <div className="request-detail-panel">
        <div className="compact-empty">{tx("请选择一条请求")}</div>
      </div>
    );
  }

  const { log } = detail;
  const usageTotals = detail.usage.reduce(
    (sum, item) => ({
      input_tokens: sum.input_tokens + (item.input_tokens || 0),
      cached_input_tokens: sum.cached_input_tokens + (item.cached_input_tokens || 0),
      cache_write_input_tokens: sum.cache_write_input_tokens + (item.cache_write_input_tokens || 0),
      input_audio_tokens: sum.input_audio_tokens + (item.input_audio_tokens || 0),
      output_tokens: sum.output_tokens + (item.output_tokens || 0),
      reasoning_output_tokens: sum.reasoning_output_tokens + (item.reasoning_output_tokens || 0),
      output_audio_tokens: sum.output_audio_tokens + (item.output_audio_tokens || 0),
      accepted_prediction_tokens: sum.accepted_prediction_tokens + (item.accepted_prediction_tokens || 0),
      rejected_prediction_tokens: sum.rejected_prediction_tokens + (item.rejected_prediction_tokens || 0),
      total_tokens: sum.total_tokens + (item.total_tokens || 0),
      estimated_cost_usd: sum.estimated_cost_usd + (item.estimated_cost_usd || 0),
      provider_cost_usd: sum.provider_cost_usd + (item.provider_cost_usd || 0),
    }),
    {
      input_tokens: 0,
      cached_input_tokens: 0,
      cache_write_input_tokens: 0,
      input_audio_tokens: 0,
      output_tokens: 0,
      reasoning_output_tokens: 0,
      output_audio_tokens: 0,
      accepted_prediction_tokens: 0,
      rejected_prediction_tokens: 0,
      total_tokens: 0,
      estimated_cost_usd: 0,
      provider_cost_usd: 0,
    },
  );
  const regularInputTokens = Math.max(
    0,
    usageTotals.input_tokens - usageTotals.cached_input_tokens - usageTotals.cache_write_input_tokens - usageTotals.input_audio_tokens,
  );
  const regularOutputTokens = Math.max(
    0,
    usageTotals.output_tokens - usageTotals.reasoning_output_tokens - usageTotals.output_audio_tokens -
      usageTotals.accepted_prediction_tokens - usageTotals.rejected_prediction_tokens,
  );
  const isError = log.status_code >= 400;

  async function copyRequestID() {
    const success = await copyText(log.request_id);
    setCopied(success);
    if (success) window.setTimeout(() => setCopied(false), 1200);
  }

  return (
    <div className="request-detail-panel">
      <div className="request-detail-head">
        <div>
          <span>{tx("请求详情")}</span>
          <strong>{log.model || "-"}</strong>
        </div>
        <StatusPill status={isError ? "error" : "ok"} label={String(log.status_code || "-")} />
      </div>

      <div className="request-id-line">
        <code>{log.request_id}</code>
        <button type="button" className="request-copy-button" onClick={() => void copyRequestID()} title={tx("复制请求 ID")}>
          {copied ? <Check size={14} /> : <Copy size={14} />}
        </button>
      </div>

      <div className="request-detail-grid">
        <DetailField label="时间" value={formatTime(log.created_at)} />
        <DetailField label="延迟" value={`${log.latency_ms || 0}ms`} />
        <DetailField label="项目" value={projectName(data, log.project_id)} />
        <DetailField label="API Key" value={apiKeyAuditLabel(data, log.api_key_id)} />
        <DetailField label="最终 Provider" value={providerAuditLabel(data, log)} />
        <DetailField label="Provider 资源" value={providerResourceAuditLabel(data, log.provider_resource_id)} />
        <DetailField label="上游模型" value={log.provider_model || "-"} />
        <DetailField label="作用域策略" value={log.routing_policy_id || tx("无绑定策略")} />
        <DetailField label="策略作用域 / 优先级" value={log.routing_policy_id ? `${tx(log.routing_policy_scope || "-")} / P${log.routing_policy_priority || 0}` : "-"} />
        <DetailField label="客户端 IP" value={log.client_ip || "-"} />
      </div>

      {log.error_code ? (
        <div className="request-error-box">
          <strong>{log.error_code}</strong>
        </div>
      ) : null}

      <RequestPayloadSection payload={detail.payload ?? null} />

      <div className="request-subsection">
        <div className="request-subsection-title">
          <span>{tx("Token 与成本")}</span>
          <strong>{detail.usage.length ? countWithUnit(detail.usage.length, "条记录", "record", "件の記録") : tx("暂无记录")}</strong>
        </div>
        <div className="request-usage-breakdown">
          <UsageBreakdownGroup
            label="输入"
            total={usageTotals.input_tokens}
            rows={[
              ["input", regularInputTokens],
              ["input_cached_tokens", usageTotals.cached_input_tokens],
              ["input_cache_write_tokens", usageTotals.cache_write_input_tokens],
              ["input_audio_tokens", usageTotals.input_audio_tokens],
            ]}
          />
          <UsageBreakdownGroup
            label="输出"
            total={usageTotals.output_tokens}
            rows={[
              ["output", regularOutputTokens],
              ["output_reasoning_tokens", usageTotals.reasoning_output_tokens],
              ["output_audio_tokens", usageTotals.output_audio_tokens],
              ["output_accepted_prediction_tokens", usageTotals.accepted_prediction_tokens],
              ["output_rejected_prediction_tokens", usageTotals.rejected_prediction_tokens],
            ]}
          />
        </div>
        <div className="request-usage-total">
          <UsageStat label="总量" value={formatNumber(usageTotals.total_tokens)} />
          <UsageStat label="对外计费" value={`$${formatMoney(usageTotals.estimated_cost_usd)}`} />
          {showProviderCost ? <UsageStat label="渠道真实成本" value={`$${formatMoney(usageTotals.provider_cost_usd)}`} /> : null}
        </div>
      </div>

      <div className="request-subsection">
        <div className="request-subsection-title">
          <span>{tx("路由尝试")}</span>
          <strong>{routeAttemptCountText(detail.attempts.length)}</strong>
        </div>
        {detail.attempts.length === 0 ? (
          <div className="compact-empty">{tx("没有记录到路由尝试")}</div>
        ) : (
          <div className="attempt-timeline">
            {detail.attempts.map((attempt) => (
              <div className="attempt-row" key={attempt.id || `${attempt.request_id}-${attempt.attempt_index}`}>
                <div className={`attempt-marker ${attempt.status_code >= 400 ? "error" : "ok"}`}>
                  {attempt.attempt_index}
                </div>
                <div className="attempt-content">
                  <div className="attempt-head">
                    <strong>{providerAttemptLabel(data, attempt)}</strong>
                    <StatusPill
                      status={attempt.status_code >= 400 ? "error" : "ok"}
                      label={String(attempt.status_code || "-")}
                    />
                  </div>
                  <div className="attempt-meta">
                    <span>{tx("上游模型")} {attempt.provider_model || "-"}</span>
                    <span>{tx("资源")} {providerResourceAuditLabel(data, attempt.provider_resource_id)}</span>
                    <span>{tx("路由")} {attempt.route_id || "-"}</span>
                  </div>
                  {attempt.error_code || attempt.error_message ? (
                    <p className="attempt-error">
                      {[attempt.error_code, attempt.error_message].filter(Boolean).join(" · ")}
                    </p>
                  ) : null}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      <div className="request-client-agent">
        <span>User-Agent</span>
        <code>{log.user_agent || "-"}</code>
      </div>
    </div>
  );
}

export function DetailField({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="detail-field">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
    </div>
  );
}

export function UsageStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="usage-stat">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
    </div>
  );
}

export function UsageBreakdownGroup({
  label,
  total,
  rows,
}: {
  label: string;
  total: number;
  rows: Array<[string, number]>;
}) {
  return (
    <div className="usage-breakdown-group">
      <div className="usage-breakdown-head">
        <span>{tx(label)}</span>
        <strong>{formatNumber(total)}</strong>
      </div>
      <div className="usage-breakdown-rows">
        {rows.map(([rowLabel, value]) => (
          <div className="usage-breakdown-row" key={rowLabel}>
            <code>{rowLabel}</code>
            <strong>{formatNumber(value)}</strong>
          </div>
        ))}
      </div>
    </div>
  );
}

export function RequestPayloadSection({ payload }: { payload: RequestPayloadLog | null }) {
  return (
    <div className="request-subsection">
      <div className="request-subsection-title">
        <span>{tx("请求与响应")}</span>
        <strong>{payload ? tx("已记录快照") : tx("未记录")}</strong>
      </div>
      {!payload ? (
        <div className="compact-empty">{tx("这条历史记录没有保存 request / response 快照")}</div>
      ) : (
        <div className="payload-grid">
          <PayloadBlock
            title="Request"
            body={payload.request_body || tx("未记录请求内容")}
            truncated={payload.request_truncated}
          />
          <PayloadBlock
            title="Response"
            body={payload.response_body || tx("未记录响应内容")}
            truncated={payload.response_truncated}
          />
        </div>
      )}
    </div>
  );
}

export function PayloadBlock({ title, body, truncated }: { title: string; body: string; truncated: boolean }) {
  return (
    <div className="payload-block">
      <div className="payload-block-head">
        <span>{title}</span>
        {truncated ? <strong>{tx("已截断")}</strong> : null}
      </div>
      <pre>
        <code>{body}</code>
      </pre>
    </div>
  );
}

export function RequestMetric({
  label,
  value,
  icon: Icon,
}: {
  label: string;
  value: string;
  icon: React.ComponentType<{ size?: number }>;
}) {
  return (
    <article className="metric compact-metric">
      <div className="metric-label">
        <Icon size={17} />
        {tx(label)}
      </div>
      <div className="metric-value">{value}</div>
    </article>
  );
}
