import { type PluginDescriptor, type ProviderCatalogEntry } from "../core/types";
import { accountProviderCatalogOptions } from "./codex-provider-profile";
import { isProviderAccountResourceType, providerResourceTypeCapabilityKind } from "./provider-resource-types";

export function accountProviderCatalogOptionsFromPlugins(catalog: ProviderCatalogEntry[], plugins: PluginDescriptor[]) {
  const accountProviderTypes = new Set<string>();
  for (const plugin of plugins) {
    for (const capability of plugin.capabilities) {
      if (capability.kind !== providerResourceTypeCapabilityKind) continue;
      if (!isProviderAccountResourceType(capability.name)) continue;
      if (capability.subject?.trim()) accountProviderTypes.add(capability.subject.trim());
    }
  }
  const candidates = catalog.filter((entry) => accountProviderTypes.has(entry.type));
  const merged = new Map<string, ProviderCatalogEntry>();
  for (const entry of candidates) {
    merged.set(entry.id, entry);
  }
  const legacyFallbacks = accountProviderTypes.size === 0
    ? accountProviderCatalogOptions
    : accountProviderCatalogOptions.filter((entry) => accountProviderTypes.has(entry.type));
  for (const entry of legacyFallbacks) {
    if (!merged.has(entry.id)) merged.set(entry.id, entry);
  }
  return Array.from(merged.values()).sort((left, right) => {
    const leftName = left.display_name || left.name || left.id;
    const rightName = right.display_name || right.name || right.id;
    return leftName.localeCompare(rightName);
  });
}

export function directProviderCatalogOptions(catalog: ProviderCatalogEntry[], accountCatalog: ProviderCatalogEntry[]) {
  const accountCatalogIDs = new Set(accountCatalog.map((entry) => entry.id));
  const accountProviderTypes = new Set(accountCatalog.map((entry) => entry.type));
  return catalog.filter((entry) => !accountCatalogIDs.has(entry.id) && !accountProviderTypes.has(entry.type));
}

export function accountProviderCatalogCategory(entry: ProviderCatalogEntry) {
  return entry.categories?.[0] ?? "all";
}

export function accountProviderResourceDefaultPatch(defaults: Record<string, string>) {
  return {
    resource_type: defaults.resource_type,
    auth_type: defaults.auth_type,
    base_url: defaults.base_url,
    max_concurrency: defaults.max_concurrency,
  };
}
