import { ArrowLeft, CalendarDays, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { appRole } from "../core/navigation";
import { type AdminUser, type ApiContext, type APIKeyUsageMetrics, type APIKeyUsageResponse, type AppData, type RequestDetail, type RequestLogPage } from "../core/types";
import { apiKeyCustomUsageRange, apiKeyUsageRangeForDays, type APIKeyUsageRange, utcDateInputValue } from "../domain/api-key-usage-range";
import { auditRequestPagePath, type AuditRequestStatus } from "../domain/audit-request-page";
import { findProvider, projectName, providerResourceAuditLabel, usageMemberLabel } from "../domain/entities";
import { formatNumber, formatTime } from "../domain/formatting";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { PaginationControls, type PaginationState } from "../shared/pagination";
import { DataSection, SimpleTable, StatusPill } from "../shared/ui";
import { RequestDetailPanel } from "./audit";

type UsageMetricKey = "request_count" | "total_tokens" | "estimated_cost_usd";

export function APIKeyUsageView({ api, data, user, keyID, onBack }: { api: ApiContext; data: AppData; user: AdminUser; keyID: string; onBack: () => void }) {
  const [rangeOption, setRangeOption] = useState<"7" | "30" | "90" | "custom">("30");
  const [range, setRange] = useState<APIKeyUsageRange>(() => apiKeyUsageRangeForDays(30));
  const [customFrom, setCustomFrom] = useState(() => utcDateInputValue(range.from));
  const [customTo, setCustomTo] = useState(() => utcDateInputValue(range.to));
  const [usage, setUsage] = useState<APIKeyUsageResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [customError, setCustomError] = useState("");
  const [chartMetric, setChartMetric] = useState<UsageMetricKey>("request_count");

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError("");
    setUsage(null);
    const query = new URLSearchParams({ from: range.from, to: range.to });
    adminFetch(api, `/api/admin/api-keys/${encodeURIComponent(keyID)}/usage?${query.toString()}`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("Key 用量加载失败")));
        return await response.json() as APIKeyUsageResponse;
      })
      .then((payload) => setUsage(payload))
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setUsage(null);
        setError(reason instanceof Error ? reason.message : tx("Key 用量加载失败"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [api, keyID, range]);

  function selectPreset(days: 7 | 30 | 90) {
    setRangeOption(String(days) as "7" | "30" | "90");
    setCustomError("");
    setRange(apiKeyUsageRangeForDays(days));
  }

  function applyCustomRange() {
    const next = apiKeyCustomUsageRange(customFrom, customTo);
    if (!next) {
      setCustomError(tx("请选择有效且不超过 366 天的 UTC 日期范围"));
      return;
    }
    setCustomError("");
    setRangeOption("custom");
    setRange(next);
  }

  const key = usage?.key ?? data.keys.find((item) => item.id === keyID);
  return (
    <div className="api-key-usage-page">
      <header className="api-key-usage-head">
        <button className="secondary-button" type="button" onClick={onBack}><ArrowLeft size={16} />{tx("返回 Key 管理")}</button>
        <div>
          <p className="eyebrow">{tx("API Key 用量")}</p>
          <h1>{key?.name || tx("Key 用量详情")}</h1>
          <p>{tx("查看单个 Key 的请求、Token、成本、额度和错误明细。")}</p>
        </div>
        {key ? <StatusPill status={key.status} /> : null}
      </header>

      {key ? <APIKeyIdentity data={data} keyData={key} /> : null}
      <UsageRangeToolbar
        rangeOption={rangeOption}
        customFrom={customFrom}
        customTo={customTo}
        error={customError}
        onPreset={selectPreset}
        onCustomFrom={setCustomFrom}
        onCustomTo={setCustomTo}
        onApply={applyCustomRange}
      />

      {loading && !usage ? <div className="compact-empty">{tx("正在加载 Key 用量...")}</div> : null}
      {error ? <div className="status-line error">{error}</div> : null}
      {usage ? (
        <>
          <UsageSummary metrics={usage.summary} />
          <QuotaOverview usage={usage} />
          <DataSection title="用量趋势">
            <div className="api-key-chart-tools">
              {(["request_count", "total_tokens", "estimated_cost_usd"] as UsageMetricKey[]).map((metric) => (
                <button className={chartMetric === metric ? "active" : ""} key={metric} onClick={() => setChartMetric(metric)} type="button">
                  {tx(metric === "request_count" ? "请求" : metric === "total_tokens" ? "Token" : "预估成本")}
                </button>
              ))}
              <span><CalendarDays size={14} />UTC</span>
            </div>
            <UsageTrend points={usage.timeseries} metric={chartMetric} />
          </DataSection>
          <div className="two-column api-key-usage-breakdowns">
            <DataSection title="模型明细">
              <SimpleTable columns={["模型", "请求", "错误率", "平均延迟", "Token", "预估成本"]} rows={usage.models.map((row) => [
                row.id || tx("未知"), formatNumber(row.request_count), formatPercent(row.error_count, row.request_count), formatDuration(row.average_latency_ms), formatNumber(row.total_tokens), formatUSD(row.estimated_cost_usd),
              ])} paginationKey="api-key-usage-models" />
            </DataSection>
            <DataSection title="错误分布">
              <SimpleTable columns={["错误码", "HTTP 状态", "次数", "最近发生"]} rows={usage.errors.map((row) => [
                row.error_code || row.id, row.status_code || "-", formatNumber(row.request_count), row.last_occurred_at ? formatTime(row.last_occurred_at) : "-",
              ])} paginationKey="api-key-usage-errors" />
            </DataSection>
          </div>
          {usage.providers?.length ? <ProviderUsage data={data} rows={usage.providers} /> : null}
          <APIKeyRequestExplorer api={api} data={data} user={user} keyID={keyID} range={range} modelOptions={usage.models.map((row) => row.id).filter(Boolean)} />
        </>
      ) : null}
    </div>
  );
}

