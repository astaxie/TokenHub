import { Activity, BarChart3, Boxes, Check, CircleDollarSign, Code2, Database, FileText, Gauge, KeyRound, LayoutDashboard, Server, ShieldCheck, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { appRole, canAccessView } from "../core/navigation";
import { type AdminUser, type AppData, type AppRole, type RequestLog, type Summary, type UsageBreakdownRow, type UsagePoint, type ViewKey } from "../core/types";
import { findProvider, providerResourceAuditLabel } from "../domain/entities";
import { compactNumber, formatDashboardMoney, formatMoney, formatNumber, playgroundModels } from "../domain/formatting";
import { countWithUnit, languageLocale, tx } from "../i18n/runtime";
import { hasUsage } from "./usage-billing";

export function OverviewView({
  data,
  user,
  onSelectView,
}: {
  data: AppData;
  user: AdminUser;
  onSelectView: (view: ViewKey) => void;
}) {
  const [range, setRange] = useState<OverviewRangeKey>("7d");
  const [chartMetric, setChartMetric] = useState<OverviewMetricKey>("requests");
  const role = appRole(user.role);
  const apiKeyCount = data.summary.api_key_count ?? data.keys.length;
  const can = (view: ViewKey) => canAccessView(user, view);
  const activeProviders = data.providers.filter((provider) => provider.status === "active" && provider.healthy).length;
  const providerTotal = data.providers.length;
  const series = overviewRangePoints(data, range);
  const requestValues = series.map((point) => point.request_count);
  const tokenValues = series.map((point) => point.total_tokens);
  const costValues = series.map((point) => point.estimated_cost_usd);
  const providerRows = overviewProviderShareRows(data);
  const topModels = overviewTopModelRows(data);
  const cards = [
    {
      label: "总请求",
      value: formatNumber(data.summary.request_count),
      icon: BarChart3,
      delta: overviewDeltaLabel(requestValues),
      values: requestValues,
    },
    {
      label: "总 Token",
      value: compactNumber(data.summary.total_tokens),
      icon: Database,
      delta: overviewDeltaLabel(tokenValues),
      values: tokenValues,
    },
    {
      label: "总成本",
      value: `$${formatMoney(data.summary.estimated_cost_usd)}`,
      icon: CircleDollarSign,
      delta: overviewDeltaLabel(costValues),
      values: costValues,
    },
    can("providers")
      ? {
          label: "Provider",
          value: `${formatNumber(activeProviders)} / ${formatNumber(providerTotal)}`,
          icon: Server,
          badge: `${tx("在线")} ${formatNumber(activeProviders)}`,
          caption: "全部健康 · 延迟 312ms",
          values: series.map(() => activeProviders),
        }
      : {
          label: "API Key",
          value: formatNumber(apiKeyCount),
          icon: KeyRound,
          badge: "已发放",
          caption: "内部调用凭证",
          values: series.map(() => apiKeyCount),
        },
  ].filter(Boolean);
  const chartValue = overviewMetricValue(series, chartMetric);

  if (role === "user" || role === "team_leader") {
    return (
      <div className="overview-report role-usage-overview">
        <RoleUsageMonitorDashboard data={data} user={user} onSelectView={onSelectView} />
        <OverviewRoleWorkbench data={data} user={user} onSelectView={onSelectView} />
      </div>
    );
  }

  return (
    <div className="overview-report">
      <header className="overview-report-head">
        <div>
          <p className="eyebrow">Enterprise AI Gateway</p>
          <h1>{tx("网关概览")}</h1>
        </div>
        <div className="overview-range-tabs" role="tablist" aria-label={tx("报表时间范围")}>
          {overviewRangeTabs.map((item) => (
            <button
              className={range === item.key ? "active" : ""}
              key={item.key}
              onClick={() => setRange(item.key)}
              type="button"
            >
              {tx(item.label)}
            </button>
          ))}
        </div>
      </header>

      <OverviewRoleWorkbench data={data} user={user} onSelectView={onSelectView} />

      <section className="metrics overview-metrics">
        {cards.map((card) => {
          const Icon = card.icon;
          return (
            <OverviewMetricCard
              badge={"badge" in card ? card.badge : card.delta}
              caption={"caption" in card ? card.caption : undefined}
              icon={Icon}
              key={card.label}
              label={card.label}
              value={card.value}
              values={card.values}
            />
          );
        })}
      </section>

      <section className="overview-report-grid">
        <article className="overview-panel overview-trend-panel">
          <div className="overview-panel-head">
            <div>
              <h2>{tx("成本与用量趋势")}</h2>
              <p>
                <strong>{chartValue.value}</strong>
                <span>{chartValue.delta}</span>
                <em>· {overviewRangeLabel(range)}</em>
              </p>
            </div>
            <div className="overview-metric-tabs" role="tablist" aria-label={tx("趋势指标")}>
              {overviewMetricTabs.map((item) => (
                <button
                  className={chartMetric === item.key ? "active" : ""}
                  key={item.key}
                  onClick={() => setChartMetric(item.key)}
                  type="button"
                >
                  {tx(item.label)}
                </button>
              ))}
            </div>
          </div>
          <OverviewTrendChart metric={chartMetric} points={series} />
        </article>

        <aside className="overview-side-stack">
          <OverviewProviderShare rows={providerRows} />
          <OverviewTopModels rows={topModels} />
        </aside>
      </section>
    </div>
  );
}

export function RoleUsageMonitorDashboard({
  data,
  user,
  onSelectView,
}: {
  data: AppData;
  user: AdminUser;
  onSelectView: (view: ViewKey) => void;
}) {
  const role = appRole(user.role);
  const stats = roleUsageMonitorStats(data);
  const modelCostRows = usageDashboardCostRows(data.breakdown.models ?? [], (row) => modelDisplayName(data, row.id)).slice(0, 5);
  const accountRows = usageDashboardAccountRows(data).slice(0, 5);
  const failures = data.logs
    .filter((log) => requestLogFailed(log))
    .sort((left, right) => right.created_at.localeCompare(left.created_at))
    .slice(0, 5);
  const generatedAt = new Intl.DateTimeFormat(languageLocale(), {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date());
  const scopeLabel = role === "team_leader" ? "团队和项目范围" : "个人可见范围";

  return (
    <section className="usage-monitor-dashboard">
      <header className="usage-monitor-status">
        <div className="usage-monitor-title">
          <span className={stats.successRate >= 95 ? "usage-monitor-dot ok" : stats.successRate >= 85 ? "usage-monitor-dot warn" : "usage-monitor-dot bad"} />
          <div>
            <p className="eyebrow">{role === "team_leader" ? "Team Usage Monitor" : "Personal Usage Monitor"}</p>
            <h1>{tx(role === "team_leader" ? "团队 AI 调用监控" : "我的 AI 调用监控")}</h1>
            <span>{tx(scopeLabel)} · {tx("统计时间")} {generatedAt}</span>
          </div>
        </div>
        <div className="usage-monitor-actions">
          <button className="secondary-button" onClick={() => onSelectView("usage")} type="button">
            <BarChart3 size={15} />
            {tx(role === "team_leader" ? "团队报表" : "用量统计")}
          </button>
          <button className="secondary-button" onClick={() => onSelectView("audit")} type="button">
            <FileText size={15} />
            {tx("请求日志")}
          </button>
        </div>
      </header>

      <div className="usage-monitor-kpis">
        <UsageMonitorKPI label="请求量" value={formatNumber(stats.requests)} detail={countWithUnit(stats.failedRequests, "次失败", "failed", "件失敗")} icon={BarChart3} tone="blue" />
        <UsageMonitorKPI label="成功率" value={`${stats.successRate.toFixed(stats.successRate >= 99 ? 1 : 2)}%`} detail={`${formatNumber(stats.successRequests)} / ${formatNumber(stats.requests)}`} icon={Check} tone="green" />
        <UsageMonitorKPI label="平均延迟" value={latencyDisplay(stats.avgLatencyMS)} detail={stats.zeroLatencyRequests > 0 ? countWithUnit(stats.zeroLatencyRequests, "次无延迟记录", "zero-latency", "件の遅延なし") : tx("最近请求")} icon={Gauge} tone="red" />
        <UsageMonitorKPI label="总成本" value={`$${formatDashboardMoney(stats.cost)}`} detail={`${tx("总 Token")} ${compactNumber(stats.totalTokens)}`} icon={CircleDollarSign} tone="amber" />
        <UsageMonitorKPI label="Token 消耗" value={compactNumber(stats.totalTokens)} detail={`${tx("输入")} ${compactNumber(stats.inputTokens)} / ${tx("输出")} ${compactNumber(stats.outputTokens)}`} icon={Database} tone="purple" />
      </div>

      <div className="usage-monitor-grid">
        <article className="usage-monitor-panel traffic">
          <div className="usage-monitor-panel-head">
            <div>
              <h2>{tx("调用趋势")}</h2>
              <span>{tx("请求量与 Token 消耗趋势")}</span>
            </div>
            <div className="usage-monitor-legend">
              <span><i className="calls" />{tx("请求")}</span>
              <span><i className="tokens" />Token</span>
            </div>
          </div>
          <UsageMonitorTrafficChart points={overviewRangePoints(data, "7d")} />
        </article>

        <article className="usage-monitor-panel health">
          <div className="usage-monitor-panel-head">
            <div>
              <h2>{tx("请求健康时间线")}</h2>
              <span>{tx("最近请求按成功、告警和失败聚合")}</span>
            </div>
            <strong className={stats.successRate >= 95 ? "health-rate ok" : "health-rate warn"}>{stats.successRate.toFixed(1)}%</strong>
          </div>
          <UsageHealthTimeline logs={data.logs} />
        </article>

        <article className="usage-monitor-panel token-mix">
          <div className="usage-monitor-panel-head compact">
            <div>
              <h2>{tx("Token 结构")}</h2>
              <span>{tx("输入和输出 Token 占比")}</span>
            </div>
          </div>
          <TokenMixPanel input={stats.inputTokens} output={stats.outputTokens} total={stats.totalTokens} />
        </article>
      </div>

      <div className="usage-monitor-bottom-grid">
        <UsageCostRankPanel title="模型成本排行" empty="暂无模型成本数据" rows={modelCostRows} />
        <UsageCostRankPanel title="账号成本排行" empty="暂无账号成本数据" rows={accountRows} />
        <RecentFailurePanel logs={failures} data={data} />
      </div>
    </section>
  );
}

export function UsageMonitorKPI({
  label,
  value,
  detail,
  icon: Icon,
  tone,
}: {
  label: string;
  value: string;
  detail: string;
  icon: typeof Activity;
  tone: "blue" | "green" | "red" | "amber" | "purple";
}) {
  return (
    <article className={`usage-monitor-kpi ${tone}`}>
      <span className="usage-monitor-kpi-icon"><Icon size={17} /></span>
      <div>
        <span>{tx(label)}</span>
        <strong>{value}</strong>
        <small>{detail}</small>
      </div>
    </article>
  );
}

export function UsageMonitorTrafficChart({ points }: { points: UsagePoint[] }) {
  const rows = points.slice(-10);
  const maxTokens = Math.max(...rows.map((point) => point.total_tokens), 1);
  const maxRequests = Math.max(...rows.map((point) => point.request_count), 1);
  const line = usageMonitorRequestLine(rows, maxRequests);
  return (
    <div className="usage-traffic-chart">
      <svg viewBox="0 0 720 260" role="img" aria-label={tx("调用趋势")}>
        <g className="usage-traffic-grid">
          {[0.25, 0.5, 0.75].map((tick) => <line key={tick} x1="42" x2="700" y1={220 - 178 * tick} y2={220 - 178 * tick} />)}
        </g>
        {rows.map((point, index) => {
          const x = 58 + index * (rows.length <= 1 ? 0 : 620 / (rows.length - 1));
          const height = Math.max(5, (point.total_tokens / maxTokens) * 168);
          return (
            <g key={`${point.date}-${index}`}>
              <rect className="usage-traffic-bar" x={x - 14} y={220 - height} width="28" height={height} rx="8" />
              <text x={x} y="244">{overviewDateLabel(point.date)}</text>
            </g>
          );
        })}
        <path className="usage-traffic-line" d={line} />
      </svg>
    </div>
  );
}

export function UsageHealthTimeline({ logs }: { logs: RequestLog[] }) {
  const gridRef = useRef<HTMLDivElement>(null);
  const [gridSize, setGridSize] = useState({ rows: 14, columns: 42 });
  useEffect(() => {
    const element = gridRef.current;
    if (!element) return;
    const updateGridSize = () => {
      const rect = element.getBoundingClientRect();
      const next = usageHealthGridSize(rect.width, rect.height);
      setGridSize((current) => (current.rows === next.rows && current.columns === next.columns ? current : next));
    };
    updateGridSize();
    if (typeof ResizeObserver === "undefined") {
      window.addEventListener("resize", updateGridSize);
      return () => window.removeEventListener("resize", updateGridSize);
    }
    const observer = new ResizeObserver(updateGridSize);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);

  const cells = usageHealthCells(logs, gridSize.rows * gridSize.columns);
  const rows = Array.from({ length: gridSize.rows }, (_, rowIndex) => cells.filter((_, cellIndex) => cellIndex % gridSize.rows === rowIndex));
  return (
    <div className="usage-health-timeline">
      <div className="usage-health-grid" ref={gridRef}>
        {rows.map((row, rowIndex) => (
          <div className="usage-health-row" key={`health-row-${rowIndex}`}>
            {row.map((cell, index) => <span className={`usage-health-cell ${cell}`} key={`${cell}-${rowIndex}-${index}`} />)}
          </div>
        ))}
      </div>
      <div className="usage-health-legend">
        <span><i className="none" />{tx("无请求")}</span>
        <span><i className="success" />{tx("成功")}</span>
        <span><i className="warning" />{tx("告警")}</span>
        <span><i className="failure" />{tx("失败")}</span>
      </div>
    </div>
  );
}

export function TokenMixPanel({ input, output, total }: { input: number; output: number; total: number }) {
  const safeTotal = Math.max(total, input + output, 1);
  const rows = [
    { label: "输入 Token", value: input, className: "input" },
    { label: "输出 Token", value: output, className: "output" },
  ];
  return (
    <div className="token-mix-list">
      <div className="token-mix-total">
        <span>{tx("总 Token")}</span>
        <strong>{compactNumber(total)}</strong>
      </div>
      {rows.map((row) => {
        const percent = (row.value / safeTotal) * 100;
        return (
          <div className="token-mix-row" key={row.label}>
            <div>
              <span><i className={row.className} />{tx(row.label)}</span>
              <strong>{compactNumber(row.value)} <em>{percent.toFixed(percent >= 10 ? 0 : 1)}%</em></strong>
            </div>
            <span className="token-mix-bar"><span className={row.className} style={{ width: `${Math.max(1, percent)}%` }} /></span>
          </div>
        );
      })}
    </div>
  );
}

export function UsageCostRankPanel({ title, empty, rows }: { title: string; empty: string; rows: UsageDashboardRankRow[] }) {
  const max = Math.max(...rows.map((row) => row.cost || row.total_tokens), 1);
  return (
    <article className="usage-monitor-panel rank">
      <div className="usage-monitor-panel-head compact">
        <div>
          <h2>{tx(title)}</h2>
          <span>{tx("按估算成本降序")}</span>
        </div>
      </div>
      <div className="usage-rank-list">
        {rows.length ? rows.map((row, index) => {
          const value = row.cost || row.total_tokens;
          const width = Math.max(4, (value / max) * 100);
          return (
            <div className="usage-rank-row" key={row.id || row.label}>
              <span className="usage-rank-index">{index + 1}</span>
              <div>
                <strong>{row.label}</strong>
                <small>{formatNumber(row.request_count)} {tx("次请求")} · {tx("输入")} {compactNumber(row.input_tokens)} · {tx("输出")} {compactNumber(row.output_tokens)}</small>
                <span className="usage-rank-progress"><span style={{ width: `${width}%` }} /></span>
              </div>
              <em>${formatMoney(row.cost)}</em>
            </div>
          );
        }) : (
          <div className="compact-empty">{tx(empty)}</div>
        )}
      </div>
    </article>
  );
}

export function RecentFailurePanel({ logs, data }: { logs: RequestLog[]; data: AppData }) {
  return (
    <article className="usage-monitor-panel failures">
      <div className="usage-monitor-panel-head compact">
        <div>
          <h2>{tx("最近失败请求")}</h2>
          <span>{tx("定位错误码、模型和延迟")}</span>
        </div>
      </div>
      <div className="usage-failure-list">
        {logs.length ? logs.map((log) => (
          <div className="usage-failure-row" key={log.id || log.request_id}>
            <div>
              <strong>{log.error_code || `HTTP ${log.status_code}`}</strong>
              <span>{log.model || "-"} · {providerFailureLabel(data, log)}</span>
            </div>
            <em>{latencyDisplay(log.latency_ms)}</em>
          </div>
        )) : (
          <div className="compact-empty">{tx("暂无失败请求")}</div>
        )}
      </div>
    </article>
  );
}

export type OverviewWorkbenchItem = {
  title: string;
  description: string;
  status: "ready" | "attention" | "next";
  statusLabel: string;
  target: ViewKey;
  action: string;
  icon: typeof Activity;
};

export function OverviewRoleWorkbench({
  data,
  user,
  onSelectView,
}: {
  data: AppData;
  user: AdminUser;
  onSelectView: (view: ViewKey) => void;
}) {
  const role = appRole(user.role);
  const can = (view: ViewKey) => canAccessView(user, view);
  const guideStorageKey = overviewWorkbenchStorageKey(user, role);
  const projects = data.projects.filter((project) => project.status === "active" || project.status === "");
  const activeProviders = data.providers.filter((provider) => provider.status === "active");
  const healthyProviders = activeProviders.filter((provider) => provider.healthy);
  const activeRoutes = data.summary.active_route_count || data.routes.filter((route) => route.status === "active").length;
  const apiKeys = data.summary.api_key_count || data.keys.length;
  const callableModels = playgroundModels(data).length || data.models.filter((model) => model.status === "active").length;
  const requestCount = data.summary.request_count || data.logs.length;
  const setupScore = overviewSetupScore(role, {
    projects: projects.length,
    activeProviders: activeProviders.length,
    healthyProviders: healthyProviders.length,
    activeRoutes,
    apiKeys,
    callableModels,
    requestCount,
  });
  const items = overviewWorkbenchItems(role, {
    projects: projects.length,
    activeProviders: activeProviders.length,
    healthyProviders: healthyProviders.length,
    activeRoutes,
    apiKeys,
    callableModels,
    requestCount,
  }).filter((item) => can(item.target));
  const primary = items.find((item) => item.status !== "ready") ?? items[0];
  const secondaryItems = primary ? items.filter((item) => item.title !== primary.title) : items;
  const setupComplete = setupScore.ready >= setupScore.total;
  const [guidePreference, setGuidePreference] = useState<"show" | "hide">(() => overviewWorkbenchInitialPreference(guideStorageKey, setupComplete));

  useEffect(() => {
    setGuidePreference(overviewWorkbenchInitialPreference(guideStorageKey, setupComplete));
  }, [guideStorageKey, setupComplete]);

  function dismissGuide(reason: "dismissed" | "opened") {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(guideStorageKey, reason);
    }
    setGuidePreference("hide");
  }

  function openGuideTarget(target: ViewKey) {
    dismissGuide("opened");
    onSelectView(target);
  }

  if (setupComplete || guidePreference !== "show" || !primary) return null;

  return (
    <section className={`overview-workbench role-${role}`}>
      <div className="overview-workbench-main">
        <div>
          <p className="eyebrow">{tx(overviewRoleEyebrow(role))}</p>
          <h2>{tx(overviewRoleTitle(role))}</h2>
          <p>{tx(overviewRoleSummary(role))}</p>
        </div>
        <div className="overview-workbench-controls">
          <div className="overview-readiness">
            <span>{tx("就绪度")}</span>
            <strong>{setupScore.ready}/{setupScore.total}</strong>
            <small>{tx(setupScore.label)}</small>
          </div>
          <button className="icon-button workbench-dismiss" onClick={() => dismissGuide("dismissed")} type="button" title={tx("不再提示")}>
            <X size={15} />
          </button>
        </div>
      </div>

      <div className="overview-primary-action">
        <div>
          <span className={`workbench-status ${primary.status}`}>{tx(primary.statusLabel)}</span>
          <strong>{tx(primary.title)}</strong>
          <small>{tx(primary.description)}</small>
        </div>
        <button className="button" onClick={() => openGuideTarget(primary.target)} type="button">
          {tx(primary.action)}
        </button>
      </div>

      <div className="overview-workbench-grid">
        {secondaryItems.map((item) => {
          const Icon = item.icon;
          return (
            <button className="overview-workbench-item" key={item.title} onClick={() => openGuideTarget(item.target)} type="button">
              <span className={`workbench-icon ${item.status}`}>
                <Icon size={16} />
              </span>
              <span className="workbench-item-body">
                <span className={`workbench-status ${item.status}`}>{tx(item.statusLabel)}</span>
                <strong>{tx(item.title)}</strong>
                <small>{tx(item.description)}</small>
              </span>
            </button>
          );
        })}
      </div>
    </section>
  );
}

export function overviewWorkbenchStorageKey(user: AdminUser, role: AppRole) {
  const userKey = user.id || user.username || user.email || "anonymous";
  return `tokenhub.overview.workbench.v1.${role}.${userKey}`;
}

export function overviewWorkbenchInitialPreference(storageKey: string, setupComplete: boolean): "show" | "hide" {
  if (typeof window === "undefined") return setupComplete ? "hide" : "show";
  if (setupComplete) {
    window.localStorage.setItem(storageKey, "completed");
    return "hide";
  }
  if (window.localStorage.getItem(storageKey)) return "hide";
  window.localStorage.setItem(storageKey, "shown");
  return "show";
}

export function overviewRoleEyebrow(role: AppRole) {
  switch (role) {
    case "admin":
      return "平台管理员工作台";
    case "security":
      return "安全审计工作台";
    case "team_leader":
      return "团队 Leader 工作台";
    default:
      return "开发者工作台";
  }
}

export function overviewRoleTitle(role: AppRole) {
  switch (role) {
    case "admin":
      return "先让平台可用，再治理成本与风险";
    case "security":
      return "聚焦审计、异常和访问边界";
    case "team_leader":
      return "先管项目和成员，再看团队用量";
    default:
      return "从项目 Key 和可用模型开始调用";
  }
}

export function overviewRoleSummary(role: AppRole) {
  switch (role) {
    case "admin":
      return "Provider、路由、身份源和成本治理是平台稳定运行的主路径。";
    case "security":
      return "优先检查请求日志、审计事件和异常失败请求。";
    case "team_leader":
      return "团队的 Key、成员和项目成本都应归属到具体项目空间。";
    default:
      return "确认项目、复制 Key、选择模型，然后按接口文档完成调用。";
  }
}

export function overviewSetupScore(role: AppRole, stats: OverviewWorkbenchStats) {
  const checks = overviewReadinessChecks(role, stats);
  const ready = checks.filter(Boolean).length;
  return {
    ready,
    total: checks.length,
    label: ready === checks.length ? "关键路径已就绪" : "仍有待处理项",
  };
}

export type OverviewWorkbenchStats = {
  projects: number;
  activeProviders: number;
  healthyProviders: number;
  activeRoutes: number;
  apiKeys: number;
  callableModels: number;
  requestCount: number;
};

export function overviewReadinessChecks(role: AppRole, stats: OverviewWorkbenchStats) {
  switch (role) {
    case "admin":
      return [stats.activeProviders > 0, stats.healthyProviders > 0, stats.activeRoutes > 0, stats.projects > 0];
    case "security":
      return [stats.requestCount > 0, stats.activeRoutes > 0, stats.healthyProviders > 0];
    case "team_leader":
      return [stats.projects > 0, stats.apiKeys > 0, stats.callableModels > 0, stats.requestCount > 0];
    default:
      return [stats.apiKeys > 0, stats.callableModels > 0, stats.requestCount > 0];
  }
}

export function overviewWorkbenchItems(role: AppRole, stats: OverviewWorkbenchStats): OverviewWorkbenchItem[] {
  switch (role) {
    case "admin":
      return [
        {
          title: "接入 Provider",
          description: stats.activeProviders > 0 ? `${formatNumber(stats.healthyProviders)}/${formatNumber(stats.activeProviders)} 个渠道健康` : "还没有可用 Provider",
          status: stats.healthyProviders > 0 ? "ready" : "attention",
          statusLabel: stats.healthyProviders > 0 ? "已就绪" : "待处理",
          target: "providers",
          action: "查看 Provider",
          icon: Server,
        },
        {
          title: "配置模型路由",
          description: stats.activeRoutes > 0 ? `${formatNumber(stats.activeRoutes)} 条启用路由` : "模型需要路由后才能调用",
          status: stats.activeRoutes > 0 ? "ready" : "attention",
          statusLabel: stats.activeRoutes > 0 ? "已就绪" : "待处理",
          target: "routes",
          action: "配置路由",
          icon: Boxes,
        },
        {
          title: "组织接入",
          description: "身份源、角色和默认授权集中在系统设置",
          status: "next",
          statusLabel: "建议",
          target: "settings",
          action: "查看身份源",
          icon: ShieldCheck,
        },
        {
          title: "成本与用量",
          description: stats.requestCount > 0 ? `${formatNumber(stats.requestCount)} 次请求可分析` : "暂无请求数据",
          status: stats.requestCount > 0 ? "ready" : "next",
          statusLabel: stats.requestCount > 0 ? "可分析" : "待观察",
          target: "usage",
          action: "查看用量",
          icon: Gauge,
        },
      ];
    case "security":
      return [
        {
          title: "请求审计",
          description: stats.requestCount > 0 ? `${formatNumber(stats.requestCount)} 次请求可追踪` : "暂无请求记录",
          status: stats.requestCount > 0 ? "ready" : "next",
          statusLabel: stats.requestCount > 0 ? "可审计" : "待观察",
          target: "audit",
          action: "查看审计",
          icon: FileText,
        },
        {
          title: "模型边界",
          description: stats.activeRoutes > 0 ? `${formatNumber(stats.activeRoutes)} 条路由在服务` : "暂无启用路由",
          status: stats.activeRoutes > 0 ? "ready" : "attention",
          statusLabel: stats.activeRoutes > 0 ? "已就绪" : "待确认",
          target: "models",
          action: "查看模型",
          icon: Boxes,
        },
        {
          title: "安全策略",
          description: "统一查看策略、代理出口和数据备份",
          status: "next",
          statusLabel: "建议",
          target: "security-policies",
          action: "查看策略",
          icon: ShieldCheck,
        },
      ];
    case "team_leader":
      return [
        {
          title: "项目空间",
          description: stats.projects > 0 ? `${formatNumber(stats.projects)} 个项目可管理` : "先创建团队项目",
          status: stats.projects > 0 ? "ready" : "attention",
          statusLabel: stats.projects > 0 ? "已就绪" : "待处理",
          target: "projects",
          action: "管理项目",
          icon: LayoutDashboard,
        },
        {
          title: "Key 管理",
          description: stats.apiKeys > 0 ? `${formatNumber(stats.apiKeys)} 个 Key 已发放` : "项目需要 Key 才能接入应用",
          status: stats.apiKeys > 0 ? "ready" : "attention",
          statusLabel: stats.apiKeys > 0 ? "已就绪" : "待发放",
          target: "api-keys",
          action: "管理 Key",
          icon: KeyRound,
        },
        {
          title: "团队用量",
          description: stats.requestCount > 0 ? "已有请求，可按项目和成员归因" : "调用后会形成团队报表",
          status: stats.requestCount > 0 ? "ready" : "next",
          statusLabel: stats.requestCount > 0 ? "可分析" : "待观察",
          target: "usage",
          action: "查看报表",
          icon: BarChart3,
        },
        {
          title: "调用文档",
          description: "项目 Key、Base URL 和模型接口都在文档里",
          status: "next",
          statusLabel: "建议",
          target: "gateway",
          action: "查看文档",
          icon: Code2,
        },
      ];
    default:
      return [
        {
          title: "Key 管理",
          description: stats.apiKeys > 0 ? `${formatNumber(stats.apiKeys)} 个 Key 可用于调用` : "创建或领取项目 Key",
          status: stats.apiKeys > 0 ? "ready" : "attention",
          statusLabel: stats.apiKeys > 0 ? "已就绪" : "待处理",
          target: "api-keys",
          action: "查看 Key",
          icon: KeyRound,
        },
        {
          title: "可用模型",
          description: stats.callableModels > 0 ? `${formatNumber(stats.callableModels)} 个模型可查看` : "暂无可见模型",
          status: stats.callableModels > 0 ? "ready" : "attention",
          statusLabel: stats.callableModels > 0 ? "可查看" : "待开通",
          target: "models",
          action: "查看模型",
          icon: Boxes,
        },
        {
          title: "调用文档",
          description: "复制 Base URL、curl 和 OpenAI SDK 示例",
          status: "next",
          statusLabel: "下一步",
          target: "gateway",
          action: "打开文档",
          icon: Code2,
        },
        {
          title: "我的用量",
          description: stats.requestCount > 0 ? `${formatNumber(stats.requestCount)} 次请求可查看` : "调用后会形成个人用量",
          status: stats.requestCount > 0 ? "ready" : "next",
          statusLabel: stats.requestCount > 0 ? "可分析" : "待观察",
          target: "usage",
          action: "查看用量",
          icon: BarChart3,
        },
      ];
  }
}

export type OverviewRangeKey = "7d" | "30d" | "month";

export type OverviewMetricKey = "requests" | "tokens" | "cost";

export const overviewRangeTabs: Array<{ key: OverviewRangeKey; label: string }> = [
  { key: "7d", label: "7 天" },
  { key: "30d", label: "30 天" },
  { key: "month", label: "本月" },
];

export const overviewMetricTabs: Array<{ key: OverviewMetricKey; label: string }> = [
  { key: "requests", label: "请求" },
  { key: "tokens", label: "Token" },
  { key: "cost", label: "成本" },
];

export function OverviewMetricCard({
  badge,
  caption,
  icon: Icon,
  label,
  value,
  values,
}: {
  badge?: string;
  caption?: string;
  icon: typeof Activity;
  label: string;
  value: string;
  values: number[];
}) {
  return (
    <article className="metric compact-metric overview-metric-card">
      <div className="overview-card-head">
        <div className="metric-label">
          <Icon size={17} />
          {tx(label)}
        </div>
        {badge ? <span>{tx(badge)}</span> : null}
      </div>
      <div className="metric-value">{value}</div>
      {caption ? (
        <div className="overview-health-caption">
          <span />
          {tx(caption)}
        </div>
      ) : (
        <OverviewSparkline values={values} />
      )}
    </article>
  );
}

export function OverviewSparkline({ values }: { values: number[] }) {
  const path = overviewLinePath(values, 160, 34, 3);
  return (
    <svg className="overview-sparkline" viewBox="0 0 160 34" preserveAspectRatio="none" aria-hidden="true">
      <path d={path} />
    </svg>
  );
}

export function OverviewTrendChart({ metric, points }: { metric: OverviewMetricKey; points: UsagePoint[] }) {
  const values = points.map((point) => usagePointMetric(point, metric));
  const width = 900;
  const height = 330;
  const left = 52;
  const right = 24;
  const top = 28;
  const bottom = 46;
  const baseline = height - bottom;
  const line = overviewLinePath(values, width - left - right, baseline - top, 0, left, top);
  const area = line ? `${line} L ${width - right} ${baseline} L ${left} ${baseline} Z` : "";
  const max = Math.max(...values, 1);
  const ticks = [0.25, 0.5, 0.75];
  const labels = overviewAxisLabels(points);

  return (
    <div className="overview-chart-wrap">
      <svg className="overview-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={tx("成本与用量趋势")}>
        <defs>
          <linearGradient id="overviewArea" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.22" />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {ticks.map((tick) => {
          const y = baseline - (baseline - top) * tick;
          return (
            <g key={tick}>
              <line x1={left} x2={width - right} y1={y} y2={y} />
              <text x={8} y={y + 4}>{overviewMetricDisplay(max * tick, metric)}</text>
            </g>
          );
        })}
        <path className="overview-chart-area" d={area} />
        <path className="overview-chart-line" d={line} />
        {labels.map((item) => (
          <text className="overview-axis-label" key={`${item.label}-${item.x}`} x={item.x} y={height - 10}>{item.label}</text>
        ))}
      </svg>
    </div>
  );
}

