import type { AppData, ModelRoute } from "../core/types";
import { tx } from "../i18n/runtime";

const strategyLabels: Record<string, string> = {
  jev: "Jev 智能路由",
  priority_weighted: "固定比例",
  adaptive: "自适应",
  quality: "质量优先",
  cost: "成本优先",
  priority_only: "主备顺序",
  balanced: "综合评分",
};

export function routeSummaryStrategy(routes: ModelRoute[]) {
  const strategies = new Set(routes.map((route) => route.strategy || "balanced"));
  if (!strategies.size) return tx("未配置");
  if (strategies.size > 1) return tx("策略不一致");
  const strategy = strategies.values().next().value!;
  return tx(strategyLabels[strategy] ?? strategy);
}

export function routeSummarySources(routes: ModelRoute[], data: AppData) {
  return [...new Set(routes.map((route) => data.providers.find((provider) => provider.id === route.provider_id)?.name || route.provider_id))];
}
