import { render, screen, fireEvent, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import type { Model, ModelRoute, SemanticRoutingPolicy } from "../core/types";
import { ModelRoutingPolicyEditor } from "./model-routing-policy";
import { readSemanticRoutingPolicy, defaultJevInstructions, initialJevPolicy } from "./semantic-routing-policy";

const model: Model = { id: "m", name: "test-model", family: "test", modality: "chat", status: "active" };
const routes: ModelRoute[] = [0, 1].map(index => ({ id: `r${index}`, model_name: "test-model", provider_id: `p${index}`, provider_model: `upstream-${index}`, priority: 1, weight: 100, quality_score: 50, cost_score: 50, status: "active", strategy: "quality" }));
const policy: SemanticRoutingPolicy = { mode: "enforce", min_confidence: 0.7, instructions: "Choose using configured criteria.", default_candidate_id: "r1", candidates: routes.map(route => ({ id: route.id, provider_id: route.provider_id, provider_model: route.provider_model, criteria: `Tasks for ${route.provider_model}` })) };
function renderEditor(current = model, currentRoutes = routes) {
  const save = vi.fn();
  render(<ModelRoutingPolicyEditor model={current} routes={currentRoutes} data={emptyData()} loading={false} draggedRouteID="" onDragStart={vi.fn()} onDragEnd={vi.fn()} onDrop={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onSave={save} />);
  return save;
}

describe("Jev model routing strategy", () => {
  it("selects Jev as a strategy and saves explicit model criteria", () => {
    const save = renderEditor();
    expect(screen.queryByLabelText("模型选择指令")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Jev 智能路由" }));
    expect(screen.getByLabelText("模型选择指令")).toHaveValue(defaultJevInstructions);
    expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("upstream-0 · p0 的适用条件"), { target: { value: "Simple extraction" } });
    fireEvent.change(screen.getByLabelText("upstream-1 · p1 的适用条件"), { target: { value: "Complex analysis" } });
    fireEvent.change(screen.getByLabelText("默认模型"), { target: { value: "r1" } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save).toHaveBeenCalledWith(model, expect.objectContaining({ strategy: "jev", semantic_routing: { mode: "enforce", min_confidence: 0.65, instructions: defaultJevInstructions, default_candidate_id: "r1", candidates: [expect.objectContaining({ id: "r0", criteria: "Simple extraction" }), expect.objectContaining({ id: "r1", criteria: "Complex analysis" })] } }));
  });
  it("loads a saved strategy and validates criteria, instructions, and confidence", () => {
    renderEditor({ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(policy) } }, routes.map(route => ({ ...route, strategy: "jev" })));
    expect(screen.getByRole("tab", { name: "Jev 智能路由" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByLabelText("默认模型")).toHaveValue("r1");
    for (const invalid of ["", "1.5", "-0.1"]) {
      fireEvent.change(screen.getByLabelText("最低置信度"), { target: { value: invalid } });
      expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
    }
    fireEvent.change(screen.getByLabelText("最低置信度"), { target: { value: "0" } });
    expect(screen.getByRole("button", { name: "应用策略" })).toBeEnabled();
    fireEvent.change(screen.getByLabelText("模型选择指令"), { target: { value: " " } });
    expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
  });
  it("keeps provider accounts grouped and restricts the default to selected candidates", () => {
    renderEditor(model, [...routes, { ...routes[0], id: "duplicate-account" }]);
    fireEvent.click(screen.getByRole("tab", { name: "Jev 智能路由" }));
    const panel = screen.getByRole("group", { name: "Jev 智能路由设置" });
    expect(within(panel).getAllByRole("checkbox")).toHaveLength(2);
    fireEvent.click(within(panel).getByRole("checkbox", { name: "upstream-0 · p0" }));
    expect(screen.getByLabelText("默认模型")).toHaveValue("r1");
    expect(within(screen.getByLabelText("默认模型")).queryByRole("option", { name: "upstream-0 · p0" })).not.toBeInTheDocument();
  });
  it("preserves configured fallback order when only confidence is edited", () => {
    const third = { ...routes[0], id: "r2", provider_id: "p2", provider_model: "upstream-2" };
    const ordered = [policy.candidates![0], { id: "r2", provider_id: "p2", provider_model: "upstream-2", criteria: "Third model tasks" }, policy.candidates![1]];
    const saved = { ...policy, default_candidate_id: "r0", candidates: ordered };
    const save = renderEditor({ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(saved) } }, [...routes, third].map(route => ({ ...route, strategy: "jev" })));
    fireEvent.change(screen.getByLabelText("最低置信度"), { target: { value: "0.8" } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save.mock.calls[0][1].semantic_routing.candidates).toEqual(ordered);
  });
  it("removes missing routes without reordering the remaining saved candidates", () => {
    const saved = { ...policy, candidates: [...policy.candidates!].reverse() };
    const current = { ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(saved) } };
    expect(initialJevPolicy(current, routes, emptyData()).candidates).toEqual(saved.candidates);
    expect(initialJevPolicy(current, [routes[1]], emptyData()).candidates).toEqual([saved.candidates[0]]);
  });
  it("switches to another strategy and disables Jev even after an invalid edit", () => {
    const save = renderEditor({ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(policy) } }, routes.map(route => ({ ...route, strategy: "jev" })));
    fireEvent.change(screen.getByLabelText("最低置信度"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("tab", { name: "固定比例" }));
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save.mock.calls[0][1]).toMatchObject({ strategy: "priority_weighted", semantic_routing: { mode: "off" } });
    expect(screen.queryByLabelText("模型选择指令")).not.toBeInTheDocument();
  });
  it("makes an active legacy overlay visible and removable", () => {
    const save = renderEditor({ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify({ mode: "enforce", min_confidence: 0.8 }) } });
    expect(screen.getByText(/此模型仍使用旧版 Jev/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save.mock.calls[0][1]).toMatchObject({ strategy: "quality", semantic_routing: { mode: "off" } });
  });
  it.each(["invalid", "null", '{"mode":"unknown","min_confidence":0.5}', '{"mode":"enforce","min_confidence":2}', '{"mode":"enforce","min_confidence":0.5,"candidates":"invalid"}', '{"mode":"enforce","min_confidence":0.5,"candidates":[null]}', '{"mode":"enforce","min_confidence":0.5,"instructions":42}'])("defaults malformed metadata to off: %s", raw => {
    expect(readSemanticRoutingPolicy({ ...model, metadata: { tokenhub_semantic_routing: raw } })).toEqual({ mode: "off", min_confidence: 0.65 });
  });
});
