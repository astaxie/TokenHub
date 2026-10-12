import type { ModelRouteStrategy } from "../core/types";
import { tx } from "../i18n/runtime";

// All views use these labels for persisted strategies, including Jev.
export const routeStrategyLabelKeys: Record<ModelRouteStrategy, string> = {
  jev: "Jev 智能路由",
  priority_weighted: "固定比例",
  adaptive: "自适应",
  quality: "质量优先",
  cost: "成本优先",
  priority_only: "主备顺序",
  balanced: "综合评分",
};

export function routeStrategyLabelKey(value?: string) {
  const strategy = value || "balanced";
  return Object.hasOwn(routeStrategyLabelKeys, strategy) ? routeStrategyLabelKeys[strategy as ModelRouteStrategy] : strategy;
}

export function routeStrategyLabel(value?: string) {
  return tx(routeStrategyLabelKey(value));
}
