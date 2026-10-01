import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AppData, Model, ModelRoute } from "../core/types";
import { emptyData } from "../domain/catalog";
import { routeConfig } from "../resources/provider-model-config";
import { RouteStrategyView } from "./model-catalog";

const models: Model[] = ["configured-model", "empty-model"].map((name) => ({ id: name, name, family: "test", modality: "chat", status: "active" }));
const routes: ModelRoute[] = [0, 1].map((index) => ({ id: `route-${index}`, model_name: models[0].name, provider_id: `provider-${index}`, provider_model: `upstream-${index}`, status: "active", priority: index + 1, weight: 75 + index, quality_score: 62, cost_score: 84, strategy: "priority_weighted", provider_resource_id: `account-${index}`, resource_group: "production", sticky_session: true, project_scope: "include", project_ids: ["project-one"], tags: ["retained"] }));
const data: AppData = { ...emptyData(), models, routes };

function props(currentData = data) {
  return { config: routeConfig(), data: currentData, loading: false, onClearError: vi.fn(), onCreate: vi.fn(), onOpenModels: vi.fn(), onOpenProviders: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onReorder: vi.fn(), onSavePolicy: vi.fn() };
}

function openEditor() {
  fireEvent.click(screen.getByRole("button", { name: "配置" }));
  return screen.getByRole("dialog", { name: "配置模型路由" });
}

