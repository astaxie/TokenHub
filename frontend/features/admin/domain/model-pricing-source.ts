import type { AppData, FieldConfig, Model, ProviderModel } from "../core/types";

export function followsProviderPricing(model: Pick<Model, "metadata">) {
  return model.metadata?.pricing_mode === "provider";
}

export function eligiblePricingSources(data: Pick<AppData, "providers" | "providerModels">) {
  const activeProviders = new Set(data.providers.filter(provider => provider.status === "active").map(provider => provider.id));
  return data.providerModels.filter(model => activeProviders.has(model.provider_id) && model.status === "active" && model.modality === "chat" && model.metadata?.pricing_status === "configured");
}

export function pricingSourceLabel(source: ProviderModel, data: Pick<AppData, "providers">) {
  const provider = data.providers.find(item => item.id === source.provider_id);
  return `${provider?.name || source.provider_id} / ${source.upstream_model} (${source.provider_id})`;
}

export const customModelPriceFields = new Set([
  "input_price_usd_per_1m", "output_price_usd_per_1m", "embedding_price_usd_per_1m", "cache_read_price_usd_per_1m",
  "cache_write_price_usd_per_1m", "cache_write_5m_price_usd_per_1m", "cache_write_1h_price_usd_per_1m", "pricing_periods",
]);

export function withPricingSourceFields(fields: FieldConfig[]): FieldConfig[] {
  return fields.map(field => customModelPriceFields.has(field.key) ? {
    ...field,
    visible: (values, data, currentUser) => (values.modality !== "chat" || values.pricing_mode !== "provider") && (field.visible?.(values, data, currentUser) ?? true),
  } : field);
}
