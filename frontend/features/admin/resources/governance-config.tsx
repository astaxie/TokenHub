import { Activity, BarChart3, Bell, Database, FileText, ShieldCheck } from "lucide-react";
import { type AdminResource, type AdminUser, type AlertDelivery, type AlertEvent, type ApiContext, type ApprovalRequest, type FieldConfig, type ResourceConfig, type SQLiteBackup, type ToolbarAction } from "../core/types";
import { roleDisplayLabel, roleSelectOptions, stringifyValue, teamLabel, teamSelectOptions } from "../domain/entities";
import { compactNumber, formatMoney, formatNumber, formatTime } from "../domain/formatting";
import { alertMetricLabel, approvalPayloadSummary, approvalStatusLabel, approvalTriggerLabel, compactList, invoiceStatusLabel, numberFromUnknown, reportDatasetLabel, reportScheduleLabel, resourceTypeLabel, roleLabel } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { genericResourceConfig, runApprovalAction } from "./generic-config";
import { adminDelete, adminFetch, adminMutate, userPayload } from "./payloads";
import { StatusPill } from "../shared/ui";

export function adminUserConfig(): ResourceConfig<AdminUser> {
  return {
    view: "users",
    title: "用户管理",
    eyebrow: "后台用户列表",
    description: "管理 TokenHub 后台登录账号、角色权限、归属团队和账号状态。",
    createLabel: "新增用户",
    columns: [
      { key: "name", label: "姓名" },
      { key: "email", label: "邮箱" },
      { key: "username", label: "用户名" },
      { key: "role", label: "角色", render: (item, ctx) => roleDisplayLabel(ctx, item.role) },
      { key: "team_id", label: "团队", render: (item, ctx) => teamLabel(ctx, item.team_id ?? "") },
      { key: "last_login_at", label: "最近登录", render: (item) => formatTime(item.last_login_at ?? "") },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      { key: "username", label: "用户名", required: true },
      { key: "name", label: "姓名", required: true },
      { key: "email", label: "邮箱", required: true },
      { key: "password", label: "密码", type: "password", placeholder: "编辑时留空则不修改" },
      { key: "role", label: "角色", type: "select", optionsFromData: roleSelectOptions, required: true },
      { key: "team_id", label: "团队", type: "select", optionsFromData: teamSelectOptions },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
    ],
    list: (ctx) => ctx.users,
    create: (ctx, values) => adminMutate(ctx, "/api/admin/users", "POST", userPayload(values, true)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/users/${item.id}`, "PATCH", userPayload(values, false)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/users/${item.id}`),
    actions: [
      {
        label: "发送重置密码邮件",
        title: "向该用户发送重置密码链接",
        run: (ctx, item) => adminMutate(ctx, `/api/admin/users/${item.id}/reset-password-email`, "POST", {}),
        doneMessage: (item) => `${item.name || item.email} 的重置密码邮件已发送`,
      },
    ],
    toolbarActions: [
      {
        label: "导入用户",
        title: "从已有系统导出的 CSV 批量导入或更新用户",
        kind: "import-users",
      },
    ],
    toForm: (item) => ({
      username: item.username,
      name: item.name,
      email: item.email,
      password: "",
      role: item.role,
      team_id: item.team_id ?? "",
      status: item.status,
    }),
  };
}

export function alertRuleConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    {
      key: "metric",
      label: "指标",
      type: "select",
      options: ["provider_health", "provider_resource_health", "request_quota_usage", "token_quota_usage", "cost_quota_usage", "error_rate", "latency_p95"],
      required: true,
    },
    { key: "threshold", label: "阈值", required: true },
    { key: "severity", label: "级别", type: "select", options: ["info", "warning", "critical"], required: true },
    { key: "scope", label: "对象范围", type: "select", options: ["provider", "provider_resource", "quota", "api_key", "project", "model"], required: true },
    { key: "channel", label: "通知渠道" },
  ];
  return {
    ...genericResourceConfig("alert-rules", "告警规则", "Provider 健康、资源实例状态和额度风险的默认告警规则。", fields),
    eyebrow: "规则列表",
    columns: [
      { key: "name", label: "名称" },
      { key: "fields.metric", label: "指标", render: (item) => alertMetricLabel(stringifyValue(item.fields?.metric)) },
      { key: "fields.threshold", label: "阈值", render: (item) => stringifyValue(item.fields?.threshold) || "-" },
      { key: "fields.severity", label: "级别", render: (item) => <StatusPill status={stringifyValue(item.fields?.severity || "warning")} /> },
      { key: "fields.channel", label: "通知渠道", render: (item) => stringifyValue(item.fields?.channel) || "default" },
      { key: "fields.event_codes", label: "触发事件", render: (item) => compactList(item.fields?.event_codes) },
      { key: "fields.managed_by", label: "来源", render: (item) => stringifyValue(item.fields?.managed_by) === "tokenhub_auto" ? tx("系统默认") : tx("自定义") },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
  };
}

