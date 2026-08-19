import { Plus, RefreshCw, Search, Trash2, UserRoundCheck, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { appRole } from "../core/navigation";
import { type AdminResource, type AdminUser, type ApiContext, type AppData, type AuditEvent, type OpenAIAccountQuota, type OpenAIQuotaWindow, type Project, type Provider, type ProviderMonitoringSnapshot, type ProviderQuotaSummary, type ProviderResource, type ReportExportHistoryItem, type RequestLog, type ResourceAction, type ResourceConfig, type ToolbarAction } from "../core/types";
import { notificationChannelLabel } from "../domain/catalog";
import { providerDisplayBaseURL, providerDisplayName, providerDisplayType, providerRoutesFor, providerRouteSummary, stringifyValue } from "../domain/entities";
import { formatNumber, formatTime } from "../domain/formatting";
import { providerLatencyLabel, providerPerformanceExplanation, providerQualityScoreLabel } from "../domain/provider-monitoring";
import { enumValueLabel, providerTypeLabel, reportDatasetLabel, roleLabel } from "../domain/labels";
import { countWithUnit, languageLocale, tx } from "../i18n/runtime";
import { reportExportDefinitions } from "../resources/governance-config";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection, SimpleTable, StatusPill } from "../shared/ui";
import { APIKeyEmptyState } from "./api-key-empty-state";
import { ModelCategoryTabs, NotificationChannelTabs } from "./model-catalog";
import { ModelGovernanceEmptyState } from "./model-governance-empty-state";
import { latencyDisplay, requestLogFailed } from "./overview";
import { PaginationControls, type PaginationState } from "../shared/pagination";
import { APIKeyFlowHint, EntityTable, ResourceEmptyState, resultCountLabel, RouteStrategyHint, TableSkeleton } from "./settings-table";

export function CrudView<T>({
  config,
  data,
  api,
  user,
  items,
  monitorItems = items,
  totalItems,
  loading = false,
  query,
  pagination,
  categoryFilter,
  onCategoryFilter,
  onQuery,
  onCreate,
  onEdit,
  onDelete,
  onAction,
  onProjectOpen,
  onToolbarAction,
  currentUser = null,
}: {
  config: ResourceConfig<T>;
  data: AppData;
  api?: ApiContext;
  user?: AdminUser;
  items: T[];
  monitorItems?: T[];
  totalItems: number;
  loading?: boolean;
  query: string;
  pagination: PaginationState;
  categoryFilter: string;
  onCategoryFilter: (value: string) => void;
  onQuery: (value: string) => void;
  onCreate: () => void;
  onEdit: (item: T) => void;
  onDelete: (item: T) => void;
  onAction: (action: ResourceAction<T>, item: T) => void;
  onProjectOpen?: (project: Project) => void;
  onToolbarAction: (action: ToolbarAction) => void;
  currentUser?: AdminUser | null;
}) {
  const [selectedTeamID, setSelectedTeamID] = useState("");
  const isTeamView = config.view === "teams";
  const isProjectView = config.view === "projects";
  const isPersonalKeyView = config.view === "api-keys" && Boolean(user && appRole(user.role) === "user");
  const tableConfig = isPersonalKeyView
    ? { ...config, columns: config.columns.filter((column) => !["project_id", "project_owner", "project_team"].includes(String(column.key))) }
    : config;
  const selectedTeam = isTeamView
    ? (items as AdminResource[]).find((item) => item.id === selectedTeamID)
    : undefined;

  useEffect(() => {
    if (!isTeamView) return;
    const teamItems = items as AdminResource[];
    if (!selectedTeamID || !teamItems.some((item) => item.id === selectedTeamID)) {
      setSelectedTeamID("");
    }
  }, [isTeamView, items, selectedTeamID]);

  const detailPanelOpen = isTeamView && selectedTeam;

  if (config.view === "api-keys" && data.keys.length === 0 && !loading && !query.trim()) {
    return (
      <DataSection title={config.eyebrow}>
        <APIKeyEmptyState onCreate={onCreate} />
      </DataSection>
    );
  }

  if (config.view === "providers" && data.providers.length === 0 && !loading && !query.trim()) {
    return (
      <DataSection title={config.eyebrow}>
        <ModelGovernanceEmptyState
          stage="providers"
          title="还没有 Provider 渠道"
          description="先添加一个上游 Provider，并选择要引入的模型。Provider 模型价格用于记录真实成本与审计。"
          actionLabel={config.createLabel ?? "新增 Provider"}
          onAction={onCreate}
        />
      </DataSection>
    );
  }

  return (
    <DataSection title={config.eyebrow}>
      {config.view === "api-keys" && !isPersonalKeyView ? <APIKeyFlowHint data={data} /> : null}
      {config.view === "routes" ? <RouteStrategyHint data={data} /> : null}
      {isProjectView ? <ProjectTeamFlowHint /> : null}
      {config.view === "providers" || config.view === "models" ? (
        <ModelCategoryTabs
          data={data}
          view={config.view}
          active={categoryFilter}
          onChange={onCategoryFilter}
        />
      ) : null}
      {config.view === "notification-channels" ? (
        <NotificationChannelTabs
          data={data}
          active={categoryFilter}
          onChange={onCategoryFilter}
        />
      ) : null}
      <div className="table-toolbar">
        <div className="search-box">
          <Search size={16} />
          <input value={query} onChange={(event) => onQuery(event.target.value)} placeholder={tx("搜索名称、ID、状态")} />
        </div>
        <div className="table-toolbar-actions">
          <span className="table-result-count">{resultCountLabel(totalItems, query)}</span>
          {config.create ? (
            <button className="button" onClick={onCreate} type="button">
              <Plus size={17} />
              {isPersonalKeyView
                ? tx("创建 Key")
                : config.view === "notification-channels"
                  ? `${tx("配置")} ${notificationChannelLabel(categoryFilter)}`
                  : tx(config.createLabel ?? "新增")}
            </button>
          ) : null}
          {(config.toolbarActions ?? []).map((action) => (
            <button className="secondary-button" key={action.label} onClick={() => onToolbarAction(action)} title={tx(action.title ?? action.label)} type="button">
              {tx(action.label)}
            </button>
          ))}
        </div>
      </div>
      <div className={detailPanelOpen ? "resource-detail-layout with-panel" : "resource-detail-layout"}>
        <div className="resource-table-pane">
          {config.view === "providers" ? (
            <ProviderChannelTable
              api={api}
              config={config as unknown as ResourceConfig<Provider>}
              currentUser={currentUser}
              data={data}
              loading={loading}
              providers={items as Provider[]}
              query={query}
              summaryProviders={monitorItems as Provider[]}
              onAction={(action, provider) => onAction(action as unknown as ResourceAction<T>, provider as T)}
              onCreate={config.create ? onCreate : undefined}
              onDelete={(provider) => onDelete(provider as T)}
              onEdit={(provider) => onEdit(provider as T)}
            />
          ) : (
            <EntityTable
              config={tableConfig}
              data={data}
              apiBaseURL={api?.baseURL}
              items={items}
              loading={loading}
              query={query}
              currentUser={currentUser}
              onCreate={config.create ? onCreate : undefined}
              onEdit={onEdit}
              onDelete={onDelete}
              onAction={onAction}
              onRowClick={
                isTeamView
                  ? (item) => setSelectedTeamID((item as AdminResource).id)
                  : isProjectView
                    ? (item) => onProjectOpen?.(item as Project)
                    : undefined
              }
              rowOpenLabel={isProjectView ? "查看与配置" : undefined}
              selectedRowID={isTeamView ? selectedTeam?.id : undefined}
            />
          )}
          <PaginationControls pagination={pagination} totalItems={totalItems} />
        </div>
        {isTeamView && selectedTeam ? (
          <TeamMembersPanel data={data} team={selectedTeam} onClose={() => setSelectedTeamID("")} />
        ) : null}
      </div>
    </DataSection>
  );
}

