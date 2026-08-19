import { type AdminResource, type AdminUser, type AppData, type SettingsTabKey } from "../core/types";
import { fieldSummary, projectName, stringifyValue, teamLabel } from "./entities";
import { routeStrategyLabel } from "./formatting";
import { displayText, tx } from "../i18n/runtime";
import { identityProviderTemplateLabel, normalizedIdentityProviderIconKey } from "../shared/auth";

export function compactList(value: unknown) {
  const values = Array.isArray(value) ? value.map(stringifyValue) : splitList(stringifyValue(value));
  if (values.length === 0) return "-";
  if (values.length <= 3) return values.join(", ");
  return `${values.slice(0, 3).join(", ")} +${values.length - 3}`;
}

export function boolLabel(value: unknown) {
  const text = stringifyValue(value).trim().toLowerCase();
  return text === "false" || text === "0" || text === "no" ? tx("否") : tx("是");
}

export function settingsTabLabel(tab: SettingsTabKey) {
  const labels: Record<SettingsTabKey, string> = {
    settings: "基础设置",
    "role-configs": "角色配置",
    "identity-providers": "身份源",
  };
  return tx(labels[tab]);
}

export function identityProviderTypeLabel(type: string) {
  const labels: Record<string, string> = {
    oidc: "OIDC",
    oauth2: "OAuth2",
    saml: "SAML",
    ldap: "LDAP",
  };
  return labels[type] ?? (type || "-");
}

export function identityProviderIconLabel(iconKey: string) {
  const normalized = normalizedIdentityProviderIconKey(iconKey);
  const labels: Record<string, string> = {
    auto: "自动",
    dingtalk: "DingTalk",
    feishu: "Feishu",
    wecom: "WeCom",
    gitlab: "GitLab",
    github: "GitHub",
    google: "Google",
    microsoft: "Microsoft",
    okta: "Okta",
    keycloak: "Keycloak",
    oidc: "OIDC",
    oauth2: "OAuth2",
    saml: "SAML",
    ldap: "LDAP",
    sso: "SSO",
  };
  return labels[normalized] ?? (iconKey || "-");
}

export function identityProviderLoginEntryLabel(item: AdminResource) {
  const icon = identityProviderIconLabel(stringifyValue(item.fields?.icon_key));
  const label = stringifyValue(item.fields?.login_label) || item.name;
  return [icon, label].filter((value) => value && value !== "-").join(" / ") || "-";
}

export function identityProviderDefaultGrantLabel(data: AppData, item: AdminResource) {
  const role = roleLabel(stringifyValue(item.fields?.default_role) || "user");
  const team = teamLabel(data, stringifyValue(item.fields?.default_team_id));
  const project = projectName(data, stringifyValue(item.fields?.default_project_id));
  const parts = [role];
  if (team !== "-") parts.push(team);
  if (project !== "-") parts.push(project);
  return parts.join(" / ");
}

export function dataScopeLabel(scope: string) {
  const labels: Record<string, string> = {
    global: "全局",
    team: "团队",
    project: "项目",
    self: "本人",
    all: "所有项目",
    include: "仅指定项目",
    exclude: "排除指定项目",
  };
  return tx(labels[scope] ?? (scope || "-"));
}

export function enumOptionLabel(fieldKey: string, value: string) {
  return fieldValueLabel(fieldKey, value);
}

