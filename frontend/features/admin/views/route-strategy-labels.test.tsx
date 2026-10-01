import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Model, ModelRoute, ModelRouteStrategy } from "../core/types";
import { emptyData } from "../domain/catalog";
import { routeStrategyLabel } from "../domain/formatting";
import { enumValueLabel, fieldValueLabel } from "../domain/labels";
import { routeSummaryStrategy } from "../domain/route-summary";
import { setActiveLanguage } from "../i18n/runtime";
import { routeConfig } from "../resources/provider-model-config";
import { RouteStrategyView } from "./model-catalog";

const strategies: ModelRouteStrategy[] = ["jev", "priority_weighted", "adaptive", "quality", "cost", "priority_only", "balanced"];
const model: Model = { id: "model", name: "test-model", family: "test", modality: "chat", status: "active" };
const route: ModelRoute = { id: "route", model_name: model.name, provider_id: "provider", provider_model: "upstream", priority: 1, weight: 100, status: "active" };
const locales = [
  { language: "zh-CN", configure: "配置", labels: ["Jev 智能路由", "固定比例", "自适应", "质量优先", "成本优先", "主备顺序", "综合评分"] },
  { language: "en", configure: "Settings", labels: ["Jev Smart Routing", "Fixed Ratio", "Adaptive", "Quality First", "Cost First", "Primary / Backup", "Combined score"] },
  { language: "ja", configure: "設定", labels: ["Jev スマートルーティング", "固定比率", "適応型", "品質優先", "コスト優先", "プライマリ / バックアップ", "総合スコア"] },
] as const;

describe.each(locales)("Routing terminology in $language", ({ language, configure, labels }) => {
  it.each(strategies)("uses the same %s name in summaries, editors and shared labels", (strategy) => {
    setActiveLanguage(language);
    const label = labels[strategies.indexOf(strategy)];
    const routes = [{ ...route, strategy }];
    const data = { ...emptyData(), models: [model], routes };
    render(<RouteStrategyView config={routeConfig()} data={data} loading={false} onCreate={vi.fn()} onOpenModels={vi.fn()} onOpenProviders={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReorder={vi.fn()} onSavePolicy={vi.fn()} />);
    expect(screen.getByRole("cell", { name: label, exact: true })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: configure, exact: true }));
    expect(within(screen.getByRole("dialog")).getByRole("tab", { name: label, exact: true })).toHaveAttribute("aria-selected", "true");
    expect(routeStrategyLabel(strategy)).toBe(label);
    expect(fieldValueLabel("strategy", strategy)).toBe(label);
    expect(enumValueLabel(strategy)).toBe(label);
  });
});

it("keeps missing strategies consistent and unknown values visible", () => {
  expect(routeSummaryStrategy([route])).toBe(routeStrategyLabel(undefined));
  expect(routeStrategyLabel("")).toBe(routeStrategyLabel(undefined));
  expect(routeSummaryStrategy([{ ...route, strategy: "future-strategy" }])).toBe("future-strategy");
});
