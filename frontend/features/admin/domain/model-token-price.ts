import type { Model } from "../core/types";
import { priceMetric } from "./catalog";
import { languageLocale } from "../i18n/runtime";

export function modelTokenPriceMetric(model: Model): string {
  const price = model.modality === "embedding" ? model.embedding_price_usd_per_1m : model.input_price_usd_per_1m;
  const retrieval = model.modality === "embedding" || model.modality === "rerank";
  if (retrieval && (price ?? 0) === 0 && model.metadata?.retrieval_pricing_confirmed === "true") {
    return `${formatRetrievalUSD(0)}/Mt`;
  }
  return retrieval && price != null && price > 0 ? `${formatRetrievalUSD(price)}/Mt` : priceMetric(price);
}

export function formatRetrievalUSD(value: number): string {
  if (!Number.isFinite(value) || value < 0) return "—";
  return new Intl.NumberFormat(languageLocale(), { style: "currency", currency: "USD", minimumFractionDigits: 6, maximumFractionDigits: 6 }).format(value);
}