export function ProjectTeamFlowHint() {
  return (
    <div className="workflow-hint project-team-flow-hint">
      <div>
        <strong>{tx("团队配置现在是项目设置的一部分")}</strong>
        <span>{tx("每个项目有 1 个主团队，还可以添加多个协作团队。主团队负责成本与审批，团队角色决定谁能访问项目。")}</span>
      </div>
      <div className="workflow-hint-stats">
        <span>{tx("主团队 · 责任归属")}</span>
        <span>{tx("协作团队 · 访问权限")}</span>
      </div>
    </div>
  );
}

export type ProviderMonitorTone = "healthy" | "degraded" | "down" | "unknown";

export type ProviderProbeTone = "ok" | "warn" | "down" | "na";

export type ProviderTrendTone = "success" | "warning" | "failure" | "none";

export type ProviderMonitorSampleSource = "codex_test" | "gateway_request";

export type ProviderMonitorSample = {
  created_at: string;
  success: boolean;
  latency_ms: number;
  error_code?: string;
};

export type ProviderMonitorRow = {
  provider: Provider;
  resources: ProviderResource[];
  routeCount: number;
  activeRouteCount: number;
  statusTone: ProviderMonitorTone;
  statusLabel: string;
  statusDetail: string;
  basicPrimaryTone: ProviderProbeTone;
  basicPrimaryDetail: string;
  basicSecondaryTone: ProviderProbeTone;
  basicSecondaryDetail: string;
  realTone: ProviderProbeTone;
  realDetail: string;
  latencyMS: number;
  availability24h: number;
  observed24h: boolean;
  sampleSource: ProviderMonitorSampleSource;
  quota: ProviderQuotaSummary;
  qualityScore: number;
  trend: ProviderTrendTone[];
};

