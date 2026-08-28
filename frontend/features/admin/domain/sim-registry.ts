export type SIMCapabilityName = "theme_tokens" | "shell_layout" | "page_template" | "dashboard_composition";

export type SIMPluginCapabilityDescriptorLike = {
  kind?: unknown;
  name?: unknown;
  subject?: unknown;
  value?: unknown;
};

export type SIMPluginDescriptorLike = {
  id?: unknown;
  name?: unknown;
  version?: unknown;
  capabilities?: unknown;
};

export type SIMJSONValue = string | number | boolean | null | SIMJSONValue[] | { [key: string]: SIMJSONValue };
export type SIMJSONObject = { [key: string]: SIMJSONValue };

export type SIMThemeMode = "light" | "dark" | "all";

export type SIMCapabilityBase<TName extends SIMCapabilityName, TPayload extends SIMJSONObject> = {
  key: string;
  pluginID: string;
  pluginName: string;
  pluginVersion: string;
  name: TName;
  id: string;
  subject: string;
  title: string;
  order: number;
  priority: number;
  payload: TPayload;
};

export type SIMThemeTokens = SIMCapabilityBase<"theme_tokens", SIMJSONObject & {
  mode: SIMThemeMode;
  default: boolean;
  tokens: Record<string, string>;
}>;

export type SIMShellLayout = SIMCapabilityBase<"shell_layout", SIMJSONObject>;
export type SIMPageTemplate = SIMCapabilityBase<"page_template", SIMJSONObject>;
export type SIMDashboardComposition = SIMCapabilityBase<"dashboard_composition", SIMJSONObject>;

export type SIMCapability = SIMThemeTokens | SIMShellLayout | SIMPageTemplate | SIMDashboardComposition;

export type SIMRegistry = {
  all: SIMCapability[];
  themeTokens: SIMThemeTokens[];
  shellLayouts: SIMShellLayout[];
  pageTemplates: SIMPageTemplate[];
  dashboardCompositions: SIMDashboardComposition[];
};

const simCapabilityKind = "sim";
const simCapabilityNames: SIMCapabilityName[] = ["theme_tokens", "shell_layout", "page_template", "dashboard_composition"];
const simCapabilityOrder = new Map<SIMCapabilityName, number>(simCapabilityNames.map((name, index) => [name, index]));
const defaultOrder = 1000;

export function simRegistryFromPlugins(plugins: readonly SIMPluginDescriptorLike[] | undefined): SIMRegistry {
  const all = simCapabilitiesFromPlugins(plugins);
  return {
    all,
    themeTokens: all.filter((capability): capability is SIMThemeTokens => capability.name === "theme_tokens"),
    shellLayouts: all.filter((capability): capability is SIMShellLayout => capability.name === "shell_layout"),
    pageTemplates: all.filter((capability): capability is SIMPageTemplate => capability.name === "page_template"),
    dashboardCompositions: all.filter((capability): capability is SIMDashboardComposition => capability.name === "dashboard_composition"),
  };
}

export function simCapabilitiesFromPlugins(plugins: readonly SIMPluginDescriptorLike[] | undefined): SIMCapability[] {
  const capabilities: SIMCapability[] = [];
  for (const plugin of plugins ?? []) {
    const rawCapabilities = Array.isArray(plugin.capabilities) ? plugin.capabilities : [];
    for (const rawCapability of rawCapabilities) {
      if (!rawCapability || typeof rawCapability !== "object" || Array.isArray(rawCapability)) continue;
      const parsed = parseSIMCapability(plugin, rawCapability as SIMPluginCapabilityDescriptorLike);
      if (parsed) capabilities.push(parsed);
    }
  }
  return capabilities.sort(compareSIMCapabilities);
}

export function parseSIMCapability(plugin: SIMPluginDescriptorLike, capability: SIMPluginCapabilityDescriptorLike): SIMCapability | null {
  if (stringValue(capability.kind) !== simCapabilityKind) return null;
  const name = simCapabilityName(capability.name);
  if (!name) return null;
  const payload = jsonObjectValue(capability.value);
  if (!payload) return null;
  if (name === "theme_tokens") return simThemeTokensCapability(plugin, capability, payload);
  return simGenericCapability(plugin, capability, name, payload);
}

