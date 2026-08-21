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
  model_access_mode?: "inherit" | "restricted";
  allowed_models?: string[];
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
  model_access_mode?: "inherit" | "restricted";
  ip_allowlist?: string[];
  status: string;
  limits?: Record<string, number>;
  rate_limit_rpm?: number;
  token_limit_tpm?: number;
  expires_at?: string;
  rotated_from_id?: string;
  grace_until?: string;
  created_at?: string;
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

// Read-only database evolution state served by /api/admin/system/schema-status.
// reason_code is the stable machine identifier for localized rendering;
// reason is the English diagnostic for logs and API clients, never localized UI.
export type SchemaEvolutionStatus = {
  ready: boolean;
  reason?: string;
  reason_code?: string;
  schema_version: number;
  dirty_version?: number;
  pending_expand?: Array<{ version: number; name: string }>;
  pending_contract?: Array<{ version: number; name: string }>;
  blocking_backfills_pending?: string[];
  backfills?: Array<{ id: string; mode: string; state: string; remaining: number }>;
  instances?: Array<{ instance_id: string; release: string; last_seen: string }>;
  compatibility?: {
    target: number;
    min_compatible: number;
    max_compatible: number;
  };
};

export type Provider = {
  id: string;
  name: string;
  type: string;
  base_url?: string;
  status: string;
  healthy: boolean;
  priority: number;
  headers?: Record<string, string>;
  sensitive_headers?: string[];
  header_validation_errors?: string[];
  options?: Record<string, string>;
};

export type BillingConnector = {
	id: string;
	name: string;
	type: "aliyun" | "newapi" | "oneapi";
	base_url: string;
	status: string;
	schedule_interval_minutes: number;
	config?: Record<string, string>;
	credentials_configured: boolean;
	last_synced_through?: string;
	last_sync_status?: string;
	last_sync_message?: string;
	last_sync_at?: string;
	next_sync_at?: string;
	created_at: string;
	updated_at: string;
};

export type BillingRecord = {
	id: string;
	connector_id: string;
	external_id: string;
	source_type: string;
	account_id?: string;
	service?: string;
	product?: string;
	model?: string;
	currency: string;
	gross_amount: string;
	discount_amount: string;
	tax_amount: string;
	refund_amount: string;
	net_amount: string;
	usage_quantity?: number;
	usage_unit?: string;
	usage_start_at: string;
	usage_end_at: string;
	source_timezone: string;
	billing_period: string;
	external_request_id?: string;
	raw_snapshot_id: string;
};

export type BillingSyncRun = {
	id: string;
	connector_id: string;
	trigger: string;
	status: string;
	range_start: string;
	range_end: string;
	pages_fetched: number;
	attempts: number;
	records_seen: number;
	records_inserted: number;
	records_updated: number;
	error_code?: string;
	error_message?: string;
	started_at: string;
	finished_at?: string;
};

export type ReconciliationRule = {
	id: string;
	name: string;
	connector_id: string;
	connector_type: string;
	provider_id: string;
	status: string;
	granularity: "detail" | "hour" | "day" | "month";
	match_dimensions: string[];
	dimension_mappings?: Record<string, Record<string, string>>;
	amount_tolerance: string;
	ratio_tolerance: string;
	usd_exchange_rate: string;
	time_window_minutes: number;
	billing_delay_minutes: number;
	schedule_interval_minutes: number;
	timezone: string;
	currency?: string;
	version: number;
	rule_hash: string;
	last_run_at?: string;
	next_run_at?: string;
	created_at: string;
	updated_at: string;
};

export type ReconciliationRun = {
	id: string;
	rule_id: string;
	connector_id: string;
	connector_type: string;
	provider_id: string;
	trigger: string;
	status: string;
	period_start: string;
	period_end: string;
	granularity: string;
	match_dimensions: string[];
	dimension_mappings?: Record<string, Record<string, string>>;
	amount_tolerance: string;
	ratio_tolerance: string;
	usd_exchange_rate: string;
	time_window_minutes: number;
	billing_delay_minutes: number;
	timezone: string;
	currency?: string;
	rule_version: number;
	rule_hash: string;
	input_hash: string;
	provider_record_count: number;
	tokenhub_record_count: number;
	matched_count: number;
	provider_only_count: number;
	tokenhub_only_count: number;
	amount_mismatch_count: number;
	provider_amount: string;
	tokenhub_amount: string;
	difference_amount: string;
	created_by: string;
	started_at: string;
	finished_at?: string;
	locked_at?: string;
	locked_by?: string;
};

