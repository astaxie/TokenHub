import type { Model } from "../core/types";
import { followsProviderPricing } from "../domain/model-pricing-source";
import { priceMetric } from "../domain/catalog";
import { modelTokenPriceMetric, formatRetrievalUSD } from "../domain/model-token-price";
import { tx } from "../i18n/runtime";

export function ModelDirectoryPrice({ model }: { model: Model }) {
  if (followsProviderPricing(model) && model.pricing_available === false) return <><strong>{tx("来源价格暂不可用")}</strong><span>{tx("请联系管理员检查定价来源")}</span></>;
  if (followsProviderPricing(model)) return <><strong>{tx("跟随上游定价")}</strong><span>{tx("按请求开始时的来源价格计费")}</span></>;
  if (model.modality === "rerank" && model.metadata?.search_unit_price_usd?.trim()) {
    return <><strong>{formatRetrievalUSD(Number(model.metadata.search_unit_price_usd))}</strong><span>{tx("搜索单元价格 USD/次")}</span>{model.input_price_usd_per_1m != null ? <span>{tx("输入")} · {modelTokenPriceMetric(model)}</span> : null}</>;
  }
  return <><strong>{modelTokenPriceMetric(model)}</strong><span>{model.modality === "embedding" ? "Embedding" : tx("输入")}{model.modality !== "embedding" && model.modality !== "rerank" ? <> · {priceMetric(model.output_price_usd_per_1m)} {tx("输出")}</> : null}</span></>;
}