export function OverviewProviderShare({ rows }: { rows: Array<{ id: string; label: string; percent: number; value: number; cost: number }> }) {
  const totalCost = rows.reduce((sum, row) => sum + row.cost, 0);
  return (
    <article className="overview-panel overview-share-panel">
      <h2>{tx("Provider 成本占比")}</h2>
      <div className="overview-share-content">
        <div className="overview-donut" style={{ background: overviewDonutGradient(rows) }}>
          <div>
            <strong>{totalCost > 0 ? `$${compactNumber(totalCost)}` : "$0"}</strong>
            <span>{tx("总成本")}</span>
          </div>
        </div>
        <div className="overview-share-list">
          {rows.length ? rows.map((row, index) => (
            <div className="overview-share-row" key={row.id}>
              <span className={`overview-share-dot color-${index}`} />
              <span>{row.label}</span>
              <strong>{row.percent}%</strong>
            </div>
          )) : (
            <div className="compact-empty">{tx("暂无 Provider 成本数据")}</div>
          )}
        </div>
      </div>
    </article>
  );
}

export function OverviewTopModels({ rows }: { rows: UsageBreakdownRow[] }) {
  const max = Math.max(...rows.map((row) => row.request_count), 1);
  return (
    <article className="overview-panel overview-top-panel">
      <h2>{tx("Top 模型 · 调用量")}</h2>
      <div className="overview-top-list">
        {rows.length ? rows.map((row) => (
          <div className="overview-top-row" key={row.id}>
            <div>
              <span>{row.id}</span>
              <em>{formatNumber(row.request_count)}</em>
            </div>
            <span className="overview-progress">
              <span style={{ width: `${Math.max(4, Math.round((row.request_count / max) * 100))}%` }} />
            </span>
          </div>
        )) : (
          <div className="compact-empty">{tx("暂无模型调用数据")}</div>
        )}
      </div>
    </article>
  );
}

