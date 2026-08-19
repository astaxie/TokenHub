import { appRole } from "../core/navigation";
import { clearSavedSession } from "../core/session";
import { type AdminResource, type AdminUser, type ApiContext, type APIKey, type AppData, type ApprovalRequest, authExpiredEventName, type FieldConfig, type Project, type ProviderCatalogModel, type ProviderResource, type ResourceConfig, type UserImportResult } from "../core/types";
import { inferModelCategoryText, normalizeNotificationChannelType, notificationChannelDescription, notificationChannelLabel, notificationChannelURLPlaceholder } from "../domain/catalog";
import { applyCodexFingerprintOption, normalizeCodexFingerprintMode } from "../domain/codex-fingerprint";
import { firstActiveModel, firstActiveProject, firstActiveProvider, firstActiveTeam, firstActiveUser, firstCostCenterCode, firstIssueableProject, projectMemberProjectSelectOptions, stringifyValue } from "../domain/entities";
import { compactNumber } from "../domain/formatting";
import { enumValueLabel, numberFromUnknown, numberOr, parseLooseValue, splitList } from "../domain/labels";
import { defaultProviderClaudeCodeAttributionPolicy } from "../domain/provider-attribution";
import { providerAnthropicAuthType } from "../domain/provider-custom-upstream";
import { initialModelRoutes } from "../domain/provider-model-selection";
import { modelMetadataPayload } from "../domain/model-display-name";
import { defaultDisplayName } from "../domain/form-defaults";
import { providerReasoningOptions, providerReasoningOverrideFormValues } from "../domain/provider-reasoning";
import { providerHeadersFormValue, providerHeadersPayload } from "../domain/provider-headers";
import { activeLanguage, tx } from "../i18n/runtime";
import { handleApprovalOrJSON } from "./governance-config";
import { projectQuotaFields, type ProjectQuotaValues } from "../domain/project-quota";

export function providerPayload(values: Record<string, string>) {
  return {
    id: values.id,
    name: values.name,
    type: values.type,
    base_url: values.base_url,
    api_key: values.api_key,
    clear_api_key: values.clear_api_key === "true",
    status: values.status || "active",
    healthy: values.healthy !== "false",
    priority: numberOr(values.priority, 10),
    ...providerHeadersPayload(values.custom_headers),
    anthropic_auth_type: providerAnthropicAuthType(values),
    claude_code_attribution_policy: values.claude_code_attribution_policy || defaultProviderClaudeCodeAttributionPolicy(values.type, values.catalog_id),
    catalog_id: values.catalog_id,
    model_category: values.model_category,
    options: providerReasoningOptions(values),
    selected_models: splitList(values.selected_models),
    custom_models: parseProviderCatalogModels(values.custom_models),
  };
}

function parseProviderCatalogModels(value?: string): ProviderCatalogModel[] {
  if (!value?.trim()) return [];
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? parsed as ProviderCatalogModel[] : [];
  } catch {
    return [];
  }
}

export function providerUpdatePayload(values: Record<string, string>) {
  const payload = providerPayload(values) as Record<string, unknown>;
  if (!values.api_key?.trim()) {
    delete payload.api_key;
  }
  return payload;
}

export function providerResourcePayload(values: Record<string, string>) {
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
    region: values.region,
    environment: values.environment,
    status: values.status || "active",
    healthy: values.healthy !== "false",
    priority: numberOr(values.priority, 1),
    weight: numberOr(values.weight, 100),
    rate_limit_rpm: numberOr(values.rate_limit_rpm, 0),
    token_limit_tpm: numberOr(values.token_limit_tpm, 0),
    max_concurrency: numberOr(values.max_concurrency, 0),
    ...providerHeadersPayload(values.custom_headers),
    credentials,
    options: providerResourceOptions(values),
  };
}