export function ProviderChannelTable({
  api,
  config,
  currentUser,
  data,
  loading,
  providers,
  query,
  summaryProviders,
  onAction,
  onCreate,
  onDelete,
  onEdit,
}: {
  api?: ApiContext;
  config: ResourceConfig<Provider>;
  currentUser: AdminUser | null;
  data: AppData;
  loading: boolean;
  providers: Provider[];
  query: string;
  summaryProviders: Provider[];
  onAction: (action: ResourceAction<Provider>, provider: Provider) => void;
  onCreate?: () => void;
  onDelete: (provider: Provider) => void;
  onEdit: (provider: Provider) => void;
}) {
  const [quotaOverrides, setQuotaOverrides] = useState<Record<string, ProviderQuotaSummary>>({});
  const [quotaRefreshing, setQuotaRefreshing] = useState<Record<string, boolean>>({});

  async function refreshQuota(resource: ProviderResource) {
    if (!api) return;
    setQuotaRefreshing((current) => ({ ...current, [resource.id]: true }));
    try {
      const resp = await adminFetch(api, `/api/admin/provider-resources/${resource.id}/quota?refresh=true`);
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("查询 Codex 套餐")));
      const quota = (await resp.json()) as OpenAIAccountQuota;
      const snapshot = data.providerMonitoring.find((item) => item.provider.id === resource.provider_id);
      if (snapshot) {
        setQuotaOverrides((current) => ({
          ...current,
          [resource.provider_id]: updateProviderQuotaSummary(
            current[resource.provider_id] ?? snapshot.quota,
            resource,
            quota,
          ),
        }));
      }
    } catch (error) {
      const snapshot = data.providerMonitoring.find((item) => item.provider.id === resource.provider_id);
      if (snapshot) {
        setQuotaOverrides((current) => ({
          ...current,
          [resource.provider_id]: updateProviderQuotaSummaryError(
            current[resource.provider_id] ?? snapshot.quota,
            resource,
            error instanceof Error ? error.message : tx("套餐查询失败"),
          ),
        }));
      }
    } finally {
      setQuotaRefreshing((current) => ({ ...current, [resource.id]: false }));
    }
  }

  if (loading && providers.length === 0) return <TableSkeleton columns={7} rows={5} />;
  if (providers.length === 0) return <ResourceEmptyState config={config} query={query} onCreate={onCreate} />;

  const summaryRows = providerMonitorRowsFromSnapshots(data, summaryProviders, quotaOverrides);
  const rowsByID = new Map(summaryRows.map((row) => [row.provider.id, row]));
  const rows = providers.map((provider) => rowsByID.get(provider.id) ?? providerMonitorRow(data, provider));
  const summary = providerMonitorSummary(summaryRows);
  return (
    <section className="provider-channel-list" aria-label={tx("Provider 可用性监控")}>
      <div className="provider-monitor-head">
        <div>
          <p className="eyebrow">Provider Availability</p>
          <h2>{tx("Provider 渠道与可用性")}</h2>
        </div>
        <div className="provider-monitor-summary" aria-label={tx("Provider 健康摘要")}>
          <span><strong>{summary.healthy}</strong>{tx("正常")}</span>
          <span><strong>{summary.degraded}</strong>{tx("降级")}</span>
          <span><strong>{summary.down}</strong>{tx("故障")}</span>
          <span><strong>{summary.unknown}</strong>{tx("待观测")}</span>
        </div>
      </div>
      <div className="provider-monitor-table-wrap">
        <table className="provider-channel-table">
          <thead>
            <tr>
              <th>{tx("服务商 / 通道")}</th>
              <th>{tx("健康与基础监控")}</th>
              <th>{tx("模型、路由与账号")}</th>
              <th>{tx("真实监控 · L3")}</th>
              <th>{tx("性能与质量")}</th>
              <th>{tx("Codex 套餐")}</th>
              <th>{tx("操作")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const routeSummary = tx(providerRouteSummary(row.provider, data));
              const accountDetail = providerChannelAccountDetail(row.resources);
              const importedModelCount = data.providerModels.filter((model) => model.provider_id === row.provider.id).length;
              return <tr key={row.provider.id}>
                <td>
                  <div className="provider-monitor-name">
                    <span className={`provider-monitor-avatar ${row.statusTone}`}>{providerDisplayName(row.provider, row.resources).slice(0, 1).toUpperCase()}</span>
                    <div>
                      <strong>{providerDisplayName(row.provider, row.resources)}</strong>
                      <span title={providerDisplayBaseURL(row.provider, row.resources)}>
                        {providerTypeLabel(providerDisplayType(row.provider, row.resources))} · {providerDisplayBaseURL(row.provider, row.resources)}
                      </span>
                    </div>
                  </div>
                </td>
                <td>
                  <div className="provider-monitor-status-cell">
                    <span className={`provider-monitor-status ${row.statusTone}`}>
                      <i />
                      {tx(row.statusLabel)}
                    </span>
                    <small>{row.statusDetail}</small>
                    <ProviderProbeLine tone={row.basicPrimaryTone} detail={row.basicPrimaryDetail} />
                  </div>
                </td>
                <td>
                  <div className="provider-channel-routing">
                    <strong>{importedModelCount} {tx("个已引入模型")}</strong>
                    <span title={routeSummary}>{routeSummary}</span>
                    <span title={accountDetail || undefined}>
                      {row.resources.length || 0} {tx("账号资源")}{accountDetail ? ` · ${accountDetail}` : ""} · P{formatNumber(row.provider.priority)}
                    </span>
                    <ProviderProbeLine tone={row.basicSecondaryTone} detail={row.basicSecondaryDetail} />
                  </div>
                </td>
                <td>
                  <ProviderProbeLine tone={row.realTone} detail={row.realDetail} />
                  <small className="provider-monitor-subtle">
                    {tx(row.observed24h ? "后端统一观测快照" : "等待真实测试或网关请求")}
                  </small>
                </td>
                <td>
                  <div className="provider-channel-performance">
                    <div>
                      <span>{tx(providerLatencyLabel)}<strong>{latencyDisplay(row.latencyMS)}</strong></span>
                      <span>{tx("24H 可用率")}<strong>{row.observed24h ? providerPercent(row.availability24h) : "-"}</strong></span>
                    </div>
                    <div className="provider-channel-quality">
                      <div className="provider-quality-score">
                        <small className="provider-monitor-subtle">{tx(providerQualityScoreLabel)}</small>
                        <strong>{row.qualityScore}</strong>
                        <span><i style={{ width: `${row.qualityScore}%` }} /></span>
                      </div>
                      <div className="provider-trend-bars" aria-label={tx("近30天趋势")}>
                        {row.trend.map((tone, index) => <span className={tone} key={`${row.provider.id}-trend-${index}`} />)}
                      </div>
                    </div>
                    <small className="provider-monitor-subtle">{tx(providerPerformanceExplanation)}</small>
                  </div>
                </td>
                <td><ProviderCodexQuota quota={row.quota} refreshing={quotaRefreshing} resources={row.resources} onRefresh={refreshQuota} /></td>
                <td>
                  <div className="row-actions provider-channel-actions">
                    {(config.actions ?? [])
                      .filter((action) => action.visible?.(row.provider) ?? true)
                      .map((action) => (
                        <button
                          className="text-button"
                          key={action.label}
                          onClick={() => onAction(action, row.provider)}
                          title={tx(action.title ?? action.label)}
                          type="button"
                        >
                          {tx(action.label)}
                        </button>
                      ))}
                    {config.update ? (
                      <button className="text-button" onClick={() => onEdit(row.provider)} type="button">
                        {tx("编辑")}
                      </button>
                    ) : null}
                    {config.remove && (config.canRemove?.(row.provider, currentUser) ?? true) ? (
                      <button className="danger-button" onClick={() => onDelete(row.provider)} title={tx("删除")} type="button">
                        <Trash2 size={15} />
                      </button>
                    ) : null}
                  </div>
                </td>
              </tr>;
            })}
          </tbody>
        </table>
      </div>
      <div className="provider-monitor-legend">
        <span><i className="success" />{tx("正常")}</span>
        <span><i className="warning" />{tx("降级/慢响应")}</span>
        <span><i className="failure" />{tx("故障")}</span>
      </div>
    </section>
  );
}

