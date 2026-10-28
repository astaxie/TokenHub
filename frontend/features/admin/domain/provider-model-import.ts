import { type AppData, type ProviderModel } from "../core/types";
import { modelIsInDirectory } from "./entities";
import { externalModelTemplateValues } from "./model-create-wizard";
import { providerModelSelectionValue } from "./provider-model-selection";

export type ProviderImportDraft = { input: string; output: string; searchUnit: string; audioSecond?: string };
export type ProviderImportCandidate = { model: ProviderModel; collision: boolean; costRequired: boolean; canConfirmZeroCost: boolean };

export function providerImportCandidates(data: AppData, providerID: string): ProviderImportCandidate[] {
  if (!data.providers.some((provider) => provider.id === providerID && provider.status === "active")) return [];
  const occupied = new Set(data.models.filter((model) => modelIsInDirectory(model, data)).map((model) => model.name));
  for (const route of data.routes) occupied.add(route.model_name);
  const seen = new Set<string>();
  return data.providerModels.filter((model) => {
    if (model.provider_id !== providerID || model.status !== "active" || model.call_supported === false || !model.upstream_model.trim()) return false;
    if (model.metadata?.call_support === "unsupported" || model.metadata?.lifecycle_status === "retired" || seen.has(model.upstream_model)) return false;
    seen.add(model.upstream_model);
    return true;
  }).map((model) => {
    const costRequired = model.metadata?.pricing_status === "unverified" && model.metadata?.retrieval_pricing_confirmed !== "true"
      && ((model.input_price_usd_per_1m ?? 0) <= 0 && (model.output_price_usd_per_1m ?? 0) <= 0 || model.metadata?.catalog_price_reference === "true");
    const zeroCost = [model.input_price_usd_per_1m, model.output_price_usd_per_1m, model.cache_read_price_usd_per_1m, model.cache_write_price_usd_per_1m, model.cache_write_5m_price_usd_per_1m, model.cache_write_1h_price_usd_per_1m].every((price) => price == null || price === 0)
      && !model.pricing_periods?.length && !model.metadata?.search_unit_price_usd && !model.metadata?.audio_price_usd_per_second;
    return { model, collision: occupied.has(model.upstream_model), costRequired, canConfirmZeroCost: costRequired && zeroCost && model.metadata?.billing_mode === "subscription" };
  }).sort((left, right) => left.model.upstream_model.localeCompare(right.model.upstream_model));
}

export function validImportPrice(value: string) {
  return value.trim() !== "" && Number.isFinite(Number(value)) && Number(value) >= 0;
}

export function validProviderImportDraft(draft?: ProviderImportDraft, modality?: string) {
  return Boolean(draft && validImportPrice(draft.input) && validImportPrice(draft.output) && (!draft.searchUnit.trim() || validImportPrice(draft.searchUnit)) && (modality !== "audio" || validImportPrice(draft.audioSecond ?? "")));
}

export function providerImportValues(model: ProviderModel, draft: ProviderImportDraft): Record<string, string> {
  const values = externalModelTemplateValues({ ...model, name: model.upstream_model, family: model.family || model.category || model.upstream_model, modality: model.modality || "chat" });
  // Provider costs never become public prices, including cache and time-of-day rates.
  for (const key of Object.keys(values)) {
    if (key.includes("price") || key === "pricing_periods") values[key] = "";
  }
  return {
    ...values,
    display_name: model.display_name || "",
    input_price_usd_per_1m: draft.input.trim(),
    output_price_usd_per_1m: draft.output.trim(),
    embedding_price_usd_per_1m: model.modality === "embedding" ? draft.input.trim() : "",
    search_unit_price_usd: draft.searchUnit.trim(),
    retrieval_pricing_confirmed: model.modality === "embedding" || model.modality === "rerank" ? "true" : "false",
    initial_provider_models: providerModelSelectionValue(model.provider_id, model.upstream_model),
  };
}

export function providerImportMetadata(model: ProviderModel, draft?: ProviderImportDraft): Record<string, string> {
  const metadata: Record<string, string> = { source: "provider-model-import", directory_role: "external", pricing_status: "configured" };
  for (const key of ["description", "documentation_url", "model_version", "knowledge_cutoff", "endpoints", "max_output_tokens", "reasoning_effort_options", "reasoning_effort_supported", "reasoning_default", "dimensions", "supported_dimensions", "default_dimensions", "encoding_formats"]) {
    if (model.metadata?.[key]) metadata[key] = model.metadata[key];
  }
  if (model.modality === "audio" && draft && validImportPrice(draft.audioSecond ?? "")) metadata.audio_price_usd_per_second = draft.audioSecond!.trim();
  return metadata;
}