export function providerResourceUpdatePayload(values: Record<string, string>) {
  const payload = providerResourcePayload(values) as Record<string, unknown>;
  const isOpenAIAccount = values.resource_type === "openai_subscription";
  if (isOpenAIAccount && !values.access_token?.trim()) delete payload.api_key;
  if (!isOpenAIAccount && !values.api_key?.trim()) delete payload.api_key;
  if (isOpenAIAccount && !values.access_token?.trim() && !values.refresh_token?.trim() && !values.id_token?.trim()) {
    delete payload.credentials;
  }
  return payload;
}

export function providerResourceOptions(values: Record<string, string>) {
  const accountOptions: Record<string, string> = values.resource_type === "openai_subscription" ? {
    credential_source: "openai_subscription",
    auth_type: values.auth_type || "oauth",
    account_email: values.account_email,
    account_id: values.account_id,
    organization_id: values.organization_id,
    plan_type: values.plan_type,
    token_expires_at: values.expires_at,
    scopes: values.scopes,
  } : {};
  const options = providerReasoningOptions(values, accountOptions);
  if (values.claude_code_attribution_policy === "preserve" || values.claude_code_attribution_policy === "strip") {
    options.claude_code_attribution_policy = values.claude_code_attribution_policy;
  } else {
    delete options.claude_code_attribution_policy;
  }
  return applyCodexFingerprintOption(options, values);
}

export function providerResourceToForm(item: ProviderResource, providerOptions?: Record<string, string>) {
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
    codex_fingerprint_mode: normalizeCodexFingerprintMode(item.options?.codex_fingerprint_mode),
    claude_code_attribution_policy: item.options?.claude_code_attribution_policy ?? "inherit",
    region: item.region ?? "",
    environment: item.environment ?? "",
    status: item.status,
    healthy: String(item.healthy),
    custom_headers: providerHeadersFormValue(item.headers, item.sensitive_headers),
    ...providerReasoningOverrideFormValues(item.options, providerOptions),
  };
}

export function providerResourceAttributionPolicyPayload(resource: ProviderResource, policy: string) {
  const options = { ...(resource.options ?? {}) };
  if (policy === "inherit") delete options.claude_code_attribution_policy;
  else options.claude_code_attribution_policy = policy;
  return {
    provider_id: resource.provider_id,
    name: resource.name,
    resource_type: resource.resource_type,
    base_url: resource.base_url ?? "",
    group: resource.group ?? "",
    region: resource.region ?? "",
    environment: resource.environment ?? "",
    status: resource.status,
    healthy: resource.healthy,
    priority: resource.priority,
    weight: resource.weight,
    rate_limit_rpm: resource.rate_limit_rpm ?? 0,
    token_limit_tpm: resource.token_limit_tpm ?? 0,
    max_concurrency: resource.max_concurrency ?? 0,
    options,
  };
}

export function modelPayload(values: Record<string, string>, existingMetadata?: Record<string, string>) {
  const payload = numberPayload(
    {
      name: values.name,
      family: values.family,
      modality: values.modality,
      context_window: values.context_window,
      input_price_usd_per_1m: values.input_price_usd_per_1m,
      cache_read_price_usd_per_1m: values.cache_read_price_usd_per_1m,
      output_price_usd_per_1m: values.output_price_usd_per_1m,
      embedding_price_usd_per_1m: values.embedding_price_usd_per_1m,
      status: values.status,
    },
    ["context_window", "input_price_usd_per_1m", "cache_read_price_usd_per_1m", "output_price_usd_per_1m", "embedding_price_usd_per_1m"],
  );
  if (values.modality === "embedding") {
    payload.cache_read_price_usd_per_1m = 0;
  }
  payload.category = values.category || inferModelCategoryText(values.name || values.family || "");
  payload.capabilities = splitList(values.capabilities);
  payload.supported_parameters = splitList(values.supported_parameters);
  payload.input_modalities = splitList(values.input_modalities);
  payload.output_modalities = splitList(values.output_modalities);
  Object.assign(payload, modelMetadataPayload(existingMetadata, values.display_name ?? ""));
  const routes = initialModelRoutes(values.initial_provider_models);
  if (routes.length > 0) payload.routes = routes;
  return payload;
}

