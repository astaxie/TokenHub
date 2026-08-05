import { CirclePause, CirclePlay, Pencil, Plus, RefreshCw, TestTube2, X } from "lucide-react";
import { type FormEvent, useState } from "react";
import { appRole } from "../core/navigation";
import { type AdminUser, type ApiContext, type AppData, type BillingConnector, type UsageBreakdownRow } from "../core/types";
import { costCenterLabel, projectName, providerCostDetailRows, teamLabel, usageMemberLabel } from "../domain/entities";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { countWithUnit, displayText, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection, SimpleTable, StatusPill } from "../shared/ui";
import { ReconciliationManager } from "./billing-reconciliation";

export function UsageView({ data, user }: { data: AppData; user: AdminUser }) {
  const modelBreakdown = data.breakdown.models ?? [];
  const showMemberBreakdown = appRole(user.role) === "team_leader";
  const showExecutiveReport = appRole(user.role) !== "user";
  return (
    <>
      {showExecutiveReport ? <ExecutiveUsageReport data={data} /> : <PersonalUsageSummary data={data} />}
      <div className="two-column">
        <DataSection title="模型用量">
          <SimpleTable
            columns={["模型", "请求", "Token", "缓存读", "缓存命中率", "成本"]}
            paginationKey="usage-models"
            rows={modelBreakdown.map((row) => [
              row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              compactNumber(row.cached_input_tokens ?? 0),
              cacheHitRate(row.cached_input_tokens ?? 0, row.input_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
        <DataSection title={showMemberBreakdown ? "成员用量" : "项目归因"}>
          <SimpleTable
            columns={[showMemberBreakdown ? "成员" : "项目", "请求", "Token", "缓存读", "缓存命中率", "成本"]}
            paginationKey={showMemberBreakdown ? "usage-members" : "usage-projects"}
            rows={(showMemberBreakdown ? data.breakdown.members ?? [] : data.breakdown.projects ?? []).map((row) => [
              showMemberBreakdown ? usageMemberLabel(data, row.id) : row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              compactNumber(row.cached_input_tokens ?? 0),
              cacheHitRate(row.cached_input_tokens ?? 0, row.input_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
      </div>
      {showMemberBreakdown ? (
        <DataSection title="项目归因">
          <SimpleTable
            columns={["项目", "请求", "Token", "缓存读", "缓存命中率", "成本"]}
            paginationKey="usage-projects"
            rows={(data.breakdown.projects ?? []).map((row) => [
              projectName(data, row.id),
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              compactNumber(row.cached_input_tokens ?? 0),
              cacheHitRate(row.cached_input_tokens ?? 0, row.input_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
      ) : null}
    </>
  );
}

export function cacheHitRate(cachedInputTokens: number, inputTokens: number) {
  if (!Number.isFinite(cachedInputTokens) || !Number.isFinite(inputTokens) || inputTokens <= 0) return "0%";
  return `${Math.min(100, Math.max(0, (cachedInputTokens / inputTokens) * 100)).toFixed(1)}%`;
}

export function PersonalUsageSummary({ data }: { data: AppData }) {
  return (
    <section className="executive-report personal-usage-report">
      <header className="executive-report-head">
        <div>
          <p className="eyebrow">Personal Usage</p>
          <h2>{tx("我的用量概览")}</h2>
        </div>
        <div className="executive-report-tools">
          <span>{tx("个人范围")}</span>
          <span>{tx("Token 口径")}</span>
        </div>
      </header>

      <div className="executive-kpi-grid">
        <ExecutiveKPI label="总 Token 消耗" value={compactNumber(data.summary.total_tokens)} detail={`${tx("输入")} ${compactNumber(data.summary.input_tokens)} / ${tx("缓存读")} ${compactNumber(data.summary.cached_input_tokens ?? 0)} / ${tx("输出")} ${compactNumber(data.summary.output_tokens)}`} />
        <ExecutiveKPI label="请求数" value={formatNumber(data.summary.request_count)} detail={countWithUnit(data.summary.usage_record_count ?? 0, "条用量记录", "usage record", "件の利用記録")} />
        <ExecutiveKPI label="估算成本" value={`$${formatMoney(data.summary.estimated_cost_usd)}`} detail={countWithUnit(data.summary.errors, "个错误", "error", "件のエラー")} />
        <ExecutiveKPI label="可见项目" value={formatNumber(data.projects.length)} detail={tx("按当前账号权限汇总")} />
      </div>
    </section>
  );
}

export type ExecutiveDepartmentRow = UsageBreakdownRow & {
  name: string;
  member_count: number;
};

export type ExecutiveMemberRow = UsageBreakdownRow & {
  name: string;
  department: string;
};

export function ExecutiveUsageReport({ data }: { data: AppData }) {
  const departments = executiveDepartmentRows(data);
  const members = executiveMemberRows(data);
  const totalTokens = data.summary.total_tokens || departments.reduce((sum, row) => sum + row.total_tokens, 0);
  const totalInput = data.summary.input_tokens || departments.reduce((sum, row) => sum + row.input_tokens, 0);
  const totalOutput = data.summary.output_tokens || departments.reduce((sum, row) => sum + row.output_tokens, 0);
  const topDepartment = departments[0];
  const activeMembers = members.filter((row) => row.total_tokens > 0 || row.request_count > 0).length;
  const departmentShare = topDepartment && totalTokens > 0 ? Math.round((topDepartment.total_tokens / totalTokens) * 100) : 0;
  const generatedAt = new Intl.DateTimeFormat(languageLocale(), {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date());
  const tokenDetail = `${tx("输入")} ${compactNumber(totalInput)} / ${tx("缓存读")} ${compactNumber(data.summary.cached_input_tokens ?? 0)} / ${tx("输出")} ${compactNumber(totalOutput)}`;
  const departmentDetail = topDepartment
    ? `${tx("最高")}：${topDepartment.name} · ${departmentShare}%`
    : tx("暂无部门归因");
  const generatedDetail = `${tx("统计时间")} ${generatedAt}`;
  const requestDetail = countWithUnit(data.summary.request_count, "次请求", "request", "件のリクエスト");

  return (
    <section className="executive-report">
      <header className="executive-report-head">
        <div>
          <p className="eyebrow">Executive Usage Report</p>
          <h2>{tx("企业 AI 用量看板")}</h2>
          <span>{tx("面向管理层的部门、个人与 Token 消耗对比")}</span>
        </div>
        <div className="executive-report-tools">
          <span>{tx("本月")}</span>
          <span>{tx("按部门")}</span>
          <span>{tx("Token 口径")}</span>
        </div>
      </header>

      <div className="executive-kpi-grid">
        <ExecutiveKPI label="总 Token 消耗" value={compactNumber(totalTokens)} detail={tokenDetail} />
        <ExecutiveKPI label="覆盖部门" value={formatNumber(departments.length)} detail={departmentDetail} />
        <ExecutiveKPI label="活跃成员" value={formatNumber(activeMembers)} detail={generatedDetail} />
        <ExecutiveKPI label="估算成本" value={`$${formatMoney(data.summary.estimated_cost_usd)}`} detail={requestDetail} />
      </div>

      <div className="executive-grid">
        <article className="executive-panel executive-chart-panel">
          <div className="executive-panel-head">
            <div>
              <h3>{tx("部门 Token 消耗对比")}</h3>
              <span>{tx("输入 Token 与输出 Token 分段展示，按总量排序")}</span>
            </div>
            <div className="executive-legend">
              <span><i className="input" />{tx("输入")}</span>
              <span><i className="output" />{tx("输出")}</span>
            </div>
          </div>
          <ExecutiveDepartmentChart rows={departments.slice(0, 8)} />
        </article>

        <article className="executive-panel executive-department-panel">
          <div className="executive-panel-head compact">
            <div>
              <h3>{tx("部门排行")}</h3>
              <span>Top {Math.min(departments.length, 8)} · {tx("Token 消耗")}</span>
            </div>
          </div>
          <ExecutiveDepartmentRanking rows={departments.slice(0, 8)} totalTokens={totalTokens} />
        </article>
      </div>

      <article className="executive-panel executive-member-panel">
        <div className="executive-panel-head">
          <div>
            <h3>{tx("个人排行")}</h3>
            <span>{tx("公司内部成员 Token 消耗 Top 20")}</span>
          </div>
          <div className="executive-report-tools subtle">
            <span>{tx("按 Token 降序")}</span>
            <span>{tx("可用于复盘配额")}</span>
          </div>
        </div>
        <ExecutiveMemberTable rows={members.slice(0, 20)} totalTokens={totalTokens} />
      </article>
    </section>
  );
}

export function ExecutiveKPI({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <article className="executive-kpi">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

export function ExecutiveDepartmentChart({ rows }: { rows: ExecutiveDepartmentRow[] }) {
  if (rows.length === 0) return <div className="empty">{tx("暂无部门 Token 数据")}</div>;
  const width = 960;
  const height = 320;
  const left = 54;
  const right = 28;
  const top = 28;
  const bottom = 70;
  const chartHeight = height - top - bottom;
  const baseline = height - bottom;
  const max = Math.max(...rows.map((row) => row.total_tokens), 1);
  const gap = 18;
  const barWidth = Math.max(28, (width - left - right - gap * (rows.length - 1)) / rows.length);
  const ticks = [0.25, 0.5, 0.75, 1];

  return (
    <div className="executive-chart-wrap">
      <svg className="executive-chart" viewBox={`0 0 ${width} ${height}`} role="img" aria-label={tx("部门 Token 消耗对比")}>
        {ticks.map((tick) => {
          const y = baseline - chartHeight * tick;
          return (
            <g key={tick}>
              <line x1={left} x2={width - right} y1={y} y2={y} />
              <text x={10} y={y + 4}>{compactNumber(max * tick)}</text>
            </g>
          );
        })}
        {rows.map((row, index) => {
          const x = left + index * (barWidth + gap);
          const inputHeight = Math.max(0, (row.input_tokens / max) * chartHeight);
          const outputHeight = Math.max(0, (row.output_tokens / max) * chartHeight);
          const totalHeight = inputHeight + outputHeight || Math.max(4, (row.total_tokens / max) * chartHeight);
          const inputY = baseline - inputHeight;
          const outputY = inputY - outputHeight;
          return (
            <g key={row.id}>
              <rect className="executive-bar-bg" x={x} y={top} width={barWidth} height={chartHeight} rx="8" />
              {row.output_tokens > 0 ? <rect className="executive-bar-output" x={x} y={outputY} width={barWidth} height={outputHeight} rx="8" /> : null}
              <rect className="executive-bar-input" x={x} y={row.input_tokens > 0 ? inputY : baseline - totalHeight} width={barWidth} height={row.input_tokens > 0 ? inputHeight : totalHeight} rx="8" />
              <text className="executive-bar-value" x={x + barWidth / 2} y={Math.max(18, baseline - totalHeight - 8)}>{compactNumber(row.total_tokens)}</text>
              <text className="executive-bar-label" x={x + barWidth / 2} y={height - 34}>{shortLabel(row.name, 8)}</text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}

export function ExecutiveDepartmentRanking({ rows, totalTokens }: { rows: ExecutiveDepartmentRow[]; totalTokens: number }) {
  if (rows.length === 0) return <div className="empty">{tx("暂无部门排行数据")}</div>;
  return (
    <div className="executive-rank-list">
      {rows.map((row, index) => {
        const percent = totalTokens > 0 ? Math.round((row.total_tokens / totalTokens) * 100) : 0;
        return (
          <div className="executive-rank-row" key={row.id}>
            <span className="executive-rank-index">{index + 1}</span>
            <div>
              <strong>{row.name}</strong>
              <small>{countWithUnit(row.member_count, "人", "member", "人")} · {countWithUnit(row.request_count, "次请求", "request", "件のリクエスト")}</small>
              <span className="executive-progress"><span style={{ width: `${Math.max(3, percent)}%` }} /></span>
            </div>
            <em>{compactNumber(row.total_tokens)}</em>
          </div>
        );
      })}
    </div>
  );
}

export function ExecutiveMemberTable({ rows, totalTokens }: { rows: ExecutiveMemberRow[]; totalTokens: number }) {
  if (rows.length === 0) return <div className="empty">{tx("暂无个人排行数据")}</div>;
  return (
    <div className="executive-table-wrap">
      <table className="executive-rank-table">
        <thead>
          <tr>
            <th>{tx("排名")}</th>
            <th>{tx("成员")}</th>
            <th>{tx("部门")}</th>
            <th>{tx("Key（已用/归属）")}</th>
            <th>{tx("请求")}</th>
            <th>{tx("输入 Token")}</th>
            <th>{tx("缓存读")}</th>
            <th>{tx("缓存命中率")}</th>
            <th>{tx("输出 Token")}</th>
            <th>{tx("总 Token")}</th>
            <th>{tx("占比")}</th>
            <th>{tx("成本")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row, index) => {
            const percent = totalTokens > 0 ? (row.total_tokens / totalTokens) * 100 : 0;
            return (
              <tr key={row.id}>
                <td><span className="executive-rank-badge">{index + 1}</span></td>
                <td><strong>{row.name}</strong><small>{row.id}</small></td>
                <td>{tx(row.department)}</td>
                <td>{formatNumber(row.used_key_count ?? 0)} / {formatNumber(row.owned_key_count ?? 0)}</td>
                <td>{formatNumber(row.request_count)}</td>
                <td>{compactNumber(row.input_tokens)}</td>
                <td>{compactNumber(row.cached_input_tokens ?? 0)}</td>
                <td>{cacheHitRate(row.cached_input_tokens ?? 0, row.input_tokens)}</td>
                <td>{compactNumber(row.output_tokens)}</td>
                <td><strong>{compactNumber(row.total_tokens)}</strong></td>
                <td>{percent.toFixed(percent >= 10 ? 0 : 1)}%</td>
                <td>${formatMoney(row.estimated_cost_usd)}</td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

export function executiveDepartmentRows(data: AppData): ExecutiveDepartmentRow[] {
  const costCenterRows = (data.breakdown.cost_centers ?? [])
    .filter((row) => hasUsage(row))
    .map((row) => ({
      ...row,
      name: costCenterLabel(data, row.id),
      member_count: membersInCostCenter(data, row.id),
    }));
  if (costCenterRows.length) return sortUsageRows(costCenterRows);

  const memberRows = data.breakdown.members ?? [];
  if (memberRows.length && data.users.length) {
    const byTeam = new Map<string, ExecutiveDepartmentRow>();
    for (const row of memberRows) {
      if (!hasUsage(row)) continue;
      const user = findUsageUser(data, row.id);
      const teamID = user?.team_id || "unknown";
      const current = byTeam.get(teamID) ?? {
        id: teamID,
        name: teamID === "unknown" ? tx("未归属部门") : teamLabel(data, teamID),
        member_count: 0,
        request_count: 0,
        input_tokens: 0,
        cached_input_tokens: 0,
        output_tokens: 0,
        total_tokens: 0,
        estimated_cost_usd: 0,
      };
      current.member_count += 1;
      addUsageRow(current, row);
      byTeam.set(teamID, current);
    }
    const rows = Array.from(byTeam.values());
    if (rows.length) return sortUsageRows(rows);
  }

  const projectRows = (data.breakdown.projects ?? [])
    .filter((row) => hasUsage(row))
    .map((row) => ({
      ...row,
      name: projectName(data, row.id),
      member_count: 0,
    }));
  return sortUsageRows(projectRows);
}

export function executiveMemberRows(data: AppData): ExecutiveMemberRow[] {
  const rows = (data.breakdown.members ?? [])
    .filter((row) => hasUsage(row))
    .map((row) => {
      const user = findUsageUser(data, row.id);
      return {
        ...row,
        name: user ? displayText(user.name) || user.username || user.email : usageMemberLabel(data, row.id),
        department: user?.team_id ? teamLabel(data, user.team_id) : tx("未归属部门"),
      };
    });
  return sortUsageRows(rows);
}

export function hasUsage(row: UsageBreakdownRow) {
  return row.request_count > 0 || row.input_tokens > 0 || row.output_tokens > 0 || row.total_tokens > 0 || row.estimated_cost_usd > 0;
}

export function sortUsageRows<T extends UsageBreakdownRow>(rows: T[]): T[] {
  return rows
    .slice()
    .sort((left, right) => right.total_tokens - left.total_tokens || right.request_count - left.request_count || right.estimated_cost_usd - left.estimated_cost_usd);
}

export function addUsageRow(target: UsageBreakdownRow, source: UsageBreakdownRow) {
  target.request_count += source.request_count;
  target.input_tokens += source.input_tokens;
  target.cached_input_tokens = (target.cached_input_tokens ?? 0) + (source.cached_input_tokens ?? 0);
  target.output_tokens += source.output_tokens;
  target.total_tokens += source.total_tokens;
  target.estimated_cost_usd += source.estimated_cost_usd;
  target.owned_key_count = (target.owned_key_count ?? 0) + (source.owned_key_count ?? 0);
  target.used_key_count = (target.used_key_count ?? 0) + (source.used_key_count ?? 0);
}

export function findUsageUser(data: AppData, id: string) {
  return data.users.find((item) => item.id === id || item.username === id || item.email === id);
}

export function membersInCostCenter(data: AppData, costCenterID: string) {
  const projectIDs = data.projects
    .filter((project) => project.cost_center === costCenterID)
    .map((project) => project.id);
  if (projectIDs.length === 0) return 0;
  const teamIDs = new Set(data.projects.filter((project) => projectIDs.includes(project.id) && project.team_id).map((project) => project.team_id as string));
  return data.users.filter((user) => user.team_id && teamIDs.has(user.team_id)).length;
}

export function shortLabel(value: string, maxLength: number) {
  if (value.length <= maxLength) return value;
  return `${value.slice(0, maxLength - 1)}…`;
}

export function BillingView({
  api,
  data,
  user,
  loading,
  onReload,
}: {
  api: ApiContext;
  data: AppData;
  user: AdminUser;
  loading: boolean;
  onReload: () => Promise<void>;
}) {
  const showMemberBreakdown = appRole(user.role) === "team_leader";
  const costCenterSection = (
    <DataSection title="成本中心">
      <SimpleTable
        columns={["成本中心", "请求", "Token", "估算成本"]}
        paginationKey="billing-cost-centers"
        rows={(data.breakdown.cost_centers ?? []).map((row) => [
          row.id,
          formatNumber(row.request_count),
          compactNumber(row.total_tokens),
          `$${formatMoney(row.estimated_cost_usd)}`,
        ])}
      />
    </DataSection>
  );
  const memberCostSection = (
    <DataSection title="成员成本">
      <SimpleTable
        columns={["成员", "请求", "Token", "估算成本"]}
        paginationKey="billing-members"
        rows={(data.breakdown.members ?? []).map((row) => [
          usageMemberLabel(data, row.id),
          formatNumber(row.request_count),
          compactNumber(row.total_tokens),
          `$${formatMoney(row.estimated_cost_usd)}`,
        ])}
      />
    </DataSection>
  );
  return (
    <>
      {appRole(user.role) === "admin" ? <BillingConnectorManager api={api} data={data} loading={loading} onReload={onReload} /> : null}
      {appRole(user.role) === "admin" ? <ReconciliationManager api={api} data={data} loading={loading} onReload={onReload} /> : null}
      {showMemberBreakdown ? (
        <div className="two-column">
          {costCenterSection}
          {memberCostSection}
        </div>
      ) : (
        costCenterSection
      )}
      <div className="two-column">
        <DataSection title="Provider 成本">
          <SimpleTable
            columns={["Provider", "请求", "Token", "估算成本"]}
            paginationKey="billing-providers"
            rows={(data.breakdown.providers ?? []).map((row) => [
              row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
        <DataSection title="Provider 明细成本">
          <SimpleTable
            columns={["命中 Provider", "请求", "Token", "估算成本"]}
            paginationKey="billing-provider-resources"
            rows={providerCostDetailRows(data).map((row) => [
              row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
      </div>
    </>
  );
}

function BillingConnectorManager({ api, data, loading, onReload }: { api: ApiContext; data: AppData; loading: boolean; onReload: () => Promise<void> }) {
  const [editor, setEditor] = useState<BillingConnector | "new" | null>(null);
  const [busy, setBusy] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const connectorNames = new Map(data.billingConnectors.map((connector) => [connector.id, connector.name]));

  async function runAction(connector: BillingConnector, action: "test" | "sync") {
    setBusy(`${connector.id}:${action}`);
    setError("");
    setNotice("");
    try {
      const response = await adminFetch(api, `/api/admin/billing/connectors/${encodeURIComponent(connector.id)}/${action}`, {
        method: "POST",
        body: JSON.stringify({}),
      });
      if (!response.ok) throw new Error(await readAdminError(response, action === "test" ? "连接测试失败" : "账单同步失败"));
      setNotice(tx(action === "test" ? "连接测试通过" : "账单同步完成"));
      await onReload();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : tx("操作失败"));
    } finally {
      setBusy("");
    }
  }

  async function toggleConnector(connector: BillingConnector) {
    const nextStatus = connector.status === "active" ? "disabled" : "active";
    setBusy(`${connector.id}:status`);
    setError("");
    setNotice("");
    try {
      const response = await adminFetch(api, `/api/admin/billing/connectors/${encodeURIComponent(connector.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ status: nextStatus }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, "连接器状态更新失败"));
      setNotice(tx(nextStatus === "active" ? "连接器已启用" : "连接器已停用"));
      await onReload();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : tx("操作失败"));
    } finally {
      setBusy("");
    }
  }

  return (
    <>
      <DataSection title="外部账单连接器">
        <div className="billing-connector-toolbar">
          <div className="billing-connector-status" aria-live="polite">
            {error ? <span className="billing-inline-error">{error}</span> : null}
            {!error && notice ? <span className="billing-inline-notice">{notice}</span> : null}
          </div>
          <button className="button" onClick={() => setEditor("new")} type="button">
            <Plus size={15} />{tx("新增连接器")}
          </button>
        </div>
        <SimpleTable
          columns={["名称", "类型", "状态", "调度", "最近同步", "操作"]}
          paginationKey="billing-connectors"
          rows={data.billingConnectors.map((connector) => [
            <div className="billing-source-name" key={`${connector.id}:name`}><strong>{connector.name}</strong><span>{connector.base_url}</span></div>,
            billingConnectorTypeLabel(connector.type),
            <StatusPill key={`${connector.id}:status`} status={connector.status} />,
            connector.schedule_interval_minutes > 0 ? `${connector.schedule_interval_minutes} min` : tx("手动"),
            <div className="billing-sync-state" key={`${connector.id}:sync`}><StatusPill status={connector.last_sync_status || "pending"} /><span>{formatBillingDate(connector.last_sync_at)}</span></div>,
            <div className="billing-row-actions" key={`${connector.id}:actions`}>
              <button className="icon-button subtle" disabled={Boolean(busy) || loading} onClick={() => setEditor(connector)} title={tx("编辑连接器")} type="button"><Pencil size={15} /></button>
              <button className="icon-button subtle" disabled={Boolean(busy) || loading} onClick={() => void runAction(connector, "test")} title={tx("测试连接")} type="button"><TestTube2 size={15} /></button>
              <button className="icon-button subtle" disabled={Boolean(busy) || loading || connector.status !== "active"} onClick={() => void runAction(connector, "sync")} title={tx("立即同步")} type="button"><RefreshCw className={busy === `${connector.id}:sync` ? "spin" : ""} size={15} /></button>
              <button className="icon-button subtle" disabled={Boolean(busy) || loading} onClick={() => void toggleConnector(connector)} title={tx(connector.status === "active" ? "停用连接器" : "启用连接器")} type="button">
                {connector.status === "active" ? <CirclePause size={15} /> : <CirclePlay size={15} />}
              </button>
            </div>,
          ])}
        />
      </DataSection>

      <div className="two-column billing-observability-grid">
        <DataSection title="最近同步">
          <SimpleTable
            columns={["连接器", "触发方式", "状态", "账单", "请求", "完成时间"]}
            paginationKey="billing-sync-runs"
            rows={data.billingSyncRuns.map((run) => [
              connectorNames.get(run.connector_id) || run.connector_id,
              run.trigger === "scheduled" ? tx("定时") : tx("手动"),
              <StatusPill key={`${run.id}:status`} status={run.status} />,
              `${formatNumber(run.records_inserted)} / ${formatNumber(run.records_updated)}`,
              formatNumber(run.attempts),
              formatBillingDate(run.finished_at || run.started_at),
            ])}
          />
        </DataSection>
        <DataSection title="外部账单明细">
          <SimpleTable
            columns={["账期", "来源", "产品 / 模型", "净额"]}
            paginationKey="billing-records"
            rows={data.billingRecords.map((record) => [
              record.billing_period,
              connectorNames.get(record.connector_id) || record.source_type,
              record.model || record.product || record.service || "-",
              `${record.currency} ${record.net_amount}`,
            ])}
          />
        </DataSection>
      </div>

      {editor ? (
        <BillingConnectorEditor
          api={api}
          connector={editor === "new" ? undefined : editor}
          key={editor === "new" ? "new" : editor.id}
          onClose={() => setEditor(null)}
          onSaved={async () => {
            setEditor(null);
            setNotice(tx("连接器已保存"));
            await onReload();
          }}
        />
      ) : null}
    </>
  );
}

function BillingConnectorEditor({ api, connector, onClose, onSaved }: { api: ApiContext; connector?: BillingConnector; onClose: () => void; onSaved: () => Promise<void> }) {
  const [values, setValues] = useState(() => billingConnectorFormValues(connector));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const update = (key: string, value: string) => setValues((current) => ({ ...current, [key]: value }));

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const payload = billingConnectorPayload(values, Boolean(connector));
      const path = connector ? `/api/admin/billing/connectors/${encodeURIComponent(connector.id)}` : "/api/admin/billing/connectors";
      const response = await adminFetch(api, path, { method: connector ? "PATCH" : "POST", body: JSON.stringify(payload) });
      if (!response.ok) throw new Error(await readAdminError(response, "连接器保存失败"));
      await onSaved();
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : tx("连接器保存失败"));
    } finally {
      setBusy(false);
    }
  }

  const isAliyun = values.type === "aliyun";
  const isNewAPI = values.type === "newapi";
  const updateType = (type: BillingConnector["type"]) => setValues((current) => ({
    ...current,
    type,
    currency: type === "aliyun" ? "CNY" : "USD",
    endpoint: type === "oneapi" ? "/api/log/self" : current.endpoint,
  }));
  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal billing-connector-modal" onSubmit={submit}>
        <div className="modal-header">
          <div><p className="eyebrow">Billing Connector</p><h2>{tx(connector ? "编辑账单连接器" : "新增账单连接器")}</h2></div>
          <button className="icon-button" onClick={onClose} title={tx("关闭")} type="button"><X size={18} /></button>
        </div>
        <div className="modal-body billing-connector-form">
          <label className="field"><span>{tx("名称")} *</span><input value={values.name} onChange={(event) => update("name", event.target.value)} required /></label>
          <label className="field"><span>{tx("类型")} *</span><select disabled={Boolean(connector)} value={values.type} onChange={(event) => updateType(event.target.value as BillingConnector["type"])}><option value="oneapi">OneAPI</option><option value="newapi">NewAPI</option><option value="aliyun">Aliyun</option></select></label>
          <label className="field billing-wide-field"><span>Base URL *</span><input type="url" value={values.base_url} onChange={(event) => update("base_url", event.target.value)} required /></label>
          <label className="field"><span>{tx("同步间隔（分钟）")}</span><input min="0" type="number" value={values.schedule_interval_minutes} onChange={(event) => update("schedule_interval_minutes", event.target.value)} /></label>
          <label className="field"><span>{tx("每秒请求上限")}</span><input min="0" type="number" value={values.rate_limit_per_second} onChange={(event) => update("rate_limit_per_second", event.target.value)} /></label>
          <label className="field"><span>{tx("币种")}</span><input maxLength={3} value={values.currency} onChange={(event) => update("currency", event.target.value.toUpperCase())} /></label>
          <label className="field"><span>{tx("TokenHub Provider ID")} *</span><input value={values.provider_id} onChange={(event) => update("provider_id", event.target.value)} required /></label>
          <label className="field"><span>{tx("TokenHub 资源账号 ID（可选）")}</span><input value={values.provider_resource_id} onChange={(event) => update("provider_resource_id", event.target.value)} /></label>
          {isAliyun ? (
            <>
              <label className="field"><span>AccessKey ID *</span><input autoComplete="off" value={values.access_key_id} onChange={(event) => update("access_key_id", event.target.value)} required={!connector?.credentials_configured} /></label>
              <label className="field"><span>AccessKey Secret *</span><input autoComplete="new-password" type="password" value={values.access_key_secret} onChange={(event) => update("access_key_secret", event.target.value)} required={!connector?.credentials_configured} /></label>
              <label className="field"><span>{tx("源时区")}</span><input value={values.source_timezone} onChange={(event) => update("source_timezone", event.target.value)} /></label>
              <label className="field"><span>Product Code</span><input value={values.product_code} onChange={(event) => update("product_code", event.target.value)} /></label>
            </>
          ) : (
            <>
              <label className="field billing-wide-field"><span>API Token *</span><input autoComplete="new-password" type="password" value={values.api_token} onChange={(event) => update("api_token", event.target.value)} required={!connector?.credentials_configured} /></label>
              {isNewAPI ? <label className="field"><span>{tx("NewAPI 用户 ID")} *</span><input inputMode="numeric" min="1" type="number" value={values.user_id} onChange={(event) => update("user_id", event.target.value)} required /></label> : null}
              {!isNewAPI ? <label className="field"><span>{tx("日志路径")}</span><input value={values.endpoint} onChange={(event) => update("endpoint", event.target.value)} /></label> : null}
              <label className="field"><span>{tx("每币种单位 Quota")}</span><input min="1" type="number" value={values.quota_per_unit} onChange={(event) => update("quota_per_unit", event.target.value)} /></label>
            </>
          )}
          {error ? <div className="billing-inline-error billing-wide-field" role="alert">{error}</div> : null}
        </div>
        <div className="modal-actions"><button className="secondary-button" disabled={busy} onClick={onClose} type="button">{tx("取消")}</button><button className="button" disabled={busy} type="submit">{busy ? tx("保存中") : tx("保存")}</button></div>
      </form>
    </div>
  );
}

function billingConnectorFormValues(connector?: BillingConnector) {
  const config = connector?.config ?? {};
  const connectorType = connector?.type ?? "oneapi";
  return {
    name: connector?.name ?? "",
    type: connectorType,
    base_url: connector?.base_url ?? "",
    schedule_interval_minutes: String(connector?.schedule_interval_minutes ?? 60),
    rate_limit_per_second: config.rate_limit_per_second ?? "5",
    currency: config.currency ?? (connectorType === "aliyun" ? "CNY" : "USD"),
    api_token: "",
    endpoint: config.endpoint ?? "/api/log/self",
    quota_per_unit: config.quota_per_unit ?? "500000",
    user_id: config.user_id ?? "",
    access_key_id: "",
    access_key_secret: "",
    source_timezone: config.source_timezone ?? "Asia/Shanghai",
    product_code: config.product_code ?? "",
    provider_id: config.provider_id ?? "",
    provider_resource_id: config.provider_resource_id ?? "",
  };
}

function billingConnectorPayload(values: ReturnType<typeof billingConnectorFormValues>, editing: boolean) {
  const commonConfig = {
    currency: values.currency,
    rate_limit_per_second: values.rate_limit_per_second,
    provider_id: values.provider_id.trim(),
    provider_resource_id: values.provider_resource_id.trim(),
  };
  const config = values.type === "aliyun"
    ? { ...commonConfig, source_timezone: values.source_timezone, product_code: values.product_code }
    : values.type === "newapi"
      ? { ...commonConfig, quota_per_unit: values.quota_per_unit, user_id: values.user_id }
      : { ...commonConfig, endpoint: values.endpoint, quota_per_unit: values.quota_per_unit };
  const credentials = values.type === "aliyun"
    ? { access_key_id: values.access_key_id, access_key_secret: values.access_key_secret }
    : { api_token: values.api_token };
  const cleanCredentials = Object.fromEntries(Object.entries(credentials).filter(([, value]) => value.trim()));
  const payload: Record<string, unknown> = {
    name: values.name,
    base_url: values.base_url,
    schedule_interval_minutes: Number(values.schedule_interval_minutes) || 0,
    config,
  };
  if (!editing) payload.type = values.type;
  if (Object.keys(cleanCredentials).length > 0) payload.credentials = cleanCredentials;
  return payload;
}

function formatBillingDate(value?: string) {
  if (!value) return "-";
  return new Intl.DateTimeFormat(languageLocale(), { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}

function billingConnectorTypeLabel(type: BillingConnector["type"]) {
  if (type === "aliyun") return "Aliyun";
  if (type === "newapi") return "NewAPI";
  return "OneAPI";
}