export function compareSIMCapabilities(left: SIMCapability, right: SIMCapability) {
  return compareNumber(left.order, right.order) ||
    compareNumber(right.priority, left.priority) ||
    left.pluginID.localeCompare(right.pluginID) ||
    compareNumber(simCapabilityOrder.get(left.name) ?? defaultOrder, simCapabilityOrder.get(right.name) ?? defaultOrder) ||
    left.id.localeCompare(right.id) ||
    left.key.localeCompare(right.key);
}

function simThemeTokensCapability(plugin: SIMPluginDescriptorLike, capability: SIMPluginCapabilityDescriptorLike, payload: SIMJSONObject): SIMThemeTokens | null {
  const tokens = stringRecordValue(payload.tokens);
  if (Object.keys(tokens).length === 0) return null;
  const mode = themeModeValue(payload.mode);
  const base = simCapabilityBase(plugin, capability, "theme_tokens", payload);
  return {
    ...base,
    payload: {
      ...payload,
      mode,
      default: payload.default === true,
      tokens,
    },
  };
}

function simGenericCapability<TName extends Exclude<SIMCapabilityName, "theme_tokens">>(
  plugin: SIMPluginDescriptorLike,
  capability: SIMPluginCapabilityDescriptorLike,
  name: TName,
  payload: SIMJSONObject,
): SIMCapabilityBase<TName, SIMJSONObject> {
  return {
    ...simCapabilityBase(plugin, capability, name, payload),
    payload,
  };
}

function simCapabilityBase<TName extends SIMCapabilityName>(
  plugin: SIMPluginDescriptorLike,
  capability: SIMPluginCapabilityDescriptorLike,
  name: TName,
  payload: SIMJSONObject,
): SIMCapabilityBase<TName, SIMJSONObject> {
  const pluginID = stringValue(plugin.id);
  const subject = stringValue(capability.subject);
  const id = stringValue(payload.id) || subject || name;
  const title = stringValue(payload.title) || stringValue(payload.name) || id;
  return {
    key: [pluginID, name, id].filter(Boolean).join(":"),
    pluginID,
    pluginName: stringValue(plugin.name),
    pluginVersion: stringValue(plugin.version),
    name,
    id,
    subject,
    title,
    order: numberValue(payload.order, defaultOrder),
    priority: numberValue(payload.priority, 0),
    payload,
  };
}

function simCapabilityName(value: unknown): SIMCapabilityName | "" {
  const normalized = stringValue(value);
  return simCapabilityNames.includes(normalized as SIMCapabilityName) ? normalized as SIMCapabilityName : "";
}

function jsonObjectValue(value: unknown): SIMJSONObject | null {
  const parsed = typeof value === "string" ? parseJSONString(value) : value;
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  const sanitized = sanitizeJSONValue(parsed);
  return sanitized && typeof sanitized === "object" && !Array.isArray(sanitized) ? sanitized : null;
}

function parseJSONString(value: string) {
  const trimmed = value.trim();
  if (!trimmed) return null;
  try {
    return JSON.parse(trimmed) as unknown;
  } catch {
    return null;
  }
}

function sanitizeJSONValue(value: unknown): SIMJSONValue | undefined {
  if (value === null) return null;
  if (typeof value === "string" || typeof value === "boolean") return value;
  if (typeof value === "number") return Number.isFinite(value) ? value : undefined;
  if (Array.isArray(value)) return value.flatMap((item) => {
    const sanitized = sanitizeJSONValue(item);
    return sanitized === undefined ? [] : [sanitized];
  });
  if (!value || typeof value !== "object") return undefined;
  const entries = Object.entries(value).flatMap(([key, item]) => {
    const normalizedKey = key.trim();
    const sanitized = sanitizeJSONValue(item);
    return normalizedKey && sanitized !== undefined ? [[normalizedKey, sanitized] as const] : [];
  });
  return Object.fromEntries(entries);
}

function stringRecordValue(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const result: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) {
    const normalizedKey = key.trim();
    if (!normalizedKey || typeof item !== "string") continue;
    const normalizedValue = item.trim();
    if (normalizedValue) result[normalizedKey] = normalizedValue;
  }
  return result;
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function themeModeValue(value: unknown): SIMThemeMode {
  return value === "light" || value === "dark" ? value : "all";
}

function numberValue(value: unknown, fallback: number) {
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function compareNumber(left: number, right: number) {
  return left < right ? -1 : left > right ? 1 : 0;
}