describe("Route management", () => {
  it("starts with configured summaries and opens only the selected editor", () => {
    const callbacks = props();
    render(<RouteStrategyView {...callbacks} />);
    expect(screen.getByRole("tab", { name: /已配置/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("configured-model")).toBeVisible();
    expect(screen.queryByText("empty-model")).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "Jev 智能路由" })).not.toBeInTheDocument();
    expect(screen.getByText("含项目范围限制")).toBeVisible();
    expect(screen.getByText("线路需检查")).toBeVisible();
    const dialog = openEditor();
    expect(callbacks.onClearError).toHaveBeenCalledTimes(1);
    expect(within(dialog).getAllByRole("tab")).toHaveLength(7);
    expect(within(dialog).getByRole("button", { name: "应用策略" })).toBeDisabled();
    fireEvent.click(within(dialog).getAllByRole("button", { name: "编辑" })[0]);
    expect(callbacks.onEdit).toHaveBeenCalledWith(routes[0]);
    expect(callbacks.onSavePolicy).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    fireEvent.click(screen.getByRole("tab", { name: /全部模型/ }));
    expect(screen.getByText("empty-model")).toBeVisible();
  });

  it("includes an unrouted model when opened from its model directory link", () => {
    const callbacks = props();
    const view = render(<RouteStrategyView {...callbacks} />);
    view.rerender(<RouteStrategyView {...callbacks} initialQuery="empty-model" />);
    expect(screen.getByRole("tab", { name: /全部模型/ })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("empty-model")).toBeVisible();
    expect(screen.queryByText("configured-model")).not.toBeInTheDocument();
    const dialog = openEditor();
    expect(within(dialog).getByText("该统一模型还没有 Provider 线路")).toBeVisible();
    fireEvent.click(within(dialog).getByRole("button", { name: "添加线路" }));
    expect(callbacks.onCreate).toHaveBeenCalledWith(models[1]);
  });

  it("shows mixed saved strategies without normalizing them on open or close", () => {
    const callbacks = props({ ...data, routes: [routes[0], { ...routes[1], strategy: "quality" }] });
    render(<RouteStrategyView {...callbacks} />);
    expect(screen.getByText("策略不一致")).toBeVisible();
    const dialog = openEditor();
    expect(within(dialog).getByText("当前 Provider 线路策略不一致，应用后将统一为所选模型策略。")).toBeVisible();
    fireEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(callbacks.onSavePolicy).not.toHaveBeenCalled();
  });

  it("retains failed drafts and confirms closing or switching models", () => {
    const callbacks = props();
    const view = render(<RouteStrategyView {...callbacks} />);
    const dialog = openEditor();
    fireEvent.change(within(dialog).getAllByLabelText("流量权重")[0], { target: { value: "23" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "应用策略" }));
    expect(callbacks.onSavePolicy).toHaveBeenCalledWith(models[0], expect.objectContaining({ strategy: "priority_weighted", routes: [{ route_id: "route-0", weight: 23, quality_score: 62, cost_score: 84 }, { route_id: "route-1", weight: 76, quality_score: 62, cost_score: 84 }] }));
    view.rerender(<RouteStrategyView {...callbacks} error="Synthetic save failure" />);
    expect(within(dialog).getByRole("alert")).toHaveTextContent("Synthetic save failure");
    expect(within(dialog).getAllByLabelText("流量权重")[0]).toHaveValue(23);
    fireEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    const confirmation = screen.getByRole("dialog", { name: "放弃路由更改？" });
    fireEvent.click(within(confirmation).getByRole("button", { name: "取消" }));
    expect(within(dialog).getAllByLabelText("流量权重")[0]).toHaveValue(23);
    fireEvent.change(within(dialog).getByLabelText("切换路由模型"), { target: { value: models[1].name } });
    fireEvent.click(screen.getByRole("button", { name: "放弃更改" }));
    const next = screen.getByRole("dialog", { name: "配置模型路由" });
    expect(within(next).getByRole("heading", { name: "empty-model" })).toBeVisible();
    expect(within(next).getByText("该统一模型还没有 Provider 线路")).toBeVisible();
  });

  it("refreshes saved policy values and clears the close confirmation after success", () => {
    const callbacks = props();
    const view = render(<RouteStrategyView {...callbacks} />);
    const dialog = openEditor();
    fireEvent.change(within(dialog).getAllByLabelText("流量权重")[0], { target: { value: "23" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "应用策略" }));
    view.rerender(<RouteStrategyView {...callbacks} data={{ ...data, routes: [{ ...routes[0], weight: 23 }, routes[1]] }} />);
    expect(within(dialog).getByRole("button", { name: "应用策略" })).toBeDisabled();
    fireEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("keeps the selected editor after deleting the last route until explicitly closed", () => {
    const currentData: AppData = {
      ...data,
      models: [models[1], models[0]],
      routes: [routes[0]],
      providers: [{ id: routes[0].provider_id, name: "Test Provider", type: "mock", priority: 1, status: "active", healthy: true }],
      providerModels: [{ id: "provider-model", provider_id: routes[0].provider_id, upstream_model: routes[0].provider_model, status: "active" }],
    };
    const callbacks = props(currentData);
    const view = render(<RouteStrategyView {...callbacks} />);
    const dialog = openEditor();
    fireEvent.click(within(dialog).getByTitle("删除"));
    expect(callbacks.onDelete).toHaveBeenCalledWith(routes[0]);
    const emptyRoutes = { ...currentData, routes: [] };
    view.rerender(<RouteStrategyView {...callbacks} data={emptyRoutes} />);
    expect(dialog).toBeVisible();
    expect(within(dialog).getByRole("heading", { name: models[0].name })).toBeVisible();
    expect(within(dialog).getByText("该统一模型还没有 Provider 线路")).toBeVisible();
    expect(screen.queryByRole("heading", { name: "还没有路由策略" })).not.toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "添加线路" }));
    expect(callbacks.onCreate).toHaveBeenLastCalledWith(models[0]);

    fireEvent.click(within(dialog).getByRole("button", { name: "关闭" }));
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "还没有路由策略" })).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "为模型添加路由" }));
    expect(callbacks.onCreate).toHaveBeenLastCalledWith(models[1]);
    view.rerender(<RouteStrategyView {...callbacks} data={emptyRoutes} loading />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    view.rerender(<RouteStrategyView {...callbacks} data={{ ...currentData, routes: [{ ...routes[0], model_name: models[1].name }] }} />);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("cell", { name: new RegExp(models[1].name) })).toBeVisible();
  });

  it.each([
    ["jev", "Jev 智能路由"],
    ["priority_weighted", "固定比例"],
    ["adaptive", "自适应"],
    ["quality", "质量优先"],
    ["cost", "成本优先"],
    ["priority_only", "主备顺序"],
    ["balanced", "综合评分"],
  ])("saves the %s strategy while preserving route scores and Jev candidate settings", (strategy, label) => {
    const semantic = { mode: "off", min_confidence: 0.8, instructions: "Choose a model using the configured criteria.", default_candidate_id: routes[1].id, candidates: [...routes].reverse().map((route) => ({ id: route.id, provider_id: route.provider_id, provider_model: route.provider_model, criteria: `Tasks for ${route.provider_model}` })) };
    const savedModel = { ...models[0], metadata: { tokenhub_semantic_routing: JSON.stringify(semantic) } };
    const currentData = { ...data, models: [savedModel, models[1]], routes: routes.map((route) => ({ ...route, strategy: strategy === "balanced" ? "priority_weighted" : "balanced" })) };
    const callbacks = props(currentData);
    render(<RouteStrategyView {...callbacks} />);
    const dialog = openEditor();
    fireEvent.click(within(dialog).getByRole("tab", { name: label }));
    if (["quality", "cost", "balanced"].includes(strategy)) expect(within(dialog).getByText("质量和成本评分由管理员维护，不会自动读取实时价格或模型评测。")).toBeVisible();
    expect(within(dialog).getByRole("button", { name: "应用策略" })).toBeEnabled();
    fireEvent.click(within(dialog).getByRole("button", { name: "应用策略" }));
    expect(callbacks.onSavePolicy).toHaveBeenCalledWith(savedModel, {
      strategy,
      semantic_routing: { ...semantic, mode: strategy === "jev" ? "enforce" : "off" },
      routes: routes.map((route) => ({ route_id: route.id, weight: route.weight, quality_score: route.quality_score, cost_score: route.cost_score })),
    });
  });
});