function APIKeyIdentity({ data, keyData }: { data: AppData; keyData: APIKeyUsageResponse["key"] }) {
  const ownerID = keyData.owner_user_id || keyData.metadata?.created_by || "";
  const successor = data.keys.find((item) => item.rotated_from_id === keyData.id);
  return (
    <section className="api-key-identity">
      <IdentityField label="Key" value={`${keyData.key_prefix}...${keyData.key_suffix}`} />
      <IdentityField label="归属项目" value={projectName(data, keyData.project_id)} />
      <IdentityField label="归属用户" value={ownerID ? usageMemberLabel(data, ownerID) : "-"} />
      <IdentityField label="用途/环境" value={keyData.group || "-"} />
      <IdentityField label="最后使用" value={keyData.last_used_at ? formatTime(keyData.last_used_at) : tx("尚未使用")} />
      <IdentityField label="有效期" value={keyData.expires_at ? formatTime(keyData.expires_at) : tx("长期有效")} />
      <IdentityField label="Key RPM / TPM" value={`${keyLimitText(keyData.rate_limit_rpm)} / ${keyLimitText(keyData.token_limit_tpm)}`} />
      <IdentityField label="轮换关系" value={keyData.rotated_from_id ? tx("由旧 Key 轮换生成") : successor ? tx("已轮换为新 Key") : tx("无轮换记录")} />
    </section>
  );
}

function IdentityField({ label, value }: { label: string; value: string }) {
  return <div><span>{tx(label)}</span><strong>{value}</strong></div>;
}

function UsageRangeToolbar({ rangeOption, customFrom, customTo, error, onPreset, onCustomFrom, onCustomTo, onApply }: {
  rangeOption: "7" | "30" | "90" | "custom";
  customFrom: string;
  customTo: string;
  error: string;
  onPreset: (days: 7 | 30 | 90) => void;
  onCustomFrom: (value: string) => void;
  onCustomTo: (value: string) => void;
  onApply: () => void;
}) {
  return (
    <div className="api-key-range-toolbar">
      <div className="api-key-range-presets">
        {([7, 30, 90] as const).map((days) => <button className={rangeOption === String(days) ? "active" : ""} key={days} onClick={() => onPreset(days)} type="button">{formatUsageDays(days)}</button>)}
      </div>
      <div className="api-key-custom-range">
        <label><span>{tx("开始日期")}</span><input aria-label={tx("开始日期")} type="date" value={customFrom} onChange={(event) => onCustomFrom(event.target.value)} /></label>
        <span>—</span>
        <label><span>{tx("结束日期")}</span><input aria-label={tx("结束日期")} type="date" value={customTo} onChange={(event) => onCustomTo(event.target.value)} /></label>
        <button className="secondary-button" type="button" onClick={onApply}>{tx("应用")}</button>
      </div>
      {error ? <span className="status-line error">{error}</span> : <small>{tx("统计和额度周期均以 UTC 为准")}</small>}
    </div>
  );
}