export function routePayload(values: Record<string, string>) {
  const payload: Record<string, unknown> = {
    model_name: values.model_name,
    provider_id: values.provider_id,
    provider_resource_id: values.provider_resource_id,
    resource_group: values.resource_group,
    provider_model: values.provider_model,
    status: values.status,
    strategy: values.strategy,
    project_scope: values.project_scope || "all",
    project_ids: splitList(values.project_ids),
    tags: splitList(values.tags),
    sticky_session: values.sticky_session === "true",
    priority: numberOr(values.priority, 0),
    weight: numberOr(values.weight, 0),
    quality_score: numberOr(values.quality_score, 0),
    cost_score: numberOr(values.cost_score, 0),
  };
  return payload;
}

export async function createModelRoutes(ctx: ApiContext, values: Record<string, string>, data?: AppData) {
  const modelNames = splitList(values.model_name);
  if (modelNames.length === 0) {
    throw new Error("请选择模型目录中的模型");
  }
  for (const modelName of modelNames) {
    const existingRoutes = data?.routes.filter((route) => route.model_name === modelName) ?? [];
    const strategy = existingRoutes[0]?.strategy || "priority_weighted";
    const priority = strategy === "priority_only"
      ? Math.max(0, ...existingRoutes.map((route) => route.priority || 0)) + 1
      : 1;
    const routeValues = {
      ...values,
      model_name: modelName,
      provider_model: values.provider_model?.trim() || modelName,
      priority: String(priority),
      weight: values.weight || "100",
      quality_score: values.quality_score || "50",
      cost_score: values.cost_score || "50",
      strategy,
    };
    await adminMutate(ctx, "/api/admin/routing-rules", "POST", routePayload(routeValues));
  }
}