export function ProviderCodexQuota({
  quota,
  refreshing,
  resources,
  onRefresh,
}: {
  quota: ProviderQuotaSummary;
  refreshing: Record<string, boolean>;
  resources: ProviderResource[];
  onRefresh: (resource: ProviderResource) => void;
}) {
  const [popoverPosition, setPopoverPosition] = useState<{ left: number; top: number }>();
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const showPopover = (element: HTMLElement) => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
    const rect = element.getBoundingClientRect();
    setPopoverPosition({
      left: Math.min(rect.left, window.innerWidth - 336),
      top: rect.bottom + 8,
    });
  };
  const keepPopover = () => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
  };
  const schedulePopoverClose = () => {
    closeTimer.current = setTimeout(() => setPopoverPosition(undefined), 120);
  };

  useEffect(() => () => {
    if (closeTimer.current) clearTimeout(closeTimer.current);
  }, []);

  if (!quota.supported) return <span className="provider-codex-quota na">-</span>;
  const accounts = quota.accounts ?? [];
  if (accounts.length === 0 || quota.successful_accounts === 0) {
    const error = accounts.find((account) => account.error_code)?.error_code;
    return <span className="provider-codex-quota error" title={error}>{tx("查询失败")}</span>;
  }
  const remaining = quota.remaining_percent ?? 100;
  const plan = quota.plan_type || "-";
  const limited = quota.limit_reached;
  return (
    <div className="provider-codex-quota-wrap" onMouseLeave={schedulePopoverClose}>
      <button
        className={`provider-codex-quota ${limited ? "limited" : "available"}`}
        onBlur={schedulePopoverClose}
        onFocus={(event) => showPopover(event.currentTarget)}
        onMouseEnter={(event) => showPopover(event.currentTarget)}
        type="button"
      >
        <strong>{formatQuotaPercent(remaining)}%</strong>
        <span>{plan} · {quotaSummaryResetLabel(quota.earliest_reset_at)}</span>
        <small>{accounts.length} {tx("个账号，显示最低余量")}</small>
      </button>
      {popoverPosition && typeof document !== "undefined" ? createPortal(
        <div
          className="provider-codex-accounts-popover"
          onFocus={keepPopover}
          onMouseEnter={keepPopover}
          onMouseLeave={schedulePopoverClose}
          role="tooltip"
          style={popoverPosition}
        >
          {accounts.map((account) => {
            const resource = resources.find((item) => item.id === account.resource_id);
            const accountQuota = account.quota;
            const accountLabel = resource?.credential_summary?.account_email || resource?.credential_summary?.account_id || account.resource_name;
            const accountPlan = accountQuota?.plan_type || resource?.credential_summary?.plan_type || "-";
            const accountLimited = accountQuota?.rate_limit?.limit_reached || accountQuota?.rate_limit?.allowed === false;
            return (
              <div className="provider-codex-account" key={account.resource_id}>
                <div>
                  <strong title={accountLabel}>{accountLabel}</strong>
                  {accountQuota ? (
                    <span className={accountLimited ? "limited" : ""}>
                      {accountPlan} · {formatQuotaPercent(quotaRemainingPercent(accountQuota))}% · {quotaResetLabel(accountQuota.rate_limit?.primary_window)}
                    </span>
                  ) : (
                    <span className="limited" title={account.error_code}>
                      {refreshing[account.resource_id] ? tx("查询中") : account.error_code || tx("查询失败")}
                    </span>
                  )}
                </div>
                <button
                  aria-label={`${tx("刷新额度")} ${accountLabel}`}
                  disabled={!resource || refreshing[account.resource_id]}
                  onClick={() => resource && onRefresh(resource)}
                  type="button"
                >
                  <RefreshCw size={13} />
                </button>
              </div>
            );
          })}
        </div>,
        document.body,
      ) : null}
    </div>
  );
}

function quotaRemainingPercent(quota: OpenAIAccountQuota) {
  const used = quota.rate_limit?.primary_window?.used_percent;
  if (!Number.isFinite(used)) return quota.rate_limit?.allowed === false ? 0 : 100;
  return clampNumber(100 - Number(used), 0, 100);
}