export function overviewRangePoints(data: AppData, range: OverviewRangeKey) {
  const source = (data.timeseries.length ? data.timeseries : fallbackOverviewDays(data.summary))
    .slice()
    .sort((left, right) => left.date.localeCompare(right.date));
  if (range === "7d") return source.slice(-7);
  if (range === "30d") return source.slice(-30);
  const latestMonth = source.at(-1)?.date.slice(0, 7);
  const monthPoints = latestMonth ? source.filter((point) => point.date.startsWith(latestMonth)) : source;
  return monthPoints.length ? monthPoints : source.slice(-30);
}

export function fallbackOverviewDays(summary: Summary): UsagePoint[] {
  const weights = [0.56, 0.48, 0.79, 0.68, 0.96, 0.86, 1.08, 0.99, 1.17, 1.25];
  const totalWeight = weights.reduce((sum, item) => sum + item, 0);
  return weights.map((weight, index) => {
    const ratio = weight / totalWeight;
    const totalTokens = Math.round((summary.total_tokens || 0) * ratio);
    const inputTokens = Math.round((summary.input_tokens || totalTokens * 0.58) * ratio);
    const outputTokens = Math.max(0, totalTokens - inputTokens);
    return {
      date: `2026-06-${String(index + 9).padStart(2, "0")}`,
      request_count: Math.round((summary.request_count || 0) * ratio),
      input_tokens: inputTokens,
      output_tokens: outputTokens,
      total_tokens: totalTokens,
      estimated_cost_usd: Number(((summary.estimated_cost_usd || 0) * ratio).toFixed(6)),
    };
  });
}