export type ReconciliationItem = {
	id: string;
	run_id: string;
	match_key: string;
	status: "matched" | "provider_only" | "tokenhub_only" | "amount_mismatch";
	bucket_start: string;
	bucket_end: string;
	request_id?: string;
	provider?: string;
	resource_account?: string;
	model?: string;
	project?: string;
	currency: string;
	provider_amount: string;
	tokenhub_amount: string;
	difference_amount: string;
	difference_ratio: string;
	possible_reason: string;
	provider_record_ids: string[];
	tokenhub_record_ids: string[];
};

export type ReconciliationDetail = {
	run: ReconciliationRun;
	items: ReconciliationItem[];
	total: number;
	limit: number;
	offset: number;
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
  headers?: Record<string, string>;
  sensitive_headers?: string[];
  header_validation_errors?: string[];
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
  tags?: string[];
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
  cache_write_input_tokens?: number;
  completion_tokens?: number;
  reasoning_output_tokens?: number;
  total_tokens?: number;
  estimated_cost_usd?: number;
  upstream_request_id?: string;
  served_model?: string;
  model_etag?: string;
  transport?: string;
};

export type PlaygroundRouteAttempt = {
  route?: PlaygroundRouteSummary;
  status: number;
  upstream_status?: number;
  code?: string;
  error?: string;
  invoked?: boolean;
  latency_ms?: number;
  usage?: PlaygroundUsage;
  started_at?: string;
  ended_at?: string;
};

export type PlaygroundTiming = {
  mode: "stream" | "buffered";
  started_at: string;
  first_token_at?: string;
  last_token_at?: string;
  completed_at: string;
  ttft_ms?: number;
  generation_ms?: number;
  total_ms: number;
  output_tokens_per_second?: number;
  end_to_end_tokens_per_second?: number;
};

export type PlaygroundChatPayload = {
  type?: "completed" | "failed" | "cancelled";
  status?: "completed" | "failed" | "cancelled";
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
  timing?: PlaygroundTiming;
  request_id?: string;
  code?: string;
  error?: string;
};

export type PlaygroundStreamEvent =
  | {
    type: "started";
    request_id: string;
    model: string;
    started_at: string;
  }
  | {
    type: "delta";
    request_id: string;
    mode: "stream" | "buffered";
    delta: string;
    received_at: string;
  }
  | PlaygroundChatPayload;

export type ApiExampleLanguage = "python" | "typescript" | "java" | "go";

export type AdminResource = {
  id: string;
  kind: string;
  name: string;
  description?: string;
  status: string;
  fields?: Record<string, unknown>;
  current_usage?: {
    daily: { requests: number; total_tokens: number; cost_usd: number };
    monthly: { requests: number; total_tokens: number; cost_usd: number };
  };
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
  routing_policy_id?: string;
  routing_policy_scope?: string;
  routing_policy_priority?: number;
  status_code: number;
  error_code?: string;
  latency_ms: number;
  client_ip?: string;
  user_agent?: string;
  created_at: string;
  input_tokens?: number;
  cached_input_tokens?: number;
  cache_write_input_tokens?: number;
  input_audio_tokens?: number;
  output_tokens?: number;
  reasoning_output_tokens?: number;
  output_audio_tokens?: number;
  accepted_prediction_tokens?: number;
  rejected_prediction_tokens?: number;
  total_tokens?: number;
  estimated_cost_usd?: number;
  provider_cost_usd?: number;
  usage_record_count?: number;
};

export type RequestLogPagination = {
  page: number;
  page_size: number;
  total: number;
  total_pages: number;
};

export type RequestLogSummary = {
  all: number;
  ok: number;
  error: number;
  average_latency_ms: number;
};

