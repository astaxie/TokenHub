import { type AppData, type PluginCapabilityDescriptor } from "../core/types";

export const providerResourceAPIKeyType = "api_key";
export const providerResourceOpenAISubscriptionType = "openai_subscription";
export const providerResourceTypeCapabilityKind = "provider_resource_type";

type ProviderResourceTypeLike = {
  resource_type?: string;
};

export type ProviderResourceTypeCapabilityMetadata = {
  type: string;
  displayName?: string;
  authModes: string[];
  defaults: Record<string, string>;
  default: boolean;
};

export function isOpenAISubscriptionResourceType(resourceType: string | undefined) {
  return resourceType === providerResourceOpenAISubscriptionType;
}

export function isOpenAISubscriptionResource(resource: ProviderResourceTypeLike) {
  return isOpenAISubscriptionResourceType(resource.resource_type);
}

export function isProviderAccountResourceType(resourceType: string | undefined) {
  return Boolean(resourceType?.trim()) && resourceType !== providerResourceAPIKeyType;
}

export function isProviderAccountResource(resource: ProviderResourceTypeLike) {
  return isProviderAccountResourceType(resource.resource_type);
}

export function providerResourceTypeOptionOrder(value: string) {
  if (value === providerResourceOpenAISubscriptionType) return 0;
  if (value === providerResourceAPIKeyType) return 1;
  return 2;
}

export function parseProviderResourceTypeCapabilityMetadata(capability: PluginCapabilityDescriptor): ProviderResourceTypeCapabilityMetadata | null {
  if (capability.kind !== providerResourceTypeCapabilityKind) return null;
  const rawValue = capability.value?.trim();
  if (!rawValue) return null;
  try {
    const payload = JSON.parse(rawValue) as unknown;
    if (!payload || typeof payload !== "object") return null;
    const data = payload as Record<string, unknown>;
    const type = stringValue(data.type) || capability.name.trim();
    if (!type) return null;
    return {
      type,
      displayName: stringValue(data.display_name),
      authModes: stringArrayValue(data.auth_modes),
      defaults: stringRecordValue(data.defaults),
      default: data.default === true,
    };
  } catch {
    return null;
  }
}

export function providerResourceTypeMetadataFromData(data: Pick<AppData, "plugins">, providerType: string) {
  const metadata: ProviderResourceTypeCapabilityMetadata[] = [];
  for (const plugin of data.plugins) {
    for (const capability of plugin.capabilities) {
      if (capability.kind !== providerResourceTypeCapabilityKind) continue;
      if (capability.subject && providerType && capability.subject !== providerType) continue;
      const parsed = parseProviderResourceTypeCapabilityMetadata(capability);
      metadata.push(parsed ?? {
        type: capability.name.trim(),
        authModes: [],
        defaults: {},
        default: false,
      });
    }
  }
  return metadata.filter((item) => Boolean(item.type));
}

export function providerResourceTypeMetadataForValues(data: AppData, values?: Record<string, string>) {
  const providerType = providerTypeForResourceValues(data, values);
  const resourceType = values?.resource_type?.trim();
  if (!resourceType) return null;
  return providerResourceTypeMetadataFromData(data, providerType).find((metadata) => metadata.type === resourceType) ?? null;
}

export function providerResourceTypeMetadataForResource(data: AppData, resource: ProviderResourceTypeLike & { provider_id?: string }) {
  return providerResourceTypeMetadataForValues(data, {
    provider_id: resource.provider_id ?? "",
    resource_type: resource.resource_type ?? "",
  });
}

export function defaultProviderResourceTypeMetadata(data: Pick<AppData, "plugins">, providerType: string) {
  const metadata = providerResourceTypeMetadataFromData(data, providerType);
  return metadata.find((item) => item.default) ?? metadata[0] ?? null;
}

export function providerResourceAuthTypeOptionsFromData(data: AppData, _currentUser?: unknown, values?: Record<string, string>) {
  const metadata = providerResourceTypeMetadataForValues(data, values);
  const options = metadata?.authModes.length
    ? metadata.authModes
    : ["oauth", "personal_access_token", "api_key"];
  return options.map((value) => ({ value, label: providerResourceAuthTypeLabel(value) }));
}

function providerTypeForResourceValues(data: AppData, values?: Record<string, string>) {
  const providerID = values?.provider_id?.trim();
  if (!providerID) return "";
  return data.providers.find((provider) => provider.id === providerID)?.type ?? "";
}

function providerResourceAuthTypeLabel(value: string) {
  const labels: Record<string, string> = {
    oauth: "OAuth",
    personal_access_token: "Personal Access Token",
    api_key: "API Key",
  };
  return labels[value] ?? value;
}

function stringValue(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function stringArrayValue(value: unknown) {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>();
  for (const item of value) {
    const normalized = stringValue(item);
    if (normalized) seen.add(normalized);
  }
  return Array.from(seen).sort();
}

function stringRecordValue(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return {};
  const result: Record<string, string> = {};
  for (const [key, item] of Object.entries(value)) {
    const normalizedKey = key.trim();
    if (!normalizedKey || typeof item !== "string") continue;
    result[normalizedKey] = item.trim();
  }
  return result;
}
