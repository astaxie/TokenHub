import type { ProviderCatalogEntry } from "../core/types";

export type ProviderCatalogCategory = "provider" | "aggregator" | "local";

// Catalog identities come from data/builtin-plugins/providers/*/plugin.yaml.
// This is a service directory classification, not a model-family or protocol map.
const aggregatorIDs = new Set([
  "302ai", "abacus", "aihubmix", "amazon-bedrock", "anyapi", "azure",
  "azure-cognitive-services", "azure-openai", "bedrock", "burncloud", "cherryin",
  "cloudflare-ai-gateway", "cloudflare-workers-ai", "databricks", "deepinfra",
  "dmxapi", "fastrouter", "firepass", "fireworks", "fireworks-ai", "github-models",
  "google-vertex", "google-vertex-anthropic", "helicone", "huggingface", "jiekou",
  "kilo", "llmgateway", "merge-gateway", "modelscope", "nano-gpt", "neon",
  "novita-ai", "openrouter", "orcarouter", "poe", "ppinfra", "qiniu-ai",
  "requesty", "routing-run", "sap-ai-core", "siliconflow", "siliconflow-com",
  "snowflake-cortex", "tencent-tokenhub", "together", "togetherai", "tokenflux",
  "vercel", "vercel-ai-gateway", "zenmux",
]);

const localIDs = new Set([
  "atomic-chat", "kronk", "lm-studio", "lmstudio", "local", "ollama-local",
  "privatemode-ai", "vllm",
]);

const localAdapterTypes = new Set(["local", "lm-studio", "lmstudio", "ollama", "vllm"]);

type CategoryEntry = Pick<ProviderCatalogEntry, "id" | "type"> & Partial<Pick<ProviderCatalogEntry, "name" | "display_name" | "base_url">>;

export function providerCatalogCategory(entry: CategoryEntry): ProviderCatalogCategory {
  const id = normalizeCatalogIdentity(entry.id);
  if (id === "ollama-cloud") return "provider";
  // The built-in `ollama` catalog is Ollama Cloud; local Ollama integrations may
  // reuse that identity with the local adapter and a different endpoint.
  if (id === "ollama") {
    const isCloud = [entry.name, entry.display_name].some((value) => normalizeCatalogIdentity(value) === "ollama-cloud")
      || /^https?:\/\/(?:www\.)?ollama\.com(?:[/:]|$)/i.test(entry.base_url || "");
    return isCloud ? "provider" : "local";
  }
  if (aggregatorIDs.has(id)) return "aggregator";
  if (localIDs.has(id) || localAdapterTypes.has(normalizeCatalogIdentity(entry.type))) return "local";
  return "provider";
}

function normalizeCatalogIdentity(value: string | undefined) {
  return (value || "").trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
}