export function alertEventConfig(): ResourceConfig<AlertEvent> {
  return {
    view: "alert-events",
    title: "告警事件",
    eyebrow: "告警事件列表",
    description: "运行时触发的额度、成本和 Provider 健康事件。",
    columns: [
      { key: "created_at", label: "时间", render: (item) => formatTime(item.created_at) },
      { key: "severity", label: "级别", render: (item) => <StatusPill status={item.severity} /> },
      { key: "code", label: "事件" },
      { key: "scope_type", label: "对象" },
      { key: "scope_id", label: "对象 ID" },
      { key: "message", label: "说明" },
    ],
    fields: [],
    list: (ctx) => ctx.alerts,
    actions: [
      {
        label: "发送",
        title: "通过默认通知渠道发送该告警",
        run: async (ctx, item) => {
          const resp = await adminFetch(ctx, `/api/admin/alerts/${item.id}/deliver`, {
            method: "POST",
            body: JSON.stringify({}),
          });
          if (!resp.ok) throw new Error(`deliver alert ${resp.status}`);
        },
        doneMessage: (item) => `${item.code} 已发送`,
      },
    ],
  };
}

export function alertDeliveryConfig(): ResourceConfig<AlertDelivery> {
  return {
    view: "alert-deliveries",
    title: "通知记录",
    eyebrow: "通知发送记录",
    description: "查看告警通知的发送状态、目标和失败原因。",
    columns: [
      { key: "created_at", label: "时间", render: (item) => formatTime(item.created_at) },
      { key: "alert_id", label: "告警 ID" },
      { key: "channel", label: "渠道" },
      { key: "target", label: "目标", render: (item) => item.target || "-" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
      { key: "status_code", label: "HTTP", render: (item) => item.status_code || "-" },
      { key: "error", label: "失败原因", render: (item) => item.error || "-" },
    ],
    fields: [],
    list: (ctx) => ctx.alertDeliveries,
  };
}

export function approvalConfig(): ResourceConfig<ApprovalRequest> {
  return {
    view: "approvals",
    title: "审批记录",
    eyebrow: "审批申请列表",
    description: "处理 Key 发放、额度提升和模型开通审批。",
    columns: [
      { key: "created_at", label: "时间", render: (item) => formatTime(item.created_at) },
      { key: "trigger", label: "触发条件", render: (item) => approvalTriggerLabel(item.trigger) },
      { key: "resource_type", label: "对象", render: (item) => resourceTypeLabel(item.resource_type) },
      { key: "requester", label: "申请人", render: (item) => item.requester || item.requester_id || "-" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} label={approvalStatusLabel(item.status)} /> },
      { key: "decided_by", label: "处理人", render: (item) => item.decided_by || "-" },
      { key: "payload", label: "内容", render: (item) => approvalPayloadSummary(item.payload) },
    ],
    fields: [],
    list: (ctx) => ctx.approvals,
    actions: [
      {
        label: "批准",
        title: "批准并执行该申请",
        visible: (item) => item.status === "pending",
        run: async (ctx, item) => runApprovalAction(ctx, item, "approve"),
        doneMessage: (item) => `${approvalTriggerLabel(item.trigger)} 已批准`,
      },
      {
        label: "驳回",
        title: "驳回该申请",
        visible: (item) => item.status === "pending",
        run: async (ctx, item) => runApprovalAction(ctx, item, "reject"),
        doneMessage: (item) => `${approvalTriggerLabel(item.trigger)} 已驳回`,
      },
    ],
  };
}

