import { appRole } from "../core/navigation";
import { type AdminUser, type AppData, type UsageBreakdownRow } from "../core/types";
import { costCenterLabel, projectName, providerCostDetailRows, teamLabel, usageMemberLabel } from "../domain/entities";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { countWithUnit, displayText, languageLocale, tx } from "../i18n/runtime";
import { DataSection, SimpleTable } from "../shared/ui";

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
            columns={["模型", "请求", "Token", "缓存读", "成本"]}
            paginationKey="usage-models"
            rows={modelBreakdown.map((row) => [
              row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              compactNumber(row.cached_input_tokens ?? 0),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
        <DataSection title={showMemberBreakdown ? "成员用量" : "项目归因"}>
          <SimpleTable
            columns={[showMemberBreakdown ? "成员" : "项目", "请求", "Token", "缓存读", "成本"]}
            paginationKey={showMemberBreakdown ? "usage-members" : "usage-projects"}
            rows={(showMemberBreakdown ? data.breakdown.members ?? [] : data.breakdown.projects ?? []).map((row) => [
              showMemberBreakdown ? usageMemberLabel(data, row.id) : row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              compactNumber(row.cached_input_tokens ?? 0),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
      </div>
      {showMemberBreakdown ? (
        <DataSection title="项目归因">
          <SimpleTable
            columns={["项目", "请求", "Token", "缓存读", "成本"]}
            paginationKey="usage-projects"
            rows={(data.breakdown.projects ?? []).map((row) => [
              projectName(data, row.id),
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              compactNumber(row.cached_input_tokens ?? 0),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
      ) : null}
    </>
  );
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
            <th>{tx("请求")}</th>
            <th>{tx("输入 Token")}</th>
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
                <td>{formatNumber(row.request_count)}</td>
                <td>{compactNumber(row.input_tokens)}</td>
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

export function BillingView({ data, user }: { data: AppData; user: AdminUser }) {
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