export function enumValueLabel(value: string | undefined) {
  const normalized = String(value ?? "").trim().toLowerCase();
  if (!normalized) return "-";
  const labels: Record<string, string> = {
    active: "启用",
    disabled: "停用",
    revoked: "已吊销",
    draft: "草稿",
    archived: "已归档",
    pending: "待处理",
    approved: "已批准",
    rejected: "已驳回",
    confirmed: "已确认",
    healthy: "健康",
    ok: "正常",
    success: "成功",
    warning: "告警",
    degraded: "降级",
    error: "异常",
    failed: "失败",
    down: "不可用",
    expired: "已过期",
    unknown: "未知",
    global: "全局",
    project: "项目",
    api_key: "普通 API Key 资源",
    openai_subscription: "OpenAI Subscription 账号",
    oauth: "OAuth 账号",
    personal_access_token: "Personal Access Token",
    team: "团队",
    self: "本人",
    provider: "Provider",
    model: "模型",
    resource: "资源",
    provider_resource: "Provider 资源",
    direct: "直连",
    http: "HTTP",
    https: "HTTPS",
    socks5: "SOCKS5",
    silent: "静默",
    popup: "弹窗",
    manual: "手动",
    daily: "每日",
    weekly: "每周",
    monthly: "每月",
    quarterly: "每季度",
    yearly: "每年",
    block: "超额拦截",
    warn: "仅告警",
    webhook: "Webhook",
    feishu: "飞书",
    dingtalk: "钉钉",
    wecom: "企业微信",
    slack: "Slack",
    discord: "Discord",
    telegram: "Telegram",
    whatsapp: "WhatsApp",
    email: "邮件",
    requests: "请求日志",
    usage: "用量归因",
    "cost-centers": "成本中心",
    budgets: "预算",
    chargebacks: "部门分摊",
    invoices: "内部账单",
    approvals: "审批记录",
    "audit-events": "操作审计",
    "alert-deliveries": "通知记录",
    api_key_create: "Key 发放",
    budget_change: "预算变更",
    model_access: "模型开通",
    quota_increase: "额度提升",
    invoice_confirm: "账单确认",
    invoice_reject: "账单驳回",
    balanced: "平衡",
    adaptive: "自适应",
    quality: "质量优先",
    cost: "成本优先",
    priority_weighted: "优先级 + 权重",
    priority_only: "仅优先级",
    inherit: "继承 Provider 策略",
    preserve: "保留归因块",
    strip: "移除归因块",
    text: "文本",
    chat: "文本对话",
    embedding: "向量嵌入",
    image: "图像",
    video: "视频",
    audio: "音频",
    pdf: "PDF",
    ocr: "OCR",
    rerank: "重排序",
  };
  return tx(labels[normalized] ?? roleLabel(normalized) ?? value ?? "-");
}

export function fieldValueLabel(fieldKey: string, value: unknown): string {
  if (Array.isArray(value)) return value.map((item) => fieldValueLabel(fieldKey, item)).join(", ");
  const text = stringifyValue(value).trim();
  if (!text) return "-";
  const normalizedKey = fieldKey.toLowerCase();
  if (normalizedKey.includes("role")) return roleLabel(text);
  if (normalizedKey.includes("scope")) return dataScopeLabel(text);
  if (normalizedKey === "provider_type") return identityProviderTypeLabel(text);
  if (normalizedKey.includes("provider_type")) return providerTypeLabel(text);
  if (normalizedKey === "provider_template") return identityProviderTemplateLabel(text);
  if (normalizedKey === "icon_key") return identityProviderIconLabel(text);
  if (normalizedKey === "status" || normalizedKey.includes("status")) return enumValueLabel(text);
  if (normalizedKey === "strategy") return routeStrategyLabel(text);
  if (normalizedKey === "claude_code_attribution_policy") return enumValueLabel(text);
  if (normalizedKey === "codex_fingerprint_mode") {
    return tx({
      off: "关闭（透传）",
      device: "仅收敛设备",
      session: "收敛设备与会话（推荐）",
      full: "完全收敛",
    }[text.toLowerCase()] ?? text);
  }
  if (normalizedKey === "trigger") return approvalTriggerLabel(text);
  if (normalizedKey === "dataset") return reportDatasetLabel(text);
  if (normalizedKey === "reasoning_effort_unsupported") {
    return tx({ passthrough: "透传", omit: "删除参数", reject: "拒绝请求" }[text.toLowerCase()] ?? text);
  }
  if (normalizedKey === "schedule" || normalizedKey === "period") return enumValueLabel(text);
  if (normalizedKey === "enforcement") return budgetEnforcementLabel(text);
  if (normalizedKey === "type" || normalizedKey === "notify_mode" || normalizedKey === "protocol" || normalizedKey === "target_type" || normalizedKey === "resource_type" || normalizedKey === "modality") {
    return enumValueLabel(text);
  }
  return text;
}