export function usagePointMetric(point: UsagePoint, metric: OverviewMetricKey) {
  if (metric === "tokens") return point.total_tokens;
  if (metric === "cost") return point.estimated_cost_usd;
  return point.request_count;
}

export function overviewMetricValue(points: UsagePoint[], metric: OverviewMetricKey) {
  const values = points.map((point) => usagePointMetric(point, metric));
  const total = values.reduce((sum, value) => sum + value, 0);
  return {
    value: overviewMetricDisplay(total, metric),
    delta: overviewDeltaLabel(values),
  };
}

export function overviewMetricDisplay(value: number, metric: OverviewMetricKey) {
  if (metric === "cost") return `$${formatMoney(value)}`;
  if (metric === "tokens") return compactNumber(value);
  return formatNumber(Math.round(value || 0));
}

export function overviewDeltaLabel(values: number[]) {
  const clean = values.filter((value) => Number.isFinite(value));
  if (clean.length < 2) return "+0%";
  const previous = clean[0] || 0;
  const current = clean.at(-1) || 0;
  if (previous <= 0 && current <= 0) return "+0%";
  if (previous <= 0) return "+100%";
  const delta = ((current - previous) / previous) * 100;
  const sign = delta >= 0 ? "+" : "";
  return `${sign}${delta.toFixed(Math.abs(delta) >= 10 ? 0 : 1)}%`;
}

