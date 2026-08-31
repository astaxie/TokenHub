import { type AdminResource, type AppData, type Model, notificationChannelTypes, type Provider, type ProviderCatalogEntry, type ProviderCatalogModel, type Summary, type ViewKey } from "../core/types";
import { providerRoutesFor, stringifyValue } from "./entities";
import { formatMoney } from "./formatting";
import { compactList } from "./labels";
import { tx } from "../i18n/runtime";
import { canonicalModelNameWithDefinitions, inferModelCategoryTextWithDefinitions, modelCategoryDefinitionsFromData, modelCategoryKeys, modelCategoryLabelFromDefinitions, preferredModelCategories, standardModelCategoryWithDefinitions } from "./model-categories";
import { accountProviderCatalogCategory, accountProviderCatalogOptionsFromPlugins } from "./provider-account-catalog";
import { isProviderAccountResourceForData } from "./provider-resource-types";

export function emptyData(): AppData {
  return {
    summary: emptySummary(),
    projects: [],
    keys: [],
    providers: [],
    providerResources: [],
    providerModels: [],
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
    dailyUsage: emptyDailyUsage(),
    timeseries: [],
    resources: {},
    providerCatalog: [],
    providerAdapters: [],
    providerMonitoring: [],
    plugins: [],
    pluginMarketplace: [],
    pluginChain: { hooks: [] },
    pluginUI: [],
    pluginActions: [],
    pluginBackgroundJobs: [],
    pluginBackgroundRuns: [],
		billingConnectors: [],
		billingRecords: [],
		billingSyncRuns: [],
		reconciliationRules: [],
		reconciliationRuns: [],
  };
}

