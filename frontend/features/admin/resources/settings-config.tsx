import { type AdminResource, type FieldConfig, notificationChannelTypes, type ResourceConfig, type SQLiteBackup, type ViewKey } from "../core/types";
import { notificationChannelLabel, notificationChannelTargetSummary, notificationChannelType, notificationChannelUsesEmail, notificationChannelUsesIncomingWebhook, notificationChannelUsesTelegram, notificationChannelUsesWhatsApp, notificationCredentialSummary } from "../domain/catalog";
import { costCenterLabel, costCenterSelectOptions, oauthDefaultProjectRoleOptions, ownerUserLabel, projectMemberProjectSelectOptions, stringifyValue, teamMemberCount, teamSelectOptions, userSelectOptions } from "../domain/entities";
import { formatBytes, formatNumber, formatTime } from "../domain/formatting";
import { boolLabel, dataScopeLabel, identityProviderDefaultGrantLabel, identityProviderLoginEntryLabel, identityProviderTypeLabel, monitorTargetLabel, numberFromUnknown, numberOr } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { genericResourceConfig } from "./generic-config";
import { adminUserConfig, alertDeliveryConfig, alertEventConfig, alertRuleConfig, approvalConfig, approvalFlowConfig, costCenterConfig, downloadSQLiteBackup, reportConfig, restoreSQLiteBackup } from "./governance-config";
import { adminDelete, adminFetch, adminMutate, identityProviderPayload, notificationChannelPayload } from "./payloads";
import { apiKeyConfig, projectConfig, projectMemberConfig } from "./project-key-config";
import { modelConfig, providerConfig, routeConfig } from "./provider-model-config";
import { StatusPill } from "../shared/ui";
import { identityProviderIconOptions, identityProviderInitialFormValues, identityProviderTemplateOptions } from "../shell/auth";

let cachedResourceConfigs: Partial<Record<ViewKey, ResourceConfig<any>>> | undefined;

export function resourceConfigFor(view: ViewKey) {
  cachedResourceConfigs ??= createResourceConfigs();
  return cachedResourceConfigs[view];
}

function createResourceConfigs(): Partial<Record<ViewKey, ResourceConfig<any>>> {
  return {
  providers: providerConfig(),
  models: modelConfig(),
  routes: routeConfig(),
  projects: projectConfig(),
  "project-members": projectMemberConfig(),
  "api-keys": apiKeyConfig(),
  teams: teamConfig(),
  users: adminUserConfig(),
  "quota-policies": genericResourceConfig("quota-policies", "项目额度", "项目、Key、用户维度的请求、Token、成本与并发上限", [
    { key: "scope", label: "作用域", type: "select", options: ["global", "project", "api_key", "team"], required: true },
    { key: "scope_id", label: "作用域 ID" },
    { key: "daily_requests", label: "日请求", type: "number" },
    { key: "monthly_requests", label: "月请求", type: "number" },
    { key: "daily_tokens", label: "日 Token", type: "number" },
    { key: "monthly_tokens", label: "月 Token", type: "number" },
    { key: "daily_cost_usd", label: "日成本 USD", type: "number" },
    { key: "monthly_cost_usd", label: "月成本 USD", type: "number" },
    { key: "max_concurrency", label: "最大并发", type: "number" },
  ]),
  "cost-centers": costCenterConfig(),
  "approval-flows": approvalFlowConfig(),
  reports: reportConfig(),
  "notification-channels": notificationChannelConfig(),
  monitors: monitorConfig(),
  alerts: alertRuleConfig(),
  "alert-events": alertEventConfig(),
  "alert-deliveries": alertDeliveryConfig(),
  approvals: approvalConfig(),
  "security-policies": genericResourceConfig("security-policies", "安全策略", "敏感数据、错误透传、IP 访问和审计策略", [
    { key: "mask_prompts", label: "脱敏 Prompt", type: "boolean", help: "开启后策略要求请求与响应审计避免直接展示完整 Prompt。" },
    { key: "ip_allowlist", label: "IP 白名单", type: "textarea", placeholder: "127.0.0.1/32\n10.0.0.0/8", help: "每行一个 CIDR 或 IP，留空表示不配置白名单。" },
    { key: "error_passthrough", label: "错误透传", type: "select", options: ["sanitized", "passthrough", "hidden"], required: true },
  ]),
  proxies: genericResourceConfig("proxies", "代理出口", "Provider 出口代理和内网访问策略", [
    { key: "protocol", label: "协议", type: "select", options: ["direct", "http", "https", "socks5"], required: true },
    { key: "host", label: "Host" },
    { key: "port", label: "端口", type: "number" },
  ]),
  "sqlite-backups": sqliteBackupConfig(),
  announcements: genericResourceConfig("announcements", "公告通知", "后台公告、试运行通知和操作提示", [
    { key: "notify_mode", label: "通知模式", type: "select", options: ["silent", "popup"], required: true },
    { key: "target", label: "目标对象" },
  ]),
  "identity-providers": identityProviderConfig(),
  settings: systemSettingConfig(),
  };
}