export function overviewRangeLabel(range: OverviewRangeKey) {
  if (range === "7d") return tx("近 7 天");
  if (range === "30d") return tx("近 30 天");
  return tx("本月");
}

export function overviewLinePath(values: number[], width: number, height: number, pad = 0, offsetX = 0, offsetY = 0) {
  if (!values.length) return "";
  const max = Math.max(...values, 1);
  const min = Math.min(...values, 0);
  const span = Math.max(max - min, 1);
  const usableWidth = Math.max(1, width - pad * 2);
  const usableHeight = Math.max(1, height - pad * 2);
  return values
    .map((value, index) => {
      const x = offsetX + pad + (values.length === 1 ? usableWidth : (index / (values.length - 1)) * usableWidth);
      const y = offsetY + pad + usableHeight - ((value - min) / span) * usableHeight;
      return `${index === 0 ? "M" : "L"} ${x.toFixed(2)} ${y.toFixed(2)}`;
    })
    .join(" ");
}

export function overviewAxisLabels(points: UsagePoint[]) {
  if (!points.length) return [];
  const indexes = [0, Math.floor((points.length - 1) / 3), Math.floor(((points.length - 1) * 2) / 3), points.length - 1];
  const unique = Array.from(new Set(indexes));
  const left = 52;
  const right = 24;
  const width = 900 - left - right;
  return unique.map((index) => ({
    x: left + (points.length === 1 ? 0 : (index / (points.length - 1)) * width),
    label: overviewDateLabel(points[index].date),
  }));
}