export function costCenterConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "code", label: "成本中心编码", required: true },
    { key: "owner", label: "负责人" },
    { key: "department", label: "部门" },
    { key: "monthly_budget_usd", label: "月预算 USD", type: "number" },
  ];
  return {
    ...genericResourceConfig("cost-centers", "成本中心", "企业内部部门、成本中心和预算归属配置", fields),
    columns: [
      { key: "code", label: "编码", render: (item) => stringifyValue(item.fields?.code) || item.id },
      { key: "name", label: "名称" },
      { key: "department", label: "部门", render: (item) => stringifyValue(item.fields?.department) || "-" },
      { key: "owner", label: "负责人", render: (item) => stringifyValue(item.fields?.owner) || "-" },
      { key: "monthly_budget_usd", label: "月预算", render: (item) => `$${formatMoney(numberFromUnknown(item.fields?.monthly_budget_usd))}` },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
  };
}

export function chargebackConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "period", label: "账期", required: true },
    { key: "cost_center", label: "成本中心", required: true },
    { key: "project_id", label: "项目 ID" },
    { key: "team_id", label: "团队 ID" },
    { key: "allocated_cost_usd", label: "分摊成本 USD", type: "number" },
    { key: "request_count", label: "请求数", type: "number" },
    { key: "total_tokens", label: "Token", type: "number" },
    { key: "allocation_rule", label: "分摊规则" },
  ];
  return {
    ...genericResourceConfig("chargebacks", "部门分摊", "将模型成本按部门、项目或成本中心进行内部归集", fields),
    columns: [
      { key: "period", label: "账期", render: (item) => stringifyValue(item.fields?.period) },
      { key: "cost_center", label: "成本中心", render: (item) => stringifyValue(item.fields?.cost_center) },
      { key: "project_id", label: "项目", render: (item) => stringifyValue(item.fields?.project_id) || "-" },
      { key: "team_id", label: "团队", render: (item) => stringifyValue(item.fields?.team_id) || "-" },
      { key: "allocated_cost_usd", label: "分摊成本", render: (item) => `$${formatMoney(numberFromUnknown(item.fields?.allocated_cost_usd))}` },
      { key: "request_count", label: "请求", render: (item) => formatNumber(numberFromUnknown(item.fields?.request_count)) },
      { key: "total_tokens", label: "Token", render: (item) => compactNumber(numberFromUnknown(item.fields?.total_tokens)) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
  };
}

export function approvalFlowConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "trigger", label: "触发条件", type: "select", options: ["api_key_create", "budget_change", "model_access", "quota_increase", "invoice_confirm", "invoice_reject"], required: true },
    { key: "approver_role", label: "审批角色", type: "select", options: ["admin", "project_admin", "security_admin"], required: true },
    { key: "threshold_usd", label: "金额阈值 USD", type: "number" },
    { key: "sla_hours", label: "SLA 小时", type: "number" },
  ];
  return {
    ...genericResourceConfig("approval-flows", "审批流", "高成本模型、预算变更、Key 发放和内部账单确认审批配置", fields),
    columns: [
      { key: "name", label: "名称" },
      { key: "trigger", label: "触发条件", render: (item) => approvalTriggerLabel(stringifyValue(item.fields?.trigger)) },
      { key: "approver_role", label: "审批角色", render: (item) => roleLabel(stringifyValue(item.fields?.approver_role)) },
      { key: "threshold_usd", label: "金额阈值", render: (item) => numberFromUnknown(item.fields?.threshold_usd) > 0 ? `$${formatMoney(numberFromUnknown(item.fields?.threshold_usd))}` : "不限" },
      { key: "sla_hours", label: "SLA", render: (item) => numberFromUnknown(item.fields?.sla_hours) > 0 ? `${numberFromUnknown(item.fields?.sla_hours)}h` : "-" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
  };
}

export function reportConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "dataset", label: "数据集", type: "select", options: ["requests", "usage", "cost-centers", "approvals", "audit-events", "alert-deliveries"], required: true },
    { key: "schedule", label: "频率", type: "select", options: ["manual", "daily", "weekly", "monthly"], required: true },
    { key: "recipients", label: "接收人" },
  ];
  return {
    ...genericResourceConfig("reports", "导出报表", "按需导出审计、用量和治理数据集", fields),
    columns: [
      { key: "name", label: "名称" },
      { key: "dataset", label: "数据集", render: (item) => reportDatasetLabel(stringifyValue(item.fields?.dataset)) },
      { key: "schedule", label: "频率", render: (item) => reportScheduleLabel(stringifyValue(item.fields?.schedule)) },
      { key: "recipients", label: "接收人", render: (item) => stringifyValue(item.fields?.recipients) || "-" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    actions: [
      {
        label: "导出",
        title: "导出 CSV 报表",
        run: async (ctx, item) => {
          const dataset = stringifyValue(item.fields?.dataset || "requests");
          const period = reportPeriodPrompt(dataset);
          if (period === null) return;
          const resp = await adminFetch(ctx, reportExportPath(dataset, period));
          if (!resp.ok) throw new Error(`export ${dataset} ${resp.status}`);
          const blob = await resp.blob();
          downloadBlob(blob, reportFilename(dataset, period));
        },
        doneMessage: (item) => `${item.name} 已导出`,
      },
    ],
    toolbarActions: reportExportActions(),
  };
}

export function invoiceConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "period", label: "账期", required: true },
    { key: "cost_center", label: "成本中心", required: true },
    { key: "amount_usd", label: "金额 USD", type: "number" },
    { key: "invoice_note", label: "发票备注", type: "textarea" },
    { key: "confirmed_by", label: "确认人" },
    { key: "confirmed_at", label: "确认时间" },
    { key: "reject_reason", label: "驳回原因", type: "textarea" },
  ];
  const base = genericResourceConfig("invoices", "内部账单", "生成内部账单、备注和成本中心确认记录", fields);
  return {
    ...base,
    columns: [
      { key: "name", label: "名称" },
      { key: "period", label: "账期", render: (item) => stringifyValue(item.fields?.period) },
      { key: "cost_center", label: "成本中心", render: (item) => stringifyValue(item.fields?.cost_center) },
      { key: "amount_usd", label: "金额", render: (item) => `$${formatMoney(numberFromUnknown(item.fields?.amount_usd))}` },
      { key: "invoice_note", label: "发票备注", render: (item) => stringifyValue(item.fields?.invoice_note) || "-" },
      { key: "confirmed_by", label: "确认人", render: (item) => stringifyValue(item.fields?.confirmed_by) || "-" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} label={invoiceStatusLabel(item.status)} /> },
    ],
    actions: [
      {
        label: "确认",
        title: "确认该内部账单",
        run: async (ctx, item) => {
          if (item.status !== "pending") return;
          const resp = await adminFetch(ctx, `/api/admin/resources/invoices/${item.id}/confirm`, {
            method: "POST",
            body: JSON.stringify({ invoice_note: stringifyValue(item.fields?.invoice_note) }),
          });
          if (!resp.ok) throw new Error(`confirm invoice ${resp.status}`);
          await handleApprovalOrJSON(resp);
        },
        doneMessage: (item) => `${item.name} 已确认`,
      },
      {
        label: "驳回",
        title: "驳回该内部账单",
        run: async (ctx, item) => {
          if (item.status !== "pending") return;
          const reason = window.prompt("请输入驳回原因", stringifyValue(item.fields?.reject_reason));
          if (reason === null) return;
          const resp = await adminFetch(ctx, `/api/admin/resources/invoices/${item.id}/reject`, {
            method: "POST",
            body: JSON.stringify({ reject_reason: reason }),
          });
          if (!resp.ok) throw new Error(`reject invoice ${resp.status}`);
          await handleApprovalOrJSON(resp);
        },
        doneMessage: (item) => `${item.name} 已驳回`,
      },
    ],
    toolbarActions: [
      {
        label: "生成本月",
        title: "按当前账期生成分摊和内部账单",
        run: async (ctx) => {
          const period = window.prompt("输入账期 YYYY-MM，留空则生成本月", currentBillingPeriod());
          if (period === null) return;
          const resp = await adminFetch(ctx, "/api/admin/billing/generate", {
            method: "POST",
            body: JSON.stringify({ period: period.trim() }),
          });
          if (!resp.ok) throw new Error(`generate billing ${resp.status}`);
        },
        doneMessage: () => tx("已生成分摊和内部账单"),
      },
    ],
  };
}