export function fieldKeyLabel(key: string) {
  const labels: Record<string, string> = {
    role_key: "角色标识",
    display_name: "显示名称",
    data_scope: "数据范围",
    permissions: "权限点",
    menu_scopes: "菜单范围",
    assignable: "可分配",
    scope: "作用域",
    scope_id: "作用域 ID",
    type: "类型",
    protocol: "协议",
    notify_mode: "通知模式",
    trigger: "触发条件",
    approver_role: "审批角色",
    dataset: "数据集",
    schedule: "频率",
    enforcement: "管控模式",
    period: "周期",
    target_type: "目标类型",
    webhook_url: "Webhook URL",
    secret: "签名密钥",
    smtp_host: "SMTP Host",
    smtp_port: "SMTP 端口",
    smtp_username: "SMTP 用户名",
    smtp_password: "SMTP 密码",
    smtp_from: "发件人",
    email_to: "收件人",
    telegram_bot_token: "Telegram Bot Token",
    telegram_chat_id: "Telegram Chat ID",
    telegram_thread_id: "Telegram Topic ID",
    whatsapp_phone_number_id: "WhatsApp Phone Number ID",
    whatsapp_to: "WhatsApp 收件人",
    access_token: "Access Token",
    whatsapp_api_version: "WhatsApp API Version",
  };
  return tx(labels[key] ?? key);
}

export function monitorTargetLabel(fields?: Record<string, unknown>) {
  const target = stringifyValue(fields?.target_type || "").toLowerCase();
  const labels: Record<string, string> = {
    provider: "Provider",
    resource: "资源实例",
    provider_resource: "资源实例",
    model: "模型路由",
  };
  return tx(labels[target] ?? (target || "-"));
}

export function alertMetricLabel(metric: string) {
  const labels: Record<string, string> = {
    provider_health: "Provider 健康",
    provider_resource_health: "资源实例健康",
    request_quota_usage: "请求额度",
    token_quota_usage: "Token 额度",
    cost_quota_usage: "成本额度",
    daily_cost_usd: "日成本额度",
    daily_tokens: "日 Token 额度",
    error_rate: "错误率",
    latency_p95: "P95 延迟",
  };
  return tx(labels[metric] ?? (metric || "-"));
}

export function parseLooseValue(value: string) {
  if (value === "true") return true;
  if (value === "false") return false;
  return value;
}

export function roleLabel(role: string) {
  const labels: Record<string, string> = {
    admin: "系统管理员",
    system_admin: "系统管理员",
    security: "安全审计",
    security_admin: "安全审计",
    team_leader: "团队 Leader",
    project_admin: "团队 Leader",
    user: "普通用户",
    member: "普通用户",
    viewer: "普通用户",
    readonly: "普通用户",
    read_only: "普通用户",
  };
  return tx(labels[role] ?? role);
}

export function providerTypeLabel(type: string | undefined) {
  const normalized = String(type ?? "").trim().toLowerCase();
  if (!normalized) return "-";
  const labels: Record<string, string> = {
    mock: "模拟渠道",
    openai: "OpenAI 官方",
    openai_codex: "Codex Subscription",
    openai_compatible: "OpenAI 兼容",
    azure_openai: "Azure OpenAI",
    anthropic: "Claude / Anthropic",
    gemini: "Gemini / Google",
    deepseek: "DeepSeek",
    qwen: "Qwen / 通义千问",
    local: "本地模型",
    kronk: "Kronk 本地推理",
  };
  return tx(labels[normalized] ?? type ?? "-");
}