export function overviewDateLabel(date: string) {
  const [, , month, day] = date.match(/^(\d{4})-(\d{2})-(\d{2})/) ?? [];
  return month && day ? `${month}/${day}` : date;
}

export function overviewProviderShareRows(data: AppData) {
  const source = (data.breakdown.providers ?? [])
    .filter((row) => row.estimated_cost_usd > 0 || row.request_count > 0)
    .sort((left, right) => right.estimated_cost_usd - left.estimated_cost_usd || right.request_count - left.request_count);
  const rows = source.length ? source.slice(0, 3) : data.providers.slice(0, 3).map((provider) => ({
    id: provider.id,
    request_count: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    estimated_cost_usd: 0,
  }));
  const total = rows.reduce((sum, row) => sum + row.estimated_cost_usd, 0);
  const fallbackTotal = rows.reduce((sum, row) => sum + row.request_count, 0);
  return rows.map((row) => {
    const value = total > 0 ? row.estimated_cost_usd : row.request_count;
    const denominator = total > 0 ? total : fallbackTotal;
    return {
      id: row.id,
      label: findProvider(data, row.id)?.name || row.id || "其他",
      cost: row.estimated_cost_usd,
      value,
      percent: denominator > 0 ? Math.round((value / denominator) * 100) : 0,
    };
  });
}

