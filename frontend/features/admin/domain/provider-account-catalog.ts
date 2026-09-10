import { type AdapterDescriptor, type PluginDescriptor, type Provider, type ProviderCatalogEntry } from "../core/types";
import { isProviderAccountResourceType, providerResourceTypeCapabilityKind } from "./provider-resource-types";

export function accountProviderCatalogOptionsFromPlugins(catalog: ProviderCatalogEntry[], plugins: PluginDescriptor[], adapters: AdapterDescriptor[] = []) {
  const accountProviderTypes = new Set<string>();
  for (const adapter of adapters) {
    if (adapter.type && adapter.provider_policy?.credentials_scope === "resource") accountProviderTypes.add(adapter.type);
  }
  for (const plugin of plugins) {
    for (const capability of plugin.capabilities) {
      const subject = capability.subject?.trim();
      if (!subject) continue;
      if (capability.kind === providerResourceTypeCapabilityKind && isProviderAccountResourceType(capability.name)) accountProviderTypes.add(subject);
      if (capability.kind === "provider_policy" && capability.name === "credentials_scope" && capability.value === "resource") accountProviderTypes.add(subject);
    }
  }
  const candidates = catalog.filter((entry) => accountProviderTypes.has(entry.type));
  const merged = new Map<string, ProviderCatalogEntry>();
  for (const entry of candidates) {
    merged.set(entry.id, entry);
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

export function accountProviderCatalogEntryFromProvider(provider: Provider): ProviderCatalogEntry {
  return {
    id: provider.options?.catalog_id || provider.type,
    name: provider.name || provider.type,
    display_name: provider.name || provider.type,
    type: provider.type,
    base_url: provider.base_url,
    categories: provider.options?.model_category ? [provider.options.model_category] : ["all"],
    category_counts: {},
    models_count: 0,
    source: "provider",
  };
}

export function accountProviderResourceDefaultPatch(defaults: Record<string, string>) {
  return {
    resource_type: defaults.resource_type,
    auth_type: defaults.auth_type,
    base_url: defaults.base_url,
    max_concurrency: defaults.max_concurrency,
  };
}
