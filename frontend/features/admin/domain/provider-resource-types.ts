import { type PluginCapabilityDescriptor } from "../core/types";

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
