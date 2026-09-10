import type { AdapterDescriptor, AdapterModelCategory, PluginDescriptor } from "../core/types";

export type ModelCategoryDefinition = {
  key: string;
  label?: string;
  order?: number;
  aliases?: string[];
  family_prefixes?: string[];
  canonical_prefixes?: string[];
  icon_src?: string;
};

const coreModelCategoryLabels: Record<string, string> = {
  all: "全部",
  custom: "自定义",
};

const coreModelCategoryDefinitions: ModelCategoryDefinition[] = [
  { key: "custom", label: "自定义", order: 1000, aliases: ["custom"], family_prefixes: ["custom"], canonical_prefixes: ["custom"] },
];

export const preferredModelCategories = coreModelCategoryDefinitions.map((definition) => definition.key);

export function preferredModelCategoriesFromData(data?: { plugins?: PluginDescriptor[]; providerAdapters?: AdapterDescriptor[] }) {
  return modelCategoryKeys(modelCategoryDefinitionsFromData(data));
}

export function modelCategoryDefinitionsFromData(data?: { plugins?: PluginDescriptor[]; providerAdapters?: AdapterDescriptor[] }) {
  return normalizeModelCategoryDefinitions([
    ...modelCategoryDefinitionsFromPlugins(data?.plugins),
    ...modelCategoryDefinitionsFromAdapters(data?.providerAdapters),
    ...coreModelCategoryDefinitions,
  ]);
}

export function modelCategoryDefinitionsFromAdapters(adapters?: AdapterDescriptor[]) {
  return normalizeModelCategoryDefinitions((adapters ?? []).flatMap((adapter) => adapter.provider_policy?.model_categories ?? []));
}

export function modelCategoryDefinitionsFromPlugins(plugins?: PluginDescriptor[]) {
  const definitions: ModelCategoryDefinition[] = [];
  for (const plugin of plugins ?? []) {
    for (const capability of plugin.capabilities ?? []) {
      if (capability.kind !== "provider_catalog" || capability.name !== "model_category" || !capability.value) continue;
      try {
        definitions.push(JSON.parse(capability.value) as ModelCategoryDefinition);
      } catch {
        continue;
      }
    }
  }
  return normalizeModelCategoryDefinitions(definitions);
}

export function modelCategoryKeys(definitions?: ModelCategoryDefinition[]) {
  return categoryDefinitionsOrFallback(definitions).map((definition) => definition.key);
}

export function modelCategorySortIndex(category: string, definitions?: ModelCategoryDefinition[]) {
  const normalized = category.trim().toLowerCase();
  const index = categoryDefinitionsOrFallback(definitions).findIndex((definition) => definition.key === normalized);
  return index >= 0 ? index : categoryDefinitionsOrFallback(definitions).length;
}

export function standardModelCategoryWithDefinitions(category: string, definitions?: ModelCategoryDefinition[]) {
  const normalized = category.trim().toLowerCase();
  if (!normalized) return "custom";
  if (categoryDefinitionsOrFallback(definitions).some((definition) => definition.key === normalized)) return normalized;
  return normalized;
}

export function modelCategoryLabelFromDefinitions(category: string, definitions?: ModelCategoryDefinition[]) {
  const normalized = category.trim().toLowerCase();
  const definition = categoryDefinitionsOrFallback(definitions).find((item) => item.key === normalized);
  return definition?.label || coreModelCategoryLabels[normalized] || category;
}

export function modelCategoryIconSourceFromDefinitions(category: string, definitions?: ModelCategoryDefinition[]) {
  const normalized = category.trim().toLowerCase();
  const definition = categoryDefinitionsOrFallback(definitions).find((item) => item.key === normalized);
  return safeIconSource(definition?.icon_src);
}

export function inferModelCategoryTextWithDefinitions(value: string, definitions?: ModelCategoryDefinition[]) {
  const normalized = value.toLowerCase();
  for (const definition of categoryDefinitionsOrFallback(definitions)) {
    if (categoryAliasesMatch(normalized, definition.aliases ?? [])) return definition.key;
  }
  return "custom";
}

