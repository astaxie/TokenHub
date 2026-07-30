import { Activity } from "lucide-react";

export type Summary = {
  request_count: number;
  input_tokens: number;
  cached_input_tokens?: number;
  cache_write_input_tokens?: number;
  output_tokens: number;
  reasoning_output_tokens?: number;
  total_tokens: number;
  estimated_cost_usd: number;
  errors: number;
  usage_record_count?: number;
  api_key_count?: number;
  route_count?: number;
  active_route_count?: number;
  user_count?: number;
};

export const DEFAULT_PROJECT_ID = "prj_default";

export type ProjectTeam = {
  project_id: string;
  team_id: string;
  role: "team_leader" | "viewer" | "developer" | "maintainer";
  is_primary?: boolean;
  created_at?: string;
  updated_at?: string;
};

export type Project = {
  id: string;
  name: string;
  team_id?: string;
  teams?: ProjectTeam[];
  owner_user_id?: string;
  cost_center?: string;
  status: string;
  default_quota_ref?: string;
  created_at?: string;
};

export type APIKey = {
  id: string;
  project_id: string;
  owner_user_id?: string;
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
  metadata?: Record<string, string>;
};

export type DatabaseStatus = {
  database_type: "sqlite" | "postgres";
  is_docker: boolean;
  connection_ok: boolean;
  postgres_version?: string;
  database_url?: string;
};

export type Provider = {
  id: string;
  name: string;
  type: string;
  base_url?: string;
  status: string;
  healthy: boolean;
  priority: number;
  options?: Record<string, string>;
};

export type AdapterDescriptor = {
  type: string;
  capabilities: string[];
};

export type ProviderMonitoringSignal = {
  state: "healthy" | "degraded" | "down" | "unknown";
  source: "configuration" | "active_probe" | "gateway_request" | string;
  detail?: string;
  samples: number;
  success_rate?: number;
  latency_ms?: number;
  error_code?: string;
  observed_at?: string;
};

export type OpenAIQuotaWindow = {
  used_percent: number;
  reset_after_seconds: number;
  reset_at: number;
};

export type OpenAIAccountQuota = {
  plan_type?: string;
  fetched_at?: number;
  rate_limit?: {
    allowed: boolean;
    limit_reached: boolean;
    primary_window?: OpenAIQuotaWindow;
    secondary_window?: OpenAIQuotaWindow;
  };
};

export type ProviderQuotaSummary = {
  supported: boolean;
  remaining_percent?: number;
  limit_reached: boolean;
  plan_type?: string;
  earliest_reset_at?: number;
  fetched_at?: number;
  successful_accounts: number;
  failed_accounts: number;
  accounts?: Array<{
    resource_id: string;
    resource_name: string;
    quota?: OpenAIAccountQuota;
    error_code?: string;
  }>;
};

export type ProviderMonitoringSnapshot = {
  provider: Provider;
  adapter: AdapterDescriptor;
  route_count: number;
  active_route_count: number;
  resource_count: number;
  active_resource_count: number;
  healthy_resource_count: number;
  state: "healthy" | "degraded" | "down" | "unknown";
  status_label: string;
  status_detail: string;
  configuration: ProviderMonitoringSignal;
  resources: ProviderMonitoringSignal;
  active_probe: ProviderMonitoringSignal;
  gateway: ProviderMonitoringSignal;
  quota: ProviderQuotaSummary;
  quality_score: number;
  trend: Array<"success" | "warning" | "failure" | "none">;
};

export type ProviderCatalogModel = {
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
  cache_read_price_usd_per_1m?: number;
  output_price_usd_per_1m?: number;
  input_modalities?: string[];
  output_modalities?: string[];
  capabilities?: string[];
  supported_parameters?: string[];
  last_updated?: string;
  metadata?: Record<string, string>;
};

