export const customModelTemplateID = "__custom__";

export type ReferenceModelTemplate = {
  name: string;
  category?: string;
  family: string;
  modality: string;
  context_window?: number;
  input_price_usd_per_1m?: number;
  cache_read_price_usd_per_1m?: number;
  output_price_usd_per_1m?: number;
  embedding_price_usd_per_1m?: number;
  input_modalities?: string[];
  output_modalities?: string[];
  capabilities?: string[];
  supported_parameters?: string[];
  metadata?: Record<string, string>;
};

const referenceModelSources = new Set(["tokenhub-standard-catalog", "public-provider-conf"]);

export function referenceModelTemplates<T extends ReferenceModelTemplate>(models: T[]) {
  return models
    .filter((model) => referenceModelSources.has(model.metadata?.source ?? ""))
    .filter((model) => model.metadata?.directory_role !== "external")
    .slice()
    .sort((left, right) => left.name.localeCompare(right.name));
}

export function filterReferenceModelTemplates<T extends ReferenceModelTemplate>(models: T[], query: string, category: string) {
  const normalizedQuery = query.trim().toLowerCase();
  return referenceModelTemplates(models).filter((model) => {
    if (category && category !== "all" && model.category !== category) return false;
    if (!normalizedQuery) return true;
    return [model.name, model.family, model.category, ...(model.capabilities ?? [])]
      .filter(Boolean)
      .join(" ")
      .toLowerCase()
      .includes(normalizedQuery);
  });
}

function formNumber(value?: number) {
  return value == null ? "" : String(value);
}

export function externalModelTemplateValues(model: ReferenceModelTemplate): Record<string, string> {
  return {
    name: model.name,
    category: model.category ?? "",
    family: model.family,
    modality: model.modality || "chat",
    context_window: formNumber(model.context_window),
    input_price_usd_per_1m: formNumber(model.input_price_usd_per_1m),
    cache_read_price_usd_per_1m: formNumber(model.cache_read_price_usd_per_1m),
    output_price_usd_per_1m: formNumber(model.output_price_usd_per_1m),
    embedding_price_usd_per_1m: formNumber(model.embedding_price_usd_per_1m),
    capabilities: (model.capabilities ?? []).join(", "),
    supported_parameters: (model.supported_parameters ?? []).join(", "),
    input_modalities: (model.input_modalities?.length ? model.input_modalities : ["text"]).join(", "),
    output_modalities: (model.output_modalities ?? []).join(", "),
    initial_provider_models: "",
    status: "active",
  };
}
