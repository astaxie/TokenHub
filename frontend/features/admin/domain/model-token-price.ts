import type { Model } from "../core/types";
import { priceMetric } from "./catalog";
import { languageLocale, tx } from "../i18n/runtime";
import { followsProviderPricing } from "./model-pricing-source";

export function modelTokenPriceMetric(model: Model): string {
  if (followsProviderPricing(model)) return tx(model.pricing_available === false ? "来源价格暂不可用" : "跟随上游定价");
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