export type ProviderCatalogEntry = {
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

export type ProviderModel = {
  id: string;
  provider_id: string;
  upstream_model: string;
  display_name?: string;
  canonical_name?: string;
  category?: string;
  family?: string;
  modality?: string;
  context_window?: number;
  input_price_usd_per_1m?: number;
  cache_read_price_usd_per_1m?: number;
  output_price_usd_per_1m?: number;
  input_modalities?: string[];
  output_modalities?: string[];
  capabilities?: string[];
  supported_parameters?: string[];
  metadata?: Record<string, string>;
  source?: string;
  status: string;
  last_seen_at?: string;
  created_at?: string;
  updated_at?: string;
};

export type ProviderResource = {
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

export type Model = {
  id: string;
  name: string;
  category?: string;
  family: string;
  modality: string;
  context_window?: number;
  status: string;
  input_price_usd_per_1m?: number;
  cache_read_price_usd_per_1m?: number;
  output_price_usd_per_1m?: number;
  embedding_price_usd_per_1m?: number;
  input_modalities?: string[];
  output_modalities?: string[];
  capabilities?: string[];
  supported_parameters?: string[];
  metadata?: Record<string, string>;
};

export type ModelRoute = {
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
  project_scope?: "all" | "include" | "exclude";
  project_ids?: string[];
  last_used_at?: string;
};

export type ModelRouteStrategy = "priority_weighted" | "adaptive" | "quality" | "cost" | "priority_only" | "balanced";

export type ModelRoutePolicyRoute = {
  route_id: string;
  weight: number;
  quality_score: number;
  cost_score: number;
};

export type ModelRoutePolicy = {
  strategy: ModelRouteStrategy;
  routes: ModelRoutePolicyRoute[];
};

export type ChatRole = "system" | "user" | "assistant";

export type PlaygroundMessage = {
  id: string;
  role: ChatRole;
  content: string;
};

export type PlaygroundRouteSummary = {
  route_id?: string;
  provider_id?: string;
  provider_name?: string;
  resource_id?: string;
  resource_name?: string;
  provider_model?: string;
  upstream_request_id?: string;
  served_model?: string;
  model_etag?: string;
  transport?: string;
  priority?: number;
  resource_priority?: number;
  weight?: number;
  quality_score?: number;
  cost_score?: number;
  strategy?: string;
};

export type PlaygroundUsage = {
  prompt_tokens?: number;
  cached_input_tokens?: number;
  completion_tokens?: number;
  total_tokens?: number;
  estimated_cost_usd?: number;
};

export type PlaygroundRouteAttempt = {
  route: PlaygroundRouteSummary;
  status: number;
  code?: string;
  error?: string;
};

export type PlaygroundChatPayload = {
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

export type ApiExampleLanguage = "python" | "typescript" | "java" | "go";

export type AdminResource = {
  id: string;
  kind: string;
  name: string;
  description?: string;
  status: string;
  fields?: Record<string, unknown>;
  created_at?: string;
  updated_at?: string;
};

export type AdminUser = {
  id: string;
  username: string;
  name: string;
  email: string;
  role: string;
  team_id?: string;
  team_ids?: string[];
  status: string;
  created_at?: string;
  updated_at?: string;
  last_login_at?: string;
};

export type LoginIdentityProvider = {
  id: string;
  name: string;
  display_name?: string;
  provider_type: string;
  issuer_url?: string;
  icon_key?: string;
};

export type AlertEvent = {
  id: string;
  severity: string;
  code: string;
  message: string;
  scope_type?: string;
  scope_id?: string;
  created_at: string;
};

export type AlertDelivery = {
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

export type ReportExportHistoryItem = {
  id: string;
  dataset: string;
  file_name: string;
  exported_at: string;
  period?: string;
};

export type ApprovalRequest = {
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

export type SQLiteBackup = {
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

export type RequestLog = {
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

export type UsageRecord = {
  id: string;
  request_id: string;
  project_id: string;
  api_key_id: string;
  attributed_user_id?: string;
  model: string;
  provider_id?: string;
  provider_resource_id?: string;
  input_tokens: number;
  cached_input_tokens?: number;
  cache_write_input_tokens?: number;
  output_tokens: number;
  reasoning_output_tokens?: number;
  total_tokens: number;
  estimated_cost_usd: number;
  created_at: string;
};

export type RouteAttemptLog = {
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
  invoked: boolean;
  latency_ms?: number;
  created_at: string;
};

export type RequestPayloadLog = {
  id: string;
  request_id: string;
  request_body?: string;
  response_body?: string;
  request_truncated: boolean;
  response_truncated: boolean;
  created_at: string;
};

export type RequestDetail = {
  log: RequestLog;
  usage: UsageRecord[];
  attempts: RouteAttemptLog[];
  payload?: RequestPayloadLog | null;
};

export type AuditEvent = {
  id: string;
  actor_user_id?: string;
  actor_name?: string;
  actor_role?: string;
  action: string;
  resource_type: string;
  resource_id: string;
  status: string;
  message?: string;
  before_snapshot?: string;
  after_snapshot?: string;
  ip?: string;
  user_agent?: string;
  created_at: string;
};

export type UsageBreakdownRow = {
  id: string;
  request_count: number;
  input_tokens: number;
  cached_input_tokens?: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
  owned_key_count?: number;
  used_key_count?: number;
};

export type UsageBreakdown = {
  projects: UsageBreakdownRow[];
  models: UsageBreakdownRow[];
  members: UsageBreakdownRow[];
  providers: UsageBreakdownRow[];
  provider_resources: UsageBreakdownRow[];
  cost_centers: UsageBreakdownRow[];
};

export type UsagePoint = {
  date: string;
  request_count: number;
  input_tokens: number;
  cached_input_tokens?: number;
  output_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
};

export type ViewKey =
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
  | "database-status"
  | "announcements"
  | "identity-providers"
  | "settings";

export const viewRoutes: Record<ViewKey, string> = {
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
  "database-status": "/database-status",
  announcements: "/announcements",
  "identity-providers": "/identity-providers",
  settings: "/settings",
};

export const routeViews = Object.fromEntries(
  Object.entries(viewRoutes).map(([view, route]) => [route.replace(/^\//, ""), view]),
) as Record<string, ViewKey>;

export const notificationChannelTypes = ["webhook", "slack", "discord", "telegram", "whatsapp", "feishu", "dingtalk", "wecom", "email"];

export type NavLeafItem = {
  view: ViewKey;
  label: string;
  icon: typeof Activity;
};

export type NavParentItem = {
  label: string;
  icon: typeof Activity;
  children: NavLeafItem[];
};

export type NavItem = NavLeafItem | NavParentItem;

export type AppRole = "admin" | "security" | "team_leader" | "user";

export type TopSearchItem = {
  id: string;
  view: ViewKey;
  label: string;
  group: string;
  description: string;
  icon: typeof Activity;
  tone?: "page" | "entity" | "recent";
  keywords: string;
};

export type FieldType = "text" | "number" | "password" | "textarea" | "select" | "multi-select" | "tags" | "boolean";

export type FieldConfig = {
  key: string;
  label: string;
  type?: FieldType;
  options?: string[];
  optionsFromData?: (data: AppData, currentUser?: AdminUser | null, values?: Record<string, string>) => Array<{ value: string; label: string }>;
  placeholder?: string;
  autoComplete?: string;
  required?: boolean;
  help?: string;
  readOnlyOnEdit?: boolean;
  multiSelectOnEdit?: boolean;
  visible?: (values: Record<string, string>) => boolean;
};

export type ProviderCredentialMode = "provider_api_key" | "account_integration";

export type ColumnConfig<T> = {
  key: string;
  label: string;
  render?: (item: T, ctx: AppData) => React.ReactNode;
};

export type ResourceConfig<T> = {
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
  canRemove?: (item: T, currentUser: AdminUser | null) => boolean;
  actions?: ResourceAction<T>[];
  toolbarActions?: ToolbarAction[];
  toForm?: (item: T) => Record<string, string>;
};

export type ResourceAction<T> = {
  label: string;
  title?: string;
  visible?: (item: T) => boolean;
  run?: (ctx: ApiContext, item: T) => Promise<void>;
  modal?: (item: T, data: AppData) => ModalState<any>;
  doneMessage?: (item: T) => string;
};

export type ToolbarAction = {
  label: string;
  title?: string;
  kind?: "import-users";
  run?: (ctx: ApiContext, items?: unknown[]) => Promise<void>;
  doneMessage?: () => string;
};

export type UserImportResult = {
  created?: number;
  updated?: number;
  skipped?: number;
  errors?: string[];
};

export type AppData = {
  summary: Summary;
  projects: Project[];
  keys: APIKey[];
  providers: Provider[];
  providerResources: ProviderResource[];
  providerModels: ProviderModel[];
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
  providerMonitoring: ProviderMonitoringSnapshot[];
};

export type ApiContext = {
  baseURL: string;
  adminToken: string;
};

export type ModalState<T> = {
  config: ResourceConfig<T>;
  item?: T;
  initialValues?: Record<string, string>;
};

export type ConfirmState<T> = {
  config: ResourceConfig<T>;
  item?: T;
  items?: T[];
};

export type SettingsTabKey = "settings" | "role-configs" | "identity-providers";

export const sessionStorageKey = "tokenhub.admin.session";

export const oauthBaseURLStorageKey = "tokenhub.admin.oauth.base_url";

export const authExpiredEventName = "tokenhub-admin-auth-expired";

export const languageStorageKey = "tokenhub.admin.language";

export const recentViewsStorageKey = "tokenhub.admin.recent.views.v1";
