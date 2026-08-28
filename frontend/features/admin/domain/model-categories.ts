import type { AdapterDescriptor, AdapterModelCategory, PluginDescriptor } from "../core/types";

export type ModelCategoryDefinition = {
  key: string;
  label?: string;
  order?: number;
  aliases?: string[];
  family_prefixes?: string[];
  canonical_prefixes?: string[];
};

export const modelCategoryLabels: Record<string, string> = {
  all: "全部",
  codex: "OpenAI Codex",
  openai: "OpenAI",
  claude: "Claude",
  deepseek: "DeepSeek",
  gemini: "Gemini",
  qwen: "Qwen",
  glm: "GLM",
  kimi: "Kimi",
  doubao: "Doubao",
  ernie: "ERNIE",
  baichuan: "Baichuan",
  minimax: "MiniMax",
  stepfun: "StepFun",
  wanx: "WanX",
  paddlepaddle: "PaddlePaddle",
  microsoft: "Microsoft",
  llama: "Llama",
  mistral: "Mistral",
  grok: "Grok",
  custom: "自定义",
};

export const fallbackModelCategoryDefinitions: ModelCategoryDefinition[] = [
  { key: "codex", label: "OpenAI Codex", order: 10, aliases: ["codex"], family_prefixes: ["codex"] },
  { key: "openai", label: "OpenAI", order: 20, aliases: ["gpt", "openai", "o1", "o3", "o4"], family_prefixes: ["gpt"], canonical_prefixes: ["gpt"] },
  { key: "claude", label: "Claude", order: 30, aliases: ["anthropic", "claude"], family_prefixes: ["claude"], canonical_prefixes: ["claude"] },
  { key: "deepseek", label: "DeepSeek", order: 40, aliases: ["deepseek"], family_prefixes: ["deepseek"], canonical_prefixes: ["deepseek"] },
  { key: "gemini", label: "Gemini", order: 50, aliases: ["gemini", "google", "google/"], family_prefixes: ["gemini"], canonical_prefixes: ["gemini"] },
  { key: "qwen", label: "Qwen", order: 60, aliases: ["alibaba", "dashscope", "qwen"], family_prefixes: ["qwen"], canonical_prefixes: ["qwen"] },
  { key: "glm", label: "GLM", order: 70, aliases: ["glm", "zhipu"], family_prefixes: ["glm"], canonical_prefixes: ["glm"] },
  { key: "kimi", label: "Kimi", order: 80, aliases: ["kimi", "moonshot"], family_prefixes: ["kimi"] },
  { key: "doubao", label: "Doubao", order: 90, aliases: ["doubao", "volcengine"], family_prefixes: ["doubao"] },
  { key: "ernie", label: "ERNIE", order: 100, aliases: ["ernie"] },
  { key: "baichuan", label: "Baichuan", order: 110, aliases: ["baichuan"] },
  { key: "minimax", label: "MiniMax", order: 120, aliases: ["hailuo", "minimax"] },
  { key: "stepfun", label: "StepFun", order: 130, aliases: ["step-", "stepaudio"] },
  { key: "wanx", label: "WanX", order: 140, aliases: ["wanx"] },
  { key: "grok", label: "Grok", order: 150, aliases: ["grok", "xai", "xai/"] },
  { key: "paddlepaddle", label: "PaddlePaddle", order: 160, aliases: ["paddleocr"] },
  { key: "microsoft", label: "Microsoft", order: 170, aliases: ["phi-"] },
  { key: "llama", label: "Llama", order: 180, aliases: ["llama", "meta", "meta/"], family_prefixes: ["llama"] },
  { key: "mistral", label: "Mistral", order: 190, aliases: ["mistral"], family_prefixes: ["mistral"] },
  { key: "custom", label: "自定义", order: 1000, aliases: ["custom"], family_prefixes: ["custom"], canonical_prefixes: ["custom"] },
];

export const preferredModelCategories = [
  "codex",
  "openai",
  "claude",
  "deepseek",
  "gemini",
  "qwen",
  "glm",
  "kimi",
  "doubao",
  "ernie",
  "baichuan",
  "minimax",
  "stepfun",
  "wanx",
  "grok",
  "paddlepaddle",
  "microsoft",
  "llama",
  "mistral",
  "custom",
];

export function modelCategoryDefinitionsFromData(data?: { plugins?: PluginDescriptor[]; providerAdapters?: AdapterDescriptor[] }) {
  return normalizeModelCategoryDefinitions([
    ...fallbackModelCategoryDefinitions,
    ...modelCategoryDefinitionsFromPlugins(data?.plugins),
    ...modelCategoryDefinitionsFromAdapters(data?.providerAdapters),
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
  return inferModelCategoryTextWithDefinitions(normalized, definitions);
}

export function modelCategoryLabelFromDefinitions(category: string, definitions?: ModelCategoryDefinition[]) {
  const normalized = category.trim().toLowerCase();
  const definition = categoryDefinitionsOrFallback(definitions).find((item) => item.key === normalized);
  return definition?.label || modelCategoryLabels[normalized] || category;
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
  return definitions && definitions.length > 0 ? normalizeModelCategoryDefinitions(definitions) : normalizeModelCategoryDefinitions(fallbackModelCategoryDefinitions);
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