export function overviewDonutGradient(rows: Array<{ percent: number }>) {
  if (!rows.length || rows.every((row) => row.percent <= 0)) return "conic-gradient(var(--surface-3) 0 100%)";
  const colors = ["var(--accent)", "var(--accent-2)", "var(--ink-4)"];
  let start = 0;
  const segments = rows.map((row, index) => {
    const end = Math.min(100, start + row.percent);
    const segment = `${colors[index] ?? "var(--border-strong)"} ${start}% ${end}%`;
    start = end;
    return segment;
  });
  if (start < 100) segments.push(`var(--surface-3) ${start}% 100%`);
  return `conic-gradient(${segments.join(", ")})`;
}

export function overviewTopModelRows(data: AppData) {
  const breakdownRows = (data.breakdown.models ?? [])
    .filter((row) => row.request_count > 0 || row.total_tokens > 0)
    .sort((left, right) => right.request_count - left.request_count || right.total_tokens - left.total_tokens)
    .slice(0, 4);
  if (breakdownRows.length) return breakdownRows;

  const logs = new Map<string, UsageBreakdownRow>();
  for (const log of data.logs) {
    const id = log.model || "-";
    const current = logs.get(id) ?? {
      id,
      request_count: 0,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      estimated_cost_usd: 0,
    };
    current.request_count += 1;
    logs.set(id, current);
  }
  const logRows = Array.from(logs.values()).sort((left, right) => right.request_count - left.request_count).slice(0, 4);
  if (logRows.length) return logRows;

  return data.models.slice(0, 4).map((model) => ({
    id: model.name,
    request_count: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    estimated_cost_usd: 0,
  }));
}

