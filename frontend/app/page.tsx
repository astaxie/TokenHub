"use client";

import {
  Activity,
  AlertCircle,
  BarChart3,
  Bell,
  Boxes,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  ChevronsLeft,
  ChevronsRight,
  Check,
  CircleDollarSign,
  Code2,
  Copy,
  Database,
  FileText,
  Fingerprint,
  Gauge,
  Globe2,
  GripVertical,
  KeyRound,
  LayoutDashboard,
  LogOut,
  LockKeyhole,
  Eye,
  EyeOff,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  Search,
  Send,
  Server,
  Settings,
  ShieldCheck,
  Sparkles,
  Sun,
  Trash2,
  UserRoundCheck,
  Users,
  WalletCards,
  X,
} from "lucide-react";
import { Fragment, type Dispatch, type FormEvent, type SetStateAction, useEffect, useMemo, useRef, useState } from "react";

type Summary = {
  request_count: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
  errors: number;
  usage_record_count?: number;
  api_key_count?: number;
  route_count?: number;
  active_route_count?: number;
  user_count?: number;
};

const DEFAULT_PROJECT_ID = "prj_default";

type Project = {
  id: string;
  name: string;
  team_id?: string;
  owner_user_id?: string;
  cost_center?: string;
  status: string;
  default_quota_ref?: string;
  created_at?: string;
};

type APIKey = {
  id: string;
  project_id: string;
  name: string;
  group?: string;
  key_prefix: string;
  key_suffix: string;
  allowed_models: string[];
  ip_allowlist?: string[];
  status: string;
  limits?: Record<string, number>;
  expires_at?: string;
  rotated_from_id?: string;
  grace_until?: string;
  last_used_at?: string;
};

type Provider = {
  id: string;
  name: string;
  type: string;
  base_url?: string;
  status: string;
  healthy: boolean;
  priority: number;
  options?: Record<string, string>;
};

type ProviderCatalogModel = {
  id: string;
  name: string;
  display_name?: string;
  canonical_name?: string;
  category?: string;
  family?: string;
  type?: string;
  context_window?: number;
  max_output_tokens?: number;
  input_price_usd_per_1m?: number;
  output_price_usd_per_1m?: number;
  input_modalities?: string[];
  output_modalities?: string[];
  capabilities?: string[];
  supported_parameters?: string[];
  last_updated?: string;
};

type ProviderCatalogEntry = {
  id: string;
  name: string;
  display_name: string;
  type: string;
  base_url?: string;
  doc_url?: string;
  categories?: string[];
  category_counts?: Record<string, number>;
  models_count: number;
  source: string;
  models?: ProviderCatalogModel[];
};

type ProviderResource = {
  id: string;
  provider_id: string;
  name: string;
  group?: string;
  resource_type: string;
  base_url?: string;
  region?: string;
  environment?: string;
  status: string;
  healthy: boolean;
  priority: number;
  weight: number;
  rate_limit_rpm?: number;
  token_limit_tpm?: number;
  max_concurrency?: number;
  options?: Record<string, string>;
  credential_summary?: Record<string, string>;
  failure_count?: number;
  cooldown_until?: string;
  last_used_at?: string;
  last_checked_at?: string;
  created_at?: string;
  updated_at?: string;
};

type Model = {
  id: string;
  name: string;
  category?: string;
  family: string;
  modality: string;
  context_window?: number;
  status: string;
  input_price_usd_per_1m?: number;
  output_price_usd_per_1m?: number;
  embedding_price_usd_per_1m?: number;
  input_modalities?: string[];
  output_modalities?: string[];
  capabilities?: string[];
  supported_parameters?: string[];
  metadata?: Record<string, string>;
};

type ModelRoute = {
  id: string;
  model_name: string;
  provider_id: string;
  provider_resource_id?: string;
  resource_group?: string;
  sticky_session?: boolean;
  provider_model: string;
  priority: number;
  weight: number;
  quality_score?: number;
  cost_score?: number;
  status: string;
  strategy?: string;
  last_used_at?: string;
};

type ChatRole = "system" | "user" | "assistant";

type PlaygroundMessage = {
  id: string;
  role: ChatRole;
  content: string;
};

type PlaygroundRouteSummary = {
  route_id?: string;
  provider_id?: string;
  provider_name?: string;
  resource_id?: string;
  resource_name?: string;
  provider_model?: string;
  priority?: number;
  resource_priority?: number;
  weight?: number;
  quality_score?: number;
  cost_score?: number;
  strategy?: string;
};

type PlaygroundUsage = {
  prompt_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  estimated_cost_usd?: number;
};

type PlaygroundRouteAttempt = {
  route: PlaygroundRouteSummary;
  status: number;
  code?: string;
  error?: string;
};

type PlaygroundChatPayload = {
  response?: {
    choices?: Array<{
      message?: {
        role?: string;
        content?: unknown;
      };
      text?: unknown;
    }>;
    output_text?: unknown;
    content?: unknown;
  };
  route?: PlaygroundRouteSummary;
  usage?: PlaygroundUsage;
  attempts?: PlaygroundRouteAttempt[];
  request_id?: string;
};

type ApiExampleLanguage = "python" | "typescript" | "java" | "go";

type AdminResource = {
  id: string;
  kind: string;
  name: string;
  description?: string;
  status: string;
  fields?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
};

type AdminUser = {
  id: string;
  username: string;
  name: string;
  email: string;
  role: string;
  team_id?: string;
  status: string;
  created_at?: string;
  updated_at?: string;
  last_login_at?: string;
};

type LoginIdentityProvider = {
  id: string;
  name: string;
  display_name?: string;
  provider_type: string;
  issuer_url?: string;
  icon_key?: string;
};

type AlertEvent = {
  id: string;
  severity: string;
  code: string;
  message: string;
  scope_type?: string;
  scope_id?: string;
  created_at: string;
};

type AlertDelivery = {
  id: string;
  alert_id: string;
  channel_id?: string;
  channel: string;
  target?: string;
  status: string;
  status_code?: number;
  error?: string;
  created_at: string;
};

type ReportExportHistoryItem = {
  id: string;
  dataset: string;
  file_name: string;
  exported_at: string;
  period?: string;
};

type ApprovalRequest = {
  id: string;
  flow_id?: string;
  trigger: string;
  resource_type: string;
  resource_id?: string;
  requester_id?: string;
  requester?: string;
  status: string;
  reason?: string;
  payload?: string;
  created_at: string;
  decided_at?: string;
  decided_by?: string;
};

type SQLiteBackup = {
  id: string;
  name: string;
  file_name: string;
  status: string;
  trigger: string;
  size_bytes: number;
  checksum_sha256?: string;
  created_by?: string;
  created_at: string;
  expires_at?: string;
  restored_by?: string;
  restored_at?: string;
  error?: string;
};

type RequestLog = {
  id: string;
  request_id: string;
  project_id: string;
  api_key_id: string;
  model: string;
  provider_id?: string;
  provider_resource_id?: string;
  provider_model?: string;
  status_code: number;
  error_code?: string;
  latency_ms: number;
  client_ip?: string;
  user_agent?: string;
  created_at: string;
};

type UsageRecord = {
  id: string;
  request_id: string;
  project_id: string;
  api_key_id: string;
  model: string;
  provider_id?: string;
  provider_resource_id?: string;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
  created_at: string;
};

type RouteAttemptLog = {
  id: string;
  request_id: string;
  attempt_index: number;
  route_id?: string;
  provider_id?: string;
  provider_resource_id?: string;
  provider_model?: string;
  status_code: number;
  error_code?: string;
  error_message?: string;
  created_at: string;
};

type RequestPayloadLog = {
  id: string;
  request_id: string;
  request_body?: string;
  response_body?: string;
  request_truncated: boolean;
  response_truncated: boolean;
  created_at: string;
};

type RequestDetail = {
  log: RequestLog;
  usage: UsageRecord[];
  attempts: RouteAttemptLog[];
  payload?: RequestPayloadLog | null;
};

type AuditEvent = {
  id: string;
  actor_user_id?: string;
  actor_name?: string;
  actor_role?: string;
  action: string;
  resource_type: string;
  resource_id: string;
  status: string;
  message?: string;
  ip?: string;
  user_agent?: string;
  created_at: string;
};

type UsageBreakdownRow = {
  id: string;
  request_count: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
};

type UsageBreakdown = {
  projects: UsageBreakdownRow[];
  models: UsageBreakdownRow[];
  members: UsageBreakdownRow[];
  providers: UsageBreakdownRow[];
  provider_resources: UsageBreakdownRow[];
  cost_centers: UsageBreakdownRow[];
};

type UsagePoint = {
  date: string;
  request_count: number;
  input_tokens: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
};

type ViewKey =
  | "overview"
  | "playground"
  | "gateway"
  | "providers"
  | "models"
  | "routes"
  | "projects"
  | "project-members"
  | "api-keys"
  | "teams"
  | "users"
  | "quota-policies"
  | "cost-centers"
  | "budgets"
  | "chargebacks"
  | "approval-flows"
  | "approvals"
  | "invoices"
  | "reports"
  | "usage"
  | "billing"
  | "audit"
  | "monitors"
  | "alerts"
  | "alert-events"
  | "notification-channels"
  | "alert-deliveries"
  | "security-policies"
  | "proxies"
  | "sqlite-backups"
  | "announcements"
  | "identity-providers"
  | "settings";

const viewRoutes: Record<ViewKey, string> = {
  overview: "/overview",
  playground: "/playground",
  gateway: "/gateway",
  providers: "/providers",
  models: "/models",
  routes: "/routes",
  projects: "/projects",
  "project-members": "/project-members",
  "api-keys": "/api-keys",
  teams: "/teams",
  users: "/users",
  "quota-policies": "/quota-policies",
  "cost-centers": "/cost-centers",
  budgets: "/budgets",
  chargebacks: "/chargebacks",
  "approval-flows": "/approval-flows",
  approvals: "/approvals",
  invoices: "/invoices",
  reports: "/reports",
  usage: "/usage",
  billing: "/billing",
  audit: "/audit",
  monitors: "/monitors",
  alerts: "/alerts",
  "alert-events": "/alert-events",
  "notification-channels": "/notification-channels",
  "alert-deliveries": "/alert-deliveries",
  "security-policies": "/security-policies",
  proxies: "/proxies",
  "sqlite-backups": "/sqlite-backups",
  announcements: "/announcements",
  "identity-providers": "/identity-providers",
  settings: "/settings",
};

const routeViews = Object.fromEntries(
  Object.entries(viewRoutes).map(([view, route]) => [route.replace(/^\//, ""), view]),
) as Record<string, ViewKey>;

const notificationChannelTypes = ["webhook", "slack", "discord", "telegram", "whatsapp", "feishu", "dingtalk", "wecom", "email"];

type NavLeafItem = {
  view: ViewKey;
  label: string;
  icon: typeof Activity;
};

type NavParentItem = {
  label: string;
  icon: typeof Activity;
  children: NavLeafItem[];
};

type NavItem = NavLeafItem | NavParentItem;

type AppRole = "admin" | "security" | "team_leader" | "user";

type TopSearchItem = {
  id: string;
  view: ViewKey;
  label: string;
  group: string;
  description: string;
  icon: typeof Activity;
  tone?: "page" | "entity" | "recent";
  keywords: string;
};

type FieldType = "text" | "number" | "password" | "textarea" | "select" | "multi-select" | "tags" | "boolean";

type FieldConfig = {
  key: string;
  label: string;
  type?: FieldType;
  options?: string[];
  optionsFromData?: (data: AppData, currentUser?: AdminUser | null) => Array<{ value: string; label: string }>;
  placeholder?: string;
  autoComplete?: string;
  required?: boolean;
  help?: string;
  readOnlyOnEdit?: boolean;
  visible?: (values: Record<string, string>) => boolean;
};

type ProviderCredentialMode = "provider_api_key" | "account_integration" | "later";

type ColumnConfig<T> = {
  key: string;
  label: string;
  render?: (item: T, ctx: AppData) => React.ReactNode;
};

type ResourceConfig<T> = {
  view: ViewKey;
  title: string;
  eyebrow: string;
  description: string;
  createLabel?: string;
  columns: ColumnConfig<T>[];
  fields: FieldConfig[];
  list: (ctx: AppData) => T[];
  create?: (ctx: ApiContext, values: Record<string, string>, data?: AppData) => Promise<void>;
  update?: (ctx: ApiContext, item: T, values: Record<string, string>) => Promise<void>;
  remove?: (ctx: ApiContext, item: T) => Promise<void>;
  actions?: ResourceAction<T>[];
  toolbarActions?: ToolbarAction[];
  toForm?: (item: T) => Record<string, string>;
};

type ResourceAction<T> = {
  label: string;
  title?: string;
  visible?: (item: T) => boolean;
  run?: (ctx: ApiContext, item: T) => Promise<void>;
  modal?: (item: T, data: AppData) => ModalState<any>;
  doneMessage?: (item: T) => string;
};

type ToolbarAction = {
  label: string;
  title?: string;
  kind?: "import-users";
  run?: (ctx: ApiContext, items?: unknown[]) => Promise<void>;
  doneMessage?: () => string;
};

type UserImportResult = {
  created?: number;
  updated?: number;
  skipped?: number;
  errors?: string[];
};

type AppData = {
  summary: Summary;
  projects: Project[];
  keys: APIKey[];
  providers: Provider[];
  providerResources: ProviderResource[];
  models: Model[];
  routes: ModelRoute[];
  logs: RequestLog[];
  auditEvents: AuditEvent[];
  alerts: AlertEvent[];
  alertDeliveries: AlertDelivery[];
  approvals: ApprovalRequest[];
  sqliteBackups: SQLiteBackup[];
  users: AdminUser[];
  breakdown: UsageBreakdown;
  timeseries: UsagePoint[];
  resources: Record<string, AdminResource[]>;
  providerCatalog: ProviderCatalogEntry[];
};

type ApiContext = {
  baseURL: string;
  adminToken: string;
};

type ModalState<T> = {
  config: ResourceConfig<T>;
  item?: T;
  initialValues?: Record<string, string>;
};

type ConfirmState<T> = {
  config: ResourceConfig<T>;
  item: T;
};

type SettingsTabKey = "settings" | "role-configs" | "identity-providers";

const defaultBaseURL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080";
const sessionStorageKey = "tokenhub.admin.session";
const oauthBaseURLStorageKey = "tokenhub.admin.oauth.base_url";
const authExpiredEventName = "tokenhub-admin-auth-expired";
const languageStorageKey = "tokenhub.admin.language";
const recentViewsStorageKey = "tokenhub.admin.recent.views.v1";

type AppLanguage = "zh-CN" | "en" | "ja";

const languageOptions: Array<{ value: AppLanguage; label: string; nativeLabel: string }> = [
  { value: "zh-CN", label: "Chinese", nativeLabel: "简体中文" },
  { value: "en", label: "English", nativeLabel: "English" },
  { value: "ja", label: "Japanese", nativeLabel: "日本語" },
];

let activeLanguage: AppLanguage = "en";

const translations: Record<Exclude<AppLanguage, "zh-CN">, Record<string, string>> = {
  en: {
    "请填写此字段": "Please fill out this field",
    "总览": "Overview",
    "网关概览": "Gateway Overview",
    "开始使用": "Get Started",
    "我的资源": "My Resources",
    "我的用量": "My Usage",
    "团队工作台": "Team Workspace",
    "24H 可用率": "24h Availability",
    "OAuth 登录失败": "OAuth login failed",
    "Provider 健康摘要": "Provider Health Summary",
    "Provider 可用性监控": "Provider Availability Monitoring",
    "Provider 在线": "Provider Online",
    "Token 端点": "Token Endpoint",
    "Token 结构": "Token Structure",
    "auto 会根据名称、Issuer URL 和类型自动选择登录页图标。": "auto picks the login page icon automatically based on the name, Issuer URL, and type.",
    "不再提示": "Don't show again",
    "两次输入的密码不一致": "The two passwords do not match",
    "只显示当前账号拥有发 Key 权限的项目；一个人可以被分配到多个项目。": "Only shows projects where the current account can issue Keys; a user can be assigned to multiple projects.",
    "团队 Claim": "Team Claim",
    "团队字段无法映射时使用。": "Used when the team field cannot be mapped.",
    "基础监控 · L1/L2": "Basic Monitoring · L1/L2",
    "定位错误码、模型和延迟": "Pinpoint error codes, models, and latency",
    "密码已重置，请使用新密码登录": "Password has been reset. Please sign in with your new password",
    "密码至少 8 位": "Password must be at least 8 characters",
    "密码重置失败": "Password reset failed",
    "就绪度": "Readiness",
    "必须与 OAuth 应用中登记的 Redirect URI 完全一致；留空时按当前后端访问地址自动生成。": "Must exactly match the Redirect URI registered in the OAuth app; when left blank, it is auto-generated from the current backend address.",
    "按估算成本降序": "By estimated cost, descending",
    "按健康检测、账号资源和真实请求日志汇总上游渠道可用性。": "Aggregates upstream channel availability from health checks, account resources, and real request logs.",
    "授权端点": "Authorization Endpoint",
    "授权范围": "Authorization Scope",
    "提交中": "Submitting",
    "故障": "Failure",
    "新密码": "New Password",
    "无真实请求": "No real requests",
    "无请求": "No requests",
    "无请求样本": "No request samples",
    "暂无失败请求": "No failed requests",
    "最近失败请求": "Recent Failed Requests",
    "最近请求": "Recent Requests",
    "最近请求按成功、告警和失败聚合": "Recent requests grouped by success, warning, and failure",
    "服务商 / 通道": "Provider / Channel",
    "未配置账号资源": "No account resources configured",
    "用户信息端点": "Userinfo Endpoint",
    "用户名 Claim": "Username Claim",
    "留空时按图标、Issuer 或身份源名称自动推断。": "When left blank, inferred from the icon, Issuer, or identity source name.",
    "留空表示不限制 Key 级模型白名单；实际可调用模型仍受模型目录、路由策略和项目权限约束。": "Leave blank to not restrict the Key-level model allowlist; the models actually callable are still constrained by the model catalog, routing policy, and project permissions.",
    "登录入口": "Login Entry",
    "登录后自动加入该项目空间。": "Automatically join this project space after login.",
    "登录图标": "Login Icon",
    "登录按钮名称": "Login Button Label",
    "真实延迟": "Real Latency",
    "真实监控 · L3": "Real Monitoring · L3",
    "真实监控来自最近请求日志；基础监控来自 Provider 和账号资源健康状态。": "Real monitoring comes from recent request logs; basic monitoring comes from Provider and account resource health.",
    "真实请求样本": "Real Request Samples",
    "确认新密码": "Confirm New Password",
    "端点、Scope 与 Claim 映射": "Endpoints, Scope, and Claim Mapping",
    "综合状态": "Overall Status",
    "自动加入默认项目时授予的项目权限。": "The project permission granted when auto-joining the default project.",
    "该资源": "this resource",
    "请求健康时间线": "Request Health Timeline",
    "请求量与 Token 消耗趋势": "Request Volume and Token Consumption Trend",
    "请设置新的控制台登录密码。": "Set a new console login password.",
    "调用趋势": "Call Trend",
    "质量评分": "Quality Score",
    "资源健康": "Resource Health",
    "身份源模板": "Identity Source Template",
    "输入和输出 Token 占比": "Input and Output Token Ratio",
    "近30天趋势": "Last 30 Days Trend",
    "连接方式": "Connection Method",
    "通用 OAuth2": "Generic OAuth2",
    "通用 OIDC": "Generic OIDC",
    "邮箱 Claim": "Email Claim",
    "重置密码": "Reset Password",
    "降级/慢响应": "Degraded / Slow Response",
    "首次 OAuth 登录创建用户时使用；不会覆盖已存在用户角色。": "Used when creating a user on first OAuth login; does not overwrite the role of an existing user.",
    "首次登录授权": "First-login Authorization",
    "高级配置": "Advanced Settings",
    "默认团队": "Default Team",
    "默认授权": "Default Authorization",
    "默认角色": "Default Role",
    "默认项目": "Default Project",
    "默认项目角色": "Default Project Role",
    "团队总览": "Team Overview",
    "团队报表": "Team Reports",
    "成本归因": "Cost Attribution",
    "项目治理": "Project Governance",
    "可用模型": "Available Models",
    "团队管理": "Team Management",
    "团队成员": "Team Members",
    "团队信息": "Team Profile",
    "平台工作台": "Platform Workspace",
    "平台总览": "Platform Overview",
    "全局用量": "Global Usage",
    "AI 资源": "AI Resources",
    "组织治理": "Organization Governance",
    "成本治理": "Cost Governance",
    "安全审计导航": "Security Audit",
    "安全总览": "Security Overview",
    "告警导航": "Alerts",
    "接入参考": "Integration Reference",
    "模型演练场": "Model Playground",
    "接口文档": "API Documentation",
    "AI 接入": "AI Access",
    "Provider 渠道": "Provider Channels",
    "模型目录": "Model Catalog",
    "路由策略": "Routing Policies",
    "企业治理": "Enterprise Governance",
    "项目空间": "Projects",
    "Key 管理": "Key Management",
    "团队分组": "Teams",
    "用户管理": "Users",
    "审批记录": "Approval Records",
    "成本审计": "Cost Audit",
    "用量统计": "Usage Analytics",
    "请求日志": "Request Logs",
    "成本账单": "Cost Billing",
    "成本中心": "Cost Centers",
    "导出报表": "Report Export",
    "健康与告警": "Health and Alerts",
    "健康检测": "Health Checks",
    "告警规则": "Alert Rules",
    "告警事件": "Alert Events",
    "通知渠道": "Notification Channels",
    "通知记录": "Notification Records",
    "安全运维": "Security Ops",
    "安全策略": "Security Policies",
    "代理出口": "Proxy Egress",
    "数据备份": "Data Backups",
    "公告通知": "Announcements",
    "系统设置": "System Settings",
    "新增系统设置": "Create System Setting",
    "网关地址、审计保留、企业集成和默认策略": "Gateway address, audit retention, enterprise integration, and default policies",
    "选择标准模型，按当前路由策略发起测试对话，验证 Provider、路由和返回内容。": "Test a standard model through the current routing policy and verify Provider, route, and response behavior.",
    "面向业务开发者的模型 API 调用说明、认证方式、示例代码和错误排查。": "Model API usage, authentication, examples, and troubleshooting for application developers.",
    "按模型、项目和日期查看请求量、Token 和成本归因。": "View requests, tokens, and cost attribution by model, project, and date.",
    "按 Provider 和项目归集估算成本，辅助成本分摊。": "Summarize estimated cost by Provider and project for cost allocation.",
    "查看最近请求日志、状态码、模型路由和延迟。": "Inspect recent request logs, status codes, model routes, and latency.",
    "查看运行时触发的额度、成本和 Provider 健康告警。": "Review quota, cost, and Provider health alerts triggered at runtime.",
    "查看告警 Webhook 发送结果、目标和失败原因。": "Review alert delivery results, targets, and failure reasons.",
    "处理 Key 发放、额度提升和模型开通等治理审批。": "Handle governance approvals such as key issuance, quota increases, and model access.",
    "登录控制台": "Login console",
    "使用": "Use",
    "登录": "login",
    "企业 AI 访问与成本治理平台": "Enterprise AI Access and Cost Governance",
    "账号 / 邮箱": "Account / Email",
    "密码": "Password",
    "登录中": "Signing in",
    "TokenHub 控制台": "TokenHub Console",
    "展开菜单": "Expand menu",
    "折叠菜单": "Collapse menu",
    "退出登录": "Sign out",
    "界面语言": "Interface Language",
    "选择控制台显示语言，偏好会保存在当前浏览器。": "Choose the console display language. The preference is saved in this browser.",
    "当前语言": "Current language",
    "点击排序": "Sort",
    "平台管理员": "Platform Admin",
    "默认项目空间": "Default Project Space",
    "平台工程团队": "Platform Engineering Team",
    "负责内部 AI Gateway 接入与平台治理": "Owns internal AI Gateway onboarding and platform governance.",
    "AI 平台成本中心": "AI Platform Cost Center",
    "平台工程与共享 AI 基础设施费用归属": "Cost attribution for platform engineering and shared AI infrastructure.",
    "网关基础设置": "Gateway Base Settings",
    "模型 API 对外地址、请求超时和审计保留周期": "Public model API address, request timeout, and audit retention period.",
    "OpenAI Compatible Gateway 默认配置": "Default OpenAI-compatible gateway configuration.",
    "产品流程": "Product Flow",
    "当前状态": "Current Status",
    "接入 Provider": "Connect Provider",
    "配置上游服务商、Base URL、API Key，并映射到标准模型目录。": "Configure upstream Providers, Base URLs, API keys, and map them to the standard model catalog.",
    "维护模型目录": "Maintain Model Catalog",
    "定义内部对外模型名、上下文窗口和计价口径。": "Define public internal model names, context windows, and pricing units.",
    "建立路由策略": "Create Routing Policies",
    "把对外模型映射到 Provider 的上游模型，并配置优先级与权重。": "Map public models to upstream Provider models and configure priority and weight.",
    "发放 API Key": "Issue API Key",
    "创建和维护当前权限范围内的内部调用凭证。": "Create and maintain internal calling credentials within the current permission scope.",
    "管理团队": "Manage Teams",
    "维护团队资料、负责人和费用归属。": "Maintain team profiles, owners, and cost attribution.",
    "管理成员": "Manage Members",
    "维护本团队成员账号和状态。": "Maintain team member accounts and status.",
    "查看用量": "View Usage",
    "查看当前权限范围内的请求量、Token 和成本。": "View requests, tokens, and cost within the current permission scope.",
    "我的用量概览": "My Usage Overview",
    "个人范围": "Personal scope",
    "可见项目": "Visible Projects",
    "条用量记录": "usage records",
    "按当前账号权限汇总": "Summarized by current account permissions",
    "查看账单": "View Billing",
    "查看当前权限范围内的成本归因。": "View cost attribution within the current permission scope.",
    "日志与治理": "Logs and Governance",
    "查看请求日志、后台操作、告警规则和安全策略。": "Review request logs, admin operations, alert rules, and security policies.",
    "个可选聊天模型": "callable chat models",
    "条启用路由": "active routes",
    "条启用公告": "active announcements",
    "管理公告": "Manage Announcements",
    "暂无公告说明": "No announcement description",
    "数量": "Count",
    "企业内部应用治理单元": "Internal application governance units",
    "内部调用凭证": "Internal calling credentials",
    "上游渠道实例，包含 Base URL 与 Key": "Upstream channel instances with Base URL and key",
    "对外模型目录": "Public model catalog",
    "对外模型到 Provider 的映射规则": "Mapping rules from public models to Providers",
    "治理事件": "Governance events",
    "当前权限范围内的用户账号": "User accounts within the current permission scope",
    "系统设置分类": "System settings category",
    "基础设置": "General Settings",
    "角色配置": "Role Settings",
    "身份源": "Identity Sources",
    "搜索名称、ID、状态": "Search name, ID, or status",
    "搜索模型": "Search models",
    "搜索模型、能力、参数": "Search models, capabilities, parameters",
    "新增": "Create",
    "新增模型": "Create Model",
    "新增 Provider": "Create Provider",
    "账号集成": "Account Integration",
    "创建 Provider 步骤": "Provider Creation Steps",
    "接入方式": "Access Method",
    "渠道信息": "Provider Info",
    "默认通道": "Default Channel",
    "账号与凭据": "Credentials",
    "路由与确认": "Routes",
    "选择接入方式": "Choose Access Method",
    "先告诉 TokenHub 你手里有什么：上游 API Key、OpenAI 账号资源，或者只是先占位建路由。": "Tell TokenHub what you have first: an upstream API key, an OpenAI account resource, or a placeholder Provider for routes.",
    "账号资源池会自动推荐 OpenAI 兼容通道，下一步只需确认 Base URL 和账号凭据。": "Account resource pool will recommend an OpenAI-compatible channel automatically. Next, confirm the Base URL and account credential.",
    "确认账号通道和基础信息": "Confirm account channel and basics",
    "选择渠道和基础信息": "Choose Provider and basics",
    "账号资源池已为你选好默认通道。这里通常只确认 Base URL；账号走企业代理时再修改。": "TokenHub selected the default channel for the account pool. Usually you only confirm the Base URL; edit it only when the account goes through an enterprise proxy.",
    "选择上游渠道商模板，TokenHub 会带出类型、Base URL 和可映射模型。": "Choose an upstream Provider template. TokenHub fills in type, Base URL, and mappable models.",
    "推荐通道": "Recommended Channel",
    "默认通道只负责协议与 Base URL，真实账号 Token 会在下一步保存为账号资源。": "The default channel only defines protocol and Base URL. The real account token is saved as an account resource in the next step.",
    "选择模型类型和渠道商": "Choose model type and Provider",
    "先确定要接入哪一类模型和哪个上游渠道，下一步再填写凭据和基础配置。": "Choose the model category and upstream Provider first, then fill credentials and basic settings.",
    "配置账号与凭据": "Configure credentials",
    "填写 Provider 基础信息，并选择是直接保存 API Key、接入账号资源池，还是稍后补齐凭据。": "Fill the Provider basics and choose whether to store an API key directly, attach an account pool, or configure credentials later.",
    "选择是直接保存 API Key、接入账号资源池，还是稍后补齐凭据。": "Choose whether to store an API key directly, attach an account pool, or configure credentials later.",
    "确认路由策略": "Review routing",
    "选择是否自动创建默认路由，并确认要映射到标准模型目录的上游模型。": "Choose whether to create default routes and review which upstream models map to the standard catalog.",
    "可映射模型": "Mappable Models",
    "模型协议": "Model Protocol",
    "兼容协议": "Compatibility Protocol",
    "通道名称": "Channel Name",
    "凭据方式": "Credential Mode",
    "已选模型": "Selected Models",
    "无": "None",
    "保存中": "Saving",
    "保存 Provider": "Save Provider",
    "请先选择一个渠道商。": "Choose a Provider first.",
    "请先选择一种接入方式。": "Choose an access method first.",
    "请填写渠道名称。": "Enter a Provider name.",
    "请填写通道名称。": "Enter a channel name.",
    "账号资源配置不完整": "Account resource setup is incomplete.",
    "认证与账号来源": "Credential Source",
    "选择 Provider 使用哪一种上游凭据。账号集成会把账号作为资源池管理，适合 OpenAI subscription 或多个账号轮询。": "Choose how this Provider gets upstream credentials. Account integration manages accounts as a resource pool for OpenAI subscriptions or multi-account routing.",
    "直接 API Key": "Direct API Key",
    "把上游 Key 保存到 Provider，适合单账号或兼容 API。": "Store the upstream key on the Provider. Best for one account or compatible APIs.",
    "账号资源池": "Account Resource Pool",
    "适合 OpenAI 账号、Subscription 或多账号轮询，默认通道会自动推荐。": "Best for OpenAI accounts, subscriptions, or multi-account rotation. The default channel is recommended automatically.",
    "稍后配置": "Configure Later",
    "先创建 Provider 和路由，稍后再添加 Key 或账号资源。": "Create the Provider and routes first, then add a key or account resource later.",
    "账号资源配置": "Account Resource Setup",
    "账号资源会在 Provider 创建成功后自动加入当前 Provider。": "The account resource will be attached to this Provider after creation.",
    "账号资源名称": "Account Resource Name",
    "账号授权": "Account Authorization",
    "输入账号地址并打开授权页；授权完成后 TokenHub 会从回调 URL 自动回填 Token。": "Enter the account address and open the authorization page. After authorization, TokenHub fills tokens from the callback URL.",
    "使用 OpenAI/Codex OAuth 授权账号；TokenHub 会在后端换取并保存账号 Token。": "Authorize an OpenAI/Codex account with OAuth. TokenHub exchanges and stores the account token on the backend.",
    "OpenAI/Codex 授权": "OpenAI/Codex Authorization",
    "账号地址/邮箱": "Account Address / Email",
    "用于区分账号资源，可填写邮箱或账号系统里的唯一地址。": "Used to identify this account resource. Enter an email or the unique address from the account system.",
    "账号授权地址": "Authorization URL",
    "粘贴上游账号系统的授权地址；TokenHub 会带上本页回调地址。": "Paste the upstream account authorization URL. TokenHub will attach this page as the callback URL.",
    "打开授权": "Open Authorization",
    "授权中": "Authorizing",
    "本页回调地址": "Callback URL",
    "授权应用跳回这个地址后，TokenHub 会自动读取 access_token / refresh_token / id_token。": "When the authorization app redirects to this URL, TokenHub reads access_token / refresh_token / id_token automatically.",
    "点击后由后端生成授权地址；授权完成会带 code 回到本页并自动换取 Token。": "TokenHub generates the authorization URL on the backend. After authorization, the code returns here and is exchanged automatically.",
    "复制回调地址": "Copy Callback URL",
    "回调结果": "Callback Result",
    "如果授权页没有自动跳回本页，把完整 callback URL 或 URL fragment 粘贴到这里。": "If the authorization page does not redirect back here, paste the full callback URL or URL fragment here.",
    "解析回填": "Parse and Fill",
    "等待授权回填": "Waiting for authorization",
    "已回填账号 Token": "Account token filled",
    "已回填访问 Token": "Access token filled",
    "已回填刷新 Token": "Refresh token filled",
    "已回填 ID Token": "ID token filled",
    "打开授权页后，请在上游账号系统完成授权。": "After opening the authorization page, complete authorization in the upstream account system.",
    "已打开 OpenAI/Codex 授权页，授权完成后会自动回填账号 Token。": "Opened the OpenAI/Codex authorization page. TokenHub will fill the account token after authorization.",
    "请先填写账号授权地址。": "Enter the authorization URL first.",
    "账号授权地址格式不正确。": "The authorization URL format is invalid.",
    "未在回调结果中识别到 Token。": "No token was found in the callback result.",
    "账号授权失败": "Account authorization failed",
    "授权回调缺少会话信息，请重新打开授权。": "The authorization callback is missing session information. Open authorization again.",
    "正在换取账号 Token...": "Exchanging account token...",
    "账号授权换取 Token": "Account authorization token exchange",
    "账号授权换取 Token 失败": "Account authorization token exchange failed",
    "生成账号授权地址": "Generate account authorization URL",
    "生成账号授权地址失败": "Failed to generate account authorization URL",
    "回调里只有授权 code，当前版本还需要返回 Token 的授权地址，或在高级选项中手动粘贴 Token。": "The callback only contains an authorization code. This version needs a callback URL that returns tokens, or manual token paste in Advanced.",
    "已从回调 URL 自动回填账号 Token。": "Account token was filled from the callback URL.",
    "已从粘贴的回调结果回填账号 Token。": "Account token was filled from the pasted callback result.",
    "已复制回调地址。": "Callback URL copied.",
    "高级：手动粘贴 Token": "Advanced: Paste Token Manually",
    "只有在授权回填不可用时使用；保存后 Token 不会再次显示。": "Use only when callback filling is unavailable. Tokens will not be shown again after saving.",
    "资源调度": "Resource Scheduling",
    "这些配置决定账号资源参与路由时的权重、并发和限流。": "These settings control route weight, concurrency, and rate limits for this account resource.",
    "先完成账号授权回填；TokenHub 会把回填的 Token 保存为账号资源。": "Complete account authorization first. TokenHub will save the returned token as an account resource.",
    "请先完成账号授权回填，或在高级选项中手动粘贴 Token。": "Complete account authorization first, or paste a token manually in Advanced.",
    "收到账号授权回调，已打开账号池创建向导。": "Received the account authorization callback and opened the account pool creation wizard.",
    "Provider 将直接保存上游 API Key；如果需要账号池、刷新凭据或多账号调度，请切换为账号集成。": "The Provider will store the upstream API key directly. Switch to account integration for account pools, refresh credentials, or multi-account scheduling.",
    "保存后不会写入上游凭据，可稍后通过编辑 Provider 或账号集成补齐。": "No upstream credential will be saved. You can add one later by editing the Provider or using account integration.",
    "已创建账号资源": "account resource created",
    "Provider 已创建，但无法确认账号资源所属 Provider。": "Provider was created, but TokenHub could not determine which Provider should own the account resource.",
    "创建账号资源": "Create Account Resource",
    "请填写至少一个账号 Token，或切换为稍后配置。": "Enter at least one account token, or switch to Configure Later.",
    "请填写账号资源的 API Key，或切换为稍后配置。": "Enter the account resource API key, or switch to Configure Later.",
    "Provider 账号资源": "Provider Account Resources",
    "OpenAI 账号资源": "OpenAI Account Resource",
    "添加账号资源": "Add Account Resource",
    "账号资源": "Account Resources",
    "使用保存的 refresh token 更新账号访问 Token": "Refresh the account access token with the saved refresh token.",
    "Token 已刷新": "Token refreshed",
    "账号类型": "Account Type",
    "认证方式": "Authentication",
    "访问 Token": "Access Token",
    "刷新 Token": "Refresh Token",
    "ID Token": "ID Token",
    "账号邮箱": "Account Email",
    "账号 ID": "Account ID",
    "组织 ID": "Organization ID",
    "计划类型": "Plan Type",
    "已保存刷新 Token": "Refresh token saved",
    "未保存刷新 Token": "No refresh token",
    "普通 API Key 资源": "API Key Resource",
    "OpenAI Subscription 账号": "OpenAI Subscription Account",
    "OAuth 账号": "OAuth Account",
    "Personal Access Token": "Personal Access Token",
    "把 OpenAI subscription、PAT 或普通 API Key 作为 Provider 资源实例加入账号池，并参与路由权重、并发和限流调度。": "Add OpenAI subscriptions, PATs, or API keys as Provider resource accounts for route weighting, concurrency, and rate-limit scheduling.",
    "OpenAI subscription / Codex OAuth access token 或 PAT；保存后不会再次显示。": "OpenAI subscription / Codex OAuth access token or PAT. It will not be shown again after saving.",
    "可选，保存到加密凭据中，用于后续自动刷新能力。": "Optional. Stored in encrypted credentials for future token refresh support.",
    "可选。填写后会自动提取账号邮箱、账号 ID、组织 ID 和计划类型。": "Optional. TokenHub extracts account email, account ID, organization ID, and plan type when provided.",
    "普通资源实例的上游 API Key；编辑时留空表示不修改。": "Upstream API key for a regular resource instance. Leave empty while editing to keep the current key.",
    "分组": "Group",
    "RPM 限制": "RPM Limit",
    "TPM 限制": "TPM Limit",
    "配置": "Configure",
    "配置路由": "Configure Routes",
    "编辑": "Edit",
    "删除": "Delete",
    "保存": "Save",
    "取消": "Cancel",
    "关闭": "Close",
    "操作": "Actions",
    "暂无数据": "No data",
    "请选择": "Select",
    "开启": "On",
    "关闭开关": "Off",
    "启用": "Enabled",
    "停用": "Disabled",
    "禁用": "Disable",
    "重新加载": "Reload",
    "全选": "Select all",
    "清空": "Clear",
    "请选择至少一个统一模型": "Select at least one unified model",
    "确认删除": "Confirm Delete",
    "操作已完成": "Operation completed",
    "操作失败": "Operation failed",
    "保存失败": "Save failed",
    "删除失败": "Delete failed",
    "导出失败": "Export failed",
    "连接失败": "Connection failed",
    "登录失败": "Login failed",
    "数据加载中，请稍后再操作。": "Data is loading. Please try again later.",
    "请先创建项目，再在项目下发放 API Key。": "Create a project before issuing an API key.",
    "请先新增 Provider 渠道，再配置路由策略。": "Create a Provider channel before configuring routing policies.",
    "请先维护模型目录，再新增路由策略。": "Maintain the model catalog before creating routing policies.",
    "新 Key 仅展示一次：": "New key is shown only once: ",
    "新 Key 已生成": "New Key Generated",
    "请现在复制并保存这个 Key。关闭弹窗后将无法再次查看完整 Key，只能通过轮换生成新的 Key。": "Copy and save this key now. After closing this dialog, the full key cannot be viewed again. You can only generate a new one by rotating it.",
    "完整 Key": "Full Key",
    "已复制": "Copied",
    "复制 Key": "Copy Key",
    "我已保存，关闭": "Saved, Close",
    "第": "Items",
    "条，共": "of",
    "每页": "Per page",
    "第一页": "First page",
    "上一页": "Previous page",
    "下一页": "Next page",
    "最后一页": "Last page",
    "名称": "Name",
    "说明": "Description",
    "状态": "Status",
    "更新时间": "Updated At",
    "类型": "Type",
    "协议": "Protocol",
    "端口": "Port",
    "优先级": "Priority",
    "权重": "Weight",
    "模型": "Model",
    "模型名": "Model",
    "项目": "Project",
    "团队": "Team",
    "成员": "Member",
    "用户": "User",
    "邮箱": "Email",
    "用户名": "Username",
    "角色": "Role",
    "最近登录": "Last Login",
    "负责人": "Owner",
    "请求": "Requests",
    "总请求": "Total Requests",
    "总 Token": "Total Tokens",
    "总成本": "Total Cost",
    "模型调用 Token": "Model call tokens",
    "等待调用数据": "Waiting for usage data",
    "输入 Token": "Input Tokens",
    "输出 Token": "Output Tokens",
    "成本": "Cost",
    "估算成本": "Estimated Cost",
    "时间": "Time",
    "延迟": "Latency",
    "最终 Provider": "Final Provider",
    "Provider 资源": "Provider Resource",
    "上游模型": "Upstream Model",
    "客户端 IP": "Client IP",
    "输入": "Input",
    "输出": "Output",
    "总量": "Total",
    "上下文": "Context",
    "条线路": "routes",
    "未配置线路": "No routes configured",
    "官方资源": "Official resource",
    "可访问": "Accessible",
    "能力": "Capability",
    "最近路由": "Last Route",
    "待请求": "Not requested",
    "请求与响应": "Request and Response",
    "已记录快照": "Snapshot recorded",
    "未记录": "Not recorded",
    "动作": "Action",
    "操作人": "Actor",
    "对象": "Object",
    "对象 ID": "Object ID",
    "来源 IP": "Source IP",
    "成功": "Success",
    "失败": "Failed",
    "成功率": "Success Rate",
    "失败请求": "Failed Requests",
    "平均延迟": "Avg Latency",
    "请求列表": "Request List",
    "请求详情": "Request Details",
    "大模型请求历史": "Model Request History",
    "后台操作审计": "Admin Operation Audit",
    "日志类型": "Log Type",
    "请求状态筛选": "Request Status Filter",
    "搜索请求历史": "Search request history",
    "搜索请求 ID、模型、Provider、状态码": "Search request ID, model, Provider, or status code",
    "全部": "All",
    "所有模型": "All Models",
    "精选": "Featured",
    "文本": "Text",
    "图像": "Image",
    "视频": "Video",
    "音频": "Audio",
    "嵌入": "Embedding",
    "重排序": "Rerank",
    "三方资源": "Third-party",
    "模型大类": "Model Categories",
    "模型能力筛选": "Model Capability Filter",
    "通知渠道类型": "Notification Channel Type",
    "自动路由": "Auto Routing",
    "搜索模型名称或 ID": "Search model name or ID",
    "个模型": "models",
    "个匹配模型": "matching models",
    "没有匹配的模型": "No matching models",
    "模型路由器": "Model Router",
    "模型路由规则": "Model Routing Rules",
    "参考模型路由器思路，按平衡、质量优先或成本优先模式选择候选 Provider 线路，并在失败时自动回退。": "Choose candidate Provider routes by balanced, quality-first, or cost-first strategy, and automatically fail over in order.",
    "平衡模式综合权重、质量和成本；质量优先会先选高质量线路；成本优先会先选低成本线路。调用失败时会按候选顺序自动回退。": "Balanced mode combines weight, quality, and cost. Quality-first prefers higher-quality routes, cost-first prefers lower-cost routes, and failures fall back in candidate order.",
    "条启用线路": "active routes",
    "个已配置路由": "configured routes",
    "统一模型": "Unified Model",
    "已配置": "Configured",
    "全部模型": "All Models",
    "路由显示范围": "Route Display Scope",
    "搜索模型或 Provider": "Search model or Provider",
    "新增路由": "Create Route",
    "未配置": "Not configured",
    "按上到下顺序调用": "Called from top to bottom",
    "该统一模型还没有 Provider 线路": "This unified model has no Provider route yet",
    "拖动调整调用顺序": "Drag to adjust call order",
    "未配置 Base URL": "Base URL not configured",
    "没有匹配的模型路由": "No matching model routes",
    "主": "P",
    "路由": "Routes",
    "自定义": "Custom",
    "对外模型列表": "Public Model List",
    "维护内部应用调用的对外模型名，并查看每个模型可用的 Provider 线路。": "Maintain public model names for internal applications and view available Provider routes per model.",
    "Provider 列表": "Provider List",
    "Provider 是一个可调用的上游渠道实例，包含服务商类型、Base URL、API Key、健康状态和标准模型路由。": "A Provider is a callable upstream channel instance with provider type, Base URL, API key, health status, and standard model routes.",
    "路由线路": "Routes",
    "系统管理员": "System Admin",
    "安全审计": "Security Auditor",
    "团队 Leader": "Team Leader",
    "普通用户": "Regular User",
    "全局": "Global",
    "本人": "Self",
    "可分配": "Assignable",
    "是": "Yes",
    "否": "No",
    "角色标识": "Role Key",
    "显示名称": "Display Name",
    "数据范围": "Data Scope",
    "已吊销": "Revoked",
    "草稿": "Draft",
    "已归档": "Archived",
    "待处理": "Pending",
    "已批准": "Approved",
    "已驳回": "Rejected",
    "已确认": "Confirmed",
    "健康": "Healthy",
    "正常": "OK",
    "告警": "Warning",
    "降级": "Degraded",
    "异常": "Error",
    "不可用": "Down",
    "已过期": "Expired",
    "未知": "Unknown",
    "直连": "Direct",
    "静默": "Silent",
    "弹窗": "Popup",
    "手动": "Manual",
    "每日": "Daily",
    "每周": "Weekly",
    "每月": "Monthly",
    "超额拦截": "Block when exceeded",
    "仅告警": "Warn only",
    "飞书": "Feishu",
    "钉钉": "DingTalk",
    "企业微信": "WeCom",
    "邮件": "Email",
    "操作审计": "Operation Audit",
    "Key 发放": "Key Issuance",
    "预算变更": "Budget Change",
    "模型开通": "Model Access",
    "额度提升": "Quota Increase",
    "账单确认": "Billing Confirmation",
    "账单驳回": "Billing Rejection",
    "平衡": "Balanced",
    "质量优先": "Quality First",
    "成本优先": "Cost First",
    "优先级 + 权重": "Priority + Weight",
    "仅优先级": "Priority Only",
    "文本对话": "Chat",
    "向量嵌入": "Embedding",
    "模拟渠道": "Mock Provider",
    "OpenAI 官方": "OpenAI Official",
    "OpenAI 兼容": "OpenAI Compatible",
    "本地模型": "Local Model",
    "Provider 健康": "Provider Health",
    "资源实例健康": "Resource Health",
    "请求额度": "Request Quota",
    "Token 额度": "Token Quota",
    "成本额度": "Cost Quota",
    "错误率": "Error Rate",
    "日成本额度": "Daily Cost Quota",
    "日 Token 额度": "Daily Token Quota",
    "P95 延迟": "P95 Latency",
    "测试": "Test",
    "健康变更": "Health Change",
    "确认": "Confirm",
    "驳回": "Reject",
    "导出": "Export",
    "切换主题": "Toggle theme",
    "企业 AI 网关": "Enterprise AI Gateway",
    "统一接入与成本治理平台": "Unified access and cost governance platform",
    "显示密码": "Show password",
    "隐藏密码": "Hide password",
    "保持登录": "Keep me signed in",
    "忘记密码？": "Forgot password?",
    "或": "or",
    "使用企业 SSO 登录": "Sign in with Enterprise SSO",
    "做一个乐于助人的助手": "Be a helpful assistant",
    "你是企业内部 AI 助手。": "You are an internal enterprise AI assistant.",
    "用两句话介绍 TokenHub": "Introduce TokenHub in two sentences",
    "总结今天的工单重点": "Summarize today's key tickets",
    "TokenHub 企业知识库": "TokenHub enterprise knowledge base",
    "请用三句话介绍 TokenHub。": "Introduce TokenHub in three sentences.",
    "请先粘贴 CSV 内容。": "Paste CSV content first.",
    "用户导入失败": "User import failed",
    "搜索控制台": "Search console",
    "搜索模型、Provider、日志...": "Search models, providers, logs...",
    "搜索结果": "Search results",
    "打开": "Open",
    "打开对应工作页面": "Open the corresponding workspace",
    "没有找到匹配入口": "No matching entry",
    "请尝试搜索模型、Key、用量、日志或设置。": "Try models, keys, usage, logs, or settings.",
    "常用操作": "Common actions",
    "当前位置": "Current location",
    "全局平台范围": "Global platform scope",
    "安全审计范围": "Security audit scope",
    "团队和项目范围": "Team and project scope",
    "个人可见范围": "Personal visible scope",
    "健康 Provider": "Healthy Providers",
    "Provider 需要关注": "Provider needs attention",
    "错误请求": "Error Requests",
    "记录": "Records",
    "条匹配": "matches",
    "没有匹配结果": "No matches",
    "清空搜索或换一个关键词再试。": "Clear search or try another keyword.",
    "还没有 Provider": "No Providers yet",
    "先接入上游服务商，再为模型配置路由。": "Connect an upstream Provider before configuring model routes.",
    "还没有模型路由": "No model routes yet",
    "路由决定模型请求会被转发到哪个 Provider。": "Routes decide which Provider receives model requests.",
    "还没有项目空间": "No projects yet",
    "项目、Key、额度和成本归属": "Project, Key, quota, and cost ownership",
    "项目是 Key、额度、成员和成本归属的基本单元。": "Projects are the base unit for Keys, quotas, members, and cost ownership.",
    "还没有 Key": "No Keys yet",
    "为项目发放 Key 后，业务应用才能调用网关。": "Issue a project Key before business apps can call the gateway.",
    "还没有用户": "No users yet",
    "可以手动创建，也可以从 CSV 批量导入。": "Create users manually or import them from CSV.",
    "还没有身份源": "No identity sources yet",
    "接入企业 OAuth/OIDC 后，用户可以使用 SSO 登录。": "Connect enterprise OAuth/OIDC so users can sign in with SSO.",
    "当前视图还没有可展示的记录。": "This view has no records to show yet.",
    "报表时间范围": "Report time range",
    "7 天": "7 days",
    "30 天": "30 days",
    "本月": "This Month",
    "近 7 天": "Last 7 days",
    "近 30 天": "Last 30 days",
    "在线": "Online",
    "全部健康 · 延迟 312ms": "All healthy · 312ms latency",
    "API Key": "API Key",
    "已发放": "Issued",
    "成本与用量趋势": "Cost and Usage Trend",
    "趋势指标": "Trend Metric",
    "Provider 成本占比": "Provider Cost Share",
    "暂无 Provider 成本数据": "No Provider cost data",
    "Top 模型 · 调用量": "Top Models · Requests",
    "暂无模型调用数据": "No model request data",
    "OpenAI 兼容协议": "OpenAI-compatible protocol",
    "接口基础信息": "API Basics",
    "面向业务开发者的模型 API 调用说明。业务侧只使用 ": "Model API usage for business developers. Business apps only use ",
    " 和项目 API Key；": " and project API Keys; ",
    " 仅用于控制台管理。": " is only for console administration.",
    "当前权限下还没有可展示模型，请先确认模型目录和路由策略。": "No models are available under the current permissions. Check the model catalog and routing policies.",
    "当前后台还没有启用可用模型路由，请让管理员在路由策略里启用模型。": "No available model routes are enabled yet. Ask an administrator to enable models in routing policies.",
    "示例模型": "Sample Model",
    "当前配置": "Current Config",
    "启用路由": "Active Routes",
    "API 导航": "API Navigation",
    "按接口类型查看详细说明": "View details by API type",
    "开始": "Start",
    "模型 API": "Model API",
    "管理 API": "Admin API",
    "参考": "Reference",
    "快速接入": "Quick Start",
    "用一个项目 API Key 调用 TokenHub 的 OpenAI 兼容模型接口。": "Call TokenHub's OpenAI-compatible model API with a project API Key.",
    "鉴权方式": "Authentication",
    "业务应用只需要调用 /v1/*，不需要也不应该访问 /api/admin/*。": "Business applications only need /v1/* and should not access /api/admin/*.",
    "每个 API Key 都会受到项目状态、模型白名单、额度、并发和 Provider 路由策略约束。": "Every API Key is constrained by project status, model allowlist, quotas, concurrency, and Provider routing policies.",
    "排查失败请求时，优先复制响应里的 request_id 到请求日志查看完整链路。": "When troubleshooting failures, copy the response request_id to request logs first.",
    "查询可用模型": "List available models",
    "发起一次对话": "Start a chat",
    "认证与权限": "Authentication and Permissions",
    "业务 API 使用项目下发的 API Key；控制台登录令牌不能替代业务 Key。": "Business APIs use project API Keys; console login tokens cannot replace business keys.",
    "格式": "Format",
    "当前 Key 数": "Current Keys",
    "项目 API Key，格式为 Bearer sk_xxx": "Project API Key in Bearer sk_xxx format",
    "POST 必填": "Required for POST",
    "JSON 请求使用 application/json": "Use application/json for JSON requests",
    "模型接口必填": "Required for model APIs",
    "统一模型名，需在 Key 白名单和路由策略中可用": "Unified model name available in the Key allowlist and routing policy",
    "401 通常表示 Key 缺失、格式错误、已停用或已过期。": "401 usually means the Key is missing, malformed, disabled, or expired.",
    "403 通常表示项目状态、模型白名单或权限范围不允许当前调用。": "403 usually means project status, model allowlist, or permission scope blocks the call.",
    "模型列表": "Model List",
    "按当前 API Key 的权限返回可用统一模型。": "Return available unified models for the current API Key.",
    "请求示例": "Request Example",
    "字段": "Field",
    "必填": "Required",
    "统一模型名称，用于后续调用的 model 字段": "Unified model name used as the model field in later calls",
    "OpenAI 兼容对象类型，通常为 model": "OpenAI-compatible object type, usually model",
    "模型归属或 Provider 标识": "Model owner or Provider identifier",
    "对话补全": "Chat Completions",
    "兼容 OpenAI Chat Completions，用于普通对话、工具调用和流式输出。": "Compatible with OpenAI Chat Completions for chat, tool calling, and streaming.",
    "统一模型名，例如 ": "Unified model name, for example ",
    "system/user/assistant 消息数组": "system/user/assistant message array",
    "采样温度，默认由上游模型决定": "Sampling temperature; defaults to the upstream model",
    "true 时返回 SSE 流式响应": "Returns SSE streaming responses when true",
    "兼容新版 Responses 风格调用，适合统一文本输入和多模态能力扩展。": "Compatible with the newer Responses style, suitable for unified text input and multimodal extension.",
    "统一模型名": "Unified model name",
    "用户输入内容": "User input",
    "是否流式返回": "Whether to stream responses",
    "文本向量": "Text Embeddings",
    "转发文本向量生成请求，并记录 Token 与成本归因。": "Forward text embedding requests and record token and cost attribution.",
    "向量统一模型名": "Unified embedding model name",
    "需要向量化的文本": "Text to embed",
    "可选 float/base64，取决于上游模型支持": "Optional float/base64 depending on upstream support",
    "当前可调用模型": "Currently Callable Models",
    "根据模型目录和路由策略汇总当前控制台可见模型。": "Summarize models visible to this console from the catalog and routing policy.",
    "控制台登录": "Console Login",
    "控制台用户登录接口，不等同于模型 API Key。": "Console user login API; not equivalent to a model API Key.",
    "用户名或邮箱": "Username or email",
    "控制台密码": "Console password",
    "管理 API 仅用于控制台和受信任后台程序，不应该暴露给业务应用前端。": "Admin APIs are only for the console and trusted backends and should not be exposed to business frontends.",
    "Provider 配置": "Provider Config",
    "管理上游 Provider、Base URL、凭证和健康状态。": "Manage upstream Providers, Base URLs, credentials, and health status.",
    "读取 Provider 列表": "Read Provider list",
    "新增 Provider 配置": "Create Provider config",
    "更新 Provider 凭证、状态和元信息": "Update Provider credentials, status, and metadata",
    "维护统一模型到 Provider 资源的优先级、权重和状态。": "Maintain priorities, weights, and status from unified models to Provider resources.",
    "当前路由": "Current Routes",
    "按权限查看模型调用日志、命中路由、耗时、Token 和错误信息。": "View model call logs, matched routes, latency, tokens, and errors by permission.",
    "日志样本": "Log Samples",
    "用途": "Purpose",
    "排查 request_id 和上游响应": "Troubleshoot request_id and upstream responses",
    "常见错误": "Common Errors",
    "按状态码定位 API Key、模型白名单、路由和额度问题。": "Locate API Key, model allowlist, route, and quota issues by status code.",
    "状态码": "Status",
    "错误码": "Error Code",
    "处理方式": "How to Handle",
    "检查 Authorization 是否使用 TokenHub API Key": "Check whether Authorization uses a TokenHub API Key",
    "检查 Key 的模型白名单和项目状态": "Check the Key model allowlist and project status",
    "为统一模型配置启用路由": "Configure an active route for the unified model",
    "检查项目额度、并发和 Provider 资源限制": "Check project quotas, concurrency, and Provider resource limits",
    "在请求日志里查看上游响应和 request_id": "Check upstream responses and request_id in request logs",
    "SDK 示例": "SDK Examples",
    "使用 OpenAI 兼容 SDK 接入 TokenHub。": "Use OpenAI-compatible SDKs to integrate with TokenHub.",
    "调用链路": "Call Flow",
    "从鉴权到 Provider 路由的完整治理链路。": "Full governance flow from authentication to Provider routing.",
    "阶段": "Stage",
    "当前数据": "Current Data",
    "认证": "Authentication",
    "权限": "Permissions",
    "模型白名单 + 项目状态": "Model allowlist + project status",
    "上游渠道实例、凭证和健康状态": "Upstream channel instances, credentials, and health",
    "对外模型到 Provider 的优先级/权重映射": "Priority/weight mapping from public models to Providers",
    "额度、审计、成本统计": "Quota, audit, and cost analytics",
    "请求参数": "Request Parameters",
    "明细": "Details",
    "示例": "Examples",
    "复制": "Copy",
    "复制代码": "Copy code",
    "企业 AI 用量看板": "Enterprise AI Usage Dashboard",
    "面向管理层的部门、个人与 Token 消耗对比": "Department, individual, and token comparisons for leadership",
    "按部门": "By Department",
    "Token 口径": "Token basis",
    "Token 消耗": "Token Usage",
    "总 Token 消耗": "Total Token Usage",
    "覆盖部门": "Covered Departments",
    "活跃成员": "Active Members",
    "统计时间": "Generated",
    "最高": "Top",
    "暂无部门归因": "No department attribution",
    "次请求": "requests",
    "部门 Token 消耗对比": "Department Token Comparison",
    "输入 Token 与输出 Token 分段展示，按总量排序": "Input and output tokens segmented and sorted by total",
    "部门排行": "Department Ranking",
    "个人排行": "Individual Ranking",
    "公司内部成员 Token 消耗 Top 20": "Top 20 internal members by token usage",
    "按 Token 降序": "Sorted by token descending",
    "可用于复盘配额": "Useful for quota review",
    "暂无部门 Token 数据": "No department token data",
    "暂无部门排行数据": "No department ranking data",
    "暂无个人排行数据": "No individual ranking data",
    "排名": "Rank",
    "部门": "Department",
    "占比": "Share",
    "未归属部门": "Unassigned Department",
    "团队用户": "Team Users",
    "关闭成员列表": "Close member list",
    "姓名": "Name",
    "成员数": "Members",
    "项目额度": "Project Quota",
    "关闭额度配置": "Close quota settings",
    "已配置项目专属额度": "Project-specific quota configured",
    "未配置项目专属额度": "No project-specific quota configured",
    "留空或填 0 表示该项不限额；Key 自身额度仍会叠加生效。": "Leave blank or 0 for unlimited; Key-level quotas still apply.",
    "已有额度提升申请待审批": "Quota increase request is pending approval",
    "最近触发了项目额度限制": "Project quota limit was recently triggered",
    "可在审批记录中处理。": "Handle it in approval records.",
    "次额度不足，请填写希望提升后的目标额度再提交审批。": "quota hits. Enter the target quota and submit for approval.",
    "待审批": "Pending approval",
    "需提升": "Increase needed",
    "日请求": "Daily Requests",
    "月请求": "Monthly Requests",
    "日 Token": "Daily Tokens",
    "月 Token": "Monthly Tokens",
    "日成本": "Daily Cost",
    "月成本": "Monthly Cost",
    "最大并发": "Max Concurrency",
    "提升额度申请": "Request Quota Increase",
    "提交项目额度提升审批": "Submit project quota increase approval",
    "保存额度": "Save Quota",
    "按需导出": "On-demand Export",
    "最近导出": "Recent Exports",
    "自动导出配置": "Scheduled Export Config",
    "新增配置": "Create Config",
    "数据集": "Dataset",
    "文件": "File",
    "账期": "Period",
    "导出 ": "Export ",
    "模型分类": "Model Categories",
    "Key 归属逻辑": "Key Ownership Logic",
    "内部应用配置项目下发放的 Key；额度、模型白名单、用量和成本都会归属到该项目。": "Keys are issued under internal application projects; quotas, model allowlists, usage, and costs are attributed to that project.",
    "个项目": "projects",
    "个 Key": "Keys",
    "批量导入": "Bulk Import",
    "CSV 内容": "CSV Content",
    "按 username 或 email 匹配已有用户；匹配到则更新，未匹配则创建。": "Match existing users by username or email; matched users are updated and unmatched users are created.",
    "字段顺序": "Field Order",
    "role 可填 admin、team_leader、user；status 可填 active 或 disabled。": "role supports admin, team_leader, user; status supports active or disabled.",
    "导入中": "Importing",
    "开始导入": "Start Import",
    "模型配置": "Model Config",
    "暂无聊天模型": "No chat models",
    "条路由": "routes",
    "未配置路由": "No route configured",
    "响应格式": "Response Format",
    "系统提示": "System Prompt",
    "函数": "Functions",
    "函数调用配置待接入": "Function calling config is not connected yet",
    "添加函数": "Add Function",
    "模型演练对话": "Model playground chat",
    "复制模型名": "Copy model name",
    "选择模型": "Select Model",
    "模型详情": "Model Details",
    "查看代码": "View Code",
    "清空历史": "Clear History",
    "默认资源": "Default Resource",
    "次尝试": "attempts",
    "当前模型": "Current Model",
    "试用": "Try",
    "当前还没有配置模型路由。": "No model routes are configured yet.",
    "体验一下，看看模型在 TokenHub 网关上的表现": "Try it and see how the model performs through TokenHub",
    "说点什么...": "Say something...",
    "文件上传待接入": "File upload is not connected yet",
    "上传文件": "Upload File",
    "发送": "Send",
    "API 使用": "API Usage",
    "使用以下代码示例集成 TokenHub 模型接口": "Use these code examples to integrate TokenHub model APIs",
    "API 调用语言": "API Language",
    "未选择模型": "No model selected",
    "模型没有返回可展示内容。": "The model did not return displayable content.",
    "演练请求失败": "Playground request failed",
    "Provider 模板加载失败": "Provider template failed to load",
    "Provider 已": "Provider ",
    "更新": "updated",
    "创建": "created",
    "模型类型": "Model Type",
    "类": "categories",
    "渠道商": "Provider",
    "个标准模型": "standard models",
    "自定义渠道商": "Custom Provider",
    "搜索渠道商、ID、类型": "Search providers, IDs, or types",
    "没有匹配的渠道商": "No matching providers",
    "使用自定义渠道商": "Use custom Provider",
    "请选择渠道商": "Select Provider",
    "例如 prv_company_proxy": "e.g. prv_company_proxy",
    "留空自动生成": "Leave blank to auto-generate",
    "渠道名称": "Channel Name",
    "渠道商类型": "Provider Type",
    "编辑时留空则不修改已保存密钥。": "Leave blank while editing to keep the saved secret.",
    "留空表示不修改现有 Key；填写新值才会覆盖。": "Leave blank to keep the existing Key; enter a new value to overwrite it.",
    "开启后会为下方勾选模型补齐缺失线路，不覆盖已有策略。": "When enabled, missing routes will be added for selected models without overwriting existing policies.",
    "保存渠道时会自动创建下方勾选模型的默认路由。": "Saving the Provider will create default routes for selected models.",
    "上游模型映射": "Upstream Model Mapping",
    "个可映射模型": "mappable models",
    "加载中": "Loading",
    "正在加载模型列表...": "Loading model list...",
    "该渠道商暂无可匹配当前标准模型目录的上游模型": "This Provider has no upstream models matching the current standard catalog",
    "已有路由": "Existing route",
    "已关闭自动路由：保存后只创建 Provider，不生成路由策略。": "Auto routing is off: saving creates only the Provider and no routing policy.",
    "当前没有勾选模型，保存后不会生成路由策略。": "No models are selected, so saving will not generate routing policies.",
    "保存后会为": "After saving, routes will be created for",
    "个已选": "selected",
    "模型创建缺失的默认路由。": "models with missing default routes.",
    "请求详情加载失败": "Request details failed to load",
    "全部 ": "All ",
    "成功 ": "Success ",
    "失败 ": "Failed ",
    "没有匹配的请求记录": "No matching request records",
    "暂无大模型请求记录": "No model request records",
    "正在加载请求详情...": "Loading request details...",
    "请选择一条请求": "Select a request",
    "复制请求 ID": "Copy request ID",
    "Token 与成本": "Tokens and Cost",
    "条记录": "records",
    "暂无记录": "No records",
    "路由尝试": "Route Attempts",
    "次，含 fallback": "attempts, with fallback",
    "没有记录到路由尝试": "No route attempts recorded",
    "这条历史记录没有保存 request / response 快照": "This history record has no saved request / response snapshot",
    "未记录请求内容": "Request content not recorded",
    "未记录响应内容": "Response content not recorded",
    "已截断": "Truncated",
    "上游模型 ": "Upstream model ",
    "资源 ": "Resource ",
    "路由 ": "Route ",
    "资源": "Resource",
    "大小": "Size",
    "触发方式": "Trigger",
    "创建时间": "Created At",
    "最近恢复": "Last Restored",
    "校验": "Checksum",
    "保留天数": "Retention Days",
    "0 表示不过期": "0 means never expire",
    "下载": "Download",
    "恢复": "Restore",
    "备份文件已开始下载": "Backup download has started",
    "已恢复备份": "Backup restored",
    "后台用户列表": "Admin User List",
    "新增用户": "Create User",
    "编辑时留空则不修改": "Leave blank while editing to keep unchanged",
    "导入用户": "Import Users",
    "从已有系统导出的 CSV 批量导入或更新用户": "Bulk import or update users from a CSV exported by an existing system",
    "指标": "Metric",
    "阈值": "Threshold",
    "级别": "Severity",
    "对象范围": "Scope",
    "规则列表": "Rule List",
    "触发事件": "Trigger Events",
    "来源": "Source",
    "系统默认": "System Default",
    "事件": "Event",
    "告警 ID": "Alert ID",
    "渠道": "Channel",
    "HTTP": "HTTP",
    "失败原因": "Failure Reason",
    "审批申请列表": "Approval Request List",
    "触发条件": "Trigger",
    "申请人": "Requester",
    "处理人": "Handler",
    "内容": "Content",
    "批准": "Approve",
    "批准并执行该申请": "Approve and execute this request",
    "驳回该申请": "Reject this request",
    "成本中心编码": "Cost Center Code",
    "编码": "Code",
    "月预算 USD": "Monthly Budget USD",
    "月预算": "Monthly Budget",
    "部门分摊": "Department Chargeback",
    "分摊成本 USD": "Allocated Cost USD",
    "请求数": "Requests",
    "分摊规则": "Allocation Rule",
    "分摊成本": "Allocated Cost",
    "审批流": "Approval Flow",
    "审批角色": "Approver Role",
    "金额阈值 USD": "Amount Threshold USD",
    "SLA 小时": "SLA Hours",
    "金额阈值": "Amount Threshold",
    "频率": "Frequency",
    "接收人": "Recipients",
    "导出 CSV 报表": "Export CSV report",
    "内部账单": "Internal Invoice",
    "金额 USD": "Amount USD",
    "发票备注": "Invoice Note",
    "确认人": "Confirmed By",
    "确认时间": "Confirmed At",
    "驳回原因": "Reject Reason",
    "金额": "Amount",
    "生成本月": "Generate This Month",
    "按当前账期生成分摊和内部账单": "Generate chargebacks and internal invoices for the current period",
    "输入账期 YYYY-MM，留空则导出全部": "Enter period YYYY-MM, leave blank to export all",
    "输入账期 YYYY-MM，留空则生成本月": "Enter period YYYY-MM, leave blank to generate this month",
    "请输入驳回原因": "Enter rejection reason",
    "已生成分摊和内部账单": "Chargebacks and internal invoices generated",
    "已提交审批：": "Approval submitted: ",
    "请选择项目空间后再发放 API Key": "Select a project before issuing an API Key",
    "发放 Key 失败": "Failed to issue Key",
    "当前账号没有可发放 Key 的项目权限，请联系项目负责人或管理员把你加入项目。": "This account has no project that can issue Keys. Ask a project owner or admin to add you to a project.",
    "创建内部调用 Key": "Create Internal API Key",
    "创建 Key 步骤": "Key Creation Steps",
    "选择项目": "Project",
    "填写用途": "Purpose",
    "模型范围": "Model Scope",
    "安全护栏": "Guardrails",
    "确认发放": "Review",
    "选择 Key 归属项目": "Choose the Key project",
    "Key 必须挂在项目空间下，用量和成本会归集到这个项目。": "Keys must belong to a project; usage and cost are attributed to that project.",
    "说明用途和环境": "Describe purpose and environment",
    "名称建议能看出调用方、环境和用途，后续审计会更容易定位。": "Use a name that shows the caller, environment, and purpose for easier auditing.",
    "设置模型范围": "Set model scope",
    "全部可路由模型": "All routed models",
    "由平台路由策略决定最终可调用范围": "The platform routing policy decides the final callable scope.",
    "指定模型白名单": "Specific model allowlist",
    "只允许这个 Key 调用已勾选的模型": "This Key can call only the selected models.",
    "当前没有可选择的启用模型。请先在模型目录和路由策略里启用模型。": "No enabled models are available. Enable models in the model catalog and routing policy first.",
    "设置安全护栏": "Set guardrails",
    "可以先使用默认额度，之后再按调用量调整。IP 白名单留空表示不限来源。": "You can start with the default limits and adjust later. Leave IP allowlist empty to allow any source.",
    "确认后生成 Key": "Review and generate Key",
    "完整 Key 只会展示一次。关闭弹窗后只能看到前后缀，后续需要通过轮换生成新 Key。": "The full Key is shown only once. After closing the modal, only the prefix and suffix are visible; rotate to generate a new Key later.",
    "上一步": "Back",
    "下一步": "Next",
    "发放中": "Issuing",
    "生成 Key": "Generate Key",
    "普通用户看到的是当前账号可见的模型；实际调用还会受项目 Key 白名单和项目权限限制。": "Users see models visible to this account; actual calls are still limited by the project Key allowlist and project permissions.",
    "可用模型需要同时满足：模型目录启用、至少一条路由启用、Provider 或账号资源健康。": "Available models require an enabled catalog entry, at least one enabled route, and a healthy Provider or account resource.",
    "模型未启用": "Model disabled",
    "模型目录状态不是启用，前台不会作为可调用模型。": "The model catalog status is not active, so it is not callable from the frontend.",
    "按权限可见": "Visible by permission",
    "当前账号可见此模型；实际调用还会受项目 Key 白名单和运行时路由策略限制。": "This model is visible to the account; actual calls are still limited by the project Key allowlist and runtime routing policy.",
    "管理员需要在路由策略中把该模型映射到一个 Provider 上游模型。": "An admin needs to map this model to an upstream Provider model in routing policy.",
    "路由未启用": "Route disabled",
    "已有 Provider 线路，但线路状态未启用，运行时不会命中。": "Provider routes exist, but none are enabled, so runtime traffic will not hit them.",
    "线路需检查": "Route needs attention",
    "启用线路存在，但 Provider 或账号资源不是健康启用状态。": "Enabled routes exist, but the Provider or account resource is not active and healthy.",
    "可调用": "Callable",
    "当前没有可调用模型。通常原因是管理员还没有启用模型目录或路由策略，或你的项目/Key 未被授予模型范围。": "No callable models are available. Usually the model catalog or routing policy is not enabled, or your project/Key has no model scope.",
    "当前还没有模型目录。请先维护模型目录，再配置路由策略。": "No model catalog exists yet. Maintain the model catalog first, then configure routing policy.",
    "当前筛选下没有可见模型。可用性由模型目录、路由策略、项目成员和 Key 白名单共同决定。": "No visible models match the current filters. Availability is determined by the model catalog, routing policy, project membership, and Key allowlist.",
    "当前没有可演练模型。请先在路由策略里启用至少一条模型线路。": "No playground-ready models are available. Enable at least one model route first.",
    "选择身份源模板": "Choose Identity Source Template",
    "选择后会自动填充协议、登录图标、Scope、Claim 和常见端点。": "Choosing a template fills protocol, login icon, scopes, claims, and common endpoints.",
    "登录按钮": "Login Button",
    "默认 Scope": "Default Scope",
    "必填项": "Required Fields",
    "Issuer、Client ID、Client Secret、Callback URL": "Issuer, Client ID, Client Secret, Callback URL",
    "适合标准 OIDC 服务，填写 Issuer 后一般可自动发现端点。": "Best for standard OIDC services; endpoints can usually be discovered from the issuer.",
    "适合非标准 OAuth2 服务，需要确认授权、Token 和用户信息端点。": "Best for non-standard OAuth2 services; verify authorization, token, and userinfo endpoints.",
    "请先填写至少一项希望提升后的目标额度": "Enter at least one target quota to increase",
    "不限额": "Unlimited",
    "项目空间内配置的专属模型调用额度": "Project-specific model call quota",
    "该记录": "this record",
    "通知模式": "Notification Mode",
    "目标对象": "Target Object",
    "脱敏 Prompt": "Mask Prompt",
    "错误透传": "Error Passthrough",
    "每行一个 CIDR 或 IP，留空表示不配置白名单。": "One CIDR or IP per line. Leave blank for no allowlist.",
    "开启后策略要求请求与响应审计避免直接展示完整 Prompt。": "When enabled, request and response audit should avoid showing full prompts directly.",
    "通用 Webhook 告警通知": "Generic Webhook alert notification",
    "飞书机器人告警通知": "Feishu bot alert notification",
    "钉钉机器人告警通知": "DingTalk bot alert notification",
    "企业微信机器人告警通知": "WeCom bot alert notification",
    "Slack Incoming Webhook 告警通知": "Slack Incoming Webhook alert notification",
    "Discord Webhook 告警通知": "Discord Webhook alert notification",
    "Telegram Bot 告警通知": "Telegram Bot alert notification",
    "WhatsApp Cloud API 告警通知": "WhatsApp Cloud API alert notification",
    "SMTP 邮件告警通知": "SMTP email alert notification",
    "告警通知渠道": "Alert notification channel",
    "SMTP 已配置": "SMTP configured",
    "SMTP 未配置": "SMTP not configured",
    "通知渠道列表": "Notification Channel List",
    "新增通知渠道": "Create Notification Channel",
    "渠道类型": "Channel Type",
    "签名密钥": "Signing Secret",
    "可选预留。当前按普通机器人 Webhook 发送，留空不影响通知。": "Optional reserved field. Notifications are sent by normal bot Webhook; leaving it blank is fine.",
    "SMTP 端口": "SMTP Port",
    "SMTP 用户名": "SMTP Username",
    "SMTP 密码": "SMTP Password",
    "收件人": "Recipients",
    "WhatsApp 收件人": "WhatsApp Recipient",
    "多个邮箱用逗号分隔。": "Separate multiple emails with commas.",
    "Bot Token 已配置": "Bot token configured",
    "Bot Token 未配置": "Bot token not configured",
    "Access Token 已配置": "Access token configured",
    "Access Token 未配置": "Access token not configured",
    "按 Webhook、Slack、Discord、Telegram、WhatsApp、飞书、钉钉、企业微信和邮件快速配置告警通知目标。": "Quickly configure alert notification targets across Webhook, Slack, Discord, Telegram, WhatsApp, Feishu, DingTalk, WeCom, and email.",
    "地址/目标": "Address / Target",
    "凭证": "Credential",
    "告警类型": "Alert Type",
    "监控名称": "Monitor Name",
    "监控类型": "Monitor Type",
    "检查间隔": "Check Interval",
    "最近检查": "Last Check",
    "环境": "Environment",
    "区域": "Region",
    "资源类型": "Resource Type",
    "资源分组": "Resource Group",
    "失败次数": "Failures",
    "冷却至": "Cooldown Until",
    "速率限制 RPM": "Rate Limit RPM",
    "Token 限制 TPM": "Token Limit TPM",
    "质量分": "Quality Score",
    "成本分": "Cost Score",
    "粘性": "Sticky",
    "不限": "Unlimited",
    "系统提示词": "System Prompt",
    "支持参数": "Supported Parameters",
    "输入模态": "Input Modalities",
    "输出模态": "Output Modalities",
    "计费输入": "Input Price",
    "计费输出": "Output Price",
    "Embedding 计费": "Embedding Price",
    "模型家族": "Model Family",
    "上下文窗口": "Context Window",
    "公开 Base URL": "Public Base URL",
    "默认超时": "Default Timeout",
    "审计保留": "Audit Retention",
    "API Key 前缀": "API Key Prefix",
    "API Key 随机长度": "API Key Random Length",
    "新建和轮换 Key 时使用；建议以 _ 结尾，例如 sk_。": "Used when creating and rotating Keys; ending with _ is recommended, for example sk_.",
    "前缀后面的随机字符数，系统会限制在 24-128 之间。": "Number of random characters after the prefix. The system limits it to 24-128.",
    "用户名字段": "Username Claim",
    "邮箱字段": "Email Claim",
    "团队字段": "Team Claim",
    "新增身份源": "Create Identity Source",
    "新增可选角色": "Create Assignable Role",
    "配置用户管理新增/编辑时可选择的后台角色。权限边界由系统内置角色模型控制。": "Configure roles available when creating or editing users. Permission boundaries are controlled by built-in role models.",
    "Embedding 价 USD/1M": "Embedding Price USD/1M",
    "IP 白名单": "IP Allowlist",
    "IP 白名单，逗号分隔": "IP Allowlist, comma-separated",
    "Key 名称": "Key Name",
    "Key 必须挂在已有项目下，用于该项目的内部应用调用网关。": "Keys must be attached to an existing project for that project's internal applications to call the gateway.",
    "SQLite 备份": "SQLite Backups",
    "上游模型/部署名": "Upstream Model / Deployment",
    "下载 SQLite 备份文件": "Download SQLite backup file",
    "为该 Provider 新增模型路由": "Create model route for this Provider",
    "为该对外模型新增 Provider 线路": "Create Provider route for this public model",
    "为该项目创建内部 API Key": "Create an internal API Key for this project",
    "从用户管理中选择团队负责人，用于审批和审计归属。": "Select a team owner from user management for approvals and audit attribution.",
    "作用域": "Scope",
    "作用域 ID": "Scope ID",
    "保存项目额度": "Save project quota",
    "内部 Key 列表": "Internal Key List",
    "创建、下载和恢复 TokenHub SQLite 数据库快照。": "Create, download, and restore TokenHub SQLite database snapshots.",
    "创建备份": "Create Backup",
    "发件人": "Sender",
    "发放 Key": "Issue Key",
    "可用供应商": "Available Providers",
    "后台操作、变更对象、操作人和时间": "Admin operations, changed objects, actors, and time",
    "告警事件列表": "Alert Event List",
    "告警通知的渠道、目标和发送结果": "Alert notification channel, target, and delivery result",
    "团队 ID": "Team ID",
    "团队名称": "Team Name",
    "处理 Key 发放、额度提升和模型开通审批。": "Handle approvals for Key issuance, quota increases, and model access.",
    "多个收件人用英文逗号分隔。": "Separate multiple recipients with commas.",
    "对外模型": "Public Model",
    "将数据库恢复到该备份": "Restore the database to this backup",
    "并发": "Concurrency",
    "归属项目": "Project",
    "成本中心、负责人和部门归属配置": "Cost center, owner, and department attribution config",
    "成本优先模式会优先选择该评分更高的线路，分数越高代表越省。": "Cost-first mode prefers routes with higher scores; higher means cheaper.",
    "成本评分 1-100": "Cost Score 1-100",
    "批量创建时留空，会为每个统一模型使用同名上游模型。": "Leave blank during bulk creation to use the same upstream model name for each unified model.",
    "按模型、项目和日期归集 Token 与成本": "Aggregate tokens and costs by model, project, and date",
    "按项目发放内部 API Key，限制模型白名单、额度、并发和有效期。": "Issue internal API Keys by project and limit model allowlists, quotas, concurrency, and validity.",
    "支持参数，逗号分隔": "Supported parameters, comma-separated",
    "数字越小越先调用；新增时留空会自动排在该统一模型已有 Provider 后面。": "Lower numbers are called first; leave blank when creating to place it after existing Providers for the unified model.",
    "新增路由时可多选模型；编辑已有路由时仍按单条规则调整。": "Select multiple models when creating routes; editing still adjusts a single rule.",
    "新增项目": "Create Project",
    "日成本 USD": "Daily Cost USD",
    "最近命中": "Last Hit",
    "最近检测": "Last Check",
    "最近消息": "Latest Message",
    "最近状态": "Latest Status",
    "月成本 USD": "Monthly Cost USD",
    "查看告警通知的发送状态、目标和失败原因。": "View alert notification delivery status, target, and failure reason.",
    "检测 Provider 可用性": "Check Provider availability",
    "模型白名单，逗号分隔": "Model allowlist, comma-separated",
    "渠道配置": "Channel Config",
    "生成新 Key，并立即吊销旧 Key": "Generate a new Key and revoke the old Key immediately",
    "用途/环境": "Purpose / Environment",
    "留空则沿用统一模型名": "Leave blank to use the unified model name",
    "留空自动追加": "Leave blank to append automatically",
    "留空表示不限制 Key 级模型白名单；实际可调用模型仍受模型目录和路由策略约束。": "Leave blank for no Key-level model allowlist; callable models are still constrained by the model catalog and routing policy.",
    "留空表示不限来源 IP。": "Leave blank to allow any source IP.",
    "目录计价": "Catalog Pricing",
    "目标": "Target",
    "目标类型": "Target Type",
    "确认该内部账单": "Confirm this internal invoice",
    "立即执行该健康检测": "Run this health check now",
    "立即检测": "Run Check",
    "策略": "Strategy",
    "管理 TokenHub 后台登录账号、角色权限、归属团队和账号状态。": "Manage TokenHub console accounts, roles, team attribution, and account status.",
    "粘性会话": "Sticky Session",
    "系列": "Family",
    "编辑时留空表示不修改。": "Leave blank while editing to keep unchanged.",
    "编辑时留空表示不修改现有 Key；只有填写新值才会覆盖。": "Leave blank while editing to keep the existing Key; only a new value overwrites it.",
    "能力标签，逗号分隔": "Capability tags, comma-separated",
    "评分": "Score",
    "请求 ID、模型、状态码、Provider 路由和延迟": "Request ID, model, status code, Provider route, and latency",
    "调度策略": "Scheduling Strategy",
    "负责人用户 ID": "Owner User ID",
    "质量优先模式会优先选择该评分更高的线路。": "Quality-first mode prefers routes with higher scores.",
    "质量评分 1-100": "Quality Score 1-100",
    "费用归集口径，可与团队不同；用于成本归集和用量统计。": "Cost attribution dimension; it may differ from the team and is used for cost and usage analytics.",
    "模型用量": "Model Usage",
    "成员用量": "Member Usage",
    "项目归因": "Project Attribution",
    "成员成本": "Member Cost",
    "Provider 成本": "Provider Cost",
    "Provider 明细成本": "Provider Detail Cost",
    "命中 Provider": "Matched Provider",
    "资源实例": "Resource Instance",
    "资源实例 ID": "Resource Instance ID",
    "路由数": "Routes",
    "轮换": "Rotate",
    "输入价 USD/1M": "Input Price USD/1M",
    "输出价 USD/1M": "Output Price USD/1M",
    "运行时触发的额度、成本和 Provider 健康事件。": "Quota, cost, and Provider health events triggered at runtime.",
    "通知发送记录": "Notification Delivery Records",
    "通过默认通知渠道发送该告警": "Send this alert through the default notification channel",
    "配置通知渠道": "Configure Notification Channel",
    "间隔秒数": "Interval Seconds",
    "项目 ID": "Project ID",
    "项目列表": "Project List",
    "项目名称": "Project Name",
    "项目是企业内部 AI 使用、Key、额度和成本归属的基本单元。": "Projects are the basic unit for internal AI usage, Keys, quotas, and cost attribution.",
    "额度": "Quota",
    "额度提升、Key 发放和模型开通审批记录": "Quota increase, Key issuance, and model access approval records",
    "驳回该内部账单": "Reject this internal invoice",
    "默认检测项": "Default Checks",
    "更新路由顺序失败": "Failed to update route order",
    "点击停用 API Key": "Click to disable API Key",
    "点击启用 API Key": "Click to enable API Key",
    },
  ja: {
    "请填写此字段": "このフィールドを入力してください",
    "总览": "概要",
    "网关概览": "ゲートウェイ概要",
    "开始使用": "はじめに",
    "我的资源": "自分のリソース",
    "我的用量": "自分の利用量",
    "团队工作台": "チームワークスペース",
    "24H 可用率": "24時間 稼働率",
    "OAuth 登录失败": "OAuth ログインに失敗しました",
    "Provider 健康摘要": "Provider ヘルス概要",
    "Provider 可用性监控": "Provider 可用性モニタリング",
    "Provider 在线": "Provider オンライン",
    "Token 端点": "Token エンドポイント",
    "Token 结构": "Token 構造",
    "auto 会根据名称、Issuer URL 和类型自动选择登录页图标。": "auto は名前、Issuer URL、種類に基づいてログインページのアイコンを自動選択します。",
    "不再提示": "今後表示しない",
    "两次输入的密码不一致": "入力した 2 つのパスワードが一致しません",
    "只显示当前账号拥有发 Key 权限的项目；一个人可以被分配到多个项目。": "現在のアカウントが Key を発行できるプロジェクトのみ表示します。1 人が複数のプロジェクトに割り当てられます。",
    "团队 Claim": "チーム Claim",
    "团队字段无法映射时使用。": "チームフィールドをマッピングできない場合に使用します。",
    "基础监控 · L1/L2": "基本モニタリング · L1/L2",
    "定位错误码、模型和延迟": "エラーコード、モデル、レイテンシを特定",
    "密码已重置，请使用新密码登录": "パスワードをリセットしました。新しいパスワードでログインしてください",
    "密码至少 8 位": "パスワードは 8 文字以上必要です",
    "密码重置失败": "パスワードのリセットに失敗しました",
    "就绪度": "準備度",
    "必须与 OAuth 应用中登记的 Redirect URI 完全一致；留空时按当前后端访问地址自动生成。": "OAuth アプリに登録された Redirect URI と完全に一致する必要があります。空欄の場合は現在のバックエンドアドレスから自動生成されます。",
    "按估算成本降序": "推定コストの降順",
    "按健康检测、账号资源和真实请求日志汇总上游渠道可用性。": "ヘルスチェック、アカウントリソース、実リクエストログから上流チャネルの可用性を集計します。",
    "授权端点": "認可エンドポイント",
    "授权范围": "認可スコープ",
    "提交中": "送信中",
    "故障": "障害",
    "新密码": "新しいパスワード",
    "无真实请求": "実リクエストなし",
    "无请求": "リクエストなし",
    "无请求样本": "リクエストサンプルなし",
    "暂无失败请求": "失敗したリクエストはありません",
    "最近失败请求": "最近の失敗リクエスト",
    "最近请求": "最近のリクエスト",
    "最近请求按成功、告警和失败聚合": "最近のリクエストを成功・警告・失敗で集計",
    "服务商 / 通道": "サービスプロバイダー / チャネル",
    "未配置账号资源": "アカウントリソースが未設定です",
    "用户信息端点": "ユーザー情報エンドポイント",
    "用户名 Claim": "ユーザー名 Claim",
    "留空时按图标、Issuer 或身份源名称自动推断。": "空欄の場合はアイコン、Issuer、または ID ソース名から自動推定します。",
    "留空表示不限制 Key 级模型白名单；实际可调用模型仍受模型目录、路由策略和项目权限约束。": "空欄の場合は Key レベルのモデル許可リストを制限しません。実際に呼び出せるモデルはモデルカタログ、ルーティングポリシー、プロジェクト権限によって制約されます。",
    "登录入口": "ログイン入口",
    "登录后自动加入该项目空间。": "ログイン後にこのプロジェクトスペースへ自動的に参加します。",
    "登录图标": "ログインアイコン",
    "登录按钮名称": "ログインボタン名",
    "真实延迟": "実レイテンシ",
    "真实监控 · L3": "実モニタリング · L3",
    "真实监控来自最近请求日志；基础监控来自 Provider 和账号资源健康状态。": "実モニタリングは最近のリクエストログに基づき、基本モニタリングは Provider とアカウントリソースのヘルス状態に基づきます。",
    "真实请求样本": "実リクエストサンプル",
    "确认新密码": "新しいパスワードの確認",
    "端点、Scope 与 Claim 映射": "エンドポイント、Scope、Claim マッピング",
    "综合状态": "総合ステータス",
    "自动加入默认项目时授予的项目权限。": "デフォルトプロジェクトに自動参加する際に付与されるプロジェクト権限。",
    "该资源": "このリソース",
    "请求健康时间线": "リクエストヘルスのタイムライン",
    "请求量与 Token 消耗趋势": "リクエスト量と Token 消費の推移",
    "请设置新的控制台登录密码。": "新しいコンソールログインパスワードを設定してください。",
    "调用趋势": "呼び出し推移",
    "质量评分": "品質スコア",
    "资源健康": "リソースヘルス",
    "身份源模板": "ID ソーステンプレート",
    "输入和输出 Token 占比": "入力と出力の Token 比率",
    "近30天趋势": "直近 30 日の推移",
    "连接方式": "接続方式",
    "通用 OAuth2": "汎用 OAuth2",
    "通用 OIDC": "汎用 OIDC",
    "邮箱 Claim": "メール Claim",
    "重置密码": "パスワードをリセット",
    "降级/慢响应": "劣化 / 低速応答",
    "首次 OAuth 登录创建用户时使用；不会覆盖已存在用户角色。": "初回 OAuth ログインでユーザーを作成する際に使用します。既存ユーザーのロールは上書きしません。",
    "首次登录授权": "初回ログイン認可",
    "高级配置": "詳細設定",
    "默认团队": "デフォルトチーム",
    "默认授权": "デフォルト認可",
    "默认角色": "デフォルトロール",
    "默认项目": "デフォルトプロジェクト",
    "默认项目角色": "デフォルトプロジェクトロール",
    "团队总览": "チーム概要",
    "团队报表": "チームレポート",
    "成本归因": "コスト配賦",
    "项目治理": "プロジェクトガバナンス",
    "可用模型": "利用可能モデル",
    "团队管理": "チーム管理",
    "团队成员": "チームメンバー",
    "团队信息": "チーム情報",
    "平台工作台": "プラットフォームワークスペース",
    "平台总览": "プラットフォーム概要",
    "全局用量": "全体利用量",
    "AI 资源": "AI リソース",
    "组织治理": "組織ガバナンス",
    "成本治理": "コストガバナンス",
    "安全审计导航": "セキュリティ監査",
    "安全总览": "セキュリティ概要",
    "告警导航": "アラート",
    "接入参考": "接続リファレンス",
    "模型演练场": "モデルプレイグラウンド",
    "接口文档": "API ドキュメント",
    "AI 接入": "AI 接続",
    "Provider 渠道": "Provider チャネル",
    "模型目录": "モデルカタログ",
    "路由策略": "ルーティングポリシー",
    "企业治理": "企業ガバナンス",
    "项目空间": "プロジェクト",
    "Key 管理": "Key 管理",
    "团队分组": "チーム",
    "用户管理": "ユーザー管理",
    "审批记录": "承認記録",
    "成本审计": "コスト監査",
    "用量统计": "利用統計",
    "请求日志": "リクエストログ",
    "成本账单": "コスト請求",
    "成本中心": "コストセンター",
    "导出报表": "レポート出力",
    "健康与告警": "ヘルスとアラート",
    "健康检测": "ヘルスチェック",
    "告警规则": "アラートルール",
    "告警事件": "アラートイベント",
    "通知渠道": "通知チャネル",
    "通知记录": "通知記録",
    "安全运维": "セキュリティ運用",
    "安全策略": "セキュリティポリシー",
    "代理出口": "プロキシ出口",
    "数据备份": "データバックアップ",
    "公告通知": "お知らせ",
    "系统设置": "システム設定",
    "新增系统设置": "システム設定を作成",
    "网关地址、审计保留、企业集成和默认策略": "ゲートウェイアドレス、監査保持、企業連携、デフォルトポリシー",
    "选择标准模型，按当前路由策略发起测试对话，验证 Provider、路由和返回内容。": "標準モデルを選択し、現在のルーティングでテスト会話を実行します。",
    "面向业务开发者的模型 API 调用说明、认证方式、示例代码和错误排查。": "開発者向けのモデル API、認証、サンプル、トラブルシュートです。",
    "按模型、项目和日期查看请求量、Token 和成本归因。": "モデル、プロジェクト、日付別にリクエスト、Token、コストを確認します。",
    "按 Provider 和项目归集估算成本，辅助成本分摊。": "Provider とプロジェクト別に推定コストを集計します。",
    "查看最近请求日志、状态码、模型路由和延迟。": "最近のリクエストログ、ステータス、モデルルート、レイテンシを確認します。",
    "查看运行时触发的额度、成本和 Provider 健康告警。": "実行時に発生したクォータ、コスト、Provider ヘルスのアラートを確認します。",
    "查看告警 Webhook 发送结果、目标和失败原因。": "アラート通知の送信結果、宛先、失敗理由を確認します。",
    "处理 Key 发放、额度提升和模型开通等治理审批。": "Key 発行、クォータ増額、モデル開通などの承認を処理します。",
    "登录控制台": "ログインコンソール",
    "使用": "使用",
    "登录": "ログイン",
    "企业 AI 访问与成本治理平台": "企業向け AI アクセス・コストガバナンス",
    "账号 / 邮箱": "アカウント / メール",
    "密码": "パスワード",
    "登录中": "ログイン中",
    "TokenHub 控制台": "TokenHub コンソール",
    "展开菜单": "メニューを展開",
    "折叠菜单": "メニューを折りたたむ",
    "退出登录": "ログアウト",
    "界面语言": "表示言語",
    "选择控制台显示语言，偏好会保存在当前浏览器。": "コンソールの表示言語を選択します。設定はこのブラウザに保存されます。",
    "当前语言": "現在の言語",
    "平台管理员": "プラットフォーム管理者",
    "默认项目空间": "デフォルトプロジェクトスペース",
    "平台工程团队": "プラットフォームエンジニアリングチーム",
    "负责内部 AI Gateway 接入与平台治理": "内部 AI Gateway のオンボーディングとプラットフォームガバナンスを担当します。",
    "AI 平台成本中心": "AI プラットフォームコストセンター",
    "平台工程与共享 AI 基础设施费用归属": "プラットフォームエンジニアリングと共有 AI インフラのコスト帰属です。",
    "网关基础设置": "ゲートウェイ基本設定",
    "模型 API 对外地址、请求超时和审计保留周期": "公開モデル API アドレス、リクエストタイムアウト、監査保持期間です。",
    "OpenAI Compatible Gateway 默认配置": "OpenAI 互換ゲートウェイのデフォルト設定です。",
    "产品流程": "プロダクトフロー",
    "当前状态": "現在の状態",
    "接入 Provider": "Provider 接続",
    "配置上游服务商、Base URL、API Key，并映射到标准模型目录。": "上流 Provider、Base URL、API Key を設定し、標準モデルカタログへマッピングします。",
    "维护模型目录": "モデルカタログ管理",
    "定义内部对外模型名、上下文窗口和计价口径。": "社内向け公開モデル名、コンテキスト長、課金単位を定義します。",
    "建立路由策略": "ルーティングポリシー作成",
    "把对外模型映射到 Provider 的上游模型，并配置优先级与权重。": "公開モデルを Provider の上流モデルへマッピングし、優先度と重みを設定します。",
    "发放 API Key": "API Key 発行",
    "创建和维护当前权限范围内的内部调用凭证。": "現在の権限範囲内で内部呼び出し用の認証情報を作成・管理します。",
    "管理团队": "チーム管理",
    "维护团队资料、负责人和费用归属。": "チーム情報、責任者、コスト帰属を管理します。",
    "管理成员": "メンバー管理",
    "维护本团队成员账号和状态。": "チームメンバーのアカウントと状態を管理します。",
    "查看用量": "利用量確認",
    "查看当前权限范围内的请求量、Token 和成本。": "現在の権限範囲内のリクエスト、Token、コストを確認します。",
    "我的用量概览": "自分の利用概要",
    "个人范围": "個人範囲",
    "可见项目": "表示可能なプロジェクト",
    "条用量记录": "件の利用記録",
    "按当前账号权限汇总": "現在のアカウント権限で集計",
    "查看账单": "請求確認",
    "查看当前权限范围内的成本归因。": "現在の権限範囲内のコスト帰属を確認します。",
    "日志与治理": "ログとガバナンス",
    "查看请求日志、后台操作、告警规则和安全策略。": "リクエストログ、管理操作、アラートルール、セキュリティポリシーを確認します。",
    "个可选聊天模型": "件の利用可能なチャットモデル",
    "条启用路由": "件の有効ルート",
    "条启用公告": "件の有効なお知らせ",
    "管理公告": "お知らせ管理",
    "暂无公告说明": "お知らせ説明なし",
    "数量": "数",
    "企业内部应用治理单元": "社内アプリケーションのガバナンス単位",
    "内部调用凭证": "内部呼び出し用認証情報",
    "上游渠道实例，包含 Base URL 与 Key": "Base URL と Key を含む上流チャネルインスタンス",
    "对外模型目录": "公開モデルカタログ",
    "对外模型到 Provider 的映射规则": "公開モデルから Provider へのマッピングルール",
    "治理事件": "ガバナンスイベント",
    "当前权限范围内的用户账号": "現在の権限範囲内のユーザーアカウント",
    "系统设置分类": "システム設定カテゴリ",
    "基础设置": "基本設定",
    "角色配置": "ロール設定",
    "身份源": "ID ソース",
    "搜索名称、ID、状态": "名前、ID、状態を検索",
    "搜索模型": "モデルを検索",
    "搜索模型、能力、参数": "モデル、機能、パラメータを検索",
    "新增": "作成",
    "新增模型": "モデル作成",
    "新增 Provider": "Provider 作成",
    "账号集成": "アカウント連携",
    "创建 Provider 步骤": "Provider 作成ステップ",
    "接入方式": "接続方式",
    "渠道信息": "Provider 情報",
    "默认通道": "デフォルトチャネル",
    "账号与凭据": "アカウントと認証情報",
    "路由与确认": "ルート確認",
    "选择接入方式": "接続方式を選択",
    "先告诉 TokenHub 你手里有什么：上游 API Key、OpenAI 账号资源，或者只是先占位建路由。": "まず手元にあるものを選びます。上流 API Key、OpenAI アカウントリソース、またはルート用の仮 Provider です。",
    "账号资源池会自动推荐 OpenAI 兼容通道，下一步只需确认 Base URL 和账号凭据。": "アカウントリソースプールでは OpenAI 互換チャネルを自動推奨します。次に Base URL とアカウント認証情報を確認します。",
    "确认账号通道和基础信息": "アカウント用チャネルと基本情報を確認",
    "选择渠道和基础信息": "Provider と基本情報を選択",
    "账号资源池已为你选好默认通道。这里通常只确认 Base URL；账号走企业代理时再修改。": "アカウントプール用のデフォルトチャネルを選択済みです。通常は Base URL だけ確認し、企業プロキシ経由の場合のみ変更します。",
    "选择上游渠道商模板，TokenHub 会带出类型、Base URL 和可映射模型。": "上流 Provider テンプレートを選ぶと、TokenHub が種別、Base URL、マッピング可能モデルを入力します。",
    "推荐通道": "推奨チャネル",
    "默认通道只负责协议与 Base URL，真实账号 Token 会在下一步保存为账号资源。": "デフォルトチャネルはプロトコルと Base URL のみを扱います。実際のアカウント Token は次のステップでアカウントリソースとして保存されます。",
    "选择模型类型和渠道商": "モデル種別と Provider を選択",
    "先确定要接入哪一类模型和哪个上游渠道，下一步再填写凭据和基础配置。": "先に接続するモデル種別と上流 Provider を決め、次のステップで認証情報と基本設定を入力します。",
    "配置账号与凭据": "認証情報を設定",
    "填写 Provider 基础信息，并选择是直接保存 API Key、接入账号资源池，还是稍后补齐凭据。": "Provider の基本情報を入力し、API Key を直接保存するか、アカウントリソースプールを使うか、後で設定するかを選択します。",
    "选择是直接保存 API Key、接入账号资源池，还是稍后补齐凭据。": "API Key を直接保存するか、アカウントリソースプールを使うか、後で認証情報を追加するかを選択します。",
    "确认路由策略": "ルーティングを確認",
    "选择是否自动创建默认路由，并确认要映射到标准模型目录的上游模型。": "デフォルトルートを自動作成するかを選択し、標準モデルカタログへマッピングする上流モデルを確認します。",
    "可映射模型": "マッピング可能モデル",
    "模型协议": "モデルプロトコル",
    "兼容协议": "互換プロトコル",
    "通道名称": "チャネル名",
    "凭据方式": "認証情報方式",
    "已选模型": "選択済みモデル",
    "无": "なし",
    "保存中": "保存中",
    "保存 Provider": "Provider を保存",
    "请先选择一个渠道商。": "先に Provider を選択してください。",
    "请先选择一种接入方式。": "先に接続方式を選択してください。",
    "请填写渠道名称。": "Provider 名を入力してください。",
    "请填写通道名称。": "チャネル名を入力してください。",
    "账号资源配置不完整": "アカウントリソース設定が不完全です。",
    "认证与账号来源": "認証とアカウント元",
    "选择 Provider 使用哪一种上游凭据。账号集成会把账号作为资源池管理，适合 OpenAI subscription 或多个账号轮询。": "Provider が使用する上流認証情報を選択します。アカウント連携は OpenAI subscription や複数アカウントのルーティングに適したリソースプールとして管理します。",
    "直接 API Key": "直接 API Key",
    "把上游 Key 保存到 Provider，适合单账号或兼容 API。": "上流 Key を Provider に保存します。単一アカウントや互換 API に適しています。",
    "账号资源池": "アカウントリソースプール",
    "适合 OpenAI 账号、Subscription 或多账号轮询，默认通道会自动推荐。": "OpenAI アカウント、Subscription、複数アカウントのローテーションに適しており、デフォルトチャネルは自動推奨されます。",
    "稍后配置": "後で設定",
    "先创建 Provider 和路由，稍后再添加 Key 或账号资源。": "先に Provider とルートを作成し、後で Key またはアカウントリソースを追加します。",
    "账号资源配置": "アカウントリソース設定",
    "账号资源会在 Provider 创建成功后自动加入当前 Provider。": "Provider 作成後、このアカウントリソースは自動で紐づきます。",
    "账号资源名称": "アカウントリソース名",
    "账号授权": "アカウント認可",
    "输入账号地址并打开授权页；授权完成后 TokenHub 会从回调 URL 自动回填 Token。": "アカウントアドレスを入力して認可ページを開きます。認可後、TokenHub は callback URL から Token を自動入力します。",
    "使用 OpenAI/Codex OAuth 授权账号；TokenHub 会在后端换取并保存账号 Token。": "OpenAI/Codex OAuth でアカウントを認可します。TokenHub がバックエンドでアカウント Token を交換して保存します。",
    "OpenAI/Codex 授权": "OpenAI/Codex 認可",
    "账号地址/邮箱": "アカウントアドレス / メール",
    "用于区分账号资源，可填写邮箱或账号系统里的唯一地址。": "アカウントリソースを識別するため、メールまたはアカウントシステム内の一意なアドレスを入力します。",
    "账号授权地址": "認可 URL",
    "粘贴上游账号系统的授权地址；TokenHub 会带上本页回调地址。": "上流アカウントシステムの認可 URL を貼り付けます。TokenHub はこのページの callback URL を付与します。",
    "打开授权": "認可を開く",
    "授权中": "認可中",
    "本页回调地址": "Callback URL",
    "授权应用跳回这个地址后，TokenHub 会自动读取 access_token / refresh_token / id_token。": "認可アプリがこの URL に戻ると、TokenHub は access_token / refresh_token / id_token を自動で読み取ります。",
    "点击后由后端生成授权地址；授权完成会带 code 回到本页并自动换取 Token。": "クリックするとバックエンドが認可 URL を生成します。認可後、code がこのページに戻り、自動で Token に交換されます。",
    "复制回调地址": "Callback URL をコピー",
    "回调结果": "Callback 結果",
    "如果授权页没有自动跳回本页，把完整 callback URL 或 URL fragment 粘贴到这里。": "認可ページが自動で戻らない場合は、完全な callback URL または URL fragment をここに貼り付けます。",
    "解析回填": "解析して入力",
    "等待授权回填": "認可入力待ち",
    "已回填账号 Token": "アカウント Token 入力済み",
    "已回填访问 Token": "Access Token 入力済み",
    "已回填刷新 Token": "Refresh Token 入力済み",
    "已回填 ID Token": "ID Token 入力済み",
    "打开授权页后，请在上游账号系统完成授权。": "認可ページを開いた後、上流アカウントシステムで認可を完了してください。",
    "已打开 OpenAI/Codex 授权页，授权完成后会自动回填账号 Token。": "OpenAI/Codex 認可ページを開きました。認可後、アカウント Token が自動入力されます。",
    "请先填写账号授权地址。": "先に認可 URL を入力してください。",
    "账号授权地址格式不正确。": "認可 URL の形式が正しくありません。",
    "未在回调结果中识别到 Token。": "Callback 結果から Token を識別できませんでした。",
    "账号授权失败": "アカウント認可に失敗しました",
    "授权回调缺少会话信息，请重新打开授权。": "認可 callback にセッション情報がありません。認可を開き直してください。",
    "正在换取账号 Token...": "アカウント Token を交換しています...",
    "账号授权换取 Token": "アカウント認可 Token 交換",
    "账号授权换取 Token 失败": "アカウント認可 Token 交換に失敗しました",
    "生成账号授权地址": "アカウント認可 URL を生成",
    "生成账号授权地址失败": "アカウント認可 URL の生成に失敗しました",
    "回调里只有授权 code，当前版本还需要返回 Token 的授权地址，或在高级选项中手动粘贴 Token。": "Callback には認可 code のみがあります。現バージョンでは Token を返す認可 URL、または詳細設定での手動 Token 入力が必要です。",
    "已从回调 URL 自动回填账号 Token。": "Callback URL からアカウント Token を自動入力しました。",
    "已从粘贴的回调结果回填账号 Token。": "貼り付けた callback 結果からアカウント Token を入力しました。",
    "已复制回调地址。": "Callback URL をコピーしました。",
    "高级：手动粘贴 Token": "詳細: Token を手動貼り付け",
    "只有在授权回填不可用时使用；保存后 Token 不会再次显示。": "Callback 入力が使えない場合のみ使用します。保存後 Token は再表示されません。",
    "资源调度": "リソース調整",
    "这些配置决定账号资源参与路由时的权重、并发和限流。": "これらの設定は、このアカウントリソースのルート重み、同時実行数、レート制限を決めます。",
    "先完成账号授权回填；TokenHub 会把回填的 Token 保存为账号资源。": "先にアカウント認可入力を完了します。TokenHub は戻った Token をアカウントリソースとして保存します。",
    "请先完成账号授权回填，或在高级选项中手动粘贴 Token。": "先にアカウント認可入力を完了するか、詳細設定で Token を手動貼り付けしてください。",
    "收到账号授权回调，已打开账号池创建向导。": "アカウント認可 callback を受信し、アカウントプール作成ウィザードを開きました。",
    "Provider 将直接保存上游 API Key；如果需要账号池、刷新凭据或多账号调度，请切换为账号集成。": "Provider は上流 API Key を直接保存します。アカウントプール、更新用認証情報、複数アカウントの調整が必要な場合はアカウント連携に切り替えてください。",
    "保存后不会写入上游凭据，可稍后通过编辑 Provider 或账号集成补齐。": "保存時に上流認証情報は書き込まれません。後で Provider 編集またはアカウント連携から追加できます。",
    "已创建账号资源": "アカウントリソース作成済み",
    "Provider 已创建，但无法确认账号资源所属 Provider。": "Provider は作成されましたが、アカウントリソースの紐づけ先 Provider を確認できませんでした。",
    "创建账号资源": "アカウントリソース作成",
    "请填写至少一个账号 Token，或切换为稍后配置。": "少なくとも 1 つのアカウント Token を入力するか、後で設定に切り替えてください。",
    "请填写账号资源的 API Key，或切换为稍后配置。": "アカウントリソースの API Key を入力するか、後で設定に切り替えてください。",
    "Provider 账号资源": "Provider アカウントリソース",
    "OpenAI 账号资源": "OpenAI アカウントリソース",
    "添加账号资源": "アカウントリソースを追加",
    "账号资源": "アカウントリソース",
    "使用保存的 refresh token 更新账号访问 Token": "保存済み refresh token でアカウント Access Token を更新します。",
    "Token 已刷新": "Token を更新しました",
    "账号类型": "アカウント種別",
    "认证方式": "認証方式",
    "访问 Token": "アクセストークン",
    "刷新 Token": "リフレッシュトークン",
    "ID Token": "ID トークン",
    "账号邮箱": "アカウントメール",
    "账号 ID": "アカウント ID",
    "组织 ID": "組織 ID",
    "计划类型": "プラン種別",
    "已保存刷新 Token": "リフレッシュトークン保存済み",
    "未保存刷新 Token": "リフレッシュトークンなし",
    "普通 API Key 资源": "API Key リソース",
    "OpenAI Subscription 账号": "OpenAI Subscription アカウント",
    "OAuth 账号": "OAuth アカウント",
    "Personal Access Token": "Personal Access Token",
    "把 OpenAI subscription、PAT 或普通 API Key 作为 Provider 资源实例加入账号池，并参与路由权重、并发和限流调度。": "OpenAI subscription、PAT、通常の API Key を Provider リソースアカウントとして追加し、ルート重み、同時実行数、レート制限の調整に参加させます。",
    "OpenAI subscription / Codex OAuth access token 或 PAT；保存后不会再次显示。": "OpenAI subscription / Codex OAuth アクセストークンまたは PAT。保存後は再表示されません。",
    "可选，保存到加密凭据中，用于后续自动刷新能力。": "任意。今後の自動更新に備えて暗号化された認証情報として保存します。",
    "可选。填写后会自动提取账号邮箱、账号 ID、组织 ID 和计划类型。": "任意。入力するとアカウントメール、アカウント ID、組織 ID、プラン種別を自動抽出します。",
    "普通资源实例的上游 API Key；编辑时留空表示不修改。": "通常リソースインスタンスの上流 API Key。編集時に空のままなら変更しません。",
    "分组": "グループ",
    "RPM 限制": "RPM 制限",
    "TPM 限制": "TPM 制限",
    "配置": "設定",
    "配置路由": "ルート設定",
    "编辑": "編集",
    "删除": "削除",
    "保存": "保存",
    "取消": "キャンセル",
    "关闭": "閉じる",
    "操作": "操作",
    "暂无数据": "データがありません",
    "请选择": "選択してください",
    "开启": "オン",
    "关闭开关": "オフ",
    "启用": "有効",
    "停用": "無効",
    "禁用": "無効化",
    "重新加载": "再読み込み",
    "全选": "すべて選択",
    "清空": "クリア",
    "请选择至少一个统一模型": "少なくとも 1 つの統一モデルを選択してください",
    "确认删除": "削除の確認",
    "操作已完成": "操作が完了しました",
    "操作失败": "操作に失敗しました",
    "保存失败": "保存に失敗しました",
    "删除失败": "削除に失敗しました",
    "导出失败": "エクスポートに失敗しました",
    "连接失败": "接続に失敗しました",
    "登录失败": "ログインに失敗しました",
    "数据加载中，请稍后再操作。": "データ読み込み中です。しばらくしてから操作してください。",
    "请先创建项目，再在项目下发放 API Key。": "先にプロジェクトを作成してから API Key を発行してください。",
    "请先新增 Provider 渠道，再配置路由策略。": "先に Provider チャネルを作成してからルートを設定してください。",
    "请先维护模型目录，再新增路由策略。": "先にモデルカタログを整備してからルートを作成してください。",
    "点击排序": "並べ替え",
    "新 Key 仅展示一次：": "新しい Key は一度だけ表示されます: ",
    "新 Key 已生成": "新しい Key を生成しました",
    "请现在复制并保存这个 Key。关闭弹窗后将无法再次查看完整 Key，只能通过轮换生成新的 Key。": "この Key を今すぐコピーして保存してください。ダイアログを閉じると完全な Key は再表示できず、ローテーションで新しい Key を生成する必要があります。",
    "完整 Key": "完全な Key",
    "已复制": "コピー済み",
    "复制 Key": "Key をコピー",
    "我已保存，关闭": "保存して閉じる",
    "第": "項目",
    "条，共": "/",
    "每页": "件/ページ",
    "第一页": "最初のページ",
    "上一页": "前のページ",
    "下一页": "次のページ",
    "最后一页": "最後のページ",
    "名称": "名前",
    "说明": "説明",
    "状态": "状態",
    "更新时间": "更新日時",
    "类型": "タイプ",
    "协议": "プロトコル",
    "端口": "ポート",
    "优先级": "優先度",
    "权重": "重み",
    "模型": "モデル",
    "模型名": "モデル",
    "项目": "プロジェクト",
    "团队": "チーム",
    "成员": "メンバー",
    "用户": "ユーザー",
    "邮箱": "メール",
    "用户名": "ユーザー名",
    "角色": "ロール",
    "最近登录": "最終ログイン",
    "负责人": "責任者",
    "请求": "リクエスト",
    "总请求": "総リクエスト",
    "总 Token": "総 Token",
    "总成本": "総コスト",
    "模型调用 Token": "モデル呼び出し Token",
    "等待调用数据": "利用データ待ち",
    "输入 Token": "入力 Token",
    "输出 Token": "出力 Token",
    "成本": "コスト",
    "估算成本": "推定コスト",
    "时间": "時間",
    "延迟": "レイテンシ",
    "最终 Provider": "最終 Provider",
    "Provider 资源": "Provider リソース",
    "上游模型": "上流モデル",
    "客户端 IP": "クライアント IP",
    "输入": "入力",
    "输出": "出力",
    "总量": "合計",
    "上下文": "コンテキスト",
    "条线路": "件のルート",
    "未配置线路": "ルート未設定",
    "官方资源": "公式リソース",
    "可访问": "アクセス可",
    "能力": "機能",
    "最近路由": "最近のルート",
    "待请求": "未リクエスト",
    "请求与响应": "リクエストとレスポンス",
    "已记录快照": "スナップショット記録済み",
    "未记录": "未記録",
    "动作": "アクション",
    "操作人": "実行者",
    "对象": "対象",
    "对象 ID": "対象 ID",
    "来源 IP": "送信元 IP",
    "成功": "成功",
    "失败": "失敗",
    "成功率": "成功率",
    "失败请求": "失敗リクエスト",
    "平均延迟": "平均レイテンシ",
    "请求列表": "リクエスト一覧",
    "请求详情": "リクエスト詳細",
    "大模型请求历史": "モデルリクエスト履歴",
    "后台操作审计": "管理操作監査",
    "日志类型": "ログ種別",
    "请求状态筛选": "リクエスト状態フィルタ",
    "搜索请求历史": "リクエスト履歴を検索",
    "搜索请求 ID、模型、Provider、状态码": "リクエスト ID、モデル、Provider、状態コードを検索",
    "全部": "すべて",
    "所有模型": "すべてのモデル",
    "精选": "注目",
    "文本": "テキスト",
    "图像": "画像",
    "视频": "動画",
    "音频": "音声",
    "嵌入": "埋め込み",
    "重排序": "リランキング",
    "三方资源": "外部リソース",
    "模型大类": "モデルカテゴリ",
    "模型能力筛选": "モデル機能フィルタ",
    "通知渠道类型": "通知チャネル種別",
    "自动路由": "自動ルーティング",
    "搜索模型名称或 ID": "モデル名または ID を検索",
    "个模型": "モデル",
    "个匹配模型": "件の一致モデル",
    "没有匹配的模型": "一致するモデルがありません",
    "模型路由器": "モデルルーター",
    "模型路由规则": "モデルルーティングルール",
    "参考模型路由器思路，按平衡、质量优先或成本优先模式选择候选 Provider 线路，并在失败时自动回退。": "バランス、品質優先、コスト優先の戦略で Provider ルート候補を選択し、失敗時は順番に自動フォールバックします。",
    "平衡模式综合权重、质量和成本；质量优先会先选高质量线路；成本优先会先选低成本线路。调用失败时会按候选顺序自动回退。": "バランスモードは重み、品質、コストを統合します。品質優先は高品質ルート、コスト優先は低コストルートを優先し、失敗時は候補順にフォールバックします。",
    "条启用线路": "件の有効ルート",
    "个已配置路由": "件の設定済みルート",
    "统一模型": "統一モデル",
    "已配置": "設定済み",
    "全部模型": "すべてのモデル",
    "路由显示范围": "ルート表示範囲",
    "搜索模型或 Provider": "モデルまたは Provider を検索",
    "新增路由": "ルートを作成",
    "未配置": "未設定",
    "按上到下顺序调用": "上から順に呼び出します",
    "该统一模型还没有 Provider 线路": "この統一モデルには Provider ルートがありません",
    "拖动调整调用顺序": "ドラッグして呼び出し順を調整",
    "未配置 Base URL": "Base URL 未設定",
    "没有匹配的模型路由": "一致するモデルルートがありません",
    "主": "主",
    "路由": "ルート",
    "自定义": "カスタム",
    "对外模型列表": "公開モデル一覧",
    "维护内部应用调用的对外模型名，并查看每个模型可用的 Provider 线路。": "社内アプリケーションが呼び出す公開モデル名を管理し、モデルごとの利用可能な Provider ルートを確認します。",
    "Provider 列表": "Provider 一覧",
    "Provider 是一个可调用的上游渠道实例，包含服务商类型、Base URL、API Key、健康状态和标准模型路由。": "Provider は呼び出し可能な上流チャネルインスタンスで、Provider 種別、Base URL、API Key、ヘルス状態、標準モデルルートを含みます。",
    "路由线路": "ルート",
    "系统管理员": "システム管理者",
    "安全审计": "セキュリティ監査",
    "团队 Leader": "チームリーダー",
    "普通用户": "一般ユーザー",
    "全局": "グローバル",
    "本人": "本人",
    "可分配": "割り当て可",
    "是": "はい",
    "否": "いいえ",
    "角色标识": "ロールキー",
    "显示名称": "表示名",
    "数据范围": "データ範囲",
    "已吊销": "失効済み",
    "草稿": "下書き",
    "已归档": "アーカイブ済み",
    "待处理": "保留中",
    "已批准": "承認済み",
    "已驳回": "却下済み",
    "已确认": "確認済み",
    "健康": "正常",
    "正常": "正常",
    "告警": "警告",
    "降级": "低下",
    "异常": "異常",
    "不可用": "利用不可",
    "已过期": "期限切れ",
    "未知": "不明",
    "直连": "直接接続",
    "静默": "サイレント",
    "弹窗": "ポップアップ",
    "手动": "手動",
    "每日": "毎日",
    "每周": "毎週",
    "每月": "毎月",
    "超额拦截": "超過時ブロック",
    "仅告警": "警告のみ",
    "飞书": "Feishu",
    "钉钉": "DingTalk",
    "企业微信": "WeCom",
    "邮件": "メール",
    "操作审计": "操作監査",
    "Key 发放": "Key 発行",
    "预算变更": "予算変更",
    "模型开通": "モデル開通",
    "额度提升": "クォータ増額",
    "账单确认": "請求確認",
    "账单驳回": "請求却下",
    "平衡": "バランス",
    "质量优先": "品質優先",
    "成本优先": "コスト優先",
    "优先级 + 权重": "優先度 + 重み",
    "仅优先级": "優先度のみ",
    "文本对话": "チャット",
    "向量嵌入": "埋め込み",
    "模拟渠道": "Mock Provider",
    "OpenAI 官方": "OpenAI 公式",
    "OpenAI 兼容": "OpenAI 互換",
    "本地模型": "ローカルモデル",
    "Provider 健康": "Provider ヘルス",
    "资源实例健康": "リソースヘルス",
    "请求额度": "リクエストクォータ",
    "Token 额度": "Token クォータ",
    "成本额度": "コストクォータ",
    "错误率": "エラー率",
    "日成本额度": "日次コストクォータ",
    "日 Token 额度": "日次 Token クォータ",
    "P95 延迟": "P95 レイテンシ",
    "测试": "テスト",
    "健康变更": "ヘルス変更",
    "确认": "確認",
    "驳回": "却下",
    "导出": "エクスポート",
    "切换主题": "テーマ切替",
    "企业 AI 网关": "企業 AI ゲートウェイ",
    "统一接入与成本治理平台": "統合アクセス・コストガバナンス基盤",
    "显示密码": "パスワードを表示",
    "隐藏密码": "パスワードを隠す",
    "保持登录": "ログイン状態を保持",
    "忘记密码？": "パスワードを忘れた場合",
    "或": "または",
    "使用企业 SSO 登录": "企業 SSO でログイン",
    "做一个乐于助人的助手": "親切で役に立つアシスタントとして振る舞ってください",
    "你是企业内部 AI 助手。": "あなたは企業内 AI アシスタントです。",
    "用两句话介绍 TokenHub": "TokenHub を 2 文で紹介してください",
    "总结今天的工单重点": "今日のチケットの要点をまとめてください",
    "TokenHub 企业知识库": "TokenHub 企業ナレッジベース",
    "请用三句话介绍 TokenHub。": "TokenHub を 3 文で紹介してください。",
    "请先粘贴 CSV 内容。": "先に CSV 内容を貼り付けてください。",
    "用户导入失败": "ユーザーインポートに失敗しました",
    "搜索控制台": "コンソールを検索",
    "搜索模型、Provider、日志...": "モデル、Provider、ログを検索...",
    "搜索结果": "検索結果",
    "打开": "開く",
    "打开对应工作页面": "該当ワークスペースを開く",
    "没有找到匹配入口": "一致する入口がありません",
    "请尝试搜索模型、Key、用量、日志或设置。": "モデル、Key、利用量、ログ、設定で検索してください。",
    "常用操作": "よく使う操作",
    "当前位置": "現在位置",
    "全局平台范围": "全体プラットフォーム範囲",
    "安全审计范围": "セキュリティ監査範囲",
    "团队和项目范围": "チームとプロジェクト範囲",
    "个人可见范围": "個人表示範囲",
    "健康 Provider": "正常 Provider",
    "Provider 需要关注": "Provider の確認が必要",
    "错误请求": "エラーリクエスト",
    "记录": "レコード",
    "条匹配": "件一致",
    "没有匹配结果": "一致する結果がありません",
    "清空搜索或换一个关键词再试。": "検索をクリアするか別のキーワードを試してください。",
    "还没有 Provider": "Provider がありません",
    "先接入上游服务商，再为模型配置路由。": "上流 Provider を接続してからモデルルートを設定します。",
    "还没有模型路由": "モデルルートがありません",
    "路由决定模型请求会被转发到哪个 Provider。": "ルートはモデルリクエストの転送先 Provider を決定します。",
    "还没有项目空间": "プロジェクトがありません",
    "项目、Key、额度和成本归属": "プロジェクト、Key、クォータ、コスト帰属",
    "项目是 Key、额度、成员和成本归属的基本单元。": "プロジェクトは Key、クォータ、メンバー、コスト帰属の基本単位です。",
    "还没有 Key": "Key がありません",
    "为项目发放 Key 后，业务应用才能调用网关。": "業務アプリがゲートウェイを呼び出すにはプロジェクト Key が必要です。",
    "还没有用户": "ユーザーがいません",
    "可以手动创建，也可以从 CSV 批量导入。": "手動作成または CSV 一括インポートができます。",
    "还没有身份源": "ID ソースがありません",
    "接入企业 OAuth/OIDC 后，用户可以使用 SSO 登录。": "企業 OAuth/OIDC を接続すると SSO ログインできます。",
    "当前视图还没有可展示的记录。": "このビューに表示できるレコードはまだありません。",
    "报表时间范围": "レポート期間",
    "7 天": "7 日",
    "30 天": "30 日",
    "本月": "今月",
    "近 7 天": "直近 7 日",
    "近 30 天": "直近 30 日",
    "在线": "オンライン",
    "全部健康 · 延迟 312ms": "すべて正常 · レイテンシ 312ms",
    "API Key": "API Key",
    "已发放": "発行済み",
    "成本与用量趋势": "コストと利用量の推移",
    "趋势指标": "推移指標",
    "Provider 成本占比": "Provider コスト比率",
    "暂无 Provider 成本数据": "Provider コストデータがありません",
    "Top 模型 · 调用量": "上位モデル · 呼び出し数",
    "暂无模型调用数据": "モデル呼び出しデータがありません",
    "OpenAI 兼容协议": "OpenAI 互換プロトコル",
    "接口基础信息": "API 基本情報",
    "面向业务开发者的模型 API 调用说明。业务侧只使用 ": "業務開発者向けのモデル API 説明です。業務側は ",
    " 和项目 API Key；": " とプロジェクト API Key のみを使用し、",
    " 仅用于控制台管理。": " はコンソール管理専用です。",
    "当前权限下还没有可展示模型，请先确认模型目录和路由策略。": "現在の権限では表示可能なモデルがありません。モデルカタログとルーティングポリシーを確認してください。",
    "当前后台还没有启用可用模型路由，请让管理员在路由策略里启用模型。": "利用可能なモデルルートがまだ有効化されていません。管理者にルーティングポリシーでモデルを有効化してもらってください。",
    "示例模型": "サンプルモデル",
    "当前配置": "現在の設定",
    "启用路由": "有効ルート",
    "API 导航": "API ナビゲーション",
    "按接口类型查看详细说明": "API 種別ごとに詳細を表示",
    "开始": "開始",
    "模型 API": "モデル API",
    "管理 API": "管理 API",
    "参考": "リファレンス",
    "快速接入": "クイックスタート",
    "用一个项目 API Key 调用 TokenHub 的 OpenAI 兼容模型接口。": "プロジェクト API Key で TokenHub の OpenAI 互換モデル API を呼び出します。",
    "鉴权方式": "認証方式",
    "业务应用只需要调用 /v1/*，不需要也不应该访问 /api/admin/*。": "業務アプリは /v1/* だけを呼び出し、/api/admin/* にはアクセスしません。",
    "每个 API Key 都会受到项目状态、模型白名单、额度、并发和 Provider 路由策略约束。": "各 API Key はプロジェクト状態、モデル許可リスト、クォータ、同時実行数、Provider ルートに制約されます。",
    "排查失败请求时，优先复制响应里的 request_id 到请求日志查看完整链路。": "失敗調査では、レスポンスの request_id をコピーしてリクエストログで全経路を確認します。",
    "查询可用模型": "利用可能モデルを取得",
    "发起一次对话": "チャットを開始",
    "认证与权限": "認証と権限",
    "业务 API 使用项目下发的 API Key；控制台登录令牌不能替代业务 Key。": "業務 API はプロジェクト発行の API Key を使います。コンソールログイントークンでは代替できません。",
    "格式": "形式",
    "当前 Key 数": "現在の Key 数",
    "项目 API Key，格式为 Bearer sk_xxx": "プロジェクト API Key。形式は Bearer sk_xxx",
    "POST 必填": "POST で必須",
    "JSON 请求使用 application/json": "JSON リクエストは application/json を使用",
    "模型接口必填": "モデル API で必須",
    "统一模型名，需在 Key 白名单和路由策略中可用": "Key 許可リストとルートで利用可能な統一モデル名",
    "401 通常表示 Key 缺失、格式错误、已停用或已过期。": "401 は通常 Key の欠落、形式不正、無効化、期限切れを示します。",
    "403 通常表示项目状态、模型白名单或权限范围不允许当前调用。": "403 は通常プロジェクト状態、モデル許可リスト、権限範囲が呼び出しを許可していないことを示します。",
    "模型列表": "モデル一覧",
    "按当前 API Key 的权限返回可用统一模型。": "現在の API Key 権限で利用可能な統一モデルを返します。",
    "请求示例": "リクエスト例",
    "字段": "フィールド",
    "必填": "必須",
    "统一模型名称，用于后续调用的 model 字段": "以降の呼び出しで model フィールドに使う統一モデル名",
    "OpenAI 兼容对象类型，通常为 model": "OpenAI 互換オブジェクト型。通常は model",
    "模型归属或 Provider 标识": "モデル所有者または Provider 識別子",
    "对话补全": "チャット補完",
    "兼容 OpenAI Chat Completions，用于普通对话、工具调用和流式输出。": "OpenAI Chat Completions 互換。通常会話、ツール呼び出し、ストリーミングに利用します。",
    "统一模型名，例如 ": "統一モデル名。例: ",
    "system/user/assistant 消息数组": "system/user/assistant メッセージ配列",
    "采样温度，默认由上游模型决定": "サンプリング温度。デフォルトは上流モデルに依存",
    "true 时返回 SSE 流式响应": "true の場合 SSE ストリーミングレスポンスを返します",
    "兼容新版 Responses 风格调用，适合统一文本输入和多模态能力扩展。": "新しい Responses 形式に互換し、統一テキスト入力とマルチモーダル拡張に適します。",
    "统一模型名": "統一モデル名",
    "用户输入内容": "ユーザー入力",
    "是否流式返回": "ストリーミングするか",
    "文本向量": "テキスト埋め込み",
    "转发文本向量生成请求，并记录 Token 与成本归因。": "テキスト埋め込み生成リクエストを転送し、Token とコスト帰属を記録します。",
    "向量统一模型名": "統一埋め込みモデル名",
    "需要向量化的文本": "埋め込み対象テキスト",
    "可选 float/base64，取决于上游模型支持": "上流モデル対応に応じて float/base64 を選択",
    "当前可调用模型": "現在呼び出し可能なモデル",
    "根据模型目录和路由策略汇总当前控制台可见模型。": "モデルカタログとルートに基づき、このコンソールで見えるモデルを集計します。",
    "控制台登录": "コンソールログイン",
    "控制台用户登录接口，不等同于模型 API Key。": "コンソールユーザーログイン API。モデル API Key とは異なります。",
    "用户名或邮箱": "ユーザー名またはメール",
    "控制台密码": "コンソールパスワード",
    "管理 API 仅用于控制台和受信任后台程序，不应该暴露给业务应用前端。": "管理 API はコンソールと信頼されたバックエンド専用で、業務フロントエンドに公開しないでください。",
    "Provider 配置": "Provider 設定",
    "管理上游 Provider、Base URL、凭证和健康状态。": "上流 Provider、Base URL、認証情報、ヘルス状態を管理します。",
    "读取 Provider 列表": "Provider 一覧を取得",
    "新增 Provider 配置": "Provider 設定を作成",
    "更新 Provider 凭证、状态和元信息": "Provider 認証情報、状態、メタ情報を更新",
    "维护统一模型到 Provider 资源的优先级、权重和状态。": "統一モデルから Provider リソースへの優先度、重み、状態を管理します。",
    "当前路由": "現在のルート",
    "按权限查看模型调用日志、命中路由、耗时、Token 和错误信息。": "権限に応じてモデル呼び出しログ、命中ルート、所要時間、Token、エラーを確認します。",
    "日志样本": "ログサンプル",
    "用途": "用途",
    "排查 request_id 和上游响应": "request_id と上流レスポンスの調査",
    "常见错误": "よくあるエラー",
    "按状态码定位 API Key、模型白名单、路由和额度问题。": "ステータスコードから API Key、モデル許可リスト、ルート、クォータ問題を特定します。",
    "状态码": "ステータス",
    "错误码": "エラーコード",
    "处理方式": "対応方法",
    "检查 Authorization 是否使用 TokenHub API Key": "Authorization が TokenHub API Key を使っているか確認",
    "检查 Key 的模型白名单和项目状态": "Key のモデル許可リストとプロジェクト状態を確認",
    "为统一模型配置启用路由": "統一モデルに有効ルートを設定",
    "检查项目额度、并发和 Provider 资源限制": "プロジェクトクォータ、同時実行数、Provider リソース制限を確認",
    "在请求日志里查看上游响应和 request_id": "リクエストログで上流レスポンスと request_id を確認",
    "SDK 示例": "SDK 例",
    "使用 OpenAI 兼容 SDK 接入 TokenHub。": "OpenAI 互換 SDK で TokenHub に接続します。",
    "调用链路": "呼び出し経路",
    "从鉴权到 Provider 路由的完整治理链路。": "認証から Provider ルーティングまでの完全なガバナンス経路です。",
    "阶段": "段階",
    "当前数据": "現在データ",
    "认证": "認証",
    "权限": "権限",
    "模型白名单 + 项目状态": "モデル許可リスト + プロジェクト状態",
    "上游渠道实例、凭证和健康状态": "上流チャネルインスタンス、認証情報、ヘルス",
    "对外模型到 Provider 的优先级/权重映射": "公開モデルから Provider への優先度/重みマッピング",
    "额度、审计、成本统计": "クォータ、監査、コスト統計",
    "请求参数": "リクエストパラメータ",
    "明细": "詳細",
    "示例": "例",
    "复制": "コピー",
    "复制代码": "コードをコピー",
    "企业 AI 用量看板": "企業 AI 利用ダッシュボード",
    "面向管理层的部门、个人与 Token 消耗对比": "経営層向けの部門、個人、Token 消費比較",
    "按部门": "部門別",
    "Token 口径": "Token 基準",
    "Token 消耗": "Token 消費",
    "总 Token 消耗": "総 Token 消費",
    "覆盖部门": "対象部門",
    "活跃成员": "アクティブメンバー",
    "统计时间": "集計時刻",
    "最高": "最高",
    "暂无部门归因": "部門帰属がありません",
    "次请求": "件のリクエスト",
    "部门 Token 消耗对比": "部門別 Token 消費比較",
    "输入 Token 与输出 Token 分段展示，按总量排序": "入力 Token と出力 Token を区分表示し、総量順に並べます",
    "部门排行": "部門ランキング",
    "个人排行": "個人ランキング",
    "公司内部成员 Token 消耗 Top 20": "社内メンバー Token 消費 Top 20",
    "按 Token 降序": "Token 降順",
    "可用于复盘配额": "クォータ見直しに利用可能",
    "暂无部门 Token 数据": "部門 Token データがありません",
    "暂无部门排行数据": "部門ランキングデータがありません",
    "暂无个人排行数据": "個人ランキングデータがありません",
    "排名": "順位",
    "部门": "部門",
    "占比": "比率",
    "未归属部门": "未割当部門",
    "团队用户": "チームユーザー",
    "关闭成员列表": "メンバー一覧を閉じる",
    "姓名": "氏名",
    "成员数": "メンバー数",
    "项目额度": "プロジェクトクォータ",
    "关闭额度配置": "クォータ設定を閉じる",
    "已配置项目专属额度": "プロジェクト専用クォータ設定済み",
    "未配置项目专属额度": "プロジェクト専用クォータ未設定",
    "留空或填 0 表示该项不限额；Key 自身额度仍会叠加生效。": "空欄または 0 は無制限です。Key 自身のクォータも重ねて有効です。",
    "已有额度提升申请待审批": "クォータ増額申請が承認待ちです",
    "最近触发了项目额度限制": "最近プロジェクトクォータ制限に達しました",
    "可在审批记录中处理。": "承認記録で処理できます。",
    "次额度不足，请填写希望提升后的目标额度再提交审批。": "回クォータ不足です。増額後の目標値を入力して承認申請してください。",
    "待审批": "承認待ち",
    "需提升": "増額が必要",
    "日请求": "日次リクエスト",
    "月请求": "月次リクエスト",
    "日 Token": "日次 Token",
    "月 Token": "月次 Token",
    "日成本": "日次コスト",
    "月成本": "月次コスト",
    "最大并发": "最大同時実行数",
    "提升额度申请": "クォータ増額申請",
    "提交项目额度提升审批": "プロジェクトクォータ増額承認を申請",
    "保存额度": "クォータを保存",
    "按需导出": "オンデマンド出力",
    "最近导出": "最近の出力",
    "自动导出配置": "自動出力設定",
    "新增配置": "設定を作成",
    "数据集": "データセット",
    "文件": "ファイル",
    "账期": "期間",
    "导出 ": "エクスポート ",
    "模型分类": "モデル分類",
    "Key 归属逻辑": "Key 帰属ロジック",
    "内部应用配置项目下发放的 Key；额度、模型白名单、用量和成本都会归属到该项目。": "社内アプリのプロジェクト配下で Key を発行し、クォータ、モデル許可リスト、利用量、コストはそのプロジェクトに帰属します。",
    "个项目": "件のプロジェクト",
    "个 Key": "件の Key",
    "批量导入": "一括インポート",
    "导入用户": "ユーザーをインポート",
    "CSV 内容": "CSV 内容",
    "按 username 或 email 匹配已有用户；匹配到则更新，未匹配则创建。": "username または email で既存ユーザーを照合し、一致すれば更新、なければ作成します。",
    "字段顺序": "フィールド順",
    "role 可填 admin、team_leader、user；status 可填 active 或 disabled。": "role は admin、team_leader、user、status は active または disabled を指定できます。",
    "导入中": "インポート中",
    "开始导入": "インポート開始",
    "模型配置": "モデル設定",
    "暂无聊天模型": "チャットモデルがありません",
    "条路由": "件のルート",
    "未配置路由": "ルート未設定",
    "响应格式": "レスポンス形式",
    "系统提示": "システムプロンプト",
    "函数": "関数",
    "函数调用配置待接入": "関数呼び出し設定は未接続です",
    "添加函数": "関数を追加",
    "模型演练对话": "モデルプレイグラウンド会話",
    "复制模型名": "モデル名をコピー",
    "选择模型": "モデルを選択",
    "模型详情": "モデル詳細",
    "查看代码": "コード表示",
    "清空历史": "履歴をクリア",
    "默认资源": "デフォルトリソース",
    "次尝试": "回試行",
    "当前模型": "現在のモデル",
    "试用": "試用",
    "当前还没有配置模型路由。": "モデルルートはまだ設定されていません。",
    "体验一下，看看模型在 TokenHub 网关上的表现": "TokenHub ゲートウェイ上でのモデル動作を試せます",
    "说点什么...": "メッセージを入力...",
    "文件上传待接入": "ファイルアップロードは未接続です",
    "上传文件": "ファイルをアップロード",
    "发送": "送信",
    "API 使用": "API 利用",
    "使用以下代码示例集成 TokenHub 模型接口": "以下のコード例で TokenHub モデル API を統合します",
    "API 调用语言": "API 呼び出し言語",
    "未选择模型": "モデル未選択",
    "模型没有返回可展示内容。": "モデルは表示可能な内容を返しませんでした。",
    "演练请求失败": "プレイグラウンドリクエストに失敗しました",
    "Provider 模板加载失败": "Provider テンプレートの読み込みに失敗しました",
    "Provider 已": "Provider は",
    "更新": "更新済み",
    "创建": "作成済み",
    "模型类型": "モデルタイプ",
    "类": "カテゴリ",
    "渠道商": "Provider",
    "个标准模型": "件の標準モデル",
    "自定义渠道商": "カスタム Provider",
    "搜索渠道商、ID、类型": "Provider、ID、タイプを検索",
    "没有匹配的渠道商": "一致する Provider がありません",
    "使用自定义渠道商": "カスタム Provider を使用",
    "请选择渠道商": "Provider を選択",
    "例如 prv_company_proxy": "例: prv_company_proxy",
    "留空自动生成": "空欄で自動生成",
    "渠道名称": "チャネル名",
    "渠道商类型": "Provider タイプ",
    "编辑时留空则不修改已保存密钥。": "編集時に空欄の場合、保存済みシークレットは変更しません。",
    "留空表示不修改现有 Key；填写新值才会覆盖。": "空欄なら既存 Key を変更しません。新しい値を入力した場合のみ上書きします。",
    "开启后会为下方勾选模型补齐缺失线路，不覆盖已有策略。": "有効にすると、選択モデルの不足ルートを追加し、既存ポリシーは上書きしません。",
    "保存渠道时会自动创建下方勾选模型的默认路由。": "Provider 保存時に選択モデルのデフォルトルートを自動作成します。",
    "上游模型映射": "上流モデルマッピング",
    "个可映射模型": "件のマッピング可能モデル",
    "加载中": "読み込み中",
    "正在加载模型列表...": "モデル一覧を読み込み中...",
    "该渠道商暂无可匹配当前标准模型目录的上游模型": "この Provider には現在の標準モデルカタログに一致する上流モデルがありません",
    "已有路由": "既存ルート",
    "已关闭自动路由：保存后只创建 Provider，不生成路由策略。": "自動ルートはオフです。保存後は Provider のみ作成し、ルートは生成しません。",
    "当前没有勾选模型，保存后不会生成路由策略。": "モデルが選択されていないため、保存後にルートは生成されません。",
    "保存后会为": "保存後、",
    "个已选": "件の選択済み",
    "模型创建缺失的默认路由。": "モデルに不足しているデフォルトルートを作成します。",
    "请求详情加载失败": "リクエスト詳細の読み込みに失敗しました",
    "全部 ": "すべて ",
    "成功 ": "成功 ",
    "失败 ": "失敗 ",
    "没有匹配的请求记录": "一致するリクエスト記録がありません",
    "暂无大模型请求记录": "モデルリクエスト記録がありません",
    "正在加载请求详情...": "リクエスト詳細を読み込み中...",
    "请选择一条请求": "リクエストを選択してください",
    "复制请求 ID": "リクエスト ID をコピー",
    "Token 与成本": "Token とコスト",
    "条记录": "件の記録",
    "暂无记录": "記録なし",
    "路由尝试": "ルート試行",
    "次，含 fallback": "回、fallback 含む",
    "没有记录到路由尝试": "ルート試行は記録されていません",
    "这条历史记录没有保存 request / response 快照": "この履歴には request / response スナップショットが保存されていません",
    "未记录请求内容": "リクエスト内容未記録",
    "未记录响应内容": "レスポンス内容未記録",
    "已截断": "切り捨て済み",
    "上游模型 ": "上流モデル ",
    "资源 ": "リソース ",
    "路由 ": "ルート ",
    "资源": "リソース",
    "大小": "サイズ",
    "触发方式": "トリガー方式",
    "创建时间": "作成日時",
    "最近恢复": "最近の復元",
    "校验": "チェックサム",
    "保留天数": "保持日数",
    "0 表示不过期": "0 は期限なし",
    "下载": "ダウンロード",
    "恢复": "復元",
    "备份文件已开始下载": "バックアップのダウンロードを開始しました",
    "已恢复备份": "バックアップを復元しました",
    "后台用户列表": "管理ユーザー一覧",
    "新增用户": "ユーザーを作成",
    "编辑时留空则不修改": "編集時に空欄の場合は変更しません",
    "从已有系统导出的 CSV 批量导入或更新用户": "既存システムから出力した CSV でユーザーを一括作成または更新",
    "指标": "指標",
    "阈值": "しきい値",
    "级别": "レベル",
    "对象范围": "対象範囲",
    "规则列表": "ルール一覧",
    "触发事件": "発生イベント",
    "来源": "ソース",
    "系统默认": "システム既定",
    "事件": "イベント",
    "告警 ID": "アラート ID",
    "渠道": "チャネル",
    "HTTP": "HTTP",
    "失败原因": "失敗理由",
    "审批申请列表": "承認申請一覧",
    "触发条件": "トリガー条件",
    "申请人": "申請者",
    "处理人": "処理者",
    "内容": "内容",
    "批准": "承認",
    "批准并执行该申请": "この申請を承認して実行",
    "驳回该申请": "この申請を却下",
    "成本中心编码": "コストセンターコード",
    "编码": "コード",
    "月预算 USD": "月次予算 USD",
    "月预算": "月次予算",
    "部门分摊": "部門配賦",
    "分摊成本 USD": "配賦コスト USD",
    "请求数": "リクエスト数",
    "分摊规则": "配賦ルール",
    "分摊成本": "配賦コスト",
    "审批流": "承認フロー",
    "审批角色": "承認ロール",
    "金额阈值 USD": "金額しきい値 USD",
    "SLA 小时": "SLA 時間",
    "金额阈值": "金額しきい値",
    "频率": "頻度",
    "接收人": "受信者",
    "导出 CSV 报表": "CSV レポートを出力",
    "内部账单": "内部請求",
    "金额 USD": "金額 USD",
    "发票备注": "請求メモ",
    "确认人": "確認者",
    "确认时间": "確認日時",
    "驳回原因": "却下理由",
    "金额": "金額",
    "生成本月": "今月分を生成",
    "按当前账期生成分摊和内部账单": "現在期間の配賦と内部請求を生成",
    "输入账期 YYYY-MM，留空则导出全部": "期間 YYYY-MM を入力。空欄ならすべて出力",
    "输入账期 YYYY-MM，留空则生成本月": "期間 YYYY-MM を入力。空欄なら今月を生成",
    "请输入驳回原因": "却下理由を入力してください",
    "已生成分摊和内部账单": "配賦と内部請求を生成しました",
    "已提交审批：": "承認申請済み: ",
    "请选择项目空间后再发放 API Key": "API Key 発行前にプロジェクトを選択してください",
    "发放 Key 失败": "Key 発行に失敗しました",
    "当前账号没有可发放 Key 的项目权限，请联系项目负责人或管理员把你加入项目。": "このアカウントには Key を発行できるプロジェクト権限がありません。プロジェクト責任者または管理者に追加を依頼してください。",
    "创建内部调用 Key": "内部呼び出し Key を作成",
    "创建 Key 步骤": "Key 作成ステップ",
    "选择项目": "プロジェクト選択",
    "填写用途": "用途入力",
    "模型范围": "モデル範囲",
    "安全护栏": "ガードレール",
    "确认发放": "確認",
    "选择 Key 归属项目": "Key の所属プロジェクトを選択",
    "Key 必须挂在项目空间下，用量和成本会归集到这个项目。": "Key はプロジェクトに紐づき、利用量とコストはそのプロジェクトに集計されます。",
    "说明用途和环境": "用途と環境を記入",
    "名称建议能看出调用方、环境和用途，后续审计会更容易定位。": "呼び出し元、環境、用途が分かる名前にすると、後の監査で特定しやすくなります。",
    "设置模型范围": "モデル範囲を設定",
    "全部可路由模型": "すべてのルーティング可能モデル",
    "由平台路由策略决定最终可调用范围": "最終的な呼び出し範囲はプラットフォームのルーティングポリシーで決まります。",
    "指定模型白名单": "モデル許可リストを指定",
    "只允许这个 Key 调用已勾选的模型": "この Key は選択したモデルのみ呼び出せます。",
    "设置安全护栏": "ガードレールを設定",
    "确认后生成 Key": "確認して Key を生成",
    "上一步": "戻る",
    "下一步": "次へ",
    "发放中": "発行中",
    "生成 Key": "Key を生成",
    "可调用": "呼び出し可",
    "模型未启用": "モデル無効",
    "按权限可见": "権限により表示",
    "路由未启用": "ルート無効",
    "线路需检查": "ルート確認が必要",
    "选择身份源模板": "ID ソーステンプレートを選択",
    "选择后会自动填充协议、登录图标、Scope、Claim 和常见端点。": "選択するとプロトコル、ログインアイコン、Scope、Claim、一般的なエンドポイントを自動入力します。",
    "登录按钮": "ログインボタン",
    "默认 Scope": "デフォルト Scope",
    "必填项": "必須項目",
    "Issuer、Client ID、Client Secret、Callback URL": "Issuer、Client ID、Client Secret、Callback URL",
    "适合标准 OIDC 服务，填写 Issuer 后一般可自动发现端点。": "標準 OIDC サービス向けです。Issuer 入力後、多くの場合エンドポイントを自動検出できます。",
    "适合非标准 OAuth2 服务，需要确认授权、Token 和用户信息端点。": "非標準 OAuth2 サービス向けです。認可、Token、ユーザー情報エンドポイントを確認してください。",
    "请先填写至少一项希望提升后的目标额度": "増額後の目標クォータを少なくとも 1 つ入力してください",
    "不限额": "無制限",
    "项目空间内配置的专属模型调用额度": "プロジェクト内で設定する専用モデル呼び出しクォータ",
    "该记录": "このレコード",
    "通知模式": "通知モード",
    "目标对象": "対象",
    "脱敏 Prompt": "Prompt をマスク",
    "错误透传": "エラー透過",
    "每行一个 CIDR 或 IP，留空表示不配置白名单。": "1 行に CIDR または IP を 1 つ入力。空欄なら許可リストなし。",
    "开启后策略要求请求与响应审计避免直接展示完整 Prompt。": "有効にすると、リクエスト/レスポンス監査で完全な Prompt を直接表示しない方針になります。",
    "通用 Webhook 告警通知": "汎用 Webhook アラート通知",
    "飞书机器人告警通知": "Feishu ボットアラート通知",
    "钉钉机器人告警通知": "DingTalk ボットアラート通知",
    "企业微信机器人告警通知": "WeCom ボットアラート通知",
    "Slack Incoming Webhook 告警通知": "Slack Incoming Webhook アラート通知",
    "Discord Webhook 告警通知": "Discord Webhook アラート通知",
    "Telegram Bot 告警通知": "Telegram Bot アラート通知",
    "WhatsApp Cloud API 告警通知": "WhatsApp Cloud API アラート通知",
    "SMTP 邮件告警通知": "SMTP メールアラート通知",
    "告警通知渠道": "アラート通知チャネル",
    "SMTP 已配置": "SMTP 設定済み",
    "SMTP 未配置": "SMTP 未設定",
    "通知渠道列表": "通知チャネル一覧",
    "新增通知渠道": "通知チャネルを作成",
    "渠道类型": "チャネルタイプ",
    "签名密钥": "署名シークレット",
    "可选预留。当前按普通机器人 Webhook 发送，留空不影响通知。": "任意の予約項目です。現在は通常のボット Webhook で送信するため、空欄でも問題ありません。",
    "SMTP 端口": "SMTP ポート",
    "SMTP 用户名": "SMTP ユーザー名",
    "SMTP 密码": "SMTP パスワード",
    "收件人": "受信者",
    "WhatsApp 收件人": "WhatsApp 受信者",
    "多个邮箱用逗号分隔。": "複数メールはカンマで区切ってください。",
    "Bot Token 已配置": "Bot Token 設定済み",
    "Bot Token 未配置": "Bot Token 未設定",
    "Access Token 已配置": "Access Token 設定済み",
    "Access Token 未配置": "Access Token 未設定",
    "按 Webhook、Slack、Discord、Telegram、WhatsApp、飞书、钉钉、企业微信和邮件快速配置告警通知目标。": "Webhook、Slack、Discord、Telegram、WhatsApp、Feishu、DingTalk、WeCom、メールのアラート通知先をすばやく設定します。",
    "地址/目标": "アドレス / 対象",
    "凭证": "認証情報",
    "告警类型": "アラートタイプ",
    "监控名称": "監視名",
    "监控类型": "監視タイプ",
    "检查间隔": "チェック間隔",
    "最近检查": "最終チェック",
    "环境": "環境",
    "区域": "リージョン",
    "资源类型": "リソースタイプ",
    "资源分组": "リソースグループ",
    "失败次数": "失敗回数",
    "冷却至": "クールダウン終了",
    "速率限制 RPM": "レート制限 RPM",
    "Token 限制 TPM": "Token 制限 TPM",
    "质量分": "品質スコア",
    "成本分": "コストスコア",
    "粘性": "スティッキー",
    "不限": "無制限",
    "系统提示词": "システムプロンプト",
    "支持参数": "対応パラメータ",
    "输入模态": "入力モダリティ",
    "输出模态": "出力モダリティ",
    "计费输入": "入力単価",
    "计费输出": "出力単価",
    "Embedding 计费": "Embedding 単価",
    "模型家族": "モデルファミリー",
    "上下文窗口": "コンテキストウィンドウ",
    "公开 Base URL": "公開 Base URL",
    "默认超时": "デフォルトタイムアウト",
    "审计保留": "監査保持",
    "API Key 前缀": "API Key プレフィックス",
    "API Key 随机长度": "API Key ランダム長",
    "新建和轮换 Key 时使用；建议以 _ 结尾，例如 sk_。": "Key の作成とローテーション時に使用します。sk_ のように _ で終えることを推奨します。",
    "前缀后面的随机字符数，系统会限制在 24-128 之间。": "プレフィックス後のランダム文字数です。システムは 24-128 に制限します。",
    "用户名字段": "ユーザー名 Claim",
    "邮箱字段": "メール Claim",
    "团队字段": "チーム Claim",
    "新增身份源": "ID ソースを作成",
    "新增可选角色": "割当可能ロールを作成",
    "配置用户管理新增/编辑时可选择的后台角色。权限边界由系统内置角色模型控制。": "ユーザー作成/編集時に選択できる管理ロールを設定します。権限境界は組み込みロールモデルで制御されます。",
    "Embedding 价 USD/1M": "Embedding 単価 USD/1M",
    "IP 白名单": "IP 許可リスト",
    "IP 白名单，逗号分隔": "IP 許可リスト、カンマ区切り",
    "Key 名称": "Key 名",
    "Key 必须挂在已有项目下，用于该项目的内部应用调用网关。": "Key は既存プロジェクトに紐づけ、そのプロジェクトの社内アプリがゲートウェイを呼び出すために使います。",
    "SQLite 备份": "SQLite バックアップ",
    "上游模型/部署名": "上流モデル / デプロイ名",
    "下载 SQLite 备份文件": "SQLite バックアップファイルをダウンロード",
    "为该 Provider 新增模型路由": "この Provider にモデルルートを作成",
    "为该对外模型新增 Provider 线路": "この公開モデルに Provider ルートを作成",
    "为该项目创建内部 API Key": "このプロジェクトに内部 API Key を作成",
    "从用户管理中选择团队负责人，用于审批和审计归属。": "ユーザー管理からチーム責任者を選び、承認と監査帰属に利用します。",
    "作用域": "スコープ",
    "作用域 ID": "スコープ ID",
    "保存项目额度": "プロジェクトクォータを保存",
    "内部 Key 列表": "内部 Key 一覧",
    "创建、下载和恢复 TokenHub SQLite 数据库快照。": "TokenHub SQLite データベーススナップショットを作成、ダウンロード、復元します。",
    "创建备份": "バックアップを作成",
    "发件人": "送信者",
    "发放 Key": "Key 発行",
    "可用供应商": "利用可能 Provider",
    "后台操作、变更对象、操作人和时间": "管理操作、変更対象、実行者、時刻",
    "告警事件列表": "アラートイベント一覧",
    "告警通知的渠道、目标和发送结果": "アラート通知のチャネル、対象、送信結果",
    "团队 ID": "チーム ID",
    "团队名称": "チーム名",
    "处理 Key 发放、额度提升和模型开通审批。": "Key 発行、クォータ増額、モデル開通の承認を処理します。",
    "多个收件人用英文逗号分隔。": "複数の受信者は英字カンマで区切ります。",
    "对外模型": "公開モデル",
    "将数据库恢复到该备份": "データベースをこのバックアップへ復元",
    "并发": "同時実行",
    "归属项目": "所属プロジェクト",
    "成本中心、负责人和部门归属配置": "コストセンター、責任者、部門帰属設定",
    "成本优先模式会优先选择该评分更高的线路，分数越高代表越省。": "コスト優先モードはスコアが高いルートを優先します。高いほど低コストです。",
    "成本评分 1-100": "コストスコア 1-100",
    "批量创建时留空，会为每个统一模型使用同名上游模型。": "一括作成時に空欄の場合、各統一モデルと同名の上流モデルを使います。",
    "按模型、项目和日期归集 Token 与成本": "モデル、プロジェクト、日付別に Token とコストを集計",
    "按项目发放内部 API Key，限制模型白名单、额度、并发和有效期。": "プロジェクト別に内部 API Key を発行し、モデル許可リスト、クォータ、同時実行、有効期限を制限します。",
    "支持参数，逗号分隔": "対応パラメータ、カンマ区切り",
    "数字越小越先调用；新增时留空会自动排在该统一模型已有 Provider 后面。": "数値が小さいほど先に呼び出します。作成時に空欄の場合、既存 Provider の後ろに自動配置されます。",
    "新增路由时可多选模型；编辑已有路由时仍按单条规则调整。": "ルート作成時は複数モデルを選択できます。既存ルート編集時は単一ルールを調整します。",
    "新增项目": "プロジェクトを作成",
    "日成本 USD": "日次コスト USD",
    "最近命中": "最終ヒット",
    "最近检测": "最終チェック",
    "最近消息": "最新メッセージ",
    "最近状态": "最新状態",
    "月成本 USD": "月次コスト USD",
    "查看告警通知的发送状态、目标和失败原因。": "アラート通知の送信状態、対象、失敗理由を確認します。",
    "检测 Provider 可用性": "Provider 可用性をチェック",
    "模型白名单，逗号分隔": "モデル許可リスト、カンマ区切り",
    "渠道配置": "チャネル設定",
    "生成新 Key，并立即吊销旧 Key": "新しい Key を生成し、古い Key をすぐ失効",
    "用途/环境": "用途 / 環境",
    "留空则沿用统一模型名": "空欄の場合は統一モデル名を使用",
    "留空自动追加": "空欄なら自動追加",
    "留空表示不限制 Key 级模型白名单；实际可调用模型仍受模型目录和路由策略约束。": "空欄なら Key レベルのモデル許可リストは無制限です。実際の呼び出し可能モデルはカタログとルートに制約されます。",
    "留空表示不限来源 IP。": "空欄なら送信元 IP を制限しません。",
    "目录计价": "カタログ単価",
    "目标": "対象",
    "目标类型": "対象タイプ",
    "确认该内部账单": "この内部請求を確認",
    "立即执行该健康检测": "このヘルスチェックを今すぐ実行",
    "立即检测": "今すぐチェック",
    "策略": "戦略",
    "管理 TokenHub 后台登录账号、角色权限、归属团队和账号状态。": "TokenHub 管理ログインアカウント、ロール権限、所属チーム、アカウント状態を管理します。",
    "粘性会话": "スティッキーセッション",
    "系列": "ファミリー",
    "编辑时留空表示不修改。": "編集時に空欄の場合は変更しません。",
    "编辑时留空表示不修改现有 Key；只有填写新值才会覆盖。": "編集時に空欄の場合、既存 Key は変更しません。新しい値を入力した場合のみ上書きします。",
    "能力标签，逗号分隔": "機能タグ、カンマ区切り",
    "评分": "スコア",
    "请求 ID、模型、状态码、Provider 路由和延迟": "リクエスト ID、モデル、ステータスコード、Provider ルート、レイテンシ",
    "调度策略": "スケジューリング戦略",
    "负责人用户 ID": "責任者ユーザー ID",
    "质量优先模式会优先选择该评分更高的线路。": "品質優先モードはスコアが高いルートを優先します。",
    "质量评分 1-100": "品質スコア 1-100",
    "费用归集口径，可与团队不同；用于成本归集和用量统计。": "コスト帰属単位です。チームと異なる場合があり、コスト集計と利用統計に使います。",
    "模型用量": "モデル利用量",
    "成员用量": "メンバー利用量",
    "项目归因": "プロジェクト帰属",
    "成员成本": "メンバーコスト",
    "Provider 成本": "Provider コスト",
    "Provider 明细成本": "Provider 詳細コスト",
    "命中 Provider": "命中 Provider",
    "资源实例": "リソースインスタンス",
    "资源实例 ID": "リソースインスタンス ID",
    "路由数": "ルート数",
    "轮换": "ローテーション",
    "输入价 USD/1M": "入力単価 USD/1M",
    "输出价 USD/1M": "出力単価 USD/1M",
    "运行时触发的额度、成本和 Provider 健康事件。": "実行時に発生したクォータ、コスト、Provider ヘルスイベントです。",
    "通知发送记录": "通知送信記録",
    "通过默认通知渠道发送该告警": "既定の通知チャネルでこのアラートを送信",
    "配置通知渠道": "通知チャネルを設定",
    "间隔秒数": "間隔秒数",
    "项目 ID": "プロジェクト ID",
    "项目列表": "プロジェクト一覧",
    "项目名称": "プロジェクト名",
    "项目是企业内部 AI 使用、Key、额度和成本归属的基本单元。": "プロジェクトは社内 AI 利用、Key、クォータ、コスト帰属の基本単位です。",
    "额度": "クォータ",
    "额度提升、Key 发放和模型开通审批记录": "クォータ増額、Key 発行、モデル開通の承認記録",
    "驳回该内部账单": "この内部請求を却下",
    "默认检测项": "デフォルトチェック項目",
    "更新路由顺序失败": "ルート順序の更新に失敗しました",
    "点击停用 API Key": "クリックして API Key を無効化",
    "点击启用 API Key": "クリックして API Key を有効化",
  },
};

function readSavedLanguage(): AppLanguage {
  if (typeof window === "undefined") return "en";
  const saved = window.localStorage.getItem(languageStorageKey);
  return saved === "en" || saved === "ja" || saved === "zh-CN" ? saved : "en";
}

function setActiveLanguage(language: AppLanguage) {
  activeLanguage = language;
}

function tx(value: string | undefined | null) {
  if (!value) return "";
  if (activeLanguage === "zh-CN") return value;
  return translations[activeLanguage][value] ?? translateGeneratedText(value, activeLanguage) ?? value;
}

// Localize the browser's native constraint-validation bubble (e.g. the required-field
// message) so it follows the app language instead of the browser locale.
function handleRequiredFieldInvalid(event: {
  currentTarget: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
}) {
  const el = event.currentTarget;
  el.setCustomValidity(el.validity.valueMissing ? tx("请填写此字段") : "");
}

// Clear any custom validity message once the user edits the field, so re-validation works.
function clearCustomValidity(event: {
  currentTarget: HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement;
}) {
  event.currentTarget.setCustomValidity("");
}

function translateGeneratedText(value: string, language: Exclude<AppLanguage, "zh-CN">) {
  const createListMatch = value.match(/^(.+)列表$/);
  if (createListMatch) {
    const base = translations[language][createListMatch[1]] ?? createListMatch[1];
    return language === "ja" ? `${base}一覧` : `${base} List`;
  }
  const createMatch = value.match(/^新增(.+)$/);
  if (createMatch) {
    const base = translations[language][createMatch[1]] ?? createMatch[1];
    return language === "ja" ? `${base}を作成` : `Create ${base}`;
  }
  const approvalMatch = value.match(/^已提交审批：(.+)$/);
  if (approvalMatch) return language === "ja" ? `承認申請済み: ${approvalMatch[1]}` : `Approval submitted: ${approvalMatch[1]}`;
  const exportMatch = value.match(/^(.+) 已导出$/);
  if (exportMatch) return language === "ja" ? `${exportMatch[1]} をエクスポートしました` : `${exportMatch[1]} exported`;
  const sentMatch = value.match(/^(.+) 已发送$/);
  if (sentMatch) return language === "ja" ? `${sentMatch[1]} を送信しました` : `${sentMatch[1]} sent`;
  const approvedMatch = value.match(/^(.+) 已批准$/);
  if (approvedMatch) return language === "ja" ? `${approvedMatch[1]} を承認しました` : `${approvedMatch[1]} approved`;
  const rejectedMatch = value.match(/^(.+) 已驳回$/);
  if (rejectedMatch) return language === "ja" ? `${rejectedMatch[1]} を却下しました` : `${rejectedMatch[1]} rejected`;
  const confirmedMatch = value.match(/^(.+) 已确认$/);
  if (confirmedMatch) return language === "ja" ? `${confirmedMatch[1]} を確認しました` : `${confirmedMatch[1]} confirmed`;
  const quotaSubmittedMatch = value.match(/^(.+) 的额度提升申请已提交$/);
  if (quotaSubmittedMatch) return language === "ja" ? `${quotaSubmittedMatch[1]} のクォータ増額申請を送信しました` : `${quotaSubmittedMatch[1]} quota increase request submitted`;
  const quotaSavedMatch = value.match(/^(.+) 的额度已保存$/);
  if (quotaSavedMatch) return language === "ja" ? `${quotaSavedMatch[1]} のクォータを保存しました` : `${quotaSavedMatch[1]} quota saved`;
  const statusMatch = value.match(/^(.+) 已(启用|禁用|轮换，新 Key 已展示)$/);
  if (statusMatch) {
    const action = statusMatch[2];
    if (language === "ja") {
      const label = action === "启用" ? "有効化しました" : action === "禁用" ? "無効化しました" : "ローテーションしました。新しい Key を表示しています";
      return `${statusMatch[1]} を${label}`;
    }
    const label = action === "启用" ? "enabled" : action === "禁用" ? "disabled" : "rotated; new Key is displayed";
    return `${statusMatch[1]} ${label}`;
  }
  const routeOrderMatch = value.match(/^已更新 (.+) 的 Provider 调用顺序$/);
  if (routeOrderMatch) return language === "ja" ? `${routeOrderMatch[1]} の Provider 呼び出し順を更新しました` : `Updated Provider call order for ${routeOrderMatch[1]}`;
  const enabledRoutesMatch = value.match(/^(\d+)\/(\d+) 启用 · (.+)$/);
  if (enabledRoutesMatch) {
    return language === "ja"
      ? `${enabledRoutesMatch[1]}/${enabledRoutesMatch[2]} 有効 · ${enabledRoutesMatch[3]}`
      : `${enabledRoutesMatch[1]}/${enabledRoutesMatch[2]} enabled · ${enabledRoutesMatch[3]}`;
  }
  return undefined;
}

function displayText(value: string | undefined | null) {
  return tx(value);
}

function isIssuedAPIKey(value: string) {
  return /^[A-Za-z][A-Za-z0-9_-]{0,23}_[A-Za-z0-9_-]{24,}$/.test(value.trim());
}

function translatedCell(value: React.ReactNode) {
  return typeof value === "string" ? tx(value) : value;
}

function languageLocale() {
  if (activeLanguage === "en") return "en-US";
  if (activeLanguage === "ja") return "ja-JP";
  return "zh-CN";
}

function countWithUnit(count: number, zhUnit: string, enUnit: string, jaUnit: string) {
  const formatted = formatNumber(count);
  if (activeLanguage === "en") return `${formatted} ${enUnit}${count === 1 ? "" : "s"}`;
  if (activeLanguage === "ja") return `${formatted} ${jaUnit}`;
  return `${formatted} ${zhUnit}`;
}

function countWithLabel(count: number, label: string) {
  if (activeLanguage === "en") return `${formatNumber(count)} ${tx(label)}`;
  if (activeLanguage === "ja") return `${formatNumber(count)} ${tx(label)}`;
  return `${formatNumber(count)} ${label}`;
}

function selectedModelsText(count: number) {
  if (activeLanguage === "en") return `${count} models selected`;
  if (activeLanguage === "ja") return `${count} 件のモデルを選択済み`;
  return `已选择 ${count} 个模型`;
}

function defaultPlaygroundSystemPrompt() {
  return tx("做一个乐于助人的助手");
}

function isDefaultPlaygroundSystemPrompt(value: string) {
  return [
    "做一个乐于助人的助手",
    translations.en["做一个乐于助人的助手"],
    translations.ja["做一个乐于助人的助手"],
  ].includes(value);
}

function importUsersDoneMessage(created: number, updated: number, skipped: number) {
  if (activeLanguage === "en") {
    return `User import complete: ${created} created, ${updated} updated${skipped > 0 ? `, ${skipped} skipped` : ""}`;
  }
  if (activeLanguage === "ja") {
    return `ユーザーインポート完了: 作成 ${created}、更新 ${updated}${skipped > 0 ? `、スキップ ${skipped}` : ""}`;
  }
  return `用户导入完成：新增 ${created}，更新 ${updated}${skipped > 0 ? `，跳过 ${skipped}` : ""}`;
}

function importUsersSkippedMessage(skipped: number, errors: string) {
  if (activeLanguage === "en") return `${skipped} rows were not imported: ${errors}`;
  if (activeLanguage === "ja") return `${skipped} 件はインポートされませんでした: ${errors}`;
  return `有 ${skipped} 条未导入：${errors}`;
}

function deleteConfirmMessage(name: string) {
  if (activeLanguage === "en") return `After deleting "${name}", the current in-memory data will be removed immediately.`;
  if (activeLanguage === "ja") return `「${name}」を削除すると、現在のメモリ上のデータはすぐに削除されます。`;
  return `删除「${name}」后，当前内存数据会立即移除。`;
}

function routeAttemptCountText(count: number) {
  if (count > 1) {
    if (activeLanguage === "en") return `${formatNumber(count)} attempts, with fallback`;
    if (activeLanguage === "ja") return `${formatNumber(count)} 回、fallback 含む`;
    return `${formatNumber(count)} 次，含 fallback`;
  }
  return countWithUnit(count, "次", "attempt", "回");
}

type NavGroup = {
  title: string;
  items: NavItem[];
};

const userNavGroups: NavGroup[] = [
  {
    title: "开始使用",
    items: [
      { view: "overview", label: "总览", icon: LayoutDashboard },
      { view: "gateway", label: "接口文档", icon: Sparkles },
      { view: "playground", label: "模型演练场", icon: Send },
    ],
  },
  {
    title: "我的资源",
    items: [
      { view: "api-keys", label: "Key 管理", icon: KeyRound },
      { view: "models", label: "可用模型", icon: Boxes },
    ],
  },
  {
    title: "我的用量",
    items: [
      { view: "usage", label: "用量统计", icon: BarChart3 },
      { view: "audit", label: "请求日志", icon: FileText },
    ],
  },
];

const teamLeaderNavGroups: NavGroup[] = [
  {
    title: "团队工作台",
    items: [
      { view: "overview", label: "团队总览", icon: LayoutDashboard },
      { view: "usage", label: "团队报表", icon: BarChart3 },
      { view: "billing", label: "成本归因", icon: WalletCards },
    ],
  },
  {
    title: "项目治理",
    items: [
      { view: "projects", label: "项目空间", icon: LayoutDashboard },
      { view: "api-keys", label: "Key 管理", icon: KeyRound },
      { view: "models", label: "可用模型", icon: Boxes },
      { view: "approvals", label: "审批记录", icon: ShieldCheck },
    ],
  },
  {
    title: "团队管理",
    items: [
      { view: "users", label: "团队成员", icon: Users },
      { view: "teams", label: "团队信息", icon: Users },
      { view: "audit", label: "请求日志", icon: FileText },
      { view: "gateway", label: "接口文档", icon: Sparkles },
    ],
  },
];

const adminNavGroups: NavGroup[] = [
  {
    title: "平台工作台",
    items: [
      { view: "overview", label: "平台总览", icon: LayoutDashboard },
      { view: "usage", label: "全局用量", icon: BarChart3 },
      { view: "reports", label: "导出报表", icon: BarChart3 },
    ],
  },
  {
    title: "AI 资源",
    items: [
      { view: "providers", label: "Provider 渠道", icon: Server },
      { view: "models", label: "模型目录", icon: Boxes },
      { view: "routes", label: "路由策略", icon: Gauge },
    ],
  },
  {
    title: "组织治理",
    items: [
      { view: "projects", label: "项目空间", icon: LayoutDashboard },
      { view: "api-keys", label: "Key 管理", icon: KeyRound },
      { view: "teams", label: "团队分组", icon: Users },
      { view: "users", label: "用户管理", icon: Users },
      { view: "approvals", label: "审批记录", icon: ShieldCheck },
    ],
  },
  {
    title: "成本治理",
    items: [
      { view: "billing", label: "成本账单", icon: WalletCards },
      { view: "cost-centers", label: "成本中心", icon: Database },
    ],
  },
  {
    title: "健康与告警",
    items: [
      { view: "monitors", label: "健康检测", icon: Activity },
      { view: "alerts", label: "告警规则", icon: AlertCircle },
      { view: "alert-events", label: "告警事件", icon: AlertCircle },
      { view: "notification-channels", label: "通知渠道", icon: Bell },
      { view: "alert-deliveries", label: "通知记录", icon: Bell },
    ],
  },
  {
    title: "安全运维",
    items: [
      { view: "security-policies", label: "安全策略", icon: ShieldCheck },
      { view: "proxies", label: "代理出口", icon: Server },
      { view: "sqlite-backups", label: "数据备份", icon: Database },
      { view: "announcements", label: "公告通知", icon: Bell },
      { view: "settings", label: "系统设置", icon: Settings },
    ],
  },
];

const securityNavGroups: NavGroup[] = [
  {
    title: "安全审计导航",
    items: [
      { view: "overview", label: "安全总览", icon: LayoutDashboard },
      { view: "usage", label: "用量统计", icon: BarChart3 },
      { view: "audit", label: "请求日志", icon: FileText },
      { view: "approvals", label: "审批记录", icon: ShieldCheck },
      { view: "security-policies", label: "安全策略", icon: ShieldCheck },
    ],
  },
  {
    title: "告警导航",
    items: [
      { view: "alerts", label: "告警规则", icon: AlertCircle },
      { view: "alert-events", label: "告警事件", icon: AlertCircle },
      { view: "notification-channels", label: "通知渠道", icon: Bell },
      { view: "alert-deliveries", label: "通知记录", icon: Bell },
    ],
  },
  {
    title: "接入参考",
    items: [
      { view: "gateway", label: "接口文档", icon: Sparkles },
    ],
  },
];

const navGroupsByRole: Record<AppRole, NavGroup[]> = {
  admin: adminNavGroups,
  security: securityNavGroups,
  team_leader: teamLeaderNavGroups,
  user: userNavGroups,
};

const allNavGroupTitles = Array.from(new Set(Object.values(navGroupsByRole).flatMap((groups) => groups.map((group) => group.title))));

const standaloneViewMeta: Partial<Record<ViewKey, { title: string; description: string }>> = {
  overview: {
    title: "网关概览",
    description: "",
  },
  playground: {
    title: "模型演练场",
    description: "选择标准模型，按当前路由策略发起测试对话，验证 Provider、路由和返回内容。",
  },
  gateway: {
    title: "接口文档",
    description: "面向业务开发者的模型 API 调用说明、认证方式、示例代码和错误排查。",
  },
  usage: {
    title: "用量统计",
    description: "按模型、项目和日期查看请求量、Token 和成本归因。",
  },
  billing: {
    title: "成本账单",
    description: "按 Provider 和项目归集估算成本，辅助成本分摊。",
  },
  audit: {
    title: "请求日志",
    description: "查看最近请求日志、状态码、模型路由和延迟。",
  },
  "alert-events": {
    title: "告警事件",
    description: "查看运行时触发的额度、成本和 Provider 健康告警。",
  },
  "alert-deliveries": {
    title: "通知记录",
    description: "查看告警 Webhook 发送结果、目标和失败原因。",
  },
  approvals: {
    title: "审批记录",
    description: "处理 Key 发放、额度提升和模型开通等治理审批。",
  },
};

const roleViewAccess: Record<AppRole, ViewKey[]> = {
  admin: (Object.keys(viewRoutes) as ViewKey[]).filter(
    (view) => view !== "project-members" && view !== "quota-policies" && view !== "approval-flows" && view !== "budgets" && view !== "chargebacks" && view !== "invoices",
  ),
  security: ["overview", "gateway", "usage", "audit", "alerts", "alert-events", "notification-channels", "alert-deliveries", "security-policies", "approvals"],
  team_leader: ["overview", "gateway", "playground", "models", "projects", "api-keys", "teams", "users", "usage", "billing", "audit", "approvals"],
  user: ["overview", "gateway", "playground", "models", "api-keys", "usage", "audit"],
};

function appRole(role: string): AppRole {
  const normalized = String(role || "").trim().toLowerCase();
  if (normalized === "admin" || normalized === "system_admin") return "admin";
  if (normalized === "security" || normalized === "security_admin") return "security";
  if (normalized === "team_leader" || normalized === "teamlead" || normalized === "project_admin") return "team_leader";
  return "user";
}

function canAccessView(user: AdminUser, view: ViewKey) {
  return roleViewAccess[appRole(user.role)].includes(view);
}

function navGroupsForUser(user: AdminUser) {
  return navGroupsByRole[appRole(user.role)];
}

function isNavParentItem(item: NavItem): item is NavParentItem {
  return "children" in item;
}

function filterNavItemByAccess(item: NavItem, user: AdminUser): NavItem | null {
  if (isNavParentItem(item)) {
    const children = item.children.filter((child) => canAccessView(user, child.view));
    return children.length > 0 ? { ...item, children } : null;
  }
  return canAccessView(user, item.view) ? item : null;
}

function isNavItemActive(item: NavItem, activeView: ViewKey) {
  if (isNavParentItem(item)) {
    return item.children.some((child) => child.view === activeView);
  }
  return item.view === activeView;
}

function canViewAdminAudit(user: AdminUser) {
  const role = appRole(user.role);
  return role === "admin" || role === "security";
}

function defaultViewForRole(user: AdminUser): ViewKey {
  return roleViewAccess[appRole(user.role)][0] ?? "overview";
}

const topSearchPreferredViews: Record<AppRole, ViewKey[]> = {
  admin: ["overview", "providers", "routes", "models", "projects", "api-keys", "usage", "settings"],
  security: ["overview", "audit", "alert-events", "security-policies", "usage", "gateway"],
  team_leader: ["overview", "projects", "api-keys", "usage", "billing", "gateway"],
  user: ["overview", "gateway", "playground", "models", "api-keys", "usage"],
};

function topSearchItemsForUser(user: AdminUser, data: AppData): TopSearchItem[] {
  const seen = new Set<ViewKey>();
  const items: TopSearchItem[] = [];
  for (const group of navGroupsForUser(user)) {
    for (const item of group.items) {
      const filtered = filterNavItemByAccess(item, user);
      if (!filtered) continue;
      if (isNavParentItem(filtered)) {
        for (const child of filtered.children) {
          addTopSearchItem(items, seen, child, `${group.title} / ${filtered.label}`);
        }
        continue;
      }
      addTopSearchItem(items, seen, filtered, group.title);
    }
  }
  return [...items, ...topSearchEntityItems(user, data)];
}

function addTopSearchItem(items: TopSearchItem[], seen: Set<ViewKey>, item: NavLeafItem, group: string) {
  if (seen.has(item.view)) return;
  seen.add(item.view);
  const meta = standaloneViewMeta[item.view];
  const description = meta?.description || "打开对应工作页面";
  const keywords = [
    item.view,
    viewRoutes[item.view],
    item.label,
    group,
    meta?.title,
    description,
    tx(item.label),
    tx(group),
    meta?.title ? tx(meta.title) : "",
    tx(description),
  ].join(" ");
  items.push({
    id: `view:${item.view}`,
    view: item.view,
    label: item.label,
    group,
    description,
    icon: item.icon,
    tone: "page",
    keywords,
  });
}

function topSearchEntityItems(user: AdminUser, data: AppData): TopSearchItem[] {
  const can = (view: ViewKey) => canAccessView(user, view);
  const items: TopSearchItem[] = [];
  if (can("models")) {
    for (const model of data.models.slice(0, 80)) {
      const label = model.name || model.id;
      items.push({
        id: `model:${model.id || model.name}`,
        view: "models",
        label,
        group: "模型目录",
        description: modelCapabilitySummary(model),
        icon: Boxes,
        tone: "entity",
        keywords: [label, model.id, model.modality, model.family, model.capabilities?.join(" "), "model", "models", "模型", "可用模型"].join(" "),
      });
    }
  }
  if (can("providers")) {
    for (const provider of data.providers.slice(0, 60)) {
      const label = provider.name || provider.id;
      items.push({
        id: `provider:${provider.id}`,
        view: "providers",
        label,
        group: "Provider 渠道",
        description: provider.healthy ? "Provider 健康" : "Provider 需要关注",
        icon: Server,
        tone: "entity",
        keywords: [label, provider.id, provider.base_url, provider.type, "provider", "渠道", "供应商"].join(" "),
      });
    }
  }
  if (can("projects")) {
    for (const project of data.projects.slice(0, 80)) {
      const label = project.name || project.id;
      items.push({
        id: `project:${project.id}`,
        view: "projects",
        label,
        group: "项目空间",
        description: "项目、Key、额度和成本归属",
        icon: LayoutDashboard,
        tone: "entity",
        keywords: [label, project.id, project.team_id, project.status, "project", "项目"].join(" "),
      });
    }
  }
  if (can("audit")) {
    for (const log of data.logs.slice(0, 50)) {
      const label = log.request_id || log.id;
      items.push({
        id: `request:${log.request_id || log.id}`,
        view: "audit",
        label,
        group: "请求日志",
        description: `${log.model || "-"} · HTTP ${log.status_code || "-"}`,
        icon: FileText,
        tone: "entity",
        keywords: [label, log.id, log.model, log.provider_id, log.provider_model, log.status_code, "request", "log", "日志"].join(" "),
      });
    }
  }
  return items;
}

function topSearchResults(items: TopSearchItem[], role: AppRole, normalizedQuery: string, recentViews: ViewKey[]) {
  if (normalizedQuery) {
    return items.filter((item) => normalizeSearchText(item.keywords).includes(normalizedQuery)).slice(0, 8);
  }
  const preferred = topSearchPreferredViews[role];
  const recentItems = recentViews
    .map((view) => items.find((item) => item.view === view && item.id.startsWith("view:")))
    .filter((item): item is TopSearchItem => Boolean(item))
    .map((item) => ({ ...item, tone: "recent" as const }));
  const recentIDs = new Set(recentItems.map((item) => item.id));
  const preferredItems = items
    .filter((item) => item.id.startsWith("view:") && !recentIDs.has(item.id))
    .slice()
    .sort((left, right) => {
      const leftIndex = preferred.indexOf(left.view);
      const rightIndex = preferred.indexOf(right.view);
      const leftRank = leftIndex === -1 ? 99 : leftIndex;
      const rightRank = rightIndex === -1 ? 99 : rightIndex;
      return leftRank - rightRank;
    })
    .slice(0, Math.max(0, 6 - recentItems.length));
  return [...recentItems, ...preferredItems].slice(0, 6);
}

function normalizeSearchText(value: string) {
  return value.trim().toLowerCase();
}

function readRecentViews(): ViewKey[] {
  if (typeof window === "undefined") return [];
  try {
    const parsed = JSON.parse(window.localStorage.getItem(recentViewsStorageKey) || "[]");
    return Array.isArray(parsed) ? parsed.filter((view): view is ViewKey => typeof view === "string" && view in viewRoutes).slice(0, 5) : [];
  } catch {
    return [];
  }
}

function rememberRecentView(view: ViewKey) {
  if (typeof window === "undefined") return;
  const next = [view, ...readRecentViews().filter((item) => item !== view)].slice(0, 5);
  window.localStorage.setItem(recentViewsStorageKey, JSON.stringify(next));
}

function topQuickActionsForUser(user: AdminUser): NavLeafItem[] {
  const role = appRole(user.role);
  const candidates: Record<AppRole, NavLeafItem[]> = {
    admin: [
      { view: "providers", label: "Provider 渠道", icon: Server },
      { view: "routes", label: "路由策略", icon: Gauge },
      { view: "settings", label: "系统设置", icon: Settings },
    ],
    security: [
      { view: "audit", label: "请求日志", icon: FileText },
      { view: "alert-events", label: "告警事件", icon: AlertCircle },
      { view: "security-policies", label: "安全策略", icon: ShieldCheck },
    ],
    team_leader: [
      { view: "projects", label: "项目空间", icon: LayoutDashboard },
      { view: "api-keys", label: "Key 管理", icon: KeyRound },
      { view: "usage", label: "团队报表", icon: BarChart3 },
    ],
    user: [
      { view: "gateway", label: "接口文档", icon: Sparkles },
      { view: "playground", label: "模型演练场", icon: Send },
      { view: "api-keys", label: "Key 管理", icon: KeyRound },
    ],
  };
  return candidates[role].filter((item) => canAccessView(user, item.view));
}

function roleScopeDescription(user: AdminUser) {
  switch (appRole(user.role)) {
    case "admin":
      return "全局平台范围";
    case "security":
      return "安全审计范围";
    case "team_leader":
      return "团队和项目范围";
    default:
      return "个人可见范围";
  }
}

type LoadPlan = {
  overview: boolean;
  providers: boolean;
  providerResources: boolean;
  keys: boolean;
  routes: boolean;
  logs: boolean;
  auditEvents: boolean;
  alerts: boolean;
  alertDeliveries: boolean;
  approvals: boolean;
  sqliteBackups: boolean;
  breakdown: boolean;
  timeseries: boolean;
  users: boolean;
  providerCatalog: boolean;
  resources: string[];
};

type LoadedData = Partial<Omit<AppData, "resources">> & {
  resources?: Record<string, AdminResource[]>;
};

function emptyLoadPlan(): LoadPlan {
  return {
    overview: false,
    providers: false,
    providerResources: false,
    keys: false,
    routes: false,
    logs: false,
    auditEvents: false,
    alerts: false,
    alertDeliveries: false,
    approvals: false,
    sqliteBackups: false,
    breakdown: false,
    timeseries: false,
    users: false,
    providerCatalog: false,
    resources: [],
  };
}

function addResourceDependency(plan: LoadPlan, kind: string) {
  if (!plan.resources.includes(kind)) {
    plan.resources.push(kind);
  }
}

function loadPlanForView(user: AdminUser, view: ViewKey): LoadPlan {
  const plan = emptyLoadPlan();
  const can = (target: ViewKey) => canAccessView(user, target);

  switch (view) {
    case "overview":
      plan.overview = true;
      plan.breakdown = true;
      plan.timeseries = true;
      plan.logs = can("audit");
      plan.users = appRole(user.role) === "team_leader";
      if (appRole(user.role) === "team_leader") {
        addResourceDependency(plan, "teams");
      }
      addResourceDependency(plan, "announcements");
      break;
    case "playground":
      plan.overview = true;
      plan.routes = can("routes");
      break;
    case "gateway":
      plan.overview = true;
      plan.keys = can("api-keys");
      plan.routes = can("routes");
      plan.logs = can("audit");
      break;
    case "usage":
      plan.overview = true;
      plan.breakdown = true;
      plan.timeseries = true;
      plan.users = can("users") || appRole(user.role) === "team_leader";
      if (appRole(user.role) !== "user") {
        addResourceDependency(plan, "teams");
        addResourceDependency(plan, "cost-centers");
      }
      break;
    case "billing":
      plan.breakdown = true;
      plan.users = appRole(user.role) === "team_leader";
      break;
    case "audit":
      plan.overview = true;
      plan.keys = can("api-keys");
      plan.logs = true;
      plan.auditEvents = canViewAdminAudit(user);
      break;
    case "providers":
      plan.providers = true;
      plan.providerResources = true;
      plan.overview = true;
      plan.routes = true;
      plan.logs = can("audit");
      plan.breakdown = can("usage") || can("billing");
      plan.providerCatalog = true;
      break;
    case "models":
      plan.overview = true;
      plan.routes = can("routes");
      break;
    case "routes":
      plan.overview = true;
      plan.routes = true;
      break;
    case "projects":
      plan.overview = true;
      plan.logs = true;
      plan.users = can("users") || appRole(user.role) === "team_leader";
      plan.approvals = can("approvals");
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "cost-centers");
      addResourceDependency(plan, "quota-policies");
      addResourceDependency(plan, "project-members");
      break;
    case "project-members":
      plan.overview = true;
      plan.users = true;
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "project-members");
      break;
    case "api-keys":
      plan.overview = true;
      plan.keys = true;
      plan.users = can("users") || appRole(user.role) === "team_leader";
      if (appRole(user.role) !== "user") {
        addResourceDependency(plan, "teams");
      }
      addResourceDependency(plan, "project-members");
      break;
    case "teams":
      plan.users = true;
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "cost-centers");
      break;
    case "users":
      plan.users = true;
      addResourceDependency(plan, "teams");
      addResourceDependency(plan, "role-configs");
      break;
    case "settings":
      addResourceDependency(plan, "settings");
      addResourceDependency(plan, "role-configs");
      addResourceDependency(plan, "identity-providers");
      break;
    case "quota-policies":
    case "cost-centers":
    case "budgets":
    case "chargebacks":
    case "approval-flows":
    case "invoices":
    case "reports":
    case "notification-channels":
    case "monitors":
    case "proxies":
    case "announcements":
    case "security-policies":
    case "identity-providers":
      addResourceDependency(plan, view);
      break;
    case "alerts":
      addResourceDependency(plan, "alert-rules");
      break;
    case "alert-events":
      plan.alerts = true;
      break;
    case "alert-deliveries":
      plan.alertDeliveries = true;
      break;
    case "approvals":
      plan.approvals = true;
      break;
    case "sqlite-backups":
      plan.sqliteBackups = true;
      break;
  }

  return plan;
}

function mergeLoadedData(current: AppData, loaded: LoadedData): AppData {
  return {
    ...current,
    summary: loaded.summary ?? current.summary,
    projects: loaded.projects ?? current.projects,
    providers: loaded.providers ?? current.providers,
    providerResources: loaded.providerResources ?? current.providerResources,
    models: loaded.models ?? current.models,
    routes: loaded.routes ?? current.routes,
    logs: loaded.logs ?? current.logs,
    auditEvents: loaded.auditEvents ?? current.auditEvents,
    alerts: loaded.alerts ?? current.alerts,
    alertDeliveries: loaded.alertDeliveries ?? current.alertDeliveries,
    approvals: loaded.approvals ?? current.approvals,
    sqliteBackups: loaded.sqliteBackups ?? current.sqliteBackups,
    users: loaded.users ?? current.users,
    breakdown: loaded.breakdown ?? current.breakdown,
    timeseries: loaded.timeseries ?? current.timeseries,
    keys: loaded.keys ?? current.keys,
    providerCatalog: loaded.providerCatalog ?? current.providerCatalog,
    resources: loaded.resources ? { ...current.resources, ...loaded.resources } : current.resources,
  };
}

export default function AdminHome() {
  const [language, setLanguage] = useState<AppLanguage>(() => readSavedLanguage());
  const [theme, setTheme] = useState<"light" | "dark">("light");
  const [baseURL, setBaseURL] = useState(defaultBaseURL);
  const [adminToken, setAdminToken] = useState("");
  const [currentUser, setCurrentUser] = useState<AdminUser | null>(null);
  const [loginIdentityProviders, setLoginIdentityProviders] = useState<LoginIdentityProvider[]>([]);
  const [oauthReturnURL, setOAuthReturnURL] = useState(() => currentOAuthReturnURL());
  const [bootstrapped, setBootstrapped] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false);
  const [openNavGroups, setOpenNavGroups] = useState<Record<string, boolean>>(() =>
    Object.fromEntries(allNavGroupTitles.map((title) => [title, true])),
  );
  const [activeView, setActiveView] = useState<ViewKey>(() => initialView());
  const [data, setData] = useState<AppData>(emptyData());
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [loading, setLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [modelCategoryFilter, setModelCategoryFilter] = useState("all");
  const [settingsTab, setSettingsTab] = useState<SettingsTabKey>("settings");
  const [modal, setModal] = useState<ModalState<any> | null>(null);
  const [providerCreateOpen, setProviderCreateOpen] = useState(false);
  const [providerEditItem, setProviderEditItem] = useState<Provider | null>(null);
  const [apiKeyWizardOpen, setApiKeyWizardOpen] = useState(false);
  const [apiKeyWizardInitialValues, setApiKeyWizardInitialValues] = useState<Record<string, string>>({});
  const [userImportOpen, setUserImportOpen] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<ConfirmState<any> | null>(null);
  const [issuedKey, setIssuedKey] = useState("");
  const [reportHistory, setReportHistory] = useState<ReportExportHistoryItem[]>([]);
  const resetToken = typeof window === "undefined" ? "" : new URLSearchParams(window.location.search).get("reset_token") ?? "";

  const api = useMemo(() => ({ baseURL, adminToken }), [baseURL, adminToken]);
  const activeConfig = resourceConfigs[activeView];
  const activeMeta = activeConfig ?? standaloneViewMeta[activeView] ?? standaloneViewMeta.overview!;
  setActiveLanguage(language);

  function changeLanguage(nextLanguage: AppLanguage) {
    setLanguage(nextLanguage);
  }

  function toggleTheme() {
    setTheme((value) => (value === "light" ? "dark" : "light"));
  }

  function selectView(view: ViewKey, options: { replace?: boolean } = {}) {
    if (view !== activeView) {
      setNotice("");
      setError("");
      setIssuedKey("");
      setModelCategoryFilter(view === "notification-channels" ? "webhook" : "all");
    }
    setActiveView(view);
    if (typeof window === "undefined") return;
    const nextPath = viewRoutes[view];
    if (window.location.pathname === nextPath) return;
    const nextURL = `${nextPath}${window.location.search}${window.location.hash}`;
    if (options.replace) {
      window.history.replaceState({ view }, "", nextURL);
    } else {
      window.history.pushState({ view }, "", nextURL);
    }
  }

  useEffect(() => {
    let cancelled = false;
    async function bootstrapSession() {
      const saved = readSavedSession();
      const oauth = readOAuthLoginResult();
      const sessionBaseURL = readPendingOAuthBaseURL() ?? saved?.baseURL ?? defaultBaseURL;
      if (!oauth && forwardOAuthAuthorizationResponse(sessionBaseURL)) return;
      if (oauth?.error) {
        clearOAuthLoginResult();
        clearPendingOAuthBaseURL();
        setError(tx("OAuth 登录失败"));
      }
      if (oauth?.token) {
        setBaseURL(sessionBaseURL);
        setLoading(true);
        try {
          const resp = await fetch(`${sessionBaseURL.replace(/\/$/, "")}/api/admin/auth/me`, {
            headers: { authorization: `Bearer ${oauth.token}` },
          });
          if (!resp.ok) {
            throw new Error(await readAdminError(resp, "OAuth 会话校验失败"));
          }
          const payload = (await resp.json()) as { user: AdminUser };
          const expiresAt = oauth.expiresAt || new Date(Date.now() + 12 * 60 * 60 * 1000).toISOString();
          if (cancelled) return;
          setData(emptyData());
          setAdminToken(oauth.token);
          setCurrentUser(payload.user);
          saveSession({ baseURL: sessionBaseURL, token: oauth.token, user: payload.user, expiresAt });
          setError("");
        } catch (err) {
          if (!cancelled) setError(err instanceof Error ? err.message : tx("OAuth 登录失败"));
        } finally {
          clearOAuthLoginResult();
          clearPendingOAuthBaseURL();
          if (!cancelled) {
            setLoading(false);
            setBootstrapped(true);
          }
        }
        return;
      }
      if (saved) {
        setBaseURL(saved.baseURL);
        setAdminToken(saved.token);
        setCurrentUser(saved.user);
      }
      setBootstrapped(true);
    }
    void bootstrapSession();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (typeof window !== "undefined") {
      setOAuthReturnURL(currentOAuthReturnURL());
    }
  }, []);

  useEffect(() => {
    const result = readProviderAccountOAuthResultFromLocation();
    if (!result) return;
    savePendingProviderAccountOAuthResult(result);
    clearProviderAccountOAuthResultFromLocation();
  }, []);

  useEffect(() => {
    if (!currentUser || !hasPendingProviderAccountOAuthResult()) return;
    selectView("providers", { replace: true });
    setProviderCreateOpen(true);
    setNotice(tx("收到账号授权回调，已打开账号池创建向导。"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentUser]);

  useEffect(() => {
    if (!bootstrapped || currentUser) return;
    let cancelled = false;
    async function loadLoginIdentityProviders() {
      try {
        const resp = await fetch(`${baseURL.replace(/\/$/, "")}/api/admin/auth/identity-providers`);
        if (!resp.ok) {
          if (!cancelled) setLoginIdentityProviders([]);
          return;
        }
        const payload = (await resp.json()) as { data?: LoginIdentityProvider[] };
        if (!cancelled) setLoginIdentityProviders(payload.data ?? []);
      } catch {
        if (!cancelled) setLoginIdentityProviders([]);
      }
    }
    void loadLoginIdentityProviders();
    return () => {
      cancelled = true;
    };
  }, [baseURL, bootstrapped, currentUser]);

  useEffect(() => {
    setActiveLanguage(language);
    if (typeof window !== "undefined") {
      window.localStorage.setItem(languageStorageKey, language);
      document.documentElement.lang = language === "zh-CN" ? "zh-CN" : language;
    }
  }, [language]);

  useEffect(() => {
    if (!bootstrapped || !adminToken || !currentUser) return;
    if (!canAccessView(currentUser, activeView)) return;
    void load(activeView);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bootstrapped, adminToken, currentUser, activeView]);

  useEffect(() => {
    if (activeView === "notification-channels" && !notificationChannelTypes.includes(modelCategoryFilter)) {
      setModelCategoryFilter("webhook");
    }
  }, [activeView, modelCategoryFilter]);

  useEffect(() => {
    if (!currentUser) return;
    if (!canAccessView(currentUser, activeView)) {
      selectView(defaultViewForRole(currentUser), { replace: true });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentUser, activeView]);

  useEffect(() => {
    if (!currentUser || typeof window === "undefined") return;
    if (!canAccessView(currentUser, activeView)) return;
    rememberRecentView(activeView);
    const expectedPath = viewRoutes[activeView];
    if (window.location.pathname !== expectedPath) {
      window.history.replaceState({ view: activeView }, "", expectedPath);
    }
  }, [currentUser, activeView]);

  useEffect(() => {
    if (isOAuthAuthorizationResponse()) return;
    if (readOAuthLoginResult()) return;
    const view = viewFromPath(window.location.pathname);
    setActiveView(view);
    if (window.location.pathname !== viewRoutes[view]) {
      window.history.replaceState({ view }, "", `${viewRoutes[view]}${window.location.search}${window.location.hash}`);
    }
    function onPopState() {
      setNotice("");
      setError("");
      const nextView = viewFromPath(window.location.pathname);
      setModelCategoryFilter(nextView === "notification-channels" ? "webhook" : "all");
      setActiveView(nextView);
    }
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  useEffect(() => {
    function onAuthExpired() {
      clearSavedSession();
      setAdminToken("");
      setCurrentUser(null);
      setData(emptyData());
      setModal(null);
      setProviderCreateOpen(false);
      setProviderEditItem(null);
      setApiKeyWizardOpen(false);
      setApiKeyWizardInitialValues({});
      setUserImportOpen(false);
      setConfirmDelete(null);
      setIssuedKey("");
      setNotice("");
      setError("");
      selectView("overview", { replace: true });
      setLoading(false);
    }
    window.addEventListener(authExpiredEventName, onAuthExpired);
    return () => window.removeEventListener(authExpiredEventName, onAuthExpired);
  }, []);

  useEffect(() => {
    function onIssuedKey(event: Event) {
      const detail = (event as CustomEvent<string>).detail;
      if (!detail) return;
      if (isIssuedAPIKey(detail)) {
        setNotice("");
        setIssuedKey(detail);
      } else {
        setIssuedKey("");
        setNotice(detail);
      }
    }
    window.addEventListener("tokenhub-issued-key", onIssuedKey);
    return () => window.removeEventListener("tokenhub-issued-key", onIssuedKey);
  }, []);

  async function load(view: ViewKey = activeView) {
    if (!adminToken || !currentUser) return;
    if (!canAccessView(currentUser, view)) return;
    setLoading(true);
    setError("");
    try {
      const plan = loadPlanForView(currentUser, view);
      const requests: Array<{ name: string; request: Promise<Response>; optional?: boolean }> = [];
      const queue = (enabled: boolean, name: string, path: string) => {
        if (enabled) requests.push({ name, request: adminFetch(api, path) });
      };

      queue(plan.overview, "overview", "/api/admin/overview");
      queue(plan.providers, "providers", "/api/admin/providers");
      queue(plan.providerResources, "provider-resources", "/api/admin/provider-resources");
      queue(plan.keys, "api-keys", "/api/admin/api-keys");
      queue(plan.routes, "routes", "/api/admin/routing-rules");
      queue(plan.logs, "audit", "/api/admin/audit/requests");
      queue(plan.auditEvents, "audit-events", "/api/admin/audit/events");
      queue(plan.alerts, "alerts", "/api/admin/alerts");
      queue(plan.alertDeliveries, "alert-deliveries", "/api/admin/alert-deliveries");
      queue(plan.approvals, "approvals", "/api/admin/approvals");
      queue(plan.sqliteBackups, "sqlite-backups", "/api/admin/sqlite/backups");
      queue(plan.breakdown, "breakdown", "/api/admin/usage/breakdown");
      queue(plan.timeseries, "timeseries", "/api/admin/usage/timeseries");
      queue(plan.users, "users", "/api/admin/users");
      queue(plan.providerCatalog, "provider-catalog", "/api/admin/provider-catalog");
      for (const kind of plan.resources) {
        requests.push({ name: `resource:${kind}`, request: adminFetch(api, `/api/admin/resources/${kind}`), optional: true });
      }

      const responses = await Promise.all(requests.map((item) => item.request));
      const skippedResources: string[] = [];
      for (let index = 0; index < responses.length; index += 1) {
        const resp = responses[index];
        if (!resp.ok) {
          if (resp.status === 403 && requests[index].optional) {
            skippedResources.push(loadRequestLabel(requests[index].name));
            continue;
          }
          throw new Error(await readLoadError(resp, requests[index].name));
        }
      }

      const loaded: LoadedData = {};
      for (let index = 0; index < responses.length; index += 1) {
        const name = requests[index].name;
        const resp = responses[index];
        if (!resp.ok) continue;
        if (name === "overview") {
          const overview = await resp.json();
          loaded.summary = overview.summary ?? emptySummary();
          loaded.projects = overview.projects ?? [];
          loaded.providers = overview.providers ?? [];
          loaded.providerResources = overview.provider_resources ?? [];
          loaded.models = overview.models ?? [];
          loaded.alerts = overview.alerts ?? [];
        } else if (name === "providers") {
          const payload = (await resp.json()) as { data: Provider[] };
          loaded.providers = payload.data ?? [];
        } else if (name === "provider-resources") {
          const payload = (await resp.json()) as { data: ProviderResource[] };
          loaded.providerResources = payload.data ?? [];
        } else if (name === "api-keys") {
          const payload = (await resp.json()) as { data: APIKey[] };
          loaded.keys = payload.data ?? [];
        } else if (name === "routes") {
          const payload = (await resp.json()) as { data: ModelRoute[] };
          loaded.routes = payload.data ?? [];
        } else if (name === "audit") {
          const payload = (await resp.json()) as { data: RequestLog[] };
          loaded.logs = payload.data ?? [];
        } else if (name === "audit-events") {
          const payload = (await resp.json()) as { data: AuditEvent[] };
          loaded.auditEvents = payload.data ?? [];
        } else if (name === "alerts") {
          const payload = (await resp.json()) as { data: AlertEvent[] };
          loaded.alerts = payload.data ?? [];
        } else if (name === "alert-deliveries") {
          const payload = (await resp.json()) as { data: AlertDelivery[] };
          loaded.alertDeliveries = payload.data ?? [];
        } else if (name === "approvals") {
          const payload = (await resp.json()) as { data: ApprovalRequest[] };
          loaded.approvals = payload.data ?? [];
        } else if (name === "sqlite-backups") {
          const payload = (await resp.json()) as { data: SQLiteBackup[] };
          loaded.sqliteBackups = payload.data ?? [];
        } else if (name === "breakdown") {
          loaded.breakdown = (await resp.json()) as UsageBreakdown;
        } else if (name === "timeseries") {
          const payload = (await resp.json()) as { data: UsagePoint[] };
          loaded.timeseries = payload.data ?? [];
        } else if (name === "users") {
          const payload = (await resp.json()) as { data: AdminUser[] };
          loaded.users = payload.data ?? [];
        } else if (name === "provider-catalog") {
          const payload = (await resp.json()) as { data: ProviderCatalogEntry[] };
          loaded.providerCatalog = payload.data ?? [];
        } else if (name.startsWith("resource:")) {
          const kind = name.slice("resource:".length);
          const payload = (await resp.json()) as { data: AdminResource[] };
          loaded.resources = { ...(loaded.resources ?? {}), [kind]: payload.data ?? [] };
        }
      }

      setData((current) => mergeLoadedData(current, loaded));
      if (skippedResources.length > 0) {
        setNotice(permissionPartialLoadMessage(skippedResources));
      }
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("连接失败"));
    } finally {
      setLoading(false);
    }
  }

  async function login(identity: string, password: string) {
    setLoading(true);
    setError("");
    try {
      const resp = await fetch(`${baseURL.replace(/\/$/, "")}/api/admin/auth/login`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ identity, password }),
      });
      if (!resp.ok) throw new Error(`login ${resp.status}`);
      const payload = (await resp.json()) as { token: string; user: AdminUser; expires_at: string };
      setData(emptyData());
      setAdminToken(payload.token);
      setCurrentUser(payload.user);
      saveSession({ baseURL, token: payload.token, user: payload.user, expiresAt: payload.expires_at });
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("登录失败"));
    } finally {
      setLoading(false);
    }
  }

  async function resetPassword(token: string, password: string) {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const resp = await fetch(`${baseURL.replace(/\/$/, "")}/api/admin/auth/reset-password`, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token, password }),
      });
      if (!resp.ok) throw new Error(`reset password ${resp.status}`);
      window.history.replaceState(null, "", window.location.pathname);
      setNotice(tx("密码已重置，请使用新密码登录"));
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("密码重置失败"));
    } finally {
      setLoading(false);
    }
  }

  async function logout() {
    if (adminToken) {
      await adminFetch(api, "/api/admin/auth/logout", { method: "POST" }).catch(() => undefined);
    }
    clearSavedSession();
    setAdminToken("");
    setCurrentUser(null);
    setData(emptyData());
    setUserImportOpen(false);
    setApiKeyWizardOpen(false);
    setApiKeyWizardInitialValues({});
    selectView("overview", { replace: true });
  }

  async function saveModal(values: Record<string, string>) {
    if (!modal) return;
    setLoading(true);
    setError("");
    try {
      if (modal.item) {
        await modal.config.update?.(api, modal.item, values);
      } else {
        await modal.config.create?.(api, values, data);
      }
      setModal(null);
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("保存失败"));
    } finally {
      setLoading(false);
    }
  }

  async function deleteItem<T>(config: ResourceConfig<T>, item: T) {
    if (!config.remove) return;
    setLoading(true);
    setError("");
    try {
      await config.remove(api, item);
      setIssuedKey("");
      setConfirmDelete(null);
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("删除失败"));
    } finally {
      setLoading(false);
    }
  }

  async function importUsersFromCSV(content: string) {
    const trimmed = content.trim();
    if (!trimmed) {
      setNotice("");
      setError(tx("请先粘贴 CSV 内容。"));
      return;
    }
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const result = await importUsersFromCSVContent(api, trimmed);
      const created = result.created ?? 0;
      const updated = result.updated ?? 0;
      const skipped = result.skipped ?? 0;
      setUserImportOpen(false);
      await load("users");
      setNotice(importUsersDoneMessage(created, updated, skipped));
      if (skipped > 0 && result.errors?.length) {
        setError(importUsersSkippedMessage(skipped, result.errors.slice(0, 3).join("；")));
      }
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("用户导入失败"));
    } finally {
      setLoading(false);
    }
  }

  function openCreateRoute() {
    if (!activeConfig) return;
    if (loading) {
      setNotice("");
      setError(tx("数据加载中，请稍后再操作。"));
      return;
    }
    if (data.models.length === 0) {
      setNotice("");
      setError(tx("请先维护模型目录，再新增路由策略。"));
      selectView("models");
      return;
    }
    if (data.providers.length === 0) {
      setNotice("");
      setError(tx("请先新增 Provider 渠道，再配置路由策略。"));
      selectView("providers");
      return;
    }
    setModal({ config: activeConfig });
  }

  function openCreateForCurrentView() {
    if (!activeConfig) return;
    if (activeView === "routes") {
      openCreateRoute();
      return;
    }
    if (loading) {
      setNotice("");
      setError(tx("数据加载中，请稍后再操作。"));
      return;
    }
    if (activeConfig.view === "providers") {
      setProviderCreateOpen(true);
      return;
    }
    if (activeConfig.view === "api-keys" && data.projects.length === 0) {
      setNotice("");
      setError(tx("请先创建项目，再在项目下发放 API Key。"));
      selectView("projects");
      return;
    }
    if (activeConfig.view === "api-keys" && projectSelectOptions(data, currentUser).length === 0) {
      setNotice("");
      setError(tx("当前账号没有可发放 Key 的项目权限，请联系项目负责人或管理员把你加入项目。"));
      return;
    }
    if (activeConfig.view === "notification-channels") {
      setModal({ config: activeConfig, initialValues: notificationChannelDefaults(modelCategoryFilter) });
      return;
    }
    if (activeConfig.view === "api-keys") {
      setIssuedKey("");
      setApiKeyWizardInitialValues({});
      setApiKeyWizardOpen(true);
      return;
    }
    setModal({ config: activeConfig });
  }

  async function reorderModelRoutes(model: Model, orderedRoutes: ModelRoute[]) {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      for (let index = 0; index < orderedRoutes.length; index += 1) {
        const route = orderedRoutes[index];
        const nextPriority = index + 1;
        if (route.priority === nextPriority) continue;
        await adminMutate(api, `/api/admin/routing-rules/${route.id}`, "PATCH", routePayload({
          ...stringifyForm(route),
          priority: String(nextPriority),
        }));
      }
      setNotice(tx(`已更新 ${model.name} 的 Provider 调用顺序`));
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("更新路由顺序失败"));
    } finally {
      setLoading(false);
    }
  }

  const viewItems = activeConfig?.list(data) ?? [];
  const categoryItems = filterByModelCategory(activeConfig?.view, viewItems, modelCategoryFilter, data);
  const filteredItems = filterRows(categoryItems, query);
  const crudPagination = usePagination(filteredItems.length, `${activeView}:${modelCategoryFilter}:${query}`);
  const pagedItems = useMemo(
    () => filteredItems.slice(crudPagination.startIndex, crudPagination.endIndex),
    [filteredItems, crudPagination.startIndex, crudPagination.endIndex],
  );

  if (!bootstrapped) {
    return <main className="login-shell" />;
  }

  if (resetToken && !currentUser) {
    return (
      <ResetPasswordView
        loading={loading}
        error={error}
        theme={theme}
        onThemeToggle={toggleTheme}
        token={resetToken}
        onReset={(token, password) => void resetPassword(token, password)}
      />
    );
  }

  if (!currentUser) {
    return (
      <LoginView
        loading={loading}
        error={error}
        baseURL={baseURL}
        identityProviders={loginIdentityProviders}
        oauthReturnURL={oauthReturnURL}
        theme={theme}
        onThemeToggle={toggleTheme}
        onLogin={(identity, password) => void login(identity, password)}
      />
    );
  }

  return (
    <main className={sidebarCollapsed ? "app-shell sidebar-collapsed" : "app-shell"} data-theme={theme}>
      <Sidebar
        activeView={activeView}
        onSelect={selectView}
        user={currentUser}
        onLogout={() => void logout()}
        collapsed={sidebarCollapsed}
        onToggleCollapse={() => setSidebarCollapsed((value) => !value)}
        openGroups={openNavGroups}
        onToggleGroup={(title) =>
          setOpenNavGroups((current) => ({ ...current, [title]: current[title] === false }))
        }
      />

      <section className="workspace">
        <TopNav
          activeView={activeView}
          data={data}
          user={currentUser}
          theme={theme}
          onSelectView={selectView}
          onThemeToggle={toggleTheme}
        />

        <div className={activeView === "playground" ? "content-panel playground-content-panel" : "content-panel"}>
          {activeView === "playground" || activeView === "overview" ? null : (
            <PageHeader activeView={activeView} data={data} meta={activeMeta} user={currentUser} />
          )}

          <StatusStack
            error={error}
            notice={notice}
            onClearError={() => setError("")}
            onClearNotice={() => setNotice("")}
          />

          {activeView === "playground" ? null : <div className="divider" />}

          {activeView === "overview" ? (
            <OverviewView data={data} user={currentUser} onSelectView={selectView} />
          ) : activeView === "playground" ? (
            <PlaygroundPage api={api} data={data} canViewRoutes={canAccessView(currentUser, "routes")} />
          ) : activeView === "gateway" ? (
            <GatewayView api={api} data={data} user={currentUser} language={language} onLanguageChange={changeLanguage} />
          ) : activeView === "usage" ? (
            <UsageView data={data} user={currentUser} />
          ) : activeView === "billing" ? (
            <BillingView data={data} user={currentUser} />
          ) : activeView === "audit" ? (
            <AuditView api={api} data={data} user={currentUser} />
          ) : activeView === "settings" ? (
            <SettingsView
              data={data}
              activeTab={settingsTab}
              language={language}
              onTabChange={setSettingsTab}
              onLanguageChange={changeLanguage}
              onCreate={(config) => setModal({ config })}
              onEdit={(config, item) => setModal({ config, item })}
              onDelete={(config, item) => setConfirmDelete({ config, item })}
              onAction={(action, item) => void runResourceAction(action, item, data)}
              onToolbarAction={(action, items) => void runToolbarAction(action, items)}
            />
          ) : activeView === "routes" && activeConfig ? (
            <RouteStrategyView
              config={activeConfig as ResourceConfig<ModelRoute>}
              data={data}
              loading={loading}
              onCreate={openCreateRoute}
              onEdit={(route) => setModal({ config: activeConfig, item: route })}
              onDelete={(route) => setConfirmDelete({ config: activeConfig, item: route })}
              onReorder={(model, routes) => void reorderModelRoutes(model, routes)}
            />
          ) : activeView === "models" && activeConfig ? (
            <ModelCatalogView
              config={activeConfig as ResourceConfig<Model>}
              data={data}
              readOnly={!canAccessView(currentUser, "routes")}
              onCreate={() => setModal({ config: activeConfig })}
              onEdit={(item) => setModal({ config: activeConfig, item })}
              onDelete={(item) => setConfirmDelete({ config: activeConfig, item })}
              onAction={(action, item) => void runResourceAction(action, item, data)}
            />
          ) : activeView === "reports" && activeConfig ? (
            <ReportsView
              config={activeConfig as ResourceConfig<AdminResource>}
              data={data}
              history={reportHistory}
              loading={loading}
              onCreate={() => setModal({ config: activeConfig })}
              onEdit={(item) => setModal({ config: activeConfig, item })}
              onDelete={(item) => setConfirmDelete({ config: activeConfig, item })}
              onAction={(action, item) => void runResourceAction(action, item, data)}
              onExport={(dataset) => void exportReportDataset(dataset)}
            />
          ) : activeConfig ? (
            <CrudView
              config={activeConfig}
              data={data}
              items={pagedItems}
              monitorItems={filteredItems}
              totalItems={filteredItems.length}
              loading={loading}
              query={query}
              pagination={crudPagination}
              categoryFilter={modelCategoryFilter}
              onCategoryFilter={setModelCategoryFilter}
              onQuery={setQuery}
              onCreate={openCreateForCurrentView}
              onEdit={(item) => {
                if (activeConfig.view === "providers") {
                  setProviderEditItem(item as Provider);
                  return;
                }
                setModal({ config: activeConfig, item });
              }}
              onDelete={(item) => setConfirmDelete({ config: activeConfig, item })}
              onAction={(action, item) => void runResourceAction(action, item, data)}
              onProjectMemberCreate={(project) => setModal({ config: projectMemberConfig(), initialValues: projectMemberInitialValues(project) })}
              onProjectMemberEdit={(member) => setModal({ config: projectMemberConfig(), item: member })}
              onProjectMemberDelete={(member) => setConfirmDelete({ config: projectMemberConfig(), item: member })}
              onToolbarAction={(action) => void runToolbarAction(action, filteredItems)}
            />
          ) : null}
        </div>
      </section>

      {modal ? (
        <EditModal
          state={modal}
          data={data}
          currentUser={currentUser}
          loading={loading}
          onClose={() => setModal(null)}
          onSave={(values) => {
            if (modal.config.view === "api-keys" && !modal.item) {
              void createKeyWithCapture(api, values, setIssuedKey, setNotice, load, setLoading, setError, () => setModal(null));
              return;
            }
            void saveModal(values);
          }}
        />
      ) : null}

      {providerCreateOpen ? (
        <ProviderUpsertModal
          mode="create"
          api={api}
          catalog={data.providerCatalog}
          standardModels={data.models}
          loading={loading}
          onClose={() => setProviderCreateOpen(false)}
          onSaved={async () => {
            setProviderCreateOpen(false);
            await load();
          }}
          setLoading={setLoading}
          setError={setError}
          setNotice={setNotice}
        />
      ) : null}

      {providerEditItem ? (
        <ProviderUpsertModal
          mode="edit"
          provider={providerEditItem}
          api={api}
          catalog={data.providerCatalog}
          standardModels={data.models}
          routes={data.routes}
          loading={loading}
          onClose={() => setProviderEditItem(null)}
          onSaved={async () => {
            setProviderEditItem(null);
            await load();
          }}
          setLoading={setLoading}
          setError={setError}
          setNotice={setNotice}
        />
      ) : null}

      {apiKeyWizardOpen ? (
        <APIKeyWizardModal
          data={data}
          currentUser={currentUser}
          initialValues={apiKeyWizardInitialValues}
          loading={loading}
          onClose={() => {
            if (!loading) {
              setApiKeyWizardOpen(false);
              setApiKeyWizardInitialValues({});
            }
          }}
          onCreate={(values) => {
            setIssuedKey("");
            void createKeyWithCapture(api, values, setIssuedKey, setNotice, load, setLoading, setError, () => {
              setApiKeyWizardOpen(false);
              setApiKeyWizardInitialValues({});
            });
          }}
        />
      ) : null}

      {userImportOpen ? (
        <UserImportModal
          loading={loading}
          onClose={() => setUserImportOpen(false)}
          onImport={(content) => void importUsersFromCSV(content)}
        />
      ) : null}

      {confirmDelete ? (
        <ConfirmDialog
          title={tx("确认删除")}
          message={deleteConfirmMessage(rowTitle(confirmDelete.item))}
          loading={loading}
          onCancel={() => setConfirmDelete(null)}
          onConfirm={() => void deleteItem(confirmDelete.config, confirmDelete.item)}
        />
      ) : null}

      {issuedKey ? (
        <IssuedKeyModal
          value={issuedKey}
          onClose={() => setIssuedKey("")}
        />
      ) : null}

    </main>
  );

  async function runResourceAction<T>(action: ResourceAction<T>, item: T, appData: AppData) {
    if (action.modal) {
      const nextModal = action.modal(item, appData);
      if (nextModal.config.view === "api-keys" && !nextModal.item) {
        setIssuedKey("");
        setApiKeyWizardInitialValues(nextModal.initialValues ?? {});
        setApiKeyWizardOpen(true);
        return;
      }
      setModal(nextModal);
      return;
    }
    if (!action.run) return;
    setLoading(true);
    setError("");
    setNotice("");
    try {
      await action.run(api, item);
      setNotice(tx(action.doneMessage?.(item) ?? "操作已完成"));
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("操作失败"));
    } finally {
      setLoading(false);
    }
  }

  async function runToolbarAction(action: ToolbarAction, items: unknown[]) {
    if (action.kind === "import-users") {
      setError("");
      setNotice("");
      setUserImportOpen(true);
      return;
    }
    if (!action.run) return;
    setLoading(true);
    setError("");
    setNotice("");
    try {
      await action.run(api, items);
      setNotice(tx(action.doneMessage?.() ?? "操作已完成"));
      await load();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("操作失败"));
    } finally {
      setLoading(false);
    }
  }

  async function exportReportDataset(dataset: string) {
    setLoading(true);
    setError("");
    setNotice("");
    try {
      const result = await downloadReport(api, dataset);
      if (result) {
        setNotice(tx(`${reportDatasetLabel(dataset)} 已导出`));
        setReportHistory((current) => [
          {
            id: uniqueUIID("export"),
            dataset,
            file_name: result.fileName,
            period: result.period,
            exported_at: new Date().toISOString(),
          },
          ...current,
        ].slice(0, 8));
      }
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("导出失败"));
    } finally {
      setLoading(false);
    }
  }
}

const identityProviderIconOptions = [
  "auto",
  "gitlab",
  "github",
  "google",
  "microsoft",
  "okta",
  "keycloak",
  "oidc",
  "oauth2",
  "saml",
  "ldap",
  "sso",
];

type IdentityProviderEndpointDefaults = {
  authorize_url?: string;
  token_url?: string;
  userinfo_url?: string;
};

type IdentityProviderTemplate = {
  key: string;
  label: string;
  providerType: "oidc" | "oauth2";
  iconKey: string;
  loginLabel: string;
  issuerPlaceholder: string;
  defaultIssuer?: string;
  scopes: string;
  usernameClaim: string;
  emailClaim: string;
  teamClaim: string;
  endpoints?: (issuerURL: string) => IdentityProviderEndpointDefaults;
};

const identityProviderTemplates: IdentityProviderTemplate[] = [
  {
    key: "generic_oidc",
    label: "通用 OIDC",
    providerType: "oidc",
    iconKey: "oidc",
    loginLabel: "SSO",
    issuerPlaceholder: "https://sso.example.com",
    scopes: "openid, profile, email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "department",
  },
  {
    key: "gitlab",
    label: "GitLab",
    providerType: "oauth2",
    iconKey: "gitlab",
    loginLabel: "GitLab",
    issuerPlaceholder: "https://gitlab.example.com",
    scopes: "openid profile email read_user",
    usernameClaim: "username",
    emailClaim: "email",
    teamClaim: "department",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/oauth/authorize`,
      token_url: `${issuerURL}/oauth/token`,
      userinfo_url: `${issuerURL}/api/v4/user`,
    }) : {},
  },
  {
    key: "google",
    label: "Google",
    providerType: "oidc",
    iconKey: "google",
    loginLabel: "Google",
    issuerPlaceholder: "https://accounts.google.com",
    defaultIssuer: "https://accounts.google.com",
    scopes: "openid profile email",
    usernameClaim: "email",
    emailClaim: "email",
    teamClaim: "hd",
    endpoints: () => ({
      authorize_url: "https://accounts.google.com/o/oauth2/v2/auth",
      token_url: "https://oauth2.googleapis.com/token",
      userinfo_url: "https://openidconnect.googleapis.com/v1/userinfo",
    }),
  },
  {
    key: "microsoft",
    label: "Microsoft Entra ID",
    providerType: "oidc",
    iconKey: "microsoft",
    loginLabel: "Microsoft",
    issuerPlaceholder: "https://login.microsoftonline.com/{tenant}/v2.0",
    scopes: "openid profile email User.Read",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "department",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/oauth2/v2.0/authorize`,
      token_url: `${issuerURL}/oauth2/v2.0/token`,
      userinfo_url: "https://graph.microsoft.com/oidc/userinfo",
    }) : {},
  },
  {
    key: "okta",
    label: "Okta",
    providerType: "oidc",
    iconKey: "okta",
    loginLabel: "Okta",
    issuerPlaceholder: "https://company.okta.com/oauth2/default",
    scopes: "openid profile email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "groups",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/v1/authorize`,
      token_url: `${issuerURL}/v1/token`,
      userinfo_url: `${issuerURL}/v1/userinfo`,
    }) : {},
  },
  {
    key: "keycloak",
    label: "Keycloak",
    providerType: "oidc",
    iconKey: "keycloak",
    loginLabel: "Keycloak",
    issuerPlaceholder: "https://keycloak.example.com/realms/company",
    scopes: "openid profile email",
    usernameClaim: "preferred_username",
    emailClaim: "email",
    teamClaim: "groups",
    endpoints: (issuerURL) => issuerURL ? ({
      authorize_url: `${issuerURL}/protocol/openid-connect/auth`,
      token_url: `${issuerURL}/protocol/openid-connect/token`,
      userinfo_url: `${issuerURL}/protocol/openid-connect/userinfo`,
    }) : {},
  },
  {
    key: "custom_oauth2",
    label: "通用 OAuth2",
    providerType: "oauth2",
    iconKey: "oauth2",
    loginLabel: "OAuth2",
    issuerPlaceholder: "https://oauth.example.com",
    scopes: "profile, email",
    usernameClaim: "username",
    emailClaim: "email",
    teamClaim: "department",
  },
];

const identityProviderTemplateOptions = identityProviderTemplates.map((template) => template.key);

type LoginIdentityProviderIconComponent = React.ComponentType<{ size?: number }>;

function identityProviderLoginURL(baseURL: string, provider: LoginIdentityProvider, returnURL: string) {
  const target = new URL(`${baseURL.replace(/\/$/, "")}/api/admin/auth/oauth/start`);
  target.searchParams.set("id", provider.id);
  target.searchParams.set("return_url", returnURL);
  return target.toString();
}

function currentOAuthReturnURL() {
  if (typeof window === "undefined") return viewRoutes.overview;
  return `${window.location.origin}${viewRoutes.overview}`;
}

function loginIdentityProviderDisplayName(provider: LoginIdentityProvider) {
  if (provider.display_name) return provider.display_name;
  const iconKey = loginIdentityProviderIconKey(provider);
  const label = identityProviderIconLabel(iconKey);
  if (label !== "自动" && label !== "SSO" && label !== "OIDC" && label !== "OAuth2" && label !== "SAML" && label !== "LDAP") {
    return label;
  }
  return provider.name;
}

function LoginIdentityProviderIcon({ provider }: { provider: LoginIdentityProvider }) {
  const iconKey = loginIdentityProviderIconKey(provider);
  const iconConfig = loginIdentityProviderIconConfig(iconKey);
  const Icon = iconConfig.icon;
  return (
    <span className={`login-sso-icon ${iconConfig.key}`} aria-hidden="true">
      <Icon size={15} />
    </span>
  );
}

function loginIdentityProviderIconKey(provider: LoginIdentityProvider) {
  const configured = normalizedIdentityProviderIconKey(provider.icon_key);
  if (configured && configured !== "auto") return configured;
  const providerType = stringifyValue(provider.provider_type).trim().toLowerCase();
  const fingerprint = `${provider.name} ${provider.issuer_url ?? ""} ${providerType}`.toLowerCase();
  for (const key of ["gitlab", "github", "google", "microsoft", "azure", "entra", "okta", "keycloak"]) {
    if (fingerprint.includes(key)) {
      return key === "azure" || key === "entra" ? "microsoft" : key;
    }
  }
  return normalizedIdentityProviderIconKey(providerType) || "sso";
}

function normalizedIdentityProviderIconKey(value: string | undefined) {
  const normalized = stringifyValue(value).trim().toLowerCase().replace(/[^a-z0-9_-]/g, "");
  return identityProviderIconOptions.includes(normalized) ? normalized : "";
}

function normalizedIdentityProviderTemplateKey(value: string | undefined) {
  const normalized = stringifyValue(value).trim().toLowerCase().replace(/[^a-z0-9_-]/g, "");
  return identityProviderTemplates.some((template) => template.key === normalized) ? normalized : "";
}

function identityProviderTemplateByKey(value: string | undefined) {
  const normalized = normalizedIdentityProviderTemplateKey(value);
  return identityProviderTemplates.find((template) => template.key === normalized) ?? identityProviderTemplates[0];
}

function inferIdentityProviderTemplateKey(values: Record<string, string>) {
  const configured = normalizedIdentityProviderTemplateKey(values.provider_template);
  if (configured) return configured;
  const iconKey = normalizedIdentityProviderIconKey(values.icon_key);
  if (iconKey && identityProviderTemplates.some((template) => template.key === iconKey)) {
    return iconKey;
  }
  const fingerprint = `${values.name ?? ""} ${values.login_label ?? ""} ${values.issuer_url ?? ""}`.toLowerCase();
  for (const template of identityProviderTemplates) {
    if (template.key !== "generic_oidc" && template.key !== "custom_oauth2" && fingerprint.includes(template.key)) {
      return template.key;
    }
  }
  return stringsEqual(values.provider_type, "oauth2") ? "custom_oauth2" : "generic_oidc";
}

function stringsEqual(left: string | undefined, right: string) {
  return String(left ?? "").trim().toLowerCase() === right;
}

function normalizeIdentityProviderIssuer(value: string) {
  return value.trim().replace(/\/+$/, "");
}

function identityProviderEndpointDefaults(template: IdentityProviderTemplate, issuerURL: string) {
  return template.endpoints?.(normalizeIdentityProviderIssuer(issuerURL)) ?? {};
}

function applyIdentityProviderTemplate(values: Record<string, string>, templateKey: string, overwrite = true) {
  const template = identityProviderTemplateByKey(templateKey);
  const next: Record<string, string> = { ...values, provider_template: template.key };
  next.provider_type = template.providerType;
  next.icon_key = template.iconKey;
  if (template.defaultIssuer && (overwrite || !next.issuer_url)) next.issuer_url = template.defaultIssuer;
  const issuer = normalizeIdentityProviderIssuer(next.issuer_url || template.defaultIssuer || "");
  for (const [key, value] of Object.entries({
    login_label: template.loginLabel,
    scopes: template.scopes,
    username_claim: template.usernameClaim,
    email_claim: template.emailClaim,
    team_claim: template.teamClaim,
  })) {
    if (overwrite || !next[key]) next[key] = value;
  }
  const endpoints = identityProviderEndpointDefaults(template, issuer);
  for (const [key, value] of Object.entries(endpoints)) {
    if (value && (overwrite || !next[key])) next[key] = value;
  }
  return next;
}

function identityProviderInitialFormValues(values: Record<string, string>, createMode: boolean) {
  const templateKey = inferIdentityProviderTemplateKey(values);
  const next: Record<string, string> = createMode ? applyIdentityProviderTemplate(values, templateKey, false) : { ...values, provider_template: templateKey };
  if (!next.default_role) next.default_role = "user";
  if (!next.default_project_role) next.default_project_role = "developer";
  return next;
}

function updateIdentityProviderFormValue(values: Record<string, string>, key: string, value: string) {
  if (key === "provider_template") {
    return applyIdentityProviderTemplate(values, value, true);
  }
  const next = { ...values, [key]: value };
  if (key === "issuer_url") {
    const template = identityProviderTemplateByKey(next.provider_template || inferIdentityProviderTemplateKey(next));
    const previousEndpoints = identityProviderEndpointDefaults(template, values.issuer_url ?? "");
    const nextEndpoints = identityProviderEndpointDefaults(template, value);
    for (const endpointKey of ["authorize_url", "token_url", "userinfo_url"] as const) {
      if (!values[endpointKey] || values[endpointKey] === previousEndpoints[endpointKey]) {
        next[endpointKey] = nextEndpoints[endpointKey] ?? "";
      }
    }
  }
  return next;
}

function identityProviderTemplateLabel(templateKey: string) {
  return identityProviderTemplateByKey(templateKey).label;
}

function identityProviderTemplateHelp(template: IdentityProviderTemplate) {
  if (template.key === "generic_oidc") return "适合标准 OIDC 服务，填写 Issuer 后一般可自动发现端点。";
  if (template.key === "custom_oauth2") return "适合非标准 OAuth2 服务，需要确认授权、Token 和用户信息端点。";
  if (activeLanguage === "en") return `Best for ${tx(template.label)} enterprise apps; common endpoints and claims are prefilled.`;
  if (activeLanguage === "ja") return `${tx(template.label)} の企業アプリ向けです。一般的なエンドポイントと Claim を事前入力します。`;
  return `适合 ${template.label} 企业应用，常用端点和 Claim 已预置。`;
}

function GoogleBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <path fill="#4285f4" d="M22.6 12.2c0-.8-.1-1.6-.2-2.3H12v4.4h5.9c-.3 1.4-1.1 2.6-2.3 3.4v2.8h3.7c2.1-2 3.3-4.8 3.3-8.3z" />
      <path fill="#34a853" d="M12 23c3 0 5.5-1 7.3-2.6l-3.7-2.8c-1 .7-2.2 1.1-3.6 1.1-2.8 0-5.2-1.9-6.1-4.5H2.1V17C3.9 20.6 7.6 23 12 23z" />
      <path fill="#fbbc05" d="M5.9 14.2c-.2-.7-.4-1.4-.4-2.2s.1-1.5.4-2.2V7H2.1C1.4 8.5 1 10.2 1 12s.4 3.5 1.1 5l3.8-2.8z" />
      <path fill="#ea4335" d="M12 5.3c1.6 0 3.1.6 4.2 1.7l3.2-3.2C17.5 2 15 1 12 1 7.6 1 3.9 3.4 2.1 7l3.8 2.8C6.8 7.2 9.2 5.3 12 5.3z" />
    </svg>
  );
}

function GitLabBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <path fill="#fc6d26" d="M12 22 3.2 8.7h5.3L12 22z" />
      <path fill="#e24329" d="M3.2 8.7 4.8 3.9c.2-.6 1-.6 1.2 0l2.5 4.8H3.2z" />
      <path fill="#fca326" d="M12 22 20.8 8.7h-5.3L12 22z" />
      <path fill="#e24329" d="m20.8 8.7-1.6-4.8c-.2-.6-1-.6-1.2 0l-2.5 4.8h5.3z" />
      <path fill="#fc6d26" d="M8.5 8.7h7L12 22 8.5 8.7z" />
    </svg>
  );
}

function GitHubBrandIcon({ size = 15 }: { size?: number }) {
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <path
        fill="currentColor"
        d="M12 1.8C6.4 1.8 1.8 6.4 1.8 12c0 4.5 2.9 8.3 7 9.7.5.1.7-.2.7-.5v-1.8c-2.8.6-3.4-1.2-3.4-1.2-.5-1.1-1.1-1.4-1.1-1.4-.9-.6.1-.6.1-.6 1 0 1.6 1.1 1.6 1.1.9 1.6 2.4 1.1 2.9.9.1-.7.4-1.1.7-1.4-2.2-.3-4.6-1.1-4.6-5 0-1.1.4-2 1.1-2.8-.1-.3-.5-1.3.1-2.7 0 0 .9-.3 2.9 1.1.8-.2 1.7-.3 2.6-.3s1.8.1 2.6.3c2-1.4 2.9-1.1 2.9-1.1.6 1.4.2 2.4.1 2.7.7.8 1.1 1.7 1.1 2.8 0 3.9-2.4 4.7-4.6 5 .4.3.7.9.7 1.8v2.6c0 .3.2.6.7.5 4.1-1.4 7-5.2 7-9.7C22.2 6.4 17.6 1.8 12 1.8z"
      />
    </svg>
  );
}

function MicrosoftBrandIcon({ size = 15 }: { size?: number }) {
  const gap = 1.2;
  const cell = (24 - gap) / 2;
  return (
    <svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true">
      <rect x="0" y="0" width={cell} height={cell} fill="#f25022" />
      <rect x={cell + gap} y="0" width={cell} height={cell} fill="#7fba00" />
      <rect x="0" y={cell + gap} width={cell} height={cell} fill="#00a4ef" />
      <rect x={cell + gap} y={cell + gap} width={cell} height={cell} fill="#ffb900" />
    </svg>
  );
}

function loginIdentityProviderIconConfig(key: string): { key: string; icon: LoginIdentityProviderIconComponent } {
  switch (key) {
    case "gitlab":
      return { key, icon: GitLabBrandIcon };
    case "github":
      return { key, icon: GitHubBrandIcon };
    case "google":
      return { key, icon: GoogleBrandIcon };
    case "microsoft":
      return { key, icon: MicrosoftBrandIcon };
    case "okta":
      return { key, icon: UserRoundCheck };
    case "keycloak":
      return { key, icon: LockKeyhole };
    case "oidc":
      return { key, icon: Fingerprint };
    case "oauth2":
      return { key, icon: KeyRound };
    case "saml":
      return { key, icon: ShieldCheck };
    case "ldap":
      return { key, icon: Users };
    default:
      return { key: "sso", icon: ShieldCheck };
  }
}

function LoginView({
  loading,
  error,
  baseURL,
  identityProviders,
  oauthReturnURL,
  theme,
  onThemeToggle,
  onLogin,
}: {
  loading: boolean;
  error: string;
  baseURL: string;
  identityProviders: LoginIdentityProvider[];
  oauthReturnURL: string;
  theme: "light" | "dark";
  onThemeToggle: () => void;
  onLogin: (identity: string, password: string) => void;
}) {
  const [identity, setIdentity] = useState("");
  const [password, setPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onLogin(identity, password);
  }

  const ssoListClassName = [
    "login-sso-list",
    identityProviders.length > 1 ? "multi" : "",
    identityProviders.length > 1 ? `count-${Math.min(identityProviders.length, 3)}` : "",
  ].filter(Boolean).join(" ");

  return (
    <main className="login-shell" data-theme={theme}>
      <button className="login-theme-toggle" onClick={onThemeToggle} title={tx("切换主题")} type="button">
        {theme === "light" ? <Moon size={17} /> : <Sun size={17} />}
      </button>
      <section className="login-stage">
        <aside className="login-hero-panel" aria-label="TokenHub">
          <div className="login-hero-orbit" aria-hidden="true" />
          <div className="login-flow-scene">
            <svg className="login-flow-svg" viewBox="0 0 460 320" aria-hidden="true">
              <path id="login-key-flow-one" className="login-flow-link" d="M 92 84 C 150 84 165 138 208 151" />
              <path id="login-key-flow-two" className="login-flow-link" d="M 92 160 C 142 160 166 160 208 160" />
              <path id="login-key-flow-three" className="login-flow-link" d="M 92 236 C 150 236 165 182 208 169" />
              <path id="login-provider-flow-one" className="login-flow-link login-provider-flow" d="M 260 150 C 298 116 318 82 362 82" />
              <path id="login-provider-flow-two" className="login-flow-link login-provider-flow" d="M 260 160 C 302 160 320 160 362 160" />
              <path id="login-provider-flow-three" className="login-flow-link login-provider-flow" d="M 260 170 C 298 204 318 238 362 238" />
              {[
                ["#login-key-flow-one", "0s"],
                ["#login-key-flow-one", "-1.25s"],
                ["#login-key-flow-two", "-0.35s"],
                ["#login-key-flow-two", "-1.7s"],
                ["#login-key-flow-three", "-0.7s"],
                ["#login-key-flow-three", "-2.05s"],
                ["#login-provider-flow-one", "-0.1s"],
                ["#login-provider-flow-one", "-1.45s"],
                ["#login-provider-flow-two", "-0.65s"],
                ["#login-provider-flow-two", "-2s"],
                ["#login-provider-flow-three", "-1.1s"],
                ["#login-provider-flow-three", "-2.45s"],
              ].map(([path, begin], index) => (
                <circle className="login-flow-token" key={`${path}-${begin}-${index}`} r={4}>
                  <animateMotion dur="3.1s" begin={begin} repeatCount="indefinite">
                    <mpath href={path} />
                  </animateMotion>
                </circle>
              ))}
            </svg>

            <div className="login-key-stack" aria-hidden="true">
              <span className="login-key-chip key-one">
                <span className="login-key-icon">
                  <KeyRound size={13} strokeWidth={2.6} />
                </span>
                Key 01
              </span>
              <span className="login-key-chip key-two">
                <span className="login-key-icon">
                  <KeyRound size={13} strokeWidth={2.6} />
                </span>
                Key 02
              </span>
              <span className="login-key-chip key-three">
                <span className="login-key-icon">
                  <KeyRound size={13} strokeWidth={2.6} />
                </span>
                Key 03
              </span>
            </div>

            <div className="login-hub-node">
              <span className="login-logo-tile">
                <img src="/brand/tokenhub-logo.png" alt="" />
              </span>
              <span className="login-hub-copy">
                <strong>TokenHub</strong>
                <small>Gateway</small>
              </span>
            </div>

            <div className="login-provider-stack" aria-hidden="true">
              <span className="login-provider-node provider-one">
                <span className="login-provider-icon">
                  <Server size={14} strokeWidth={2.35} />
                </span>
                <strong>Provider A</strong>
                <span className="login-provider-bars">
                  <span />
                  <span />
                  <span />
                </span>
              </span>
              <span className="login-provider-node provider-two">
                <span className="login-provider-icon">
                  <Server size={14} strokeWidth={2.35} />
                </span>
                <strong>Provider B</strong>
                <span className="login-provider-bars">
                  <span />
                  <span />
                  <span />
                </span>
              </span>
              <span className="login-provider-node provider-three">
                <span className="login-provider-icon">
                  <Server size={14} strokeWidth={2.35} />
                </span>
                <strong>Provider C</strong>
                <span className="login-provider-bars">
                  <span />
                  <span />
                  <span />
                </span>
              </span>
            </div>
          </div>
          <div className="login-signal-strip" aria-hidden="true">
            <span />
            <span />
            <span />
          </div>
        </aside>

        <form className="login-card" onSubmit={submit}>
          <div className="login-card-head">
            <h1>{tx("登录控制台")}</h1>
          </div>
          <label className="field">
            <span>{tx("账号 / 邮箱")}</span>
            <input value={identity} onChange={(event) => setIdentity(event.target.value)} required />
          </label>
          <label className="field">
            <span>{tx("密码")}</span>
            <span className="password-field">
              <input
                value={password}
                type={passwordVisible ? "text" : "password"}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
              <button
                aria-label={passwordVisible ? tx("隐藏密码") : tx("显示密码")}
                className="password-toggle"
                onClick={() => setPasswordVisible((value) => !value)}
                type="button"
              >
                {passwordVisible ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </span>
          </label>
          <div className="login-helper-row">
            <span>
              <span className="login-checkmark">
                <Check size={13} />
              </span>
              {tx("保持登录")}
            </span>
            <button type="button">{tx("忘记密码？")}</button>
          </div>
          {error ? <div className="login-error">{error}</div> : null}
          <button className="button login-submit" disabled={loading} type="submit">
            {loading ? tx("登录中") : tx("登录控制台")}
          </button>
          {identityProviders.length > 0 ? (
            <>
              <div className="login-divider" aria-hidden="true">
                <span />
                <small>{tx("或")}</small>
                <span />
              </div>
              <div className={ssoListClassName}>
                {identityProviders.map((provider) => {
                  const displayName = loginIdentityProviderDisplayName(provider);
                  return (
                    <a
                      aria-disabled={loading}
                      aria-label={`${tx("使用")} ${displayName} ${tx("登录")}`}
                      className="login-sso-button"
                      href={identityProviderLoginURL(baseURL, provider, oauthReturnURL)}
                      key={provider.id}
                      onClick={(event) => {
                        if (loading) {
                          event.preventDefault();
                          return;
                        }
                        savePendingOAuthBaseURL(baseURL);
                      }}
                    >
                      <LoginIdentityProviderIcon provider={provider} />
                      <span className="login-sso-label">
                        {identityProviders.length > 1 ? displayName : `${tx("使用")} ${displayName} ${tx("登录")}`}
                      </span>
                    </a>
                  );
                })}
              </div>
            </>
          ) : null}
        </form>
      </section>
    </main>
  );
}

function ResetPasswordView({
  loading,
  error,
  theme,
  onThemeToggle,
  token,
  onReset,
}: {
  loading: boolean;
  error: string;
  theme: "light" | "dark";
  onThemeToggle: () => void;
  token: string;
  onReset: (token: string, password: string) => void;
}) {
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [passwordVisible, setPasswordVisible] = useState(false);
  const mismatch = confirmPassword !== "" && password !== confirmPassword;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (mismatch || password.length < 8) return;
    onReset(token, password);
  }

  return (
    <main className="login-shell" data-theme={theme}>
      <button className="login-theme-toggle" onClick={onThemeToggle} title={tx("切换主题")} type="button">
        {theme === "light" ? <Moon size={17} /> : <Sun size={17} />}
      </button>
      <section className="login-stage">
        <form className="login-card" onSubmit={submit}>
          <div className="login-card-head">
            <h1>{tx("重置密码")}</h1>
            <p>{tx("请设置新的控制台登录密码。")}</p>
          </div>
          <label className="field">
            <span>{tx("新密码")}</span>
            <span className="password-field">
              <input
                minLength={8}
                value={password}
                type={passwordVisible ? "text" : "password"}
                onChange={(event) => setPassword(event.target.value)}
                required
              />
              <button
                aria-label={passwordVisible ? tx("隐藏密码") : tx("显示密码")}
                className="password-toggle"
                onClick={() => setPasswordVisible((value) => !value)}
                type="button"
              >
                {passwordVisible ? <EyeOff size={16} /> : <Eye size={16} />}
              </button>
            </span>
          </label>
          <label className="field">
            <span>{tx("确认新密码")}</span>
            <input
              minLength={8}
              value={confirmPassword}
              type={passwordVisible ? "text" : "password"}
              onChange={(event) => setConfirmPassword(event.target.value)}
              required
            />
          </label>
          {password !== "" && password.length < 8 ? <div className="login-error">{tx("密码至少 8 位")}</div> : null}
          {mismatch ? <div className="login-error">{tx("两次输入的密码不一致")}</div> : null}
          {error ? <div className="login-error">{error}</div> : null}
          <button className="button login-submit" disabled={loading || mismatch || password.length < 8} type="submit">
            {loading ? tx("提交中") : tx("重置密码")}
          </button>
        </form>
      </section>
    </main>
  );
}

function Sidebar({
  activeView,
  onSelect,
  user,
  onLogout,
  collapsed,
  onToggleCollapse,
  openGroups,
  onToggleGroup,
}: {
  activeView: ViewKey;
  onSelect: (view: ViewKey) => void;
  user: AdminUser;
  onLogout: () => void;
  collapsed: boolean;
  onToggleCollapse: () => void;
  openGroups: Record<string, boolean>;
  onToggleGroup: (title: string) => void;
}) {
  const visibleGroups = navGroupsForUser(user)
    .map((group) => ({ ...group, items: group.items.map((item) => filterNavItemByAccess(item, user)).filter((item): item is NavItem => Boolean(item)) }))
    .filter((group) => group.items.length > 0);
  return (
    <aside className={collapsed ? "sidebar collapsed" : "sidebar"}>
      <div className="brand">
        <img src="/brand/tokenhub-logo.png" alt="TokenHub" className="brand-logo" />
        <span className="brand-name">TokenHub</span>
        <span className="version">v0.3.0</span>
        <button
          className="sidebar-toggle"
          aria-label={collapsed ? tx("展开菜单") : tx("折叠菜单")}
          onClick={onToggleCollapse}
          title={collapsed ? tx("展开菜单") : tx("折叠菜单")}
          type="button"
        >
          {collapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
        </button>
      </div>
      <div className="sidebar-nav-scroll">
        {visibleGroups.map((group) => {
          const groupOpen = collapsed || openGroups[group.title] !== false;
          return (
            <div className={groupOpen ? "nav-group" : "nav-group closed"} key={group.title}>
              <button
                aria-expanded={groupOpen}
                className={groupOpen ? "nav-title" : "nav-title closed"}
                onClick={() => onToggleGroup(group.title)}
                type="button"
              >
                <span>{tx(group.title)}</span>
                <ChevronDown className="nav-chevron" size={14} />
              </button>
              {groupOpen ? (
                <div className="nav">
                  {group.items.map((item) => {
                    const Icon = item.icon;
                    if (isNavParentItem(item)) {
                      if (collapsed) {
                        return item.children.map((child) => {
                          const ChildIcon = child.icon;
                          return (
                            <button
                              className={activeView === child.view ? "nav-item active" : "nav-item"}
                              key={child.view}
                              onClick={() => onSelect(child.view)}
                              title={tx(child.label)}
                              type="button"
                            >
                              <ChildIcon size={17} />
                              <span>{tx(child.label)}</span>
                            </button>
                          );
                        });
                      }
	                      const childOpen = openGroups[`nav:${item.label}`] !== false || isNavItemActive(item, activeView);
                      return (
                        <div className={childOpen ? "nav-branch" : "nav-branch closed"} key={item.label}>
                          <button
                            aria-expanded={childOpen}
                            className={isNavItemActive(item, activeView) ? "nav-item nav-parent active-parent" : "nav-item nav-parent"}
                            onClick={() => onToggleGroup(`nav:${item.label}`)}
                            type="button"
                          >
                            <Icon size={17} />
	                            <span>{tx(item.label)}</span>
                            <ChevronDown className="nav-chevron" size={14} />
                          </button>
                          {childOpen ? (
                            <div className="nav-subnav">
                              {item.children.map((child) => {
                                const ChildIcon = child.icon;
                                return (
                                  <button
                                    className={activeView === child.view ? "nav-item nav-child active" : "nav-item nav-child"}
                                    key={child.view}
                                    onClick={() => onSelect(child.view)}
                                    type="button"
                                  >
                                    <ChildIcon size={16} />
	                                    <span>{tx(child.label)}</span>
                                  </button>
                                );
                              })}
                            </div>
                          ) : null}
                        </div>
                      );
                    }
                    return (
                      <button
                        className={activeView === item.view ? "nav-item active" : "nav-item"}
                        key={item.view}
                        onClick={() => onSelect(item.view)}
	                        title={collapsed ? tx(item.label) : undefined}
                        type="button"
                      >
                        <Icon size={17} />
	                        <span>{tx(item.label)}</span>
                      </button>
                    );
                  })}
                </div>
              ) : null}
            </div>
          );
        })}
      </div>
      <div className="sidebar-account">
        <div className="account-avatar">{userInitial(user)}</div>
        <div className="account-meta">
          <strong>{displayText(user.name) || user.username}</strong>
          <span>{roleLabel(user.role)}</span>
        </div>
        <button className="account-logout" onClick={onLogout} type="button" title={tx("退出登录")}>
          <LogOut size={15} />
        </button>
      </div>
    </aside>
  );
}

function PageHeader({
  activeView,
  data,
  meta,
  user,
}: {
  activeView: ViewKey;
  data: AppData;
  meta: { title: string; description: string; eyebrow?: string };
  user: AdminUser;
}) {
  const path = navPathForView(user, activeView);
  const chips = pageHeaderChips(activeView, data, user);
  const pathGroup = path.group || pageHeaderFallbackGroup(activeView, user);
  const pathSegments = ["TokenHub", pathGroup, path.parent, path.label || meta.title].filter(Boolean);
  return (
    <header className="page-header page-context-header">
      <div className="page-context-main">
        <div className="page-breadcrumb" aria-label={tx("当前位置")}>
          {pathSegments.map((segment, index) => (
            <Fragment key={`${segment}-${index}`}>
              {index > 0 ? <ChevronRight aria-hidden="true" className="page-breadcrumb-separator" size={13} /> : null}
              <span className={index === pathSegments.length - 1 ? "current" : undefined}>
                {tx(segment)}
              </span>
            </Fragment>
          ))}
        </div>
      </div>
      <div className="page-context-side">
        <span className="scope-chip">{tx(roleScopeDescription(user))}</span>
        {chips.map((chip) => (
          <span className="page-context-chip" key={chip.label}>
            <strong>{chip.value}</strong>
            <em>{tx(chip.label)}</em>
          </span>
        ))}
      </div>
    </header>
  );
}

function StatusStack({
  error,
  notice,
  onClearError,
  onClearNotice,
}: {
  error: string;
  notice: string;
  onClearError: () => void;
  onClearNotice: () => void;
}) {
  if (!error && !notice) return null;
  return (
    <div className="status-stack">
      {error ? (
        <div className="status-line error">
          <AlertCircle size={16} />
          <span>{error}</span>
          <button className="icon-button subtle" onClick={onClearError} title={tx("关闭")} type="button">
            <X size={14} />
          </button>
        </div>
      ) : null}
      {notice ? (
        <div className="status-line success">
          <Check size={16} />
          <span>{notice}</span>
          <button className="icon-button subtle" onClick={onClearNotice} title={tx("关闭")} type="button">
            <X size={14} />
          </button>
        </div>
      ) : null}
    </div>
  );
}

function navPathForView(user: AdminUser, view: ViewKey) {
  for (const group of navGroupsForUser(user)) {
    for (const item of group.items) {
      const filtered = filterNavItemByAccess(item, user);
      if (!filtered) continue;
      if (isNavParentItem(filtered)) {
        const child = filtered.children.find((entry) => entry.view === view);
        if (child) return { group: group.title, parent: filtered.label, label: child.label };
      } else if (filtered.view === view) {
        return { group: group.title, parent: "", label: filtered.label };
      }
    }
  }
  return { group: "", parent: "", label: standaloneViewMeta[view]?.title ?? view };
}

function pageHeaderFallbackGroup(view: ViewKey, user: AdminUser) {
  if (view === "gateway") {
    const role = appRole(user.role);
    if (role === "team_leader") return "团队管理";
    if (role === "user") return "开始使用";
    return "接入参考";
  }
  return "";
}

function pageHeaderChips(view: ViewKey, data: AppData, user: AdminUser) {
  const role = appRole(user.role);
  switch (view) {
    case "providers":
      return [
        { label: "健康 Provider", value: `${data.providers.filter((item) => item.healthy).length}/${data.providers.length}` },
        { label: "资源实例", value: formatNumber(data.providerResources.length) },
      ];
    case "models":
      return [
        { label: "可用模型", value: formatNumber(playgroundModels(data).length || data.models.length) },
        { label: "启用路由", value: formatNumber(data.summary.active_route_count || data.routes.filter((item) => item.status === "active").length) },
      ];
    case "routes":
      return [
        { label: "启用路由", value: formatNumber(data.routes.filter((item) => item.status === "active").length) },
        { label: "Provider", value: formatNumber(data.providers.length) },
      ];
    case "projects":
      return [
        { label: "项目", value: formatNumber(data.projects.length) },
        { label: role === "team_leader" ? "团队成员" : "用户", value: formatNumber(data.users.length) },
      ];
    case "api-keys":
      return [
        { label: "Key", value: formatNumber(data.keys.length) },
        { label: "项目", value: formatNumber(data.projects.length) },
      ];
    case "usage":
    case "billing":
      return [
        { label: "请求", value: formatNumber(data.summary.request_count) },
        { label: "成本", value: `$${formatMoney(data.summary.estimated_cost_usd)}` },
      ];
    case "audit":
      return [
        { label: "请求日志", value: formatNumber(data.logs.length) },
        { label: "错误请求", value: formatNumber(data.summary.errors) },
      ];
    default:
      return [{ label: "记录", value: formatNumber(pageRecordCount(view, data)) }];
  }
}

function pageRecordCount(view: ViewKey, data: AppData) {
  if (resourceConfigs[view]) return resourceConfigs[view].list(data).length;
  if (view === "alert-events") return data.alerts.length;
  if (view === "alert-deliveries") return data.alertDeliveries.length;
  if (view === "approvals") return data.approvals.length;
  return 0;
}

function TopNav({
  activeView,
  data,
  user,
  theme,
  onSelectView,
  onThemeToggle,
}: {
  activeView: ViewKey;
  data: AppData;
  user: AdminUser;
  theme: "light" | "dark";
  onSelectView: (view: ViewKey) => void;
  onThemeToggle: () => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [recentViews, setRecentViews] = useState<ViewKey[]>(() => readRecentViews());
  const searchItems = topSearchItemsForUser(user, data);
  const normalizedQuery = normalizeSearchText(query);
  const results = topSearchResults(searchItems, appRole(user.role), normalizedQuery, recentViews);
  const showResults = open && (query.trim().length > 0 || results.length > 0);
  const quickActions = topQuickActionsForUser(user);

  useEffect(() => {
    function handleShortcut(event: KeyboardEvent) {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        inputRef.current?.focus();
        setOpen(true);
      }
    }
    window.addEventListener("keydown", handleShortcut);
    return () => window.removeEventListener("keydown", handleShortcut);
  }, []);

  useEffect(() => {
    setRecentViews(readRecentViews());
  }, [activeView]);

  function openResult(view: ViewKey) {
    setQuery("");
    setOpen(false);
    onSelectView(view);
  }

  function handleSearchKeyDown(event: React.KeyboardEvent<HTMLInputElement>) {
    if (event.key === "Escape") {
      setQuery("");
      setOpen(false);
      inputRef.current?.blur();
      return;
    }
    if (event.key === "Enter" && results[0]) {
      event.preventDefault();
      openResult(results[0].view);
    }
  }

  return (
    <header className="topbar">
      <div className="top-search-wrap">
        <label className={showResults ? "top-search active" : "top-search"} aria-label={tx("搜索控制台")}>
          <Search size={16} />
          <input
            ref={inputRef}
            value={query}
            onBlur={() => window.setTimeout(() => setOpen(false), 120)}
            onChange={(event) => {
              setQuery(event.target.value);
              setOpen(true);
            }}
            onFocus={() => setOpen(true)}
            onKeyDown={handleSearchKeyDown}
            placeholder={tx("搜索模型、Provider、日志...")}
          />
          <span>⌘K</span>
        </label>
        {showResults ? (
          <div className="top-search-panel" role="listbox" aria-label={tx("搜索结果")} onMouseDown={(event) => event.preventDefault()}>
            {results.length > 0 ? (
              results.map((item) => {
                const Icon = item.icon;
                return (
                  <button
                    className={item.view === activeView ? "top-search-result active" : "top-search-result"}
                    key={item.id}
                    onClick={() => openResult(item.view)}
                    role="option"
                    type="button"
                    aria-selected={item.view === activeView}
                  >
                    <span className={`top-search-result-icon ${item.tone ?? "page"}`}>
                      <Icon size={16} />
                    </span>
                    <span className="top-search-result-body">
                      <strong>{tx(item.label)}</strong>
                      <small>{tx(item.group)} · {tx(item.description)}</small>
                    </span>
                    <span className="top-search-result-action">{tx("打开")}</span>
                  </button>
                );
              })
            ) : (
              <div className="top-search-empty">
                <strong>{tx("没有找到匹配入口")}</strong>
                <span>{tx("请尝试搜索模型、Key、用量、日志或设置。")}</span>
              </div>
            )}
          </div>
        ) : null}
      </div>
      <div className="topbar-spacer" />
      <div className="topbar-actions">
        <div className="top-quick-actions" aria-label={tx("常用操作")}>
          {quickActions.map((item) => {
            const Icon = item.icon;
            return (
              <button
                className={item.view === activeView ? "top-icon-button active" : "top-icon-button"}
                key={item.view}
                onClick={() => openResult(item.view)}
                title={tx(item.label)}
                type="button"
              >
                <Icon size={17} />
              </button>
            );
          })}
        </div>
        <div className="top-scope-pill" title={tx(roleScopeDescription(user))}>
          <span>{userInitial(user)}</span>
          <strong>{roleLabel(user.role)}</strong>
        </div>
        <button className="top-icon-button" onClick={onThemeToggle} title={tx("切换主题")} type="button">
          {theme === "light" ? <Moon size={17} /> : <Sun size={17} />}
        </button>
      </div>
    </header>
  );
}

function OverviewView({
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

function RoleUsageMonitorDashboard({
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

function UsageMonitorKPI({
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

function UsageMonitorTrafficChart({ points }: { points: UsagePoint[] }) {
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

function UsageHealthTimeline({ logs }: { logs: RequestLog[] }) {
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

function TokenMixPanel({ input, output, total }: { input: number; output: number; total: number }) {
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

function UsageCostRankPanel({ title, empty, rows }: { title: string; empty: string; rows: UsageDashboardRankRow[] }) {
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

function RecentFailurePanel({ logs, data }: { logs: RequestLog[]; data: AppData }) {
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

type OverviewWorkbenchItem = {
  title: string;
  description: string;
  status: "ready" | "attention" | "next";
  statusLabel: string;
  target: ViewKey;
  action: string;
  icon: typeof Activity;
};

function OverviewRoleWorkbench({
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

function overviewWorkbenchStorageKey(user: AdminUser, role: AppRole) {
  const userKey = user.id || user.username || user.email || "anonymous";
  return `tokenhub.overview.workbench.v1.${role}.${userKey}`;
}

function overviewWorkbenchInitialPreference(storageKey: string, setupComplete: boolean): "show" | "hide" {
  if (typeof window === "undefined") return setupComplete ? "hide" : "show";
  if (setupComplete) {
    window.localStorage.setItem(storageKey, "completed");
    return "hide";
  }
  if (window.localStorage.getItem(storageKey)) return "hide";
  window.localStorage.setItem(storageKey, "shown");
  return "show";
}

function overviewRoleEyebrow(role: AppRole) {
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

function overviewRoleTitle(role: AppRole) {
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

function overviewRoleSummary(role: AppRole) {
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

function overviewSetupScore(role: AppRole, stats: OverviewWorkbenchStats) {
  const checks = overviewReadinessChecks(role, stats);
  const ready = checks.filter(Boolean).length;
  return {
    ready,
    total: checks.length,
    label: ready === checks.length ? "关键路径已就绪" : "仍有待处理项",
  };
}

type OverviewWorkbenchStats = {
  projects: number;
  activeProviders: number;
  healthyProviders: number;
  activeRoutes: number;
  apiKeys: number;
  callableModels: number;
  requestCount: number;
};

function overviewReadinessChecks(role: AppRole, stats: OverviewWorkbenchStats) {
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

function overviewWorkbenchItems(role: AppRole, stats: OverviewWorkbenchStats): OverviewWorkbenchItem[] {
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

type OverviewRangeKey = "7d" | "30d" | "month";
type OverviewMetricKey = "requests" | "tokens" | "cost";

const overviewRangeTabs: Array<{ key: OverviewRangeKey; label: string }> = [
  { key: "7d", label: "7 天" },
  { key: "30d", label: "30 天" },
  { key: "month", label: "本月" },
];

const overviewMetricTabs: Array<{ key: OverviewMetricKey; label: string }> = [
  { key: "requests", label: "请求" },
  { key: "tokens", label: "Token" },
  { key: "cost", label: "成本" },
];

function OverviewMetricCard({
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

function OverviewSparkline({ values }: { values: number[] }) {
  const path = overviewLinePath(values, 160, 34, 3);
  return (
    <svg className="overview-sparkline" viewBox="0 0 160 34" preserveAspectRatio="none" aria-hidden="true">
      <path d={path} />
    </svg>
  );
}

function OverviewTrendChart({ metric, points }: { metric: OverviewMetricKey; points: UsagePoint[] }) {
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

function OverviewProviderShare({ rows }: { rows: Array<{ id: string; label: string; percent: number; value: number; cost: number }> }) {
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

function OverviewTopModels({ rows }: { rows: UsageBreakdownRow[] }) {
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

function overviewRangePoints(data: AppData, range: OverviewRangeKey) {
  const source = (data.timeseries.length ? data.timeseries : fallbackOverviewDays(data.summary))
    .slice()
    .sort((left, right) => left.date.localeCompare(right.date));
  if (range === "7d") return source.slice(-7);
  if (range === "30d") return source.slice(-30);
  const latestMonth = source.at(-1)?.date.slice(0, 7);
  const monthPoints = latestMonth ? source.filter((point) => point.date.startsWith(latestMonth)) : source;
  return monthPoints.length ? monthPoints : source.slice(-30);
}

function fallbackOverviewDays(summary: Summary): UsagePoint[] {
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

function usagePointMetric(point: UsagePoint, metric: OverviewMetricKey) {
  if (metric === "tokens") return point.total_tokens;
  if (metric === "cost") return point.estimated_cost_usd;
  return point.request_count;
}

function overviewMetricValue(points: UsagePoint[], metric: OverviewMetricKey) {
  const values = points.map((point) => usagePointMetric(point, metric));
  const total = values.reduce((sum, value) => sum + value, 0);
  return {
    value: overviewMetricDisplay(total, metric),
    delta: overviewDeltaLabel(values),
  };
}

function overviewMetricDisplay(value: number, metric: OverviewMetricKey) {
  if (metric === "cost") return `$${formatMoney(value)}`;
  if (metric === "tokens") return compactNumber(value);
  return formatNumber(Math.round(value || 0));
}

function overviewDeltaLabel(values: number[]) {
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

function overviewRangeLabel(range: OverviewRangeKey) {
  if (range === "7d") return tx("近 7 天");
  if (range === "30d") return tx("近 30 天");
  return tx("本月");
}

function overviewLinePath(values: number[], width: number, height: number, pad = 0, offsetX = 0, offsetY = 0) {
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

function overviewAxisLabels(points: UsagePoint[]) {
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

function overviewDateLabel(date: string) {
  const [, , month, day] = date.match(/^(\d{4})-(\d{2})-(\d{2})/) ?? [];
  return month && day ? `${month}/${day}` : date;
}

function overviewProviderShareRows(data: AppData) {
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

function overviewDonutGradient(rows: Array<{ percent: number }>) {
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

function overviewTopModelRows(data: AppData) {
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

type UsageDashboardRankRow = UsageBreakdownRow & {
  label: string;
  cost: number;
};

function roleUsageMonitorStats(data: AppData) {
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

function usageDashboardCostRows(rows: UsageBreakdownRow[], labelFor: (row: UsageBreakdownRow) => string): UsageDashboardRankRow[] {
  return rows
    .filter((row) => hasUsage(row))
    .map((row) => ({ ...row, label: labelFor(row), cost: row.estimated_cost_usd }))
    .sort((left, right) => right.cost - left.cost || right.total_tokens - left.total_tokens || right.request_count - left.request_count);
}

function usageDashboardAccountRows(data: AppData): UsageDashboardRankRow[] {
  const resourceRows = usageDashboardCostRows(data.breakdown.provider_resources ?? [], (row) => providerResourceAuditLabel(data, row.id));
  if (resourceRows.length) return resourceRows;
  return usageDashboardCostRows(data.breakdown.providers ?? [], (row) => findProvider(data, row.id)?.name || row.id);
}

function modelDisplayName(data: AppData, modelID: string) {
  const model = data.models.find((item) => item.name === modelID || item.id === modelID);
  return model?.name || modelID || "-";
}

function requestLogFailed(log: RequestLog) {
  return log.status_code >= 400 || Boolean(log.error_code);
}

function latencyDisplay(value: number) {
  if (!value) return "-";
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10_000 ? 1 : 2)}s`;
  return `${Math.round(value)}ms`;
}

function usageMonitorRequestLine(points: UsagePoint[], maxRequests: number) {
  if (!points.length) return "";
  return points
    .map((point, index) => {
      const x = 58 + index * (points.length <= 1 ? 0 : 620 / (points.length - 1));
      const y = 220 - (point.request_count / Math.max(maxRequests, 1)) * 168;
      return `${index === 0 ? "M" : "L"} ${x.toFixed(2)} ${y.toFixed(2)}`;
    })
    .join(" ");
}

function usageHealthGridSize(width: number, height: number) {
  const cellSize = 6;
  const gap = 3;
  return {
    rows: clampInt(Math.floor((Math.max(height, 120) + gap) / (cellSize + gap)), 7, 28),
    columns: clampInt(Math.floor((Math.max(width, 180) + gap) / (cellSize + gap)), 24, 120),
  };
}

function clampInt(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, Number.isFinite(value) ? value : min));
}

function usageHealthCells(logs: RequestLog[], cellCount: number) {
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

function providerFailureLabel(data: AppData, log: RequestLog) {
  if (log.provider_resource_id) return providerResourceAuditLabel(data, log.provider_resource_id);
  if (log.provider_id) return findProvider(data, log.provider_id)?.name || log.provider_id;
  return "-";
}

function GatewayView({
  api,
  data,
  user,
  language,
  onLanguageChange,
}: {
  api: ApiContext;
  data: AppData;
  user: AdminUser;
  language: AppLanguage;
  onLanguageChange: (language: AppLanguage) => void;
}) {
  const baseURL = apiGatewayBaseURL(api.baseURL);
  const activeRoutes = data.routes.filter((route) => route.status === "active").length;
  const callableModels = playgroundModels(data);
  const sampleModel = callableModels.find((model) => activeRouteCount(model.name, data) > 0)?.name ?? callableModels[0]?.name ?? "gpt-4.1-mini";
  const keyHint = data.keys[0] ? `${data.keys[0].key_prefix}...${data.keys[0].key_suffix}` : "YOUR_TOKENHUB_API_KEY";
  const docBundle = gatewayDocBundle({ language, baseURL, keyHint, sampleModel, activeRoutes, data, callableModels, role: appRole(user.role) });
  const [activeDocID, setActiveDocID] = useState(docBundle.defaultDocID ?? "quickstart");
  const allDocs = docBundle.groups.flatMap((group) => group.items);
  const activeDoc = allDocs.find((item) => item.id === activeDocID) ?? allDocs[0]!;
  const defaultDocID = docBundle.defaultDocID ?? allDocs[0]?.id ?? "quickstart";

  useEffect(() => {
    if (!allDocs.some((item) => item.id === activeDocID)) {
      setActiveDocID(defaultDocID);
    }
  }, [activeDocID, allDocs, defaultDocID]);

  return (
    <div className="gateway-docs">
      <div className="api-doc-shell">
        <GatewayDocNav groups={docBundle.groups} activeID={activeDoc.id} onSelect={setActiveDocID} ui={docBundle.nav} />
        <section className="api-doc-main">
          <header className="api-doc-main-head">
            <div>
              <p className="eyebrow">{docBundle.eyebrow}</p>
              <h2>{docBundle.title}</h2>
              <p>{docBundle.description}</p>
            </div>
            <div className="api-doc-language-switcher" aria-label={docBundle.languageLabel}>
              {languageOptions.map((option) => (
                <button
                  aria-pressed={language === option.value}
                  className={language === option.value ? "active" : ""}
                  key={option.value}
                  onClick={() => onLanguageChange(option.value)}
                  type="button"
                >
                  {gatewayLanguageLabel(option.value)}
                </button>
              ))}
            </div>
          </header>

          <section className="api-doc-quick-grid" aria-label={docBundle.quickInfoLabel}>
            <GatewayCopyCard label={docBundle.quickCards.baseURL} value={baseURL} />
            <GatewayCopyCard label={docBundle.quickCards.authorization} value={`Bearer ${keyHint}`} />
            <GatewayCopyCard label={docBundle.quickCards.sampleModel} value={sampleModel} />
            <article className="gateway-copy-card api-doc-config-card">
              <span>{docBundle.quickCards.currentConfig}</span>
              <strong>{docBundle.quickCards.activeRoutes}</strong>
              <small>{docBundle.quickCards.apiKeys}</small>
            </article>
          </section>

          <GatewayDocContent doc={activeDoc} />
        </section>
      </div>
    </div>
  );
}

type GatewayDocTable = {
  title: string;
  columns: string[];
  rows: React.ReactNode[][];
};

type GatewayDocItem = {
  id: string;
  group: string;
  title: string;
  description: string;
  badge?: string;
  method?: string;
  path?: string;
  details?: Array<{ label: string; value: string }>;
  notesTitle?: string;
  notes?: string[];
  params?: GatewayDocTable;
  table?: GatewayDocTable;
  examplesTitle?: string;
  examples?: Array<{ title: string; code: string }>;
};

type GatewayDocGroup = {
  title: string;
  items: GatewayDocItem[];
};

type GatewayDocNavCopy = {
  title: string;
  subtitle: string;
  searchPlaceholder: string;
  noResults: string;
};

type GatewayDocBundle = {
  defaultDocID?: string;
  nav: GatewayDocNavCopy;
  eyebrow: string;
  title: string;
  description: string;
  languageLabel: string;
  quickInfoLabel: string;
  quickCards: {
    baseURL: string;
    authorization: string;
    sampleModel: string;
    currentConfig: string;
    activeRoutes: string;
    apiKeys: string;
  };
  groups: GatewayDocGroup[];
};

function GatewayDocNav({
  groups,
  activeID,
  onSelect,
  ui,
}: {
  groups: GatewayDocGroup[];
  activeID: string;
  onSelect: (id: string) => void;
  ui: GatewayDocNavCopy;
}) {
  const [searchText, setSearchText] = useState("");
  const normalizedSearch = searchText.trim().toLowerCase();
  const visibleGroups = normalizedSearch
    ? groups
      .map((group) => ({
        ...group,
        items: group.items.filter((item) =>
          [group.title, item.title, item.description, item.path, item.badge, item.group]
            .filter(Boolean)
            .join(" ")
            .toLowerCase()
            .includes(normalizedSearch),
        ),
      }))
      .filter((group) => group.items.length > 0)
    : groups;

  return (
    <aside className="api-doc-nav" aria-label={ui.title}>
      <div className="api-doc-nav-head">
        <FileText size={16} />
        <div>
          <strong>{ui.title}</strong>
          <span>{ui.subtitle}</span>
        </div>
      </div>
      <label className="api-doc-search">
        <Search size={15} />
        <input
          value={searchText}
          onChange={(event) => setSearchText(event.target.value)}
          placeholder={ui.searchPlaceholder}
        />
      </label>
      <div className="api-doc-nav-list">
        {visibleGroups.length ? visibleGroups.map((group) => (
          <section className="api-doc-nav-group" key={group.title}>
            <h3>{group.title}</h3>
            {group.items.map((item) => (
              <button
                aria-selected={activeID === item.id}
                className={activeID === item.id ? "api-doc-nav-item active" : "api-doc-nav-item"}
                key={item.id}
                onClick={() => onSelect(item.id)}
                type="button"
              >
                {item.method ? <span className={`api-method ${apiMethodClass(item.method)}`}>{item.method}</span> : <span className="api-method muted">{item.badge ?? "DOC"}</span>}
                <span>
                  <strong>{item.title}</strong>
                  {item.path ? <em>{item.path}</em> : <em>{item.description}</em>}
                </span>
              </button>
            ))}
          </section>
        )) : <div className="api-doc-empty">{ui.noResults}</div>}
      </div>
    </aside>
  );
}

function GatewayDocContent({ doc }: { doc: GatewayDocItem }) {
  return (
    <article className="api-doc-content-card">
      <GatewayDocTitle doc={doc} />
      {doc.details ? (
        <div className="api-doc-detail-grid">
          {doc.details.map((item) => (
            <div key={item.label}>
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </div>
          ))}
        </div>
      ) : null}
      {doc.notes ? (
        <section className="api-doc-panel">
          <h3>{doc.notesTitle ?? "Notes"}</h3>
          <ul className="api-doc-notes">
            {doc.notes.map((note) => <li key={note}>{note}</li>)}
          </ul>
        </section>
      ) : null}
      {doc.params ? (
        <section className="api-doc-panel">
          <h3>{doc.params.title}</h3>
          <SimpleTable columns={doc.params.columns} rows={doc.params.rows} />
        </section>
      ) : null}
      {doc.table ? (
        <section className="api-doc-panel">
          <h3>{doc.table.title}</h3>
          <SimpleTable columns={doc.table.columns} rows={doc.table.rows} />
        </section>
      ) : null}
      {doc.examples ? (
        <section className="api-doc-panel">
          <h3>{doc.examplesTitle ?? "Examples"}</h3>
          <div className="api-doc-code-grid">
            {doc.examples.map((example) => (
              <div className="api-doc-code-card" key={example.title}>
                <strong>{example.title}</strong>
                <GatewayCodeBlock code={example.code} />
              </div>
            ))}
          </div>
        </section>
      ) : null}
    </article>
  );
}

function GatewayDocTitle({ doc }: { doc: GatewayDocItem }) {
  return (
    <div className="api-doc-title">
      <div>
        <span>{doc.group}</span>
        <h2>{doc.title}</h2>
        <p>{doc.description}</p>
      </div>
      {doc.path ? (
        <div className="api-doc-endpoint">
          <span className={`api-method ${apiMethodClass(doc.method)}`}>{doc.method}</span>
          <code>{doc.path}</code>
        </div>
      ) : null}
    </div>
  );
}

function gatewayDocBundle({
  language,
  baseURL,
  keyHint,
  sampleModel,
  activeRoutes,
  data,
  callableModels,
  role,
}: {
  language: AppLanguage;
  baseURL: string;
  keyHint: string;
  sampleModel: string;
  activeRoutes: number;
  data: AppData;
  callableModels: Model[];
  role: AppRole;
}): GatewayDocBundle {
  const authHeader = `Authorization: Bearer ${keyHint}`;
  const activeRouteCountValue = activeRoutes || data.summary.active_route_count || 0;
  const apiKeyCount = data.keys.length || data.summary.api_key_count || 0;
  const projectCount = data.projects.length;
  const userCount = data.users.length || data.summary.user_count || 0;
  const providerCount = data.providers.length;
  const routeCount = data.routes.length;
  const requestLogCount = data.logs.length;
  const visibleModelCount = callableModels.length;
  const chatCurl = `curl -X POST "${baseURL}/chat/completions" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${sampleModel}",
    "messages": [
      {"role": "system", "content": "You are an internal enterprise AI assistant."},
      {"role": "user", "content": "Introduce TokenHub in two concise sentences."}
    ],
    "temperature": 0.7,
    "stream": false
  }'`;

  const commonEN = gatewayEnglishDocs({
    baseURL,
    keyHint,
    authHeader,
    sampleModel,
    chatCurl,
    activeRouteCountValue,
    apiKeyCount,
    projectCount,
    userCount,
    providerCount,
    routeCount,
    requestLogCount,
    visibleModelCount,
  });
  if (role === "user" || role === "team_leader") {
    return gatewayLLMUsageDocs({
      language,
      role,
      baseURL,
      keyHint,
      authHeader,
      sampleModel,
      chatCurl,
      activeRouteCountValue,
      apiKeyCount,
      projectCount,
      userCount,
      providerCount,
      routeCount,
      requestLogCount,
      visibleModelCount,
    });
  }
  if (language === "zh-CN") return gatewayChineseDocs(commonEN);
  if (language === "ja") return gatewayJapaneseDocs(commonEN);
  return commonEN;
}

type GatewayDocStats = {
  baseURL: string;
  keyHint: string;
  authHeader: string;
  sampleModel: string;
  chatCurl: string;
  activeRouteCountValue: number;
  apiKeyCount: number;
  projectCount: number;
  userCount: number;
  providerCount: number;
  routeCount: number;
  requestLogCount: number;
  visibleModelCount: number;
};

function gatewayLLMUsageDocs({
  language,
  role,
  ...stats
}: GatewayDocStats & { language: AppLanguage; role: AppRole }): GatewayDocBundle {
  if (language === "zh-CN") return gatewayChineseLLMUsageDocs(stats, role);
  if (language === "ja") return gatewayJapaneseLLMUsageDocs(stats, role);
  return gatewayEnglishLLMUsageDocs(stats, role);
}

function gatewayListModelsCurl(stats: GatewayDocStats) {
  return `curl --request GET \\
  --url "${stats.baseURL}/models" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json"`;
}

function gatewayRetrieveModelCurl(stats: GatewayDocStats) {
  return `curl --request GET \\
  --url "${stats.baseURL}/models/${encodeURIComponent(stats.sampleModel)}" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json"`;
}

function gatewayStreamingCurl(stats: GatewayDocStats) {
  return `curl -N --request POST \\
  --url "${stats.baseURL}/chat/completions" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "${stats.sampleModel}",
    "messages": [
      {"role": "user", "content": "Stream a short release note for this model gateway."}
    ],
    "stream": true,
    "stream_options": {"include_usage": true}
  }'`;
}

function gatewayResponsesCurl(stats: GatewayDocStats) {
  return `curl --request POST \\
  --url "${stats.baseURL}/responses" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "${stats.sampleModel}",
    "input": "Summarize the weekly project status in three bullets."
  }'`;
}

function gatewayEmbeddingsCurl(stats: GatewayDocStats) {
  return `curl --request POST \\
  --url "${stats.baseURL}/embeddings" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "text-embedding-3-small",
    "input": "TokenHub enterprise knowledge base"
  }'`;
}

function gatewayOpenAISDKExample(stats: GatewayDocStats) {
  return `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: "${stats.baseURL}",
});

const completion = await client.chat.completions.create({
  model: "${stats.sampleModel}",
  messages: [
    { role: "user", content: "Write a concise onboarding checklist." },
  ],
});

console.log(completion.choices[0]?.message?.content);`;
}

function gatewayPythonSDKExample(stats: GatewayDocStats) {
  return `from openai import OpenAI

client = OpenAI(
    api_key="YOUR_TOKENHUB_API_KEY",
    base_url="${stats.baseURL}",
)

completion = client.chat.completions.create(
    model="${stats.sampleModel}",
    messages=[
        {"role": "user", "content": "Write a concise onboarding checklist."},
    ],
)

print(completion.choices[0].message.content)`;
}

function gatewayEnglishLLMUsageDocs(stats: GatewayDocStats, role: AppRole): GatewayDocBundle {
  const teamLeader = role === "team_leader";
  return {
    defaultDocID: "quickstart",
    nav: {
      title: "API Documentation",
      subtitle: "OpenAI-compatible LLM usage",
      searchPlaceholder: "Search endpoints, parameters, or errors",
      noResults: "No matching API document",
    },
    eyebrow: "LLM API Docs",
    title: "Call Large Language Models",
    description: teamLeader
      ? "Use project-scoped keys to call approved models, then manage access, quota, and cost attribution at the project level."
      : "Use a project API key to call OpenAI-compatible model endpoints. Start with model discovery, then call chat, responses, or embeddings.",
    languageLabel: "Documentation language",
    quickInfoLabel: "API basics",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "Sample model",
      currentConfig: "Current API scope",
      activeRoutes: `${formatNumber(stats.visibleModelCount)} callable model${stats.visibleModelCount === 1 ? "" : "s"}`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} project key${stats.apiKeyCount === 1 ? "" : "s"}`,
    },
    groups: [
      {
        title: "Start Here",
        items: [
          {
            id: "quickstart",
            group: "Start Here",
            badge: "GUIDE",
            title: "Quick Start",
            description: "Make your first OpenAI-compatible LLM request through TokenHub.",
            details: [
              { label: "Base URL", value: stats.baseURL },
              { label: "Auth header", value: "Authorization: Bearer <API Key>" },
              { label: "Sample model", value: stats.sampleModel },
              { label: "Current scope", value: teamLeader ? "Team projects" : "Assigned projects" },
            ],
            notesTitle: "Call sequence",
            notes: [
              "Create or copy a project API key from Key Management. Console login tokens are not accepted by /v1 model endpoints.",
              "Call GET /v1/models first. The response is the model list available to that API key.",
              "Use one of those model IDs in POST /v1/chat/completions, /v1/responses, or /v1/embeddings.",
              "For failed calls, copy request_id from the response and search Request Logs.",
            ],
            examplesTitle: "First requests",
            examples: [
              { title: "List available models", code: gatewayListModelsCurl(stats) },
              { title: "Create a chat completion", code: stats.chatCurl },
            ],
          },
          {
            id: "authentication",
            group: "Start Here",
            badge: "AUTH",
            title: "Authentication",
            description: "Every model API request uses a project API key in the Authorization header.",
            table: {
              title: "Required headers",
              columns: ["Header", "Required", "Value"],
              rows: [
                ["Authorization", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "POST requests", "application/json"],
              ],
            },
            notesTitle: "Permission checks",
            notes: [
              "The key must be active and attached to an active project.",
              "The requested model must be visible to the project and backed by at least one enabled route.",
              teamLeader
                ? "Team leaders should issue keys from the intended project so usage rolls up to the right team and cost center."
                : "If you belong to multiple projects, choose the project that should own usage and cost before creating a key.",
            ],
          },
        ],
      },
      {
        title: "LLM API Reference",
        items: [
          {
            id: "list-models",
            group: "LLM API Reference",
            method: "GET",
            path: "/v1/models",
            title: "List Models",
            description: "Return the LLM models currently available to the API key. This endpoint is OpenAI-compatible.",
            params: {
              title: "Request headers",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["Authorization", "header", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "Yes", "application/json"],
              ],
            },
            table: {
              title: "Model fields",
              columns: ["Field", "Description"],
              rows: [
                ["id", "Model identifier used in later API calls."],
                ["object", "Object type, usually model."],
                ["created", "Model creation Unix timestamp."],
                ["input_token_price_per_m", "JieKou-compatible integer input price per million tokens."],
                ["output_token_price_per_m", "JieKou-compatible integer output price per million tokens."],
                ["title", "Model title."],
                ["description", "Model description."],
                ["context_size", "Maximum context size."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayListModelsCurl(stats) }],
          },
          {
            id: "retrieve-model",
            group: "LLM API Reference",
            method: "GET",
            path: "/v1/models/{model}",
            title: "Retrieve Model",
            description: "Return one model visible to the API key. The response fields match the JieKou-compatible model object.",
            params: {
              title: "Path and headers",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "path", "Yes", `Model ID from /v1/models, for example ${stats.sampleModel}.`],
                ["Authorization", "header", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "Yes", "application/json"],
              ],
            },
            table: {
              title: "Response fields",
              columns: ["Field", "Description"],
              rows: [
                ["id", "Model identifier used in API calls."],
                ["created", "Model creation Unix timestamp."],
                ["object", "Object type, always model."],
                ["input_token_price_per_m", "JieKou-compatible integer input price per million tokens."],
                ["output_token_price_per_m", "JieKou-compatible integer output price per million tokens."],
                ["title", "Model title."],
                ["description", "Model description."],
                ["context_size", "Maximum context size."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayRetrieveModelCurl(stats) }],
          },
          {
            id: "chat-completions",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/chat/completions",
            title: "Create Chat Completion",
            description: "Generate a model response from a message list. Use this for normal chat, tool calling, structured output, and streaming.",
            params: {
              title: "Request body",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "string", "Yes", `A model ID from /v1/models, for example ${stats.sampleModel}.`],
                ["messages", "array", "Yes", "Conversation messages with system, user, or assistant roles."],
                ["max_tokens", "integer", "No", "Maximum generated tokens."],
                ["temperature", "number", "No", "Sampling temperature."],
                ["stream", "boolean", "No", "When true, returns Server-Sent Events ending with data: [DONE]."],
                ["tools", "array", "No", "Function tools supported by compatible upstream models."],
                ["response_format", "object", "No", "JSON object or JSON schema output when supported."],
              ],
            },
            table: {
              title: "Response fields",
              columns: ["Field", "Description"],
              rows: [
                ["id", "Unique completion ID."],
                ["choices[].message", "Assistant response content."],
                ["choices[].finish_reason", "Why generation stopped, such as stop or length."],
                ["usage", "prompt, completion, and total token counts."],
              ],
            },
            examplesTitle: "Examples",
            examples: [
              { title: "Non-streaming", code: stats.chatCurl },
              { title: "Streaming", code: gatewayStreamingCurl(stats) },
            ],
          },
          {
            id: "responses-api",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/responses",
            title: "Create Response",
            description: "Use the Responses-style API for simple text input and future multimodal extensions.",
            params: {
              title: "Request body",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "string", "Yes", "A callable model ID."],
                ["input", "string | array", "Yes", "Input text or structured input content."],
                ["stream", "boolean", "No", "Whether to return a streaming response."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayResponsesCurl(stats) }],
          },
          {
            id: "embeddings-api",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/embeddings",
            title: "Create Embeddings",
            description: "Create vector embeddings for search, retrieval, classification, and clustering workflows.",
            params: {
              title: "Request body",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "string", "Yes", "An embedding model ID visible to the key."],
                ["input", "string | array", "Yes", "Text to embed."],
                ["encoding_format", "string", "No", "float or base64 when supported by the upstream model."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayEmbeddingsCurl(stats) }],
          },
        ],
      },
      {
        title: teamLeader ? "Team Rollout" : "Project Keys",
        items: [
          {
            id: "project-keys",
            group: teamLeader ? "Team Rollout" : "Project Keys",
            badge: "KEY",
            title: teamLeader ? "Issue Keys for Projects" : "Use Project Keys",
            description: teamLeader
              ? "A team member may belong to multiple projects. Issue each key under the project that should own usage, quota, and cost."
              : "You may belong to multiple projects. Create the key under the project that should own usage, quota, and cost.",
            table: {
              title: teamLeader ? "Team key rollout checklist" : "Project key rules",
              columns: ["Item", "Rule"],
              rows: teamLeader ? [
                ["Project", "Create or select the project before issuing a key."],
                ["Members", "Add the application owner to the project member panel."],
                ["Models", "Verify GET /v1/models with the new key before handing it to the app."],
                ["Reports", "Review team usage by member, project, model, and cost center."],
              ] : [
                ["Project", "Each API key belongs to exactly one project."],
                ["Models", "The model list is filtered by project and key permissions."],
                ["Secret", "A new key is shown once. Store it in your app secret manager."],
                ["Usage", "Requests are attributed to the key's project and your account."],
              ],
            },
          },
          {
            id: "sdk-examples",
            group: teamLeader ? "Team Rollout" : "Project Keys",
            badge: "SDK",
            title: "OpenAI-Compatible SDKs",
            description: "Point any OpenAI-compatible SDK at the TokenHub Base URL and use a TokenHub project API key.",
            examplesTitle: "SDK examples",
            examples: [
              { title: "Node.js", code: gatewayOpenAISDKExample(stats) },
              { title: "Python", code: gatewayPythonSDKExample(stats) },
            ],
          },
          {
            id: "errors",
            group: teamLeader ? "Team Rollout" : "Project Keys",
            badge: "REF",
            title: "Errors and Troubleshooting",
            description: "Use status codes to locate API key, project membership, model routing, and quota problems.",
            table: {
              title: "Common errors",
              columns: ["Status", "Likely cause", "Action"],
              rows: [
                ["401", "Missing, malformed, disabled, or expired API key.", "Check the Authorization header and key status."],
                ["403", "Project, key, or model permission does not allow the request.", teamLeader ? "Check project membership, key model scope, and team project ownership." : "Ask your team leader to check project membership and model access."],
                ["404/503", "No enabled healthy route can serve the model.", "Ask an administrator to enable routing or check provider health."],
                ["429", "Project quota, concurrency, or provider resource limit was reached.", teamLeader ? "Review project quota and concurrency limits." : "Wait for quota reset or request a quota increase."],
                ["500", "Upstream provider or routing error.", `Search request_id in Request Logs. Current visible log sample: ${formatNumber(stats.requestLogCount)}.`],
              ],
            },
          },
        ],
      },
    ],
  };
}

function gatewayChineseLLMUsageDocs(stats: GatewayDocStats, role: AppRole): GatewayDocBundle {
  const teamLeader = role === "team_leader";
  return {
    defaultDocID: "quickstart",
    nav: {
      title: "接口文档",
      subtitle: "OpenAI 兼容大模型调用",
      searchPlaceholder: "搜索接口、参数或错误码",
      noResults: "没有匹配的接口文档",
    },
    eyebrow: "LLM API Docs",
    title: "调用大语言模型",
    description: teamLeader
      ? "使用项目 Key 调用已批准模型，并在项目维度管理成员权限、额度和成本归因。"
      : "使用项目 API Key 调用 OpenAI 兼容模型接口。先查询可用模型，再发起对话、Responses 或向量请求。",
    languageLabel: "文档语言",
    quickInfoLabel: "接口基础信息",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "示例模型",
      currentConfig: "当前接口范围",
      activeRoutes: `${formatNumber(stats.visibleModelCount)} 个可调用模型`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} 个项目 Key`,
    },
    groups: [
      {
        title: "快速开始",
        items: [
          {
            id: "quickstart",
            group: "快速开始",
            badge: "GUIDE",
            title: "快速接入",
            description: "通过 TokenHub 发起第一笔 OpenAI 兼容的大模型请求。",
            details: [
              { label: "Base URL", value: stats.baseURL },
              { label: "鉴权 Header", value: "Authorization: Bearer <API Key>" },
              { label: "示例模型", value: stats.sampleModel },
              { label: "当前范围", value: teamLeader ? "团队项目" : "已分配项目" },
            ],
            notesTitle: "调用顺序",
            notes: [
              "先在 Key 管理中创建或复制项目 API Key。控制台登录 Token 不能用于 /v1 模型接口。",
              "先调用 GET /v1/models，这个返回值就是当前 Key 可用的模型列表。",
              "从模型列表里选择 model，调用 POST /v1/chat/completions、/v1/responses 或 /v1/embeddings。",
              "调用失败时复制响应里的 request_id，到请求日志里排查。",
            ],
            examplesTitle: "首个请求",
            examples: [
              { title: "查询可用模型", code: gatewayListModelsCurl(stats) },
              { title: "创建聊天对话", code: stats.chatCurl },
            ],
          },
          {
            id: "authentication",
            group: "快速开始",
            badge: "AUTH",
            title: "鉴权方式",
            description: "所有模型接口都使用项目 API Key，通过 Authorization header 传入。",
            table: {
              title: "必填请求头",
              columns: ["Header", "必填", "值"],
              rows: [
                ["Authorization", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "POST 请求", "application/json"],
              ],
            },
            notesTitle: "权限检查",
            notes: [
              "Key 必须处于启用状态，并且绑定到启用的项目。",
              "请求模型必须对该项目可见，并且至少有一条启用路由。",
              teamLeader
                ? "团队 Leader 发放 Key 时，应选择真实归属项目，确保用量能回到正确团队和成本中心。"
                : "如果你在多个项目中，创建 Key 前先选择应该承担用量和成本的项目。",
            ],
          },
        ],
      },
      {
        title: "大模型 API",
        items: [
          {
            id: "list-models",
            group: "大模型 API",
            method: "GET",
            path: "/v1/models",
            title: "获取模型列表",
            description: "返回当前 API Key 可用的大语言模型列表。该 Endpoint 与 OpenAI API 兼容。",
            params: {
              title: "请求头",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["Authorization", "header", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "是", "application/json"],
              ],
            },
            table: {
              title: "模型字段",
              columns: ["字段", "说明"],
              rows: [
                ["id", "模型标识符，后续调用时填写到 model 字段。"],
                ["object", "对象类型，通常为 model。"],
                ["created", "模型创建 Unix 时间戳。"],
                ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格。"],
                ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格。"],
                ["title", "模型标题。"],
                ["description", "模型描述。"],
                ["context_size", "模型最大上下文长度。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayListModelsCurl(stats) }],
          },
          {
            id: "retrieve-model",
            group: "大模型 API",
            method: "GET",
            path: "/v1/models/{model}",
            title: "获取指定模型信息",
            description: "返回当前 API Key 可见的单个模型信息。响应字段与 jiekou 兼容模型对象一致。",
            params: {
              title: "路径参数和请求头",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "path", "是", `来自 /v1/models 的模型 ID，例如 ${stats.sampleModel}。`],
                ["Authorization", "header", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "是", "application/json"],
              ],
            },
            table: {
              title: "响应字段",
              columns: ["字段", "说明"],
              rows: [
                ["id", "模型 ID，在 API Endpoints 中引用。"],
                ["created", "模型创建 Unix 时间戳。"],
                ["object", "对象类型，始终为 model。"],
                ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格。"],
                ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格。"],
                ["title", "模型标题。"],
                ["description", "模型描述。"],
                ["context_size", "模型最大上下文长度。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayRetrieveModelCurl(stats) }],
          },
          {
            id: "chat-completions",
            group: "大模型 API",
            method: "POST",
            path: "/v1/chat/completions",
            title: "创建聊天对话请求",
            description: "根据消息列表生成模型回复，适用于普通对话、工具调用、结构化输出和流式输出。",
            params: {
              title: "请求体",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "string", "是", `来自 /v1/models 的模型 ID，例如 ${stats.sampleModel}。`],
                ["messages", "array", "是", "由 system、user、assistant 组成的消息数组。"],
                ["max_tokens", "integer", "否", "最大生成 token 数。"],
                ["temperature", "number", "否", "采样温度。"],
                ["stream", "boolean", "否", "true 时返回 SSE 流，结束标记为 data: [DONE]。"],
                ["tools", "array", "否", "兼容上游模型的函数工具。"],
                ["response_format", "object", "否", "上游支持时可指定 JSON object 或 JSON schema。"],
              ],
            },
            table: {
              title: "响应字段",
              columns: ["字段", "说明"],
              rows: [
                ["id", "本次对话请求 ID。"],
                ["choices[].message", "模型返回的 assistant 消息。"],
                ["choices[].finish_reason", "生成停止原因，例如 stop 或 length。"],
                ["usage", "prompt、completion 和 total token 统计。"],
              ],
            },
            examplesTitle: "示例",
            examples: [
              { title: "非流式", code: stats.chatCurl },
              { title: "流式", code: gatewayStreamingCurl(stats) },
            ],
          },
          {
            id: "responses-api",
            group: "大模型 API",
            method: "POST",
            path: "/v1/responses",
            title: "创建 Responses 请求",
            description: "使用 Responses 风格接口处理简单文本输入，并为后续多模态能力保留统一入口。",
            params: {
              title: "请求体",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "string", "是", "可调用模型 ID。"],
                ["input", "string | array", "是", "输入文本或结构化输入内容。"],
                ["stream", "boolean", "否", "是否流式返回。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayResponsesCurl(stats) }],
          },
          {
            id: "embeddings-api",
            group: "大模型 API",
            method: "POST",
            path: "/v1/embeddings",
            title: "创建文本向量",
            description: "为搜索、RAG、分类和聚类等场景生成文本向量。",
            params: {
              title: "请求体",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["model", "string", "是", "当前 Key 可见的向量模型 ID。"],
                ["input", "string | array", "是", "需要向量化的文本。"],
                ["encoding_format", "string", "否", "上游支持时可选 float 或 base64。"],
              ],
            },
            examplesTitle: "示例",
            examples: [{ title: "cURL", code: gatewayEmbeddingsCurl(stats) }],
          },
        ],
      },
      {
        title: teamLeader ? "团队接入" : "项目 Key",
        items: [
          {
            id: "project-keys",
            group: teamLeader ? "团队接入" : "项目 Key",
            badge: "KEY",
            title: teamLeader ? "为项目发放 Key" : "使用项目 Key",
            description: teamLeader
              ? "一个成员可以加入多个项目；每个 Key 应发放到真实归属项目，用于用量、额度和成本归因。"
              : "你可能属于多个项目；创建 Key 时应选择真实归属项目，用于用量、额度和成本归因。",
            table: {
              title: teamLeader ? "团队 Key 发放检查表" : "项目 Key 规则",
              columns: ["项目", "规则"],
              rows: teamLeader ? [
                ["Project", "先创建或选择项目，再发放 Key。"],
                ["Members", "在项目详情侧边栏添加应用负责人。"],
                ["Models", "交付给应用前，用新 Key 调用 GET /v1/models 验证模型范围。"],
                ["Reports", "按成员、项目、模型和成本中心查看团队用量。"],
              ] : [
                ["Project", "每个 API Key 只属于一个项目。"],
                ["Models", "模型列表会按项目和 Key 权限过滤。"],
                ["Secret", "新 Key 只展示一次，请保存到应用密钥管理系统。"],
                ["Usage", "请求会归因到 Key 所属项目和当前账号。"],
              ],
            },
          },
          {
            id: "sdk-examples",
            group: teamLeader ? "团队接入" : "项目 Key",
            badge: "SDK",
            title: "OpenAI 兼容 SDK",
            description: "把任意 OpenAI 兼容 SDK 指向 TokenHub Base URL，并使用 TokenHub 项目 API Key。",
            examplesTitle: "SDK 示例",
            examples: [
              { title: "Node.js", code: gatewayOpenAISDKExample(stats) },
              { title: "Python", code: gatewayPythonSDKExample(stats) },
            ],
          },
          {
            id: "errors",
            group: teamLeader ? "团队接入" : "项目 Key",
            badge: "REF",
            title: "错误码与排查",
            description: "按状态码定位 API Key、项目成员、模型路由和额度问题。",
            table: {
              title: "常见错误",
              columns: ["状态", "常见原因", "处理方式"],
              rows: [
                ["401", "API Key 缺失、格式错误、已停用或已过期。", "检查 Authorization header 和 Key 状态。"],
                ["403", "项目、Key 或模型权限不允许当前请求。", teamLeader ? "检查项目成员、Key 模型范围和团队项目归属。" : "联系团队负责人检查项目成员和模型权限。"],
                ["404/503", "该模型没有可用健康路由。", "请管理员启用路由或检查 Provider 健康状态。"],
                ["429", "项目额度、并发或 Provider 资源限制触发。", teamLeader ? "查看项目额度和并发限制。" : "等待额度恢复或申请提升额度。"],
                ["500", "上游 Provider 或路由错误。", `在请求日志中搜索 request_id。当前可见日志样本：${formatNumber(stats.requestLogCount)} 条。`],
              ],
            },
          },
        ],
      },
    ],
  };
}

function gatewayJapaneseLLMUsageDocs(stats: GatewayDocStats, role: AppRole): GatewayDocBundle {
  const teamLeader = role === "team_leader";
  return {
    defaultDocID: "quickstart",
    nav: {
      title: "API ドキュメント",
      subtitle: "OpenAI 互換 LLM 利用",
      searchPlaceholder: "エンドポイント、パラメーター、エラーを検索",
      noResults: "一致する API ドキュメントはありません",
    },
    eyebrow: "LLM API Docs",
    title: "大規模言語モデルを呼び出す",
    description: teamLeader
      ? "Project Key で承認済みモデルを呼び出し、Project 単位で権限、クォータ、コスト配賦を管理します。"
      : "Project API Key で OpenAI 互換モデル API を呼び出します。モデル一覧を確認してから Chat、Responses、Embeddings を利用します。",
    languageLabel: "ドキュメント言語",
    quickInfoLabel: "API 基本情報",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "サンプルモデル",
      currentConfig: "現在の API 範囲",
      activeRoutes: `${formatNumber(stats.visibleModelCount)} 件の呼び出し可能モデル`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} 件の Project Key`,
    },
    groups: [
      {
        title: "クイックスタート",
        items: [
          {
            id: "quickstart",
            group: "クイックスタート",
            badge: "GUIDE",
            title: "はじめての接続",
            description: "TokenHub 経由で最初の OpenAI 互換 LLM リクエストを送信します。",
            details: [
              { label: "Base URL", value: stats.baseURL },
              { label: "認証 Header", value: "Authorization: Bearer <API Key>" },
              { label: "サンプルモデル", value: stats.sampleModel },
              { label: "現在の範囲", value: teamLeader ? "チーム Project" : "割り当て済み Project" },
            ],
            notesTitle: "呼び出し順序",
            notes: [
              "Key Management で Project API Key を作成またはコピーします。コンソールログイントークンは /v1 モデル API では利用できません。",
              "まず GET /v1/models を呼び出します。レスポンスがその Key で利用できるモデル一覧です。",
              "モデル ID を選び、POST /v1/chat/completions、/v1/responses、/v1/embeddings を呼び出します。",
              "失敗時はレスポンスの request_id をコピーし、Request Logs で調査します。",
            ],
            examplesTitle: "最初のリクエスト",
            examples: [
              { title: "利用可能モデル一覧", code: gatewayListModelsCurl(stats) },
              { title: "Chat Completion 作成", code: stats.chatCurl },
            ],
          },
          {
            id: "authentication",
            group: "クイックスタート",
            badge: "AUTH",
            title: "認証方式",
            description: "すべてのモデル API は Project API Key を Authorization header で送信します。",
            table: {
              title: "必須 Header",
              columns: ["Header", "必須", "値"],
              rows: [
                ["Authorization", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "POST リクエスト", "application/json"],
              ],
            },
            notesTitle: "権限チェック",
            notes: [
              "Key は有効で、有効な Project に紐づいている必要があります。",
              "リクエストしたモデルは Project に表示され、少なくとも 1 つの有効ルートが必要です。",
              teamLeader
                ? "チームリーダーは Key 発行時に正しい Project を選び、利用量が正しいチームと Cost Center に集計されるようにします。"
                : "複数 Project に所属している場合、利用量とコストを持つ Project を選んでから Key を作成します。",
            ],
          },
        ],
      },
      {
        title: "LLM API",
        items: [
          {
            id: "list-models",
            group: "LLM API",
            method: "GET",
            path: "/v1/models",
            title: "モデル一覧を取得",
            description: "現在の API Key で利用できる LLM モデル一覧を返します。この Endpoint は OpenAI API 互換です。",
            params: {
              title: "リクエスト Header",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["Authorization", "header", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "はい", "application/json"],
              ],
            },
            table: {
              title: "モデルフィールド",
              columns: ["フィールド", "説明"],
              rows: [
                ["id", "以降の API 呼び出しで model に指定するモデル ID。"],
                ["object", "オブジェクト種別。通常は model。"],
                ["created", "モデル作成 Unix timestamp。"],
                ["input_token_price_per_m", "JieKou 互換の 100 万 input tokens あたり整数価格。"],
                ["output_token_price_per_m", "JieKou 互換の 100 万 output tokens あたり整数価格。"],
                ["title", "モデルタイトル。"],
                ["description", "モデル説明。"],
                ["context_size", "最大コンテキスト長。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayListModelsCurl(stats) }],
          },
          {
            id: "retrieve-model",
            group: "LLM API",
            method: "GET",
            path: "/v1/models/{model}",
            title: "指定モデル情報を取得",
            description: "現在の API Key で利用できる単一モデル情報を返します。レスポンスは JieKou 互換のモデルオブジェクトです。",
            params: {
              title: "Path と Header",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "path", "はい", `/v1/models のモデル ID。例: ${stats.sampleModel}`],
                ["Authorization", "header", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "はい", "application/json"],
              ],
            },
            table: {
              title: "レスポンスフィールド",
              columns: ["フィールド", "説明"],
              rows: [
                ["id", "API 呼び出しで使うモデル ID。"],
                ["created", "モデル作成 Unix timestamp。"],
                ["object", "オブジェクト種別。常に model。"],
                ["input_token_price_per_m", "JieKou 互換の 100 万 input tokens あたり整数価格。"],
                ["output_token_price_per_m", "JieKou 互換の 100 万 output tokens あたり整数価格。"],
                ["title", "モデルタイトル。"],
                ["description", "モデル説明。"],
                ["context_size", "最大コンテキスト長。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayRetrieveModelCurl(stats) }],
          },
          {
            id: "chat-completions",
            group: "LLM API",
            method: "POST",
            path: "/v1/chat/completions",
            title: "Chat Completion を作成",
            description: "メッセージ一覧からモデル応答を生成します。通常のチャット、ツール呼び出し、構造化出力、ストリーミングに利用します。",
            params: {
              title: "リクエスト Body",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "string", "はい", `/v1/models のモデル ID。例: ${stats.sampleModel}`],
                ["messages", "array", "はい", "system、user、assistant のメッセージ配列。"],
                ["max_tokens", "integer", "いいえ", "最大生成 tokens。"],
                ["temperature", "number", "いいえ", "サンプリング温度。"],
                ["stream", "boolean", "いいえ", "true の場合は SSE で返し、data: [DONE] で終了します。"],
                ["tools", "array", "いいえ", "互換上流モデルの関数ツール。"],
                ["response_format", "object", "いいえ", "対応モデルでは JSON object または JSON schema を指定できます。"],
              ],
            },
            table: {
              title: "レスポンスフィールド",
              columns: ["フィールド", "説明"],
              rows: [
                ["id", "リクエスト ID。"],
                ["choices[].message", "モデルが返した assistant メッセージ。"],
                ["choices[].finish_reason", "停止理由。stop や length など。"],
                ["usage", "prompt、completion、total token 統計。"],
              ],
            },
            examplesTitle: "例",
            examples: [
              { title: "非ストリーミング", code: stats.chatCurl },
              { title: "ストリーミング", code: gatewayStreamingCurl(stats) },
            ],
          },
          {
            id: "responses-api",
            group: "LLM API",
            method: "POST",
            path: "/v1/responses",
            title: "Responses リクエストを作成",
            description: "Responses 形式でシンプルなテキスト入力を扱い、将来のマルチモーダル拡張にも備えます。",
            params: {
              title: "リクエスト Body",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "string", "はい", "呼び出し可能なモデル ID。"],
                ["input", "string | array", "はい", "入力テキストまたは構造化入力。"],
                ["stream", "boolean", "いいえ", "ストリーミングで返すかどうか。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayResponsesCurl(stats) }],
          },
          {
            id: "embeddings-api",
            group: "LLM API",
            method: "POST",
            path: "/v1/embeddings",
            title: "Embeddings を作成",
            description: "検索、RAG、分類、クラスタリング向けのテキストベクトルを生成します。",
            params: {
              title: "リクエスト Body",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "string", "はい", "Key から見える embedding モデル ID。"],
                ["input", "string | array", "はい", "ベクトル化するテキスト。"],
                ["encoding_format", "string", "いいえ", "対応時は float または base64。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayEmbeddingsCurl(stats) }],
          },
        ],
      },
      {
        title: teamLeader ? "チーム導入" : "Project Key",
        items: [
          {
            id: "project-keys",
            group: teamLeader ? "チーム導入" : "Project Key",
            badge: "KEY",
            title: teamLeader ? "Project に Key を発行" : "Project Key を利用",
            description: teamLeader
              ? "メンバーは複数 Project に所属できます。利用量、クォータ、コスト配賦を持つ正しい Project に Key を発行します。"
              : "複数 Project に所属している場合があります。利用量、クォータ、コスト配賦を持つ Project を選んで Key を作成します。",
            table: {
              title: teamLeader ? "チーム Key 発行チェックリスト" : "Project Key ルール",
              columns: ["項目", "ルール"],
              rows: teamLeader ? [
                ["Project", "Key 発行前に Project を作成または選択します。"],
                ["Members", "Project 詳細パネルでアプリ責任者を追加します。"],
                ["Models", "アプリに渡す前に新しい Key で GET /v1/models を検証します。"],
                ["Reports", "メンバー、Project、モデル、Cost Center 別にチーム利用量を確認します。"],
              ] : [
                ["Project", "各 API Key は 1 つの Project に属します。"],
                ["Models", "モデル一覧は Project と Key の権限でフィルタリングされます。"],
                ["Secret", "新しい Key は一度だけ表示されます。アプリのシークレット管理に保存します。"],
                ["Usage", "リクエストは Key の Project とアカウントに配賦されます。"],
              ],
            },
          },
          {
            id: "sdk-examples",
            group: teamLeader ? "チーム導入" : "Project Key",
            badge: "SDK",
            title: "OpenAI 互換 SDK",
            description: "OpenAI 互換 SDK の base URL を TokenHub に向け、TokenHub Project API Key を使います。",
            examplesTitle: "SDK 例",
            examples: [
              { title: "Node.js", code: gatewayOpenAISDKExample(stats) },
              { title: "Python", code: gatewayPythonSDKExample(stats) },
            ],
          },
          {
            id: "errors",
            group: teamLeader ? "チーム導入" : "Project Key",
            badge: "REF",
            title: "エラーと調査",
            description: "ステータスコードから API Key、Project メンバー、モデルルート、クォータの問題を切り分けます。",
            table: {
              title: "よくあるエラー",
              columns: ["ステータス", "主な原因", "対応"],
              rows: [
                ["401", "API Key の不足、形式不正、無効化、期限切れ。", "Authorization header と Key 状態を確認します。"],
                ["403", "Project、Key、モデル権限がリクエストを許可していません。", teamLeader ? "Project メンバー、Key のモデル範囲、チーム Project 所有を確認します。" : "チームリーダーに Project メンバーとモデル権限の確認を依頼します。"],
                ["404/503", "モデルを処理できる健全なルートがありません。", "管理者にルート有効化または Provider ヘルス確認を依頼します。"],
                ["429", "Project クォータ、同時実行、Provider リソース制限に達しました。", teamLeader ? "Project クォータと同時実行制限を確認します。" : "クォータ回復を待つか、増枠を依頼します。"],
                ["500", "上流 Provider またはルーティングエラー。", `Request Logs で request_id を検索します。現在見えるログサンプル: ${formatNumber(stats.requestLogCount)} 件。`],
              ],
            },
          },
        ],
      },
    ],
  };
}

function gatewayEnglishDocs(stats: GatewayDocStats): GatewayDocBundle {
  return {
    nav: {
      title: "Documentation",
      subtitle: "Role-based guides and API references",
      searchPlaceholder: "Search guides, APIs, or error codes",
      noResults: "No matching documents",
    },
    eyebrow: "TokenHub Docs",
    title: "Role-Based Gateway Guides",
    description: "TokenHub is documented around three enterprise roles: users call approved models, team leaders manage projects and members, and administrators govern providers, routing, identity, audit, and cost.",
    languageLabel: "Documentation language",
    quickInfoLabel: "API basics",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "Sample model",
      currentConfig: "Current configuration",
      activeRoutes: `${formatNumber(stats.activeRouteCountValue)} active route${stats.activeRouteCountValue === 1 ? "" : "s"}`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} API Key${stats.apiKeyCount === 1 ? "" : "s"}`,
    },
    groups: [
      {
        title: "Introduction",
        items: [
          {
            id: "overview",
            group: "Introduction",
            badge: "DOC",
            title: "Platform Overview",
            description: "Understand how TokenHub connects models, projects, keys, routing, audit, and cost attribution into one governed AI access path.",
            details: [
              { label: "Primary entry points", value: "Model Playground / Key Management / Usage Analytics" },
              { label: "Application API", value: "/v1/*" },
              { label: "Admin API", value: "/api/admin/*" },
              { label: "Data scope", value: "Personal / Team / Platform" },
            ],
            notesTitle: "First steps",
            notes: [
              "Users start with available models, key management, personal usage, and request logs; they do not need provider credentials.",
              "Team leaders work from project spaces and use the project detail panel to manage members, keys, quotas, and attribution.",
              "Administrators connect providers, publish model catalog entries, enable routing rules, configure identity sources, and monitor audit and cost controls.",
            ],
          },
          {
            id: "concepts",
            group: "Introduction",
            badge: "DOC",
            title: "Core Concepts",
            description: "A shared vocabulary for the resource and permission boundaries in the enterprise AI gateway.",
            table: {
              title: "Concepts",
              columns: ["Concept", "Meaning"],
              rows: [
                ["Project", "An internal application or business space. It is the basic unit for keys, quota, members, and cost attribution."],
                ["API Key", "A credential attached to a project and used by applications to call /v1/* model endpoints."],
                ["Model Catalog", "The standard model list shown to users. A model is callable only when it has an enabled route."],
                ["Routing Rule", "Maps a standard model to an upstream provider model and defines priority, weight, and strategy."],
                ["Provider", "An upstream model service or internal model resource with Base URL, credentials, and health state."],
                ["Usage Attribution", "Requests, tokens, and cost are attributed to users, projects, teams, and cost centers."],
              ],
            },
          },
        ],
      },
      {
        title: "Role Guides",
        items: [
          {
            id: "user-guide",
            group: "Role Guides",
            badge: "USER",
            title: "User Guide",
            description: "Users focus on available models, project keys, API examples, personal usage, and request logs.",
            details: [
              { label: "Default menu", value: "Overview / API Documentation / Model Playground" },
              { label: "Resource scope", value: `${formatNumber(stats.visibleModelCount)} visible models` },
              { label: "Key ownership", value: "Assigned project" },
              { label: "Report scope", value: "Personal usage" },
            ],
            notesTitle: "Daily workflow",
            notes: [
              "Open Available Models or Model Playground to confirm which models are callable for your account.",
              "Open Key Management, choose an assigned project, and create or copy an application API key.",
              "Applications should call model endpoints such as /v1/chat/completions, /v1/responses, and /v1/embeddings.",
              "For 401, 403, or 429 responses, copy request_id into Request Logs or ask your team leader to adjust project access.",
            ],
            table: {
              title: "What users can do",
              columns: ["Task", "Where", "Notes"],
              rows: [
                ["Review models", "Available Models", "Shows the models callable by the current account."],
                ["Test prompts", "Model Playground", "Checks prompts, responses, routing, and estimated cost."],
                ["Manage keys", "Key Management", "Keys must be created under an assigned project."],
                ["Review usage", "Usage Analytics", "Shows only requests, tokens, and cost visible to the current account."],
              ],
            },
          },
          {
            id: "team-leader-guide",
            group: "Role Guides",
            badge: "LEAD",
            title: "Team Leader Guide",
            description: "Team leaders manage project spaces, project members, key issuance, team reports, and project-level cost attribution.",
            details: [
              { label: "Default menu", value: "Team Overview / Projects / Key Management" },
              { label: "Projects", value: `${formatNumber(stats.projectCount)} projects` },
              { label: "Member management", value: "Project detail side panel" },
              { label: "Report scope", value: "Team and project usage" },
            ],
            notesTitle: "Project governance workflow",
            notes: [
              "Create or select a project in Project Spaces. A project is the boundary for members, keys, quota, and cost attribution.",
              "Click a project to open the right-side detail panel, then view, add, edit, or remove project members there.",
              "When issuing keys, use project membership roles to decide whether a user can create application keys.",
              "Use Team Reports to compare usage by member, project, model, and cost center.",
            ],
            table: {
              title: "Project membership roles",
              columns: ["Role", "Default capability"],
              rows: [
                ["Owner", "Manages project settings, members, keys, and quota."],
                ["Maintainer", "Maintains members and keys; suitable for project technical owners."],
                ["Developer", "Creates and uses project keys; suitable for application developers."],
                ["Viewer", "Views project data and usage but cannot issue keys."],
              ],
            },
          },
          {
            id: "administrator-guide",
            group: "Role Guides",
            badge: "ADMIN",
            title: "Administrator Guide",
            description: "Administrators govern providers, model catalog, routing policies, identity sources, RBAC, audit, security, and cost controls.",
            details: [
              { label: "Default menu", value: "Platform Overview / Providers / Routes / Settings" },
              { label: "Providers", value: `${formatNumber(stats.providerCount)} providers` },
              { label: "Routing rules", value: `${formatNumber(stats.routeCount)} rules` },
              { label: "Users", value: `${formatNumber(stats.userCount)} users` },
            ],
            notesTitle: "Production setup order",
            notes: [
              "Configure upstream Base URLs, credentials, resource groups, and health checks in Provider Channels.",
              "Maintain standard public model names, capability tags, context windows, and price units in Model Catalog.",
              "Create at least one enabled routing rule for every model that should be visible and callable.",
              "Configure identity providers, role permissions, default policies, audit retention, and enterprise integrations in System Settings.",
            ],
            table: {
              title: "Administrator checklist",
              columns: ["Area", "Check"],
              rows: [
                ["Identity", "Configure at least one enterprise identity source and retain a controlled administrator account."],
                ["Routing", "Models without configured routes must be visually distinguished in the admin model catalog."],
                ["Security", "API keys are shown once; rotation and deletion must leave audit records."],
                ["Cost", "Provider, project, team, and cost center attribution should remain traceable."],
              ],
            },
          },
        ],
      },
      {
        title: "API Reference",
        items: [
          {
            id: "model-api",
            group: "API Reference",
            title: "Model API",
            method: "POST",
            path: "/v1/chat/completions",
            description: "Call OpenAI-compatible model endpoints with a project API key.",
            params: {
              title: "Request parameters",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["Authorization", "header", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["model", "string", "Yes", `Standard model name, for example ${stats.sampleModel}`],
                ["messages", "array", "Yes", "system/user/assistant message array"],
                ["stream", "boolean", "No", "When true, returns an SSE streaming response"],
              ],
            },
            examplesTitle: "English examples",
            examples: [{ title: "Chat completion", code: stats.chatCurl }],
          },
          {
            id: "keys-projects",
            group: "API Reference",
            badge: "REF",
            title: "Keys and Projects",
            description: "Keys always belong to projects. One person can belong to multiple projects and chooses the project when creating a key.",
            table: {
              title: "Assignment model",
              columns: ["Object", "Managed by", "Notes"],
              rows: [
                ["Project", "Administrator or team leader", "Contains members, keys, quota, and cost attribution."],
                ["Membership", "Project Owner or Maintainer", "Controls whether a user can view the project or issue keys."],
                ["API Key", "Authorized project member", "Can call only models visible to the project and backed by enabled routes."],
              ],
            },
          },
          {
            id: "troubleshooting",
            group: "API Reference",
            badge: "REF",
            title: "Troubleshooting",
            description: "Use status codes to locate API key, project membership, model routing, and quota problems.",
            table: {
              title: "Common errors",
              columns: ["Status", "Code", "Fix"],
              rows: [
                ["401", "invalid_api_key", "Check that Authorization uses a TokenHub API key."],
                ["403", "project_forbidden / model_not_allowed", "Check project membership and whether the model is open to the project."],
                ["404/503", "provider_unavailable", "Enable a route for the model or check upstream provider health."],
                ["429", "quota_exceeded", "Check project quota, concurrency limits, and provider resource limits."],
                ["500", "upstream_error", `Inspect request_id in Request Logs; current log sample is ${formatNumber(stats.requestLogCount)} records.`],
              ],
            },
          },
        ],
      },
    ],
  };
}

function gatewayChineseDocs(stats: GatewayDocBundle): GatewayDocBundle {
  return {
    nav: {
      title: "文档导航",
      subtitle: "按角色和任务查看指南",
      searchPlaceholder: "搜索指南、API 或错误码",
      noResults: "没有匹配的文档",
    },
    eyebrow: "TokenHub Docs",
    title: "面向三种角色的网关指南",
    description: "TokenHub 文档按企业角色组织：普通用户调用已批准模型，团队负责人管理项目和成员，管理员治理 Provider、路由、身份源、审计和成本。",
    languageLabel: "文档语言",
    quickInfoLabel: "接口基础信息",
    quickCards: {
      ...stats.quickCards,
      sampleModel: "示例模型",
      currentConfig: "当前配置",
      activeRoutes: stats.quickCards.activeRoutes.replace("active routes", "条启用路由").replace("active route", "条启用路由"),
      apiKeys: stats.quickCards.apiKeys.replace("API Keys", "个 API Key").replace("API Key", "个 API Key"),
    },
    groups: [
      {
        title: "开始",
        items: [
          {
            id: "overview",
            group: "开始",
            badge: "DOC",
            title: "平台概览",
            description: "理解 TokenHub 如何把模型、项目、Key、路由、审计和成本归因放在同一条治理链路里。",
            details: [
              { label: "主要入口", value: "Model Playground / Key Management / Usage Analytics" },
              { label: "业务接口", value: "/v1/*" },
              { label: "管理接口", value: "/api/admin/*" },
              { label: "数据范围", value: "Personal / Team / Platform" },
            ],
            notesTitle: "上手路径",
            notes: [
              "普通用户从可用模型、Key 管理、个人用量和请求日志开始，不需要理解 Provider 凭证。",
              "团队负责人围绕项目空间工作，在项目详情侧边栏维护成员、Key、额度和费用归属。",
              "管理员接入 Provider，发布模型目录，启用路由策略，配置身份源，并监控审计和成本治理。",
            ],
          },
          {
            ...stats.groups[0].items[1],
            group: "开始",
            title: "核心概念",
            description: "用统一术语解释企业 AI 网关中的资源边界和权限边界。",
            table: {
              title: "概念表",
              columns: ["概念", "含义"],
              rows: [
                ["Project", "企业内部应用或业务空间，是 Key、额度、成员和成本归因的基本单元。"],
                ["API Key", "绑定到 Project 的调用凭证，用于业务应用访问 /v1/* 模型接口。"],
                ["Model Catalog", "对用户展示的标准模型目录，只有配置了启用路由的模型才可调用。"],
                ["Routing Rule", "把标准模型映射到上游 Provider 模型，并定义优先级、权重和策略。"],
                ["Provider", "上游模型服务商或内部模型服务资源，包含 Base URL、凭证和健康状态。"],
                ["Usage Attribution", "请求、Token 和成本会归因到个人、Project、Team 和成本中心。"],
              ],
            },
          },
        ],
      },
      {
        title: "角色指南",
        items: [
          {
            ...stats.groups[1].items[0],
            group: "角色指南",
            title: "普通用户指南",
            description: "普通用户关注可用模型、项目 Key、调用示例、个人用量和请求日志。",
            notesTitle: "日常流程",
            notes: [
              "先在 Available Models 或 Model Playground 确认可调用模型。",
              "在 Key Management 中选择被分配的 Project，再创建或复制业务 API Key。",
              "业务应用只调用 /v1/chat/completions、/v1/responses、/v1/embeddings 等模型接口。",
              "遇到 401/403/429 时，复制 request_id 到 Request Logs 查看原因，或联系团队负责人调整项目权限。",
            ],
            table: {
              title: "普通用户能做什么",
              columns: ["任务", "位置", "说明"],
              rows: [
                ["查看模型", "Available Models", "显示当前账号可调用的模型。"],
                ["测试模型", "Model Playground", "验证提示词、模型返回、路由和成本估算。"],
                ["管理 Key", "Key Management", "Key 必须选择已分配的 Project。"],
                ["查看用量", "Usage Analytics", "只展示当前账号可见的请求、Token 和成本。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[1],
            group: "角色指南",
            title: "团队负责人指南",
            description: "团队负责人负责项目空间、项目成员、Key 发放、团队报表和项目级成本归因。",
            notesTitle: "项目治理流程",
            notes: [
              "在 Project Spaces 中创建或选择项目，项目是成员、Key、额度和成本归属的边界。",
              "点击项目后，在右侧详情栏查看、添加、编辑或移除项目成员。",
              "为项目发放 Key 时，根据成员角色决定是否允许创建业务 Key。",
              "用 Team Reports 查看成员、项目、模型和成本中心维度的消费归因。",
            ],
            table: {
              title: "项目成员角色",
              columns: ["角色", "默认能力"],
              rows: [
                ["Owner", "管理项目设置、成员、Key 和额度。"],
                ["Maintainer", "维护成员和 Key，适合项目技术负责人。"],
                ["Developer", "可以创建和使用项目 Key，适合应用开发者。"],
                ["Viewer", "只能查看项目和用量，不能发放 Key。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[2],
            group: "角色指南",
            title: "管理员指南",
            description: "管理员负责全局 Provider、模型目录、路由策略、身份源、角色权限、安全审计和成本治理。",
            notesTitle: "上线顺序",
            notes: [
              "先在 Provider Channels 配置上游 Base URL、凭证、资源组和健康检查。",
              "在 Model Catalog 中维护对业务开放的标准模型名、能力标签和价格口径。",
              "在 Routing Policies 中为每个可用模型配置至少一条启用路由。",
              "在 System Settings 中配置身份源、角色权限、默认策略、审计保留和企业集成。",
            ],
            table: {
              title: "管理员检查清单",
              columns: ["领域", "检查项"],
              rows: [
                ["Identity", "至少配置一个企业身份源，并保留可控的管理员账号。"],
                ["Routing", "未配置路由的模型需要在后台以不同背景色提示。"],
                ["Security", "API Key 只展示一次，轮换和删除需要审计记录。"],
                ["Cost", "Provider、Project、Team 和 Cost Center 都需要可追踪。"],
              ],
            },
          },
        ],
      },
      {
        title: "API 参考",
        items: [
          {
            ...stats.groups[2].items[0],
            group: "API 参考",
            title: "模型 API",
            description: "使用项目 API Key 调用 OpenAI 兼容的模型接口。",
            params: {
              title: "请求参数",
              columns: ["字段", "类型", "必填", "说明"],
              rows: [
                ["Authorization", "header", "是", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["model", "string", "是", "统一模型名，例如示例模型卡片中的模型名称"],
                ["messages", "array", "是", "system/user/assistant 消息数组"],
                ["stream", "boolean", "否", "true 时返回 SSE 流式响应"],
              ],
            },
            examplesTitle: "英文样例",
          },
          {
            ...stats.groups[2].items[1],
            group: "API 参考",
            title: "Key 与项目",
            description: "Key 始终属于 Project；一个人可以加入多个 Project，并在创建 Key 时选择归属项目。",
            table: {
              title: "分配模型",
              columns: ["对象", "谁来管理", "说明"],
              rows: [
                ["Project", "管理员或团队负责人", "承载成员、Key、额度和成本归因。"],
                ["Membership", "项目 Owner 或 Maintainer", "决定用户是否可查看项目或发放 Key。"],
                ["API Key", "有权限的项目成员", "只允许调用项目可见且已配置路由的模型。"],
              ],
            },
          },
          {
            ...stats.groups[2].items[2],
            group: "API 参考",
            title: "错误排查",
            description: "按状态码定位 API Key、项目成员、模型路由和额度问题。",
            table: {
              title: "常见错误",
              columns: ["状态", "错误码", "处理方式"],
              rows: [
                ["401", "invalid_api_key", "检查 Authorization 是否使用 TokenHub API Key。"],
                ["403", "project_forbidden / model_not_allowed", "检查用户是否在项目中，以及模型是否对项目开放。"],
                ["404/503", "provider_unavailable", "为模型配置启用路由，或检查上游 Provider 健康状态。"],
                ["429", "quota_exceeded", "检查项目额度、并发限制和 Provider 资源限制。"],
                ["500", "upstream_error", "在 Request Logs 中查看 request_id。"],
              ],
            },
          },
        ],
      },
    ],
  };
}

function gatewayJapaneseDocs(stats: GatewayDocBundle): GatewayDocBundle {
  return {
    ...gatewayChineseDocs(stats),
    nav: {
      title: "ドキュメント",
      subtitle: "ロールとタスク別に確認",
      searchPlaceholder: "ガイド、API、エラーコードを検索",
      noResults: "一致するドキュメントはありません",
    },
    title: "3 つのロール別ゲートウェイガイド",
    description: "TokenHub のドキュメントは、利用者、チームリーダー、管理者の 3 つの企業ロールを中心に整理しています。",
    languageLabel: "ドキュメント言語",
    quickInfoLabel: "API 基本情報",
    quickCards: {
      ...stats.quickCards,
      sampleModel: "サンプルモデル",
      currentConfig: "現在の設定",
      activeRoutes: stats.quickCards.activeRoutes.replace("active routes", "件の有効ルート").replace("active route", "件の有効ルート"),
      apiKeys: stats.quickCards.apiKeys.replace("API Keys", "件の API Key").replace("API Key", "件の API Key"),
    },
    groups: [
      {
        title: "はじめに",
        items: [
          {
            ...stats.groups[0].items[0],
            group: "はじめに",
            title: "プラットフォーム概要",
            description: "TokenHub がモデル、プロジェクト、Key、ルーティング、監査、コスト配賦を 1 つの統制フローにまとめる仕組みを説明します。",
            notesTitle: "開始手順",
            notes: [
              "利用者は利用可能モデル、Key 管理、個人利用量から始めます。Provider 認証情報を理解する必要はありません。",
              "チームリーダーはプロジェクトスペースを中心に、詳細サイドパネルでメンバー、Key、コスト帰属を管理します。",
              "管理者は Provider、モデルカタログ、ルーティング、ID プロバイダー、監査とコスト統制を設定します。",
            ],
          },
          {
            ...stats.groups[0].items[1],
            group: "はじめに",
            title: "主要概念",
            description: "企業 AI ゲートウェイのリソース境界と権限境界を共通語彙で整理します。",
            table: {
              title: "概念一覧",
              columns: ["概念", "意味"],
              rows: [
                ["Project", "社内アプリケーションまたは業務スペース。Key、クォータ、メンバー、コスト配賦の基本単位です。"],
                ["API Key", "Project に紐づく呼び出し認証情報。業務アプリが /v1/* を呼び出すために使います。"],
                ["Model Catalog", "ユーザーに表示する標準モデル一覧。有効なルートがあるモデルだけ呼び出せます。"],
                ["Routing Rule", "標準モデルを上流 Provider モデルへ割り当て、優先度、重み、戦略を定義します。"],
                ["Provider", "上流モデルサービスまたは社内モデルリソース。Base URL、認証情報、ヘルス状態を持ちます。"],
                ["Usage Attribution", "リクエスト、Token、コストを個人、Project、Team、Cost Center に配賦します。"],
              ],
            },
          },
        ],
      },
      {
        title: "ロールガイド",
        items: [
          {
            ...stats.groups[1].items[0],
            group: "ロールガイド",
            title: "利用者ガイド",
            description: "利用者は利用可能モデル、プロジェクト Key、呼び出し例、個人利用量、リクエストログを確認します。",
            notesTitle: "日常フロー",
            notes: [
              "Available Models または Model Playground で呼び出せるモデルを確認します。",
              "Key Management で割り当て済み Project を選び、業務 API Key を作成またはコピーします。",
              "業務アプリは /v1/chat/completions、/v1/responses、/v1/embeddings などのモデル API だけを呼び出します。",
              "401/403/429 が出た場合は request_id を Request Logs で検索し、必要に応じてチームリーダーに権限調整を依頼します。",
            ],
            table: {
              title: "利用者の操作",
              columns: ["タスク", "画面", "説明"],
              rows: [
                ["モデル確認", "Available Models", "現在のアカウントで呼び出せるモデルを表示します。"],
                ["モデル検証", "Model Playground", "プロンプト、応答、ルーティング、コスト見積もりを確認します。"],
                ["Key 管理", "Key Management", "Key 作成時は割り当て済み Project を選択します。"],
                ["利用量確認", "Usage Analytics", "現在のアカウントに見えるリクエスト、Token、コストだけを表示します。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[1],
            group: "ロールガイド",
            title: "チームリーダーガイド",
            description: "チームリーダーはプロジェクト、メンバー、Key 発行、チームレポート、プロジェクト別コスト配賦を管理します。",
            notesTitle: "プロジェクト統制フロー",
            notes: [
              "Project Spaces でプロジェクトを作成または選択します。Project はメンバー、Key、クォータ、コスト帰属の境界です。",
              "プロジェクトをクリックし、右側の詳細パネルでメンバーの表示、追加、編集、削除を行います。",
              "プロジェクトに Key を発行するときは、メンバーのロールに応じて Key 作成可否を決めます。",
              "Team Reports でメンバー、Project、モデル、Cost Center 別の利用量とコストを確認します。",
            ],
            table: {
              title: "プロジェクトメンバーのロール",
              columns: ["ロール", "既定能力"],
              rows: [
                ["Owner", "プロジェクト設定、メンバー、Key、クォータを管理します。"],
                ["Maintainer", "メンバーと Key を保守します。プロジェクトの技術責任者に適しています。"],
                ["Developer", "プロジェクト Key を作成、利用できます。アプリ開発者に適しています。"],
                ["Viewer", "プロジェクトと利用量のみ閲覧でき、Key は発行できません。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[2],
            group: "ロールガイド",
            title: "管理者ガイド",
            description: "管理者は Provider、モデルカタログ、ルーティング、ID プロバイダー、権限、監査、コスト統制を管理します。",
            notesTitle: "公開前チェック",
            notes: [
              "Provider Channels で上流 Base URL、認証情報、リソースグループ、ヘルスチェックを設定します。",
              "Model Catalog で業務に公開する標準モデル名、能力タグ、価格単位を管理します。",
              "Routing Policies で公開モデルごとに少なくとも 1 つの有効ルートを設定します。",
              "System Settings で ID プロバイダー、ロール権限、既定ポリシー、監査保持期間、企業連携を設定します。",
            ],
            table: {
              title: "管理者チェックリスト",
              columns: ["領域", "確認項目"],
              rows: [
                ["Identity", "少なくとも 1 つの企業 ID プロバイダーを設定し、管理者アカウントを保持します。"],
                ["Routing", "ルート未設定モデルは管理画面で異なる背景色で表示します。"],
                ["Security", "API Key は一度だけ表示し、ローテーションと削除は監査します。"],
                ["Cost", "Provider、Project、Team、Cost Center を追跡可能にします。"],
              ],
            },
          },
        ],
      },
      {
        title: "API リファレンス",
        items: [
          {
            ...stats.groups[2].items[0],
            group: "API リファレンス",
            title: "モデル API",
            description: "Project API Key で OpenAI 互換のモデル API を呼び出します。",
            params: {
              title: "リクエストパラメーター",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["Authorization", "header", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["model", "string", "はい", "標準モデル名"],
                ["messages", "array", "はい", "system/user/assistant のメッセージ配列"],
                ["stream", "boolean", "いいえ", "true の場合は SSE ストリーミングレスポンスを返します"],
              ],
            },
            examplesTitle: "英語サンプル",
          },
          {
            ...stats.groups[2].items[1],
            group: "API リファレンス",
            title: "Key と Project",
            description: "Key は常に Project に属します。1 人のユーザーは複数 Project に参加でき、Key 作成時に所属 Project を選びます。",
            table: {
              title: "割り当てモデル",
              columns: ["対象", "管理者", "説明"],
              rows: [
                ["Project", "管理者またはチームリーダー", "メンバー、Key、クォータ、コスト配賦を持ちます。"],
                ["Membership", "Project Owner または Maintainer", "ユーザーが Project を閲覧できるか、Key を発行できるかを決めます。"],
                ["API Key", "権限を持つ Project メンバー", "Project に見えて、有効ルートがあるモデルだけ呼び出せます。"],
              ],
            },
          },
          {
            ...stats.groups[2].items[2],
            group: "API リファレンス",
            title: "トラブルシューティング",
            description: "ステータスコードから API Key、Project メンバー、モデルルート、クォータの問題を切り分けます。",
            table: {
              title: "よくあるエラー",
              columns: ["ステータス", "エラーコード", "対応"],
              rows: [
                ["401", "invalid_api_key", "Authorization に TokenHub API Key を指定しているか確認します。"],
                ["403", "project_forbidden / model_not_allowed", "ユーザーが Project に所属しているか、モデルが Project に公開されているか確認します。"],
                ["404/503", "provider_unavailable", "モデルに有効ルートを設定するか、上流 Provider のヘルスを確認します。"],
                ["429", "quota_exceeded", "Project クォータ、同時実行制限、Provider リソース制限を確認します。"],
                ["500", "upstream_error", "Request Logs で request_id を確認します。"],
              ],
            },
          },
        ],
      },
    ],
  };
}

function gatewayLanguageLabel(language: AppLanguage) {
  if (language === "zh-CN") return "中文";
  if (language === "ja") return "日本語";
  return "English";
}

function gatewayDocGroups({
  baseURL,
  keyHint,
  sampleModel,
  activeRoutes,
  data,
}: {
  baseURL: string;
  keyHint: string;
  sampleModel: string;
  activeRoutes: number;
  data: AppData;
}): any[] {
  const authHeader = `Authorization: Bearer ${keyHint}`;
  const sampleSystemPrompt = tx("你是企业内部 AI 助手。");
  const sampleIntroPrompt = tx("用两句话介绍 TokenHub");
  const sampleWorkOrderPrompt = tx("总结今天的工单重点");
  const sampleKnowledgeInput = tx("TokenHub 企业知识库");
  const chatCurl = `curl -X POST "${baseURL}/chat/completions" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "${sampleModel}",
    "messages": [
      {"role": "system", "content": "${sampleSystemPrompt}"},
      {"role": "user", "content": "${sampleIntroPrompt}"}
    ],
    "temperature": 0.7,
    "stream": false
  }'`;

  return [
    {
      title: "开始",
      items: [
        {
          id: "quickstart",
          group: "开始",
          title: "快速接入",
          description: "用一个项目 API Key 调用 TokenHub 的 OpenAI 兼容模型接口。",
          details: [
            { label: "Base URL", value: baseURL },
            { label: "鉴权方式", value: "Bearer API Key" },
            { label: "示例模型", value: sampleModel },
            { label: "启用路由", value: countWithUnit(activeRoutes || data.summary.active_route_count || 0, "条", "route", "件") },
          ],
          notes: [
            "业务应用只需要调用 /v1/*，不需要也不应该访问 /api/admin/*。",
            "每个 API Key 都会受到项目状态、模型白名单、额度、并发和 Provider 路由策略约束。",
            "排查失败请求时，优先复制响应里的 request_id 到请求日志查看完整链路。",
          ],
          examples: [
            {
              title: "查询可用模型",
              code: `curl "${baseURL}/models" \\
  -H "${authHeader}"`,
            },
            { title: "发起一次对话", code: chatCurl },
          ],
        },
        {
          id: "auth",
          group: "开始",
          title: "认证与权限",
          description: "业务 API 使用项目下发的 API Key；控制台登录令牌不能替代业务 Key。",
          details: [
            { label: "Header", value: "Authorization" },
            { label: "格式", value: "Bearer YOUR_TOKENHUB_API_KEY" },
            { label: "当前 Key 数", value: countWithUnit(data.keys.length || data.summary.api_key_count || 0, "个", "Key", "件") },
          ],
          params: [
            ["Authorization", "header", "是", "项目 API Key，格式为 Bearer sk_xxx"],
            ["Content-Type", "header", "POST 必填", "JSON 请求使用 application/json"],
            ["model", "body", "模型接口必填", "统一模型名，需在 Key 白名单和路由策略中可用"],
          ],
          notes: [
            "401 通常表示 Key 缺失、格式错误、已停用或已过期。",
            "403 通常表示项目状态、模型白名单或权限范围不允许当前调用。",
          ],
        },
      ],
    },
    {
      title: "模型 API",
      items: [
        {
          id: "models",
          group: "模型 API",
          title: "模型列表",
          method: "GET",
          path: "/v1/models",
          description: "按当前 API Key 的权限返回可用统一模型。",
          status: "active",
          params: [
            ["Authorization", "header", "是", "Bearer API Key"],
            ["Content-Type", "header", "是", "application/json"],
          ],
          examples: [{ title: "请求示例", code: `curl "${baseURL}/models" -H "${authHeader}" -H "Content-Type: application/json"` }],
          table: {
            columns: ["字段", "说明"],
            rows: [
              ["id", "统一模型名称，用于后续调用的 model 字段"],
              ["object", "OpenAI 兼容对象类型，通常为 model"],
              ["created", "模型创建 Unix 时间戳"],
              ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格"],
              ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格"],
              ["title", "模型标题"],
              ["description", "模型描述"],
              ["context_size", "模型最大上下文长度"],
            ],
          },
        },
        {
          id: "model-detail",
          group: "模型 API",
          title: "指定模型信息",
          method: "GET",
          path: "/v1/models/{model}",
          description: "按当前 API Key 的权限返回单个可用统一模型。",
          status: "active",
          params: [
            ["model", "path", "是", "来自 /v1/models 的统一模型名"],
            ["Authorization", "header", "是", "Bearer API Key"],
            ["Content-Type", "header", "是", "application/json"],
          ],
          examples: [{ title: "请求示例", code: `curl "${baseURL}/models/${encodeURIComponent(sampleModel)}" -H "${authHeader}" -H "Content-Type: application/json"` }],
          table: {
            columns: ["字段", "说明"],
            rows: [
              ["id", "统一模型名称，用于后续调用的 model 字段"],
              ["created", "模型创建 Unix 时间戳"],
              ["object", "OpenAI 兼容对象类型，始终为 model"],
              ["input_token_price_per_m", "兼容 jiekou 的每百万输入 tokens 整数价格"],
              ["output_token_price_per_m", "兼容 jiekou 的每百万输出 tokens 整数价格"],
              ["title", "模型标题"],
              ["description", "模型描述"],
              ["context_size", "模型最大上下文长度"],
            ],
          },
        },
        {
          id: "chat",
          group: "模型 API",
          title: "对话补全",
          method: "POST",
          path: "/v1/chat/completions",
          description: "兼容 OpenAI Chat Completions，用于普通对话、工具调用和流式输出。",
          status: "active",
          params: [
            ["model", "string", "是", "统一模型名，例如 " + sampleModel],
            ["messages", "array", "是", "system/user/assistant 消息数组"],
            ["temperature", "number", "否", "采样温度，默认由上游模型决定"],
            ["stream", "boolean", "否", "true 时返回 SSE 流式响应"],
          ],
          examples: [{ title: "cURL", code: chatCurl }],
        },
        {
          id: "responses",
          group: "模型 API",
          title: "Responses API",
          method: "POST",
          path: "/v1/responses",
          description: "兼容新版 Responses 风格调用，适合统一文本输入和多模态能力扩展。",
          status: "active",
          params: [
            ["model", "string", "是", "统一模型名"],
            ["input", "string | array", "是", "用户输入内容"],
            ["stream", "boolean", "否", "是否流式返回"],
          ],
          examples: [
            {
              title: "cURL",
              code: `curl -X POST "${baseURL}/responses" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"${sampleModel}","input":"${sampleWorkOrderPrompt}"}'`,
            },
          ],
        },
        {
          id: "embeddings",
          group: "模型 API",
          title: "文本向量",
          method: "POST",
          path: "/v1/embeddings",
          description: "转发文本向量生成请求，并记录 Token 与成本归因。",
          status: "active",
          params: [
            ["model", "string", "是", "向量统一模型名"],
            ["input", "string | array", "是", "需要向量化的文本"],
            ["encoding_format", "string", "否", "可选 float/base64，取决于上游模型支持"],
          ],
          examples: [
            {
              title: "cURL",
              code: `curl -X POST "${baseURL}/embeddings" \\
  -H "${authHeader}" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"text-embedding-3-small","input":"${sampleKnowledgeInput}"}'`,
            },
          ],
        },
        {
          id: "models-current",
          group: "模型 API",
          title: "当前可调用模型",
          description: "根据模型目录和路由策略汇总当前控制台可见模型。",
        },
      ],
    },
    {
      title: "管理 API",
      items: [
        {
          id: "admin-login",
          group: "管理 API",
          title: "控制台登录",
          method: "POST",
          path: "/api/admin/auth/login",
          description: "控制台用户登录接口，不等同于模型 API Key。",
          params: [
            ["identity", "string", "是", "用户名或邮箱"],
            ["password", "string", "是", "控制台密码"],
          ],
          notes: ["管理 API 仅用于控制台和受信任后台程序，不应该暴露给业务应用前端。"],
        },
        {
          id: "admin-providers",
          group: "管理 API",
          title: "Provider 配置",
          method: "GET/POST",
          path: "/api/admin/providers",
          description: "管理上游 Provider、Base URL、凭证和健康状态。",
          table: {
            columns: ["能力", "说明"],
            rows: [
              ["GET", "读取 Provider 列表"],
              ["POST", "新增 Provider 配置"],
              ["PATCH", "更新 Provider 凭证、状态和元信息"],
            ],
          },
        },
        {
          id: "admin-routes",
          group: "管理 API",
          title: "路由策略",
          method: "GET/POST",
          path: "/api/admin/routing-rules",
          description: "维护统一模型到 Provider 资源的优先级、权重和状态。",
          details: [
            { label: "当前路由", value: countWithUnit(data.routes.length, "条", "route", "件") },
            { label: "启用路由", value: countWithUnit(activeRoutes, "条", "active route", "件") },
          ],
        },
        {
          id: "admin-audit",
          group: "管理 API",
          title: "请求日志",
          method: "GET",
          path: "/api/admin/audit/requests",
          description: "按权限查看模型调用日志、命中路由、耗时、Token 和错误信息。",
          details: [
            { label: "日志样本", value: countWithUnit(data.logs.length, "条", "log", "件") },
            { label: "用途", value: "排查 request_id 和上游响应" },
          ],
        },
      ],
    },
    {
      title: "参考",
      items: [
        {
          id: "errors",
          group: "参考",
          title: "常见错误",
          description: "按状态码定位 API Key、模型白名单、路由和额度问题。",
          table: {
            columns: ["状态", "错误码", "处理方式"],
            rows: [
              ["401", "invalid_api_key", "检查 Authorization 是否使用 TokenHub API Key"],
              ["403", "model_not_allowed", "检查 Key 的模型白名单和项目状态"],
              ["404/503", "provider_unavailable", "为统一模型配置启用路由"],
              ["429", "quota_exceeded", "检查项目额度、并发和 Provider 资源限制"],
              ["500", "upstream_error", "在请求日志里查看上游响应和 request_id"],
            ],
          },
        },
        {
          id: "sdk",
          group: "参考",
          title: "SDK 示例",
          description: "使用 OpenAI 兼容 SDK 接入 TokenHub。",
        },
        {
          id: "pipeline",
          group: "参考",
          title: "调用链路",
          description: "从鉴权到 Provider 路由的完整治理链路。",
          table: {
            columns: ["阶段", "能力", "当前数据"],
            rows: [
              ["认证", "Bearer API Key", `${formatNumber(data.keys.length)} keys`],
              ["权限", "模型白名单 + 项目状态", `${formatNumber(data.projects.length)} projects`],
              ["Provider", "上游渠道实例、凭证和健康状态", `${formatNumber(data.providers.length)} providers`],
              ["路由", "对外模型到 Provider 的优先级/权重映射", `${formatNumber(data.routes.length)} rules`],
              ["治理", "额度、审计、成本统计", `${formatNumber(data.logs.length)} logs`],
            ],
          },
        },
      ],
    },
  ];
}

function apiMethodClass(method?: string) {
  const normalized = (method || "").toLowerCase();
  if (normalized.includes("/") || normalized.includes(",")) return "mixed";
  return normalized || "muted";
}

function GatewayCopyCard({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  async function copyValue() {
    try {
      await navigator.clipboard?.writeText(value);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      setCopied(false);
    }
  }
  return (
    <article className="gateway-copy-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <button className="icon-button subtle" onClick={() => void copyValue()} type="button" title={tx("复制")}>
        {copied ? <Check size={15} /> : <Copy size={15} />}
      </button>
    </article>
  );
}

function GatewayCodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  async function copyCode() {
    try {
      await navigator.clipboard?.writeText(code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      setCopied(false);
    }
  }
  return (
    <div className="gateway-code-block">
      <button className="icon-button subtle" onClick={() => void copyCode()} type="button" title={tx("复制代码")}>
        {copied ? <Check size={15} /> : <Copy size={15} />}
      </button>
      <pre><code>{code}</code></pre>
    </div>
  );
}

function UsageView({ data, user }: { data: AppData; user: AdminUser }) {
  const modelBreakdown = data.breakdown.models ?? [];
  const showMemberBreakdown = appRole(user.role) === "team_leader";
  const showExecutiveReport = appRole(user.role) !== "user";
  return (
    <>
      {showExecutiveReport ? <ExecutiveUsageReport data={data} /> : <PersonalUsageSummary data={data} />}
      <div className="two-column">
        <DataSection title="模型用量">
          <SimpleTable
            columns={["模型", "请求", "Token", "成本"]}
            paginationKey="usage-models"
            rows={modelBreakdown.map((row) => [
              row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
        <DataSection title={showMemberBreakdown ? "成员用量" : "项目归因"}>
          <SimpleTable
            columns={[showMemberBreakdown ? "成员" : "项目", "请求", "Token", "成本"]}
            paginationKey={showMemberBreakdown ? "usage-members" : "usage-projects"}
            rows={(showMemberBreakdown ? data.breakdown.members ?? [] : data.breakdown.projects ?? []).map((row) => [
              showMemberBreakdown ? usageMemberLabel(data, row.id) : row.id,
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
      </div>
      {showMemberBreakdown ? (
        <DataSection title="项目归因">
          <SimpleTable
            columns={["项目", "请求", "Token", "成本"]}
            paginationKey="usage-projects"
            rows={(data.breakdown.projects ?? []).map((row) => [
              projectName(data, row.id),
              formatNumber(row.request_count),
              compactNumber(row.total_tokens),
              `$${formatMoney(row.estimated_cost_usd)}`,
            ])}
          />
        </DataSection>
      ) : null}
    </>
  );
}

function PersonalUsageSummary({ data }: { data: AppData }) {
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
        <ExecutiveKPI label="总 Token 消耗" value={compactNumber(data.summary.total_tokens)} detail={`${tx("输入")} ${compactNumber(data.summary.input_tokens)} / ${tx("输出")} ${compactNumber(data.summary.output_tokens)}`} />
        <ExecutiveKPI label="请求数" value={formatNumber(data.summary.request_count)} detail={countWithUnit(data.summary.usage_record_count ?? 0, "条用量记录", "usage record", "件の利用記録")} />
        <ExecutiveKPI label="估算成本" value={`$${formatMoney(data.summary.estimated_cost_usd)}`} detail={countWithUnit(data.summary.errors, "个错误", "error", "件のエラー")} />
        <ExecutiveKPI label="可见项目" value={formatNumber(data.projects.length)} detail={tx("按当前账号权限汇总")} />
      </div>
    </section>
  );
}

type ExecutiveDepartmentRow = UsageBreakdownRow & {
  name: string;
  member_count: number;
};

type ExecutiveMemberRow = UsageBreakdownRow & {
  name: string;
  department: string;
};

function ExecutiveUsageReport({ data }: { data: AppData }) {
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
  const tokenDetail = `${tx("输入")} ${compactNumber(totalInput)} / ${tx("输出")} ${compactNumber(totalOutput)}`;
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

function ExecutiveKPI({ label, value, detail }: { label: string; value: string; detail: string }) {
  return (
    <article className="executive-kpi">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </article>
  );
}

function ExecutiveDepartmentChart({ rows }: { rows: ExecutiveDepartmentRow[] }) {
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

function ExecutiveDepartmentRanking({ rows, totalTokens }: { rows: ExecutiveDepartmentRow[]; totalTokens: number }) {
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

function ExecutiveMemberTable({ rows, totalTokens }: { rows: ExecutiveMemberRow[]; totalTokens: number }) {
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

function executiveDepartmentRows(data: AppData): ExecutiveDepartmentRow[] {
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

function executiveMemberRows(data: AppData): ExecutiveMemberRow[] {
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

function hasUsage(row: UsageBreakdownRow) {
  return row.request_count > 0 || row.input_tokens > 0 || row.output_tokens > 0 || row.total_tokens > 0 || row.estimated_cost_usd > 0;
}

function sortUsageRows<T extends UsageBreakdownRow>(rows: T[]): T[] {
  return rows
    .slice()
    .sort((left, right) => right.total_tokens - left.total_tokens || right.request_count - left.request_count || right.estimated_cost_usd - left.estimated_cost_usd);
}

function addUsageRow(target: UsageBreakdownRow, source: UsageBreakdownRow) {
  target.request_count += source.request_count;
  target.input_tokens += source.input_tokens;
  target.output_tokens += source.output_tokens;
  target.total_tokens += source.total_tokens;
  target.estimated_cost_usd += source.estimated_cost_usd;
}

function findUsageUser(data: AppData, id: string) {
  return data.users.find((item) => item.id === id || item.username === id || item.email === id);
}

function membersInCostCenter(data: AppData, costCenterID: string) {
  const projectIDs = data.projects
    .filter((project) => project.cost_center === costCenterID)
    .map((project) => project.id);
  if (projectIDs.length === 0) return 0;
  const teamIDs = new Set(data.projects.filter((project) => projectIDs.includes(project.id) && project.team_id).map((project) => project.team_id as string));
  return data.users.filter((user) => user.team_id && teamIDs.has(user.team_id)).length;
}

function shortLabel(value: string, maxLength: number) {
  if (value.length <= maxLength) return value;
  return `${value.slice(0, maxLength - 1)}…`;
}

function BillingView({ data, user }: { data: AppData; user: AdminUser }) {
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

function AuditView({ api, data, user }: { api: ApiContext; data: AppData; user: AdminUser }) {
  const [activeAuditTab, setActiveAuditTab] = useState<"requests" | "admin">("requests");
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState<"all" | "ok" | "error">("all");
  const [selectedRequestID, setSelectedRequestID] = useState("");
  const [detail, setDetail] = useState<RequestDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState("");
  const showAdminAudit = canViewAdminAudit(user);

  useEffect(() => {
    if (!showAdminAudit && activeAuditTab === "admin") {
      setActiveAuditTab("requests");
    }
  }, [activeAuditTab, showAdminAudit]);

  const filteredLogs = useMemo(() => {
    const keyword = query.trim().toLowerCase();
    return data.logs.filter((log) => {
      if (statusFilter === "ok" && log.status_code >= 400) return false;
      if (statusFilter === "error" && log.status_code < 400) return false;
      if (!keyword) return true;
      return [
        log.request_id,
        log.project_id,
        projectName(data, log.project_id),
        log.api_key_id,
        log.model,
        log.provider_id,
        providerAuditLabel(data, log),
        log.provider_resource_id,
        log.provider_model,
        log.error_code,
        String(log.status_code),
      ]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(keyword));
    });
  }, [data, query, statusFilter]);

  const requestLogPagination = usePagination(filteredLogs.length, `request-logs:${statusFilter}:${query.trim()}`);
  const visibleLogs = useMemo(
    () => filteredLogs.slice(requestLogPagination.startIndex, requestLogPagination.endIndex),
    [filteredLogs, requestLogPagination.startIndex, requestLogPagination.endIndex],
  );

  useEffect(() => {
    if (activeAuditTab !== "requests") return;
    if (filteredLogs.length === 0) {
      setSelectedRequestID("");
      setDetail(null);
      return;
    }
    const selectedVisible = visibleLogs.some((log) => log.request_id === selectedRequestID);
    if (!selectedRequestID || !selectedVisible) {
      setSelectedRequestID((visibleLogs[0] ?? filteredLogs[0]).request_id);
    }
  }, [activeAuditTab, filteredLogs, selectedRequestID, visibleLogs]);

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
    const total = data.logs.length;
    const failures = data.logs.filter((log) => log.status_code >= 400).length;
    const averageLatency = total
      ? Math.round(data.logs.reduce((sum, log) => sum + (log.latency_ms || 0), 0) / total)
      : 0;
    const successRate = total ? Math.round(((total - failures) / total) * 100) : 0;
    return { total, failures, averageLatency, successRate };
  }, [data.logs]);

  const filters = [
    { key: "all", label: `${tx("全部")} ${data.logs.length}` },
    { key: "ok", label: `${tx("成功")} ${data.logs.length - requestStats.failures}` },
    { key: "error", label: `${tx("失败")} ${requestStats.failures}` },
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
          <strong>{formatNumber(data.logs.length)}</strong>
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
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder={tx("搜索请求 ID、模型、Provider、状态码")}
                />
              </label>
              <div className="request-filter-tabs" role="tablist" aria-label={tx("请求状态筛选")}>
                {filters.map((filter) => (
                  <button
                    key={filter.key}
                    type="button"
                    className={statusFilter === filter.key ? "active" : ""}
                    onClick={() => setStatusFilter(filter.key)}
                  >
                    {filter.label}
                  </button>
                ))}
              </div>
            </div>

            <div className="metrics request-metrics">
              <RequestMetric label="总请求" value={formatNumber(requestStats.total)} icon={Activity} />
              <RequestMetric label="成功率" value={`${requestStats.successRate}%`} icon={Check} />
              <RequestMetric label="失败请求" value={formatNumber(requestStats.failures)} icon={AlertCircle} />
              <RequestMetric label="平均延迟" value={`${requestStats.averageLatency}ms`} icon={Gauge} />
            </div>

            <div className="request-history-layout">
              <div className="request-list-panel">
                <div className="request-list-head">
                  <span>{tx("请求列表")}</span>
                  <strong>{countWithUnit(filteredLogs.length, "条", "record", "件")}</strong>
                </div>
                {filteredLogs.length === 0 ? (
                  <div className="compact-empty">{tx("没有匹配的请求记录")}</div>
                ) : (
                  <div className="request-list" role="list">
                    {visibleLogs.map((log) => (
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
                        </span>
                        <span className="request-row-tail">
                          <StatusPill status={log.status_code >= 400 ? "error" : "ok"} label={String(log.status_code || "-")} />
                          <span>{log.latency_ms || 0}ms</span>
                        </span>
                      </button>
                    ))}
                  </div>
                )}
                <PaginationControls pagination={requestLogPagination} totalItems={filteredLogs.length} />
              </div>

              <RequestDetailPanel
                data={data}
                requestID={selectedRequestID}
                detail={detail?.log.request_id === selectedRequestID ? detail : null}
                loading={detailLoading}
                error={detailError}
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

function RequestDetailPanel({
  data,
  requestID,
  detail,
  loading,
  error,
}: {
  data: AppData;
  requestID: string;
  detail: RequestDetail | null;
  loading: boolean;
  error: string;
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
      output_tokens: sum.output_tokens + (item.output_tokens || 0),
      total_tokens: sum.total_tokens + (item.total_tokens || 0),
      estimated_cost_usd: sum.estimated_cost_usd + (item.estimated_cost_usd || 0),
    }),
    { input_tokens: 0, output_tokens: 0, total_tokens: 0, estimated_cost_usd: 0 },
  );
  const isError = log.status_code >= 400;

  async function copyRequestID() {
    await navigator.clipboard?.writeText(log.request_id).catch(() => undefined);
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
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
        <div className="request-usage-strip">
          <UsageStat label="输入" value={compactNumber(usageTotals.input_tokens)} />
          <UsageStat label="输出" value={compactNumber(usageTotals.output_tokens)} />
          <UsageStat label="总量" value={compactNumber(usageTotals.total_tokens)} />
          <UsageStat label="估算成本" value={`$${formatMoney(usageTotals.estimated_cost_usd)}`} />
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

function DetailField({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="detail-field">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
    </div>
  );
}

function UsageStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="usage-stat">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
    </div>
  );
}

function RequestPayloadSection({ payload }: { payload: RequestPayloadLog | null }) {
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

function PayloadBlock({ title, body, truncated }: { title: string; body: string; truncated: boolean }) {
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

function RequestMetric({
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

function CrudView<T>({
  config,
  data,
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
  onProjectMemberCreate,
  onProjectMemberEdit,
  onProjectMemberDelete,
  onToolbarAction,
}: {
  config: ResourceConfig<T>;
  data: AppData;
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
  onProjectMemberCreate?: (project: Project) => void;
  onProjectMemberEdit?: (member: AdminResource) => void;
  onProjectMemberDelete?: (member: AdminResource) => void;
  onToolbarAction: (action: ToolbarAction) => void;
}) {
  const [selectedTeamID, setSelectedTeamID] = useState("");
  const [selectedProjectID, setSelectedProjectID] = useState("");
  const isTeamView = config.view === "teams";
  const isProjectView = config.view === "projects";
  const selectedTeam = isTeamView
    ? (items as AdminResource[]).find((item) => item.id === selectedTeamID)
    : undefined;
  const selectedProject = isProjectView
    ? (items as Project[]).find((item) => item.id === selectedProjectID)
    : undefined;

  useEffect(() => {
    if (!isTeamView) return;
    const teamItems = items as AdminResource[];
    if (!selectedTeamID || !teamItems.some((item) => item.id === selectedTeamID)) {
      setSelectedTeamID("");
    }
  }, [isTeamView, items, selectedTeamID]);

  useEffect(() => {
    if (!isProjectView) return;
    const projectItems = items as Project[];
    if (!selectedProjectID || !projectItems.some((item) => item.id === selectedProjectID)) {
      setSelectedProjectID("");
    }
  }, [isProjectView, items, selectedProjectID]);

  const detailPanelOpen = (isTeamView && selectedTeam) || (isProjectView && selectedProject);

  return (
    <DataSection title={config.eyebrow}>
      {config.view === "api-keys" ? <APIKeyFlowHint data={data} /> : null}
      {config.view === "routes" ? <RouteStrategyHint data={data} /> : null}
      {config.view === "providers" || config.view === "models" ? (
        <ModelCategoryTabs
          data={data}
          view={config.view}
          active={categoryFilter}
          onChange={onCategoryFilter}
        />
      ) : null}
      {config.view === "providers" ? <ProviderAvailabilityMonitor data={data} providers={monitorItems as Provider[]} /> : null}
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
              {config.view === "notification-channels" ? `${tx("配置")} ${notificationChannelLabel(categoryFilter)}` : tx(config.createLabel ?? "新增")}
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
          <EntityTable
            config={config}
            data={data}
            items={items}
            loading={loading}
            query={query}
            onCreate={config.create ? onCreate : undefined}
            onEdit={onEdit}
            onDelete={onDelete}
            onAction={onAction}
            onRowClick={
              isTeamView
                ? (item) => setSelectedTeamID((item as AdminResource).id)
                : isProjectView
                  ? (item) => setSelectedProjectID((item as Project).id)
                  : undefined
            }
            selectedRowID={isTeamView ? selectedTeam?.id : isProjectView ? selectedProject?.id : undefined}
          />
          <PaginationControls pagination={pagination} totalItems={totalItems} />
        </div>
        {isTeamView && selectedTeam ? (
          <TeamMembersPanel data={data} team={selectedTeam} onClose={() => setSelectedTeamID("")} />
        ) : null}
        {isProjectView && selectedProject ? (
          <ProjectQuotaPanel
            data={data}
            project={selectedProject}
            onClose={() => setSelectedProjectID("")}
            onAction={(action) => onAction(action as unknown as ResourceAction<T>, selectedProject as T)}
            onCreateMember={() => onProjectMemberCreate?.(selectedProject)}
            onEditMember={(member) => onProjectMemberEdit?.(member)}
            onDeleteMember={(member) => onProjectMemberDelete?.(member)}
          />
        ) : null}
      </div>
    </DataSection>
  );
}

type ProviderMonitorTone = "healthy" | "degraded" | "down";
type ProviderProbeTone = "ok" | "warn" | "down" | "na";
type ProviderTrendTone = "success" | "warning" | "failure" | "none";

type ProviderMonitorRow = {
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
  qualityScore: number;
  trend: ProviderTrendTone[];
};

function ProviderAvailabilityMonitor({ data, providers }: { data: AppData; providers: Provider[] }) {
  if (providers.length === 0) return null;
  const rows = providerMonitorRows(data, providers);
  const summary = providerMonitorSummary(rows);
  return (
    <section className="provider-monitor-card" aria-label={tx("Provider 可用性监控")}>
      <div className="provider-monitor-head">
        <div>
          <p className="eyebrow">Provider Availability</p>
          <h2>{tx("Provider 可用性监控")}</h2>
          <span>{tx("按健康检测、账号资源和真实请求日志汇总上游渠道可用性。")}</span>
        </div>
        <div className="provider-monitor-summary" aria-label={tx("Provider 健康摘要")}>
          <span><strong>{summary.healthy}</strong>{tx("正常")}</span>
          <span><strong>{summary.degraded}</strong>{tx("降级")}</span>
          <span><strong>{summary.down}</strong>{tx("故障")}</span>
        </div>
      </div>
      <div className="provider-monitor-table-wrap">
        <table className="provider-monitor-table">
          <thead>
            <tr>
              <th>{tx("服务商 / 通道")}</th>
              <th>{tx("综合状态")}</th>
              <th>{tx("基础监控 · L1/L2")}</th>
              <th>{tx("真实监控 · L3")}</th>
              <th>{tx("真实延迟")}</th>
              <th>{tx("24H 可用率")}</th>
              <th>{tx("质量评分")}</th>
              <th>{tx("近30天趋势")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => (
              <tr key={row.provider.id}>
                <td>
                  <div className="provider-monitor-name">
                    <span className={`provider-monitor-avatar ${row.statusTone}`}>{providerInitial(row.provider)}</span>
                    <div>
                      <strong>{row.provider.name || row.provider.id}</strong>
                      <span>{providerTypeLabel(row.provider.type)} · {row.activeRouteCount}/{row.routeCount || 0} {tx("启用路由")} · {row.resources.length || 0} {tx("账号资源")}</span>
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
                  </div>
                </td>
                <td>
                  <ProviderProbeLine tone={row.basicPrimaryTone} detail={row.basicPrimaryDetail} />
                  <ProviderProbeLine tone={row.basicSecondaryTone} detail={row.basicSecondaryDetail} />
                </td>
                <td>
                  <ProviderProbeLine tone={row.realTone} detail={row.realDetail} />
                  <small className="provider-monitor-subtle">{row.observed24h ? tx("真实请求样本") : tx("无请求样本")}</small>
                </td>
                <td><strong className="provider-monitor-metric">{latencyDisplay(row.latencyMS)}</strong></td>
                <td><strong className="provider-monitor-metric">{providerPercent(row.availability24h)}</strong></td>
                <td>
                  <div className="provider-quality-score">
                    <strong>{row.qualityScore}</strong>
                    <span><i style={{ width: `${row.qualityScore}%` }} /></span>
                  </div>
                </td>
                <td>
                  <div className="provider-trend-bars" aria-label={tx("近30天趋势")}>
                    {row.trend.map((tone, index) => <span className={tone} key={`${row.provider.id}-trend-${index}`} />)}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="provider-monitor-legend">
        <span><i className="success" />{tx("正常")}</span>
        <span><i className="warning" />{tx("降级/慢响应")}</span>
        <span><i className="failure" />{tx("故障")}</span>
        <em>{tx("真实监控来自最近请求日志；基础监控来自 Provider 和账号资源健康状态。")}</em>
      </div>
    </section>
  );
}

function ProviderProbeLine({ tone, detail }: { tone: ProviderProbeTone; detail: string }) {
  return (
    <span className={`provider-probe-line ${tone}`}>
      <i />
      {tx(providerProbeLabel(tone))}
      <small>{detail}</small>
    </span>
  );
}

function providerMonitorRows(data: AppData, providers: Provider[]): ProviderMonitorRow[] {
  return providers
    .slice()
    .sort((left, right) => (left.priority - right.priority) || left.name.localeCompare(right.name))
    .map((provider) => providerMonitorRow(data, provider));
}

function providerMonitorRow(data: AppData, provider: Provider): ProviderMonitorRow {
  const resources = data.providerResources.filter((resource) => resource.provider_id === provider.id);
  const routes = providerRoutesFor(provider, data);
  const logs = providerLogsFor(data, provider, resources);
  const now = Date.now();
  const recent24h = logs.filter((log) => now - safeTime(log.created_at) <= 24 * 60 * 60 * 1000);
  const success24h = recent24h.filter((log) => !requestLogFailed(log));
  const warning24h = recent24h.filter((log) => !requestLogFailed(log) && (log.status_code >= 300 || log.latency_ms >= 5000));
  const failed24h = recent24h.length - success24h.length;
  const observed24h = recent24h.length > 0;
  const activeResources = resources.filter((resource) => resource.status === "active");
  const healthyResources = activeResources.filter((resource) => resource.healthy);
  const healthyProvider = provider.status === "active" && provider.healthy;
  const resourceScore = activeResources.length > 0 ? (healthyResources.length / activeResources.length) * 100 : (healthyProvider ? 100 : 0);
  const availability24h = observed24h ? (success24h.length / recent24h.length) * 100 : (healthyProvider ? 100 : 0);
  const latencyLogs = (success24h.length ? success24h : logs.filter((log) => !requestLogFailed(log))).filter((log) => log.latency_ms > 0);
  const latencyMS = percentileLatency(latencyLogs, 0.5);
  const statusTone = providerMonitorTone(provider, observed24h, availability24h, warning24h.length, failed24h, activeResources.length, healthyResources.length);
  const activeRouteCount = routes.filter((route) => route.status === "active").length;
  return {
    provider,
    resources,
    routeCount: routes.length,
    activeRouteCount,
    statusTone,
    statusLabel: providerStatusLabel(statusTone),
    statusDetail: providerStatusDetail(provider, logs, resources),
    basicPrimaryTone: healthyProvider ? "ok" : "down",
    basicPrimaryDetail: provider.status === "active" ? tx("Provider 在线") : enumValueLabel(provider.status),
    basicSecondaryTone: providerResourceProbeTone(activeResources.length, healthyResources.length),
    basicSecondaryDetail: activeResources.length > 0
      ? `${formatNumber(healthyResources.length)}/${formatNumber(activeResources.length)} ${tx("资源健康")}`
      : tx("未配置账号资源"),
    realTone: providerRealProbeTone(observed24h, availability24h, warning24h.length, failed24h),
    realDetail: observed24h
      ? `${providerPercent(availability24h)} · ${formatNumber(recent24h.length)} ${tx("次请求")}`
      : tx("无真实请求"),
    latencyMS,
    availability24h,
    observed24h,
    qualityScore: providerQualityScore(availability24h, latencyMS, resourceScore, observed24h, healthyProvider),
    trend: providerTrend(data, provider, resources),
  };
}

function providerMonitorSummary(rows: ProviderMonitorRow[]) {
  return rows.reduce(
    (summary, row) => {
      summary[row.statusTone] += 1;
      return summary;
    },
    { healthy: 0, degraded: 0, down: 0 } as Record<ProviderMonitorTone, number>,
  );
}

function providerLogsFor(data: AppData, provider: Provider, resources: ProviderResource[]) {
  const resourceIDs = new Set(resources.map((resource) => resource.id));
  return data.logs
    .filter((log) => log.provider_id === provider.id || (log.provider_resource_id ? resourceIDs.has(log.provider_resource_id) : false))
    .sort((left, right) => safeTime(left.created_at) - safeTime(right.created_at));
}

function providerMonitorTone(provider: Provider, observed: boolean, availability: number, warnings: number, failures: number, activeResources: number, healthyResources: number): ProviderMonitorTone {
  if (provider.status !== "active" || !provider.healthy) return "down";
  if (observed && (availability < 90 || failures > 0 && availability < 95)) return "down";
  if (activeResources > 0 && healthyResources === 0) return "down";
  if ((observed && availability < 99) || warnings > 0 || (activeResources > 0 && healthyResources < activeResources)) return "degraded";
  return "healthy";
}

function providerStatusLabel(tone: ProviderMonitorTone) {
  if (tone === "healthy") return "Healthy";
  if (tone === "degraded") return "Degraded";
  return "Functional Down";
}

function providerStatusDetail(provider: Provider, logs: RequestLog[], resources: ProviderResource[]) {
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

function providerResourceProbeTone(total: number, healthy: number): ProviderProbeTone {
  if (total === 0) return "na";
  if (healthy === total) return "ok";
  if (healthy > 0) return "warn";
  return "down";
}

function providerRealProbeTone(observed: boolean, availability: number, warnings: number, failures: number): ProviderProbeTone {
  if (!observed) return "na";
  if (availability < 90 || failures > 0 && availability < 95) return "down";
  if (availability < 99 || warnings > 0 || failures > 0) return "warn";
  return "ok";
}

function providerProbeLabel(tone: ProviderProbeTone) {
  if (tone === "ok") return "ok";
  if (tone === "warn") return "warn";
  if (tone === "down") return "down";
  return "na";
}

function percentileLatency(logs: RequestLog[], percentile: number) {
  const values = logs.map((log) => log.latency_ms || 0).filter((value) => value > 0).sort((left, right) => left - right);
  if (values.length === 0) return 0;
  const index = Math.min(values.length - 1, Math.max(0, Math.floor((values.length - 1) * percentile)));
  return values[index];
}

function providerQualityScore(availability: number, latencyMS: number, resourceScore: number, observed: boolean, healthyProvider: boolean) {
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

function providerTrend(data: AppData, provider: Provider, resources: ProviderResource[]) {
  const logs = providerLogsFor(data, provider, resources);
  const days = 30;
  const now = new Date();
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  return Array.from({ length: days }, (_, index) => {
    const dayStart = today - (days - 1 - index) * 24 * 60 * 60 * 1000;
    const dayEnd = dayStart + 24 * 60 * 60 * 1000;
    const dayLogs = logs.filter((log) => {
      const time = safeTime(log.created_at);
      return time >= dayStart && time < dayEnd;
    });
    if (dayLogs.length === 0) return "none" as ProviderTrendTone;
    const failures = dayLogs.filter((log) => requestLogFailed(log)).length;
    const slow = dayLogs.filter((log) => !requestLogFailed(log) && log.latency_ms >= 5000).length;
    const availability = ((dayLogs.length - failures) / dayLogs.length) * 100;
    if (availability < 90) return "failure" as ProviderTrendTone;
    if (failures > 0 || slow > 0 || availability < 99) return "warning" as ProviderTrendTone;
    return "success" as ProviderTrendTone;
  });
}

function providerInitial(provider: Provider) {
  return (provider.name || provider.type || provider.id || "P").trim().slice(0, 1).toUpperCase();
}

function providerPercent(value: number) {
  return `${clampNumber(value, 0, 100).toFixed(1)}%`;
}

function safeTime(value: string | undefined) {
  if (!value) return 0;
  const time = new Date(value).getTime();
  return Number.isFinite(time) ? time : 0;
}

function timeLabel(value: string | undefined) {
  const time = safeTime(value);
  if (!time) return "-";
  return new Intl.DateTimeFormat(languageLocale(), { hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(new Date(time));
}

function clampNumber(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, Number.isFinite(value) ? value : min));
}

function TeamMembersPanel({ data, team, onClose }: { data: AppData; team: AdminResource; onClose: () => void }) {
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

type ProjectQuotaValues = {
  status: string;
  daily_requests: string;
  monthly_requests: string;
  daily_tokens: string;
  monthly_tokens: string;
  daily_cost_usd: string;
  monthly_cost_usd: string;
  max_concurrency: string;
};

const projectQuotaFields: Array<{ key: keyof ProjectQuotaValues; label: string; suffix?: string }> = [
  { key: "daily_requests", label: "日请求" },
  { key: "monthly_requests", label: "月请求" },
  { key: "daily_tokens", label: "日 Token" },
  { key: "monthly_tokens", label: "月 Token" },
  { key: "daily_cost_usd", label: "日成本", suffix: "USD" },
  { key: "monthly_cost_usd", label: "月成本", suffix: "USD" },
  { key: "max_concurrency", label: "最大并发" },
];

function ProjectQuotaPanel({
  data,
  project,
  onClose,
  onAction,
  onCreateMember,
  onEditMember,
  onDeleteMember,
}: {
  data: AppData;
  project: Project;
  onClose: () => void;
  onAction: (action: ResourceAction<Project>) => void;
  onCreateMember?: () => void;
  onEditMember?: (member: AdminResource) => void;
  onDeleteMember?: (member: AdminResource) => void;
}) {
  const quota = projectQuotaPolicy(data, project);
  const [values, setValues] = useState<ProjectQuotaValues>(() => projectQuotaValues(quota));

  useEffect(() => {
    setValues(projectQuotaValues(quota));
  }, [project.id, quota?.id]);

  const hasQuota = Boolean(quota);
  const quotaIssue = projectQuotaIssue(data, project);
  const pendingApproval = pendingProjectQuotaApproval(data, project);
  const members = projectMembersForProject(data, project.id);
  return (
    <div className="project-quota-panel project-detail-panel">
      <div className="project-quota-head">
        <div>
          <span>{tx("项目详情")}</span>
          <strong>{project.name || project.id}</strong>
        </div>
        <button className="icon-button subtle" onClick={onClose} type="button" title={tx("关闭项目详情")}>
          <X size={15} />
        </button>
      </div>
      <div className="project-quota-body">
        <div className="project-panel-section-head">
          <div>
            <strong>{tx("项目成员")}</strong>
            <span>{countWithUnit(members.length, "人", "member", "人")}</span>
          </div>
          <button className="secondary-button compact-button" onClick={onCreateMember} type="button">
            <Plus size={15} />
            {tx("添加成员")}
          </button>
        </div>
        <div className="project-member-list">
          {members.length === 0 ? (
            <div className="empty compact-empty">{tx("暂无项目成员")}</div>
          ) : members.map((member) => (
            <ProjectMemberRow
              key={member.id}
              data={data}
              member={member}
              onEdit={() => onEditMember?.(member)}
              onDelete={() => onDeleteMember?.(member)}
            />
          ))}
        </div>

        <div className="project-panel-section-head">
          <div>
            <strong>{tx("项目额度")}</strong>
            <span>{hasQuota ? tx("已配置项目专属额度") : tx("未配置项目专属额度")}</span>
          </div>
        </div>
        <div className="quota-status-row">
          <div>
            <strong>{hasQuota ? tx("已配置项目专属额度") : tx("未配置项目专属额度")}</strong>
            <span>{tx("留空或填 0 表示该项不限额；Key 自身额度仍会叠加生效。")}</span>
          </div>
          <StatusPill status={values.status || "active"} />
        </div>

        {quotaIssue || pendingApproval ? (
          <div className="quota-request-banner">
            <div>
              <strong>{pendingApproval ? tx("已有额度提升申请待审批") : tx("最近触发了项目额度限制")}</strong>
              <span>
                {pendingApproval
                  ? `${approvalTriggerLabel(pendingApproval.trigger)} ${pendingApproval.id}，${tx("可在审批记录中处理。")}`
                  : `${formatNumber(quotaIssue?.count ?? 0)} ${tx("次额度不足，请填写希望提升后的目标额度再提交审批。")}`}
              </span>
            </div>
            {pendingApproval ? <StatusPill status="pending" label="待审批" /> : <StatusPill status="warning" label="需提升" />}
          </div>
        ) : null}

        <label className="field">
          <span>{tx("状态")}</span>
          <select value={values.status} onChange={(event) => setValues((current) => ({ ...current, status: event.target.value }))}>
            <option value="active">{tx("启用")}</option>
            <option value="disabled">{tx("停用")}</option>
          </select>
        </label>

        <div className="project-quota-grid">
          {projectQuotaFields.map((field) => (
            <label className="field" key={field.key}>
              <span>{tx(field.label)}</span>
              <input
                min="0"
                type="number"
                value={values[field.key]}
                onChange={(event) => setValues((current) => ({ ...current, [field.key]: event.target.value }))}
              />
              {field.suffix ? <small>{field.suffix}</small> : null}
            </label>
          ))}
        </div>

        <div className="project-quota-actions">
          {quotaIssue && !pendingApproval ? (
            <button
              className="secondary-button"
              onClick={() =>
                onAction({
                  label: "提升额度申请",
                  title: "提交项目额度提升审批",
                  run: (ctx) => requestProjectQuotaIncrease(ctx, project, quota, values),
                  doneMessage: () => `${project.name || project.id} 的额度提升申请已提交`,
                })
              }
              type="button"
            >
              {tx("提升额度申请")}
            </button>
          ) : null}
          <button
            className="button"
            onClick={() =>
              onAction({
                label: "保存额度",
                title: "保存项目额度",
                run: (ctx) => saveProjectQuota(ctx, project, quota, values),
                doneMessage: () => `${project.name || project.id} 的额度已保存`,
              })
            }
            type="button"
          >
            {tx("保存额度")}
          </button>
        </div>
      </div>
    </div>
  );
}

function ProjectMemberRow({
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

function ReportsView({
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

function ModelCategoryTabs({
  data,
  view,
  active,
  onChange,
}: {
  data: AppData;
  view: ViewKey;
  active: string;
  onChange: (value: string) => void;
}) {
  const tabs = modelCategoryTabs(data, view);
  if (tabs.length <= 1) return null;
  return (
    <div className="category-tabs" role="tablist" aria-label={tx("模型分类")}>
      {tabs.map((tab) => (
        <button
          className={active === tab.key ? "category-tab active" : "category-tab"}
          key={tab.key}
          onClick={() => onChange(tab.key)}
          type="button"
        >
          <span>{tx(tab.label)}</span>
          <em>{tab.count}</em>
        </button>
      ))}
    </div>
  );
}

function NotificationChannelTabs({
  data,
  active,
  onChange,
}: {
  data: AppData;
  active: string;
  onChange: (value: string) => void;
}) {
  const tabs = notificationChannelTabs(data);
  return (
    <div className="category-tabs" role="tablist" aria-label={tx("通知渠道类型")}>
      {tabs.map((tab) => (
        <button
          aria-selected={active === tab.key}
          className={active === tab.key ? "category-tab active" : "category-tab"}
          key={tab.key}
          onClick={() => onChange(tab.key)}
          role="tab"
          type="button"
        >
          <span>{tab.label}</span>
          <em>{tab.count}</em>
        </button>
      ))}
    </div>
  );
}

function ModelCatalogView({
  config,
  data,
  readOnly = false,
  onCreate,
  onEdit,
  onDelete,
  onAction,
}: {
  config: ResourceConfig<Model>;
  data: AppData;
  readOnly?: boolean;
  onCreate: () => void;
  onEdit: (item: Model) => void;
  onDelete: (item: Model) => void;
  onAction: (action: ResourceAction<Model>, item: Model) => void;
}) {
  const [category, setCategory] = useState("all");
  const [capability, setCapability] = useState("all");
  const [query, setQuery] = useState("");
  const categories = modelCatalogCategories(data);
  const capabilities = modelCatalogCapabilityTabs(data);
  const filtered = useMemo(
    () => filterModelCatalog(data.models, data, category, capability, query),
    [data, category, capability, query],
  );

  return (
    <DataSection title={config.eyebrow}>
      <div className="model-catalog model-catalog-table-mode">
        <section className="model-catalog-main">
          <div className="model-category-strip">
            <div className="model-category-strip-head">
              <strong>{tx("模型大类")}</strong>
              <span>{data.models.length} {tx("个模型")}</span>
            </div>
            <div className="model-category-tabs" role="tablist" aria-label={tx("模型大类")}>
              {categories.map((item) => (
                <button
                  aria-selected={category === item.key}
                  className={category === item.key ? "model-category-tab active" : "model-category-tab"}
                  key={item.key}
                  onClick={() => setCategory(item.key)}
                  role="tab"
                  type="button"
                >
                  <ModelBrandIcon compact category={item.key} label={tx(item.label)} />
                  <strong>{tx(item.label)}</strong>
                  <em>{item.count}</em>
                </button>
              ))}
            </div>
          </div>

          <div className="model-filterbar">
            <div className="model-capability-tabs" role="tablist" aria-label={tx("模型能力筛选")}>
              {capabilities.map((item) => (
                <button
                  aria-selected={capability === item.key}
                  className={capability === item.key ? "model-capability-tab active" : "model-capability-tab"}
                  key={item.key}
                  onClick={() => setCapability(item.key)}
                  role="tab"
                type="button"
              >
                <item.icon size={14} />
                <span>{tx(item.label)}</span>
                <em>{item.count}</em>
              </button>
              ))}
            </div>
            <div className="model-catalog-actions">
              <div className="search-box model-search">
                <Search size={16} />
                <input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder={tx("搜索模型名称或 ID")}
                />
              </div>
              {!readOnly ? (
                <button className="button" onClick={onCreate} type="button">
                  <Plus size={17} />
                  {tx(config.createLabel ?? "新增模型")}
                </button>
              ) : null}
            </div>
          </div>

          <div className="model-catalog-summary">
            <span>{tx(modelCatalogFilterLabel(categories, category))}</span>
            <strong>{filtered.length}</strong>
            <em>{tx("个匹配模型")}</em>
          </div>
          <div className="model-availability-note">
            <AlertCircle size={15} />
            <span>{tx(readOnly ? "普通用户看到的是当前账号可见的模型；实际调用还会受项目 Key 白名单和项目权限限制。" : "可用模型需要同时满足：模型目录启用、至少一条路由启用、Provider 或账号资源健康。")}</span>
          </div>

          {filtered.length === 0 ? (
            <div className="empty model-catalog-empty">
              {modelCatalogEmptyText(data, readOnly, query)}
            </div>
          ) : (
            <ModelCatalogPriceTable
              models={filtered}
              data={data}
              readOnly={readOnly}
              actions={readOnly ? [] : config.actions ?? []}
              onAction={onAction}
              onEdit={readOnly ? undefined : onEdit}
              onDelete={readOnly ? undefined : onDelete}
            />
          )}
        </section>
      </div>
    </DataSection>
  );
}

type ModelCatalogPriceSortKey = "default" | "name" | "input" | "output" | "cache" | "context" | "monthly" | "index";
type ModelCatalogPriceSortDirection = "asc" | "desc";
type ModelCatalogPriceSort = {
  key: ModelCatalogPriceSortKey;
  direction: ModelCatalogPriceSortDirection;
};
type ModelCatalogPriceRowData = ReturnType<typeof modelCatalogPriceRow>;

function ModelCatalogPriceTable({
  models,
  data,
  readOnly,
  actions,
  onAction,
  onEdit,
  onDelete,
}: {
  models: Model[];
  data: AppData;
  readOnly: boolean;
  actions: ResourceAction<Model>[];
  onAction: (action: ResourceAction<Model>, item: Model) => void;
  onEdit?: (item: Model) => void;
  onDelete?: (item: Model) => void;
}) {
  const [sort, setSort] = useState<ModelCatalogPriceSort>({ key: "default", direction: "asc" });
  const defaultSorted = modelCatalogPriceSortedModels(models);
  const baseline = modelCatalogPriceBaseline(defaultSorted);
  const rows = modelCatalogSortRows(defaultSorted.map((model) => modelCatalogPriceRow(model, data, readOnly, baseline)), sort);
  const maxIndex = Math.max(1, ...rows.map((row) => row.priceIndex || 0));
  return (
    <div className="model-price-table-wrap">
      <table className={readOnly ? "model-price-table" : "model-price-table admin"}>
        <thead>
          <tr>
            <th aria-sort={modelCatalogSortAria(sort, "name")}>
              <ModelCatalogSortHeader label="模型" sortKey="name" sort={sort} onSort={setSort} />
            </th>
            <th>{tx("类型")}</th>
            <th aria-sort={modelCatalogSortAria(sort, "input")}>
              <ModelCatalogSortHeader label="输入价" sortKey="input" sort={sort} onSort={setSort} />
            </th>
            <th aria-sort={modelCatalogSortAria(sort, "output")}>
              <ModelCatalogSortHeader label="输出价" sortKey="output" sort={sort} onSort={setSort} />
            </th>
            <th aria-sort={modelCatalogSortAria(sort, "cache")}>
              <ModelCatalogSortHeader label="缓存读" sortKey="cache" sort={sort} onSort={setSort} />
            </th>
            <th aria-sort={modelCatalogSortAria(sort, "context")}>
              <ModelCatalogSortHeader label="上下文" sortKey="context" sort={sort} onSort={setSort} />
            </th>
            <th aria-sort={modelCatalogSortAria(sort, "monthly")}>
              <ModelCatalogSortHeader label="估算月成本" sortKey="monthly" sort={sort} onSort={setSort} />
            </th>
            <th aria-sort={modelCatalogSortAria(sort, "index")}>
              <ModelCatalogSortHeader label="价格指数" sortKey="index" sort={sort} onSort={setSort} />
            </th>
            <th>{tx("来源")}</th>
            {!readOnly ? <th>{tx("操作")}</th> : null}
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const visibleActions = actions.filter((action) => !action.visible || action.visible(row.model));
            return (
              <tr className={row.availability.tone === "blocked" && !readOnly ? "unrouted" : undefined} key={row.model.name}>
                <td>
                  <div className="model-price-name">
                    <ModelBrandIcon category={row.category} label={row.categoryLabel} />
                    <div>
                      <strong>{modelDisplayTitle(row.model)}</strong>
                      <span>{row.categoryLabel} · {row.model.name}</span>
                    </div>
                  </div>
                </td>
                <td>
                  <span className={`model-type-badge ${row.typeTone}`}>{tx(row.typeLabel)}</span>
                </td>
                <td><strong className="model-price-number">{modelCatalogPriceValue(row.inputPrice)}</strong></td>
                <td><strong className="model-price-number output">{modelCatalogPriceValue(row.outputPrice)}</strong></td>
                <td><strong className={row.cacheReadPrice ? "model-price-number" : "model-price-number muted"}>{modelCatalogPriceValue(row.cacheReadPrice)}</strong></td>
                <td>
                  <div className="model-context-cell">
                    <strong>{row.contextLabel}</strong>
                    <span>{row.contextDetail}</span>
                  </div>
                </td>
                <td><strong className="model-monthly-cost">{row.monthlyCost > 0 ? `$${modelCatalogMoney(row.monthlyCost)}` : "-"}</strong></td>
                <td>
                  <div className="model-price-index">
                    <span>
                      <i style={{ width: row.priceIndex > 0 ? `${clampNumber((row.priceIndex / maxIndex) * 100, 8, 100)}%` : "0%" }} />
                    </span>
                    <strong>{row.priceIndex > 0 ? `${row.priceIndex.toFixed(2)}x` : "-"}</strong>
                  </div>
                </td>
                <td>
                  <div className="model-source-cell">
                    <span className={`model-source-pill ${row.sourceTone}`}>{tx(row.sourceLabel)}</span>
                    <small>{row.availability.activeRoutes}/{row.availability.totalRoutes} {tx("启用路由")}</small>
                  </div>
                </td>
                {!readOnly ? (
                  <td>
                    <div className="model-row-actions">
                      {visibleActions.map((action) => (
                        <button className="text-button" key={action.label} onClick={() => onAction(action, row.model)} type="button">
                          {tx(action.label)}
                        </button>
                      ))}
                      {onEdit ? <button className="text-button" onClick={() => onEdit(row.model)} type="button">{tx("编辑")}</button> : null}
                      {onDelete ? (
                        <button className="danger-button" onClick={() => onDelete(row.model)} title={tx("删除")} type="button">
                          <Trash2 size={15} />
                        </button>
                      ) : null}
                    </div>
                  </td>
                ) : null}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function RouteStrategyView({
  config,
  data,
  loading,
  onCreate,
  onEdit,
  onDelete,
  onReorder,
}: {
  config: ResourceConfig<ModelRoute>;
  data: AppData;
  loading: boolean;
  onCreate: () => void;
  onEdit: (item: ModelRoute) => void;
  onDelete: (item: ModelRoute) => void;
  onReorder: (model: Model, routes: ModelRoute[]) => void;
}) {
  const [category, setCategory] = useState("all");
  const [scope, setScope] = useState<"configured" | "all">("configured");
  const [query, setQuery] = useState("");
  const [draggedRouteID, setDraggedRouteID] = useState("");
  const categories = routeModelCategories(data);
  const filtered = useMemo(
    () => filterRouteModels(data, category, scope, query),
    [data, category, scope, query],
  );
  const configuredCount = data.models.filter((model) => modelRoutesFor(model, data).length > 0).length;
  const activeRouteCount = data.routes.filter((route) => route.status === "active").length;

  return (
    <DataSection title={config.eyebrow}>
      <RouteStrategyHint data={data} />
      <div className="route-matrix">
        <aside className="model-catalog-sidebar">
          <div className="model-catalog-sidebar-head">
            <strong>{tx("统一模型")}</strong>
            <span>{configuredCount} {tx("个已配置路由")}</span>
          </div>
          <div className="model-provider-list">
            {categories.map((item) => (
              <button
                className={category === item.key ? "model-provider-filter active" : "model-provider-filter"}
                key={item.key}
                onClick={() => setCategory(item.key)}
                type="button"
              >
                <span className="model-provider-icon">{modelCategoryInitial(item.key, item.label)}</span>
                <strong>{tx(item.label)}</strong>
                <em>{item.count}</em>
              </button>
            ))}
          </div>
        </aside>

        <section className="model-catalog-main">
          <div className="model-filterbar">
            <div className="model-capability-tabs" role="tablist" aria-label={tx("路由显示范围")}>
              <button
                aria-selected={scope === "configured"}
                className={scope === "configured" ? "model-capability-tab active" : "model-capability-tab"}
                onClick={() => setScope("configured")}
                role="tab"
                type="button"
              >
                <Gauge size={14} />
                <span>{tx("已配置")}</span>
                <em>{configuredCount}</em>
              </button>
              <button
                aria-selected={scope === "all"}
                className={scope === "all" ? "model-capability-tab active" : "model-capability-tab"}
                onClick={() => setScope("all")}
                role="tab"
                type="button"
              >
                <Boxes size={14} />
                <span>{tx("全部模型")}</span>
                <em>{data.models.length}</em>
              </button>
            </div>
            <div className="model-catalog-actions">
              <div className="search-box model-search">
                <Search size={16} />
                <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索模型或 Provider")} />
              </div>
              <button className="button" onClick={onCreate} type="button">
                <Plus size={17} />
                {tx(config.createLabel ?? "新增路由")}
              </button>
            </div>
          </div>

          <div className="model-catalog-summary">
            <span>{tx(modelCatalogFilterLabel(categories, category))}</span>
            <strong>{filtered.length}</strong>
            <em>{tx("个模型")} · {activeRouteCount}/{data.routes.length} {tx("条启用线路")}</em>
          </div>

          {filtered.length === 0 ? (
            <div className="empty model-catalog-empty">{tx("没有匹配的模型路由")}</div>
          ) : (
            <div className="route-model-list">
              {filtered.map((model) => (
                <RouteModelCard
                  key={model.name}
                  model={model}
                  data={data}
                  loading={loading}
                  draggedRouteID={draggedRouteID}
                  onDragStart={setDraggedRouteID}
                  onDragEnd={() => setDraggedRouteID("")}
                  onDrop={(targetRouteID) => {
                    const routes = modelRoutesFor(model, data);
                    const reordered = reorderRoutes(routes, draggedRouteID, targetRouteID);
                    setDraggedRouteID("");
                    if (reordered !== routes) onReorder(model, reordered);
                  }}
                  onEdit={onEdit}
                  onDelete={onDelete}
                />
              ))}
            </div>
          )}
        </section>
      </div>
    </DataSection>
  );
}

function RouteModelCard({
  model,
  data,
  loading,
  draggedRouteID,
  onDragStart,
  onDragEnd,
  onDrop,
  onEdit,
  onDelete,
}: {
  model: Model;
  data: AppData;
  loading: boolean;
  draggedRouteID: string;
  onDragStart: (routeID: string) => void;
  onDragEnd: () => void;
  onDrop: (targetRouteID: string) => void;
  onEdit: (route: ModelRoute) => void;
  onDelete: (route: ModelRoute) => void;
}) {
  const routes = modelRoutesFor(model, data);
  const activeRoutes = routes.filter((route) => route.status === "active");
  const category = modelCategory(model);
  return (
    <article className="route-model-card">
      <div className="route-model-head">
        <div>
          <div className="model-card-brand compact">
            <span>{modelCategoryInitial(category, modelCategoryLabel(category))}</span>
            <div>
              <em>{modelCategoryLabel(category)}</em>
              <strong>{model.modality || "chat"}</strong>
            </div>
          </div>
          <h2>{model.name}</h2>
        </div>
        <div className="route-model-stats">
          <StatusPill status={routes.length > 0 ? "active" : "disabled"} label={routes.length > 0 ? `${activeRoutes.length}/${routes.length} ${tx("启用")}` : tx("未配置")} />
          <span>{tx("按上到下顺序调用")}</span>
        </div>
      </div>

      {routes.length === 0 ? (
        <div className="empty route-empty">{tx("该统一模型还没有 Provider 线路")}</div>
      ) : (
        <div className="route-order-list">
          {routes.map((route, index) => (
            <RouteProviderRow
              key={route.id}
              route={route}
              index={index}
              provider={findProvider(data, route.provider_id)}
              dragging={draggedRouteID === route.id}
              loading={loading}
              onDragStart={() => onDragStart(route.id)}
              onDragEnd={onDragEnd}
              onDrop={() => onDrop(route.id)}
              onEdit={() => onEdit(route)}
              onDelete={() => onDelete(route)}
            />
          ))}
        </div>
      )}
    </article>
  );
}

function RouteProviderRow({
  route,
  provider,
  index,
  dragging,
  loading,
  onDragStart,
  onDragEnd,
  onDrop,
  onEdit,
  onDelete,
}: {
  route: ModelRoute;
  provider?: Provider;
  index: number;
  dragging: boolean;
  loading: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div
      className={dragging ? "route-provider-row dragging" : "route-provider-row"}
      draggable={!loading}
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = "move";
        onDragStart();
      }}
      onDragOver={(event) => {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
      }}
      onDragEnd={onDragEnd}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
    >
      <button className="route-drag-handle" disabled={loading} title={tx("拖动调整调用顺序")} type="button">
        <GripVertical size={15} />
      </button>
      <div className="route-order-badge">{index === 0 ? tx("主") : index + 1}</div>
      <div className="route-provider-main">
        <strong>{provider?.name || route.provider_id}</strong>
        <span>{providerTypeLabel(provider?.type)} · {provider?.base_url || tx("未配置 Base URL")}</span>
      </div>
      <div className="route-upstream-model">
        <strong>{route.provider_model}</strong>
        <span>{routeStrategyLabel(route.strategy)} · P{route.priority} · W{route.weight}</span>
      </div>
      <StatusPill status={route.status} />
      <div className="route-row-actions">
        <button className="text-button" onClick={onEdit} type="button">{tx("编辑")}</button>
        <button className="danger-button" onClick={onDelete} title={tx("删除")} type="button">
          <Trash2 size={15} />
        </button>
      </div>
    </div>
  );
}

function ModelCatalogCard({
  model,
  data,
  readOnly = false,
  actions,
  onAction,
  onEdit,
  onDelete,
}: {
  model: Model;
  data: AppData;
  readOnly?: boolean;
  actions: ResourceAction<Model>[];
  onAction: (action: ResourceAction<Model>, item: Model) => void;
  onEdit?: (item: Model) => void;
  onDelete?: (item: Model) => void;
}) {
  const category = modelCategory(model);
  const availability = modelAvailabilitySummary(model, data, readOnly);
  const routeCount = availability.activeRoutes;
  const hasConfiguredRoute = availability.activeRoutes > 0;
  const cardClassName = !readOnly && availability.tone === "blocked" ? "model-card unrouted" : "model-card";
  return (
    <article className={cardClassName}>
      <div className="model-card-head">
        <div className="model-card-brand">
          <span>{modelCategoryInitial(category, modelCategoryLabel(category))}</span>
          <div>
            <em>{modelCategoryLabel(category)}</em>
            <strong>{model.modality || "chat"}</strong>
          </div>
        </div>
        <StatusPill status={model.status} />
      </div>

      <h2>{model.name}</h2>

      <div className="model-card-tags">
        <span>{modelCapabilityLabel(model)}</span>
        {readOnly ? (
          <span className="official">{tx(availability.label)}</span>
        ) : (
          <>
            <span className={hasConfiguredRoute ? undefined : "unrouted-tag"}>{hasConfiguredRoute ? `${routeCount} ${tx("条线路")}` : tx("未配置线路")}</span>
            {hasThirdPartyRoute(model, data) ? <span className="third">{tx("三方资源")}</span> : <span className="official">{tx("官方资源")}</span>}
          </>
        )}
      </div>

      <div className={`model-availability ${availability.tone}`}>
        <strong>{tx(availability.label)}</strong>
        <span>{tx(availability.detail)}</span>
      </div>

      <div className="model-card-pricing">
        <ModelMetric label="输入" value={priceMetric(model.input_price_usd_per_1m)} muted={!model.input_price_usd_per_1m} />
        <ModelMetric label="输出" value={priceMetric(model.output_price_usd_per_1m)} muted={!model.output_price_usd_per_1m} />
        <ModelMetric label="上下文" value={model.context_window ? formatNumber(model.context_window) : "-"} />
        <ModelMetric label="Embedding" value={priceMetric(model.embedding_price_usd_per_1m)} muted={!model.embedding_price_usd_per_1m} />
      </div>

      {!readOnly ? (
        <div className="model-card-routes">
          <ModelRouteProviders model={model} data={data} />
        </div>
      ) : null}

      {actions.length > 0 || onEdit || onDelete ? (
        <div className="model-card-actions">
          {actions.map((action) => (
            <button className="text-button" key={action.label} onClick={() => onAction(action, model)} type="button">
              {tx(action.label)}
            </button>
          ))}
          {onEdit ? <button className="text-button" onClick={() => onEdit(model)} type="button">{tx("编辑")}</button> : null}
          {onDelete ? (
            <button className="danger-button" onClick={() => onDelete(model)} title={tx("删除")} type="button">
              <Trash2 size={15} />
            </button>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

function ModelMetric({ label, value, muted }: { label: string; value: string; muted?: boolean }) {
  return (
    <div className={muted ? "model-metric muted" : "model-metric"}>
      <strong>{value}</strong>
      <span>{tx(label)}</span>
    </div>
  );
}

function ModelBrandIcon({ category, label, compact = false }: { category: string; label: string; compact?: boolean }) {
  const source = modelBrandIconSource(category);
  const className = `model-brand-icon${compact ? " compact" : ""}${source ? "" : " fallback"}`;
  if (source) {
    return (
      <span aria-label={label} className={className} title={label}>
        <img alt="" src={source} />
      </span>
    );
  }
  return (
    <span aria-label={label} className={className} title={label}>
      <Boxes size={18} />
    </span>
  );
}

function ModelCatalogSortHeader({
  label,
  sortKey,
  sort,
  onSort,
}: {
  label: string;
  sortKey: Exclude<ModelCatalogPriceSortKey, "default">;
  sort: ModelCatalogPriceSort;
  onSort: (sort: ModelCatalogPriceSort) => void;
}) {
  const active = sort.key === sortKey;
  return (
    <button
      className={active ? `model-sort-button active ${sort.direction}` : "model-sort-button"}
      onClick={() => onSort(modelCatalogNextSort(sort, sortKey))}
      title={tx("点击排序")}
      type="button"
    >
      <span>{tx(label)}</span>
      <ChevronDown aria-hidden="true" className="model-sort-icon" size={13} />
    </button>
  );
}

function modelCatalogNextSort(current: ModelCatalogPriceSort, key: Exclude<ModelCatalogPriceSortKey, "default">): ModelCatalogPriceSort {
  if (current.key === key) {
    return { key, direction: current.direction === "asc" ? "desc" : "asc" };
  }
  return { key, direction: modelCatalogDefaultSortDirection(key) };
}

function modelCatalogDefaultSortDirection(key: ModelCatalogPriceSortKey): ModelCatalogPriceSortDirection {
  if (key === "name" || key === "input" || key === "output" || key === "cache" || key === "monthly" || key === "index") return "asc";
  return "desc";
}

function modelCatalogSortAria(sort: ModelCatalogPriceSort, key: ModelCatalogPriceSortKey) {
  if (sort.key !== key) return "none";
  return sort.direction === "asc" ? "ascending" : "descending";
}

function modelCatalogPriceSortedModels(models: Model[]) {
  return models.slice().sort((left, right) => {
    const leftCost = modelEstimatedMonthlyCost(left);
    const rightCost = modelEstimatedMonthlyCost(right);
    const leftMissingPrice = leftCost <= 0 ? 1 : 0;
    const rightMissingPrice = rightCost <= 0 ? 1 : 0;
    return leftMissingPrice - rightMissingPrice
      || leftCost - rightCost
      || modelCategoryRank(left) - modelCategoryRank(right)
      || left.name.localeCompare(right.name);
  });
}

function modelCatalogSortRows(rows: ModelCatalogPriceRowData[], sort: ModelCatalogPriceSort) {
  if (sort.key === "default") return rows;
  const direction = sort.direction === "asc" ? 1 : -1;
  return rows
    .map((row, index) => ({ row, index }))
    .sort((left, right) => {
      const compared = modelCatalogCompareSortValues(
        modelCatalogSortValue(left.row, sort.key),
        modelCatalogSortValue(right.row, sort.key),
        direction,
      );
      return compared || left.index - right.index;
    })
    .map((item) => item.row);
}

function modelCatalogSortValue(row: ModelCatalogPriceRowData, key: ModelCatalogPriceSortKey) {
  switch (key) {
    case "name":
      return modelDisplayTitle(row.model).toLowerCase();
    case "input":
      return row.inputPrice || undefined;
    case "output":
      return row.outputPrice || undefined;
    case "cache":
      return row.cacheReadPrice || undefined;
    case "context":
      return row.model.context_window || undefined;
    case "monthly":
      return row.monthlyCost || undefined;
    case "index":
      return row.priceIndex || undefined;
    default:
      return undefined;
  }
}

function modelCatalogCompareSortValues(left: string | number | undefined, right: string | number | undefined, direction: number) {
  const leftMissing = left === undefined || left === "";
  const rightMissing = right === undefined || right === "";
  if (leftMissing && rightMissing) return 0;
  if (leftMissing) return 1;
  if (rightMissing) return -1;
  if (typeof left === "string" || typeof right === "string") {
    return String(left).localeCompare(String(right)) * direction;
  }
  return (left - right) * direction;
}

function modelCatalogPriceBaseline(models: Model[]) {
  const preferred = models.find((model) => /gpt-4\.1-mini|gpt-4o-mini|deepseek-chat/i.test(model.name));
  const preferredCost = preferred ? modelEstimatedMonthlyCost(preferred) : 0;
  if (preferredCost > 0) return preferredCost;
  const costs = models.map(modelEstimatedMonthlyCost).filter((cost) => cost > 0).sort((left, right) => left - right);
  return costs[Math.floor(costs.length / 2)] || costs[0] || 1;
}

function modelCatalogPriceRow(model: Model, data: AppData, readOnly: boolean, baseline: number) {
  const category = modelCategory(model);
  const inputPrice = modelCatalogInputPrice(model);
  const outputPrice = model.output_price_usd_per_1m || undefined;
  const cacheReadPrice = modelCachedReadPrice(model);
  const monthlyCost = modelEstimatedMonthlyCost(model);
  const priceIndex = monthlyCost > 0 && baseline > 0 ? monthlyCost / baseline : 0;
  const availability = modelAvailabilitySummary(model, data, readOnly);
  const source = modelCatalogSource(model, data);
  const type = modelCatalogTypeBadge(model, monthlyCost, baseline);
  return {
    model,
    availability,
    category,
    categoryLabel: modelCategoryLabel(category),
    inputPrice,
    outputPrice,
    cacheReadPrice,
    monthlyCost,
    priceIndex,
    contextLabel: model.context_window ? modelCatalogCompactNumber(model.context_window) : "-",
    contextDetail: model.context_window ? tx("上下文窗口") : tx("未配置"),
    sourceLabel: source.label,
    sourceTone: source.tone,
    typeLabel: type.label,
    typeTone: type.tone,
  };
}

function modelCatalogInputPrice(model: Model) {
  if (model.input_price_usd_per_1m) return model.input_price_usd_per_1m;
  if (model.modality === "embedding" && model.embedding_price_usd_per_1m) return model.embedding_price_usd_per_1m;
  return undefined;
}

function modelCachedReadPrice(model: Model) {
  const configured = readModelMetadataNumber(model, [
    "cached_input_price_usd_per_1m",
    "cache_read_price_usd_per_1m",
    "cached_read_price_usd_per_1m",
  ]);
  if (configured > 0) return configured;
  const input = model.input_price_usd_per_1m || 0;
  if (!input || model.modality === "embedding") return undefined;
  const category = modelCategory(model);
  const cacheHint = [model.name, model.family, ...(model.capabilities ?? []), ...(model.supported_parameters ?? [])]
    .join(" ")
    .toLowerCase();
  const commonlyCached = ["openai", "claude", "gemini", "deepseek"].includes(category)
    || cacheHint.includes("cache")
    || cacheHint.includes("prompt");
  return commonlyCached ? input * 0.25 : undefined;
}

function readModelMetadataNumber(model: Model, keys: string[]) {
  for (const key of keys) {
    const value = Number(model.metadata?.[key] ?? "");
    if (Number.isFinite(value) && value > 0) return value;
  }
  return 0;
}

function modelEstimatedMonthlyCost(model: Model) {
  const embeddingPrice = model.embedding_price_usd_per_1m || 0;
  if (model.modality === "embedding" && embeddingPrice > 0) return embeddingPrice * 100;
  const input = model.input_price_usd_per_1m || 0;
  const output = model.output_price_usd_per_1m || 0;
  return input * 100 + output * 50;
}

function modelCatalogTypeBadge(model: Model, monthlyCost: number, baseline: number) {
  const text = [model.name, model.family, model.modality, ...(model.capabilities ?? [])].join(" ").toLowerCase();
  if (/code|coder|codestral|devstral|codex|build/.test(text)) return { label: "代码", tone: "code" };
  if (/reason|thinking|r1|o1|o3/.test(text)) return { label: "推理", tone: "reasoning" };
  if (/image|vision|video|audio|ocr|multimodal/.test(text)) return { label: "多模态", tone: "media" };
  const index = monthlyCost > 0 && baseline > 0 ? monthlyCost / baseline : 0;
  if (index > 0 && index <= 0.8) return { label: "低价", tone: "low" };
  if (index >= 2.4 || /pro|opus|large|gpt-5|grok-4/.test(text)) return { label: "旗舰", tone: "flagship" };
  return { label: "均衡", tone: "balanced" };
}

function modelCatalogSource(model: Model, data: AppData) {
  const hasPrice = modelEstimatedMonthlyCost(model) > 0;
  if (!hasPrice) return { label: "未配置", tone: "missing" };
  if (hasThirdPartyRoute(model, data)) return { label: "三方价", tone: "third" };
  return { label: "官方价", tone: "official" };
}

function modelBrandIconSource(category: string) {
  const sources: Record<string, string> = {
    openai: "/model-icons/openai.svg",
    claude: "/model-icons/claude.svg",
    deepseek: "/model-icons/deepseek.svg",
    gemini: "/model-icons/gemini.svg",
    qwen: "/model-icons/qwen.svg",
    glm: "/model-icons/glm.svg",
    kimi: "/model-icons/kimi.svg",
    doubao: "/model-icons/doubao.svg",
    ernie: "/model-icons/ernie.svg",
    baichuan: "/model-icons/baichuan.svg",
    minimax: "/model-icons/minimax.svg",
    stepfun: "/model-icons/stepfun.svg",
    wanx: "/model-icons/wanx.svg",
    paddlepaddle: "/model-icons/paddlepaddle.svg",
    microsoft: "/model-icons/microsoft.svg",
    llama: "/model-icons/llama.svg",
    mistral: "/model-icons/mistral.svg",
    grok: "/model-icons/grok.svg",
  };
  return sources[category] ?? "";
}

function modelDisplayTitle(model: Model) {
  return model.metadata?.title || model.name;
}

function modelCatalogPriceValue(value: number | undefined) {
  return value && value > 0 ? `$${modelCatalogMoney(value)}` : "-";
}

function modelCatalogMoney(value: number) {
  const amount = Math.max(0, value || 0);
  if (amount >= 100) return amount.toFixed(0);
  if (amount >= 10) return amount.toFixed(1);
  if (amount >= 1) return amount.toFixed(2);
  return amount.toFixed(3);
}

function modelCatalogCompactNumber(value: number) {
  if (value >= 1_000_000) {
    const scaled = value / 1_000_000;
    return `${Number.isInteger(scaled) ? scaled.toFixed(0) : scaled.toFixed(1)}M`;
  }
  if (value >= 1_000) {
    const scaled = value / 1_000;
    return `${Number.isInteger(scaled) ? scaled.toFixed(0) : scaled.toFixed(1)}K`;
  }
  return formatNumber(value || 0);
}

function SettingsView({
  data,
  activeTab,
  language,
  onTabChange,
  onLanguageChange,
  onCreate,
  onEdit,
  onDelete,
  onAction,
  onToolbarAction,
}: {
  data: AppData;
  activeTab: SettingsTabKey;
  language: AppLanguage;
  onTabChange: (tab: SettingsTabKey) => void;
  onLanguageChange: (language: AppLanguage) => void;
  onCreate: (config: ResourceConfig<AdminResource>) => void;
  onEdit: (config: ResourceConfig<AdminResource>, item: AdminResource) => void;
  onDelete: (config: ResourceConfig<AdminResource>, item: AdminResource) => void;
  onAction: (action: ResourceAction<AdminResource>, item: AdminResource) => void;
  onToolbarAction: (action: ToolbarAction, items: AdminResource[]) => void;
}) {
  const configs = useMemo(() => [systemSettingConfig(), roleConfig(), identityProviderConfig()], []);
  const activeConfig = configs.find((config) => config.view === activeTab) ?? configs[0];
  const [queries, setQueries] = useState<Record<string, string>>({});
  const query = queries[activeConfig.view] ?? "";
  const allItems = activeConfig.list(data);
  const filteredItems = filterRows(allItems, query);
  const pagination = usePagination(filteredItems.length, `settings:${activeConfig.view}:${query}`);
  const pagedItems = useMemo(
    () => filteredItems.slice(pagination.startIndex, pagination.endIndex),
    [filteredItems, pagination.startIndex, pagination.endIndex],
  );

  return (
    <div className="settings-view">
      <LanguagePreferenceCard language={language} onChange={onLanguageChange} />
      <div className="settings-tabs" role="tablist" aria-label={tx("系统设置分类")}>
        {configs.map((config) => (
          <button
            aria-selected={activeConfig.view === config.view}
            className={activeConfig.view === config.view ? "settings-tab active" : "settings-tab"}
            key={config.view}
            onClick={() => onTabChange(config.view as SettingsTabKey)}
            role="tab"
            type="button"
          >
            {settingsTabLabel(config.view as SettingsTabKey)}
          </button>
        ))}
      </div>
      <CrudView
        config={activeConfig}
        data={data}
        items={pagedItems}
        totalItems={filteredItems.length}
        loading={false}
        query={query}
        pagination={pagination}
        categoryFilter="all"
        onCategoryFilter={() => undefined}
        onQuery={(value) => setQueries((current) => ({ ...current, [activeConfig.view]: value }))}
        onCreate={() => onCreate(activeConfig)}
        onEdit={(item) => onEdit(activeConfig, item)}
        onDelete={(item) => onDelete(activeConfig, item)}
        onAction={onAction}
        onToolbarAction={(action) => onToolbarAction(action, filteredItems)}
      />
    </div>
  );
}

function LanguagePreferenceCard({
  language,
  onChange,
}: {
  language: AppLanguage;
  onChange: (language: AppLanguage) => void;
}) {
  const current = languageOptions.find((option) => option.value === language) ?? languageOptions[0];
  return (
    <section className="language-card">
      <div>
        <strong>{tx("界面语言")}</strong>
        <span>{tx("选择控制台显示语言，偏好会保存在当前浏览器。")}</span>
      </div>
      <div className="language-card-control">
        <small>{tx("当前语言")}: {languageOptionLabel(current, language)}</small>
        <LanguageSwitcher language={language} onChange={onChange} />
      </div>
    </section>
  );
}

function LanguageSwitcher({
  language,
  onChange,
  className,
}: {
  language: AppLanguage;
  onChange: (language: AppLanguage) => void;
  className?: string;
}) {
  return (
    <div className={className ? `language-switcher ${className}` : "language-switcher"} role="radiogroup" aria-label={tx("界面语言")}>
      {languageOptions.map((option) => (
        <button
          aria-checked={language === option.value}
          className={language === option.value ? "active" : ""}
          key={option.value}
          onClick={() => onChange(option.value)}
          role="radio"
          type="button"
        >
          <span>{languageOptionLabel(option, language)}</span>
        </button>
      ))}
    </div>
  );
}

function languageOptionLabel(option: { label: string; nativeLabel: string }, language: AppLanguage) {
  return language === "en" ? option.label : option.nativeLabel;
}

function APIKeyFlowHint({ data }: { data: AppData }) {
  return (
    <div className="workflow-hint">
      <div>
        <strong>{tx("Key 归属逻辑")}</strong>
        <span>{tx("内部应用配置项目下发放的 Key；额度、模型白名单、用量和成本都会归属到该项目。")}</span>
      </div>
      <div className="workflow-hint-stats">
        <span>{countWithLabel(data.projects.length, "个项目")}</span>
        <span>{countWithLabel(data.keys.length, "个 Key")}</span>
      </div>
    </div>
  );
}

function RouteStrategyHint({ data }: { data: AppData }) {
  const activeRoutes = data.routes.filter((route) => route.status === "active").length;
  return (
    <div className="workflow-hint">
      <div>
        <strong>{tx("模型路由器")}</strong>
        <span>{tx("平衡模式综合权重、质量和成本；质量优先会先选高质量线路；成本优先会先选低成本线路。调用失败时会按候选顺序自动回退。")}</span>
      </div>
      <div className="workflow-hint-stats">
        <span>{activeRoutes} {tx("条启用线路")}</span>
        <span>{data.providers.filter((provider) => provider.status === "active").length} Provider</span>
      </div>
    </div>
  );
}

function EntityTable<T>({
  config,
  data,
  items,
  loading = false,
  query = "",
  onCreate,
  onEdit,
  onDelete,
  onAction,
  onRowClick,
  selectedRowID,
}: {
  config: ResourceConfig<T>;
  data: AppData;
  items: T[];
  loading?: boolean;
  query?: string;
  onCreate?: () => void;
  onEdit: (item: T) => void;
  onDelete: (item: T) => void;
  onAction: (action: ResourceAction<T>, item: T) => void;
  onRowClick?: (item: T) => void;
  selectedRowID?: string;
}) {
  if (loading && items.length === 0) {
    return <TableSkeleton columns={Math.max(3, config.columns.length + 1)} rows={5} />;
  }
  if (items.length === 0) {
    return <ResourceEmptyState config={config} query={query} onCreate={onCreate} />;
  }
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {config.columns.map((column) => (
              <th key={column.key}>{tx(column.label)}</th>
            ))}
            <th>{tx("操作")}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr
              className={`${onRowClick ? "clickable-row" : ""} ${selectedRowID === rowID(item) ? "selected-row" : ""}`}
              key={rowID(item)}
              onClick={onRowClick ? () => onRowClick(item) : undefined}
            >
              {config.columns.map((column) => (
                <td key={column.key}>
                  {config.view === "api-keys" && column.key === "status" ? (
                    <APIKeyStatusSwitch
                      item={item as APIKey}
                      onToggle={(nextStatus) => onAction(apiKeyStatusAction(nextStatus) as unknown as ResourceAction<T>, item)}
                    />
                  ) : column.render ? (
                    translatedCell(column.render(item, data))
                  ) : (
                    displayCellValue(readPath(item, column.key))
                  )}
                </td>
              ))}
              <td>
                <div className="row-actions" onClick={(event) => event.stopPropagation()}>
                  {(config.actions ?? [])
                    .filter((action) => action.visible?.(item) ?? true)
                    .map((action) => (
                      <button
                        className="text-button"
                        key={action.label}
                        onClick={() => onAction(action, item)}
                        title={tx(action.title ?? action.label)}
                        type="button"
                      >
                        {tx(action.label)}
                      </button>
                    ))}
                  {config.update ? (
                    <button className="text-button" onClick={() => onEdit(item)} type="button">
                      {tx("编辑")}
                    </button>
                  ) : null}
                  {config.remove ? (
                    <button className="danger-button" onClick={() => onDelete(item)} type="button" title={tx("删除")}>
                      <Trash2 size={15} />
                    </button>
                  ) : null}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function displayCellValue(value: unknown) {
  if (typeof value === "string") return displayText(value);
  return value as React.ReactNode;
}

function ResourceEmptyState<T>({
  config,
  query,
  onCreate,
}: {
  config: ResourceConfig<T>;
  query: string;
  onCreate?: () => void;
}) {
  const copy = resourceEmptyCopy(config.view, Boolean(query.trim()));
  return (
    <div className="resource-empty">
      <div className="resource-empty-icon">
        <Search size={18} />
      </div>
      <strong>{tx(copy.title)}</strong>
      <span>{tx(copy.description)}</span>
      {onCreate && !query.trim() ? (
        <button className="button" onClick={onCreate} type="button">
          <Plus size={16} />
          {tx(config.createLabel ?? "新增")}
        </button>
      ) : null}
    </div>
  );
}

function resourceEmptyCopy(view: ViewKey, filtered: boolean) {
  if (filtered) {
    return {
      title: "没有匹配结果",
      description: "清空搜索或换一个关键词再试。",
    };
  }
  switch (view) {
    case "providers":
      return {
        title: "还没有 Provider",
        description: "先接入上游服务商，再为模型配置路由。",
      };
    case "routes":
      return {
        title: "还没有模型路由",
        description: "路由决定模型请求会被转发到哪个 Provider。",
      };
    case "projects":
      return {
        title: "还没有项目空间",
        description: "项目是 Key、额度、成员和成本归属的基本单元。",
      };
    case "api-keys":
      return {
        title: "还没有 Key",
        description: "为项目发放 Key 后，业务应用才能调用网关。",
      };
    case "users":
      return {
        title: "还没有用户",
        description: "可以手动创建，也可以从 CSV 批量导入。",
      };
    case "identity-providers":
      return {
        title: "还没有身份源",
        description: "接入企业 OAuth/OIDC 后，用户可以使用 SSO 登录。",
      };
    default:
      return {
        title: "暂无数据",
        description: "当前视图还没有可展示的记录。",
      };
  }
}

function TableSkeleton({ columns, rows }: { columns: number; rows: number }) {
  return (
    <div className="table-wrap skeleton-table" aria-busy="true">
      {Array.from({ length: rows }).map((_, rowIndex) => (
        <div className="skeleton-row" key={rowIndex} style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
          {Array.from({ length: columns }).map((__, columnIndex) => (
            <span key={columnIndex} />
          ))}
        </div>
      ))}
    </div>
  );
}

function resultCountLabel(totalItems: number, query: string) {
  return query.trim() ? `${formatNumber(totalItems)} ${tx("条匹配")}` : `${formatNumber(totalItems)} ${tx("条记录")}`;
}

type PaginationState = {
  page: number;
  pageSize: number;
  pageCount: number;
  startIndex: number;
  endIndex: number;
  setPage: (page: number) => void;
  setPageSize: (pageSize: number) => void;
};

const pageSizeOptions = [20, 50, 100];

function usePagination(totalItems: number, resetKey: string): PaginationState {
  const [page, setPageState] = useState(1);
  const [pageSize, setPageSizeState] = useState(20);
  const pageCount = Math.max(1, Math.ceil(totalItems / pageSize));

  useEffect(() => {
    setPageState(1);
  }, [resetKey]);

  useEffect(() => {
    if (page > pageCount) {
      setPageState(pageCount);
    }
  }, [page, pageCount]);

  const safePage = Math.min(page, pageCount);
  const startIndex = totalItems === 0 ? 0 : (safePage - 1) * pageSize;
  const endIndex = Math.min(startIndex + pageSize, totalItems);

  return {
    page: safePage,
    pageSize,
    pageCount,
    startIndex,
    endIndex,
    setPage: (nextPage) => setPageState(Math.min(Math.max(nextPage, 1), pageCount)),
    setPageSize: (nextPageSize) => {
      setPageSizeState(nextPageSize);
      setPageState(1);
    },
  };
}

function PaginationControls({
  pagination,
  totalItems,
}: {
  pagination: PaginationState;
  totalItems: number;
}) {
  if (totalItems <= pageSizeOptions[0]) return null;
  return (
    <div className="pagination">
      <div className="pagination-summary">
        {activeLanguage === "zh-CN"
          ? `第 ${pagination.startIndex + 1}-${pagination.endIndex} 条，共 ${totalItems} 条`
          : activeLanguage === "ja"
            ? `${pagination.startIndex + 1}-${pagination.endIndex} / ${totalItems} 件`
            : `${pagination.startIndex + 1}-${pagination.endIndex} of ${totalItems}`}
      </div>
      <div className="pagination-controls">
        <label className="page-size">
          <span>{tx("每页")}</span>
          <select
            value={pagination.pageSize}
            onChange={(event) => pagination.setPageSize(Number(event.target.value))}
          >
            {pageSizeOptions.map((option) => (
              <option key={option} value={option}>{option}</option>
            ))}
          </select>
        </label>
        <div className="page-buttons">
          <button
            type="button"
            title={tx("第一页")}
            onClick={() => pagination.setPage(1)}
            disabled={pagination.page <= 1}
          >
            <ChevronsLeft size={15} />
          </button>
          <button
            type="button"
            title={tx("上一页")}
            onClick={() => pagination.setPage(pagination.page - 1)}
            disabled={pagination.page <= 1}
          >
            <ChevronLeft size={15} />
          </button>
          <span>{pagination.page} / {pagination.pageCount}</span>
          <button
            type="button"
            title={tx("下一页")}
            onClick={() => pagination.setPage(pagination.page + 1)}
            disabled={pagination.page >= pagination.pageCount}
          >
            <ChevronRight size={15} />
          </button>
          <button
            type="button"
            title={tx("最后一页")}
            onClick={() => pagination.setPage(pagination.pageCount)}
            disabled={pagination.page >= pagination.pageCount}
          >
            <ChevronsRight size={15} />
          </button>
        </div>
      </div>
    </div>
  );
}

function EditModal<T>({
  state,
  data,
  currentUser,
  loading,
  onClose,
  onSave,
}: {
  state: ModalState<T>;
  data: AppData;
  currentUser?: AdminUser | null;
  loading: boolean;
  onClose: () => void;
  onSave: (values: Record<string, string>) => void;
}) {
  const initial = {
    ...(state.item ? state.config.toForm?.(state.item) ?? {} : defaultFormValues(state.config, data, currentUser)),
    ...(state.initialValues ?? {}),
  };
  const [values, setValues] = useState<Record<string, string>>(
    state.config.view === "identity-providers" ? identityProviderInitialFormValues(initial, !state.item) : initial,
  );

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSave(values);
  }

  if (state.config.view === "identity-providers") {
    return (
      <IdentityProviderEditModal
        state={state as unknown as ModalState<AdminResource>}
        data={data}
        currentUser={currentUser}
        values={values}
        setValues={setValues}
        loading={loading}
        onClose={onClose}
        onSave={onSave}
      />
    );
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{state.item ? tx("编辑") : tx("新增")}</p>
            <h2>{tx(state.config.title)}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>
        <div className="modal-body">
          {state.config.fields.filter((field) => field.visible?.(values) ?? true).map((field) => (
            <FieldInput
              key={field.key}
              field={field}
              data={data}
              currentUser={currentUser}
              value={values[field.key] ?? ""}
              editing={Boolean(state.item)}
              onChange={(value) => setValues((prev) => ({ ...prev, [field.key]: value }))}
            />
          ))}
        </div>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          <button className="button" disabled={loading} type="submit">{tx("保存")}</button>
        </div>
      </form>
    </div>
  );
}

function IdentityProviderEditModal({
  state,
  data,
  currentUser,
  values,
  setValues,
  loading,
  onClose,
  onSave,
}: {
  state: ModalState<AdminResource>;
  data: AppData;
  currentUser?: AdminUser | null;
  values: Record<string, string>;
  setValues: Dispatch<SetStateAction<Record<string, string>>>;
  loading: boolean;
  onClose: () => void;
  onSave: (values: Record<string, string>) => void;
}) {
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const templateKey = inferIdentityProviderTemplateKey(values);
  const template = identityProviderTemplateByKey(templateKey);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSave(values);
  }

  function update(key: string, value: string) {
    setValues((prev) => updateIdentityProviderFormValue(prev, key, value));
  }

  function fieldConfig(key: string, override?: Partial<FieldConfig>) {
    const field = state.config.fields.find((item) => item.key === key);
    return field ? { ...field, ...override } : undefined;
  }

  function renderField(key: string, override?: Partial<FieldConfig>) {
    const field = fieldConfig(key, override);
    if (!field || !(field.visible?.(values) ?? true)) return null;
    return (
      <FieldInput
        key={key}
        field={field}
        data={data}
        currentUser={currentUser}
        value={values[key] ?? ""}
        editing={Boolean(state.item)}
        onChange={(value) => update(key, value)}
      />
    );
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal identity-provider-modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{state.item ? tx("编辑") : tx("新增")}</p>
            <h2>{tx("身份源")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>

        <div className="identity-provider-body">
          <section className="identity-provider-template-panel">
            <div className="identity-provider-section-head">
              <h3>{tx("选择身份源模板")}</h3>
              <span>{tx("选择后会自动填充协议、登录图标、Scope、Claim 和常见端点。")}</span>
            </div>
            <div className="identity-template-grid">
              {identityProviderTemplates.map((item) => {
                const iconConfig = loginIdentityProviderIconConfig(item.iconKey);
                const Icon = iconConfig.icon;
                return (
                  <button
                    className={template.key === item.key ? "identity-template-card active" : "identity-template-card"}
                    key={item.key}
                    onClick={() => update("provider_template", item.key)}
                    type="button"
                  >
                    <span className={`login-sso-icon ${iconConfig.key}`}><Icon size={16} /></span>
                    <strong>{tx(item.label)}</strong>
                    <em>{identityProviderTypeLabel(item.providerType)}</em>
                    <small>{tx(identityProviderTemplateHelp(item))}</small>
                  </button>
                );
              })}
            </div>
            <div className="identity-template-summary">
              <DetailField label="登录按钮" value={values.login_label || template.loginLabel || template.label} />
              <DetailField label="默认 Scope" value={values.scopes || template.scopes} />
              <DetailField label="必填项" value={tx("Issuer、Client ID、Client Secret、Callback URL")} />
            </div>
          </section>

          <section className="identity-provider-section">
            <div className="identity-provider-section-head">
              <h3>{tx("连接方式")}</h3>
              <span>{tx(template.label)}</span>
            </div>
            <div className="identity-provider-grid">
              {renderField("name")}
              {renderField("provider_type")}
              {renderField("status")}
              {renderField("issuer_url", { placeholder: template.issuerPlaceholder })}
              {renderField("client_id")}
              {renderField("client_secret", {
                placeholder: state.item ? "留空则不修改" : "",
                help: state.item ? "留空则不修改已保存密钥。" : "来自身份源应用的密钥。",
              })}
              {renderField("redirect_uri")}
            </div>
          </section>

          <section className="identity-provider-section">
            <div className="identity-provider-section-head">
              <h3>{tx("登录入口")}</h3>
              <span>{identityProviderIconLabel(values.icon_key)} / {values.login_label || values.name || tx("SSO")}</span>
            </div>
            <div className="identity-provider-grid compact">
              {renderField("icon_key")}
              {renderField("login_label")}
            </div>
          </section>

          <section className="identity-provider-section">
            <div className="identity-provider-section-head">
              <h3>{tx("首次登录授权")}</h3>
              <span>{identityProviderDefaultGrantLabel(data, { ...state.item, fields: values } as AdminResource)}</span>
            </div>
            <div className="identity-provider-grid">
              {renderField("default_role")}
              {renderField("default_team_id")}
              {renderField("default_project_id")}
              {renderField("default_project_role")}
            </div>
          </section>

          <details
            className="identity-provider-advanced"
            open={advancedOpen}
            onToggle={(event) => setAdvancedOpen(event.currentTarget.open)}
          >
            <summary>
              <strong>{tx("高级配置")}</strong>
              <span>{tx("端点、Scope 与 Claim 映射")}</span>
            </summary>
            <div className="identity-provider-grid">
              {renderField("authorize_url")}
              {renderField("token_url")}
              {renderField("userinfo_url")}
              {renderField("scopes")}
              {renderField("username_claim")}
              {renderField("email_claim")}
              {renderField("team_claim")}
            </div>
          </details>
        </div>

        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          <button className="button" disabled={loading} type="submit">{tx("保存")}</button>
        </div>
      </form>
    </div>
  );
}

function APIKeyWizardModal({
  data,
  currentUser,
  initialValues,
  loading,
  onClose,
  onCreate,
}: {
  data: AppData;
  currentUser?: AdminUser | null;
  initialValues?: Record<string, string>;
  loading: boolean;
  onClose: () => void;
  onCreate: (values: Record<string, string>) => void;
}) {
  const config = useMemo(() => apiKeyConfig(), []);
  const [step, setStep] = useState(0);
  const [modelScope, setModelScope] = useState<"all" | "selected">(initialValues?.allowed_models ? "selected" : "all");
  const [values, setValues] = useState<Record<string, string>>(() => ({
    ...defaultFormValues(config, data, currentUser),
    status: "active",
    ...(initialValues ?? {}),
  }));
  const projectOptions = projectSelectOptions(data, currentUser);
  const selectedProject = findProject(data, values.project_id);
  const selectableModels = keyWizardModelOptions(data);
  const selectedModels = splitList(values.allowed_models);
  const steps = [
    { title: "选择项目", icon: Boxes },
    { title: "填写用途", icon: KeyRound },
    { title: "模型范围", icon: Sparkles },
    { title: "安全护栏", icon: ShieldCheck },
    { title: "确认发放", icon: Check },
  ];
  const fieldByKey = (key: string, override?: Partial<FieldConfig>) => {
    const field = config.fields.find((item) => item.key === key);
    return field ? { ...field, ...override } : undefined;
  };

  function update(key: string, value: string) {
    setValues((current) => ({ ...current, [key]: value }));
  }

  function renderField(key: string, override?: Partial<FieldConfig>) {
    const field = fieldByKey(key, override);
    if (!field) return null;
    return (
      <FieldInput
        key={key}
        field={field}
        data={data}
        currentUser={currentUser}
        value={values[key] ?? ""}
        editing={false}
        onChange={(value) => update(key, value)}
      />
    );
  }

  function toggleModel(modelName: string) {
    const current = new Set(splitList(values.allowed_models));
    if (current.has(modelName)) current.delete(modelName);
    else current.add(modelName);
    update("allowed_models", Array.from(current).join(", "));
  }

  function canContinue(targetStep = step) {
    if (targetStep === 0) return Boolean(values.project_id);
    if (targetStep === 1) return Boolean(values.name?.trim());
    if (targetStep === 2) return modelScope === "all" || selectedModels.length > 0;
    return true;
  }

  function goNext() {
    if (!canContinue()) return;
    setStep((current) => Math.min(current + 1, steps.length - 1));
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (step < steps.length - 1) {
      goNext();
      return;
    }
    if (!canContinue(0) || !canContinue(1) || !canContinue(2)) return;
    onCreate({
      ...values,
      allowed_models: modelScope === "all" ? "" : values.allowed_models,
      status: "active",
    });
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal api-key-wizard-modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx("发放 Key")}</p>
            <h2>{tx("创建内部调用 Key")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")} disabled={loading}>×</button>
        </div>

        <div className="wizard-stepper" aria-label={tx("创建 Key 步骤")}>
          {steps.map((item, index) => {
            const Icon = item.icon;
            return (
              <button
                aria-current={step === index ? "step" : undefined}
                className={step === index ? "wizard-step active" : index < step ? "wizard-step done" : "wizard-step"}
                disabled={index > step || loading}
                key={item.title}
                onClick={() => setStep(index)}
                type="button"
              >
                <span><Icon size={14} /></span>
                <strong>{tx(item.title)}</strong>
              </button>
            );
          })}
        </div>

        <div className="api-key-wizard-body">
          {step === 0 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("选择 Key 归属项目")}</h3>
                <p>{tx("Key 必须挂在项目空间下，用量和成本会归集到这个项目。")}</p>
              </div>
              {projectOptions.length === 0 ? (
                <div className="empty wizard-empty">{tx("当前账号没有可发放 Key 的项目权限，请联系项目负责人或管理员把你加入项目。")}</div>
              ) : (
                <div className="wizard-project-grid">
                  {projectOptions.map((option) => {
                    const project = findProject(data, option.value);
                    return (
                      <button
                        className={values.project_id === option.value ? "wizard-project-card active" : "wizard-project-card"}
                        key={option.value}
                        onClick={() => update("project_id", option.value)}
                        type="button"
                      >
                        <strong>{project?.name || option.label}</strong>
                        <span>{tx("团队")}：{projectTeamLabel(data, option.value)}</span>
                        <span>{tx("负责人")}：{projectOwnerLabel(data, option.value)}</span>
                      </button>
                    );
                  })}
                </div>
              )}
            </section>
          ) : null}

          {step === 1 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("说明用途和环境")}</h3>
                <p>{tx("名称建议能看出调用方、环境和用途，后续审计会更容易定位。")}</p>
              </div>
              <div className="wizard-form-grid">
                {renderField("name", { placeholder: selectedProject ? `${selectedProject.name} production` : "backend production" })}
                {renderField("group", { placeholder: "prod、dev、backend-service" })}
              </div>
            </section>
          ) : null}

          {step === 2 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("设置模型范围")}</h3>
                <p>{tx("留空表示不限制 Key 级模型白名单；实际可调用模型仍受模型目录、路由策略和项目权限约束。")}</p>
              </div>
              <div className="wizard-choice-row">
                <button className={modelScope === "all" ? "wizard-choice active" : "wizard-choice"} onClick={() => setModelScope("all")} type="button">
                  <strong>{tx("全部可路由模型")}</strong>
                  <span>{tx("由平台路由策略决定最终可调用范围")}</span>
                </button>
                <button className={modelScope === "selected" ? "wizard-choice active" : "wizard-choice"} onClick={() => setModelScope("selected")} type="button">
                  <strong>{tx("指定模型白名单")}</strong>
                  <span>{tx("只允许这个 Key 调用已勾选的模型")}</span>
                </button>
              </div>
              {modelScope === "selected" ? (
                <div className="wizard-model-list">
                  {selectableModels.length === 0 ? (
                    <div className="empty wizard-empty">{tx("当前没有可选择的启用模型。请先在模型目录和路由策略里启用模型。")}</div>
                  ) : (
                    selectableModels.map((model) => (
                      <label className="wizard-model-option" key={model.name}>
                        <input
                          checked={selectedModels.includes(model.name)}
                          onChange={() => toggleModel(model.name)}
                          type="checkbox"
                        />
                        <span>
                          <strong>{model.name}</strong>
                          <em>{modelAvailabilitySummary(model, data, false).label}</em>
                        </span>
                      </label>
                    ))
                  )}
                </div>
              ) : null}
            </section>
          ) : null}

          {step === 3 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("设置安全护栏")}</h3>
                <p>{tx("可以先使用默认额度，之后再按调用量调整。IP 白名单留空表示不限来源。")}</p>
              </div>
              <div className="wizard-form-grid">
                {renderField("ip_allowlist")}
                {renderField("max_concurrency")}
                {renderField("daily_requests")}
                {renderField("monthly_requests")}
                {renderField("daily_tokens")}
                {renderField("monthly_tokens")}
                {renderField("daily_cost_usd")}
                {renderField("monthly_cost_usd")}
              </div>
            </section>
          ) : null}

          {step === 4 ? (
            <section className="wizard-panel">
              <div className="wizard-panel-head">
                <h3>{tx("确认后生成 Key")}</h3>
                <p>{tx("完整 Key 只会展示一次。关闭弹窗后只能看到前后缀，后续需要通过轮换生成新 Key。")}</p>
              </div>
              <div className="wizard-review-grid">
                <ReviewItem label="归属项目" value={selectedProject?.name || values.project_id || "-"} />
                <ReviewItem label="用途/环境" value={values.group || "default"} />
                <ReviewItem label="Key 名称" value={values.name || "-"} />
                <ReviewItem label="模型范围" value={modelScope === "all" ? tx("全部可路由模型") : selectedModels.join(", ") || "-"} />
                <ReviewItem label="IP 白名单" value={splitList(values.ip_allowlist).join(", ") || tx("不限")} />
                <ReviewItem label="最大并发" value={values.max_concurrency || "-"} />
                <ReviewItem label="日请求" value={values.daily_requests || "-"} />
                <ReviewItem label="月成本 USD" value={values.monthly_cost_usd || "-"} />
              </div>
            </section>
          ) : null}
        </div>

        <div className="modal-actions wizard-actions">
          <button className="secondary-button" onClick={onClose} type="button" disabled={loading}>{tx("取消")}</button>
          {step > 0 ? (
            <button className="secondary-button" onClick={() => setStep((current) => Math.max(current - 1, 0))} type="button" disabled={loading}>
              {tx("上一步")}
            </button>
          ) : null}
          <button className="button" disabled={loading || !canContinue()} type="submit">
            {step === steps.length - 1 ? (loading ? tx("发放中") : tx("生成 Key")) : tx("下一步")}
          </button>
        </div>
      </form>
    </div>
  );
}

function ReviewItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="wizard-review-item">
      <span>{tx(label)}</span>
      <strong>{value}</strong>
    </div>
  );
}

function UserImportModal({
  loading,
  onClose,
  onImport,
}: {
  loading: boolean;
  onClose: () => void;
  onImport: (content: string) => void;
}) {
  const [content, setContent] = useState("");

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onImport(content);
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal user-import-modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx("批量导入")}</p>
            <h2>{tx("导入用户")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>
        <div className="modal-body user-import-body">
          <label className="field">
            <span>{tx("CSV 内容")}</span>
            <textarea
              className="user-import-textarea"
              value={content}
              onChange={(event) => setContent(event.target.value)}
              placeholder={"username,name,email,role,team_id,status\nzhangsan,张三,zhangsan@example.com,user,team_platform,active"}
              required
            />
            <small>{tx("按 username 或 email 匹配已有用户；匹配到则更新，未匹配则创建。")}</small>
          </label>
          <div className="user-import-example">
            <strong>{tx("字段顺序")}</strong>
            <code>username,name,email,role,team_id,status</code>
            <span>{tx("role 可填 admin、team_leader、user；status 可填 active 或 disabled。")}</span>
          </div>
        </div>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          <button className="button" disabled={loading} type="submit">{loading ? tx("导入中") : tx("开始导入")}</button>
        </div>
      </form>
    </div>
  );
}

function PlaygroundPage({ api, data, canViewRoutes }: { api: ApiContext; data: AppData; canViewRoutes: boolean }) {
  return (
    <section className="playground-page">
      <PlaygroundPanel api={api} data={data} canViewRoutes={canViewRoutes} />
    </section>
  );
}

function PlaygroundPanel({
  api,
  data,
  canViewRoutes,
}: {
  api: ApiContext;
  data: AppData;
  canViewRoutes: boolean;
}) {
  const models = useMemo(() => {
    const candidates = playgroundModels(data, canViewRoutes);
    return canViewRoutes ? candidates.filter((model) => activeRouteCount(model.name, data) > 0) : candidates;
  }, [data.models, data.routes, canViewRoutes]);
  const [modelName, setModelName] = useState(models[0]?.name ?? "");
  const [messages, setMessages] = useState<PlaygroundMessage[]>([]);
  const [draft, setDraft] = useState("");
  const [systemPrompt, setSystemPrompt] = useState(defaultPlaygroundSystemPrompt);
  const [responseFormat, setResponseFormat] = useState("text");
  const [maxTokens, setMaxTokens] = useState("4096");
  const [temperature, setTemperature] = useState("0.7");
  const [presencePenalty, setPresencePenalty] = useState("0.0");
  const [frequencyPenalty, setFrequencyPenalty] = useState("0.0");
  const [minP, setMinP] = useState("0.00");
  const [topK, setTopK] = useState("50");
  const [showCode, setShowCode] = useState(false);
  const [showModelDetails, setShowModelDetails] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [lastResult, setLastResult] = useState<PlaygroundChatPayload | null>(null);
  const selectedModel = models.find((model) => model.name === modelName);
  const selectedModelRouteCount = canViewRoutes && selectedModel ? activeRouteCount(selectedModel.name, data) : 0;
  const contextWindow = selectedModel?.context_window ?? 0;
  const maxTokenLimit = Math.max(4096, Math.min(contextWindow || 32768, 200000));
  const inputPrice = selectedModel?.input_price_usd_per_1m ?? 0;
  const outputPrice = selectedModel?.output_price_usd_per_1m ?? 0;

  useEffect(() => {
    if (!modelName && models[0]?.name) {
      setModelName(models[0].name);
      return;
    }
    if (modelName && !models.some((model) => model.name === modelName)) {
      setModelName(models[0]?.name ?? "");
    }
  }, [modelName, models]);

  const languageVersion = activeLanguage;
  useEffect(() => {
    setSystemPrompt((current) => isDefaultPlaygroundSystemPrompt(current) ? defaultPlaygroundSystemPrompt() : current);
  }, [languageVersion]);

  useEffect(() => {
    const parsed = Number(maxTokens);
    if (Number.isFinite(parsed) && parsed > maxTokenLimit) {
      setMaxTokens(String(maxTokenLimit));
    }
  }, [maxTokenLimit, maxTokens]);

  async function sendMessage(event?: FormEvent<HTMLFormElement>) {
    event?.preventDefault();
    const content = draft.trim();
    if (!content || !modelName || loading) return;
    const userMessage: PlaygroundMessage = { id: uniqueUIID("msg"), role: "user", content };
    const nextMessages = [...messages, userMessage];
    const messagesForRequest = systemPrompt.trim()
      ? [{ role: "system", content: systemPrompt.trim() }, ...nextMessages.map((message) => ({ role: message.role, content: message.content }))]
      : nextMessages.map((message) => ({ role: message.role, content: message.content }));
    setMessages(nextMessages);
    setDraft("");
    setError("");
    setLoading(true);
    try {
      const numericTemperature = Number(temperature);
      const numericMaxTokens = Number(maxTokens);
      const resp = await adminFetch(api, "/api/admin/playground/chat", {
        method: "POST",
        body: JSON.stringify({
          model: modelName,
          messages: messagesForRequest,
          max_tokens: Number.isFinite(numericMaxTokens) && numericMaxTokens > 0 ? Math.round(numericMaxTokens) : undefined,
          temperature: Number.isFinite(numericTemperature) ? numericTemperature : undefined,
        }),
      });
      if (!resp.ok) {
        throw new Error(await readAPIError(resp));
      }
      const payload = (await resp.json()) as PlaygroundChatPayload;
      const assistantText = extractAssistantText(payload);
      setLastResult(payload);
      setMessages((current) => [
        ...current,
        {
          id: uniqueUIID("msg"),
          role: "assistant",
          content: assistantText || tx("模型没有返回可展示内容。"),
        },
      ]);
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("演练请求失败"));
    } finally {
      setLoading(false);
    }
  }

  function clearHistory() {
    setMessages([]);
    setLastResult(null);
    setError("");
  }

  return (
    <div className="playground-shell">
      <aside className="playground-config" aria-label={tx("模型配置")}>
        <label className="playground-model-select">
          <select value={modelName} onChange={(event) => setModelName(event.target.value)} disabled={models.length === 0}>
            {models.length === 0 ? <option value="">{tx("暂无聊天模型")}</option> : null}
            {models.map((model) => {
              const routeCount = canViewRoutes ? activeRouteCount(model.name, data) : 0;
              return (
                <option key={model.name} value={model.name}>
                  {!canViewRoutes ? model.name : routeCount > 0 ? `${model.name} · ${countWithUnit(routeCount, "条路由", "route", "件のルート")}` : `${model.name} · ${tx("未配置路由")}`}
                </option>
              );
            })}
          </select>
        </label>

        <div className="playground-config-body">
          <h2>{tx("模型配置")}</h2>
          <label className="playground-field">
            <span>{tx("响应格式")}</span>
            <select value={responseFormat} onChange={(event) => setResponseFormat(event.target.value)}>
              <option value="text">text</option>
            </select>
          </label>
          <label className="playground-field">
            <span>{tx("系统提示")}</span>
            <textarea value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} />
          </label>
          <PlaygroundConfigSlider label="max_tokens" value={maxTokens} onChange={setMaxTokens} min={128} max={maxTokenLimit} step={128} />
          <PlaygroundConfigSlider label="temperature" value={temperature} onChange={setTemperature} min={0} max={2} step={0.1} />
          <PlaygroundConfigSlider label="presence_penalty" value={presencePenalty} onChange={setPresencePenalty} min={-2} max={2} step={0.1} />
          <PlaygroundConfigSlider label="frequency_penalty" value={frequencyPenalty} onChange={setFrequencyPenalty} min={-2} max={2} step={0.1} />
          <PlaygroundConfigSlider label="min_p" value={minP} onChange={setMinP} min={0} max={1} step={0.01} />
          <PlaygroundConfigSlider label="top_k" value={topK} onChange={setTopK} min={0} max={100} step={1} />
          <div className="playground-functions">
            <strong>{tx("函数")}</strong>
            <button type="button" className="secondary-button compact" disabled title={tx("函数调用配置待接入")}>
              <Plus size={14} />
              {tx("添加函数")}
            </button>
          </div>
        </div>
      </aside>

      <section className="playground-main" aria-label={tx("模型演练对话")}>
        <div className="playground-model-bar">
          <div className="playground-model-title">
            <button type="button" className="playground-copy-model" title={tx("复制模型名")} onClick={() => navigator.clipboard?.writeText(modelName).catch(() => undefined)}>
              <strong>{modelName || tx("选择模型")}</strong>
              <Copy size={13} />
            </button>
            <span>
              {contextWindow ? `${formatNumber(contextWindow)} ${tx("上下文")}` : `${tx("上下文")} -`}
              <em />
              ${formatMoney(inputPrice)}/Mt {tx("输入")}
              <em />
              ${formatMoney(outputPrice)}/Mt {tx("输出")}
              {canViewRoutes ? (
                <>
                  <em />
                  {countWithUnit(selectedModelRouteCount, "条启用路由", "active route", "件の有効ルート")}
                </>
              ) : null}
            </span>
          </div>
          <div className="playground-actions">
            <button type="button" className={showModelDetails ? "secondary-button compact active" : "secondary-button compact"} onClick={() => setShowModelDetails((value) => !value)}>
              <Sparkles size={14} />
              {tx("模型详情")}
            </button>
            <button type="button" className={showCode ? "secondary-button compact active" : "secondary-button compact"} onClick={() => setShowCode((value) => !value)}>
              <Code2 size={14} />
              {tx("查看代码")}
            </button>
            <button type="button" className="secondary-button compact" onClick={clearHistory} disabled={messages.length === 0 && !lastResult}>
              <Trash2 size={14} />
              {tx("清空历史")}
            </button>
          </div>
        </div>

        {showModelDetails ? (
          <div className="playground-detail-strip">
            <DetailField label="类型" value={selectedModel ? modelCategoryLabel(modelCategory(selectedModel)) : "-"} />
            <DetailField label="能力" value={selectedModel?.modality || "chat"} />
            <DetailField label="上下文" value={contextWindow ? formatNumber(contextWindow) : "-"} />
            <DetailField label="最近路由" value={lastResult?.route ? `${lastResult.route.provider_name || lastResult.route.provider_id} / ${lastResult.route.provider_model}` : "待请求"} />
          </div>
        ) : null}

        {showCode ? (
          <div className="playground-code-drawer">
            <PlaygroundAPIExamples baseURL={api.baseURL} modelName={modelName || selectedModel?.name || "gpt-4.1-mini"} />
          </div>
        ) : null}

        {lastResult?.route ? (
          <div className="playground-route">
            <span>{lastResult.route.provider_name || lastResult.route.provider_id || "-"}</span>
            <em>{lastResult.route.provider_model || "-"}</em>
            <small>{lastResult.route.resource_name || lastResult.route.resource_id || tx("默认资源")} · {routeStrategyLabel(lastResult.route.strategy)} · {countWithUnit(lastResult.attempts?.length ?? 0, "次尝试", "attempt", "回試行")}</small>
          </div>
        ) : null}

        {error ? <div className="status-line error playground-error">{error}</div> : null}

        <div className="playground-chat">
          {messages.length === 0 ? (
            <div className="playground-empty">
              <Sparkles size={22} />
              <strong>{tx("试用")} {modelName || tx("当前模型")}</strong>
              <span>{models.length === 0 ? tx("当前没有可演练模型。请先在路由策略里启用至少一条模型线路。") : tx("体验一下，看看模型在 TokenHub 网关上的表现")}</span>
            </div>
          ) : (
            messages.map((message) => (
              <div className={`playground-message ${message.role}`} key={message.id}>
                <span>{message.role === "assistant" ? "Assistant" : message.role === "system" ? "System" : "User"}</span>
                <p>{message.content}</p>
              </div>
            ))
          )}
        </div>

        <form className="playground-composer" onSubmit={sendMessage}>
          <textarea
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={tx("说点什么...")}
            disabled={loading || models.length === 0}
          />
          <div className="playground-composer-actions">
            <button className="secondary-button compact" type="button" disabled title={tx("文件上传待接入")}>
              <Plus size={14} />
              {tx("上传文件")}
            </button>
            {lastResult?.usage ? (
              <div className="playground-foot">
                <span>Prompt {formatNumber(lastResult.usage.prompt_tokens ?? 0)}</span>
                <span>Completion {formatNumber(lastResult.usage.completion_tokens ?? 0)}</span>
                <span>Total {formatNumber(lastResult.usage.total_tokens ?? 0)}</span>
                {lastResult.request_id ? <span>{lastResult.request_id}</span> : null}
              </div>
            ) : null}
            <button className="playground-send-button" disabled={loading || !draft.trim() || models.length === 0} type="submit" title={tx("发送")}>
              <Send size={18} />
            </button>
          </div>
        </form>
      </section>
    </div>
  );
}

function PlaygroundConfigSlider({
  label,
  value,
  onChange,
  min,
  max,
  step,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  min: number;
  max: number;
  step: number;
}) {
  const numeric = Number(value);
  const rangeValue = Number.isFinite(numeric) ? Math.min(max, Math.max(min, numeric)) : min;
  return (
    <label className="playground-slider">
      <div>
        <span>{label}</span>
        <input type="number" value={value} min={min} max={max} step={step} onChange={(event) => onChange(event.target.value)} />
      </div>
      <input type="range" value={rangeValue} min={min} max={max} step={step} onChange={(event) => onChange(event.target.value)} />
    </label>
  );
}

function PlaygroundAPIExamples({ baseURL, modelName }: { baseURL: string; modelName: string }) {
  const [language, setLanguage] = useState<ApiExampleLanguage>("python");
  const [copied, setCopied] = useState(false);
  const examples = useMemo(() => apiExampleScripts(baseURL, modelName), [baseURL, modelName]);
  const current = examples[language];

  async function copyCurrent() {
    try {
      await navigator.clipboard?.writeText(current);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    } catch {
      setCopied(false);
    }
  }

  return (
    <section className="api-example-panel">
      <div className="api-example-header">
        <div>
          <strong>{tx("API 使用")}</strong>
          <span>{tx("使用以下代码示例集成 TokenHub 模型接口")}</span>
        </div>
        <button className="icon-button subtle" onClick={() => void copyCurrent()} type="button" title={tx("复制代码")}>
          {copied ? <Check size={15} /> : <Copy size={15} />}
        </button>
      </div>
      <div className="api-example-tabs" role="tablist" aria-label={tx("API 调用语言")}>
        {apiExampleLanguages.map((item) => (
          <button
            aria-selected={language === item.key}
            className={language === item.key ? "api-example-tab active" : "api-example-tab"}
            key={item.key}
            onClick={() => {
              setLanguage(item.key);
              setCopied(false);
            }}
            role="tab"
            type="button"
          >
            {item.label}
          </button>
        ))}
      </div>
      <div className="api-example-meta">
        <span>Chat Completions</span>
        <em>{modelName || tx("未选择模型")}</em>
      </div>
      <pre className="api-code-block"><code>{current}</code></pre>
    </section>
  );
}

function ProviderUpsertModal({
  mode,
  provider,
  api,
  catalog,
  standardModels,
  routes = [],
  loading,
  onClose,
  onSaved,
  setLoading,
  setError,
  setNotice,
}: {
  mode: "create" | "edit";
  provider?: Provider;
  api: ApiContext;
  catalog: ProviderCatalogEntry[];
  standardModels: Model[];
  routes?: ModelRoute[];
  loading: boolean;
  onClose: () => void;
  onSaved: () => Promise<void>;
  setLoading: (value: boolean) => void;
  setError: (value: string) => void;
  setNotice: (value: string) => void;
}) {
  const availableCategories = useMemo(() => catalogModelCategoryOptions(catalog), [catalog]);
  const initialCategory = availableCategories.find((item) => item.key !== "all")?.key ?? "custom";
  const initialEntry = catalog.find((entry) => providerEntrySupportsCategory(entry, initialCategory)) ?? catalog.find((entry) => entry.id === "custom") ?? catalog[0];
  const [modelCategory, setModelCategory] = useState(initialCategory);
  const [catalogID, setCatalogID] = useState(initialEntry?.id ?? "custom");
  const [detail, setDetail] = useState<ProviderCatalogEntry | null>(null);
  const [catalogQuery, setCatalogQuery] = useState("");
  const [modelQuery, setModelQuery] = useState("");
  const [modelLoading, setModelLoading] = useState(false);
  const [modelError, setModelError] = useState("");
  const [catalogReloadKey, setCatalogReloadKey] = useState(0);
  const [selectedModels, setSelectedModels] = useState<Record<string, boolean>>({});
  const [values, setValues] = useState<Record<string, string>>(() => ({
    id: mode === "edit" ? provider?.id ?? "" : "",
    name: mode === "edit" ? provider?.name ?? "" : initialEntry?.display_name ?? "",
    type: mode === "edit" ? provider?.type ?? "openai_compatible" : initialEntry?.type ?? "openai_compatible",
    base_url: mode === "edit" ? provider?.base_url ?? "" : initialEntry?.base_url ?? "",
    api_key: "",
    priority: String(provider?.priority ?? 10),
    status: provider?.status ?? "active",
    healthy: String(provider?.healthy ?? true),
    create_routes: mode === "create" ? "true" : "false",
  }));
  const [credentialMode, setCredentialMode] = useState<ProviderCredentialMode>("provider_api_key");
  const [accountValues, setAccountValues] = useState<Record<string, string>>(() =>
    providerResourceDraftDefaults({
      provider_id: "",
      name: mode === "edit" ? provider?.name ?? "" : initialEntry?.display_name || initialEntry?.name || "",
      base_url: mode === "edit" ? provider?.base_url ?? "" : initialEntry?.base_url ?? "",
    }),
  );
  const [accountOAuthCallback, setAccountOAuthCallback] = useState("");
  const [accountOAuthStatus, setAccountOAuthStatus] = useState("");
  const [accountOAuthBusy, setAccountOAuthBusy] = useState(false);
  const [createStep, setCreateStep] = useState(0);
  const createSteps = useMemo(() => providerCreateWizardSteps(), []);
  const lastCreateStep = createSteps.length - 1;
  const accountCallbackURL = useMemo(() => providerAccountOAuthCallbackURL(), []);
  const modalRef = useRef<HTMLFormElement | null>(null);
  const existingRouteModels = useMemo(
    () => new Set(routes.filter((route) => provider && route.provider_id === provider.id).map((route) => route.model_name)),
    [provider, routes],
  );

  const categoryCatalog = useMemo(
    () => catalog.filter((entry) => providerEntrySupportsCategory(entry, modelCategory)),
    [catalog, modelCategory],
  );
  const customCatalogEntry = useMemo(() => buildCustomProviderCatalogEntry(modelCategory, standardModels), [modelCategory, standardModels]);

  useEffect(() => {
    if (categoryCatalog.length === 0) return;
    if (!categoryCatalog.some((entry) => entry.id === catalogID)) {
      selectCatalog(categoryCatalog[0]);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelCategory, categoryCatalog.length]);

  useEffect(() => {
    const entry = catalogID === "custom" ? customCatalogEntry : catalog.find((item) => item.id === catalogID);
    setModelQuery("");
    setModelError("");
    if (entry && mode === "create") {
      setValues((current) => ({
        ...current,
        name: catalogID === "custom" ? (current.name === initialEntry?.display_name ? "" : current.name) : entry.display_name || entry.name || current.name,
        type: entry.type || current.type || "openai_compatible",
        base_url: catalogID === "custom" ? current.base_url : entry.base_url ?? "",
      }));
    }
    let cancelled = false;
    setDetail(null);
    setSelectedModels({});
    if (catalogID === "custom") {
      setDetail(customCatalogEntry);
      setModelLoading(false);
      return () => {
        cancelled = true;
      };
    }
    setModelLoading(true);
    adminFetch(api, `/api/admin/provider-catalog/${encodeURIComponent(catalogID)}`)
      .then(async (resp) => {
        if (!resp.ok) throw new Error(`provider catalog ${resp.status}`);
        return (await resp.json()) as { data: ProviderCatalogEntry };
      })
      .then((payload) => {
        if (cancelled) return;
        setDetail(payload.data);
        setModelError("");
        if (mode === "create") {
          setValues((current) => ({
            ...current,
            name: payload.data.display_name || payload.data.name || current.name,
            type: payload.data.type || current.type || "openai_compatible",
            base_url: payload.data.base_url ?? "",
          }));
        }
      })
      .catch((err) => {
        if (!cancelled) {
          if (isAuthExpiredError(err)) return;
          const message = err instanceof Error ? err.message : tx("Provider 模板加载失败");
          setModelError(message);
          setError(message);
        }
      })
      .finally(() => {
        if (!cancelled) setModelLoading(false);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalogID, catalogReloadKey, customCatalogEntry, initialEntry?.display_name, mode]);

  useEffect(() => {
    if (mode === "create") {
      modalRef.current?.scrollTo({ top: 0 });
    }
  }, [createStep, mode]);

  const models = useMemo(
    () => (detail?.models ?? []).filter((model) => {
      if (modelCategory !== "all" && modelCategoryForCatalog(model) !== modelCategory) return false;
      const canonical = model.canonical_name || canonicalModelNameForUI(model.id, model.display_name);
      return standardModels.some((standard) => canonicalModelNameForUI(standard.name, standard.name) === canonicalModelNameForUI(canonical, canonical));
    }),
    [detail, modelCategory, standardModels],
  );
  useEffect(() => {
    if (mode !== "create" || !detail || values.create_routes !== "true") return;
    const nextSelected: Record<string, boolean> = {};
    for (const model of models) {
      nextSelected[model.id] = true;
    }
    setSelectedModels(nextSelected);
  }, [detail, mode, models, values.create_routes]);
  const filteredCatalog = useMemo(() => {
    const normalized = catalogQuery.trim().toLowerCase();
    const entries = categoryCatalog;
    if (!normalized) return entries;
    return entries.filter((entry) =>
      [
        entry.id,
        entry.name,
        entry.display_name,
        entry.type,
        entry.base_url,
        entry.doc_url,
        ...(entry.categories ?? []),
      ].filter(Boolean).join(" ").toLowerCase().includes(normalized),
    );
  }, [categoryCatalog, catalogQuery]);
  const filteredModels = useMemo(() => {
    const normalized = modelQuery.trim().toLowerCase();
    if (!normalized) return models.slice(0, 80);
    return models
      .filter((model) => JSON.stringify(model).toLowerCase().includes(normalized))
      .slice(0, 80);
  }, [models, modelQuery]);
  const selectedModelIDs = Object.entries(selectedModels)
    .filter(([, selected]) => selected)
    .map(([id]) => id);
  const autoRouteEnabled = values.create_routes === "true";
  const selectedRouteCount = autoRouteEnabled ? selectedModelIDs.length : 0;
  const selectedEntry = detail ?? (catalogID === "custom" ? customCatalogEntry : catalog.find((entry) => entry.id === catalogID));
  const showProviderCatalog = mode === "edit" || (mode === "create" && createStep === 1 && credentialMode !== "account_integration");
  const providerBodyClassName = mode === "create" && !showProviderCatalog ? "provider-modal-body provider-wizard-single" : "provider-modal-body";
  const accountRuntimeFields = useMemo(() => providerCreateAccountRuntimeFields(), []);
  const accountManualTokenFields = useMemo(() => providerCreateAccountManualTokenFields(), []);
  const accountTokenSummary = useMemo(() => providerAccountTokenSummary(accountValues), [accountValues]);

  useEffect(() => {
    if (mode !== "create" || !hasPendingProviderAccountOAuthResult()) return;
    selectCredentialMode("account_integration");
    setCreateStep(2);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode]);

  useEffect(() => {
    if (mode !== "create" || credentialMode !== "account_integration") return;
    const pending = consumePendingProviderAccountOAuthResult();
    if (!pending) return;
    void applyProviderAccountOAuthResult(pending, tx("已从回调 URL 自动回填账号 Token。"));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mode, credentialMode]);

  useEffect(() => {
    if (mode !== "create" || credentialMode !== "account_integration" || catalog.length === 0 || catalogID !== "custom") return;
    const recommended = recommendedAccountProviderEntry(catalog);
    if (!recommended) return;
    setModelCategory("openai");
    selectCatalog(recommended);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [catalog.length, credentialMode, mode, catalogID]);

  function update(key: string, value: string) {
    const previousProviderName = values.name;
    const previousBaseURL = values.base_url;
    setValues((current) => ({ ...current, [key]: value }));
    if (mode !== "create") return;
    if (key === "name") {
      setAccountValues((current) => {
        if (current.name && current.name !== defaultProviderResourceName(previousProviderName)) return current;
        return { ...current, name: defaultProviderResourceName(value) };
      });
    }
    if (key === "base_url") {
      setAccountValues((current) => {
        if (current.base_url && current.base_url !== previousBaseURL) return current;
        return { ...current, base_url: value || "https://api.openai.com/v1" };
      });
    }
  }

  function updateAccountValue(key: string, value: string) {
    setAccountValues((current) => ({ ...current, [key]: value }));
  }

  async function applyProviderAccountOAuthResult(result: ProviderAccountOAuthResult, message: string) {
    if (result.error) {
      const errorMessage = `${tx("账号授权失败")}：${result.error}`;
      setAccountOAuthStatus(errorMessage);
      setError(errorMessage);
      clearPendingProviderAccountOAuthSession();
      return;
    }
    if (result.authorization_code && !result.access_token && !result.refresh_token && !result.id_token) {
      await exchangeProviderAccountAuthorizationCode(result, message);
      return;
    }
    if (!result.access_token && !result.refresh_token && !result.id_token) {
      setAccountOAuthStatus(tx("未在回调结果中识别到 Token。"));
      setError(tx("未在回调结果中识别到 Token。"));
      return;
    }
    setAccountValues((current) => ({
      ...current,
      resource_type: "openai_subscription",
      auth_type: "oauth",
      access_token: result.access_token || current.access_token || "",
      refresh_token: result.refresh_token || current.refresh_token || "",
      id_token: result.id_token || current.id_token || "",
      account_email: result.account_email || current.account_email || "",
      account_id: result.account_id || current.account_id || "",
      organization_id: result.organization_id || current.organization_id || "",
      plan_type: result.plan_type || current.plan_type || "",
      token_type: result.token_type || current.token_type || "",
      expires_at: result.expires_at || current.expires_at || "",
      scopes: result.scopes || current.scopes || "",
    }));
    setAccountOAuthStatus(message);
    setError("");
  }

  function parseAccountOAuthCallback(raw: string) {
    setAccountOAuthCallback(raw);
    const result = parseProviderAccountOAuthResult(raw, true);
    if (!result) return;
    void applyProviderAccountOAuthResult(result, tx("已从粘贴的回调结果回填账号 Token。"));
  }

  function parseAccountOAuthCallbackNow() {
    const result = parseProviderAccountOAuthResult(accountOAuthCallback, true);
    if (!result) {
      setAccountOAuthStatus(tx("未在回调结果中识别到 Token。"));
      setError(tx("未在回调结果中识别到 Token。"));
      return;
    }
    void applyProviderAccountOAuthResult(result, tx("已从粘贴的回调结果回填账号 Token。"));
  }

  async function exchangeProviderAccountAuthorizationCode(result: ProviderAccountOAuthResult, message: string) {
    const pendingSession = readPendingProviderAccountOAuthSession();
    const sessionID = result.session_id || pendingSession?.session_id || "";
    const state = result.state || pendingSession?.state || "";
    if (!sessionID || !state || !result.authorization_code) {
      setAccountOAuthStatus(tx("授权回调缺少会话信息，请重新打开授权。"));
      setError(tx("授权回调缺少会话信息，请重新打开授权。"));
      return;
    }
    setAccountOAuthBusy(true);
    setAccountOAuthStatus(tx("正在换取账号 Token..."));
    try {
      const resp = await adminFetch(api, "/api/admin/provider-account-oauth/openai/exchange-code", {
        method: "POST",
        body: JSON.stringify({
          session_id: sessionID,
          state,
          code: result.authorization_code,
        }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("账号授权换取 Token")));
      const tokenInfo = (await resp.json()) as ProviderAccountOAuthResult;
      clearPendingProviderAccountOAuthSession();
      await applyProviderAccountOAuthResult(tokenInfo, message);
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      const errorMessage = err instanceof Error ? err.message : tx("账号授权换取 Token 失败");
      setAccountOAuthStatus(errorMessage);
      setError(errorMessage);
    } finally {
      setAccountOAuthBusy(false);
    }
  }

  async function openProviderAccountAuthorization() {
    try {
      setAccountOAuthBusy(true);
      const resp = await adminFetch(api, "/api/admin/provider-account-oauth/openai/generate-auth-url", {
        method: "POST",
        body: JSON.stringify({ return_url: accountCallbackURL }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("生成账号授权地址")));
      const generated = (await resp.json()) as ProviderAccountOAuthGenerateResponse;
      savePendingProviderAccountOAuthSession({ session_id: generated.session_id, state: generated.state });
      window.open(generated.auth_url, "_blank", "noopener,noreferrer");
      setAccountOAuthStatus(tx("已打开 OpenAI/Codex 授权页，授权完成后会自动回填账号 Token。"));
      setError("");
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      const errorMessage = err instanceof Error ? err.message : tx("生成账号授权地址失败");
      setAccountOAuthStatus(errorMessage);
      setError(errorMessage);
    } finally {
      setAccountOAuthBusy(false);
    }
  }

  async function copyProviderAccountCallbackURL() {
    if (!accountCallbackURL) return;
    try {
      await navigator.clipboard.writeText(accountCallbackURL);
      setAccountOAuthStatus(tx("已复制回调地址。"));
    } catch {
      setAccountOAuthCallback(accountCallbackURL);
      setAccountOAuthStatus(accountCallbackURL);
    }
  }

  function selectCredentialMode(nextMode: ProviderCredentialMode) {
    setCredentialMode(nextMode);
    if (nextMode === "account_integration") {
      setAccountValues((current) => ({
        ...current,
        resource_type: "openai_subscription",
        auth_type: "oauth",
      }));
      const recommended = recommendedAccountProviderEntry(catalog);
      if (recommended) {
        setModelCategory("openai");
        setCatalogQuery("");
        setModelQuery("");
        setSelectedModels({});
        selectCatalog(recommended);
      }
    }
  }

  function syncAccountDefaults(providerName: string, baseURL?: string) {
    if (mode !== "create") return;
    setAccountValues((current) => ({
      ...current,
      name: defaultProviderResourceName(providerName),
      base_url: baseURL || "https://api.openai.com/v1",
    }));
  }

  function selectCategory(category: string) {
    setModelCategory(category);
    setCatalogQuery("");
    setModelQuery("");
    setSelectedModels({});
    const nextEntry = catalog.find((entry) => providerEntrySupportsCategory(entry, category));
    if (nextEntry) selectCatalog(nextEntry);
  }

  function selectCatalog(entry: ProviderCatalogEntry) {
    const nextName = entry.display_name || entry.name || values.name;
    setCatalogID(entry.id);
    setCatalogReloadKey((current) => current + 1);
    setDetail(null);
    setSelectedModels({});
    setModelQuery("");
    setModelError("");
    setValues((current) => ({
      ...current,
      name: mode === "create" ? entry.display_name || entry.name || current.name : current.name,
      type: entry.type || current.type || "openai_compatible",
      base_url: mode === "create" ? entry.base_url ?? "" : current.base_url,
    }));
    syncAccountDefaults(nextName, entry.base_url);
  }

  function selectCustomCatalog() {
    setCatalogID("custom");
    setCatalogReloadKey((current) => current + 1);
    setDetail(customCatalogEntry);
    setSelectedModels({});
    setModelQuery("");
    setModelError("");
    setValues((current) => ({
      ...current,
      id: mode === "create" ? "" : current.id,
      name: mode === "create" ? "" : current.name,
      type: current.type || "openai_compatible",
      base_url: mode === "create" ? "" : current.base_url,
    }));
    syncAccountDefaults(values.name || "Provider", "");
  }

  function canContinueCreateStep(targetStep = createStep) {
    if (mode !== "create") return true;
    if (targetStep === 0) return Boolean(credentialMode);
    if (targetStep === 1) {
      return Boolean(selectedEntry && values.name?.trim());
    }
    if (targetStep === 2 && credentialMode === "account_integration") return providerAccountResourceReady(accountValues);
    return true;
  }

  function validateCreateStep(targetStep = createStep) {
    if (mode !== "create") return true;
    if (targetStep === 0 && !credentialMode) {
      setError(tx("请先选择一种接入方式。"));
      return false;
    }
    if (targetStep === 1 && !selectedEntry) {
      setError(tx("请先选择一个渠道商。"));
      return false;
    }
    if (targetStep === 1 && !values.name?.trim()) {
      setError(tx(credentialMode === "account_integration" ? "请填写通道名称。" : "请填写渠道名称。"));
      return false;
    }
    if (targetStep === 2 && credentialMode === "account_integration") {
      try {
        assertProviderAccountResourceReady(accountValues);
      } catch (err) {
        setError(err instanceof Error ? err.message : tx("账号资源配置不完整"));
        return false;
      }
    }
    setError("");
    return true;
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (mode === "create" && createStep < lastCreateStep) {
      if (!validateCreateStep(createStep)) return;
      setCreateStep((current) => Math.min(current + 1, lastCreateStep));
      return;
    }
    setLoading(true);
    setError("");
    setNotice("");
    try {
      if (mode === "create" && credentialMode === "account_integration") {
        assertProviderAccountResourceReady(accountValues);
      }
      const payload = (mode === "edit" ? providerUpdatePayload : providerPayload)({
        ...values,
        api_key: mode === "create" && credentialMode !== "provider_api_key" ? "" : values.api_key,
        create_routes: autoRouteEnabled && selectedModelIDs.length > 0 ? "true" : "false",
        catalog_id: catalogID,
        model_category: modelCategory,
        selected_models: selectedModelIDs.length > 0 ? selectedModelIDs.join(",") : "",
      });
      const resp = await adminFetch(api, mode === "edit" && provider ? `/api/admin/providers/${provider.id}` : "/api/admin/providers", {
        method: mode === "edit" ? "PATCH" : "POST",
        body: JSON.stringify(payload),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, `${mode === "edit" ? tx("更新") : tx("创建")} ${tx("Provider 渠道")}`));
      const result = (await resp.json()) as { created_routes?: number; provider?: Provider };
      let accountResourceCreated = false;
      if (mode === "create" && credentialMode === "account_integration") {
        const payloadProviderID = typeof payload.id === "string" ? payload.id : "";
        const providerID = result.provider?.id || payloadProviderID || values.id;
        if (!providerID) throw new Error(tx("Provider 已创建，但无法确认账号资源所属 Provider。"));
        const resourceValues = {
          ...accountValues,
          provider_id: providerID,
          name: accountValues.name?.trim() || defaultProviderResourceName(result.provider?.name || values.name || providerID),
          base_url: accountValues.base_url?.trim() || values.base_url || "https://api.openai.com/v1",
        };
        const resourceResp = await adminFetch(api, "/api/admin/provider-resources", {
          method: "POST",
          body: JSON.stringify(providerResourcePayload(resourceValues)),
        });
        if (!resourceResp.ok) throw new Error(await readAdminError(resourceResp, tx("创建账号资源")));
        accountResourceCreated = true;
      }
      const routed = result.created_routes ?? 0;
      setNotice(`${tx("Provider 已")}${tx(mode === "edit" ? "更新" : "新增")}${accountResourceCreated ? `，${tx("已创建账号资源")}` : ""}${routed ? `，${tx("创建")} ${countWithUnit(routed, `条${modelCategoryLabel(modelCategory)}路由`, `${modelCategoryLabel(modelCategory)} route`, `${modelCategoryLabel(modelCategory)} ルート`)}` : ""}`);
      await onSaved();
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      setError(err instanceof Error ? err.message : tx("保存失败"));
    } finally {
      setLoading(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className={mode === "create" ? "modal provider-modal provider-wizard-modal" : "modal provider-modal"} ref={modalRef} onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{tx(mode === "edit" ? "编辑" : "新增")}</p>
            <h2>{tx("Provider 渠道")}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>
        {mode === "create" ? (
          <div className="wizard-stepper provider-wizard-stepper" aria-label={tx("创建 Provider 步骤")}>
            {createSteps.map((item, index) => {
              const Icon = item.icon;
              const title = providerCreateWizardStepTitle(item.title, credentialMode);
              return (
                <button
                  aria-current={createStep === index ? "step" : undefined}
                  className={createStep === index ? "wizard-step active" : index < createStep ? "wizard-step done" : "wizard-step"}
                  disabled={index > createStep || loading}
                  key={item.title}
                  onClick={() => setCreateStep(index)}
                  type="button"
                >
                  <span><Icon size={14} /></span>
                  <strong>{tx(title)}</strong>
                </button>
              );
            })}
          </div>
        ) : null}
        <div className={providerBodyClassName}>
          {showProviderCatalog ? (
            <section className="provider-catalog-pane">
            <div className="provider-catalog-head">
              <strong>{tx("模型类型")}</strong>
              <span>{countWithLabel(availableCategories.length, "类")}</span>
            </div>
            <div className="provider-category-list">
              {availableCategories.map((category) => (
                <button
                  className={category.key === modelCategory ? "provider-category-item active" : "provider-category-item"}
                  key={category.key}
                  onClick={() => selectCategory(category.key)}
                  type="button"
                >
                  <strong>{tx(category.label)}</strong>
                  <span>{countWithLabel(category.count, "个模型")}</span>
                </button>
              ))}
            </div>

            <div className="provider-catalog-head provider-catalog-subhead">
              <strong>{tx("渠道商")}</strong>
              <span>{filteredCatalog.length}/{categoryCatalog.length}</span>
            </div>
            <button
              className={catalogID === "custom" ? "custom-provider-button active" : "custom-provider-button"}
              onClick={selectCustomCatalog}
              type="button"
            >
              <Plus size={14} />
              <span>{tx("自定义渠道商")}</span>
              <em>{modelCategoryLabel(modelCategory)} · {countWithLabel(customCatalogEntry.models_count, "个标准模型")}</em>
            </button>
            <div className="provider-template-search">
              <Search size={14} />
              <input
                value={catalogQuery}
                onChange={(event) => setCatalogQuery(event.target.value)}
                placeholder={tx("搜索渠道商、ID、类型")}
              />
            </div>
            <div className="provider-catalog-list compact">
              {filteredCatalog.length === 0 ? (
                <div className="empty compact-empty">
                  <span>{tx("没有匹配的渠道商")}</span>
                  <button className="secondary-button" onClick={selectCustomCatalog} type="button">{tx("使用自定义渠道商")}</button>
                </div>
              ) : filteredCatalog.map((entry) => (
                <button
                  className={entry.id === catalogID ? "catalog-item active" : "catalog-item"}
                  key={entry.id}
                  onClick={() => selectCatalog(entry)}
                  type="button"
                >
                  <strong>{entry.display_name || entry.name}</strong>
                  <span>{providerTypeLabel(entry.type)} · {countWithLabel(providerEntryCategoryCount(entry, modelCategory), "个模型")}</span>
                </button>
              ))}
            </div>
          </section>
          ) : null}

          <section className="provider-config-pane">
            {mode === "edit" || createStep > 0 ? (
              <div className="provider-selected-summary">
                <strong>{modelCategoryLabel(modelCategory)}</strong>
                <span>{selectedEntry?.display_name || selectedEntry?.name || tx("请选择渠道商")}</span>
                <em>{providerTypeLabel(selectedEntry?.type || values.type || "openai_compatible")}</em>
              </div>
            ) : null}
            {mode === "create" && createStep === 0 ? (
              <section className="provider-wizard-panel provider-access-panel">
                <div className="wizard-panel-head">
                  <h3>{tx("选择接入方式")}</h3>
                  <p>{tx("先告诉 TokenHub 你手里有什么：上游 API Key、OpenAI 账号资源，或者只是先占位建路由。")}</p>
                </div>
                <div className="provider-access-options" role="radiogroup" aria-label={tx("选择接入方式")}>
                  {providerCredentialOptions().map((option) => {
                    const Icon = option.icon;
                    const active = credentialMode === option.key;
                    return (
                      <button
                        aria-checked={active}
                        className={active ? "provider-access-card active" : "provider-access-card"}
                        key={option.key}
                        onClick={() => selectCredentialMode(option.key)}
                        role="radio"
                        type="button"
                      >
                        <span><Icon size={18} /></span>
                        <strong>{tx(option.label)}</strong>
                        <em>{tx(option.description)}</em>
                        {option.key === "account_integration" ? <small>{tx("账号资源池会自动推荐 OpenAI 兼容通道，下一步只需确认 Base URL 和账号凭据。")}</small> : null}
                      </button>
                    );
                  })}
                </div>
              </section>
            ) : null}
            {mode === "create" && createStep === 1 ? (
              <section className="provider-wizard-panel">
                <div className="wizard-panel-head">
                  <h3>{tx(credentialMode === "account_integration" ? "确认账号通道和基础信息" : "选择渠道和基础信息")}</h3>
                  <p>{tx(credentialMode === "account_integration" ? "账号资源池已为你选好默认通道。这里通常只确认 Base URL；账号走企业代理时再修改。" : "选择上游渠道商模板，TokenHub 会带出类型、Base URL 和可映射模型。")}</p>
                </div>
                {credentialMode === "account_integration" ? (
                  <div className="provider-account-channel-note">
                    <strong>{tx("推荐通道")}</strong>
                    <span>{tx("默认通道只负责协议与 Base URL，真实账号 Token 会在下一步保存为账号资源。")}</span>
                  </div>
                ) : null}
                {!showProviderCatalog ? (
                  <div className="wizard-review-grid provider-create-review">
                    <ReviewItem label={credentialMode === "account_integration" ? "模型协议" : "模型类型"} value={modelCategoryLabel(modelCategory)} />
                    <ReviewItem label={credentialMode === "account_integration" ? "默认通道" : "渠道商"} value={selectedEntry?.display_name || selectedEntry?.name || "-"} />
                    <ReviewItem label={credentialMode === "account_integration" ? "兼容协议" : "渠道商类型"} value={providerTypeLabel(selectedEntry?.type || values.type || "openai_compatible")} />
                    <ReviewItem label="可映射模型" value={detail ? `${models.length}/${detail.models_count}` : tx("加载中")} />
                  </div>
                ) : null}
              </section>
            ) : null}
            {mode === "create" && createStep === 2 ? (
              <section className="provider-wizard-panel">
                <div className="wizard-panel-head">
                  <h3>{tx("配置账号与凭据")}</h3>
                  <p>{tx(credentialMode === "account_integration" ? "先完成账号授权回填；TokenHub 会把回填的 Token 保存为账号资源。" : "选择是直接保存 API Key、接入账号资源池，还是稍后补齐凭据。")}</p>
                </div>
              </section>
            ) : null}
            {mode === "create" && createStep === 3 ? (
              <section className="provider-wizard-panel">
                <div className="wizard-panel-head">
                  <h3>{tx("确认路由策略")}</h3>
                  <p>{tx("选择是否自动创建默认路由，并确认要映射到标准模型目录的上游模型。")}</p>
                </div>
                <div className="wizard-review-grid provider-create-review">
                  <ReviewItem label="渠道商" value={values.name || selectedEntry?.display_name || selectedEntry?.name || "-"} />
                  <ReviewItem label="凭据方式" value={providerCredentialModeLabel(credentialMode)} />
                  <ReviewItem label="自动路由" value={autoRouteEnabled ? tx("开启") : tx("关闭开关")} />
                  <ReviewItem label="已选模型" value={selectedRouteCount ? String(selectedRouteCount) : tx("无")} />
                </div>
              </section>
            ) : null}
            {mode === "create" && createStep === 2 ? (
              <section className="provider-credential-panel">
                <div className="provider-credential-head">
                  <div>
                    <strong>{tx("认证与账号来源")}</strong>
                    <span>{tx("选择 Provider 使用哪一种上游凭据。账号集成会把账号作为资源池管理，适合 OpenAI subscription 或多个账号轮询。")}</span>
                  </div>
                </div>
                <div className="provider-credential-options" role="radiogroup" aria-label={tx("认证与账号来源")}>
                  {providerCredentialOptions().map((option) => {
                    const Icon = option.icon;
                    const active = credentialMode === option.key;
                    return (
                      <button
                        aria-checked={active}
                        className={active ? "provider-credential-option active" : "provider-credential-option"}
                        key={option.key}
                        onClick={() => selectCredentialMode(option.key)}
                        role="radio"
                        type="button"
                      >
                        <Icon size={16} />
                        <span>
                          <strong>{tx(option.label)}</strong>
                          <em>{tx(option.description)}</em>
                        </span>
                      </button>
                    );
                  })}
                </div>
                {credentialMode === "provider_api_key" ? (
                  <div className="provider-direct-key-fields">
                    <label className="field">
                      <span>API Key</span>
                      <input value={values.api_key ?? ""} type="password" onChange={(event) => update("api_key", event.target.value)} />
                    </label>
                  </div>
                ) : credentialMode === "account_integration" ? (
                  <div className="provider-account-inline">
                    <div className="provider-account-inline-head">
                      <strong>{tx("账号授权")}</strong>
                      <span>{tx("使用 OpenAI/Codex OAuth 授权账号；TokenHub 会在后端换取并保存账号 Token。")}</span>
                    </div>
                    <div className="provider-account-auth-grid">
                      <label className="field">
                        <span>{tx("账号资源名称")}</span>
                        <input value={accountValues.name ?? ""} onChange={(event) => updateAccountValue("name", event.target.value)} required />
                      </label>
                      <label className="field">
                        <span>{tx("账号地址/邮箱")}</span>
                        <input value={accountValues.account_email ?? ""} onChange={(event) => updateAccountValue("account_email", event.target.value)} placeholder="name@example.com" />
                        <small>{tx("用于区分账号资源，可填写邮箱或账号系统里的唯一地址。")}</small>
                      </label>
                      <label className="field provider-account-auth-wide">
                        <span>{tx("OpenAI/Codex 授权")}</span>
                        <div className="field-action-row">
                          <input readOnly value={accountCallbackURL} />
                          <button className="secondary-button" onClick={openProviderAccountAuthorization} type="button" disabled={accountOAuthBusy}>
                            <Send size={14} />
                            {tx(accountOAuthBusy ? "授权中" : "打开授权")}
                          </button>
                        </div>
                        <small>{tx("点击后由后端生成授权地址；授权完成会带 code 回到本页并自动换取 Token。")}</small>
                      </label>
                      <label className="field provider-account-auth-wide">
                        <span>{tx("回调结果")}</span>
                        <textarea
                          value={accountOAuthCallback}
                          onChange={(event) => parseAccountOAuthCallback(event.target.value)}
                          placeholder="http://localhost:3000/providers?provider_account_oauth=1&code=..."
                        />
                        <small>{tx("如果授权页没有自动跳回本页，把完整 callback URL 或 URL fragment 粘贴到这里。")}</small>
                      </label>
                      <div className="provider-account-auth-actions">
                        <button className="secondary-button" onClick={parseAccountOAuthCallbackNow} type="button">
                          <Check size={14} />
                          {tx("解析回填")}
                        </button>
                        <button className="secondary-button" onClick={copyProviderAccountCallbackURL} type="button">
                          <Copy size={14} />
                          {tx("复制回调地址")}
                        </button>
                        <div className={accountTokenSummary.ready ? "provider-account-token-status ready" : "provider-account-token-status"}>
                          {accountTokenSummary.ready ? <Check size={15} /> : <AlertCircle size={15} />}
                          <span>{tx(accountTokenSummary.ready ? "已回填账号 Token" : "等待授权回填")}</span>
                          {accountTokenSummary.items.map((item) => <em key={item}>{tx(item)}</em>)}
                        </div>
                      </div>
                    </div>
                    {accountOAuthStatus ? <p className="provider-credential-note">{accountOAuthStatus}</p> : null}
                    <div className="provider-account-runtime">
                      <div className="provider-account-inline-head">
                        <strong>{tx("资源调度")}</strong>
                        <span>{tx("这些配置决定账号资源参与路由时的权重、并发和限流。")}</span>
                      </div>
                      <div className="provider-account-fields compact">
                        {accountRuntimeFields.filter((field) => field.visible?.(accountValues) ?? true).map((field) => (
                          <ProviderInlineField
                            key={field.key}
                            field={field}
                            value={accountValues[field.key] ?? ""}
                            values={accountValues}
                            onChange={(value) => updateAccountValue(field.key, value)}
                          />
                        ))}
                      </div>
                    </div>
                    <details className="provider-account-advanced">
                      <summary>{tx("高级：手动粘贴 Token")}</summary>
                      <p>{tx("只有在授权回填不可用时使用；保存后 Token 不会再次显示。")}</p>
                      <div className="provider-account-fields">
                        {accountManualTokenFields.filter((field) => field.visible?.(accountValues) ?? true).map((field) => (
                          <ProviderInlineField
                            key={field.key}
                            field={field}
                            value={accountValues[field.key] ?? ""}
                            values={accountValues}
                            onChange={(value) => updateAccountValue(field.key, value)}
                          />
                        ))}
                      </div>
                    </details>
                  </div>
                ) : (
                  <p className="provider-credential-note">
                    {tx("保存后不会写入上游凭据，可稍后通过编辑 Provider 或账号集成补齐。")}
                  </p>
                )}
              </section>
            ) : null}
            {mode === "edit" || createStep === 1 ? (
              <div className="provider-form-grid">
              <label className="field">
                <span>Provider ID</span>
                <input value={values.id ?? ""} onChange={(event) => update("id", event.target.value)} placeholder={catalogID === "custom" ? tx("例如 prv_company_proxy") : tx("留空自动生成")} readOnly={mode === "edit"} />
              </label>
              <label className="field">
                <span>{tx(credentialMode === "account_integration" ? "通道名称" : "渠道名称")}</span>
                <input value={values.name ?? ""} onChange={(event) => update("name", event.target.value)} required />
              </label>
              <label className="field">
                <span>{tx(credentialMode === "account_integration" ? "兼容协议" : "渠道商类型")}</span>
                <select value={values.type ?? ""} onChange={(event) => update("type", event.target.value)} required>
                  {providerTypeOptions.map((option) => <option key={option} value={option}>{providerTypeLabel(option)}</option>)}
                </select>
              </label>
              <label className="field">
                <span>Base URL</span>
                <input value={values.base_url ?? ""} onChange={(event) => update("base_url", event.target.value)} />
              </label>
              {mode === "edit" ? (
                <label className="field">
                  <span>API Key</span>
                  <input value={values.api_key ?? ""} type="password" onChange={(event) => update("api_key", event.target.value)} />
                  {mode === "edit" ? <small>{tx("留空表示不修改现有 Key；填写新值才会覆盖。")}</small> : null}
                </label>
              ) : null}
              <label className="field">
                <span>{tx("优先级")}</span>
                <input value={values.priority ?? "10"} type="number" onChange={(event) => update("priority", event.target.value)} />
              </label>
            </div>
            ) : null}

            {mode === "edit" || createStep === 3 ? (
              <>
            <div className="provider-import-options">
              <div>
                <strong>{tx("自动路由")}</strong>
                <span>{mode === "edit" ? tx("开启后会为下方勾选模型补齐缺失线路，不覆盖已有策略。") : tx("保存渠道时会自动创建下方勾选模型的默认路由。")}</span>
              </div>
              <div className="boolean-toggle provider-route-toggle" role="radiogroup" aria-label={tx("自动路由")}>
                <button
                  aria-checked={autoRouteEnabled}
                  className={autoRouteEnabled ? "active" : ""}
                  onClick={() => update("create_routes", "true")}
                  role="radio"
                  type="button"
                >
                  {tx("开启")}
                </button>
                <button
                  aria-checked={!autoRouteEnabled}
                  className={!autoRouteEnabled ? "active" : ""}
                  onClick={() => update("create_routes", "false")}
                  role="radio"
                  type="button"
                >
                  {tx("关闭开关")}
                </button>
              </div>
            </div>

            <div className="provider-model-head">
              <div>
                <strong>{tx("上游模型映射")}</strong>
                <span>{detail ? `${models.length}/${detail.models_count} ${tx("个可映射模型")}` : tx("加载中")}</span>
              </div>
              <div className="provider-model-tools">
                <input value={modelQuery} onChange={(event) => setModelQuery(event.target.value)} placeholder={tx("搜索模型、能力、参数")} />
                <button className="secondary-button" onClick={() => selectedEntry && selectCatalog(selectedEntry)} type="button">
                  {tx("重新加载")}
                </button>
              </div>
            </div>
            <div className="provider-model-list">
              {modelLoading ? (
                <div className="empty">{tx("正在加载模型列表...")}</div>
              ) : modelError ? (
                <div className="empty">{modelError}</div>
              ) : filteredModels.length === 0 ? (
                <div className="empty">{models.length === 0 ? tx("该渠道商暂无可匹配当前标准模型目录的上游模型") : tx("没有匹配的模型")}</div>
              ) : filteredModels.map((model) => (
                <label className="model-option" key={model.id}>
                  <input
                    checked={autoRouteEnabled && selectedModels[model.id] === true}
                    disabled={!autoRouteEnabled}
                    onChange={(event) => setSelectedModels((current) => ({ ...current, [model.id]: event.target.checked }))}
                    type="checkbox"
                  />
                  <div>
                    <strong>{model.display_name || model.name}</strong>
                    <span>{model.canonical_name || model.id} ← {model.id}</span>
                    <small>
                      {modelCategoryLabel(modelCategoryForCatalog(model))} · {model.family || "model"} · {model.type || "chat"} · {formatModelPrice(model)} · {model.context_window ? `${compactNumber(model.context_window)} ctx` : "ctx -"}
                      {existingRouteModels.has(model.canonical_name || canonicalModelNameForUI(model.id, model.display_name)) ? ` · ${tx("已有路由")}` : ""}
                    </small>
                    <div className="capability-row">
                      {modelCapabilities(model).map((capability) => <em key={capability}>{capability}</em>)}
                    </div>
                  </div>
                </label>
              ))}
            </div>
            <p className="provider-import-hint">
              {!autoRouteEnabled
                ? tx("已关闭自动路由：保存后只创建 Provider，不生成路由策略。")
                : selectedRouteCount > 0
                  ? `${tx("保存后会为")} ${selectedRouteCount} ${tx("个已选")} ${modelCategoryLabel(modelCategory)} ${tx("模型创建缺失的默认路由。")}`
                  : tx("当前没有勾选模型，保存后不会生成路由策略。")}
            </p>
              </>
            ) : null}
          </section>
        </div>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          {mode === "create" && createStep > 0 ? (
            <button className="secondary-button" onClick={() => setCreateStep((current) => Math.max(current - 1, 0))} type="button" disabled={loading}>
              {tx("上一步")}
            </button>
          ) : null}
          <button className="button" disabled={loading} type="submit">
            {mode === "create"
              ? createStep === lastCreateStep
                ? loading ? tx("保存中") : tx("保存 Provider")
                : tx("下一步")
              : tx("保存")}
          </button>
        </div>
      </form>
    </div>
  );
}

function ProviderInlineField({
  field,
  value,
  values,
  onChange,
}: {
  field: FieldConfig;
  value: string;
  values: Record<string, string>;
  onChange: (value: string) => void;
}) {
  if (!(field.visible?.(values) ?? true)) return null;
  const autoComplete = field.autoComplete ?? "off";
  const inputName = `tokenhub-provider-account-${field.key}`;
  const options = (field.options ?? []).map((option) => ({ value: option, label: enumOptionLabel(field.key, option) }));
  if (field.type === "select") {
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <select value={value} onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }} onInvalid={handleRequiredFieldInvalid} required={field.required}>
          <option value="">{tx("请选择")}</option>
          {options.map((option) => (
            <option key={option.value} value={option.value}>{tx(option.label)}</option>
          ))}
        </select>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  if (field.type === "textarea") {
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <textarea
          autoComplete={autoComplete}
          data-1p-ignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
          data-lpignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
          name={inputName}
          value={value}
          onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }}
          onInvalid={handleRequiredFieldInvalid}
          placeholder={tx(field.placeholder)}
          required={field.required}
        />
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  if (field.type === "boolean") {
    const checked = value === "true";
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <div className="boolean-toggle" role="radiogroup" aria-label={tx(field.label)}>
          <button aria-checked={checked} className={checked ? "active" : ""} onClick={() => onChange("true")} role="radio" type="button">
            {tx("开启")}
          </button>
          <button aria-checked={!checked} className={!checked ? "active" : ""} onClick={() => onChange("false")} role="radio" type="button">
            {tx("关闭开关")}
          </button>
        </div>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  return (
    <label className="field" data-field-key={field.key}>
      <span>{tx(field.label)}</span>
      <input
        autoComplete={autoComplete}
        data-1p-ignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
        data-lpignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
        name={inputName}
        value={value}
        type={field.type === "number" ? "number" : field.type === "password" ? "password" : "text"}
        onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }}
        onInvalid={handleRequiredFieldInvalid}
        placeholder={tx(field.placeholder)}
        required={field.required}
      />
      {field.help ? <small>{tx(field.help)}</small> : null}
    </label>
  );
}

function providerCredentialOptions(): Array<{ key: ProviderCredentialMode; label: string; description: string; icon: typeof KeyRound }> {
  return [
    {
      key: "provider_api_key",
      label: "直接 API Key",
      description: "把上游 Key 保存到 Provider，适合单账号或兼容 API。",
      icon: KeyRound,
    },
    {
      key: "account_integration",
      label: "账号资源池",
      description: "适合 OpenAI 账号、Subscription 或多账号轮询，默认通道会自动推荐。",
      icon: UserRoundCheck,
    },
    {
      key: "later",
      label: "稍后配置",
      description: "先创建 Provider 和路由，稍后再添加 Key 或账号资源。",
      icon: Settings,
    },
  ];
}

function providerCreateWizardSteps(): Array<{ title: string; icon: typeof Search }> {
  return [
    { title: "接入方式", icon: UserRoundCheck },
    { title: "渠道信息", icon: Server },
    { title: "账号与凭据", icon: KeyRound },
    { title: "路由与确认", icon: Boxes },
  ];
}

function providerCreateWizardStepTitle(title: string, credentialMode: ProviderCredentialMode) {
  if (credentialMode === "account_integration" && title === "渠道信息") return "默认通道";
  return title;
}

function providerCredentialModeLabel(mode: ProviderCredentialMode) {
  return providerCredentialOptions().find((option) => option.key === mode)?.label ?? mode;
}

function providerAccountResourceReady(values: Record<string, string>) {
  if (values.resource_type === "openai_subscription") {
    return Boolean(values.access_token?.trim() || values.refresh_token?.trim() || values.id_token?.trim());
  }
  return Boolean(values.api_key?.trim());
}

function recommendedAccountProviderEntry(catalog: ProviderCatalogEntry[]) {
  const openAIEntries = catalog.filter((entry) => providerEntrySupportsCategory(entry, "openai"));
  const exactOpenAI = openAIEntries.find((entry) => {
    const candidates = [entry.id, entry.name, entry.display_name].map((item) => item?.trim().toLowerCase()).filter(Boolean);
    return candidates.some((item) => item === "openai" || item === "openai official");
  });
  if (exactOpenAI) return exactOpenAI;
  return openAIEntries.find((entry) => ["openai", "openai_compatible"].includes(entry.type)) ?? openAIEntries[0] ?? catalog[0];
}

function ConfirmDialog({
  title,
  message,
  loading,
  onCancel,
  onConfirm,
}: {
  title: string;
  message: string;
  loading: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div className="modal-backdrop" role="presentation">
      <div className="confirm-modal" role="dialog" aria-modal="true">
        <h2>{tx(title)}</h2>
        <p>{tx(message)}</p>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onCancel} type="button">{tx("取消")}</button>
          <button className="danger-confirm" onClick={onConfirm} disabled={loading} type="button">{tx("删除")}</button>
        </div>
      </div>
    </div>
  );
}

function IssuedKeyModal({ value, onClose }: { value: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const [closeCountdown, setCloseCountdown] = useState(3);

  useEffect(() => {
    if (closeCountdown <= 0) return;
    const timer = window.setTimeout(() => setCloseCountdown((current) => Math.max(current - 1, 0)), 1000);
    return () => window.clearTimeout(timer);
  }, [closeCountdown]);

  async function copyKey() {
    try {
      await navigator.clipboard?.writeText(value);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <div className="confirm-modal issued-key-modal" role="dialog" aria-modal="true" aria-labelledby="issued-key-title">
        <div className="issued-key-icon" aria-hidden="true">
          <KeyRound size={18} />
        </div>
        <div>
          <p className="eyebrow">{tx("新 Key 仅展示一次：")}</p>
          <h2 id="issued-key-title">{tx("新 Key 已生成")}</h2>
          <p>{tx("请现在复制并保存这个 Key。关闭弹窗后将无法再次查看完整 Key，只能通过轮换生成新的 Key。")}</p>
        </div>
        <label className="issued-key-field">
          <span>{tx("完整 Key")}</span>
          <textarea
            readOnly
            value={value}
            onFocus={(event) => event.currentTarget.select()}
          />
        </label>
        <div className="modal-actions">
          <button className="secondary-button" onClick={() => void copyKey()} type="button">
            {copied ? <Check size={16} /> : <Copy size={16} />}
            {copied ? tx("已复制") : tx("复制 Key")}
          </button>
          <button className="button" disabled={closeCountdown > 0} onClick={onClose} type="button">
            {closeCountdown > 0 ? issuedKeyCloseCountdownLabel(closeCountdown) : tx("我已保存，关闭")}
          </button>
        </div>
      </div>
    </div>
  );
}

function issuedKeyCloseCountdownLabel(seconds: number) {
  if (activeLanguage === "en") return `Close in ${seconds}s`;
  if (activeLanguage === "ja") return `${seconds} 秒後に閉じる`;
  return `${seconds}s 后可关闭`;
}

function FieldInput({
  field,
  data,
  currentUser,
  value,
  editing,
  onChange,
}: {
  field: FieldConfig;
  data: AppData;
  currentUser?: AdminUser | null;
  value: string;
  editing: boolean;
  onChange: (value: string) => void;
}) {
  const [filter, setFilter] = useState("");
  const readOnly = editing && field.readOnlyOnEdit;
  const autoComplete = field.autoComplete ?? "off";
  const inputName = `tokenhub-${field.key}`;
  let options = field.optionsFromData?.(data, currentUser) ?? (field.options ?? []).map((option) => ({ value: option, label: enumOptionLabel(field.key, option) }));
  if (value && !options.some((option) => option.value === value)) {
    options = [...options, { value, label: value }];
  }
  if (field.type === "multi-select" && !editing) {
    const selected = new Set(splitList(value));
    const normalizedFilter = filter.trim().toLowerCase();
    const filteredOptions = normalizedFilter
      ? options.filter((option) => `${option.label} ${option.value}`.toLowerCase().includes(normalizedFilter))
      : options;
    const selectedCount = selected.size;
    const updateSelected = (next: Set<string>) => onChange(Array.from(next).join(", "));
    return (
      <div className="field multi-select-field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <div className="multi-select-tools">
          <input
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={tx("搜索模型")}
            type="search"
          />
          <button
            className="secondary-button"
            onClick={() => updateSelected(new Set([...selected, ...filteredOptions.map((option) => option.value)]))}
            type="button"
          >
            {tx("全选")}
          </button>
          <button className="secondary-button" onClick={() => onChange("")} type="button">
            {tx("清空")}
          </button>
        </div>
        <div className="multi-select-list">
          {filteredOptions.length === 0 ? (
            <div className="empty">{tx("没有匹配的模型")}</div>
          ) : filteredOptions.map((option) => (
            <label className="multi-select-option" key={option.value}>
              <input
                checked={selected.has(option.value)}
                onChange={(event) => {
                  const next = new Set(selected);
                  if (event.target.checked) {
                    next.add(option.value);
                  } else {
                    next.delete(option.value);
                  }
                  updateSelected(next);
                }}
                type="checkbox"
              />
              <span>{tx(option.label)}</span>
            </label>
          ))}
        </div>
        <small>{selectedCount > 0 ? selectedModelsText(selectedCount) : tx("请选择至少一个统一模型")}</small>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </div>
    );
  }
  if (field.type === "select" || field.type === "multi-select") {
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <select value={value} onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }} onInvalid={handleRequiredFieldInvalid} required={field.required} disabled={readOnly}>
          <option value="">{tx("请选择")}</option>
          {options.map((option) => (
            <option key={option.value} value={option.value}>{tx(option.label)}</option>
          ))}
        </select>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  if (field.type === "textarea") {
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <textarea
          autoComplete={autoComplete}
          data-1p-ignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
          data-lpignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
          name={inputName}
          value={value}
          onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }}
          onInvalid={handleRequiredFieldInvalid}
          placeholder={tx(field.placeholder)}
          required={field.required}
          readOnly={readOnly}
        />
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  if (field.type === "boolean") {
    const checked = value === "true";
    return (
      <label className="field" data-field-key={field.key}>
        <span>{tx(field.label)}</span>
        <div className="boolean-toggle" role="radiogroup" aria-label={tx(field.label)}>
          <button
            aria-checked={checked}
            className={checked ? "active" : ""}
            disabled={readOnly}
            onClick={() => onChange("true")}
            role="radio"
            type="button"
          >
            {tx("开启")}
          </button>
          <button
            aria-checked={!checked}
            className={!checked ? "active" : ""}
            disabled={readOnly}
            onClick={() => onChange("false")}
            role="radio"
            type="button"
          >
            {tx("关闭开关")}
          </button>
        </div>
        {field.help ? <small>{tx(field.help)}</small> : null}
      </label>
    );
  }
  return (
    <label className="field" data-field-key={field.key}>
      <span>{tx(field.label)}</span>
      <input
        autoComplete={autoComplete}
        data-1p-ignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
        data-lpignore={autoComplete === "off" || autoComplete === "new-password" ? "true" : undefined}
        name={inputName}
        value={value}
        type={field.type === "number" ? "number" : field.type === "password" ? "password" : "text"}
        onChange={(event) => { clearCustomValidity(event); onChange(event.target.value); }}
        onInvalid={handleRequiredFieldInvalid}
        placeholder={tx(field.placeholder)}
        required={field.required}
        readOnly={readOnly}
      />
      {field.help ? <small>{tx(field.help)}</small> : null}
    </label>
  );
}

function DataSection({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="section">
      <div className="section-header">
        <h2>{tx(title)}</h2>
      </div>
      <div className="section-body">{children}</div>
    </section>
  );
}

function SimpleTable({
  columns,
  rows,
  paginationKey,
}: {
  columns: string[];
  rows: React.ReactNode[][];
  paginationKey?: string;
}) {
  if (rows.length === 0) return <div className="empty">{tx("暂无数据")}</div>;
  if (paginationKey) {
    return <PaginatedSimpleTable columns={columns} rows={rows} paginationKey={paginationKey} />;
  }
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>{columns.map((column) => <th key={column}>{tx(column)}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((row, index) => (
            <tr key={index}>{row.map((cell, cellIndex) => <td key={cellIndex}>{translatedCell(cell)}</td>)}</tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

function PaginatedSimpleTable({
  columns,
  rows,
  paginationKey,
}: {
  columns: string[];
  rows: React.ReactNode[][];
  paginationKey: string;
}) {
  const pagination = usePagination(rows.length, paginationKey);
  const visibleRows = useMemo(
    () => rows.slice(pagination.startIndex, pagination.endIndex),
    [rows, pagination.startIndex, pagination.endIndex],
  );
  return (
    <>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>{columns.map((column) => <th key={column}>{tx(column)}</th>)}</tr>
          </thead>
          <tbody>
            {visibleRows.map((row, index) => (
              <tr key={pagination.startIndex + index}>{row.map((cell, cellIndex) => <td key={cellIndex}>{translatedCell(cell)}</td>)}</tr>
            ))}
          </tbody>
        </table>
      </div>
      <PaginationControls pagination={pagination} totalItems={rows.length} />
    </>
  );
}

function UsageBarChart({ data }: { data: UsagePoint[] }) {
  const points = data.length ? data : fallbackDays();
  const max = Math.max(...points.map((point) => point.total_tokens), 1);
  const width = 1240;
  const height = 330;
  const chartTop = 24;
  const chartBottom = 52;
  const chartHeight = height - chartTop - chartBottom;
  const gap = 8;
  const barWidth = Math.max(8, (width - 100 - gap * points.length) / points.length);

  return (
    <div className="chart-wrap">
      <div className="legend">
        <span className="legend-dot" />
        <span>{points.some((point) => point.total_tokens > 0) ? tx("模型调用 Token") : tx("等待调用数据")}</span>
      </div>
      <svg className="chart" viewBox={`0 0 ${width} ${height}`} role="img">
        {[0, 0.25, 0.5, 0.75, 1].map((ratio) => {
          const y = chartTop + chartHeight - chartHeight * ratio;
          return (
            <g key={ratio}>
              <line x1="40" x2={width - 20} y1={y} y2={y} />
              <text x="8" y={y + 4}>{compactNumber(Math.round(max * ratio))}</text>
            </g>
          );
        })}
        {points.map((point, index) => {
          const x = 60 + index * (barWidth + gap);
          const barHeight = (point.total_tokens / max) * chartHeight;
          const y = chartTop + chartHeight - barHeight;
          return (
            <g key={point.date}>
              <rect x={x} y={y} width={barWidth} height={Math.max(1, barHeight)} rx="1" />
              {index % 2 === 0 ? (
                <text x={x + barWidth / 2} y={height - 18} textAnchor="middle">
                  {point.date.slice(5)}
                </text>
              ) : null}
            </g>
          );
        })}
      </svg>
    </div>
  );
}

function StatusPill({ status, label }: { status: string; label?: string }) {
  const normalized = String(status).toLowerCase();
  const kind =
    normalized === "active" || normalized === "healthy" || normalized === "ok" || normalized === "confirmed" || normalized === "approved"
      ? "ok"
      : normalized === "warning" || normalized === "degraded" || normalized === "pending"
        ? "warn"
        : normalized === "error" || normalized === "down" || normalized === "disabled" || normalized === "rejected" || normalized === "failed" || normalized === "revoked" || normalized === "expired"
          ? "error"
          : "";
  return <span className={`pill ${kind}`}>{label ? tx(label) : enumValueLabel(status)}</span>;
}

function ModelNameCell({ model }: { model: Model }) {
  return (
    <div className="model-name-cell">
      <strong>{model.name}</strong>
      <span>{modelCategoryLabel(modelCategory(model))} · {model.family || "-"} · {model.modality || "chat"} · {model.context_window ? `${compactNumber(model.context_window)} ctx` : "ctx -"}</span>
    </div>
  );
}

function ModelRouteProviders({ model, data }: { model: Model; data: AppData }) {
  const routes = modelRoutesFor(model, data);
  if (routes.length === 0) {
    return <span className="muted-inline">{tx("未配置线路")}</span>;
  }
  return (
    <div className="route-provider-list">
      {routes.slice(0, 4).map((route) => {
        const provider = findProvider(data, route.provider_id);
        return (
          <div className="route-provider-chip" key={route.id}>
            <span className={route.status === "active" ? "route-dot ok" : "route-dot"} />
            <strong>{provider?.name || route.provider_id}</strong>
            <em>{route.provider_model}</em>
            <small>{routeStrategyLabel(route.strategy)} · P{route.priority} · W{route.weight}</small>
          </div>
        );
      })}
      {routes.length > 4 ? <span className="route-overflow">+{routes.length - 4}</span> : null}
    </div>
  );
}

const providerTypeOptions = ["mock", "openai", "openai_compatible", "azure_openai", "anthropic", "gemini", "deepseek", "qwen", "local"];

const modelCategoryLabels: Record<string, string> = {
  all: "全部",
  openai: "OpenAI",
  claude: "Claude",
  deepseek: "DeepSeek",
  gemini: "Gemini",
  qwen: "Qwen",
  glm: "GLM",
  kimi: "Kimi",
  doubao: "Doubao",
  ernie: "ERNIE",
  baichuan: "Baichuan",
  minimax: "MiniMax",
  stepfun: "StepFun",
  wanx: "WanX",
  paddlepaddle: "PaddlePaddle",
  microsoft: "Microsoft",
  llama: "Llama",
  mistral: "Mistral",
  grok: "Grok",
  custom: "自定义",
};

const preferredModelCategories = [
  "openai",
  "claude",
  "deepseek",
  "gemini",
  "qwen",
  "glm",
  "kimi",
  "doubao",
  "ernie",
  "baichuan",
  "minimax",
  "stepfun",
  "wanx",
  "grok",
  "paddlepaddle",
  "microsoft",
  "llama",
  "mistral",
  "custom",
];

const resourceConfigs: Partial<Record<ViewKey, ResourceConfig<any>>> = {
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

function systemSettingConfig(): ResourceConfig<AdminResource> {
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

function identityProviderConfig(): ResourceConfig<AdminResource> {
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

function roleConfig(): ResourceConfig<AdminResource> {
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

function teamConfig(): ResourceConfig<AdminResource> {
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

function sqliteBackupConfig(): ResourceConfig<SQLiteBackup> {
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

function notificationChannelConfig(): ResourceConfig<AdminResource> {
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

function monitorConfig(): ResourceConfig<AdminResource> {
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

function providerConfig(): ResourceConfig<Provider> {
  return {
    view: "providers",
    title: "Provider 渠道",
    eyebrow: "Provider 列表",
    description: "Provider 是一个可调用的上游渠道实例，包含服务商类型、Base URL、API Key、健康状态和标准模型路由。",
    createLabel: "新增 Provider",
    columns: [
      { key: "name", label: "名称" },
      { key: "type", label: "类型", render: (item) => providerTypeLabel(item.type) },
      { key: "base_url", label: "Base URL", render: (item) => item.base_url || "local mock" },
      { key: "routes", label: "路由线路", render: (item, ctx) => providerRouteSummary(item, ctx) },
      { key: "account_resources", label: "账号资源", render: (item, ctx) => providerAccountResourceSummary(item, ctx) },
      { key: "priority", label: "优先级" },
      { key: "healthy", label: "健康", render: (item) => <StatusPill status={item.healthy ? "healthy" : "down"} /> },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      { key: "name", label: "名称", required: true },
      { key: "type", label: "类型", type: "select", options: providerTypeOptions, required: true },
      { key: "base_url", label: "Base URL" },
      { key: "api_key", label: "API Key", type: "password", help: "编辑时留空表示不修改现有 Key；只有填写新值才会覆盖。" },
      { key: "priority", label: "优先级", type: "number", placeholder: "留空自动追加", help: "数字越小越先调用；新增时留空会自动排在该统一模型已有 Provider 后面。" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
      { key: "healthy", label: "健康", type: "boolean" },
    ],
    list: (ctx) => ctx.providers,
    create: (ctx, values) => adminMutate(ctx, "/api/admin/providers", "POST", providerPayload(values)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/providers/${item.id}`, "PATCH", providerUpdatePayload(values)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/providers/${item.id}`),
    actions: [
      {
        label: "配置路由",
        title: "为该 Provider 新增模型路由",
        modal: (item, ctx) => ({
          config: routeConfig(),
          initialValues: providerRouteDefaults(item, ctx),
        }),
      },
      {
        label: "测试",
        title: "检测 Provider 可用性",
        run: (ctx, item) => adminMutate(ctx, `/api/admin/providers/${item.id}/test`, "POST", {}),
        doneMessage: (item) => `${item.name} 检测完成`,
      },
    ],
    toForm: (item) => ({
      name: item.name,
      type: item.type,
      base_url: item.base_url ?? "",
      priority: String(item.priority ?? 10),
      status: item.status,
      healthy: String(item.healthy),
    }),
  };
}

function providerResourceFieldConfigs(provider?: Provider): FieldConfig[] {
  return [
    { key: "provider_id", label: "Provider", type: "select", optionsFromData: providerSelectOptions, required: true, readOnlyOnEdit: Boolean(provider) },
    { key: "name", label: "名称", required: true },
    { key: "resource_type", label: "账号类型", type: "select", options: ["openai_subscription", "api_key"], required: true },
    { key: "auth_type", label: "认证方式", type: "select", options: ["oauth", "personal_access_token", "api_key"], visible: openAIAccountFieldVisible },
    { key: "access_token", label: "访问 Token", type: "password", autoComplete: "new-password", visible: openAIAccountFieldVisible, help: "OpenAI subscription / Codex OAuth access token 或 PAT；保存后不会再次显示。" },
    { key: "refresh_token", label: "刷新 Token", type: "password", autoComplete: "new-password", visible: openAIAccountFieldVisible, help: "可选，保存到加密凭据中，用于后续自动刷新能力。" },
    { key: "id_token", label: "ID Token", type: "textarea", autoComplete: "off", visible: openAIAccountFieldVisible, help: "可选。填写后会自动提取账号邮箱、账号 ID、组织 ID 和计划类型。" },
    { key: "api_key", label: "API Key", type: "password", autoComplete: "new-password", visible: (values) => values.resource_type !== "openai_subscription", help: "普通资源实例的上游 API Key；编辑时留空表示不修改。" },
    { key: "account_email", label: "账号邮箱", autoComplete: "off", visible: openAIAccountFieldVisible },
    { key: "account_id", label: "账号 ID", autoComplete: "off", visible: openAIAccountFieldVisible },
    { key: "organization_id", label: "组织 ID", autoComplete: "off", visible: openAIAccountFieldVisible },
    { key: "plan_type", label: "计划类型", visible: openAIAccountFieldVisible },
    { key: "base_url", label: "Base URL", placeholder: "https://api.openai.com/v1" },
    { key: "group", label: "分组" },
    { key: "priority", label: "优先级", type: "number" },
    { key: "weight", label: "权重", type: "number" },
    { key: "rate_limit_rpm", label: "RPM 限制", type: "number" },
    { key: "token_limit_tpm", label: "TPM 限制", type: "number" },
    { key: "max_concurrency", label: "最大并发", type: "number" },
    { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
    { key: "healthy", label: "健康", type: "boolean" },
  ];
}

function providerResourceConfig(provider?: Provider): ResourceConfig<ProviderResource> {
  return {
    view: "providers",
    title: "账号集成",
    eyebrow: "Provider 账号资源",
    description: "把 OpenAI subscription、PAT 或普通 API Key 作为 Provider 资源实例加入账号池，并参与路由权重、并发和限流调度。",
    createLabel: "添加账号资源",
    columns: [
      { key: "name", label: "名称" },
      { key: "provider_id", label: "Provider", render: (item, ctx) => findProvider(ctx, item.provider_id)?.name || item.provider_id },
      { key: "resource_type", label: "账号类型", render: (item) => resourceTypeLabel(item.resource_type) },
      { key: "credential_summary", label: "账号邮箱", render: (item) => item.credential_summary?.account_email || item.credential_summary?.account_id || "-" },
      { key: "weight", label: "权重" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: providerResourceFieldConfigs(provider),
    list: (ctx) => ctx.providerResources.filter((item) => !provider || item.provider_id === provider.id),
    create: (ctx, values) => adminMutate(ctx, "/api/admin/provider-resources", "POST", providerResourcePayload(values)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/provider-resources/${item.id}`, "PATCH", providerResourceUpdatePayload(values)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/provider-resources/${item.id}`),
    actions: [
      {
        label: "刷新 Token",
        title: "使用保存的 refresh token 更新账号访问 Token",
        visible: (item) => item.resource_type === "openai_subscription" && item.credential_summary?.has_refresh_token === "true",
        run: (ctx, item) => adminMutate(ctx, `/api/admin/provider-resources/${item.id}/refresh-token`, "POST", {}),
        doneMessage: (item) => `${item.name} ${tx("Token 已刷新")}`,
      },
    ],
    toForm: providerResourceToForm,
  };
}

function openAIAccountFieldVisible(values: Record<string, string>) {
  return values.resource_type === "openai_subscription";
}

function providerCreateAccountResourceFields() {
  const hiddenKeys = new Set(["provider_id", "healthy"]);
  return providerResourceFieldConfigs()
    .filter((field) => !hiddenKeys.has(field.key))
    .map((field) => field.key === "name" ? { ...field, label: "账号资源名称" } : field);
}

function providerCreateAccountRuntimeFields() {
  const keys = new Set(["base_url", "group", "priority", "weight", "rate_limit_rpm", "token_limit_tpm", "max_concurrency", "status"]);
  return providerResourceFieldConfigs()
    .filter((field) => keys.has(field.key))
    .map((field) => field.key === "base_url" ? { ...field, required: true } : field);
}

function providerCreateAccountManualTokenFields() {
  const keys = new Set(["access_token", "refresh_token", "id_token", "account_id", "organization_id", "plan_type"]);
  return providerResourceFieldConfigs().filter((field) => keys.has(field.key));
}

function providerAccountTokenSummary(values: Record<string, string>) {
  const items: string[] = [];
  if (values.access_token?.trim()) items.push("已回填访问 Token");
  if (values.refresh_token?.trim()) items.push("已回填刷新 Token");
  if (values.id_token?.trim()) items.push("已回填 ID Token");
  return { ready: items.length > 0, items };
}

function defaultProviderResourceName(providerName?: string) {
  const normalized = providerName?.trim() || "Provider";
  return `${normalized} OpenAI Account`;
}

function providerResourceDraftDefaults(provider: { provider_id?: string; name?: string; base_url?: string }) {
  return {
    provider_id: provider.provider_id ?? "",
    name: defaultProviderResourceName(provider.name),
    resource_type: "openai_subscription",
    auth_type: "oauth",
    authorization_url: "",
    base_url: provider.base_url || "https://api.openai.com/v1",
    group: "default",
    priority: "1",
    weight: "100",
    rate_limit_rpm: "",
    token_limit_tpm: "",
    max_concurrency: "3",
    token_type: "",
    expires_at: "",
    scopes: "",
    status: "active",
    healthy: "true",
  };
}

function providerResourceDefaults(provider: Provider) {
  return providerResourceDraftDefaults({
    provider_id: provider.id,
    name: provider.name || provider.id,
    base_url: provider.base_url,
  });
}

function assertProviderAccountResourceReady(values: Record<string, string>) {
  if (values.resource_type === "openai_subscription") {
    if (values.access_token?.trim() || values.refresh_token?.trim() || values.id_token?.trim()) return;
    throw new Error(tx("请先完成账号授权回填，或在高级选项中手动粘贴 Token。"));
  }
  if (!values.api_key?.trim()) {
    throw new Error(tx("请填写账号资源的 API Key，或切换为稍后配置。"));
  }
}

function modelConfig(): ResourceConfig<Model> {
  return {
    view: "models",
    title: "模型目录",
    eyebrow: "对外模型列表",
    description: "维护内部应用调用的对外模型名，并查看每个模型可用的 Provider 线路。",
    createLabel: "新增模型",
    columns: [
      { key: "name", label: "对外模型", render: (item) => <ModelNameCell model={item} /> },
      { key: "category", label: "模型类型", render: (item) => modelCategoryLabel(modelCategory(item)) },
      { key: "capabilities", label: "能力", render: (item) => modelCapabilitySummary(item) },
      { key: "routes", label: "可用供应商", render: (item, ctx) => <ModelRouteProviders model={item} data={ctx} /> },
      { key: "route_count", label: "路由数", render: (item, ctx) => modelRoutesFor(item, ctx).length },
      { key: "price", label: "目录计价", render: (item) => modelPriceSummary(item) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      { key: "name", label: "模型名", required: true },
      { key: "category", label: "模型类型", type: "select", options: modelCategoryFormOptions(), required: true },
      { key: "family", label: "系列", required: true },
      { key: "modality", label: "能力", type: "select", options: ["chat", "embedding", "image", "video", "audio", "ocr", "rerank"], required: true },
      { key: "context_window", label: "上下文窗口", type: "number" },
      { key: "input_price_usd_per_1m", label: "输入价 USD/1M", type: "number" },
      { key: "output_price_usd_per_1m", label: "输出价 USD/1M", type: "number" },
      { key: "embedding_price_usd_per_1m", label: "Embedding 价 USD/1M", type: "number" },
      { key: "capabilities", label: "能力标签，逗号分隔" },
      { key: "supported_parameters", label: "支持参数，逗号分隔" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
    ],
    list: (ctx) => ctx.models,
    create: (ctx, values) => adminMutate(ctx, "/api/admin/models", "POST", modelPayload(values)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/models/${encodeURIComponent(item.name)}`, "PATCH", modelPayload(values)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/models/${encodeURIComponent(item.name)}`),
    actions: [
      {
        label: "配置路由",
        title: "为该对外模型新增 Provider 线路",
        modal: (item, ctx) => ({
          config: routeConfig(),
          initialValues: modelRouteDefaults(item, ctx),
        }),
      },
    ],
    toForm: (item) => modelToForm(item),
  };
}

function routeConfig(): ResourceConfig<ModelRoute> {
  return {
    view: "routes",
    title: "路由策略",
    eyebrow: "模型路由规则",
    description: "参考模型路由器思路，按平衡、质量优先或成本优先模式选择候选 Provider 线路，并在失败时自动回退。",
    createLabel: "新增路由",
    columns: [
      { key: "model_name", label: "统一模型" },
      { key: "provider_id", label: "Provider", render: (item, ctx) => findProvider(ctx, item.provider_id)?.name || item.provider_id },
      { key: "provider_model", label: "上游模型" },
      { key: "priority", label: "优先级" },
      { key: "weight", label: "权重" },
      { key: "score", label: "评分", render: (item) => routeScoreSummary(item) },
      { key: "strategy", label: "策略", render: (item) => routeStrategyLabel(item.strategy) },
      { key: "sticky_session", label: "粘性", render: (item) => item.sticky_session ? tx("开启") : tx("关闭开关") },
      { key: "last_used_at", label: "最近命中", render: (item) => formatTime(item.last_used_at ?? "") },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      {
        key: "model_name",
        label: "统一模型",
        type: "multi-select",
        optionsFromData: modelSelectOptions,
        required: true,
        help: "新增路由时可多选模型；编辑已有路由时仍按单条规则调整。",
      },
      { key: "provider_id", label: "Provider", type: "select", optionsFromData: providerSelectOptions, required: true },
      { key: "provider_model", label: "上游模型/部署名", placeholder: "留空则沿用统一模型名", help: "批量创建时留空，会为每个统一模型使用同名上游模型。" },
      { key: "priority", label: "优先级", type: "number" },
      { key: "weight", label: "权重", type: "number" },
      { key: "quality_score", label: "质量评分 1-100", type: "number", help: "质量优先模式会优先选择该评分更高的线路。" },
      { key: "cost_score", label: "成本评分 1-100", type: "number", help: "成本优先模式会优先选择该评分更高的线路，分数越高代表越省。" },
      { key: "strategy", label: "调度策略", type: "select", options: ["balanced", "quality", "cost", "priority_weighted", "priority_only"], required: true },
      { key: "sticky_session", label: "粘性会话", type: "boolean" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
    ],
    list: (ctx) => ctx.routes,
    create: (ctx, values) => createModelRoutes(ctx, values),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/routing-rules/${item.id}`, "PATCH", routePayload(values)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/routing-rules/${item.id}`),
    toForm: (item) => stringifyForm(item),
  };
}

function projectConfig(): ResourceConfig<Project> {
  return {
    view: "projects",
    title: "项目空间",
    eyebrow: "项目列表",
    description: "项目是企业内部 AI 使用、Key、额度和成本归属的基本单元。",
    createLabel: "新增项目",
    columns: [
      { key: "name", label: "项目" },
      { key: "team_id", label: "团队", render: (item, ctx) => teamLabel(ctx, item.team_id ?? "") },
      { key: "owner_user_id", label: "负责人", render: (item, ctx) => ownerUserLabel(ctx, item.owner_user_id ?? "") },
      { key: "cost_center", label: "成本中心", render: (item, ctx) => costCenterLabel(ctx, item.cost_center ?? "") },
      { key: "quota", label: "额度", render: (item, ctx) => projectQuotaSummary(ctx, item) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      { key: "name", label: "项目名称", required: true },
      { key: "team_id", label: "所属团队", type: "select", optionsFromData: teamSelectOptions, help: "管理员分配项目归属团队；团队 Leader 创建时会自动固定为自己的团队。" },
      { key: "owner_user_id", label: "项目负责人", type: "select", optionsFromData: userSelectOptions, help: "负责人默认拥有该项目的 Key 管理权限。" },
      { key: "cost_center", label: "成本中心", type: "select", optionsFromData: costCenterSelectOptions },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
    ],
    list: (ctx) => ctx.projects,
    create: (ctx, values) => adminMutate(ctx, "/api/admin/projects", "POST", values),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/projects/${item.id}`, "PATCH", values),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/projects/${item.id}`),
    actions: [
      {
        label: "发放 Key",
        title: "为该项目创建内部 API Key",
        modal: (item) => ({
          config: apiKeyConfig(),
          initialValues: {
            project_id: item.id,
            name: `${item.name} Key`,
          },
        }),
      },
    ],
    toForm: (item) => stringifyForm(item),
  };
}

function projectMemberConfig(): ResourceConfig<AdminResource> {
  const fields: FieldConfig[] = [
    {
      key: "project_id",
      label: "项目空间",
      type: "select",
      optionsFromData: projectMemberProjectSelectOptions,
      required: true,
      visible: () => false,
    },
    {
      key: "user_id",
      label: "用户",
      type: "select",
      optionsFromData: userSelectOptions,
      required: true,
      readOnlyOnEdit: true,
    },
    {
      key: "role",
      label: "项目角色",
      type: "select",
      optionsFromData: projectMemberRoleOptions,
      required: true,
      help: "owner/maintainer 可管理项目 Key；developer 可发放自己的 Key；viewer 只用于可见和统计。",
    },
    {
      key: "can_issue_keys",
      label: "允许发 Key",
      type: "boolean",
      help: "需要单独给 viewer 或特殊成员开放发 Key 时启用。",
    },
    { key: "status", label: "状态", type: "select", options: ["active", "disabled"], required: true },
  ];
  return {
    view: "project-members",
    title: "项目成员",
    eyebrow: "项目成员",
    description: "把用户分配到一个或多个项目空间，并控制项目内 Key 发放权限。",
    createLabel: "分配项目成员",
    columns: [
      { key: "user_id", label: "用户", render: (item, ctx) => ownerUserLabel(ctx, stringifyValue(item.fields?.user_id)) },
      { key: "role", label: "项目角色", render: (item) => projectMemberRoleLabel(stringifyValue(item.fields?.role)) },
      { key: "can_issue_keys", label: "发 Key", render: (item) => projectMemberCanIssueLabel(item) },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields,
    list: (ctx) => ctx.resources["project-members"] ?? [],
    create: (ctx, values, data) => adminMutate(ctx, "/api/admin/resources/project-members", "POST", projectMemberPayload(values, data)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/resources/project-members/${item.id}`, "PATCH", projectMemberPayload(values, undefined, item)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/resources/project-members/${item.id}`),
    toForm: (item) => ({
      project_id: stringifyValue(item.fields?.project_id),
      user_id: stringifyValue(item.fields?.user_id),
      role: stringifyValue(item.fields?.role) || "developer",
      can_issue_keys: stringifyValue(item.fields?.can_issue_keys || "false"),
      status: item.status,
    }),
  };
}

function projectMemberInitialValues(project: Project): Record<string, string> {
  return {
    project_id: project.id,
    role: "developer",
    can_issue_keys: "false",
    status: "active",
  };
}

function projectMemberPayload(values: Record<string, string>, data?: AppData, existing?: AdminResource) {
  const user = data?.users.find((item) => item.id === values.user_id);
  const displayName = user ? user.name || user.username || user.email : values.user_id;
  return {
    name: existing?.name || `${displayName || "项目成员"} 项目成员`,
    status: values.status || existing?.status || "active",
    fields: {
      project_id: values.project_id || stringifyValue(existing?.fields?.project_id),
      user_id: values.user_id || stringifyValue(existing?.fields?.user_id),
      role: values.role || stringifyValue(existing?.fields?.role) || "developer",
      can_issue_keys: truthyValue(values.can_issue_keys),
    },
  };
}

function apiKeyConfig(): ResourceConfig<APIKey> {
  return {
    view: "api-keys",
    title: "Key 管理",
    eyebrow: "内部 Key 列表",
    description: "按项目发放内部 API Key，限制模型白名单、额度、并发和有效期。",
    createLabel: "发放 Key",
    columns: [
      { key: "name", label: "名称" },
      { key: "project_id", label: "归属项目", render: (item, ctx) => projectName(ctx, item.project_id) },
      { key: "project_owner", label: "负责人", render: (item, ctx) => projectOwnerLabel(ctx, item.project_id) },
      { key: "project_team", label: "团队", render: (item, ctx) => projectTeamLabel(ctx, item.project_id) },
      { key: "key_prefix", label: "Key", render: (item) => `${item.key_prefix}...${item.key_suffix}` },
      { key: "allowed_models", label: "模型", render: (item) => (item.allowed_models ?? []).join(", ") || tx("全部") },
      { key: "ip_allowlist", label: "IP 白名单", render: (item) => (item.ip_allowlist ?? []).join(", ") || tx("不限") },
      { key: "limits.max_concurrency", label: "并发" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
    ],
    fields: [
      {
        key: "project_id",
        label: "归属项目",
        type: "select",
        required: true,
        optionsFromData: projectSelectOptions,
        help: "只显示当前账号拥有发 Key 权限的项目；一个人可以被分配到多个项目。",
        readOnlyOnEdit: true,
      },
      { key: "name", label: "Key 名称", required: true },
      { key: "group", label: "用途/环境", placeholder: "prod、dev、backend-service" },
      { key: "allowed_models", label: "模型白名单，逗号分隔", help: "留空表示不限制 Key 级模型白名单；实际可调用模型仍受模型目录和路由策略约束。" },
      { key: "ip_allowlist", label: "IP 白名单，逗号分隔", help: "留空表示不限来源 IP。" },
      { key: "daily_requests", label: "日请求", type: "number" },
      { key: "monthly_requests", label: "月请求", type: "number" },
      { key: "daily_tokens", label: "日 Token", type: "number" },
      { key: "monthly_tokens", label: "月 Token", type: "number" },
      { key: "daily_cost_usd", label: "日成本 USD", type: "number" },
      { key: "monthly_cost_usd", label: "月成本 USD", type: "number" },
      { key: "max_concurrency", label: "最大并发", type: "number" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled", "revoked"], required: true },
    ],
    list: (ctx) => ctx.keys,
    create: async () => undefined,
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/api-keys/${item.id}`, "PATCH", keyPatchPayload(values)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/api-keys/${item.id}`),
    actions: [
      {
        label: "轮换",
        title: "生成新 Key，并立即吊销旧 Key",
        run: async (ctx, item) => {
          const resp = await adminFetch(ctx, `/api/admin/api-keys/${item.id}/rotate`, {
            method: "POST",
            body: JSON.stringify({}),
          });
          if (!resp.ok) throw new Error(`rotate api key ${resp.status}`);
          const payload = (await resp.json()) as { api_key: string };
          window.dispatchEvent(new CustomEvent("tokenhub-issued-key", { detail: payload.api_key }));
        },
        doneMessage: (item) => `${item.name} 已轮换，新 Key 已展示`,
      },
    ],
    toForm: (item) => ({
      project_id: item.project_id,
      name: item.name,
      group: item.group ?? "default",
      allowed_models: (item.allowed_models ?? []).join(", "),
      ip_allowlist: (item.ip_allowlist ?? []).join(", "),
      daily_requests: String(item.limits?.daily_requests ?? ""),
      monthly_requests: String(item.limits?.monthly_requests ?? ""),
      daily_tokens: String(item.limits?.daily_tokens ?? ""),
      monthly_tokens: String(item.limits?.monthly_tokens ?? ""),
      daily_cost_usd: String(item.limits?.daily_cost_usd ?? ""),
      monthly_cost_usd: String(item.limits?.monthly_cost_usd ?? ""),
      max_concurrency: String(item.limits?.max_concurrency ?? ""),
      status: item.status,
    }),
  };
}

function APIKeyStatusSwitch({
  item,
  onToggle,
}: {
  item: APIKey;
  onToggle: (status: "active" | "disabled") => void;
}) {
  if (item.status !== "active" && item.status !== "disabled") {
    return <StatusPill status={item.status} />;
  }
  const enabled = item.status === "active";
  const nextStatus = enabled ? "disabled" : "active";
  return (
    <button
      aria-checked={enabled}
      className={enabled ? "status-switch active" : "status-switch"}
      onClick={(event) => {
        event.stopPropagation();
        onToggle(nextStatus);
      }}
      role="switch"
      title={enabled ? tx("点击停用 API Key") : tx("点击启用 API Key")}
      type="button"
    >
      <span className="status-switch-track">
        <span className="status-switch-thumb" />
      </span>
      <strong>{enabled ? tx("启用") : tx("停用")}</strong>
    </button>
  );
}

function apiKeyStatusAction(status: "active" | "disabled"): ResourceAction<APIKey> {
  return {
    label: status === "active" ? "启用" : "禁用",
    title: status === "active" ? "重新启用该 API Key" : "立即禁用该 API Key",
    run: (ctx, item) => updateAPIKeyStatus(ctx, item, status),
    doneMessage: (item) => tx(`${item.name} 已${status === "active" ? "启用" : "禁用"}`),
  };
}

function adminUserConfig(): ResourceConfig<AdminUser> {
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

function alertRuleConfig(): ResourceConfig<AdminResource> {
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

function alertEventConfig(): ResourceConfig<AlertEvent> {
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

function alertDeliveryConfig(): ResourceConfig<AlertDelivery> {
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

function approvalConfig(): ResourceConfig<ApprovalRequest> {
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

function costCenterConfig(): ResourceConfig<AdminResource> {
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

function chargebackConfig(): ResourceConfig<AdminResource> {
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

function approvalFlowConfig(): ResourceConfig<AdminResource> {
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

function reportConfig(): ResourceConfig<AdminResource> {
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

function invoiceConfig(): ResourceConfig<AdminResource> {
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

async function handleApprovalOrJSON(resp: Response) {
  if (resp.status === 202) {
    const data = (await resp.json()) as { approval_required?: boolean; approval?: ApprovalRequest };
    if (data.approval_required) {
      window.dispatchEvent(new CustomEvent("tokenhub-issued-key", { detail: `已提交审批：${data.approval?.id ?? ""}` }));
    }
    return;
  }
  await resp.json().catch(() => undefined);
}

function reportExportDefinitions(): Array<{
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

function reportExportActions(): ToolbarAction[] {
  return reportExportDefinitions().map(({ dataset, label }) => ({
    label,
    title: `导出 ${label} CSV`,
    run: async (ctx) => {
      await downloadReport(ctx, dataset);
    },
    doneMessage: () => `${label} 已导出`,
  }));
}

async function downloadReport(ctx: ApiContext, dataset: string) {
  const period = reportPeriodPrompt(dataset);
  if (period === null) return null;
  const resp = await adminFetch(ctx, reportExportPath(dataset, period));
  if (!resp.ok) throw new Error(`export ${dataset} ${resp.status}`);
  const blob = await resp.blob();
  const fileName = reportFilename(dataset, period);
  downloadBlob(blob, fileName);
  return { fileName, period };
}

function reportPeriodPrompt(dataset: string) {
  if (!["usage", "budgets"].includes(dataset)) return "";
  const period = window.prompt("输入账期 YYYY-MM，留空则导出全部", currentBillingPeriod());
  if (period === null) return null;
  return period.trim();
}

function reportExportPath(dataset: string, period?: string) {
  const query = period ? `?period=${encodeURIComponent(period)}` : "";
  return `/api/admin/export/${encodeURIComponent(dataset)}${query}`;
}

function reportFilename(dataset: string, period?: string) {
  return `tokenhub-${dataset}${period ? `-${period}` : ""}.csv`;
}

function currentBillingPeriod() {
  return new Date().toISOString().slice(0, 7);
}

async function downloadSQLiteBackup(ctx: ApiContext, item: SQLiteBackup) {
  const resp = await adminFetch(ctx, `/api/admin/sqlite/backups/${item.id}/download`);
  if (!resp.ok) throw new Error(`download sqlite backup ${resp.status}`);
  const blob = await resp.blob();
  downloadBlob(blob, item.file_name || `tokenhub-${item.id}.sqlite3`);
}

async function restoreSQLiteBackup(ctx: ApiContext, item: SQLiteBackup) {
  const confirmation = window.prompt(`输入 RESTORE ${item.id} 确认恢复该 SQLite 备份`);
  if (confirmation == null) return;
  const resp = await adminFetch(ctx, `/api/admin/sqlite/backups/${item.id}/restore`, {
    method: "POST",
    body: JSON.stringify({ confirmation }),
  });
  if (!resp.ok) throw new Error(`restore sqlite backup ${resp.status}`);
  await resp.json().catch(() => undefined);
}

function downloadBlob(blob: Blob, filename: string) {
  const url = window.URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  window.URL.revokeObjectURL(url);
}

function genericResourceConfig(kind: string, title: string, description: string, fields: FieldConfig[]): ResourceConfig<AdminResource> {
  return {
    view: kind as ViewKey,
    title,
    eyebrow: `${title}列表`,
    description,
    createLabel: `新增${title}`,
    columns: [
      { key: "name", label: "名称" },
      { key: "description", label: "说明" },
      { key: "status", label: "状态", render: (item) => <StatusPill status={item.status} /> },
      { key: "fields", label: "配置", render: (item) => fieldSummary(item.fields) },
      { key: "updated_at", label: "更新时间", render: (item) => formatTime(item.updated_at ?? "") },
    ],
    fields: [
      { key: "name", label: "名称", required: true },
      { key: "description", label: "说明", type: "textarea" },
      { key: "status", label: "状态", type: "select", options: ["active", "disabled", "draft", "archived"], required: true },
      ...fields,
    ],
    list: (ctx) => ctx.resources[kind] ?? [],
    create: (ctx, values) => adminMutate(ctx, `/api/admin/resources/${kind}`, "POST", resourcePayload(values, fields)),
    update: (ctx, item, values) => adminMutate(ctx, `/api/admin/resources/${kind}/${item.id}`, "PATCH", resourcePayload(values, fields)),
    remove: (ctx, item) => adminDelete(ctx, `/api/admin/resources/${kind}/${item.id}`),
    toForm: (item) => {
      const form: Record<string, string> = {
        name: item.name,
        description: item.description ?? "",
        status: item.status,
      };
      for (const field of fields) {
        form[field.key] = stringifyValue(item.fields?.[field.key]);
      }
      return form;
    },
  };
}

async function runApprovalAction(ctx: ApiContext, item: ApprovalRequest, action: "approve" | "reject") {
  if (item.status !== "pending") return;
  const resp = await adminFetch(ctx, `/api/admin/approvals/${item.id}/${action}`, {
    method: "POST",
    body: JSON.stringify({}),
  });
  if (!resp.ok) throw new Error(`${action} approval ${resp.status}`);
  const payload = (await resp.json()) as { result?: { api_key?: string } };
  if (payload.result?.api_key) {
    window.dispatchEvent(new CustomEvent("tokenhub-issued-key", { detail: payload.result.api_key }));
  }
}

async function createKeyWithCapture(
  ctx: ApiContext,
  values: Record<string, string>,
  setIssuedKey: (value: string) => void,
  setNotice: (value: string) => void,
  load: () => Promise<void>,
  setLoading: (value: boolean) => void,
  setError: (value: string) => void,
  closeForm: () => void,
) {
  setLoading(true);
  setError("");
  setNotice("");
  try {
    if (!values.project_id) {
      throw new Error("请选择项目空间后再发放 API Key");
    }
    const payload = keyCreatePayload(values);
    const resp = await adminFetch(ctx, `/api/admin/projects/${values.project_id}/keys`, {
      method: "POST",
      body: JSON.stringify(payload),
    });
    if (!resp.ok) throw new Error(await readAdminError(resp, "项目 Key 发放"));
    const data = (await resp.json()) as { api_key?: string; approval_required?: boolean; approval?: ApprovalRequest };
    if (data.approval_required) {
      setIssuedKey("");
      setNotice(`已提交审批：${data.approval?.id ?? ""}`);
    } else if (data.api_key) {
      setNotice("");
      setIssuedKey(data.api_key);
    }
    closeForm();
    await load();
  } catch (err) {
    if (isAuthExpiredError(err)) return;
    setError(err instanceof Error ? err.message : tx("发放 Key 失败"));
  } finally {
    setLoading(false);
  }
}

function providerPayload(values: Record<string, string>) {
  return {
    id: values.id,
    name: values.name,
    type: values.type,
    base_url: values.base_url,
    api_key: values.api_key,
    status: values.status || "active",
    healthy: values.healthy !== "false",
    priority: numberOr(values.priority, 10),
    catalog_id: values.catalog_id,
    model_category: values.model_category,
    create_routes: values.create_routes === "true",
    selected_models: splitList(values.selected_models),
  };
}

function providerUpdatePayload(values: Record<string, string>) {
  const payload = providerPayload(values) as Record<string, unknown>;
  if (!values.api_key?.trim()) {
    delete payload.api_key;
  }
  return payload;
}

function providerResourcePayload(values: Record<string, string>) {
  const isOpenAIAccount = values.resource_type === "openai_subscription";
  const credentials = isOpenAIAccount
    ? {
        auth_type: values.auth_type || "oauth",
        access_token: values.access_token,
        refresh_token: values.refresh_token,
        id_token: values.id_token,
        email: values.account_email,
        account_id: values.account_id,
        organization_id: values.organization_id,
        plan_type: values.plan_type,
        token_type: values.token_type,
        expires_at: values.expires_at,
        scopes: values.scopes,
      }
    : undefined;
  return {
    provider_id: values.provider_id,
    name: values.name,
    resource_type: values.resource_type || "api_key",
    base_url: values.base_url,
    api_key: isOpenAIAccount ? values.access_token : values.api_key,
    group: values.group || "default",
    status: values.status || "active",
    healthy: values.healthy !== "false",
    priority: numberOr(values.priority, 1),
    weight: numberOr(values.weight, 100),
    rate_limit_rpm: numberOr(values.rate_limit_rpm, 0),
    token_limit_tpm: numberOr(values.token_limit_tpm, 0),
    max_concurrency: numberOr(values.max_concurrency, 0),
    credentials,
    options: providerResourceOptions(values),
  };
}

function providerResourceUpdatePayload(values: Record<string, string>) {
  const payload = providerResourcePayload(values) as Record<string, unknown>;
  const isOpenAIAccount = values.resource_type === "openai_subscription";
  if (isOpenAIAccount && !values.access_token?.trim()) delete payload.api_key;
  if (!isOpenAIAccount && !values.api_key?.trim()) delete payload.api_key;
  if (isOpenAIAccount && !values.access_token?.trim() && !values.refresh_token?.trim() && !values.id_token?.trim()) {
    delete payload.credentials;
  }
  return payload;
}

function providerResourceOptions(values: Record<string, string>) {
  if (values.resource_type !== "openai_subscription") return {};
  return {
    credential_source: "openai_subscription",
    auth_type: values.auth_type || "oauth",
    account_email: values.account_email,
    account_id: values.account_id,
    organization_id: values.organization_id,
    plan_type: values.plan_type,
    token_expires_at: values.expires_at,
    scopes: values.scopes,
  };
}

function providerResourceToForm(item: ProviderResource) {
  const summary = item.credential_summary ?? {};
  return {
    provider_id: item.provider_id,
    name: item.name,
    resource_type: item.resource_type,
    auth_type: summary.auth_type || item.options?.auth_type || "oauth",
    access_token: "",
    refresh_token: "",
    id_token: "",
    api_key: "",
    account_email: summary.account_email || "",
    account_id: summary.account_id || "",
    organization_id: summary.organization_id || "",
    plan_type: summary.plan_type || "",
    token_type: summary.token_type || "",
    expires_at: summary.token_expires_at || item.options?.token_expires_at || "",
    scopes: summary.scopes || item.options?.scopes || "",
    base_url: item.base_url ?? "",
    group: item.group ?? "default",
    priority: String(item.priority ?? 1),
    weight: String(item.weight ?? 100),
    rate_limit_rpm: String(item.rate_limit_rpm ?? ""),
    token_limit_tpm: String(item.token_limit_tpm ?? ""),
    max_concurrency: String(item.max_concurrency ?? ""),
    status: item.status,
    healthy: String(item.healthy),
  };
}

function modelPayload(values: Record<string, string>) {
  const payload = numberPayload(values, ["context_window", "input_price_usd_per_1m", "output_price_usd_per_1m", "embedding_price_usd_per_1m"]);
  payload.category = values.category || inferModelCategoryText(values.name || values.family || "");
  payload.capabilities = splitList(values.capabilities);
  payload.supported_parameters = splitList(values.supported_parameters);
  payload.input_modalities = splitList(values.input_modalities);
  payload.output_modalities = splitList(values.output_modalities);
  return payload;
}

function routePayload(values: Record<string, string>) {
  const payload: Record<string, unknown> = {
    model_name: values.model_name,
    provider_id: values.provider_id,
    provider_resource_id: values.provider_resource_id,
    resource_group: values.resource_group,
    provider_model: values.provider_model,
    status: values.status,
    strategy: values.strategy,
    sticky_session: values.sticky_session === "true",
    priority: numberOr(values.priority, 0),
    weight: numberOr(values.weight, 0),
    quality_score: numberOr(values.quality_score, 0),
    cost_score: numberOr(values.cost_score, 0),
  };
  return payload;
}

async function createModelRoutes(ctx: ApiContext, values: Record<string, string>) {
  const modelNames = splitList(values.model_name);
  if (modelNames.length === 0) {
    throw new Error("请选择至少一个统一模型");
  }
  for (const modelName of modelNames) {
    const routeValues = {
      ...values,
      model_name: modelName,
      provider_model: values.provider_model?.trim() || modelName,
    };
    await adminMutate(ctx, "/api/admin/routing-rules", "POST", routePayload(routeValues));
  }
}

function projectQuotaPolicy(data: AppData, project: Project) {
  const policies = data.resources["quota-policies"] ?? [];
  if (project.default_quota_ref) {
    const byRef = policies.find((item) => item.id === project.default_quota_ref);
    if (byRef) return byRef;
  }
  return policies.find((item) => {
    const scope = stringifyValue(item.fields?.scope || item.fields?.scope_type).toLowerCase();
    const scopeID = stringifyValue(item.fields?.scope_id);
    return scope === "project" && scopeID === project.id;
  });
}

function projectQuotaValues(quota?: AdminResource): ProjectQuotaValues {
  return {
    status: quota?.status || "active",
    daily_requests: quotaFieldValue(quota, "daily_requests"),
    monthly_requests: quotaFieldValue(quota, "monthly_requests"),
    daily_tokens: quotaFieldValue(quota, "daily_tokens"),
    monthly_tokens: quotaFieldValue(quota, "monthly_tokens"),
    daily_cost_usd: quotaFieldValue(quota, "daily_cost_usd"),
    monthly_cost_usd: quotaFieldValue(quota, "monthly_cost_usd"),
    max_concurrency: quotaFieldValue(quota, "max_concurrency"),
  };
}

function quotaFieldValue(quota: AdminResource | undefined, key: string) {
  const value = quota?.fields?.[key];
  if (value === undefined || value === null || value === "" || Number(value) === 0) return "";
  return String(value);
}

function projectQuotaPayload(project: Project, values: ProjectQuotaValues) {
  return {
    name: `${project.name || project.id} 项目额度`,
    description: "项目空间内配置的专属模型调用额度",
    status: values.status || "active",
    fields: {
      scope: "project",
      scope_id: project.id,
      daily_requests: numberOr(values.daily_requests, 0),
      monthly_requests: numberOr(values.monthly_requests, 0),
      daily_tokens: numberOr(values.daily_tokens, 0),
      monthly_tokens: numberOr(values.monthly_tokens, 0),
      daily_cost_usd: numberOr(values.daily_cost_usd, 0),
      monthly_cost_usd: numberOr(values.monthly_cost_usd, 0),
      max_concurrency: numberOr(values.max_concurrency, 0),
    },
  };
}

async function saveProjectQuota(ctx: ApiContext, project: Project, quota: AdminResource | undefined, values: ProjectQuotaValues) {
  const payload = projectQuotaPayload(project, values);
  const path = quota ? `/api/admin/resources/quota-policies/${quota.id}` : "/api/admin/resources/quota-policies";
  const resp = await adminFetch(ctx, path, {
    method: quota ? "PATCH" : "POST",
    body: JSON.stringify(payload),
  });
  if (resp.status === 202) {
    await handleApprovalOrJSON(resp);
    return;
  }
  if (!resp.ok) throw new Error(`save project quota ${resp.status}`);
  const saved = (await resp.json()) as AdminResource;
  if (saved.id && project.default_quota_ref !== saved.id) {
    const projectResp = await adminFetch(ctx, `/api/admin/projects/${project.id}`, {
      method: "PATCH",
      body: JSON.stringify({
        name: project.name,
        team_id: project.team_id ?? "",
        owner_user_id: project.owner_user_id ?? "",
        cost_center: project.cost_center ?? "",
        status: project.status,
        default_quota_ref: saved.id,
      }),
    });
    if (!projectResp.ok) throw new Error(`link project quota ${projectResp.status}`);
    await projectResp.json().catch(() => undefined);
  }
}

async function requestProjectQuotaIncrease(ctx: ApiContext, project: Project, quota: AdminResource | undefined, values: ProjectQuotaValues) {
  if (!projectQuotaValuesHaveLimit(values)) {
    throw new Error("请先填写至少一项希望提升后的目标额度");
  }
  const payload = projectQuotaPayload(project, values);
  const resp = await adminFetch(ctx, `/api/admin/projects/${project.id}/quota-increase`, {
    method: "POST",
    body: JSON.stringify({
      ...payload,
      resource_id: quota?.id ?? "",
    }),
  });
  if (resp.status === 202) {
    await handleApprovalOrJSON(resp);
    return;
  }
  if (!resp.ok) throw new Error(`request project quota increase ${resp.status}`);
  await resp.json().catch(() => undefined);
}

function projectQuotaValuesHaveLimit(values: ProjectQuotaValues) {
  return projectQuotaFields.some((field) => numberOr(values[field.key], 0) > 0);
}

function projectQuotaSummary(data: AppData, project: Project) {
  const quota = projectQuotaPolicy(data, project);
  if (!quota) return "未配置";
  if (quota.status !== "active") return enumValueLabel(quota.status);
  const parts = [
    quotaSummaryPart(quota, "daily_requests", "日请求"),
    quotaSummaryPart(quota, "monthly_requests", "月请求"),
    quotaSummaryPart(quota, "daily_tokens", "日 Token"),
    quotaSummaryPart(quota, "monthly_tokens", "月 Token"),
    quotaSummaryPart(quota, "daily_cost_usd", "日成本", "$"),
    quotaSummaryPart(quota, "monthly_cost_usd", "月成本", "$"),
    quotaSummaryPart(quota, "max_concurrency", "并发"),
  ].filter(Boolean);
  return parts.slice(0, 2).join(" · ") || "不限额";
}

function quotaSummaryPart(quota: AdminResource, key: string, label: string, prefix = "") {
  const value = numberFromUnknown(quota.fields?.[key]);
  if (!value) return "";
  return `${label} ${prefix}${compactNumber(value)}`;
}

function projectQuotaIssue(data: AppData, project: Project) {
  const quotaLogs = data.logs.filter(
    (log) => log.project_id === project.id && (log.error_code === "quota_exceeded" || log.status_code === 429),
  );
  const quotaAlerts = data.alerts.filter((alert) => {
    const code = String(alert.code || "").toLowerCase();
    if (!code.includes("quota")) return false;
    if (alert.scope_type === "project" && alert.scope_id === project.id) return true;
    return alert.scope_id === project.id;
  });
  const count = quotaLogs.length + quotaAlerts.length;
  if (count === 0) return null;
  const latest = [...quotaLogs.map((log) => log.created_at), ...quotaAlerts.map((alert) => alert.created_at)]
    .filter(Boolean)
    .sort()
    .at(-1);
  return { count, latest };
}

function pendingProjectQuotaApproval(data: AppData, project: Project) {
  return data.approvals.find((approval) => {
    if (approval.status !== "pending" || approval.trigger !== "quota_increase" || approval.resource_type !== "quota-policies") {
      return false;
    }
    const payload = parseApprovalPayload(approval.payload);
    if (stringifyValue(payload.project_id) === project.id) return true;
    const fields = payload.fields && typeof payload.fields === "object" ? payload.fields as Record<string, unknown> : {};
    return stringifyValue(fields.scope).toLowerCase() === "project" && stringifyValue(fields.scope_id) === project.id;
  });
}

function parseApprovalPayload(payload?: string): Record<string, unknown> {
  if (!payload) return {};
  try {
    const data = JSON.parse(payload);
    return data && typeof data === "object" && !Array.isArray(data) ? data as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

function defaultFormValues<T>(config: ResourceConfig<T>, data: AppData, currentUser?: AdminUser | null) {
  const values: Record<string, string> = {};
  for (const field of config.fields) {
    if (field.key === "status") values[field.key] = "active";
    if (field.key === "healthy") values[field.key] = "true";
    if (field.key === "priority") values[field.key] = config.view === "routes" ? "" : "10";
    if (field.key === "weight") values[field.key] = "100";
    if (field.key === "quality_score") values[field.key] = "50";
    if (field.key === "cost_score") values[field.key] = "50";
    if (field.key === "strategy") values[field.key] = "balanced";
    if (field.key === "provider_id") values[field.key] = firstActiveProvider(data)?.id ?? "";
    if (field.key === "model_name") values[field.key] = firstActiveModel(data)?.name ?? "";
    if (field.key === "group") values[field.key] = "default";
    if (field.key === "resource_type") values[field.key] = "api_key";
    if (field.key === "environment") values[field.key] = "prod";
    if (field.key === "project_id") values[field.key] = config.view === "api-keys" ? firstIssueableProject(data, currentUser) : (firstActiveProject(data)?.id ?? "");
    if (field.key === "team_id") values[field.key] = firstActiveTeam(data)?.id ?? "";
    if (field.key === "allowed_models") values[field.key] = "";
    if (field.key === "daily_requests") values[field.key] = "1000";
    if (field.key === "monthly_requests") values[field.key] = "30000";
    if (field.key === "daily_tokens") values[field.key] = "1000000";
    if (field.key === "monthly_tokens") values[field.key] = "20000000";
    if (field.key === "daily_cost_usd") values[field.key] = "100";
    if (field.key === "monthly_cost_usd") values[field.key] = "2000";
    if (field.key === "max_concurrency") values[field.key] = "20";
    if (field.key === "modality") values[field.key] = "chat";
    if (field.key === "type") values[field.key] = "openai_compatible";
    if (field.key === "auth_type") values[field.key] = "api_key";
    if (field.key === "scope") values[field.key] = "project";
    if (field.key === "period") values[field.key] = "monthly";
    if (field.key === "enforcement") values[field.key] = "block";
    if (field.key === "dataset") values[field.key] = "requests";
    if (field.key === "schedule") values[field.key] = "manual";
    if (field.key === "trigger") values[field.key] = "quota_increase";
    if (field.key === "approver_role") values[field.key] = "admin";
    if (field.key === "webhook_url") values[field.key] = "http://localhost:8081/tokenhub-alert";
    if (field.key === "protocol") values[field.key] = "direct";
    if (field.key === "notify_mode") values[field.key] = "silent";
    if (field.key === "role") values[field.key] = config.view === "project-members" ? "developer" : "user";
    if (field.key === "user_id") values[field.key] = firstActiveUser(data)?.id ?? "";
    if (field.key === "can_issue_keys") values[field.key] = "false";
    if (field.key === "owner") values[field.key] = firstActiveUser(data)?.id ?? "";
    if (field.key === "cost_center") values[field.key] = firstCostCenterCode(data);
    if (field.key === "role_key") values[field.key] = "user";
    if (field.key === "display_name") values[field.key] = "普通用户";
    if (field.key === "data_scope") values[field.key] = "self";
    if (field.key === "permissions") values[field.key] = "overview:read, project:read";
    if (field.key === "menu_scopes") values[field.key] = "overview, projects";
    if (field.key === "assignable") values[field.key] = "true";
    if (field.key === "provider_template") values[field.key] = "generic_oidc";
    if (field.key === "provider_type") values[field.key] = "oidc";
    if (field.key === "icon_key") values[field.key] = "auto";
    if (field.key === "login_label") values[field.key] = "";
    if (field.key === "issuer_url") values[field.key] = "https://sso.example.com";
    if (field.key === "client_id") values[field.key] = "tokenhub-admin";
    if (field.key === "redirect_uri") values[field.key] = "http://localhost:8080/api/admin/auth/oauth/callback";
    if (field.key === "scopes") values[field.key] = "openid, profile, email";
    if (field.key === "username_claim") values[field.key] = "preferred_username";
    if (field.key === "email_claim") values[field.key] = "email";
    if (field.key === "team_claim") values[field.key] = "department";
    if (field.key === "default_role") values[field.key] = "user";
    if (field.key === "default_team_id") values[field.key] = firstActiveTeam(data)?.id ?? "";
    if (field.key === "default_project_id") values[field.key] = projectMemberProjectSelectOptions(data)[0]?.value ?? "";
    if (field.key === "default_project_role") values[field.key] = "developer";
    if (field.key === "api_key_prefix") values[field.key] = "sk_";
    if (field.key === "api_key_random_length") values[field.key] = "48";
    if (field.key === "password") values[field.key] = "changeme123456";
    if (field.key === "expire_days") values[field.key] = "14";
  }
  return values;
}

function keyCreatePayload(values: Record<string, string>) {
  return {
    name: values.name,
    group: values.group || "default",
    allowed_models: splitList(values.allowed_models),
    ip_allowlist: splitList(values.ip_allowlist),
    limits: keyLimits(values),
  };
}

function keyPatchPayload(values: Record<string, string>) {
  return {
    name: values.name,
    group: values.group || "default",
    status: values.status || "active",
    allowed_models: splitList(values.allowed_models),
    ip_allowlist: splitList(values.ip_allowlist),
    limits: keyLimits(values),
  };
}

function notificationChannelPayload(values: Record<string, string>, existing?: AdminResource) {
  const type = normalizeNotificationChannelType(values.type);
  const secret = values.secret || stringifyValue(existing?.fields?.secret);
  const smtpPassword = values.smtp_password || stringifyValue(existing?.fields?.smtp_password);
  const telegramBotToken = values.telegram_bot_token || stringifyValue(existing?.fields?.telegram_bot_token || existing?.fields?.bot_token || existing?.fields?.secret);
  const whatsappAccessToken = values.access_token || stringifyValue(existing?.fields?.access_token || existing?.fields?.whatsapp_access_token || existing?.fields?.secret);
  let fields: Record<string, unknown>;
  if (type === "email") {
    fields = {
      type,
      smtp_host: values.smtp_host,
      smtp_port: numberOr(values.smtp_port, 587),
      smtp_username: values.smtp_username,
      smtp_password: smtpPassword,
      smtp_from: values.smtp_from,
      email_to: values.email_to,
    };
  } else if (type === "telegram") {
    fields = {
      type,
      telegram_bot_token: telegramBotToken,
      telegram_chat_id: values.telegram_chat_id,
      telegram_thread_id: values.telegram_thread_id,
    };
  } else if (type === "whatsapp") {
    fields = {
      type,
      whatsapp_phone_number_id: values.whatsapp_phone_number_id,
      whatsapp_to: values.whatsapp_to,
      access_token: whatsappAccessToken,
      whatsapp_api_version: values.whatsapp_api_version || "v20.0",
    };
  } else {
    fields = {
      type,
      webhook_url: values.webhook_url,
      secret,
    };
  }
  return {
    name: values.name || `${notificationChannelLabel(type)} 通知渠道`,
    description: values.description || notificationChannelDescription(type),
    status: values.status || "active",
    fields,
  };
}

function notificationChannelDefaults(type: string) {
  const normalized = normalizeNotificationChannelType(type);
  return {
    name: `${notificationChannelLabel(normalized)} 通知渠道`,
    description: notificationChannelDescription(normalized),
    status: "active",
    type: normalized,
    webhook_url: notificationChannelURLPlaceholder(normalized),
    secret: "",
    telegram_bot_token: "",
    telegram_chat_id: "",
    telegram_thread_id: "",
    whatsapp_phone_number_id: "",
    whatsapp_to: "",
    access_token: "",
    whatsapp_api_version: "v20.0",
    smtp_host: "smtp.example.com",
    smtp_port: "587",
    smtp_username: "tokenhub@example.com",
    smtp_password: "",
    smtp_from: "tokenhub@example.com",
    email_to: "ops@example.com",
  };
}

async function updateAPIKeyStatus(ctx: ApiContext, item: APIKey, status: "active" | "disabled") {
  const resp = await adminFetch(ctx, `/api/admin/api-keys/${item.id}`, {
    method: "PATCH",
    body: JSON.stringify({ status }),
  });
  if (!resp.ok) throw new Error(`update api key ${resp.status}`);
  await resp.json().catch(() => undefined);
}

function keyLimits(values: Record<string, string>) {
  return {
    daily_requests: numberOr(values.daily_requests, 0),
    monthly_requests: numberOr(values.monthly_requests, 0),
    daily_tokens: numberOr(values.daily_tokens, 0),
    monthly_tokens: numberOr(values.monthly_tokens, 0),
    daily_cost_usd: numberOr(values.daily_cost_usd, 0),
    monthly_cost_usd: numberOr(values.monthly_cost_usd, 0),
    max_concurrency: numberOr(values.max_concurrency, 0),
  };
}

function userPayload(values: Record<string, string>, includePassword: boolean) {
  const payload: Record<string, unknown> = {
    username: values.username,
    name: values.name,
    email: values.email,
    role: values.role || "user",
    team_id: values.team_id,
    status: values.status || "active",
  };
  if (includePassword || values.password) {
    payload.password = values.password;
  }
  return payload;
}

function numberPayload(values: Record<string, string>, keys: string[]) {
  const payload: Record<string, unknown> = { ...values };
  for (const key of keys) payload[key] = numberOr(values[key], 0);
  return payload;
}

function resourcePayload(values: Record<string, string>, customFields: FieldConfig[]) {
  const fields: Record<string, unknown> = {};
  for (const field of customFields) {
    fields[field.key] = field.type === "number" ? numberOr(values[field.key], 0) : parseLooseValue(values[field.key]);
  }
  return {
    name: values.name,
    description: values.description,
    status: values.status || "active",
    fields,
  };
}

function identityProviderPayload(values: Record<string, string>, fields: FieldConfig[], existing?: AdminResource) {
  const payload = resourcePayload(values, fields);
  if (existing && !values.client_secret) {
    payload.fields.client_secret = stringifyValue(existing.fields?.client_secret);
  }
  return payload;
}

async function importUsersFromCSVContent(ctx: ApiContext, content: string): Promise<UserImportResult> {
  const resp = await adminFetch(ctx, "/api/admin/users/import", {
    method: "POST",
    body: JSON.stringify({
      source: "manual_csv",
      format: "csv",
      content,
    }),
  });
  if (!resp.ok) {
    const message = await readAdminError(resp, "用户导入失败");
    throw new Error(message);
  }
  return await resp.json() as UserImportResult;
}

async function readAdminError(resp: Response, fallback: string) {
  if (resp.status === 403) {
    return permissionDeniedMessage(fallback);
  }
  const body = await resp.text().catch(() => "");
  if (!body) return `${fallback} (${resp.status})`;
  try {
    const parsed = JSON.parse(body) as { error?: { message?: string }; message?: string };
    return parsed.error?.message || parsed.message || `${fallback} (${resp.status})`;
  } catch {
    return body.length > 180 ? `${body.slice(0, 180)}...` : body;
  }
}

async function adminMutate(ctx: ApiContext, path: string, method: "POST" | "PATCH", payload: unknown) {
  const resp = await adminFetch(ctx, path, {
    method,
    body: JSON.stringify(payload),
  });
  if (!resp.ok) throw new Error(await readAdminError(resp, operationLabel(method, path)));
  if (resp.status === 202) {
    const data = (await resp.json()) as { approval_required?: boolean; approval?: ApprovalRequest };
    if (data.approval_required) {
      window.dispatchEvent(new CustomEvent("tokenhub-issued-key", { detail: `已提交审批：${data.approval?.id ?? ""}` }));
    }
  }
}

async function adminDelete(ctx: ApiContext, path: string) {
  const resp = await adminFetch(ctx, path, { method: "DELETE" });
  if (!resp.ok && resp.status !== 204) throw new Error(await readAdminError(resp, operationLabel("DELETE", path)));
}

async function readLoadError(resp: Response, name: string) {
  if (resp.status === 403) return permissionDeniedMessage(loadRequestLabel(name));
  return readAdminError(resp, loadRequestLabel(name));
}

function permissionDeniedMessage(target: string) {
  const label = target || tx("该资源");
  if (activeLanguage === "en") {
    return `This account does not have permission to access ${label}. Data outside your permission scope is hidden; ask an admin to adjust your role or project membership if needed.`;
  }
  if (activeLanguage === "ja") {
    return `このアカウントには ${label} へのアクセス権限がありません。権限外のデータは非表示です。必要に応じて管理者にロールまたはプロジェクトメンバー権限の調整を依頼してください。`;
  }
  return `当前账号没有访问 ${label} 的权限。页面已隐藏无权限数据；如需查看或管理，请联系管理员调整角色或项目成员权限。`;
}

function permissionPartialLoadMessage(labels: string[]) {
  const unique = Array.from(new Set(labels.filter(Boolean))).slice(0, 4);
  const summary = unique.join("、");
  if (activeLanguage === "en") return `Hidden due to insufficient permission: ${summary}. This page only shows content you can access.`;
  if (activeLanguage === "ja") return `権限不足のため非表示: ${summary}。このページにはアクセス可能な内容のみ表示します。`;
  return `已隐藏无权限数据：${summary}。当前页面只展示你有权限查看的内容。`;
}

function operationLabel(method: string, path: string) {
  const resource = resourceLabelFromPath(path);
  const action = method === "POST" ? "新增" : method === "PATCH" ? "编辑" : method === "DELETE" ? "删除" : "操作";
  return `${tx(resource)}${tx(action)}`;
}

function loadRequestLabel(name: string) {
  if (name.startsWith("resource:")) return resourceKindLabel(name.slice("resource:".length));
  const labels: Record<string, string> = {
    overview: "总览数据",
    providers: "Provider 渠道",
    "provider-resources": "Provider 账号资源",
    "api-keys": "Key 管理",
    routes: "路由策略",
    audit: "请求日志",
    "audit-events": "后台审计",
    alerts: "告警事件",
    "alert-deliveries": "通知记录",
    approvals: "审批记录",
    "sqlite-backups": "数据备份",
    breakdown: "用量统计",
    timeseries: "趋势统计",
    users: "用户管理",
    "provider-catalog": "Provider 模型目录",
  };
  return tx(labels[name] ?? name);
}

function resourceLabelFromPath(path: string) {
  if (path.includes("/api/admin/projects/") && path.endsWith("/keys")) return "项目 Key 发放";
  if (path.includes("/api/admin/api-keys")) return "Key 管理";
  if (path.includes("/api/admin/routing-rules")) return "路由策略";
  if (path.includes("/api/admin/providers")) return "Provider 渠道";
  if (path.includes("/api/admin/users")) return "用户管理";
  if (path.includes("/api/admin/approvals")) return "审批记录";
  if (path.includes("/api/admin/sqlite/backups")) return "数据备份";
  const resourceMatch = path.match(/\/api\/admin\/resources\/([^/]+)/);
  if (resourceMatch) return resourceKindLabel(resourceMatch[1]);
  return "该资源";
}

function resourceKindLabel(kind: string) {
  const labels: Record<string, string> = {
    teams: "团队分组",
    "cost-centers": "成本中心",
    "quota-policies": "额度策略",
    "project-members": "项目成员",
    settings: "系统设置",
    "role-configs": "角色配置",
    "identity-providers": "身份源",
    "alert-rules": "告警规则",
    budgets: "预算",
    chargebacks: "成本分摊",
    "approval-flows": "审批流",
    invoices: "成本账单",
    reports: "导出报表",
    "notification-channels": "通知渠道",
    monitors: "健康检测",
    proxies: "代理出口",
    announcements: "公告通知",
    "security-policies": "安全策略",
  };
  return tx(labels[kind] ?? kind);
}

class AuthExpiredError extends Error {
  constructor() {
    super("auth_expired");
    this.name = "AuthExpiredError";
  }
}

function isAuthExpiredError(error: unknown) {
  return error instanceof AuthExpiredError;
}

async function adminFetch(ctx: ApiContext, path: string, init: RequestInit = {}) {
  const headers = new Headers(init.headers);
  headers.set("authorization", `Bearer ${ctx.adminToken}`);
  if (init.body && !headers.has("content-type")) {
    headers.set("content-type", "application/json");
  }
  const resp = await fetch(`${ctx.baseURL.replace(/\/$/, "")}${path}`, { ...init, headers });
  if (resp.status === 401) {
    clearSavedSession();
    window.dispatchEvent(new CustomEvent(authExpiredEventName));
    throw new AuthExpiredError();
  }
  return resp;
}

function emptyData(): AppData {
  return {
    summary: emptySummary(),
    projects: [],
    keys: [],
    providers: [],
    providerResources: [],
    models: [],
    routes: [],
    logs: [],
    auditEvents: [],
    alerts: [],
    alertDeliveries: [],
    approvals: [],
    sqliteBackups: [],
    users: [],
    breakdown: { projects: [], models: [], members: [], providers: [], provider_resources: [], cost_centers: [] },
    timeseries: [],
    resources: {},
    providerCatalog: [],
  };
}

function emptySummary(): Summary {
  return {
    request_count: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    estimated_cost_usd: 0,
    errors: 0,
    api_key_count: 0,
    route_count: 0,
    active_route_count: 0,
    user_count: 0,
  };
}

function filterRows<T>(items: T[], query: string) {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return items;
  return items.filter((item) => JSON.stringify(item).toLowerCase().includes(normalized));
}

function catalogModelCategoryOptions(catalog: ProviderCatalogEntry[]) {
  const counts = new Map<string, number>();
  for (const entry of catalog) {
    if (entry.category_counts) {
      for (const [category, count] of Object.entries(entry.category_counts)) {
        const normalized = standardModelCategory(category);
        counts.set(normalized, (counts.get(normalized) ?? 0) + count);
      }
      continue;
    }
    for (const category of entry.categories ?? []) {
      const normalized = standardModelCategory(category);
      counts.set(normalized, (counts.get(normalized) ?? 0) + 1);
    }
    for (const model of entry.models ?? []) {
      const category = modelCategoryForCatalog(model);
      counts.set(category, (counts.get(category) ?? 0) + 1);
    }
  }
  if (counts.size === 0) counts.set("custom", 1);
  const ordered = preferredModelCategories.filter((category) => counts.has(category));
  return ordered.map((category) => ({
    key: category,
    label: modelCategoryLabel(category),
    count: counts.get(category) ?? 0,
  }));
}

function providerEntrySupportsCategory(entry: ProviderCatalogEntry, category: string) {
  if (category === "all") return true;
  for (const [rawCategory, count] of Object.entries(entry.category_counts ?? {})) {
    if (count > 0 && standardModelCategory(rawCategory) === category) return true;
  }
  if ((entry.categories ?? []).some((rawCategory) => standardModelCategory(rawCategory) === category)) return true;
  return (entry.models ?? []).some((model) => modelCategoryForCatalog(model) === category);
}

function providerEntryCategoryCount(entry: ProviderCatalogEntry, category: string) {
  if (category === "all") return entry.models_count;
  let count = 0;
  for (const [rawCategory, rawCount] of Object.entries(entry.category_counts ?? {})) {
    if (standardModelCategory(rawCategory) === category) count += rawCount;
  }
  if (count > 0) return count;
  const modelCount = (entry.models ?? []).filter((model) => modelCategoryForCatalog(model) === category).length;
  if (modelCount > 0) return modelCount;
  return (entry.categories ?? []).some((rawCategory) => standardModelCategory(rawCategory) === category) ? entry.models_count : 0;
}

function buildCustomProviderCatalogEntry(category: string, standardModels: Model[]): ProviderCatalogEntry {
  const normalizedCategory = standardModelCategory(category);
  const models = standardModels
    .filter((model) => normalizedCategory === "all" || modelCategory(model) === normalizedCategory)
    .map((model) => ({
      id: model.name,
      name: model.name,
      display_name: model.name,
      canonical_name: model.name,
      category: modelCategory(model),
      family: model.family,
      type: model.modality,
      context_window: model.context_window,
      input_price_usd_per_1m: model.input_price_usd_per_1m,
      output_price_usd_per_1m: model.output_price_usd_per_1m,
      input_modalities: model.input_modalities,
      output_modalities: model.output_modalities,
      capabilities: model.capabilities,
      supported_parameters: model.supported_parameters,
    }));
  return {
    id: "custom",
    name: "自定义渠道商",
    display_name: "自定义渠道商",
    type: "openai_compatible",
    categories: [normalizedCategory],
    category_counts: { [normalizedCategory]: models.length },
    models_count: models.length,
    source: "tokenhub-standard-catalog",
    models,
  };
}

function modelCategoryForCatalog(model: ProviderCatalogModel) {
  return standardModelCategory(modelCategory(model));
}

function canonicalModelNameForUI(id: string, displayName?: string) {
  let value = (id || "").trim();
  const slash = value.lastIndexOf("/");
  if (slash >= 0 && slash < value.length - 1) value = value.slice(slash + 1);
  if (!value) value = (displayName || "").trim();
  value = value.toLowerCase().replaceAll(" ", "-").replaceAll("_", "-");
  while (value.includes("--")) value = value.replaceAll("--", "-");
  value = value.replace(/^-+|-+$/g, "");
  for (const prefix of ["deepseek", "claude", "gemini", "qwen", "gpt", "glm"]) {
    value = normalizeCompactModelVersionForUI(value, prefix);
  }
  return value || "custom-model";
}

function normalizeCompactModelVersionForUI(value: string, prefix: string) {
  const compact = `${prefix}v`;
  if (value.startsWith(compact) && value.length > compact.length && /\d/.test(value[compact.length])) {
    return `${prefix}-v${value.slice(compact.length)}`;
  }
  if (value.startsWith(prefix) && value.length > prefix.length && /\d/.test(value[prefix.length])) {
    return `${prefix}-${value.slice(prefix.length)}`;
  }
  return value;
}

function filterByModelCategory<T>(view: ViewKey | undefined, items: T[], category: string, data: AppData) {
  if (!view || category === "all") return items;
  if (view === "models") {
    return items.filter((item) => modelCategory(item as Model) === category);
  }
  if (view === "providers") {
    return items.filter((item) => providerCategories(item as Provider, data).includes(category));
  }
  if (view === "notification-channels") {
    return items.filter((item) => notificationChannelType(item as AdminResource) === category);
  }
  return items;
}

function modelCategoryTabs(data: AppData, view: ViewKey) {
  const counts = new Map<string, number>();
  if (view === "providers") {
    for (const provider of data.providers) {
      for (const category of providerCategories(provider, data)) {
        counts.set(category, (counts.get(category) ?? 0) + 1);
      }
    }
  } else {
    for (const model of data.models) {
      const category = modelCategory(model);
      counts.set(category, (counts.get(category) ?? 0) + 1);
    }
  }
  const ordered = preferredModelCategories.filter((category) => counts.has(category));
  for (const category of Array.from(counts.keys()).sort()) {
    if (!ordered.includes(category)) ordered.push(category);
  }
  return [
    { key: "all", label: "全部", count: view === "providers" ? data.providers.length : data.models.length },
    ...ordered.map((category) => ({
      key: category,
      label: modelCategoryLabel(category),
      count: counts.get(category) ?? 0,
    })),
  ];
}

function notificationChannelTabs(data: AppData) {
  const counts = new Map<string, number>();
  for (const item of data.resources["notification-channels"] ?? []) {
    const type = notificationChannelType(item);
    counts.set(type, (counts.get(type) ?? 0) + 1);
  }
  return notificationChannelTypes.map((type) => ({
    key: type,
    label: notificationChannelLabel(type),
    count: counts.get(type) ?? 0,
  }));
}

function notificationChannelType(item: AdminResource) {
  return normalizeNotificationChannelType(stringifyValue(item.fields?.type));
}

function notificationChannelFormType(values: Record<string, string>) {
  return normalizeNotificationChannelType(values.type);
}

function notificationChannelUsesIncomingWebhook(values: Record<string, string>) {
  return !["email", "telegram", "whatsapp"].includes(notificationChannelFormType(values));
}

function notificationChannelUsesEmail(values: Record<string, string>) {
  return notificationChannelFormType(values) === "email";
}

function notificationChannelUsesTelegram(values: Record<string, string>) {
  return notificationChannelFormType(values) === "telegram";
}

function notificationChannelUsesWhatsApp(values: Record<string, string>) {
  return notificationChannelFormType(values) === "whatsapp";
}

function normalizeNotificationChannelType(type: string) {
  const normalized = type.trim().toLowerCase();
  if (normalized === "dingding" || normalized === "ding_talk") return "dingtalk";
  if (normalized === "wechat_work" || normalized === "weixin_work" || normalized === "enterprise_wechat") return "wecom";
  if (normalized === "tg") return "telegram";
  if (["whatsapp_cloud", "whatsapp_business", "wa"].includes(normalized)) return "whatsapp";
  if (notificationChannelTypes.includes(normalized)) return normalized;
  return "webhook";
}

function notificationChannelLabel(type: string) {
  const labels: Record<string, string> = {
    webhook: "Webhook",
    feishu: "飞书",
    dingtalk: "钉钉",
    wecom: "企业微信",
    slack: "Slack",
    discord: "Discord",
    telegram: "Telegram",
    whatsapp: "WhatsApp",
    email: "邮件",
  };
  return tx(labels[normalizeNotificationChannelType(type)] ?? type);
}

function notificationChannelDescription(type: string) {
  const descriptions: Record<string, string> = {
    webhook: "通用 Webhook 告警通知",
    feishu: "飞书机器人告警通知",
    dingtalk: "钉钉机器人告警通知",
    wecom: "企业微信机器人告警通知",
    slack: "Slack Incoming Webhook 告警通知",
    discord: "Discord Webhook 告警通知",
    telegram: "Telegram Bot 告警通知",
    whatsapp: "WhatsApp Cloud API 告警通知",
    email: "SMTP 邮件告警通知",
  };
  return descriptions[normalizeNotificationChannelType(type)] ?? "告警通知渠道";
}

function notificationChannelURLPlaceholder(type: string) {
  const urls: Record<string, string> = {
    webhook: "http://localhost:8081/tokenhub-alert",
    feishu: "https://open.feishu.cn/open-apis/bot/v2/hook/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    dingtalk: "https://oapi.dingtalk.com/robot/send?access_token=xxxxxxxx",
    wecom: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    slack: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
    discord: "https://discord.com/api/webhooks/000000000000000000/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
    telegram: "Telegram Bot Token + Chat ID",
    whatsapp: "WhatsApp Phone Number ID + Access Token",
  };
  return urls[normalizeNotificationChannelType(type)] ?? urls.webhook;
}

function notificationChannelTargetSummary(item: AdminResource) {
  const type = notificationChannelType(item);
  if (type === "email") {
    return compactList(item.fields?.email_to);
  }
  if (type === "telegram") {
    return stringifyValue(item.fields?.telegram_chat_id || item.fields?.chat_id || item.fields?.recipient || item.fields?.to) || "-";
  }
  if (type === "whatsapp") {
    return stringifyValue(item.fields?.whatsapp_to || item.fields?.recipient || item.fields?.to) || "-";
  }
  return maskWebhookURL(stringifyValue(item.fields?.webhook_url));
}

function notificationCredentialSummary(item: AdminResource) {
  const type = notificationChannelType(item);
  if (type === "email") {
    return stringifyValue(item.fields?.smtp_password) ? "SMTP 已配置" : "SMTP 未配置";
  }
  if (type === "telegram") {
    return stringifyValue(item.fields?.telegram_bot_token || item.fields?.bot_token || item.fields?.secret) ? "Bot Token 已配置" : "Bot Token 未配置";
  }
  if (type === "whatsapp") {
    return stringifyValue(item.fields?.access_token || item.fields?.whatsapp_access_token || item.fields?.secret) ? "Access Token 已配置" : "Access Token 未配置";
  }
  return stringifyValue(item.fields?.secret) ? "已配置" : "未配置";
}

function maskWebhookURL(url: string) {
  if (!url) return "-";
  try {
    const parsed = new URL(url);
    const token = parsed.pathname.split("/").filter(Boolean).at(-1) || "";
    const maskedToken = token.length > 8 ? `${token.slice(0, 4)}...${token.slice(-4)}` : token;
    parsed.pathname = parsed.pathname.replace(token, maskedToken);
    if (parsed.search) parsed.search = "?...";
    return parsed.toString();
  } catch {
    return url.length > 24 ? `${url.slice(0, 14)}...${url.slice(-6)}` : url;
  }
}

function modelCatalogCategories(data: AppData) {
  const counts = new Map<string, number>();
  for (const model of data.models) {
    const category = modelCategory(model);
    counts.set(category, (counts.get(category) ?? 0) + 1);
  }
  const ordered = preferredModelCategories.filter((item) => counts.has(item));
  for (const category of Array.from(counts.keys()).sort()) {
    if (!ordered.includes(category)) ordered.push(category);
  }
  return [
    { key: "all", label: modelCategoryLabel("all"), count: data.models.length },
    ...ordered.map((key) => ({ key, label: modelCategoryLabel(key), count: counts.get(key) ?? 0 })),
  ];
}

function modelCatalogCapabilityTabs(data: AppData) {
  const models = data.models;
  const tabs: Array<{ key: string; label: string; icon: typeof Boxes; count: number }> = [
    { key: "all", label: "所有模型", icon: Boxes, count: models.length },
    { key: "featured", label: "精选", icon: Sparkles, count: models.filter(isFeaturedModel).length },
    { key: "text", label: "文本", icon: FileText, count: models.filter((model) => modelCapabilityKeys(model).includes("text")).length },
    { key: "image", label: "图像", icon: Activity, count: models.filter((model) => modelCapabilityKeys(model).includes("image")).length },
    { key: "video", label: "视频", icon: Send, count: models.filter((model) => modelCapabilityKeys(model).includes("video")).length },
    { key: "audio", label: "音频", icon: Bell, count: models.filter((model) => modelCapabilityKeys(model).includes("audio")).length },
    { key: "embedding", label: "嵌入", icon: Database, count: models.filter((model) => modelCapabilityKeys(model).includes("embedding")).length },
    { key: "rerank", label: "重排序", icon: BarChart3, count: models.filter((model) => modelCapabilityKeys(model).includes("rerank")).length },
    { key: "third_party", label: "三方资源", icon: Users, count: models.filter((model) => hasThirdPartyRoute(model, data)).length },
  ];
  return tabs.filter((tab) => tab.key === "all" || tab.key === "third_party" || tab.count > 0);
}

function filterModelCatalog(models: Model[], data: AppData, category: string, capability: string, query: string) {
  const normalized = query.trim().toLowerCase();
  return models
    .filter((model) => category === "all" || modelCategory(model) === category)
    .filter((model) => {
      if (capability === "all") return true;
      if (capability === "featured") return isFeaturedModel(model);
      if (capability === "third_party") return hasThirdPartyRoute(model, data);
      return modelCapabilityKeys(model).includes(capability);
    })
    .filter((model) => {
      if (!normalized) return true;
      return [
        model.name,
        model.id,
        model.family,
        model.modality,
        model.category,
        ...(model.capabilities ?? []),
        ...(model.supported_parameters ?? []),
      ].filter(Boolean).join(" ").toLowerCase().includes(normalized);
    })
    .sort((left, right) => {
      const featuredDiff = Number(isFeaturedModel(right)) - Number(isFeaturedModel(left));
      return featuredDiff || modelCategoryRank(left) - modelCategoryRank(right) || left.name.localeCompare(right.name);
    });
}

function modelCapabilityKey(model: Model) {
  return modelCapabilityKeys(model)[0] ?? "text";
}

function modelCapabilityKeys(model: Model) {
  const values = [model.modality, ...(model.capabilities ?? []), ...(model.input_modalities ?? []), ...(model.output_modalities ?? [])]
    .join(" ")
    .toLowerCase();
  const keys = new Set<string>();
  if (values.includes("chat") || values.includes("text") || values.includes("llm")) keys.add("text");
  if (values.includes("embed")) keys.add("embedding");
  if (values.includes("rerank")) keys.add("rerank");
  if (values.includes("image") || values.includes("vision") || values.includes("ocr")) keys.add("image");
  if (values.includes("video")) keys.add("video");
  if (values.includes("audio") || values.includes("tts") || values.includes("asr")) keys.add("audio");
  if (keys.size === 0) keys.add("text");
  return Array.from(keys);
}

function modelCapabilityLabel(model: Model) {
  const labels: Record<string, string> = {
    text: "文本",
    image: "图像",
    video: "视频",
    audio: "音频",
    embedding: "嵌入",
    rerank: "重排序",
  };
  return modelCapabilityKeys(model).map((key) => tx(labels[key] ?? key)).slice(0, 3).join(" / ");
}

function isFeaturedModel(model: Model) {
  const routes = model.name.toLowerCase();
  return model.status === "active" && (
    routes.includes("gpt-5")
    || routes.includes("claude")
    || routes.includes("deepseek")
    || routes.includes("gemini")
    || routes.includes("qwen")
  );
}

function hasThirdPartyRoute(model: Model, data: AppData) {
  return modelRoutesFor(model, data).some((route) => {
    const provider = findProvider(data, route.provider_id);
    if (!provider) return false;
    return provider.type === "openai_compatible" || provider.type === "local" || provider.type === "mock";
  });
}

function modelCategoryInitial(category: string, label: string) {
  const normalized = category.toLowerCase();
  if (normalized === "claude") return "A";
  if (normalized === "gemini") return "G";
  if (normalized === "openai") return "O";
  if (normalized === "deepseek") return "D";
  if (normalized === "qwen") return "Q";
  if (normalized === "grok") return "X";
  return (label || category || "M").trim().slice(0, 1).toUpperCase();
}

function modelCatalogFilterLabel(categories: Array<{ key: string; label: string }>, active: string) {
  return categories.find((item) => item.key === active)?.label ?? "全部";
}

function priceMetric(value: number | undefined) {
  if (!value) return "$-";
  return `$${formatMoney(value)}/Mt`;
}

function modelCategory(model: Model | ProviderCatalogModel | undefined) {
  const explicit = model?.category?.trim().toLowerCase();
  if (explicit) return standardModelCategory(explicit);
  const displayName = model && "display_name" in model ? model.display_name : "";
  return inferModelCategoryText([model?.name, model?.id, displayName, model?.family].filter(Boolean).join(" "));
}

function providerCategories(provider: Provider, data: AppData) {
  const routeModels = providerRoutesFor(provider, data)
    .map((route) => data.models.find((model) => model.name === route.model_name))
    .filter(Boolean) as Model[];
  const categories = routeModels.map(modelCategory);
  const optionCategory = provider.options?.model_category;
  if (optionCategory) categories.push(standardModelCategory(optionCategory));
  if (categories.length === 0) categories.push(providerTypeToModelCategory(provider.type));
  return Array.from(new Set(categories.filter(Boolean))).sort();
}

function providerTypeToModelCategory(type: string) {
  const normalized = type.toLowerCase();
  if (normalized.includes("anthropic")) return "claude";
  if (normalized.includes("gemini")) return "gemini";
  if (normalized.includes("deepseek")) return "deepseek";
  if (normalized.includes("qwen")) return "qwen";
  if (normalized.includes("azure") || normalized.includes("openai")) return "openai";
  if (normalized.includes("local")) return "custom";
  return "custom";
}

function modelCategoryFormOptions() {
  return preferredModelCategories.filter((category) => category !== "custom").concat("custom");
}

function standardModelCategory(category: string) {
  const normalized = category.trim().toLowerCase();
  if (!normalized) return "custom";
  if (modelCategoryLabels[normalized] && normalized !== "all") return normalized;
  return inferModelCategoryText(normalized);
}

function modelCategoryLabel(category: string) {
  return tx(modelCategoryLabels[category] ?? category);
}

function inferModelCategoryText(value: string) {
  const normalized = value.toLowerCase();
  if (normalized.includes("gpt") || normalized.includes("openai") || /\bo[134]\b/.test(normalized)) return "openai";
  if (normalized.includes("claude") || normalized.includes("anthropic")) return "claude";
  if (normalized.includes("deepseek")) return "deepseek";
  if (normalized.includes("gemini") || normalized.includes("google")) return "gemini";
  if (normalized.includes("qwen") || normalized.includes("dashscope") || normalized.includes("alibaba")) return "qwen";
  if (normalized.includes("glm") || normalized.includes("zhipu")) return "glm";
  if (normalized.includes("kimi") || normalized.includes("moonshot")) return "kimi";
  if (normalized.includes("doubao") || normalized.includes("volcengine")) return "doubao";
  if (normalized.includes("ernie")) return "ernie";
  if (normalized.includes("baichuan")) return "baichuan";
  if (normalized.includes("minimax") || normalized.includes("hailuo")) return "minimax";
  if (normalized.includes("step-")) return "stepfun";
  if (normalized.includes("wanx")) return "wanx";
  if (normalized.includes("paddleocr")) return "paddlepaddle";
  if (normalized.includes("phi-")) return "microsoft";
  if (normalized.includes("llama")) return "llama";
  if (normalized.includes("mistral")) return "mistral";
  if (normalized.includes("grok") || normalized.includes("xai")) return "grok";
  return "custom";
}

function rowID(item: unknown) {
  return String(readPath(item, "id") || readPath(item, "name") || JSON.stringify(item));
}

function rowTitle(item: unknown) {
  return String(readPath(item, "name") || readPath(item, "id") || tx("该记录"));
}

function readPath(item: unknown, path: string): React.ReactNode {
  const value = path.split(".").reduce<unknown>((acc, key) => {
    if (acc && typeof acc === "object") return (acc as Record<string, unknown>)[key];
    return undefined;
  }, item);
  if (Array.isArray(value)) return value.join(", ");
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") return value;
  if (typeof value === "string") return value;
  if (value == null) return "-";
  return JSON.stringify(value);
}

function stringifyForm(item: Record<string, unknown>) {
  const result: Record<string, string> = {};
  for (const [key, value] of Object.entries(item)) {
    result[key] = stringifyValue(value);
  }
  return result;
}

function stringifyValue(value: unknown) {
  if (value == null) return "";
  if (Array.isArray(value)) return value.join(", ");
  if (typeof value === "object") return JSON.stringify(value);
  return String(value);
}

function findProvider(data: AppData, providerID: string) {
  return data.providers.find((provider) => provider.id === providerID);
}

function findProviderResource(data: AppData, resourceID: string) {
  return data.providerResources.find((resource) => resource.id === resourceID);
}

function findProject(data: AppData, projectID: string) {
  return data.projects.find((project) => project.id === projectID);
}

function firstActiveProject(data: AppData) {
  return data.projects.find((project) => project.id === DEFAULT_PROJECT_ID && project.status === "active")
    ?? data.projects.find((project) => project.status === "active")
    ?? data.projects[0];
}

function firstIssueableProject(data: AppData, currentUser?: AdminUser | null) {
  return projectSelectOptions(data, currentUser)[0]?.value ?? "";
}

function firstActiveModel(data: AppData) {
  return data.models.find((model) => model.status === "active") ?? data.models[0];
}

function firstActiveProvider(data: AppData) {
  return data.providers.find((provider) => provider.status === "active") ?? data.providers[0];
}

function firstActiveUser(data: AppData) {
  return data.users.find((user) => user.status === "active") ?? data.users[0];
}

function firstActiveTeam(data: AppData) {
  return (data.resources.teams ?? []).find((team) => team.status === "active") ?? data.resources.teams?.[0];
}

function firstCostCenterCode(data: AppData) {
  const item = (data.resources["cost-centers"] ?? []).find((resource) => resource.status === "active")
    ?? data.resources["cost-centers"]?.[0];
  if (!item) return "";
  return stringifyValue(item.fields?.code) || item.id;
}

function projectSelectOptions(data: AppData, currentUser?: AdminUser | null) {
  return data.projects.filter((project) => projectCanIssueKey(data, project, currentUser)).map((project) => ({
    value: project.id,
    label: projectOptionLabel(data, project),
  }));
}

function projectMemberProjectSelectOptions(data: AppData) {
  return data.projects
    .filter((project) => project.status === "active")
    .map((project) => ({
      value: project.id,
      label: projectOptionLabel(data, project),
    }));
}

function projectMemberRoleOptions() {
  return [
    { value: "owner", label: "负责人" },
    { value: "maintainer", label: "维护者" },
    { value: "developer", label: "开发者" },
    { value: "viewer", label: "只读" },
  ];
}

function oauthDefaultProjectRoleOptions() {
  return [
    { value: "developer", label: "开发者" },
    { value: "viewer", label: "只读" },
    { value: "maintainer", label: "维护者" },
  ];
}

function modelSelectOptions(data: AppData) {
  return data.models
    .slice()
    .sort((left, right) => modelCategoryRank(left) - modelCategoryRank(right) || left.name.localeCompare(right.name))
    .map((model) => ({
      value: model.name,
      label: `${model.name} / ${modelCategoryLabel(modelCategory(model))}${model.status !== "active" ? ` / ${enumValueLabel(model.status)}` : ""}`,
    }));
}

function providerSelectOptions(data: AppData) {
  return data.providers
    .slice()
    .sort((left, right) => (left.priority - right.priority) || left.name.localeCompare(right.name))
    .map((provider) => ({
      value: provider.id,
      label: `${provider.name || provider.id} / ${providerTypeLabel(provider.type)}${provider.status !== "active" ? ` / ${enumValueLabel(provider.status)}` : ""}`,
    }));
}

function roleSelectOptions(data: AppData) {
  const configured = (data.resources["role-configs"] ?? [])
    .filter((role) => role.status === "active" && roleConfigAssignable(role))
    .map((role) => {
      const value = stringifyValue(role.fields?.role_key) || role.id;
      return {
        value,
        label: roleDisplayName(role) || roleLabel(value),
      };
    })
    .filter((option) => option.value);
  if (configured.length > 0) {
    return configured;
  }
  return ["user", "team_leader", "admin"].map((role) => ({ value: role, label: roleLabel(role) }));
}

function roleConfigAssignable(role: AdminResource) {
  const value = stringifyValue(role.fields?.assignable).trim().toLowerCase();
  return value === "" || value === "true" || value === "1" || value === "yes";
}

function roleDisplayName(role: AdminResource) {
  return stringifyValue(role.fields?.display_name) || role.name;
}

function roleDisplayLabel(data: AppData, role: string) {
  const normalized = String(role || "").trim();
  const configured = (data.resources["role-configs"] ?? []).find((item) => stringifyValue(item.fields?.role_key) === normalized);
  return configured ? roleDisplayName(configured) : roleLabel(normalized);
}

function userSelectOptions(data: AppData) {
  return data.users
    .slice()
    .sort((left, right) => (left.status === "active" ? 0 : 1) - (right.status === "active" ? 0 : 1) || (left.name || left.username).localeCompare(right.name || right.username))
    .map((user) => ({
      value: user.id,
      label: `${user.name || user.username} / ${user.email || user.username}${user.status !== "active" ? ` / ${enumValueLabel(user.status)}` : ""}`,
    }));
}

function teamSelectOptions(data: AppData) {
  return (data.resources.teams ?? [])
    .slice()
    .sort((left, right) => (left.status === "active" ? 0 : 1) - (right.status === "active" ? 0 : 1) || (left.name || left.id).localeCompare(right.name || right.id))
    .map((team) => ({
      value: team.id,
      label: `${team.name || team.id}${team.status !== "active" ? ` / ${enumValueLabel(team.status)}` : ""}`,
    }));
}

function costCenterSelectOptions(data: AppData) {
  return (data.resources["cost-centers"] ?? [])
    .slice()
    .sort((left, right) => (left.name || left.id).localeCompare(right.name || right.id))
    .map((item) => {
      const code = stringifyValue(item.fields?.code) || item.id;
      return {
        value: code,
        label: `${code} / ${item.name || item.id}${item.status !== "active" ? ` / ${enumValueLabel(item.status)}` : ""}`,
      };
    });
}

function projectOptionLabel(data: AppData, project: Project) {
  return [
    project.name || project.id,
    project.team_id ? `团队 ${teamLabel(data, project.team_id)}` : "",
    project.owner_user_id ? `负责人 ${ownerUserLabel(data, project.owner_user_id)}` : "",
  ]
    .filter(Boolean)
    .join(" / ");
}

function projectCanIssueKey(data: AppData, project: Project, currentUser?: AdminUser | null) {
  if (project.status !== "active") return false;
  if (!currentUser) return true;
  const role = appRole(currentUser.role);
  if (role === "admin") return true;
  if (role === "security") return false;
  if (project.owner_user_id && project.owner_user_id === currentUser.id) return true;
  if (role === "team_leader" && currentUser.team_id && project.team_id === currentUser.team_id) return true;
  return projectIssueMembership(data, project.id, currentUser.id);
}

function projectIssueMembership(data: AppData, projectID: string, userID: string) {
  return (data.resources["project-members"] ?? []).some((member) => {
    if (member.status !== "active") return false;
    if (stringifyValue(member.fields?.project_id) !== projectID || stringifyValue(member.fields?.user_id) !== userID) return false;
    const role = stringifyValue(member.fields?.role).trim().toLowerCase();
    return ["owner", "maintainer", "developer"].includes(role) || truthyValue(member.fields?.can_issue_keys);
  });
}

function projectMembersForProject(data: AppData, projectID: string) {
  return (data.resources["project-members"] ?? [])
    .filter((member) => stringifyValue(member.fields?.project_id) === projectID)
    .slice()
    .sort((left, right) => {
      const leftUser = data.users.find((user) => user.id === stringifyValue(left.fields?.user_id));
      const rightUser = data.users.find((user) => user.id === stringifyValue(right.fields?.user_id));
      return (leftUser?.name || leftUser?.username || stringifyValue(left.fields?.user_id))
        .localeCompare(rightUser?.name || rightUser?.username || stringifyValue(right.fields?.user_id));
    });
}

function projectMemberCanIssueLabel(item: AdminResource) {
  const role = stringifyValue(item.fields?.role).trim().toLowerCase();
  return ["owner", "maintainer", "developer"].includes(role) || truthyValue(item.fields?.can_issue_keys) ? tx("允许") : tx("不允许");
}

function projectMemberRoleLabel(role: string) {
  switch (role.trim().toLowerCase()) {
    case "owner":
      return tx("负责人");
    case "maintainer":
      return tx("维护者");
    case "developer":
      return tx("开发者");
    case "viewer":
      return tx("只读");
    default:
      return role || "-";
  }
}

function truthyValue(value: unknown) {
  const text = stringifyValue(value).trim().toLowerCase();
  return ["true", "1", "yes", "y", "on", "enabled"].includes(text);
}

function overviewAnnouncements(data: AppData, user: AdminUser) {
  return (data.resources.announcements ?? [])
    .filter((item) => item.status === "active" && announcementTargetsUser(item, user))
    .slice()
    .sort((left, right) => Date.parse(right.updated_at || right.created_at || "") - Date.parse(left.updated_at || left.created_at || ""));
}

function announcementTargetsUser(item: AdminResource, user: AdminUser) {
  const target = stringifyValue(item.fields?.target).trim();
  if (!target || ["all", "all_users", "everyone"].includes(target.toLowerCase())) return true;
  const targets = splitList(target).map((value) => value.toLowerCase());
  const role = appRole(user.role);
  const identityValues = [
    user.id,
    user.username,
    user.email,
    user.team_id,
    role,
    user.role,
  ].filter(Boolean).map((value) => String(value).toLowerCase());
  if (targets.includes("all_admins") && role !== "user") return true;
  return targets.some((value) => identityValues.includes(value));
}

function announcementMode(item: AdminResource) {
  const mode = stringifyValue(item.fields?.notify_mode).toLowerCase();
  return mode === "popup" ? "popup" : "silent";
}

function announcementModeLabel(item: AdminResource) {
  return announcementMode(item) === "popup" ? "弹窗" : "静默";
}

function ownerUserLabel(data: AppData, owner: string) {
  if (!owner) return "-";
  const user = data.users.find((item) => item.id === owner || item.username === owner || item.email === owner);
  if (!user) return owner;
  return [user.name || user.username, user.email].filter(Boolean).join(" / ");
}

function usageMemberLabel(data: AppData, memberID: string) {
  if (!memberID || memberID === "unknown") return "未归属成员";
  return ownerUserLabel(data, memberID);
}

function teamLabel(data: AppData, teamID: string) {
  if (!teamID) return "-";
  const team = (data.resources.teams ?? []).find((item) => item.id === teamID);
  return team?.name || teamID;
}

function teamMemberCount(data: AppData, team: AdminResource) {
  return data.users.filter((user) => user.team_id === team.id).length;
}

function costCenterLabel(data: AppData, costCenter: string) {
  if (!costCenter) return "-";
  const item = (data.resources["cost-centers"] ?? []).find((resource) => {
    const code = stringifyValue(resource.fields?.code);
    return resource.id === costCenter || code === costCenter;
  });
  if (!item) return costCenter;
  const code = stringifyValue(item.fields?.code) || item.id;
  return `${code} / ${item.name || item.id}`;
}

function projectName(data: AppData, projectID: string) {
  const project = findProject(data, projectID);
  return project ? project.name : projectID || "-";
}

function projectOwnerLabel(data: AppData, projectID: string) {
  const project = findProject(data, projectID);
  return ownerUserLabel(data, project?.owner_user_id ?? "");
}

function projectTeamLabel(data: AppData, projectID: string) {
  const project = findProject(data, projectID);
  return teamLabel(data, project?.team_id ?? "");
}

function modelRoutesFor(model: Model, data: AppData) {
  return data.routes
    .filter((route) => route.model_name === model.name)
    .sort((left, right) => (left.priority - right.priority) || (right.weight - left.weight));
}

function routeModelCategories(data: AppData) {
  const counts = new Map<string, number>();
  for (const model of data.models) {
    if (modelRoutesFor(model, data).length === 0) continue;
    const category = modelCategory(model);
    counts.set(category, (counts.get(category) ?? 0) + 1);
  }
  const items = Array.from(counts.entries())
    .map(([key, count]) => ({ key, label: modelCategoryLabel(key), count }))
    .sort((left, right) => preferredModelCategories.indexOf(left.key) - preferredModelCategories.indexOf(right.key) || left.label.localeCompare(right.label));
  const total = items.reduce((sum, item) => sum + item.count, 0);
  return [{ key: "all", label: modelCategoryLabel("all"), count: total }, ...items];
}

function filterRouteModels(data: AppData, category: string, scope: "configured" | "all", query: string) {
  const normalizedQuery = query.trim().toLowerCase();
  return data.models
    .filter((model) => {
      const routes = modelRoutesFor(model, data);
      if (scope === "configured" && routes.length === 0) return false;
      if (category !== "all" && modelCategory(model) !== category) return false;
      if (!normalizedQuery) return true;
      return routeModelSearchText(model, routes, data).includes(normalizedQuery);
    })
    .sort((left, right) => {
      const leftRoutes = modelRoutesFor(left, data).length;
      const rightRoutes = modelRoutesFor(right, data).length;
      if (leftRoutes !== rightRoutes) return rightRoutes - leftRoutes;
      return modelCategoryRank(left) - modelCategoryRank(right) || left.name.localeCompare(right.name);
    });
}

function routeModelSearchText(model: Model, routes: ModelRoute[], data: AppData) {
  const routeText = routes.flatMap((route) => {
    const provider = findProvider(data, route.provider_id);
    return [
      route.model_name,
      route.provider_model,
      route.provider_id,
      provider?.name,
      provider?.type,
      provider?.base_url,
    ];
  });
  return [model.name, model.id, model.family, model.modality, model.category, ...routeText]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function reorderRoutes(routes: ModelRoute[], draggedID: string, targetID: string) {
  if (!draggedID || !targetID || draggedID === targetID) return routes;
  const current = routes.slice();
  const from = current.findIndex((route) => route.id === draggedID);
  const to = current.findIndex((route) => route.id === targetID);
  if (from < 0 || to < 0 || from === to) return routes;
  const [moved] = current.splice(from, 1);
  current.splice(to, 0, moved);
  return current;
}

function providerRoutesFor(provider: Provider, data: AppData) {
  return data.routes.filter((route) => route.provider_id === provider.id);
}

function providerRouteSummary(provider: Provider, data: AppData) {
  const routes = providerRoutesFor(provider, data);
  if (routes.length === 0) return "未配置";
  const active = routes.filter((route) => route.status === "active").length;
  const models = Array.from(new Set(routes.map((route) => route.model_name))).slice(0, 3);
  return `${active}/${routes.length} 启用 · ${models.join(", ")}`;
}

function providerAccountResourceSummary(provider: Provider, data: AppData) {
  const resources = data.providerResources.filter((resource) => resource.provider_id === provider.id && resource.resource_type === "openai_subscription");
  if (resources.length === 0) return <span className="muted-inline">-</span>;
  const active = resources.filter((resource) => resource.status === "active" && resource.healthy).length;
  const first = resources[0]?.credential_summary;
  const label = first?.account_email || first?.account_id || resources[0]?.name || "";
  return (
    <div className="model-name-cell">
      <strong>{active}/{resources.length} {tx("启用")}</strong>
      <span>{label || tx("OpenAI 账号资源")}</span>
    </div>
  );
}

function providerCostDetailRows(data: AppData) {
  if (data.breakdown.providers.length > 0) return data.breakdown.providers;
  const resourcesByID = new Map(data.providerResources.map((resource) => [resource.id, resource]));
  const providersByID = new Map(data.providers.map((provider) => [provider.id, provider]));
  const totals = new Map<string, UsageBreakdownRow>();
  for (const row of data.breakdown.provider_resources ?? []) {
    const resource = resourcesByID.get(row.id);
    const provider = resource ? providersByID.get(resource.provider_id) : undefined;
    const id = provider?.name || resource?.provider_id || row.id;
    const current = totals.get(id) ?? {
      id,
      request_count: 0,
      input_tokens: 0,
      output_tokens: 0,
      total_tokens: 0,
      estimated_cost_usd: 0,
    };
    current.request_count += row.request_count;
    current.input_tokens += row.input_tokens;
    current.output_tokens += row.output_tokens;
    current.total_tokens += row.total_tokens;
    current.estimated_cost_usd += row.estimated_cost_usd;
    totals.set(id, current);
  }
  return Array.from(totals.values());
}

function providerAuditLabel(data: AppData, log: RequestLog) {
  const provider = log.provider_id ? findProvider(data, log.provider_id) : undefined;
  if (provider) return provider.name;
  if (log.provider_id) return log.provider_id;
  const resource = log.provider_resource_id
    ? data.providerResources.find((item) => item.id === log.provider_resource_id)
    : undefined;
  if (!resource) return "-";
  return findProvider(data, resource.provider_id)?.name || resource.provider_id || "-";
}

function providerAttemptLabel(data: AppData, attempt: RouteAttemptLog) {
  const provider = attempt.provider_id ? findProvider(data, attempt.provider_id) : undefined;
  if (provider) return provider.name;
  if (attempt.provider_id) return attempt.provider_id;
  const resource = attempt.provider_resource_id
    ? data.providerResources.find((item) => item.id === attempt.provider_resource_id)
    : undefined;
  if (!resource) return "-";
  return findProvider(data, resource.provider_id)?.name || resource.provider_id || "-";
}

function providerResourceAuditLabel(data: AppData, resourceID?: string) {
  if (!resourceID) return "-";
  const resource = data.providerResources.find((item) => item.id === resourceID);
  if (!resource) return resourceID;
  const provider = findProvider(data, resource.provider_id);
  return [resource.name || resource.id, provider?.name || resource.provider_id].filter(Boolean).join(" / ");
}

function apiKeyAuditLabel(data: AppData, apiKeyID?: string) {
  if (!apiKeyID) return "-";
  const key = data.keys.find((item) => item.id === apiKeyID);
  if (!key) return apiKeyID;
  return `${key.name || key.id} (${key.key_prefix}...${key.key_suffix})`;
}

function providerRouteDefaults(provider: Provider, data: AppData) {
  const firstModel = firstActiveModel(data);
  return {
    model_name: firstModel?.name ?? "",
    provider_id: provider.id,
    provider_model: firstModel?.name ?? "",
    priority: "1",
    weight: "100",
    quality_score: "50",
    cost_score: "50",
    strategy: "balanced",
    sticky_session: "false",
    status: "active",
  };
}

function modelRouteDefaults(model: Model, data: AppData) {
  const firstProvider = firstActiveProvider(data);
  return {
    model_name: model.name,
    provider_id: firstProvider?.id ?? "",
    provider_model: model.name,
    priority: "1",
    weight: "100",
    quality_score: "50",
    cost_score: "50",
    strategy: "balanced",
    sticky_session: "false",
    status: "active",
  };
}

function routeScoreSummary(route: ModelRoute) {
  return `质量 ${route.quality_score ?? 50} / 成本 ${route.cost_score ?? 50}`;
}

function modelCapabilitySummary(model: Model) {
  const capabilities = [
    model.modality,
    ...(model.capabilities ?? []),
    ...(model.supported_parameters ?? []).map((item) => `param:${item}`),
  ].filter(Boolean);
  return compactList(capabilities);
}

function modelPriceSummary(model: Model) {
  const embedding = model.embedding_price_usd_per_1m || 0;
  if (model.modality === "embedding" || embedding > 0) {
    return `$${formatMoney(embedding)}/1M`;
  }
  const input = model.input_price_usd_per_1m || 0;
  const output = model.output_price_usd_per_1m || 0;
  if (!input && !output) return "$-";
  return `$${formatMoney(input)} / $${formatMoney(output)}`;
}

function fieldSummary(fields?: Record<string, unknown>) {
  if (!fields || Object.keys(fields).length === 0) return "-";
  return Object.entries(fields)
    .slice(0, 3)
    .map(([key, value]) => `${fieldKeyLabel(key)}: ${fieldValueLabel(key, value)}`)
    .join(" / ");
}

function compactList(value: unknown) {
  const values = Array.isArray(value) ? value.map(stringifyValue) : splitList(stringifyValue(value));
  if (values.length === 0) return "-";
  if (values.length <= 3) return values.join(", ");
  return `${values.slice(0, 3).join(", ")} +${values.length - 3}`;
}

function boolLabel(value: unknown) {
  const text = stringifyValue(value).trim().toLowerCase();
  return text === "false" || text === "0" || text === "no" ? tx("否") : tx("是");
}

function settingsTabLabel(tab: SettingsTabKey) {
  const labels: Record<SettingsTabKey, string> = {
    settings: "基础设置",
    "role-configs": "角色配置",
    "identity-providers": "身份源",
  };
  return tx(labels[tab]);
}

function identityProviderTypeLabel(type: string) {
  const labels: Record<string, string> = {
    oidc: "OIDC",
    oauth2: "OAuth2",
    saml: "SAML",
    ldap: "LDAP",
  };
  return labels[type] ?? (type || "-");
}

function identityProviderIconLabel(iconKey: string) {
  const normalized = normalizedIdentityProviderIconKey(iconKey);
  const labels: Record<string, string> = {
    auto: "自动",
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

function identityProviderLoginEntryLabel(item: AdminResource) {
  const icon = identityProviderIconLabel(stringifyValue(item.fields?.icon_key));
  const label = stringifyValue(item.fields?.login_label) || item.name;
  return [icon, label].filter((value) => value && value !== "-").join(" / ") || "-";
}

function identityProviderDefaultGrantLabel(data: AppData, item: AdminResource) {
  const role = roleLabel(stringifyValue(item.fields?.default_role) || "user");
  const team = teamLabel(data, stringifyValue(item.fields?.default_team_id));
  const project = projectName(data, stringifyValue(item.fields?.default_project_id));
  const parts = [role];
  if (team !== "-") parts.push(team);
  if (project !== "-") parts.push(project);
  return parts.join(" / ");
}

function dataScopeLabel(scope: string) {
  const labels: Record<string, string> = {
    global: "全局",
    team: "团队",
    project: "项目",
    self: "本人",
  };
  return tx(labels[scope] ?? (scope || "-"));
}

function enumOptionLabel(fieldKey: string, value: string) {
  return fieldValueLabel(fieldKey, value);
}

function enumValueLabel(value: string | undefined) {
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
    quality: "质量优先",
    cost: "成本优先",
    priority_weighted: "优先级 + 权重",
    priority_only: "仅优先级",
    chat: "文本对话",
    embedding: "向量嵌入",
    image: "图像",
    video: "视频",
    audio: "音频",
    ocr: "OCR",
    rerank: "重排序",
  };
  return tx(labels[normalized] ?? roleLabel(normalized) ?? value ?? "-");
}

function fieldValueLabel(fieldKey: string, value: unknown): string {
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
  if (normalizedKey === "trigger") return approvalTriggerLabel(text);
  if (normalizedKey === "dataset") return reportDatasetLabel(text);
  if (normalizedKey === "schedule" || normalizedKey === "period") return enumValueLabel(text);
  if (normalizedKey === "enforcement") return budgetEnforcementLabel(text);
  if (normalizedKey === "type" || normalizedKey === "notify_mode" || normalizedKey === "protocol" || normalizedKey === "target_type" || normalizedKey === "resource_type" || normalizedKey === "modality") {
    return enumValueLabel(text);
  }
  return text;
}

function fieldKeyLabel(key: string) {
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

function monitorTargetLabel(fields?: Record<string, unknown>) {
  const target = stringifyValue(fields?.target_type || "").toLowerCase();
  const labels: Record<string, string> = {
    provider: "Provider",
    resource: "资源实例",
    provider_resource: "资源实例",
    model: "模型路由",
  };
  return tx(labels[target] ?? (target || "-"));
}

function alertMetricLabel(metric: string) {
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

function parseLooseValue(value: string) {
  if (value === "true") return true;
  if (value === "false") return false;
  return value;
}

function roleLabel(role: string) {
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

function providerTypeLabel(type: string | undefined) {
  const normalized = String(type ?? "").trim().toLowerCase();
  if (!normalized) return "-";
  const labels: Record<string, string> = {
    mock: "模拟渠道",
    openai: "OpenAI 官方",
    openai_compatible: "OpenAI 兼容",
    azure_openai: "Azure OpenAI",
    anthropic: "Claude / Anthropic",
    gemini: "Gemini / Google",
    deepseek: "DeepSeek",
    qwen: "Qwen / 通义千问",
    local: "本地模型",
  };
  return tx(labels[normalized] ?? type ?? "-");
}

function budgetScopeLabel(scope: string) {
  const labels: Record<string, string> = {
    project: "项目",
    team: "团队",
    cost_center: "成本中心",
    "cost-center": "成本中心",
  };
  return tx(labels[scope] ?? scope);
}

function budgetEnforcementLabel(value: string) {
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

function reportDatasetLabel(dataset: string) {
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

function reportScheduleLabel(schedule: string) {
  const labels: Record<string, string> = {
    manual: "手动",
    daily: "每日",
    weekly: "每周",
    monthly: "每月",
  };
  return tx(labels[schedule] ?? schedule);
}

function actionLabel(action: string) {
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

function resourceTypeLabel(type: string) {
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

function approvalTriggerLabel(trigger: string) {
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

function approvalStatusLabel(status: string) {
  const labels: Record<string, string> = {
    pending: "待审批",
    approved: "已批准",
    rejected: "已驳回",
  };
  return tx(labels[status] ?? status);
}

function invoiceStatusLabel(status: string) {
  const labels: Record<string, string> = {
    pending: "待确认",
    confirmed: "已确认",
    rejected: "已驳回",
    active: "有效",
    disabled: "停用",
  };
  return tx(labels[status] ?? status);
}

function approvalPayloadSummary(payload?: string) {
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

function userInitial(user: AdminUser) {
  const source = displayText(user.name) || user.username || user.email || "U";
  return source.trim().slice(0, 1).toUpperCase();
}

function numberOr(value: string | undefined, fallback: number) {
  const num = Number(value);
  return Number.isFinite(num) ? num : fallback;
}

function numberFromUnknown(value: unknown) {
  if (typeof value === "number") return value;
  if (typeof value === "string") return numberOr(value, 0);
  return 0;
}

function splitList(value: string | undefined) {
  return (value ?? "")
    .split(",")
    .map((item) => item.trim())
    .filter(Boolean);
}

function initialView(): ViewKey {
  if (typeof window === "undefined") return "overview";
  return viewFromPath(window.location.pathname);
}

function viewFromPath(pathname: string): ViewKey {
  const normalized = pathname.replace(/^\/+|\/+$/g, "");
  if (!normalized) return "overview";
  return routeViews[normalized] ?? "overview";
}

function playgroundModels(data: AppData, sortByRoutes = data.routes.length > 0) {
  return data.models
    .filter((model) => model.status === "active" && (model.modality === "" || model.modality === "chat"))
    .sort((a, b) => {
      const routeDiff = sortByRoutes ? activeRouteCount(b.name, data) - activeRouteCount(a.name, data) : 0;
      return routeDiff || modelCategoryRank(a) - modelCategoryRank(b) || a.name.localeCompare(b.name);
    });
}

const apiExampleLanguages: Array<{ key: ApiExampleLanguage; label: string }> = [
  { key: "python", label: "Python" },
  { key: "typescript", label: "TypeScript" },
  { key: "java", label: "Java" },
  { key: "go", label: "Go" },
];

function apiExampleScripts(baseURL: string, modelName: string): Record<ApiExampleLanguage, string> {
  const normalizedBaseURL = apiGatewayBaseURL(baseURL);
  const model = modelName || "gpt-4.1-mini";
  const systemPrompt = tx("你是企业内部 AI 助手。");
  const prompt = tx("请用三句话介绍 TokenHub。");
  return {
    python: `from openai import OpenAI

client = OpenAI(
    api_key="YOUR_TOKENHUB_API_KEY",
    base_url="${normalizedBaseURL}"
)

response = client.chat.completions.create(
    model="${model}",
    messages=[
        {"role": "system", "content": "${systemPrompt}"},
        {"role": "user", "content": "${prompt}"},
    ],
    temperature=0.7,
)

print(response.choices[0].message.content)`,
    typescript: `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY ?? "YOUR_TOKENHUB_API_KEY",
  baseURL: "${normalizedBaseURL}",
});

const response = await client.chat.completions.create({
  model: "${model}",
  messages: [
    { role: "system", content: "${systemPrompt}" },
    { role: "user", content: "${prompt}" },
  ],
  temperature: 0.7,
});

console.log(response.choices[0]?.message?.content);`,
    java: `import com.openai.client.OpenAIClient;
import com.openai.client.okhttp.OpenAIOkHttpClient;
import com.openai.models.ChatModel;
import com.openai.models.chat.completions.ChatCompletionCreateParams;

public class TokenHubExample {
  public static void main(String[] args) {
    OpenAIClient client = OpenAIOkHttpClient.builder()
        .apiKey(System.getenv().getOrDefault("TOKENHUB_API_KEY", "YOUR_TOKENHUB_API_KEY"))
        .baseUrl("${normalizedBaseURL}")
        .build();

    ChatCompletionCreateParams params = ChatCompletionCreateParams.builder()
        .model(ChatModel.of("${model}"))
        .addSystemMessage("${systemPrompt}")
        .addUserMessage("${prompt}")
        .temperature(0.7)
        .build();

    System.out.println(client.chat().completions().create(params).choices().get(0).message().content().orElse(""));
  }
}`,
    go: `package main

import (
	"context"
	"fmt"
	"os"

	openai "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	client := openai.NewClient(
		option.WithAPIKey(os.Getenv("TOKENHUB_API_KEY")),
		option.WithBaseURL("${normalizedBaseURL}"),
	)

	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model: "${model}",
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("${systemPrompt}"),
			openai.UserMessage("${prompt}"),
		},
		Temperature: openai.Float(0.7),
	})
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
}`,
  };
}

function apiGatewayBaseURL(baseURL: string) {
  const trimmed = (baseURL || "http://localhost:8080").replace(/\/+$/, "");
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

function activeRouteCount(modelName: string, data: AppData) {
  return data.routes.filter((route) => route.model_name === modelName && route.status === "active").length;
}

type ModelAvailabilityTone = "ready" | "warning" | "blocked" | "restricted";

type ModelAvailabilitySummary = {
  tone: ModelAvailabilityTone;
  label: string;
  detail: string;
  totalRoutes: number;
  activeRoutes: number;
  healthyRoutes: number;
};

function modelAvailabilitySummary(model: Model, data: AppData, readOnly = false): ModelAvailabilitySummary {
  const routes = modelRoutesFor(model, data);
  const activeRoutes = routes.filter((route) => route.status === "active");
  const healthyRoutes = activeRoutes.filter((route) => routeHasHealthyTarget(route, data));
  if (model.status !== "active") {
    return {
      tone: "blocked",
      label: "模型未启用",
      detail: "模型目录状态不是启用，前台不会作为可调用模型。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (readOnly && routes.length === 0) {
    return {
      tone: "restricted",
      label: "按权限可见",
      detail: "当前账号可见此模型；实际调用还会受项目 Key 白名单和运行时路由策略限制。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (routes.length === 0) {
    return {
      tone: "blocked",
      label: "未配置路由",
      detail: "管理员需要在路由策略中把该模型映射到一个 Provider 上游模型。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (activeRoutes.length === 0) {
    return {
      tone: "blocked",
      label: "路由未启用",
      detail: "已有 Provider 线路，但线路状态未启用，运行时不会命中。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  if (healthyRoutes.length === 0) {
    return {
      tone: "warning",
      label: "线路需检查",
      detail: "启用线路存在，但 Provider 或账号资源不是健康启用状态。",
      totalRoutes: routes.length,
      activeRoutes: activeRoutes.length,
      healthyRoutes: healthyRoutes.length,
    };
  }
  return {
    tone: "ready",
    label: "可调用",
    detail: `${healthyRoutes.length} 条健康启用线路，调用时仍会受项目 Key 白名单和额度限制。`,
    totalRoutes: routes.length,
    activeRoutes: activeRoutes.length,
    healthyRoutes: healthyRoutes.length,
  };
}

function routeHasHealthyTarget(route: ModelRoute, data: AppData) {
  const provider = findProvider(data, route.provider_id);
  if (!provider || provider.status !== "active" || provider.healthy === false) return false;
  if (route.provider_resource_id) {
    const resource = findProviderResource(data, route.provider_resource_id);
    return Boolean(resource && resource.status === "active" && resource.healthy !== false);
  }
  const group = stringifyValue(route.resource_group);
  if (group) {
    const groupedResources = data.providerResources.filter((resource) =>
      resource.provider_id === route.provider_id && resource.group === group && resource.status === "active",
    );
    if (groupedResources.length > 0) return groupedResources.some((resource) => resource.healthy !== false);
  }
  return true;
}

function modelCatalogEmptyText(data: AppData, readOnly: boolean, query: string) {
  if (query.trim()) return tx("没有匹配的模型");
  if (data.models.length === 0) {
    return readOnly
      ? tx("当前没有可调用模型。通常原因是管理员还没有启用模型目录或路由策略，或你的项目/Key 未被授予模型范围。")
      : tx("当前还没有模型目录。请先维护模型目录，再配置路由策略。");
  }
  return readOnly
    ? tx("当前筛选下没有可见模型。可用性由模型目录、路由策略、项目成员和 Key 白名单共同决定。")
    : tx("没有匹配的模型");
}

function keyWizardModelOptions(data: AppData) {
  const activeChatModels = playgroundModels(data, data.routes.length > 0);
  const routed = activeChatModels.filter((model) => data.routes.length === 0 || activeRouteCount(model.name, data) > 0);
  return (routed.length > 0 ? routed : activeChatModels).sort((left, right) =>
    modelCategoryRank(left) - modelCategoryRank(right) || left.name.localeCompare(right.name),
  );
}

function modelCategoryRank(model: Model) {
  const index = preferredModelCategories.indexOf(modelCategory(model));
  return index >= 0 ? index : preferredModelCategories.length;
}

function uniqueUIID(prefix: string) {
  if (typeof crypto !== "undefined" && "randomUUID" in crypto) {
    return `${prefix}_${crypto.randomUUID()}`;
  }
  return `${prefix}_${Date.now()}_${Math.random().toString(36).slice(2)}`;
}

async function readAPIError(resp: Response) {
  const payload = await resp.json().catch(() => null);
  const code = payload?.error?.code || payload?.error?.type || `HTTP ${resp.status}`;
  const message = payload?.error?.message || "请求失败";
  if (code === "provider_unavailable") return "该模型暂无可用路由，请先在路由策略中配置启用线路。";
  if (code === "provider_not_configured") return "命中的 Provider 尚未配置 Base URL 或凭证。";
  if (code === "provider_resource_concurrency_exceeded") return "Provider 资源并发已满，请稍后再试。";
  if (code === "provider_resource_cooling_down") return "Provider 资源处于冷却中，请检查资源健康状态。";
  return `${message} (${code})`;
}

function extractAssistantText(payload: PlaygroundChatPayload) {
  const choice = payload.response?.choices?.[0];
  const content = choice?.message?.content ?? choice?.text ?? payload.response?.output_text ?? payload.response?.content;
  return stringifyChatContent(content);
}

function stringifyChatContent(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((item) => {
        if (typeof item === "string") return item;
        if (item && typeof item === "object") {
          const record = item as Record<string, unknown>;
          return stringifyChatContent(record.text ?? record.content ?? record.value);
        }
        return "";
      })
      .filter(Boolean)
      .join("\n");
  }
  if (content == null) return "";
  if (typeof content === "object") return JSON.stringify(content, null, 2);
  return String(content);
}

function fallbackDays(): UsagePoint[] {
  return Array.from({ length: 31 }, (_, index) => ({
    date: `2026-06-${String(index + 1).padStart(2, "0")}`,
    request_count: 0,
    input_tokens: 0,
    output_tokens: 0,
    total_tokens: 0,
    estimated_cost_usd: 0,
  }));
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("en-US").format(value || 0);
}

function compactNumber(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(2)}K`;
  return formatNumber(value || 0);
}

function formatMoney(value: number) {
  return (value || 0).toFixed(value >= 1 ? 2 : 6);
}

function formatDashboardMoney(value: number) {
  const amount = Math.max(0, value || 0);
  if (amount === 0) return "0.00";
  if (amount >= 1_000_000) return `${(amount / 1_000_000).toFixed(amount >= 10_000_000 ? 1 : 2)}M`;
  if (amount >= 1_000) return `${(amount / 1_000).toFixed(amount >= 10_000 ? 1 : 2)}K`;
  if (amount >= 1) return amount.toFixed(2);
  if (amount >= 0.01) return amount.toFixed(4);
  return "<0.01";
}

function modelCapabilities(model: ProviderCatalogModel) {
  return [
    ...(model.capabilities ?? []),
    ...(model.supported_parameters ?? []).map((item) => `param:${item}`),
  ].slice(0, 8);
}

function formatModelPrice(model: ProviderCatalogModel) {
  const input = model.input_price_usd_per_1m ?? 0;
  const output = model.output_price_usd_per_1m ?? 0;
  if (!input && !output) return "$-";
  return `$${formatMoney(input)}/$${formatMoney(output)}`;
}

function modelToForm(item: Model) {
  return {
    ...stringifyForm(item),
    capabilities: (item.capabilities ?? []).join(", "),
    supported_parameters: (item.supported_parameters ?? []).join(", "),
    input_modalities: (item.input_modalities ?? []).join(", "),
    output_modalities: (item.output_modalities ?? []).join(", "),
  };
}

function formatBytes(value: number) {
  if (!value) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let size = value;
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

function routeStrategyLabel(value?: string) {
  const labels: Record<string, string> = {
    balanced: "平衡",
    quality: "质量优先",
    cost: "成本优先",
    priority_weighted: "优先级 + 权重",
    priority_only: "仅优先级",
  };
  return tx(labels[value || "balanced"] ?? value ?? "平衡");
}

function formatTime(value: string) {
  if (!value) return "-";
  return new Date(value).toLocaleString();
}

type SavedSession = {
  baseURL: string;
  token: string;
  user: AdminUser;
  expiresAt: string;
};

type OAuthLoginResult = {
  token?: string;
  expiresAt?: string;
  error?: string;
};

type ProviderAccountOAuthResult = {
  access_token?: string;
  refresh_token?: string;
  id_token?: string;
  session_id?: string;
  state?: string;
  account_email?: string;
  account_id?: string;
  organization_id?: string;
  plan_type?: string;
  token_type?: string;
  expires_at?: string;
  scopes?: string;
  authorization_code?: string;
  error?: string;
};

type ProviderAccountOAuthGenerateResponse = {
  auth_url: string;
  session_id: string;
  state: string;
  redirect_uri: string;
  expires_at: string;
};

function readOAuthLoginResult(): OAuthLoginResult | null {
  if (typeof window === "undefined") return null;
  const sources = [window.location.hash.replace(/^#/, ""), window.location.search.replace(/^\?/, "")];
  for (const source of sources) {
    if (!source) continue;
    const params = new URLSearchParams(source);
    const token = params.get("oauth_token") ?? "";
    const error = params.get("oauth_error") ?? "";
    if (token || error) {
      return {
        token,
        error,
        expiresAt: params.get("oauth_expires_at") ?? undefined,
      };
    }
  }
  return null;
}

const providerAccountOAuthStorageKey = "tokenhub_provider_account_oauth_result";
const providerAccountOAuthSessionStorageKey = "tokenhub_provider_account_oauth_session";

function providerAccountOAuthCallbackURL() {
  if (typeof window === "undefined") return "";
  const url = new URL(window.location.href);
  url.hash = "";
  url.search = "";
  url.searchParams.set("provider_account_oauth", "1");
  return url.toString();
}

function parseProviderAccountOAuthResult(source: string, allowGenericTokenNames = false): ProviderAccountOAuthResult | null {
  const raw = source.trim();
  if (!raw) return null;
  const candidates: string[] = [];
  try {
    const url = new URL(raw);
    const search = url.search.replace(/^\?/, "");
    const hash = url.hash.replace(/^#/, "");
    candidates.push(search);
    candidates.push(hash);
    candidates.push([search, hash].filter(Boolean).join("&"));
  } catch {
    candidates.push(raw.replace(/^[?#]/, ""));
  }
  for (const candidate of candidates) {
    if (!candidate || !candidate.includes("=")) continue;
    const params = new URLSearchParams(candidate);
    const marked = allowGenericTokenNames || params.get("provider_account_oauth") === "1" || params.get("tokenhub_provider_account") === "1";
    const result: ProviderAccountOAuthResult = {};
    result.access_token = firstParam(params, marked ? ["account_access_token", "provider_access_token", "access_token", "token"] : ["account_access_token", "provider_access_token"]);
    result.refresh_token = firstParam(params, marked ? ["account_refresh_token", "refresh_token"] : ["account_refresh_token"]);
    result.id_token = firstParam(params, marked ? ["account_id_token", "id_token"] : ["account_id_token"]);
    result.session_id = firstParam(params, ["provider_account_oauth_session_id", "account_oauth_session_id", "session_id"]);
    result.state = firstParam(params, ["provider_account_oauth_state", "account_oauth_state", "state"]);
    result.error = firstParam(params, ["provider_account_oauth_error", "oauth_error", "error"]);
    result.account_email = firstParam(params, ["account_email", "email", "login", "username"]);
    result.account_id = firstParam(params, ["account_id", "sub", "user_id"]);
    result.organization_id = firstParam(params, ["organization_id", "org_id"]);
    result.plan_type = firstParam(params, ["plan_type", "plan"]);
    result.token_type = firstParam(params, ["token_type"]);
    result.expires_at = firstParam(params, ["expires_at", "token_expires_at"]);
    result.scopes = firstParam(params, ["scope", "scopes"]);
    result.authorization_code = firstParam(params, ["code", "authorization_code"]);
    if (result.error) return result;
    if (result.access_token || result.refresh_token || result.id_token) return result;
    if (result.authorization_code) return result;
  }
  return null;
}

function firstParam(params: URLSearchParams, keys: string[]) {
  for (const key of keys) {
    const value = params.get(key)?.trim();
    if (value) return value;
  }
  return "";
}

function readProviderAccountOAuthResultFromLocation() {
  if (typeof window === "undefined") return null;
  const search = window.location.search.replace(/^\?/, "");
  const hash = window.location.hash.replace(/^#/, "");
  const sources = [search, hash, [search, hash].filter(Boolean).join("&")];
  for (const source of sources) {
    const result = parseProviderAccountOAuthResult(source, false);
    if (result) return result;
  }
  return null;
}

function clearProviderAccountOAuthResultFromLocation() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  let changed = false;
  for (const key of [
    "provider_account_oauth",
    "tokenhub_provider_account",
    "provider_account_oauth_session_id",
    "account_oauth_session_id",
    "session_id",
    "provider_account_oauth_state",
    "account_oauth_state",
    "provider_account_oauth_error",
    "oauth_error",
    "error",
    "account_access_token",
    "provider_access_token",
    "account_refresh_token",
    "account_id_token",
    "account_email",
    "email",
    "login",
    "username",
    "account_id",
    "sub",
    "user_id",
    "organization_id",
    "org_id",
    "plan_type",
    "plan",
    "authorization_code",
    "code",
  ]) {
    if (url.searchParams.has(key)) {
      url.searchParams.delete(key);
      changed = true;
    }
  }
  if (url.hash) {
    const hashParams = new URLSearchParams(url.hash.replace(/^#/, ""));
    let hashChanged = false;
    for (const key of [
      "provider_account_oauth",
      "tokenhub_provider_account",
      "provider_account_oauth_session_id",
      "account_oauth_session_id",
      "session_id",
      "provider_account_oauth_state",
      "account_oauth_state",
      "provider_account_oauth_error",
      "oauth_error",
      "error",
      "access_token",
      "refresh_token",
      "id_token",
      "account_access_token",
      "account_refresh_token",
      "account_id_token",
      "account_email",
      "account_id",
      "email",
      "login",
      "username",
      "sub",
      "user_id",
      "organization_id",
      "org_id",
      "plan_type",
      "plan",
      "code",
      "authorization_code",
    ]) {
      if (hashParams.has(key)) {
        hashParams.delete(key);
        hashChanged = true;
      }
    }
    if (hashChanged) {
      const nextHash = hashParams.toString();
      url.hash = nextHash ? `#${nextHash}` : "";
      changed = true;
    }
  }
  if (changed) {
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  }
}

function savePendingProviderAccountOAuthResult(result: ProviderAccountOAuthResult) {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(providerAccountOAuthStorageKey, JSON.stringify(result));
}

function savePendingProviderAccountOAuthSession(result: Pick<ProviderAccountOAuthResult, "session_id" | "state">) {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(providerAccountOAuthSessionStorageKey, JSON.stringify(result));
}

function readPendingProviderAccountOAuthSession() {
  if (typeof window === "undefined") return null;
  const raw = window.sessionStorage.getItem(providerAccountOAuthSessionStorageKey);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as Pick<ProviderAccountOAuthResult, "session_id" | "state">;
  } catch {
    return null;
  }
}

function clearPendingProviderAccountOAuthSession() {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(providerAccountOAuthSessionStorageKey);
}

function hasPendingProviderAccountOAuthResult() {
  if (typeof window === "undefined") return false;
  return Boolean(window.sessionStorage.getItem(providerAccountOAuthStorageKey));
}

function consumePendingProviderAccountOAuthResult() {
  if (typeof window === "undefined") return null;
  const raw = window.sessionStorage.getItem(providerAccountOAuthStorageKey);
  if (!raw) return null;
  window.sessionStorage.removeItem(providerAccountOAuthStorageKey);
  try {
    return JSON.parse(raw) as ProviderAccountOAuthResult;
  } catch {
    return null;
  }
}

function clearOAuthLoginResult() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  let changed = false;
  for (const key of ["oauth_token", "oauth_expires_at", "oauth_error"]) {
    if (url.searchParams.has(key)) {
      url.searchParams.delete(key);
      changed = true;
    }
  }
  if (url.hash) {
    const hashParams = new URLSearchParams(url.hash.replace(/^#/, ""));
    let hashChanged = false;
    for (const key of ["oauth_token", "oauth_expires_at", "oauth_error"]) {
      if (hashParams.has(key)) {
        hashParams.delete(key);
        hashChanged = true;
      }
    }
    if (hashChanged) {
      const nextHash = hashParams.toString();
      url.hash = nextHash ? `#${nextHash}` : "";
      changed = true;
    }
  }
  if (changed) {
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  }
}

function isOAuthAuthorizationResponse() {
  if (typeof window === "undefined") return false;
  const params = new URLSearchParams(window.location.search.replace(/^\?/, ""));
  return Boolean(params.get("state") && (params.get("code") || params.get("error")));
}

function isProviderAccountOAuthAuthorizationResponse() {
  if (typeof window === "undefined") return false;
  const params = new URLSearchParams(window.location.search.replace(/^\?/, ""));
  return params.get("provider_account_oauth") === "1" || params.has("provider_account_oauth_session_id");
}

function forwardOAuthAuthorizationResponse(baseURL: string) {
  if (isProviderAccountOAuthAuthorizationResponse()) return false;
  if (typeof window === "undefined" || !isOAuthAuthorizationResponse()) return false;
  const target = new URL(`${baseURL.replace(/\/$/, "")}/api/admin/auth/oauth/callback`);
  target.search = window.location.search;
  window.location.replace(target.toString());
  return true;
}

function readPendingOAuthBaseURL() {
  if (typeof window === "undefined") return null;
  return window.sessionStorage.getItem(oauthBaseURLStorageKey);
}

function savePendingOAuthBaseURL(baseURL: string) {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(oauthBaseURLStorageKey, baseURL);
}

function clearPendingOAuthBaseURL() {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(oauthBaseURLStorageKey);
}

function readSavedSession(): SavedSession | null {
  if (typeof window === "undefined") return null;
  const raw = window.localStorage.getItem(sessionStorageKey);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as SavedSession;
    if (!parsed.token || !parsed.user || new Date(parsed.expiresAt).getTime() <= Date.now()) {
      window.localStorage.removeItem(sessionStorageKey);
      return null;
    }
    return parsed;
  } catch {
    window.localStorage.removeItem(sessionStorageKey);
    return null;
  }
}

function saveSession(session: SavedSession) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(sessionStorageKey, JSON.stringify(session));
}

function clearSavedSession() {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(sessionStorageKey);
}