function UsageSummary({ metrics }: { metrics: APIKeyUsageMetrics }) {
  const successCount = Math.max(0, metrics.request_count - metrics.error_count);
  return (
    <section className="api-key-usage-kpis">
      <UsageKPI label="请求总数" value={formatNumber(metrics.request_count)} detail={formatTranslationTemplate(tx("成功 {success} · 失败 {failure}"), { success: formatNumber(successCount), failure: formatNumber(metrics.error_count) })} />
      <UsageKPI label="成功率" value={formatPercent(successCount, metrics.request_count)} detail={formatTranslationTemplate(tx("平均延迟 {latency}"), { latency: formatDuration(metrics.average_latency_ms) })} />
      <UsageKPI label="总 Token" value={formatNumber(metrics.total_tokens)} detail={formatTranslationTemplate(tx("输入 {input} · 输出 {output}"), { input: formatNumber(metrics.input_tokens), output: formatNumber(metrics.output_tokens) })} />
      <UsageKPI label="缓存 Token" value={formatNumber(metrics.cached_input_tokens + metrics.cache_write_input_tokens)} detail={formatTranslationTemplate(tx("缓存读 {read} · 缓存写 {write}"), { read: formatNumber(metrics.cached_input_tokens), write: formatNumber(metrics.cache_write_input_tokens) })} />
      <UsageKPI label="推理 Token" value={formatNumber(metrics.reasoning_output_tokens)} detail={tx("包含在输出和总 Token 中")} />
      <UsageKPI label="预估成本" value={formatUSD(metrics.estimated_cost_usd)} detail={tx("按对外模型价格估算")} />
    </section>
  );
}

function UsageKPI({ label, value, detail }: { label: string; value: string; detail: string }) {
  return <article><span>{tx(label)}</span><strong>{value}</strong><small>{detail}</small></article>;
}

function QuotaOverview({ usage }: { usage: APIKeyUsageResponse }) {
  const limits = usage.quota.effective_limits;
  return (
    <DataSection title="当前 Key 有效额度">
      <div className="api-key-quota-grid">
        <QuotaCard label="今日请求" used={usage.quota.day.usage.requests} limit={limits.daily_requests} />
        <QuotaCard label="本月请求" used={usage.quota.month.usage.requests} limit={limits.monthly_requests} />
        <QuotaCard label="今日 Token" used={usage.quota.day.usage.total_tokens} limit={limits.daily_tokens} />
        <QuotaCard label="本月 Token" used={usage.quota.month.usage.total_tokens} limit={limits.monthly_tokens} />
        <QuotaCard label="今日成本" used={usage.quota.day.usage.cost_usd} limit={limits.daily_cost_usd} money />
        <QuotaCard label="本月成本" used={usage.quota.month.usage.cost_usd} limit={limits.monthly_cost_usd} money />
      </div>
      <div className="api-key-effective-limits">
        <span>{formatTranslationTemplate(tx("RPM {limit}"), { limit: limitText(limits.rate_limit_rpm) })}</span>
        <span>{formatTranslationTemplate(tx("TPM {limit}"), { limit: limitText(limits.token_limit_tpm) })}</span>
        <span>{formatTranslationTemplate(tx("最大并发 {limit}"), { limit: limitText(limits.max_concurrency) })}</span>
      </div>
    </DataSection>
  );
}

function QuotaCard({ label, used, limit, money = false }: { label: string; used: number; limit: number; money?: boolean }) {
  const percent = limit > 0 ? Math.max(0, used / limit * 100) : 0;
  const value = money ? formatUSD(used) : formatNumber(used);
  const limitValue = limit > 0 ? (money ? formatUSD(limit) : formatNumber(limit)) : tx("不限");
  return (
    <article className="api-key-quota-card">
      <span>{tx(label)}</span><strong>{value}</strong><small>{formatTranslationTemplate(tx("上限 {limit}"), { limit: limitValue })}</small>
      {limit > 0 ? <div className="api-key-quota-track"><span style={{ width: `${Math.min(100, percent)}%` }} /></div> : null}
      {limit > 0 ? <em>{formatStandalonePercent(percent)}</em> : null}
    </article>
  );
}

function UsageTrend({ points, metric }: { points: APIKeyUsageResponse["timeseries"]; metric: UsageMetricKey }) {
  if (!points.length) return <div className="compact-empty">{tx("所选时间范围内暂无用量")}</div>;
  const values = points.map((point) => point[metric]);
  const max = Math.max(...values, 1);
  return (
    <div className="api-key-usage-chart" role="img" aria-label={tx("用量趋势")}>
      {points.map((point, index) => {
        const value = point[metric];
        const labelEvery = Math.max(1, Math.ceil(points.length / 8));
        return (
          <div className="api-key-usage-bar" key={point.date} title={`${formatUTCDate(point.date)} · ${formatMetric(value, metric)}`}>
            <span style={{ height: `${Math.max(value > 0 ? 3 : 0, value / max * 100)}%` }} />
            {index % labelEvery === 0 || index === points.length - 1 ? <small>{formatUTCDate(point.date, true)}</small> : null}
          </div>
        );
      })}
    </div>
  );
}

