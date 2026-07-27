import { type FieldConfig, type Model, type ModelRoute, type Provider, type ProviderResource, type ResourceConfig } from "../core/types";
import { modelCategory, modelCategoryFormOptions, modelCategoryLabel } from "../domain/catalog";
import { codexImageCapableResources, findProvider, isCodexSubscriptionImageModel, modelCapabilitySummary, modelPriceSummary, modelRouteDefaults, modelRoutesFor, modelSelectOptions, providerAccountResourceSummary, providerDisplayBaseURL, providerDisplayName, providerDisplayType, providerRouteDefaults, providerRouteSummary, providerSelectOptions, routeScoreSummary, stringifyForm } from "../domain/entities";
import { formatTime, modelToForm, routeStrategyLabel } from "../domain/formatting";
import { providerTypeLabel, resourceTypeLabel } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { adminDelete, adminMutate, createModelRoutes, modelPayload, providerPayload, providerResourcePayload, providerResourceToForm, providerResourceUpdatePayload, providerUpdatePayload, routePayload, testProviderAvailability } from "./payloads";
import { ModelNameCell, ModelRouteProviders, providerTypeOptions, StatusPill } from "../shared/ui";

export function providerConfig(): ResourceConfig<Provider> {
  return {
    view: "providers",
    title: "Provider 渠道",
    eyebrow: "Provider 列表",
    description: "Provider 是一个可调用的上游渠道实例，包含服务商类型、Base URL、API Key、健康状态和标准模型路由。",
    createLabel: "新增 Provider",
    columns: [
      { key: "name", label: "名称", render: (item, ctx) => providerDisplayName(item, ctx.providerResources) },
      { key: "type", label: "类型", render: (item, ctx) => providerTypeLabel(providerDisplayType(item, ctx.providerResources)) },
      { key: "base_url", label: "Base URL", render: (item, ctx) => providerDisplayBaseURL(item, ctx.providerResources) },
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
        title: "检测 Provider 可用性；Codex 订阅使用 Luna 中等推理标准速度真实测试",
        run: testProviderAvailability,
        doneMessage: (item) => `${item.name} ${tx("检测完成")}`,
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

export function providerResourceFieldConfigs(provider?: Provider): FieldConfig[] {
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

export function providerResourceConfig(provider?: Provider): ResourceConfig<ProviderResource> {
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

export function openAIAccountFieldVisible(values: Record<string, string>) {
  return values.resource_type === "openai_subscription";
}

export function providerCreateAccountResourceFields() {
  const hiddenKeys = new Set(["provider_id", "healthy"]);
  return providerResourceFieldConfigs()
    .filter((field) => !hiddenKeys.has(field.key))
    .map((field) => field.key === "name" ? { ...field, label: "账号资源名称" } : field);
}

export function providerCreateAccountRuntimeFields() {
  const keys = new Set(["base_url", "group", "priority", "weight", "rate_limit_rpm", "token_limit_tpm", "max_concurrency", "status"]);
  return providerResourceFieldConfigs()
    .filter((field) => keys.has(field.key))
    .map((field) => field.key === "base_url" ? { ...field, required: true } : field);
}

export function providerCreateAccountManualTokenFields() {
  const keys = new Set(["access_token", "refresh_token", "id_token", "account_id", "organization_id", "plan_type"]);
  return providerResourceFieldConfigs().filter((field) => keys.has(field.key));
}

export function providerAccountTokenSummary(values: Record<string, string>) {
  const items: string[] = [];
  if (values.access_token?.trim()) items.push("已回填访问 Token");
  if (values.refresh_token?.trim()) items.push("已回填刷新 Token");
  if (values.id_token?.trim()) items.push("已回填 ID Token");
  return { ready: items.length > 0, items };
}

export function defaultProviderResourceName(providerName?: string) {
  const normalized = providerName?.trim() || "Provider";
  return `${normalized} Codex Account`;
}

export function providerResourceDraftDefaults(provider: { provider_id?: string; name?: string; base_url?: string }) {
  return {
    provider_id: provider.provider_id ?? "",
    name: defaultProviderResourceName(provider.name),
    resource_type: "openai_subscription",
    auth_type: "oauth",
    authorization_url: "",
    base_url: provider.base_url || "https://chatgpt.com/backend-api/codex",
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

export function providerResourceDefaults(provider: Provider) {
  return providerResourceDraftDefaults({
    provider_id: provider.id,
    name: provider.name || provider.id,
    base_url: provider.base_url,
  });
}

export function assertProviderAccountResourceReady(values: Record<string, string>) {
  if (values.resource_type === "openai_subscription") {
    if (values.access_token?.trim() || values.refresh_token?.trim() || values.id_token?.trim()) return;
    throw new Error(tx("请先完成账号授权回填，或在高级选项中手动粘贴 Token。"));
  }
  if (!values.api_key?.trim()) {
    throw new Error(tx("请填写账号资源的 API Key。"));
  }
}

export function modelConfig(): ResourceConfig<Model> {
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
      { key: "route_count", label: "路由数", render: (item, ctx) => isCodexSubscriptionImageModel(item) ? codexImageCapableResources(ctx).length : modelRoutesFor(item, ctx).length },
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
      {
        key: "cache_read_price_usd_per_1m",
        label: "缓存读价 USD/1M",
        type: "number",
        placeholder: "可选，留空时按同类模型常见比例估算",
        help: "配置值优先用于成本计算；留空时 DeepSeek V4 Pro 按约 0.83%、其他 DeepSeek 按 2%、其余模型按 10% 估算。",
        visible: (values) => values.modality !== "embedding",
      },
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

export function routeConfig(): ResourceConfig<ModelRoute> {
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