export type RequestLogPage = {
  data: RequestLog[];
  pagination: RequestLogPagination;
  summary: RequestLogSummary;
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
  input_audio_tokens?: number;
  output_tokens: number;
  reasoning_output_tokens?: number;
  output_audio_tokens?: number;
  accepted_prediction_tokens?: number;
  rejected_prediction_tokens?: number;
  total_tokens: number;
  estimated_cost_usd: number;
  provider_cost_usd?: number;
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
  api_keys?: UsageBreakdownRow[];
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

export type UsageDaily = {
  timezone: string;
  date: string;
  window_start: string;
  window_end: string;
  summary: Summary;
  breakdown: UsageBreakdown;
};

export type APIKeyUsageMetrics = {
  request_count: number;
  error_count: number;
  average_latency_ms: number;
  input_tokens: number;
  cached_input_tokens: number;
  cache_write_input_tokens: number;
  input_audio_tokens: number;
  output_tokens: number;
  reasoning_output_tokens: number;
  output_audio_tokens: number;
  accepted_prediction_tokens: number;
  rejected_prediction_tokens: number;
  total_tokens: number;
  estimated_cost_usd: number;
};

export type APIKeyUsagePoint = APIKeyUsageMetrics & { date: string };

export type APIKeyUsageBreakdownRow = APIKeyUsageMetrics & {
  id: string;
  resource_id?: string;
  status_code?: number;
  error_code?: string;
  last_occurred_at?: string;
};

export type APIKeyQuotaLimits = {
  rate_limit_rpm: number;
  token_limit_tpm: number;
  daily_requests: number;
  monthly_requests: number;
  daily_tokens: number;
  monthly_tokens: number;
  daily_cost_usd: number;
  monthly_cost_usd: number;
  max_concurrency: number;
};

export type APIKeyQuotaCounter = {
  requests: number;
  prompt_tokens: number;
  completion_tokens: number;
  total_tokens: number;
  cost_usd: number;
};

export type APIKeyUsageResponse = {
  key: APIKey;
  range: { from: string; to: string };
  generated_at: string;
  summary: APIKeyUsageMetrics;
  quota: {
    effective_limits: APIKeyQuotaLimits;
    day: { bucket: string; usage: APIKeyQuotaCounter };
    month: { bucket: string; usage: APIKeyQuotaCounter };
  };
  timeseries: APIKeyUsagePoint[];
  models: APIKeyUsageBreakdownRow[];
  errors: APIKeyUsageBreakdownRow[];
  providers?: APIKeyUsageBreakdownRow[];
};

export type ViewKey =
  | "overview"
  | "playground"
  | "gateway"
  | "providers"
  | "models"
  | "routes"
  | "routing-policies"
  | "projects"
  | "project-members"
  | "api-keys"
  | "teams"
  | "users"
  | "quota-policies"
  | "cost-centers"
  | "approval-flows"
  | "approvals"
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
  "routing-policies": "/routing-policies",
  projects: "/projects",
  "project-members": "/project-members",
  "api-keys": "/api-keys",
  teams: "/teams",
  users: "/users",
  "quota-policies": "/quota-policies",
  "cost-centers": "/cost-centers",
  "approval-flows": "/approval-flows",
  approvals: "/approvals",
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

export type FieldType = "text" | "number" | "password" | "textarea" | "select" | "multi-select" | "tag-select" | "tags" | "boolean";

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
  createOnly?: boolean;
  emptyOptionsText?: string;
  emptySelectionText?: string;
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
  href?: (item: T) => string;
  navigate?: (item: T) => ViewKey;
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
  dailyUsage: UsageDaily;
  timeseries: UsagePoint[];
  resources: Record<string, AdminResource[]>;
  providerCatalog: ProviderCatalogEntry[];
  providerMonitoring: ProviderMonitoringSnapshot[];
	billingConnectors: BillingConnector[];
	billingRecords: BillingRecord[];
	billingSyncRuns: BillingSyncRun[];
	reconciliationRules: ReconciliationRule[];
	reconciliationRuns: ReconciliationRun[];
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