export type UsageDashboardRankRow = UsageBreakdownRow & {
  label: string;
  cost: number;
};

export function roleUsageMonitorStats(data: AppData) {
  const requests = data.summary.request_count || data.logs.length;
  const failedRequests = data.summary.errors || data.logs.filter(requestLogFailed).length;
  const successRequests = Math.max(0, requests - failedRequests);
  const latencyLogs = data.logs.filter((log) => log.latency_ms > 0);
  const avgLatencyMS = latencyLogs.length
    ? Math.round(latencyLogs.reduce((sum, log) => sum + log.latency_ms, 0) / latencyLogs.length)
    : 0;
  return {
    requests,
    failedRequests,
    successRequests,
    successRate: requests > 0 ? (successRequests / requests) * 100 : 100,
    avgLatencyMS,
    zeroLatencyRequests: Math.max(0, data.logs.length - latencyLogs.length),
    inputTokens: data.summary.input_tokens,
    outputTokens: data.summary.output_tokens,
    totalTokens: data.summary.total_tokens,
    cost: data.summary.estimated_cost_usd,
  };
}

export function usageDashboardCostRows(rows: UsageBreakdownRow[], labelFor: (row: UsageBreakdownRow) => string): UsageDashboardRankRow[] {
  return rows
    .filter((row) => hasUsage(row))
    .map((row) => ({ ...row, label: labelFor(row), cost: row.estimated_cost_usd }))
    .sort((left, right) => right.cost - left.cost || right.total_tokens - left.total_tokens || right.request_count - left.request_count);
}

export function usageDashboardAccountRows(data: AppData): UsageDashboardRankRow[] {
  const resourceRows = usageDashboardCostRows(data.breakdown.provider_resources ?? [], (row) => providerResourceAuditLabel(data, row.id));
  if (resourceRows.length) return resourceRows;
  return usageDashboardCostRows(data.breakdown.providers ?? [], (row) => findProvider(data, row.id)?.name || row.id);
}

export function modelDisplayName(data: AppData, modelID: string) {
  const model = data.models.find((item) => item.name === modelID || item.id === modelID);
  return model?.name || modelID || "-";
}

export function requestLogFailed(log: RequestLog) {
  return log.status_code >= 400 || Boolean(log.error_code);
}

export function latencyDisplay(value: number) {
  if (!value) return "-";
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)}s`;
  return `${Math.round(value)}ms`;
}

export function usageMonitorRequestLine(points: UsagePoint[], maxRequests: number) {
  if (!points.length) return "";
  return points
    .map((point, index) => {
      const x = 58 + index * (points.length <= 1 ? 0 : 620 / (points.length - 1));
      const y = 220 - (point.request_count / Math.max(maxRequests, 1)) * 168;
      return `${index === 0 ? "M" : "L"} ${x.toFixed(2)} ${y.toFixed(2)}`;
    })
    .join(" ");
}

export function usageHealthGridSize(width: number, height: number) {
  const cellSize = 6;
  const gap = 3;
  return {
    rows: clampInt(Math.floor((Math.max(height, 120) + gap) / (cellSize + gap)), 7, 28),
    columns: clampInt(Math.floor((Math.max(width, 180) + gap) / (cellSize + gap)), 24, 120),
  };
}

export function clampInt(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, Number.isFinite(value) ? value : min));
}

export function usageHealthCells(logs: RequestLog[], cellCount: number) {
  const recent = logs
    .slice()
    .sort((left, right) => left.created_at.localeCompare(right.created_at))
    .slice(-cellCount)
    .map((log) => {
      if (requestLogFailed(log)) return "failure";
      if (log.status_code >= 300 || log.latency_ms >= 5000) return "warning";
      return "success";
    });
  return [...Array.from({ length: Math.max(0, cellCount - recent.length) }, () => "none"), ...recent];
}

export function providerFailureLabel(data: AppData, log: RequestLog) {
  if (log.provider_resource_id) return providerResourceAuditLabel(data, log.provider_resource_id);
  if (log.provider_id) return findProvider(data, log.provider_id)?.name || log.provider_id;
  return "-";
}