export async function handleApprovalOrJSON(resp: Response) {
  if (resp.status === 202) {
    const data = (await resp.json()) as { approval_required?: boolean; approval?: ApprovalRequest };
    if (data.approval_required) {
      window.dispatchEvent(new CustomEvent("tokenhub-issued-key", { detail: `已提交审批：${data.approval?.id ?? ""}` }));
    }
    return;
  }
  await resp.json().catch(() => undefined);
}

export function reportExportDefinitions(): Array<{
  dataset: string;
  label: string;
  description: string;
  icon: typeof Activity;
  tone: string;
}> {
  return [
    {
      dataset: "requests",
      label: reportDatasetLabel("requests"),
      description: "请求 ID、模型、状态码、Provider 路由和延迟",
      icon: FileText,
      tone: "blue",
    },
    {
      dataset: "usage",
      label: reportDatasetLabel("usage"),
      description: "按模型、项目和日期归集 Token 与成本",
      icon: BarChart3,
      tone: "green",
    },
    {
      dataset: "cost-centers",
      label: reportDatasetLabel("cost-centers"),
      description: "成本中心、负责人和部门归属配置",
      icon: Database,
      tone: "slate",
    },
    {
      dataset: "approvals",
      label: reportDatasetLabel("approvals"),
      description: "额度提升、Key 发放和模型开通审批记录",
      icon: ShieldCheck,
      tone: "amber",
    },
    {
      dataset: "audit-events",
      label: reportDatasetLabel("audit-events"),
      description: "后台操作、变更对象、操作人和时间",
      icon: Activity,
      tone: "violet",
    },
    {
      dataset: "alert-deliveries",
      label: reportDatasetLabel("alert-deliveries"),
      description: "告警通知的渠道、目标和发送结果",
      icon: Bell,
      tone: "red",
    },
  ];
}