function ProviderUsage({ data, rows }: { data: AppData; rows: APIKeyUsageResponse["models"] }) {
  return (
    <DataSection title="Provider 路由表现">
      <SimpleTable columns={["Provider", "资源", "请求", "错误率", "平均延迟", "Token", "预估成本"]} rows={rows.map((row) => [
        findProvider(data, row.id)?.name || row.id || tx("未路由"), providerResourceAuditLabel(data, row.resource_id), formatNumber(row.request_count), formatPercent(row.error_count, row.request_count), formatDuration(row.average_latency_ms), formatNumber(row.total_tokens), formatUSD(row.estimated_cost_usd),
      ])} paginationKey="api-key-usage-providers" />
    </DataSection>
  );
}

function APIKeyRequestExplorer({ api, data, user, keyID, range, modelOptions }: { api: ApiContext; data: AppData; user: AdminUser; keyID: string; range: APIKeyUsageRange; modelOptions: string[] }) {
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState<AuditRequestStatus>("all");
  const [model, setModel] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(20);
  const [payload, setPayload] = useState<RequestLogPage>({ data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } });
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [selectedRequestID, setSelectedRequestID] = useState("");
  const [detail, setDetail] = useState<RequestDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");

  useEffect(() => setPage(1), [keyID, model, query, range, status]);
  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      setLoading(true);
      setError("");
      adminFetch(api, auditRequestPagePath({ page, pageSize, status, query, model, apiKeyID: keyID, since: range.from, until: inclusiveRangeEnd(range.to) }), { signal: controller.signal })
        .then(async (response) => {
          if (!response.ok) throw new Error(await readAdminError(response, tx("请求明细加载失败")));
          return await response.json() as RequestLogPage;
        })
        .then((next) => {
          setPayload(next);
          setSelectedRequestID((current) => next.data.some((item) => item.request_id === current) ? current : (next.data[0]?.request_id ?? ""));
        })
        .catch((reason) => {
          if (controller.signal.aborted || isAuthExpiredError(reason)) return;
          setPayload({ data: [], pagination: { page, page_size: pageSize, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } });
          setSelectedRequestID("");
          setError(reason instanceof Error ? reason.message : tx("请求明细加载失败"));
        })
        .finally(() => {
          if (!controller.signal.aborted) setLoading(false);
        });
    }, query.trim() ? 250 : 0);
    return () => { window.clearTimeout(timer); controller.abort(); };
  }, [api, keyID, model, page, pageSize, query, range, status]);

  useEffect(() => {
    if (!selectedRequestID) { setDetail(null); return; }
    const controller = new AbortController();
    setDetailLoading(true);
    setDetailError("");
    adminFetch(api, `/api/admin/audit/requests/${encodeURIComponent(selectedRequestID)}`, { signal: controller.signal })
      .then(async (response) => {
        if (!response.ok) throw new Error(await readAdminError(response, tx("请求详情加载失败")));
        return await response.json() as RequestDetail;
      })
      .then((next) => setDetail({ log: next.log, usage: next.usage ?? [], attempts: next.attempts ?? [], payload: next.payload ?? null }))
      .catch((reason) => {
        if (controller.signal.aborted || isAuthExpiredError(reason)) return;
        setDetail(null);
        setDetailError(reason instanceof Error ? reason.message : tx("请求详情加载失败"));
      })
      .finally(() => { if (!controller.signal.aborted) setDetailLoading(false); });
    return () => controller.abort();
  }, [api, selectedRequestID]);

  const pagination = useMemo<PaginationState>(() => ({
    page: Math.min(page, Math.max(1, payload.pagination.total_pages)), pageSize,
    pageCount: Math.max(1, payload.pagination.total_pages),
    startIndex: payload.pagination.total ? (page - 1) * pageSize : 0,
    endIndex: Math.min((page - 1) * pageSize + payload.data.length, payload.pagination.total),
    setPage: (next) => setPage(Math.min(Math.max(1, next), Math.max(1, payload.pagination.total_pages))),
    setPageSize: (next) => { setPageSize(next); setPage(1); },
  }), [page, pageSize, payload]);

  return (
    <DataSection title="请求明细">
      <div className="api-key-request-filters">
        <label className="search-box"><Search size={16} /><input aria-label={tx("搜索请求")} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索请求 ID、模型或错误码")} /></label>
        <select aria-label={tx("请求状态")} value={status} onChange={(event) => setStatus(event.target.value as AuditRequestStatus)}>
          <option value="all">{tx("全部状态")}</option><option value="ok">{tx("成功")}</option><option value="error">{tx("失败")}</option>
        </select>
        <select aria-label={tx("模型筛选")} value={model} onChange={(event) => setModel(event.target.value)}>
          <option value="">{tx("全部模型")}</option>{modelOptions.map((item) => <option key={item} value={item}>{item}</option>)}
        </select>
      </div>
      <div className="request-history-layout">
        <div className="request-list-panel">
          <div className="request-list-summary">
            <span>{formatTranslationTemplate(tx("请求 {count}"), { count: formatNumber(payload.summary.all) })}</span>
            <span>{formatTranslationTemplate(tx("失败 {count}"), { count: formatNumber(payload.summary.error) })}</span>
            <span>{formatTranslationTemplate(tx("平均延迟 {latency}"), { latency: formatDuration(payload.summary.average_latency_ms) })}</span>
          </div>
          {error ? <div className="status-line error">{error}</div> : null}
          {loading && !payload.data.length ? <div className="compact-empty">{tx("正在加载请求明细...")}</div> : null}
          {!loading && !payload.data.length ? <div className="compact-empty">{tx("所选条件下暂无请求")}</div> : null}
          <div className="request-list" role="list">
            {payload.data.map((log) => (
              <button className={`request-list-row ${selectedRequestID === log.request_id ? "active" : ""}`} key={log.request_id} onClick={() => setSelectedRequestID(log.request_id)} type="button">
                <span className="request-row-main"><strong>{log.model || "-"}</strong><span>{log.request_id}</span></span>
                <span className="request-row-meta"><span>{formatTime(log.created_at)}</span><span>{formatNumber(log.total_tokens ?? 0)} Token</span><span>{formatUSD(log.estimated_cost_usd ?? 0)}</span></span>
                <span className="request-row-tail"><StatusPill status={log.status_code >= 400 ? "error" : "ok"} label={String(log.status_code || "-")} /><span>{formatDuration(log.latency_ms)}</span></span>
              </button>
            ))}
          </div>
          <PaginationControls pagination={pagination} totalItems={payload.pagination.total} />
        </div>
        <RequestDetailPanel data={data} requestID={selectedRequestID} detail={detail?.log.request_id === selectedRequestID ? detail : null} loading={detailLoading} error={detailError} showProviderCost={appRole(user.role) === "admin"} />
      </div>
    </DataSection>
  );
}