export function projectQuotaPolicy(data: AppData, project: Project) {
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

export function projectQuotaValues(quota?: AdminResource): ProjectQuotaValues {
  return {
    status: quota?.status || "active",
    rate_limit_rpm: quotaFieldValue(quota, "rate_limit_rpm"),
    token_limit_tpm: quotaFieldValue(quota, "token_limit_tpm"),
    daily_requests: quotaFieldValue(quota, "daily_requests"),
    monthly_requests: quotaFieldValue(quota, "monthly_requests"),
    daily_tokens: quotaFieldValue(quota, "daily_tokens"),
    monthly_tokens: quotaFieldValue(quota, "monthly_tokens"),
    daily_cost_usd: quotaFieldValue(quota, "daily_cost_usd"),
    monthly_cost_usd: quotaFieldValue(quota, "monthly_cost_usd"),
    max_concurrency: quotaFieldValue(quota, "max_concurrency"),
  };
}

export function quotaFieldValue(quota: AdminResource | undefined, key: string) {
  const value = quota?.fields?.[key];
  if (value === undefined || value === null || value === "" || Number(value) === 0) return "";
  return String(value);
}

export function projectQuotaPayload(project: Project, values: ProjectQuotaValues) {
  return {
    name: `${project.name || project.id} 项目额度`,
    description: "项目空间内配置的专属模型调用额度",
    status: values.status || "active",
    fields: {
      scope: "project",
      scope_id: project.id,
      rate_limit_rpm: numberOr(values.rate_limit_rpm, 0),
      token_limit_tpm: numberOr(values.token_limit_tpm, 0),
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

export async function saveProjectQuota(ctx: ApiContext, project: Project, quota: AdminResource | undefined, values: ProjectQuotaValues) {
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

export async function requestProjectQuotaIncrease(ctx: ApiContext, project: Project, quota: AdminResource | undefined, values: ProjectQuotaValues) {
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

export function projectQuotaValuesHaveLimit(values: ProjectQuotaValues) {
  return projectQuotaFields.some((field) => numberOr(values[field.key], 0) > 0);
}

export function projectQuotaSummary(data: AppData, project: Project) {
  const quota = projectQuotaPolicy(data, project);
  if (!quota) return "未配置";
  if (quota.status !== "active") return enumValueLabel(quota.status);
  const parts = [
    quotaSummaryPart(quota, "rate_limit_rpm", "RPM"),
    quotaSummaryPart(quota, "token_limit_tpm", "TPM"),
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

export function quotaSummaryPart(quota: AdminResource, key: string, label: string, prefix = "") {
  const value = numberFromUnknown(quota.fields?.[key]);
  if (!value) return "";
  return `${label} ${prefix}${compactNumber(value)}`;
}

export function projectQuotaIssue(data: AppData, project: Project) {
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

export function pendingProjectQuotaApproval(data: AppData, project: Project) {
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

export function parseApprovalPayload(payload?: string): Record<string, unknown> {
  if (!payload) return {};
  try {
    const data = JSON.parse(payload);
    return data && typeof data === "object" && !Array.isArray(data) ? data as Record<string, unknown> : {};
  } catch {
    return {};
  }
}

export function defaultFormValues<T>(config: ResourceConfig<T>, data: AppData, currentUser?: AdminUser | null) {
  const values: Record<string, string> = {};
  for (const field of config.fields) {
    if (field.key === "status") values[field.key] = "active";
    if (field.key === "healthy") values[field.key] = "true";
    if (field.key === "priority") values[field.key] = config.view === "routes" ? "" : "10";
    if (field.key === "weight") values[field.key] = "100";
    if (field.key === "quality_score") values[field.key] = "50";
    if (field.key === "cost_score") values[field.key] = "50";
    if (field.key === "strategy") values[field.key] = config.view === "routes" ? "priority_weighted" : config.view === "routing-policies" ? "inherit" : "balanced";
    if (field.key === "project_scope") values[field.key] = "all";
    if (field.key === "provider_id") values[field.key] = firstActiveProvider(data)?.id ?? "";
    if (field.key === "model_name") values[field.key] = firstActiveModel(data)?.name ?? "";
    if (field.key === "group") values[field.key] = "default";
    if (field.key === "resource_type") values[field.key] = "api_key";
    if (field.key === "environment") values[field.key] = "prod";
    if (field.key === "project_id") values[field.key] = config.view === "api-keys" ? firstIssueableProject(data, currentUser) : (firstActiveProject(data)?.id ?? "");
    if (field.key === "owner_user_id" && config.view === "api-keys") values[field.key] = currentUser && appRole(currentUser.role) !== "admin" ? currentUser.id : "";
    if (field.key === "team_id") values[field.key] = firstActiveTeam(data)?.id ?? "";
    if (field.key === "allowed_models") values[field.key] = "";
    if (field.key === "model_access_mode") values[field.key] = "inherit";
    if (field.key === "daily_requests") values[field.key] = "1000";
    if (field.key === "monthly_requests") values[field.key] = "30000";
    if (field.key === "daily_tokens") values[field.key] = config.view === "api-keys" ? "100000000" : "1000000";
    if (field.key === "monthly_tokens") values[field.key] = config.view === "api-keys" ? "2000000000" : "20000000";
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
    if (field.key === "display_name") values[field.key] = defaultDisplayName(config.view);
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

export function keyCreatePayload(values: Record<string, string>) {
  return {
    name: values.name,
    group: values.group || "default",
    owner_user_id: values.owner_user_id,
    model_access_mode: values.model_access_mode || "inherit",
    allowed_models: splitList(values.allowed_models),
    ip_allowlist: splitList(values.ip_allowlist),
    rate_limit_rpm: minuteLimitValue(values.rate_limit_rpm, false),
    token_limit_tpm: minuteLimitValue(values.token_limit_tpm, false),
    limits: keyLimits(values),
  };
}

export function keyPatchPayload(values: Record<string, string>) {
  return {
    name: values.name,
    group: values.group || "default",
    owner_user_id: values.owner_user_id,
    model_access_mode: values.model_access_mode || "inherit",
    status: values.status || "active",
    allowed_models: splitList(values.allowed_models),
    ip_allowlist: splitList(values.ip_allowlist),
    rate_limit_rpm: minuteLimitValue(values.rate_limit_rpm, true),
    token_limit_tpm: minuteLimitValue(values.token_limit_tpm, true),
    limits: keyLimits(values),
  };
}

function minuteLimitValue(value: string | undefined, clearWhenBlank: boolean) {
  if (!value?.trim()) return clearWhenBlank ? null : undefined;
  return numberOr(value, 0);
}

export function notificationChannelPayload(values: Record<string, string>, existing?: AdminResource) {
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

export function notificationChannelDefaults(type: string) {
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

export async function updateAPIKeyStatus(ctx: ApiContext, item: APIKey, status: "active" | "disabled") {
  const resp = await adminFetch(ctx, `/api/admin/api-keys/${item.id}`, {
    method: "PATCH",
    body: JSON.stringify({ status }),
  });
  if (!resp.ok) throw new Error(`update api key ${resp.status}`);
  await resp.json().catch(() => undefined);
}

export function keyLimits(values: Record<string, string>) {
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

export function userPayload(values: Record<string, string>, includePassword: boolean) {
  const payload: Record<string, unknown> = {
    username: values.username,
    name: values.name,
    email: values.email,
    role: values.role || "user",
    team_id: values.team_id,
    team_ids: Array.from(new Set([values.team_id, ...splitList(values.team_ids)].filter(Boolean))),
    status: values.status || "active",
  };
  if (includePassword || values.password) {
    payload.password = values.password;
  }
  return payload;
}

export function numberPayload(values: Record<string, string>, keys: string[]) {
  const payload: Record<string, unknown> = { ...values };
  for (const key of keys) payload[key] = numberOr(values[key], 0);
  return payload;
}

export function resourcePayload(values: Record<string, string>, customFields: FieldConfig[]) {
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

export function identityProviderPayload(values: Record<string, string>, fields: FieldConfig[], existing?: AdminResource) {
  const payload = resourcePayload(values, fields);
  if (existing && !values.client_secret) {
    payload.fields.client_secret = stringifyValue(existing.fields?.client_secret);
  }
  return payload;
}

export async function importUsersFromCSVContent(ctx: ApiContext, content: string): Promise<UserImportResult> {
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

export async function readAdminError(resp: Response, fallback: string) {
  const body = await resp.text().catch(() => "");
  if (!body) return resp.status === 403 ? permissionDeniedMessage(fallback) : `${fallback} (${resp.status})`;
  try {
    const parsed = JSON.parse(body) as { error?: { code?: string; message?: string }; message?: string };
    const localized = localizedAdminErrorCode(parsed.error?.code);
    if (localized) return localized;
    if (parsed.error?.code === "provider_resource_reauthorization_required") return tx("OpenAI/Codex 账号会话已失效，请重新进行账号授权。");
    if (parsed.error?.code === "codex_image_forbidden") return tx("所选 Codex 账号不支持生图，无法创建图片。可更换账号后重试。");
    if (parsed.error?.code === "codex_rate_limited") return tx("Codex 生图测试被限流，请稍后重试；本次结果不会标记为不支持。");
    if (["codex_upstream_unavailable", "codex_upstream_timeout", "codex_image_request_failed", "codex_image_response_failed"].includes(parsed.error?.code ?? "")) {
      return tx("Codex 生图上游暂时不可用，请稍后重试；本次结果不会标记为不支持。");
    }
    if (resp.status === 403) return permissionDeniedMessage(fallback);
    return parsed.error?.message || parsed.message || `${fallback} (${resp.status})`;
  } catch {
    return body.length > 180 ? `${body.slice(0, 180)}...` : body;
  }
}

function localizedAdminErrorCode(code?: string) {
  switch (code) {
    case "provider_synthetic_dns_cidrs_required":
      return tx("开启时至少填写一个 Synthetic DNS CIDR。");
    case "provider_synthetic_dns_cidrs_invalid":
      return tx("Synthetic DNS CIDR 格式无效，请每行填写一个 CIDR。");
    case "provider_synthetic_dns_cidrs_too_broad":
      return tx("Synthetic DNS CIDR 范围过宽，请缩小后重试。");
    case "provider_synthetic_dns_cidrs_not_allowed":
      return tx("该 Synthetic DNS CIDR 与受保护地址范围重叠，无法保存。");
    case "provider_synthetic_dns_private_cidr_requires_unsafe_mode":
      return tx("该 Synthetic DNS CIDR 属于私网或 ULA；如确认代理使用此网段，请先开启高风险私网信任。");
    default:
      return "";
  }
}

export async function adminMutate(ctx: ApiContext, path: string, method: "POST" | "PATCH", payload: unknown) {
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

export async function testProviderAvailability(ctx: ApiContext, provider: { id: string }) {
  const resourcesResp = await adminFetch(ctx, "/api/admin/provider-resources");
  if (!resourcesResp.ok) throw new Error(await readAdminError(resourcesResp, tx("读取 Provider 账号资源")));
  const payload = (await resourcesResp.json()) as { data?: ProviderResource[] };
  const subscription = (payload.data ?? []).find((resource) =>
    resource.provider_id === provider.id && resource.resource_type === "openai_subscription" && resource.status === "active",
  );
  if (!subscription) {
    await adminMutate(ctx, `/api/admin/providers/${provider.id}/test`, "POST", {});
    return;
  }
  const testResp = await adminFetch(ctx, `/api/admin/provider-resources/${subscription.id}/test`, {
    method: "POST",
    body: JSON.stringify({
      model: "gpt-5.6-luna",
      reasoning_effort: "medium",
      speed: "standard",
      prompt: "请用一句话确认 Codex 连接正常。",
    }),
  });
  if (!testResp.ok) throw new Error(await readAdminError(testResp, tx("Codex Luna 中等推理标准测试")));
}

export async function adminDelete(ctx: ApiContext, path: string) {
  const resp = await adminFetch(ctx, path, { method: "DELETE" });
  if (!resp.ok && resp.status !== 204) throw new Error(await readAdminError(resp, operationLabel("DELETE", path)));
}

export async function restoreDefaultModelCatalog(ctx: ApiContext) {
  await adminMutate(ctx, "/api/admin/models/restore-defaults", "POST", {});
}

export async function readLoadError(resp: Response, name: string) {
  if (resp.status === 403) return permissionDeniedMessage(loadRequestLabel(name));
  return readAdminError(resp, loadRequestLabel(name));
}

export function permissionDeniedMessage(target: string) {
  const label = target || tx("该资源");
  if (activeLanguage === "en") {
    return `This account does not have permission to access ${label}. Data outside your permission scope is hidden; ask an admin to adjust your role or project membership if needed.`;
  }
  if (activeLanguage === "ja") {
    return `このアカウントには ${label} へのアクセス権限がありません。権限外のデータは非表示です。必要に応じて管理者にロールまたはプロジェクトメンバー権限の調整を依頼してください。`;
  }
  return `当前账号没有访问 ${label} 的权限。页面已隐藏无权限数据；如需查看或管理，请联系管理员调整角色或项目成员权限。`;
}

export function permissionPartialLoadMessage(labels: string[]) {
  const unique = Array.from(new Set(labels.filter(Boolean))).slice(0, 4);
  const summary = unique.join("、");
  if (activeLanguage === "en") return `Hidden due to insufficient permission: ${summary}. This page only shows content you can access.`;
  if (activeLanguage === "ja") return `権限不足のため非表示: ${summary}。このページにはアクセス可能な内容のみ表示します。`;
  return `已隐藏无权限数据：${summary}。当前页面只展示你有权限查看的内容。`;
}

export function operationLabel(method: string, path: string) {
  const resource = resourceLabelFromPath(path);
  const action = method === "POST" ? "新增" : method === "PATCH" ? "编辑" : method === "DELETE" ? "删除" : "操作";
  return `${tx(resource)}${tx(action)}`;
}

export function loadRequestLabel(name: string) {
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

export function resourceLabelFromPath(path: string) {
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

export function resourceKindLabel(kind: string) {
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

export class AuthExpiredError extends Error {
  constructor() {
    super("auth_expired");
    this.name = "AuthExpiredError";
  }
}

export function isAuthExpiredError(error: unknown) {
  return error instanceof AuthExpiredError;
}

export async function adminFetch(ctx: ApiContext, path: string, init: RequestInit = {}) {
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