export function reportExportActions(): ToolbarAction[] {
  return reportExportDefinitions().map(({ dataset, label }) => ({
    label,
    title: `导出 ${label} CSV`,
    run: async (ctx) => {
      await downloadReport(ctx, dataset);
    },
    doneMessage: () => `${label} 已导出`,
  }));
}

export async function downloadReport(ctx: ApiContext, dataset: string) {
  const period = reportPeriodPrompt(dataset);
  if (period === null) return null;
  const resp = await adminFetch(ctx, reportExportPath(dataset, period));
  if (!resp.ok) throw new Error(`export ${dataset} ${resp.status}`);
  const blob = await resp.blob();
  const fileName = reportFilename(dataset, period);
  downloadBlob(blob, fileName);
  return { fileName, period };
}

export function reportPeriodPrompt(dataset: string) {
  if (!["usage", "budgets"].includes(dataset)) return "";
  const period = window.prompt("输入账期 YYYY-MM，留空则导出全部", currentBillingPeriod());
  if (period === null) return null;
  return period.trim();
}

export function reportExportPath(dataset: string, period?: string) {
  const query = period ? `?period=${encodeURIComponent(period)}` : "";
  return `/api/admin/export/${encodeURIComponent(dataset)}${query}`;
}

export function reportFilename(dataset: string, period?: string) {
  return `tokenhub-${dataset}${period ? `-${period}` : ""}.csv`;
}

export function currentBillingPeriod() {
  return new Date().toISOString().slice(0, 7);
}

export async function downloadSQLiteBackup(ctx: ApiContext, item: SQLiteBackup) {
  const resp = await adminFetch(ctx, `/api/admin/sqlite/backups/${item.id}/download`);
  if (!resp.ok) throw new Error(`download sqlite backup ${resp.status}`);
  const blob = await resp.blob();
  downloadBlob(blob, item.file_name || `tokenhub-${item.id}.sqlite3`);
}

export async function restoreSQLiteBackup(ctx: ApiContext, item: SQLiteBackup) {
  const confirmation = window.prompt(`输入 RESTORE ${item.id} 确认恢复该 SQLite 备份`);
  if (confirmation == null) return;
  const resp = await adminFetch(ctx, `/api/admin/sqlite/backups/${item.id}/restore`, {
    method: "POST",
    body: JSON.stringify({ confirmation }),
  });
  if (!resp.ok) throw new Error(`restore sqlite backup ${resp.status}`);
  await resp.json().catch(() => undefined);
}

export function downloadBlob(blob: Blob, filename: string) {
  const url = window.URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  window.URL.revokeObjectURL(url);
}