export function systemSettingConfig(): ResourceConfig<AdminResource> {
  return {
    ...genericResourceConfig("settings", "系统设置", "网关地址、审计保留、企业集成和默认策略", [
      { key: "public_base_url", label: "公开 Base URL" },
      { key: "default_timeout", label: "默认超时" },
      { key: "audit_retention", label: "审计保留" },
      { key: "api_key_prefix", label: "API Key 前缀", placeholder: "sk_", help: "新建和轮换 Key 时使用；建议以 _ 结尾，例如 sk_。" },
      { key: "api_key_random_length", label: "API Key 随机长度", type: "number", placeholder: "48", help: "前缀后面的随机字符数，系统会限制在 24-128 之间。" },
    ]),
    eyebrow: "基础设置",
  };
}

export function identityProviderConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "provider_template", label: "身份源模板", type: "select", options: identityProviderTemplateOptions },
    { key: "provider_type", label: "协议", type: "select", options: ["oidc", "oauth2", "saml", "ldap"], required: true },
    { key: "icon_key", label: "登录图标", type: "select", options: identityProviderIconOptions, help: "auto 会根据名称、Issuer URL 和类型自动选择登录页图标。" },
    { key: "login_label", label: "登录按钮名称", placeholder: "Google", help: "留空时按图标、Issuer 或身份源名称自动推断。" },
    { key: "issuer_url", label: "Issuer URL" },
    { key: "client_id", label: "Client ID" },
    { key: "client_secret", label: "Client Secret", type: "password", help: "编辑时留空则不修改已保存密钥。" },
    { key: "authorize_url", label: "授权端点" },
    { key: "token_url", label: "Token 端点" },
    { key: "userinfo_url", label: "用户信息端点" },
    { key: "redirect_uri", label: "Callback URL", help: "必须与 OAuth 应用中登记的 Redirect URI 完全一致；留空时按当前后端访问地址自动生成。" },
    { key: "scopes", label: "授权范围" },
    { key: "username_claim", label: "用户名 Claim" },
    { key: "email_claim", label: "邮箱 Claim" },
    { key: "team_claim", label: "团队 Claim" },
    { key: "default_role", label: "默认角色", type: "select", options: ["user", "team_leader"], help: "首次 OAuth 登录创建用户时使用；不会覆盖已存在用户角色。" },
    { key: "default_team_id", label: "默认团队", type: "select", optionsFromData: teamSelectOptions, help: "团队字段无法映射时使用。" },
    { key: "default_project_id", label: "默认项目", type: "select", optionsFromData: projectMemberProjectSelectOptions, help: "登录后自动加入该项目空间。" },
    { key: "default_project_role", label: "默认项目角色", type: "select", optionsFromData: oauthDefaultProjectRoleOptions, help: "自动加入默认项目时授予的项目权限。" },
  ];
  const base = genericResourceConfig("identity-providers", "身份源", "配置企业 SSO/OAuth/OIDC 登录和首次登录授权。", fields);
  return {
    ...base,
    eyebrow: "身份源",
    createLabel: "新增身份源",
    columns: [
      { key: "name", label: "名称" },
      { key: "login_entry", label: "登录入口", render: (item) => identityProviderLoginEntryLabel(item) },
      { key: "provider_type", label: "协议", render: (item) => identityProviderTypeLabel(stringifyValue(item.fields?.provider_type)) },
      { key: "issuer_url", label: "Issuer", render: (item) => stringifyValue(item.fields?.issuer_url) || "-" },
      { key: "default_grant", label: "默认授权", render: (item, ctx) => identityProviderDefaultGrantLabel(ctx, item) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    create: (ctx, values) => adminMutate(ctx, "/api/admin/resources/identity-providers", "POST", identityProviderPayload(values, fields)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/resources/identity-providers/${item.id}`, "PATCH", identityProviderPayload(values, fields, item)),
    toForm: (item) => {
      const form = base.toForm?.(item) ?? {};
      form.client_secret = "";
      return identityProviderInitialFormValues(form, false);
    },
  };
}

export function roleConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "role_key", label: "角色标识", required: true, placeholder: "user", readOnlyOnEdit: true },
    { key: "display_name", label: "显示名称", required: true, placeholder: "普通用户" },
    { key: "data_scope", label: "数据范围", type: "select", options: ["global", "team", "project", "self"], required: true },
    { key: "assignable", label: "可分配", type: "boolean" },
  ];
  return {
    ...genericResourceConfig("role-configs", "角色配置", "配置用户管理新增/编辑时可选择的后台角色。权限边界由系统内置角色模型控制。", fields),
    eyebrow: "角色配置",
    createLabel: "新增可选角色",
    columns: [
      { key: "name", label: "名称" },
      { key: "role_key", label: "角色标识", render: (item) => stringifyValue(item.fields?.role_key) || item.id },
      { key: "display_name", label: "显示名称", render: (item) => stringifyValue(item.fields?.display_name) || item.name },
      { key: "data_scope", label: "数据范围", render: (item) => dataScopeLabel(stringifyValue(item.fields?.data_scope)) },
      { key: "assignable", label: "可分配", render: (item) => boolLabel(item.fields?.assignable) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
  };
}

export function teamConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    {
      key: "owner",
      label: "负责人",
      type: "select",
      optionsFromData: userSelectOptions,
      help: "从用户管理中选择团队负责人，用于审批和审计归属。",
    },
    {
      key: "cost_center",
      label: "成本中心",
      type: "select",
      optionsFromData: costCenterSelectOptions,
      help: "费用归集口径，可与团队不同；用于成本归集和用量统计。",
    },
  ];
  return {
    ...genericResourceConfig("teams", "团队分组", "企业团队、负责人和费用归属", fields),
    columns: [
      { key: "name", label: "团队名称" },
      { key: "owner", label: "负责人", render: (item, ctx) => ownerUserLabel(ctx, stringifyValue(item.fields?.owner)) },
      { key: "cost_center", label: "成本中心", render: (item, ctx) => costCenterLabel(ctx, stringifyValue(item.fields?.cost_center)) },
      { key: "members", label: "成员数", render: (item, ctx) => formatNumber(teamMemberCount(ctx, item)) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
      { key: "updated_at", label: "更新时间", render: (item) => formatTime(item.updated_at ?? "") },
    ],
  };
}

export function sqliteBackupConfig(): ResourceConfig<SQLiteBackup> {
  return {
    view: "sqlite-backups",
    title: "数据备份",
    eyebrow: "SQLite 备份",
    description: "创建、下载和恢复 TokenHub SQLite 数据库快照。",
    createLabel: "创建备份",
    columns: [
      { key: "name", label: "名称" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
      { key: "size_bytes", label: "大小", render: (item) => formatBytes(item.size_bytes) },
      { key: "trigger", label: "触发方式", render: (item) => item.trigger || "manual" },
      { key: "created_at", label: "创建时间", render: (item) => formatTime(item.created_at) },
      { key: "restored_at", label: "最近恢复", render: (item) => item.restored_at ? formatTime(item.restored_at) : "-" },
      { key: "checksum_sha256", label: "校验", render: (item) => item.checksum_sha256 ? `${item.checksum_sha256.slice(0, 10)}...` : "-" },
    ],
    fields: [
      { key: "expire_days", label: "保留天数", type: "number", placeholder: "0 表示不过期" },
    ],
    list: (ctx) => ctx.sqliteBackups,
    create: async (ctx, values) => {
      const resp = await adminFetch(ctx, "/api/admin/sqlite/backups", {
        method: "POST",
        body: JSON.stringify({ expire_days: numberOr(values.expire_days, 14) }),
      });
      if (!resp.ok) throw new Error(`create sqlite backup ${resp.status}`);
      await resp.json().catch(() => undefined);
    },
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/sqlite/backups/${item.id}`),
    actions: [
      {
        label: "下载",
        title: "下载 SQLite 备份文件",
        run: async (ctx, item) => downloadSQLiteBackup(ctx, item),
        doneMessage: () => tx("备份文件已开始下载"),
      },
      {
        label: "恢复",
        title: "将数据库恢复到该备份",
        run: async (ctx, item) => restoreSQLiteBackup(ctx, item),
        doneMessage: (item) => `${tx("已恢复备份")} ${item.id}`,
      },
    ],
  };
}

export function notificationChannelConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "type", label: "渠道类型", type: "select", options: notificationChannelTypes, required: true },
    { key: "webhook_url", label: "Webhook URL", required: true, visible: notificationChannelUsesIncomingWebhook },
    { key: "secret", label: "签名密钥", type: "password", help: "可选预留。当前按普通机器人 Webhook 发送，留空不影响通知。", visible: notificationChannelUsesIncomingWebhook },
    { key: "telegram_bot_token", label: "Telegram Bot Token", type: "password", required: true, help: "编辑时留空表示不修改。", visible: notificationChannelUsesTelegram },
    { key: "telegram_chat_id", label: "Telegram Chat ID", required: true, visible: notificationChannelUsesTelegram },
    { key: "telegram_thread_id", label: "Telegram Topic ID", visible: notificationChannelUsesTelegram },
    { key: "whatsapp_phone_number_id", label: "WhatsApp Phone Number ID", required: true, visible: notificationChannelUsesWhatsApp },
    { key: "whatsapp_to", label: "WhatsApp 收件人", required: true, visible: notificationChannelUsesWhatsApp },
    { key: "access_token", label: "Access Token", type: "password", required: true, help: "编辑时留空表示不修改。", visible: notificationChannelUsesWhatsApp },
    { key: "whatsapp_api_version", label: "WhatsApp API Version", visible: notificationChannelUsesWhatsApp },
    { key: "smtp_host", label: "SMTP Host", required: true, visible: notificationChannelUsesEmail },
    { key: "smtp_port", label: "SMTP 端口", type: "number", required: true, visible: notificationChannelUsesEmail },
    { key: "smtp_username", label: "SMTP 用户名", visible: notificationChannelUsesEmail },
    { key: "smtp_password", label: "SMTP 密码", type: "password", help: "编辑时留空表示不修改。", visible: notificationChannelUsesEmail },
    { key: "smtp_from", label: "发件人", required: true, visible: notificationChannelUsesEmail },
    { key: "email_to", label: "收件人", required: true, help: "多个收件人用英文逗号分隔。", visible: notificationChannelUsesEmail },
  ];
  const config = genericResourceConfig(
    "notification-channels",
    "通知渠道",
    "按 Webhook、Slack、Discord、Telegram、WhatsApp、飞书、钉钉、企业微信和邮件快速配置告警通知目标。",
    fields,
  );
  return {
    ...config,
    eyebrow: "渠道配置",
    createLabel: "配置通知渠道",
    columns: [
      { key: "name", label: "名称" },
      { key: "fields.type", label: "渠道", render: (item) => notificationChannelLabel(notificationChannelType(item)) },
      { key: "fields.webhook_url", label: "目标", render: (item) => notificationChannelTargetSummary(item) },
      { key: "fields.secret", label: "凭证", render: (item) => notificationCredentialSummary(item) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
      { key: "updated_at", label: "更新时间", render: (item) => formatTime(item.updated_at ?? "") },
    ],
    create: (ctx, values) => adminMutate(ctx, "/api/admin/resources/notification-channels", "POST", notificationChannelPayload(values)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/resources/notification-channels/${item.id}`, "PATCH", notificationChannelPayload(values, item)),
    toForm: (item) => ({
      name: item.name,
      description: item.description ?? "",
      status: item.status,
      type: notificationChannelType(item),
      webhook_url: stringifyValue(item.fields?.webhook_url),
      secret: "",
      telegram_bot_token: "",
      telegram_chat_id: stringifyValue(item.fields?.telegram_chat_id || item.fields?.chat_id),
      telegram_thread_id: stringifyValue(item.fields?.telegram_thread_id || item.fields?.message_thread_id),
      whatsapp_phone_number_id: stringifyValue(item.fields?.whatsapp_phone_number_id || item.fields?.phone_number_id),
      whatsapp_to: stringifyValue(item.fields?.whatsapp_to || item.fields?.recipient || item.fields?.to),
      access_token: "",
      whatsapp_api_version: stringifyValue(item.fields?.whatsapp_api_version || item.fields?.api_version || "v20.0"),
      smtp_host: stringifyValue(item.fields?.smtp_host),
      smtp_port: stringifyValue(item.fields?.smtp_port),
      smtp_username: stringifyValue(item.fields?.smtp_username),
      smtp_password: "",
      smtp_from: stringifyValue(item.fields?.smtp_from),
      email_to: stringifyValue(item.fields?.email_to),
    }),
  };
}

export function monitorConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    { key: "target_type", label: "目标类型", type: "select", options: ["provider", "resource", "model"], required: true },
    { key: "provider_id", label: "Provider ID" },
    { key: "provider_resource_id", label: "资源实例 ID" },
    { key: "model", label: "模型" },
    { key: "interval_seconds", label: "间隔秒数", type: "number" },
  ];
  const config = genericResourceConfig(
    "monitors",
    "健康检测",
    "系统自动检测 Provider、资源实例和模型路由状态，帮助确认模型 API 链路是否可用。",
    fields,
  );
  return {
    ...config,
    eyebrow: "默认检测项",
    columns: [
      { key: "name", label: "名称" },
      { key: "fields.target_type", label: "目标类型", render: (item) => monitorTargetLabel(item.fields) },
      { key: "fields.provider_id", label: "Provider", render: (item) => stringifyValue(item.fields?.provider_id) || "-" },
      { key: "fields.provider_resource_id", label: "资源实例", render: (item) => stringifyValue(item.fields?.provider_resource_id) || "-" },
      { key: "fields.model", label: "模型", render: (item) => stringifyValue(item.fields?.model) || "-" },
      { key: "fields.last_status", label: "最近状态", render: (item) => <StatusPill status={stringifyValue(item.fields?.last_status || item.fields?.last_result || "unknown")} /> },
      { key: "fields.last_message", label: "最近消息", render: (item) => stringifyValue(item.fields?.last_message) || "-" },
      { key: "fields.latency_ms", label: "延迟", render: (item) => `${numberFromUnknown(item.fields?.latency_ms)}ms` },
      { key: "fields.last_checked_at", label: "最近检测", render: (item) => formatTime(stringifyValue(item.fields?.last_checked_at)) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    create: undefined,
    update: undefined,
    remove: undefined,
    actions: [
      {
        label: "立即检测",
        title: "立即执行该健康检测",
        run: async (ctx, item) => {
          const resp = await adminFetch(ctx, `/api/admin/resources/monitors/${item.id}/run`, {
            method: "POST",
            body: JSON.stringify({}),
          });
          if (!resp.ok) throw new Error(`run monitor ${resp.status}`);
          await resp.json().catch(() => undefined);
        },
        doneMessage: (item) => `${item.name} 已完成检测`,
      },
    ],
  };
}