export function canonicalModelNameWithDefinitions(id: string, displayName?: string, definitions?: ModelCategoryDefinition[]) {
  let value = (id || "").trim();
  const slash = value.lastIndexOf("/");
  if (slash >= 0 && slash < value.length - 1) value = value.slice(slash + 1);
  if (!value) value = (displayName || "").trim();
  value = value.toLowerCase().replaceAll(" ", "-").replaceAll("_", "-");
  while (value.includes("--")) value = value.replaceAll("--", "-");
  value = value.replace(/^-+|-+$/g, "");
  for (const definition of categoryDefinitionsOrFallback(definitions)) {
    for (const prefix of definition.canonical_prefixes ?? []) {
      value = normalizeCompactModelVersionForUI(value, prefix);
    }
  }
  return value || "custom-model";
}

export function normalizeCompactModelVersionForUI(value: string, prefix: string) {
  const compact = `${prefix}v`;
  if (value.startsWith(compact) && value.length > compact.length && /\d/.test(value[compact.length])) {
    return `${prefix}-v${value.slice(compact.length)}`;
  }
  if (value.startsWith(prefix) && value.length > prefix.length && /\d/.test(value[prefix.length])) {
    return `${prefix}-${value.slice(prefix.length)}`;
  }
  return value;
}

function categoryDefinitionsOrFallback(definitions?: ModelCategoryDefinition[]) {
  return definitions && definitions.length > 0 ? normalizeModelCategoryDefinitions(definitions) : normalizeModelCategoryDefinitions(coreModelCategoryDefinitions);
}

function normalizeModelCategoryDefinitions(definitions: Array<ModelCategoryDefinition | AdapterModelCategory>) {
  const byKey = new Map<string, ModelCategoryDefinition>();
  for (const raw of definitions) {
    const definition = normalizeModelCategoryDefinition(raw);
    if (!definition.key) continue;
    const existing = byKey.get(definition.key);
    if (existing) {
      definition.aliases = uniqueStrings([...(existing.aliases ?? []), ...(definition.aliases ?? [])]);
      definition.family_prefixes = uniqueStrings([...(existing.family_prefixes ?? []), ...(definition.family_prefixes ?? [])]);
      definition.canonical_prefixes = uniqueStrings([...(existing.canonical_prefixes ?? []), ...(definition.canonical_prefixes ?? [])]);
      definition.label ||= existing.label;
      definition.order ||= existing.order;
      if (!definition.icon_src && existing.icon_src) definition.icon_src = existing.icon_src;
    }
    byKey.set(definition.key, definition);
  }
  return Array.from(byKey.values()).sort((left, right) => (left.order ?? 0) - (right.order ?? 0) || left.key.localeCompare(right.key));
}

function normalizeModelCategoryDefinition(raw: ModelCategoryDefinition | AdapterModelCategory) {
  const key = (raw.key || "").trim().toLowerCase();
  const aliases = uniqueStrings((raw.aliases ?? []).map((item) => item.trim().toLowerCase()).filter(Boolean));
  return {
    key,
    label: raw.label?.trim(),
    order: raw.order,
    aliases: aliases.length > 0 ? aliases : key ? [key] : [],
    family_prefixes: uniqueStrings((raw.family_prefixes ?? []).map((item) => item.trim().toLowerCase()).filter(Boolean)),
    canonical_prefixes: uniqueStrings((raw.canonical_prefixes ?? []).map((item) => item.trim().toLowerCase()).filter(Boolean)),
    icon_src: safeIconSource("icon_src" in raw ? raw.icon_src : undefined),
  };
}

function categoryAliasesMatch(value: string, aliases: string[]) {
  for (const alias of aliases) {
    if (!alias) continue;
    if (alias.length <= 2 && /^[a-z0-9]+$/.test(alias)) {
      if (value.split(/[^a-z0-9]+/).includes(alias)) return true;
      continue;
    }
    if (value.includes(alias)) return true;
  }
  return false;
}

function uniqueStrings(values: string[]) {
  return Array.from(new Set(values.filter(Boolean))).sort();
}

function safeIconSource(value: unknown) {
  if (typeof value !== "string") return "";
  const source = value.trim();
  if (!source) return "";
  if (source.startsWith("/model-icons/") && source.endsWith(".svg")) return source;
  return "";
}
