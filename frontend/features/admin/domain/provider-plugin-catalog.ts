import { type PluginDescriptor, type ProviderCatalogEntry } from "../core/types";

export function providerCatalogEntriesFromPluginCapabilities(plugins: PluginDescriptor[] | undefined) {
  const entries: ProviderCatalogEntry[] = [];
  for (const plugin of plugins ?? []) {
    for (const capability of plugin.capabilities ?? []) {
      if (capability.kind !== "provider_catalog" || capability.name !== "entry" || !capability.value?.trim()) continue;
      const entry = providerCatalogEntryFromCapabilityValue(capability.value, capability.subject, plugin);
      if (entry) entries.push(entry);
    }
  }
  return entries;
}

function providerCatalogEntryFromCapabilityValue(value: string, subject: string | undefined, plugin: PluginDescriptor): ProviderCatalogEntry | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return null;
  const raw = parsed as Record<string, unknown>;
  const type = stringField(raw.type) || String(subject || "").trim();
  if (!type) return null;
  const id = stringField(raw.id) || type;
  const name = stringField(raw.name) || stringField(raw.display_name) || plugin.name || id;
  const displayName = stringField(raw.display_name) || name;
  return {
    id,
    name,
    display_name: displayName,
    type,
    base_url: stringField(raw.base_url),
    doc_url: stringField(raw.doc_url),
    categories: stringArrayField(raw.categories),
    category_counts: numberRecordField(raw.category_counts),
    models_count: numberField(raw.models_count),
    source: stringField(raw.source) || `plugin:${plugin.source}`,
  };
}

function stringField(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function numberField(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) && value > 0 ? value : 0;
}

function stringArrayField(value: unknown) {
  return Array.isArray(value) ? value.map(stringField).filter(Boolean) : undefined;
}

function numberRecordField(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return undefined;
  const entries = Object.entries(value).flatMap(([key, item]) => (
    typeof item === "number" && Number.isFinite(item) ? [[key, item] as const] : []
  ));
  return entries.length > 0 ? Object.fromEntries(entries) : undefined;
}
