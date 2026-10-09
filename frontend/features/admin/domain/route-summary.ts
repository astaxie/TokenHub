import type { AppData, ModelRoute } from "../core/types";
import { routeStrategyLabel } from "./route-strategy";
import { tx } from "../i18n/runtime";

export function routeSummaryStrategy(routes: ModelRoute[]) {
  const strategies = new Set(routes.map((route) => route.strategy || "balanced"));
  if (!strategies.size) return tx("未配置");
  if (strategies.size > 1) return tx("策略不一致");
  const strategy = strategies.values().next().value!;
  return routeStrategyLabel(strategy);
}

export function routeSummarySources(routes: ModelRoute[], data: AppData) {
  return [...new Set(routes.map((route) => data.providers.find((provider) => provider.id === route.provider_id)?.name || route.provider_id))];
}