function quotaResetLabel(window?: OpenAIQuotaWindow) {
  if (!window) return tx("无重置信息");
  if (window.reset_at > 0) {
    return new Date(window.reset_at * 1000).toLocaleString(languageLocale(), { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
  }
  if (window.reset_after_seconds > 0) {
    const hours = Math.ceil(window.reset_after_seconds / 3600);
    return hours > 1 ? `${hours} ${tx("小时后重置")}` : tx("1 小时内重置");
  }
  return tx("即将重置");
}

function quotaSummaryResetLabel(resetAt?: number) {
  if (!resetAt) return tx("无重置信息");
  return new Date(resetAt * 1000).toLocaleString(languageLocale(), { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

function formatQuotaPercent(value: number) {
  return String(Math.round(value * 10) / 10);
}

function updateProviderQuotaSummary(summary: ProviderQuotaSummary, resource: ProviderResource, quota: OpenAIAccountQuota): ProviderQuotaSummary {
  const accounts = (summary.accounts ?? []).filter((account) => account.resource_id !== resource.id);
  accounts.push({ resource_id: resource.id, resource_name: resource.name, quota });
  return recalculateProviderQuotaSummary({ ...summary, accounts });
}

function updateProviderQuotaSummaryError(summary: ProviderQuotaSummary, resource: ProviderResource, errorCode: string): ProviderQuotaSummary {
  const accounts = (summary.accounts ?? []).filter((account) => account.resource_id !== resource.id);
  accounts.push({ resource_id: resource.id, resource_name: resource.name, error_code: errorCode });
  return recalculateProviderQuotaSummary({ ...summary, accounts });
}

function recalculateProviderQuotaSummary(summary: ProviderQuotaSummary): ProviderQuotaSummary {
  const successful = (summary.accounts ?? []).filter((account) => account.quota);
  const limiting = successful.reduce<typeof successful[number] | undefined>((lowest, current) => {
    if (!lowest) return current;
    return quotaRemainingPercent(current.quota!) < quotaRemainingPercent(lowest.quota!) ? current : lowest;
  }, undefined);
  return {
    ...summary,
    successful_accounts: successful.length,
    failed_accounts: (summary.accounts?.length ?? 0) - successful.length,
    remaining_percent: limiting?.quota ? quotaRemainingPercent(limiting.quota) : undefined,
    limit_reached: successful.some((account) => account.quota?.rate_limit?.limit_reached || account.quota?.rate_limit?.allowed === false),
    plan_type: limiting?.quota?.plan_type,
  };
}

export function ProviderProbeLine({ tone, detail }: { tone: ProviderProbeTone; detail: string }) {
  return (
    <span className={`provider-probe-line ${tone}`}>
      <i />
      {tx(providerProbeLabel(tone))}
      <small>{detail}</small>
    </span>
  );
}

export function providerMonitorRowsFromSnapshots(
  data: AppData,
  providers: Provider[],
  quotaOverrides: Record<string, ProviderQuotaSummary> = {},
): ProviderMonitorRow[] {
  const snapshots = new Map(data.providerMonitoring.map((snapshot) => [snapshot.provider.id, snapshot]));
  return providers
    .map((provider) => {
      const snapshot = snapshots.get(provider.id);
      if (!snapshot) return providerMonitorRow(data, provider);
      return {
        ...providerMonitorRowFromSnapshot(data, snapshot, quotaOverrides[provider.id]),
        provider,
      };
    })
    .sort((left, right) => (left.provider.priority - right.provider.priority) || left.provider.name.localeCompare(right.provider.name));
}

export function providerChannelAccountDetail(resources: ProviderResource[]) {
  const accounts = resources.filter((resource) => resource.resource_type === "openai_subscription");
  if (accounts.length === 0) return "";
  const active = accounts.filter((resource) => resource.status === "active" && resource.healthy).length;
  const first = accounts[0];
  const label = first.credential_summary?.account_email || first.credential_summary?.account_id || first.name || tx("OpenAI 账号资源");
  return `${active}/${accounts.length} ${tx("启用")} · ${label}`;
}

function providerMonitorRowFromSnapshot(data: AppData, snapshot: ProviderMonitoringSnapshot, quotaOverride?: ProviderQuotaSummary): ProviderMonitorRow {
  const resources = data.providerResources.filter((resource) => resource.provider_id === snapshot.provider.id);
  const observedSignal = snapshot.gateway.samples > 0 ? snapshot.gateway : snapshot.active_probe;
  const observed = observedSignal.samples > 0;
  return {
    provider: snapshot.provider,
    resources,
    routeCount: snapshot.route_count,
    activeRouteCount: snapshot.active_route_count,
    statusTone: snapshot.state,
    statusLabel: snapshot.status_label,
    statusDetail: monitoringDetail(snapshot.status_detail),
    basicPrimaryTone: monitoringProbeTone(snapshot.configuration.state),
    basicPrimaryDetail: monitoringDetail(snapshot.configuration.detail),
    basicSecondaryTone: monitoringProbeTone(snapshot.resources.state),
    basicSecondaryDetail: `${snapshot.healthy_resource_count}/${snapshot.active_resource_count} ${tx("资源健康")}`,
    realTone: monitoringProbeTone(observedSignal.state),
    realDetail: observed
      ? `${providerPercent(observedSignal.success_rate ?? 0)} · ${providerObservationCount(observedSignal.samples, observedSignal.source === "active_probe")}`
      : tx("等待真实测试或网关请求"),
    latencyMS: observedSignal.latency_ms ?? 0,
    availability24h: observedSignal.success_rate ?? 0,
    observed24h: observed,
    sampleSource: observedSignal.source === "active_probe" ? "codex_test" : "gateway_request",
    quota: quotaOverride ?? snapshot.quota,
    qualityScore: snapshot.quality_score,
    trend: snapshot.trend,
  };
}

function monitoringProbeTone(state: ProviderMonitoringSnapshot["configuration"]["state"]): ProviderProbeTone {
  if (state === "healthy") return "ok";
  if (state === "degraded") return "warn";
  if (state === "down") return "down";
  return "na";
}

function monitoringDetail(value?: string) {
  if (!value) return "-";
  const [source, detail] = value.split(":", 2);
  const labels: Record<string, string> = {
    configuration: tx("配置"),
    active_probe: tx("主动测试"),
    gateway_request: tx("真实请求"),
    provider_online: tx("Provider 在线"),
    resources_healthy: tx("资源健康"),
    no_active_resources: tx("未配置账号资源"),
    no_healthy_resources: tx("无健康资源"),
    some_resources_unhealthy: tx("部分资源异常"),
    awaiting_observation: tx("等待测试或真实请求"),
    ok: "ok",
  };
  if (detail) return `${labels[source] ?? source} · ${labels[detail] ?? detail}`;
  return labels[value] ?? value;
}

export function providerMonitorRows(data: AppData, providers: Provider[]): ProviderMonitorRow[] {
  return providers
    .slice()
    .sort((left, right) => (left.priority - right.priority) || left.name.localeCompare(right.name))
    .map((provider) => providerMonitorRow(data, provider));
}

export function providerMonitorRow(data: AppData, provider: Provider): ProviderMonitorRow {
  const resources = data.providerResources.filter((resource) => resource.provider_id === provider.id);
  const routes = providerRoutesFor(provider, data);
  const logs = providerLogsFor(data, provider, resources);
  const { source: sampleSource, samples } = providerMonitorSamples(data, provider, resources);
  const now = Date.now();
  const recent24h = samples.filter((sample) => now - safeTime(sample.created_at) <= 24 * 60 * 60 * 1000);
  const success24h = recent24h.filter((sample) => sample.success);
  const warning24h = recent24h.filter((sample) => sample.success && sample.latency_ms >= 5000);
  const failed24h = recent24h.length - success24h.length;
  const observed24h = recent24h.length > 0;
  const activeResources = resources.filter((resource) => resource.status === "active");
  const healthyResources = activeResources.filter((resource) => resource.healthy);
  const healthyProvider = provider.status === "active" && provider.healthy;
  const resourceScore = activeResources.length > 0 ? (healthyResources.length / activeResources.length) * 100 : (healthyProvider ? 100 : 0);
  const availability24h = observed24h ? (success24h.length / recent24h.length) * 100 : (healthyProvider ? 100 : 0);
  const latencySamples = (success24h.length ? success24h : samples.filter((sample) => sample.success)).filter((sample) => sample.latency_ms > 0);
  const latencyMS = percentileLatency(latencySamples, 0.5);
  const statusTone = providerMonitorTone(provider, observed24h, availability24h, warning24h.length, failed24h, activeResources.length, healthyResources.length);
  const activeRouteCount = routes.filter((route) => route.status === "active").length;
  return {
    provider,
    resources,
    routeCount: routes.length,
    activeRouteCount,
    statusTone,
    statusLabel: providerStatusLabel(statusTone),
    statusDetail: sampleSource === "codex_test"
      ? providerCodexTestStatusDetail(samples, resources)
      : providerStatusDetail(provider, logs, resources),
    basicPrimaryTone: healthyProvider ? "ok" : "down",
    basicPrimaryDetail: provider.status === "active" ? tx("Provider 在线") : enumValueLabel(provider.status),
    basicSecondaryTone: providerResourceProbeTone(activeResources.length, healthyResources.length),
    basicSecondaryDetail: activeResources.length > 0
      ? `${formatNumber(healthyResources.length)}/${formatNumber(activeResources.length)} ${tx("资源健康")}`
      : tx("未配置账号资源"),
    realTone: providerRealProbeTone(observed24h, availability24h, warning24h.length, failed24h),
    realDetail: observed24h
      ? `${providerPercent(availability24h)} · ${providerObservationCount(recent24h.length, sampleSource === "codex_test")}`
      : tx(sampleSource === "codex_test" ? "无 Codex 测试" : "无真实请求"),
    latencyMS,
    availability24h,
    observed24h,
    sampleSource,
    quota: { supported: false, limit_reached: false, successful_accounts: 0, failed_accounts: 0 },
    qualityScore: providerQualityScore(availability24h, latencyMS, resourceScore, observed24h, healthyProvider),
    trend: providerTrend(samples),
  };
}

function providerObservationCount(count: number, test: boolean) {
  return test
    ? countWithUnit(count, "次测试", "test", "回のテスト")
    : countWithUnit(count, "次请求", "request", "件のリクエスト");
}

export function providerMonitorSamples(data: AppData, provider: Provider, resources: ProviderResource[]): { source: ProviderMonitorSampleSource; samples: ProviderMonitorSample[] } {
  const codexResources = resources.filter((resource) => resource.resource_type === "openai_subscription");
  if (codexResources.length > 0) {
    return { source: "codex_test", samples: providerCodexTestSamples(data.auditEvents, codexResources) };
  }
  const samples = providerLogsFor(data, provider, resources).map((log) => ({
    created_at: log.created_at,
    success: !requestLogFailed(log),
    latency_ms: log.latency_ms,
    error_code: log.error_code,
  }));
  return { source: "gateway_request", samples };
}

export function providerCodexTestSamples(events: AuditEvent[], resources: ProviderResource[]): ProviderMonitorSample[] {
  const resourceIDs = new Set(resources.map((resource) => resource.id));
  return events
    .filter((event) => event.action === "test" && event.resource_type === "provider_resource" && resourceIDs.has(event.resource_id))
    .map((event) => {
      const snapshot = auditSnapshot(event.after_snapshot);
      return {
        created_at: event.created_at,
        success: event.status === "success" && snapshot.healthy !== false,
        latency_ms: finiteNumber(snapshot.latency_ms),
        error_code: stringValue(snapshot.error_code) || event.message,
      };
    })
    .sort((left, right) => safeTime(left.created_at) - safeTime(right.created_at));
}

export function providerCodexTestStatusDetail(samples: ProviderMonitorSample[], resources: ProviderResource[]) {
  const latest = samples[samples.length - 1];
  if (latest?.error_code) return `${timeLabel(latest.created_at)} · ${latest.error_code}`;
  if (latest) return timeLabel(latest.created_at);
  const latestResourceCheck = resources
    .map((resource) => resource.last_checked_at || resource.updated_at || "")
    .filter(Boolean)
    .sort((left, right) => safeTime(right) - safeTime(left))[0];
  return latestResourceCheck ? timeLabel(latestResourceCheck) : tx("等待 Codex 测试");
}

export function auditSnapshot(value: string | undefined): Record<string, unknown> {
  if (!value) return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

export function finiteNumber(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function stringValue(value: unknown) {
  return typeof value === "string" ? value : "";
}

export function providerMonitorSummary(rows: ProviderMonitorRow[]) {
  return rows.reduce(
    (summary, row) => {
      summary[row.statusTone] += 1;
      return summary;
    },
    { healthy: 0, degraded: 0, down: 0, unknown: 0 } as Record<ProviderMonitorTone, number>,
  );
}

export function providerLogsFor(data: AppData, provider: Provider, resources: ProviderResource[]) {
  const resourceIDs = new Set(resources.map((resource) => resource.id));
  return data.logs
    .filter((log) => log.provider_id === provider.id || (log.provider_resource_id ? resourceIDs.has(log.provider_resource_id) : false))
    .sort((left, right) => safeTime(left.created_at) - safeTime(right.created_at));
}

export function providerMonitorTone(provider: Provider, observed: boolean, availability: number, warnings: number, failures: number, activeResources: number, healthyResources: number): ProviderMonitorTone {
  if (provider.status !== "active" || !provider.healthy) return "down";
  if (observed && (availability < 90 || failures > 0 && availability < 95)) return "down";
  if (activeResources > 0 && healthyResources === 0) return "down";
  if ((observed && availability < 99) || warnings > 0 || (activeResources > 0 && healthyResources < activeResources)) return "degraded";
  return "healthy";
}

export function providerStatusLabel(tone: ProviderMonitorTone) {
  if (tone === "healthy") return "Healthy";
  if (tone === "degraded") return "Degraded";
  if (tone === "unknown") return "Awaiting Test";
  return "Functional Down";
}

export function providerStatusDetail(provider: Provider, logs: RequestLog[], resources: ProviderResource[]) {
  const latestLog = logs.slice().sort((left, right) => safeTime(right.created_at) - safeTime(left.created_at))[0];
  if (latestLog?.error_code) return `${timeLabel(latestLog.created_at)} · ${latestLog.error_code}`;
  if (latestLog) return timeLabel(latestLog.created_at);
  const latestResourceCheck = resources
    .map((resource) => resource.last_checked_at || resource.updated_at || "")
    .filter(Boolean)
    .sort((left, right) => safeTime(right) - safeTime(left))[0];
  if (latestResourceCheck) return timeLabel(latestResourceCheck);
  return enumValueLabel(provider.status);
}

export function providerResourceProbeTone(total: number, healthy: number): ProviderProbeTone {
  if (total === 0) return "na";
  if (healthy === total) return "ok";
  if (healthy > 0) return "warn";
  return "down";
}

export function providerRealProbeTone(observed: boolean, availability: number, warnings: number, failures: number): ProviderProbeTone {
  if (!observed) return "na";
  if (availability < 90 || failures > 0 && availability < 95) return "down";
  if (availability < 99 || warnings > 0 || failures > 0) return "warn";
  return "ok";
}

export function providerProbeLabel(tone: ProviderProbeTone) {
  if (tone === "ok") return "ok";
  if (tone === "warn") return "warn";
  if (tone === "down") return "down";
  return "na";
}

export function percentileLatency(logs: Array<{ latency_ms: number }>, percentile: number) {
  const values = logs.map((log) => log.latency_ms || 0).filter((value) => value > 0).sort((left, right) => left - right);
  if (values.length === 0) return 0;
  const index = Math.min(values.length - 1, Math.max(0, Math.floor((values.length - 1) * percentile)));
  return values[index];
}

export function providerQualityScore(availability: number, latencyMS: number, resourceScore: number, observed: boolean, healthyProvider: boolean) {
  const availabilityScore = observed ? availability : (healthyProvider ? 95 : 20);
  const latencyScore = latencyMS === 0
    ? (healthyProvider ? 86 : 25)
    : latencyMS <= 250
      ? 100
      : latencyMS <= 800
        ? 94
        : latencyMS <= 1800
          ? 84
          : latencyMS <= 3500
            ? 68
            : latencyMS <= 6000
              ? 48
              : 30;
  return Math.round(clampNumber(availabilityScore * 0.62 + latencyScore * 0.24 + resourceScore * 0.14, 0, 100));
}

export function providerTrend(samples: ProviderMonitorSample[]) {
  const days = 30;
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  return Array.from({ length: days }, (_, index) => {
    const dayStart = today - (days - 1 - index) * 24 * 60 * 60 * 1000;
    const dayEnd = dayStart + 24 * 60 * 60 * 1000;
    const daySamples = samples.filter((sample) => {
      const time = safeTime(sample.created_at);
      return time >= dayStart && time < dayEnd;
    });
    if (daySamples.length === 0) return "none" as ProviderTrendTone;
    const failures = daySamples.filter((sample) => !sample.success).length;
    const slow = daySamples.filter((sample) => sample.success && sample.latency_ms >= 5000).length;
    const availability = ((daySamples.length - failures) / daySamples.length) * 100;
    if (availability < 90) return "failure" as ProviderTrendTone;
    if (failures > 0 || slow > 0 || availability < 99) return "warning" as ProviderTrendTone;
    return "success" as ProviderTrendTone;
  });
}

export function providerInitial(provider: Provider) {
  return (provider.name || provider.type || provider.id || "P").trim().slice(0, 1).toUpperCase();
}

export function providerPercent(value: number) {
  return `${clampNumber(value, 0, 100).toFixed(1)}%`;
}

export function safeTime(value: string | undefined) {
  if (!value) return 0;
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : 0;
}

export function timeLabel(value: string | undefined) {
  const time = safeTime(value);
  if (!time) return "-";
  return new Intl.DateTimeFormat(languageLocale(), { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(new Date(time));
}

export function clampNumber(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, Number.isFinite(value) ? value : min));
}

export function TeamMembersPanel({ data, team, onClose }: { data: AppData; team: AdminResource; onClose: () => void }) {
  const users = data.users
    .filter((user) => user.team_id === team.id)
    .sort((left, right) => (left.name || left.username).localeCompare(right.name || right.username));
  return (
    <div className="team-members-panel">
      <div className="team-members-head">
        <div>
          <span>{tx("团队用户")}</span>
          <strong>{team.name || team.id}</strong>
        </div>
        <span>{countWithUnit(users.length, "人", "member", "人")}</span>
        <button className="icon-button subtle" onClick={onClose} type="button" title={tx("关闭成员列表")}>
          <X size={15} />
        </button>
      </div>
      <SimpleTable
        columns={["姓名", "邮箱", "用户名", "角色", "状态", "最近登录"]}
        rows={users.map((user) => [
          user.name || "-",
          user.email || "-",
          user.username || "-",
          roleLabel(user.role),
          <StatusPill key={user.id} status={user.status} />,
          formatTime(user.last_login_at ?? ""),
        ])}
      />
    </div>
  );
}

export function ProjectMemberRow({
  data,
  member,
  onEdit,
  onDelete,
}: {
  data: AppData;
  member: AdminResource;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const userID = stringifyValue(member.fields?.user_id);
  const user = data.users.find((item) => item.id === userID);
  const title = user ? user.name || user.username : userID || "-";
  const subtitle = user ? [user.email, user.username].filter(Boolean).join(" / ") : userID;
  return (
    <div className="project-member-row">
      <div className="project-member-user">
        <span className="project-member-avatar"><UserRoundCheck size={16} /></span>
        <div>
          <strong>{title}</strong>
          <span>{subtitle || "-"}</span>
        </div>
      </div>
      <div className="project-member-actions">
        <button className="text-button" onClick={onEdit} type="button">{tx("编辑")}</button>
        <button className="danger-button" onClick={onDelete} type="button" title={tx("删除")}>
          <Trash2 size={15} />
        </button>
      </div>
    </div>
  );
}

export function ReportsView({
  config,
  data,
  history,
  loading,
  onCreate,
  onEdit,
  onDelete,
  onAction,
  onExport,
}: {
  config: ResourceConfig<AdminResource>;
  data: AppData;
  history: ReportExportHistoryItem[];
  loading: boolean;
  onCreate: () => void;
  onEdit: (item: AdminResource) => void;
  onDelete: (item: AdminResource) => void;
  onAction: (action: ResourceAction<AdminResource>, item: AdminResource) => void;
  onExport: (dataset: string) => void;
}) {
  const savedReports = config.list(data);
  const exports = reportExportDefinitions();
  return (
    <div className="reports-center">
      <div className="reports-export-head">
        <div>
          <h2>{tx("按需导出")}</h2>
          <span>CSV</span>
        </div>
      </div>
      <div className="reports-export-grid">
        {exports.map((item) => {
          const Icon = item.icon;
          return (
            <button
              className={`report-export-card ${item.tone}`}
              disabled={loading}
              key={item.dataset}
              onClick={() => onExport(item.dataset)}
              title={`${tx("导出")} ${item.label}`}
              type="button"
            >
              <span className="report-export-icon">
                <Icon size={18} />
              </span>
              <span className="report-export-copy">
                <strong>{item.label}</strong>
                <span>{tx(item.description)}</span>
              </span>
              <em>CSV</em>
            </button>
          );
        })}
      </div>

      {history.length > 0 ? (
        <DataSection title="最近导出">
          <SimpleTable
            columns={["数据集", "文件", "时间", "账期"]}
            rows={history.map((item) => [
              reportDatasetLabel(item.dataset),
              item.file_name,
              formatTime(item.exported_at),
              item.period || "-",
            ])}
          />
        </DataSection>
      ) : null}

      {savedReports.length > 0 ? (
        <DataSection title="自动导出配置">
          <div className="reports-config-toolbar">
            <button className="button" onClick={onCreate} type="button">
              <Plus size={16} />
              {tx("新增配置")}
            </button>
          </div>
          <EntityTable
            config={config}
            data={data}
            items={savedReports}
            onEdit={onEdit}
            onDelete={onDelete}
            onAction={onAction}
          />
        </DataSection>
      ) : null}
    </div>
  );
}