export function emptySummary(): Summary {
  return {
    request_count: 0,
    input_tokens: 0,
    cached_input_tokens: 0,
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

export function emptyDailyUsage() {
  return {
    timezone: "UTC",
    date: "",
    window_start: "",
    window_end: "",
    summary: emptySummary(),
    breakdown: { projects: [], models: [], members: [], providers: [], provider_resources: [], cost_centers: [], api_keys: [] },
  };
}

export function filterRows<T>(items: T[], query: string) {
  const normalized = query.trim().toLowerCase();
  if (!normalized) return items;
  return items.filter((item) => JSON.stringify(item).toLowerCase().includes(normalized));
}

export function catalogModelCategoryOptions(catalog: ProviderCatalogEntry[], data?: Pick<AppData, "plugins" | "providerAdapters">) {
  const definitions = modelCategoryDefinitionsFromData(data);
  const counts = new Map<string, number>();
  for (const entry of catalog) {
    if (entry.category_counts) {
      for (const [category, count] of Object.entries(entry.category_counts)) {
        const normalized = standardModelCategory(category, definitions);
        counts.set(normalized, (counts.get(normalized) ?? 0) + count);
      }
      continue;
    }
    for (const category of entry.categories ?? []) {
      const normalized = standardModelCategory(category, definitions);
      counts.set(normalized, (counts.get(normalized) ?? 0) + 1);
    }
    for (const model of entry.models ?? []) {
      const category = modelCategoryForCatalog(model, definitions);
      counts.set(category, (counts.get(category) ?? 0) + 1);
    }
  }
  if (counts.size === 0) counts.set("custom", 1);
  const ordered = modelCategoryKeys(definitions).filter((category) => counts.has(category));
  for (const category of Array.from(counts.keys()).sort()) {
    if (!ordered.includes(category)) ordered.push(category);
  }
  return ordered.map((category) => ({
    key: category,
    label: modelCategoryLabel(category, definitions),
    count: counts.get(category) ?? 0,
  }));
}

export function providerEntrySupportsCategory(entry: ProviderCatalogEntry, category: string, data?: Pick<AppData, "plugins" | "providerAdapters">) {
  const definitions = modelCategoryDefinitionsFromData(data);
  const normalizedCategory = standardModelCategory(category, definitions);
  if (category === "all") return true;
  for (const [rawCategory, count] of Object.entries(entry.category_counts ?? {})) {
    if (count > 0 && standardModelCategory(rawCategory, definitions) === normalizedCategory) return true;
  }
  if ((entry.categories ?? []).some((rawCategory) => standardModelCategory(rawCategory, definitions) === normalizedCategory)) return true;
  return (entry.models ?? []).some((model) => modelCategoryForCatalog(model, definitions) === normalizedCategory);
}

export function providerEntryCategoryCount(entry: ProviderCatalogEntry, category: string, data?: Pick<AppData, "plugins" | "providerAdapters">) {
  if (category === "all") return entry.models_count;
  const definitions = modelCategoryDefinitionsFromData(data);
  const normalizedCategory = standardModelCategory(category, definitions);
  let count = 0;
  for (const [rawCategory, rawCount] of Object.entries(entry.category_counts ?? {})) {
    if (standardModelCategory(rawCategory, definitions) === normalizedCategory) count += rawCount;
  }
  if (count > 0) return count;
  const modelCount = (entry.models ?? []).filter((model) => modelCategoryForCatalog(model, definitions) === normalizedCategory).length;
  if (modelCount > 0) return modelCount;
  return (entry.categories ?? []).some((rawCategory) => standardModelCategory(rawCategory, definitions) === normalizedCategory) ? entry.models_count : 0;
}

export function buildCustomProviderCatalogEntry(category: string, standardModels: Model[]): ProviderCatalogEntry {
  void standardModels;
  const normalizedCategory = standardModelCategory(category);
  return {
    id: "custom",
    name: "自定义渠道商",
    display_name: "自定义渠道商",
    type: "",
    categories: [normalizedCategory],
    category_counts: { [normalizedCategory]: 0 },
    models_count: 0,
    source: "custom-upstream-pending",
    models: [],
  };
}

export function modelCategoryForCatalog(model: ProviderCatalogModel, dataOrDefinitions?: Pick<AppData, "plugins" | "providerAdapters"> | ReturnType<typeof modelCategoryDefinitionsFromData>) {
  const definitions = Array.isArray(dataOrDefinitions) ? dataOrDefinitions : modelCategoryDefinitionsFromData(dataOrDefinitions);
  return standardModelCategory(modelCategory(model, definitions), definitions);
}

export function canonicalModelNameForUI(id: string, displayName?: string, data?: Pick<AppData, "plugins" | "providerAdapters">) {
  return canonicalModelNameWithDefinitions(id, displayName, modelCategoryDefinitionsFromData(data));
}

export function filterByModelCategory<T>(view: ViewKey | undefined, items: T[], category: string, data: AppData) {
  if (!view || category === "all") return items;
  if (view === "models") {
    return items.filter((item) => modelCategory(item as Model, data) === category);
  }
  if (view === "providers") {
    return items.filter((item) => providerCategories(item as Provider, data).includes(category));
  }
  if (view === "notification-channels") {
    return items.filter((item) => notificationChannelType(item as AdminResource) === category);
  }
  return items;
}

export function modelCategoryTabs(data: AppData, view: ViewKey) {
  const definitions = modelCategoryDefinitionsFromData(data);
  const counts = new Map<string, number>();
  if (view === "providers") {
    for (const provider of data.providers) {
      for (const category of providerCategories(provider, data)) {
        counts.set(category, (counts.get(category) ?? 0) + 1);
      }
    }
  } else {
    for (const model of data.models) {
      const category = modelCategory(model, definitions);
      counts.set(category, (counts.get(category) ?? 0) + 1);
    }
  }
  const ordered = modelCategoryKeys(definitions).filter((category) => counts.has(category));
  for (const category of Array.from(counts.keys()).sort()) {
    if (!ordered.includes(category)) ordered.push(category);
  }
  return [
    { key: "all", label: "全部", count: view === "providers" ? data.providers.length : data.models.length },
    ...ordered.map((category) => ({
      key: category,
      label: modelCategoryLabel(category, definitions),
      count: counts.get(category) ?? 0,
    })),
  ];
}

export function notificationChannelTabs(data: AppData) {
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

export function notificationChannelType(item: AdminResource) {
  return normalizeNotificationChannelType(stringifyValue(item.fields?.type));
}

export function notificationChannelFormType(values: Record<string, string>) {
  return normalizeNotificationChannelType(values.type);
}

export function notificationChannelUsesIncomingWebhook(values: Record<string, string>) {
  return !["email", "telegram", "whatsapp"].includes(notificationChannelFormType(values));
}

export function notificationChannelUsesEmail(values: Record<string, string>) {
  return notificationChannelFormType(values) === "email";
}

export function notificationChannelUsesTelegram(values: Record<string, string>) {
  return notificationChannelFormType(values) === "telegram";
}

export function notificationChannelUsesWhatsApp(values: Record<string, string>) {
  return notificationChannelFormType(values) === "whatsapp";
}

export function normalizeNotificationChannelType(type: string) {
  const normalized = type.trim().toLowerCase();
  if (normalized === "dingding" || normalized === "ding_talk") return "dingtalk";
  if (normalized === "wechat_work" || normalized === "weixin_work" || normalized === "enterprise_wechat") return "wecom";
  if (normalized === "tg") return "telegram";
  if (["whatsapp_cloud", "whatsapp_business", "wa"].includes(normalized)) return "whatsapp";
  if (notificationChannelTypes.includes(normalized)) return normalized;
  return "webhook";
}

export function notificationChannelLabel(type: string) {
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

export function notificationChannelDescription(type: string) {
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

export function notificationChannelURLPlaceholder(type: string) {
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

export function notificationChannelTargetSummary(item: AdminResource) {
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

export function notificationCredentialSummary(item: AdminResource) {
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

export function maskWebhookURL(url: string) {
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

export function modelCategoryInitial(category: string, label: string) {
  return (label || category || "M").trim().slice(0, 1).toUpperCase();
}

export function modelCatalogFilterLabel(categories: Array<{ key: string; label: string }>, active: string) {
  return categories.find((item) => item.key === active)?.label ?? "全部";
}

export function priceMetric(value: number | undefined) {
  if (!value) return "$-";
  return `$${formatMoney(value)}/Mt`;
}

export function modelCategory(model: Model | ProviderCatalogModel | undefined, dataOrDefinitions?: Pick<AppData, "plugins" | "providerAdapters"> | ReturnType<typeof modelCategoryDefinitionsFromData>) {
  const definitions = Array.isArray(dataOrDefinitions) ? dataOrDefinitions : modelCategoryDefinitionsFromData(dataOrDefinitions);
  const explicit = model?.category?.trim().toLowerCase();
  if (explicit) return standardModelCategory(explicit, definitions);
  const displayName = model && "display_name" in model ? model.display_name : "";
  return inferModelCategoryText([model?.name, model?.id, displayName, model?.family].filter(Boolean).join(" "), definitions);
}

export function providerCategories(provider: Provider, data: AppData) {
  const definitions = modelCategoryDefinitionsFromData(data);
  if (data.providerResources.some((resource) => resource.provider_id === provider.id && isProviderAccountResourceForData(data, resource))) {
    const accountCatalog = accountProviderCatalogOptionsFromPlugins(data.providerCatalog, data.plugins, data.providerAdapters).find((entry) => entry.type === provider.type);
    if (accountCatalog) return [accountProviderCatalogCategory(accountCatalog)];
  }
  const routeModels = providerRoutesFor(provider, data)
    .map((route) => data.models.find((model) => model.name === route.model_name))
    .filter(Boolean) as Model[];
  const categories = routeModels.map((model) => modelCategory(model, definitions));
  const optionCategory = provider.options?.model_category;
  if (optionCategory) categories.push(standardModelCategory(optionCategory, definitions));
  if (categories.length === 0) {
    const catalogCategory = providerCatalogCategoryForType(provider.type, data.providerCatalog, definitions);
    categories.push(catalogCategory || providerAdapterCategoryForType(provider.type, data, definitions) || providerTypeToModelCategory(provider.type));
  }
  return Array.from(new Set(categories.filter(Boolean))).sort();
}

export function providerCatalogCategoryForType(type: string, catalog: ProviderCatalogEntry[], definitions = modelCategoryDefinitionsFromData()) {
  const normalizedType = type.trim().toLowerCase();
  if (!normalizedType) return "";
  const entry = catalog.find((item) => item.type.trim().toLowerCase() === normalizedType);
  if (!entry) return "";
  const category = entry.categories?.find((item) => item.trim());
  if (category) return standardModelCategory(category, definitions);
  const countedCategory = Object.entries(entry.category_counts ?? {}).find(([, count]) => count > 0)?.[0];
  if (countedCategory) return standardModelCategory(countedCategory, definitions);
  return "";
}

export function providerAdapterCategoryForType(type: string, data: Pick<AppData, "providerAdapters">, definitions = modelCategoryDefinitionsFromData(data)) {
  const normalizedType = type.trim().toLowerCase();
  const adapter = data.providerAdapters.find((item) => item.type.trim().toLowerCase() === normalizedType);
  const category = adapter?.provider_policy?.model_categories?.find((item) => item.key?.trim())?.key;
  return category ? standardModelCategory(category, definitions) : "";
}

export function providerTypeToModelCategory(type: string) {
  void type;
  return "custom";
}

export function modelCategoryFormOptions() {
  return preferredModelCategories.filter((category) => category !== "custom").concat("custom");
}

export function standardModelCategory(category: string, definitions = modelCategoryDefinitionsFromData()) {
  return standardModelCategoryWithDefinitions(category, definitions);
}

export function modelCategoryLabel(category: string, dataOrDefinitions?: Pick<AppData, "plugins" | "providerAdapters"> | ReturnType<typeof modelCategoryDefinitionsFromData>) {
  const definitions = Array.isArray(dataOrDefinitions) ? dataOrDefinitions : modelCategoryDefinitionsFromData(dataOrDefinitions);
  return tx(modelCategoryLabelFromDefinitions(category, definitions));
}

export function inferModelCategoryText(value: string, definitions = modelCategoryDefinitionsFromData()) {
  return inferModelCategoryTextWithDefinitions(value, definitions);
}
