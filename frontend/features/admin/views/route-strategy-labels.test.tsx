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
    expect(screen.getByRole("cell", { name: label })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: configure }));
    expect(within(screen.getByRole("dialog")).getByRole("tab", { name: label })).toHaveAttribute("aria-selected", "true");
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

it.each(["balanced", "priority_weighted"] as const)("requires explicit selection before replacing an unknown strategy with %s", (strategy) => {
  setActiveLanguage("zh-CN");
  const unknown = { ...route, strategy: "future-strategy", weight: 75, quality_score: 80, cost_score: 60 };
  const data = { ...emptyData(), models: [model], routes: [unknown] };
  const save = vi.fn();
  render(<RouteStrategyView config={routeConfig()} data={data} loading={false} onCreate={vi.fn()} onOpenModels={vi.fn()} onOpenProviders={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReorder={vi.fn()} onSavePolicy={save} />);
  fireEvent.click(screen.getByRole("button", { name: "配置" }));
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText("此模型包含未知路由策略：future-strategy。请选择受支持的策略后再保存。")).toBeVisible();
  expect(within(dialog).getByRole("button", { name: "应用策略" })).toBeDisabled();
  expect(within(dialog).getAllByRole("tab")).toHaveLength(7);
  expect(within(dialog).queryByRole("tab", { selected: true })).not.toBeInTheDocument();
  expect(within(dialog).queryByRole("spinbutton")).not.toBeInTheDocument();
  expect(save).not.toHaveBeenCalled();
  fireEvent.click(within(dialog).getByRole("tab", { name: routeStrategyLabel(strategy) }));
  expect(within(dialog).queryByText(/此模型包含未知路由策略/)).not.toBeInTheDocument();
  expect(within(dialog).getByRole("button", { name: "应用策略" })).toBeEnabled();
  fireEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
  expect(screen.getByRole("dialog", { name: "放弃路由更改？" })).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(within(dialog).getByRole("tab", { name: routeStrategyLabel(strategy) })).toHaveAttribute("aria-selected", "true");
  fireEvent.click(within(dialog).getByRole("button", { name: "应用策略" }));
  expect(save).toHaveBeenCalledWith(model, expect.objectContaining({ strategy, routes: [{ route_id: route.id, weight: 75, quality_score: 80, cost_score: 60 }] }));
});

it.each(["balanced", "jev"])("keeps mixed %s and unknown strategies unselected until explicitly replaced", (strategy) => {
  setActiveLanguage("zh-CN");
  const routes = [{ ...route, strategy }, { ...route, id: "unknown", strategy: "future-strategy" }, { ...route, id: "unknown-two", strategy: "another-strategy" }];
  const data = { ...emptyData(), models: [model], routes };
  render(<RouteStrategyView config={routeConfig()} data={data} loading={false} onCreate={vi.fn()} onOpenModels={vi.fn()} onOpenProviders={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onReorder={vi.fn()} onSavePolicy={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "配置" }));
  const dialog = within(screen.getByRole("dialog"));
  expect(dialog.getByText("当前 Provider 线路策略不一致，应用后将统一为所选模型策略。")).toBeVisible();
  expect(dialog.getByText("此模型包含未知路由策略：future-strategy, another-strategy。请选择受支持的策略后再保存。")).toBeVisible();
  expect(dialog.queryByRole("tab", { selected: true })).not.toBeInTheDocument();
  expect(dialog.queryByRole("spinbutton")).not.toBeInTheDocument();
  expect(dialog.queryByLabelText("模型选择指令")).not.toBeInTheDocument();
  expect(dialog.getByRole("button", { name: "应用策略" })).toBeDisabled();
});