function limitText(value: number | undefined) { return value && value > 0 ? formatNumber(value) : tx("不限"); }
function keyLimitText(value: number | undefined) { return value == null ? tx("继承上级") : value > 0 ? formatNumber(value) : tx("不额外限制"); }
function formatUsageDays(days: number) { return formatTranslationTemplate(tx("最近 {days} 天"), { days: formatNumber(days) }); }
function formatDuration(value: number) { return new Intl.NumberFormat(languageLocale(), { maximumFractionDigits: 0 }).format(value || 0) + " ms"; }
function formatUSD(value: number) { return new Intl.NumberFormat(languageLocale(), { style: "currency", currency: "USD", minimumFractionDigits: value >= 1 ? 2 : 4, maximumFractionDigits: value >= 1 ? 2 : 6 }).format(value || 0); }
function formatPercent(numerator: number, denominator: number) { return formatStandalonePercent(denominator > 0 ? numerator / denominator * 100 : 0); }
function formatStandalonePercent(value: number) { return new Intl.NumberFormat(languageLocale(), { style: "percent", maximumFractionDigits: 1 }).format((value || 0) / 100); }
function formatUTCDate(value: string, compact = false) { return new Intl.DateTimeFormat(languageLocale(), compact ? { month: "2-digit", day: "2-digit", timeZone: "UTC" } : { dateStyle: "medium", timeZone: "UTC" }).format(new Date(`${value}T00:00:00Z`)); }
function formatMetric(value: number, metric: UsageMetricKey) { return metric === "estimated_cost_usd" ? formatUSD(value) : formatNumber(value); }
function inclusiveRangeEnd(value: string) { return new Date(new Date(value).getTime() - 1).toISOString(); }