export function budgetScopeLabel(scope: string) {
  const labels: Record<string, string> = {
    project: "项目",
    team: "团队",
    cost_center: "成本中心",
    "cost-center": "成本中心",
  };
  return tx(labels[scope] ?? scope);
}

export function budgetEnforcementLabel(value: string) {
  const labels: Record<string, string> = {
    block: "超额拦截",
    enforce: "超额拦截",
    warn: "仅告警",
    monitor: "仅告警",
    off: "关闭",
    disabled: "关闭",
  };
  return tx(labels[value || "block"] ?? value);
}

export function reportDatasetLabel(dataset: string) {
  const labels: Record<string, string> = {
    requests: "请求日志",
    usage: "用量归因",
    "cost-centers": "成本中心",
    budgets: "预算",
    chargebacks: "部门分摊",
    invoices: "内部账单",
    approvals: "审批记录",
    "audit-events": "操作审计",
    "alert-deliveries": "通知记录",
  };
  return tx(labels[dataset] ?? dataset);
}

export function reportScheduleLabel(schedule: string) {
  const labels: Record<string, string> = {
    manual: "手动",
    daily: "每日",
    weekly: "每周",
    monthly: "每月",
  };
  return tx(labels[schedule] ?? schedule);
}

export function actionLabel(action: string) {
  const labels: Record<string, string> = {
    create: "新增",
    update: "编辑",
    delete: "删除",
    test: "测试",
    health: "健康变更",
    confirm: "确认",
    reject: "驳回",
    export: "导出",
  };
  return tx(labels[action] ?? action);
}

export function resourceTypeLabel(type: string) {
  const labels: Record<string, string> = {
    provider: "Provider",
    provider_resource: "Provider",
    project: "项目",
    api_key: "API Key",
    model: "模型",
    routing_rule: "路由",
    users: "用户",
    "quota-policies": "项目额度",
    "security-policies": "安全策略",
    "alert-rules": "告警规则",
  };
  return tx(labels[type] ?? type);
}

export function approvalTriggerLabel(trigger: string) {
  const labels: Record<string, string> = {
    api_key_create: "Key 发放",
    budget_change: "预算变更",
    model_access: "模型开通",
    quota_increase: "额度提升",
    invoice_confirm: "账单确认",
    invoice_reject: "账单驳回",
  };
  return tx(labels[trigger] ?? trigger);
}

export function approvalStatusLabel(status: string) {
  const labels: Record<string, string> = {
    pending: "待审批",
    approved: "已批准",
    rejected: "已驳回",
  };
  return tx(labels[status] ?? status);
}
export function approvalPayloadSummary(payload?: string) {
  if (!payload) return "-";
  try {
    const data = JSON.parse(payload) as Record<string, unknown>;
    const parts = [
      stringifyValue(data.name),
      stringifyValue(data.project_id),
      stringifyValue(data.kind),
      stringifyValue(data.resource_id),
    ].filter(Boolean);
    if (data.fields && typeof data.fields === "object") {
      parts.push(fieldSummary(data.fields as Record<string, unknown>));
    }
    return parts.filter((item) => item !== "-").slice(0, 3).join(" / ") || "-";
  } catch {
    return payload.slice(0, 80);
  }
}

export function userInitial(user: AdminUser) {
  const source = displayText(user.name) || user.username || user.email || "U";
  return source.trim().slice(0, 1).toUpperCase();
}

export function numberOr(value: string | undefined, fallback: number) {
  const num = Number(value);
  return Number.isFinite(num) ? num : fallback;
}

export function numberFromUnknown(value: unknown) {
  if (typeof value === "number") return value;
  if (typeof value === "string") return numberOr(value, 0);
  return 0;
}

export function splitList(value: string | undefined) {
  return (value ?? "")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}
