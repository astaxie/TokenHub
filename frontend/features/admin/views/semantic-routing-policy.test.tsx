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

function classifierData() {
  const models = [model, ...["router-small", "router-disabled", "router-unrouted", "router-jev", "router-retired-jev"].map(name => ({ ...model, id: name, name, status: name === "router-disabled" ? "disabled" : "active" }))];
  const classifierRoutes = ["router-small", "router-disabled", "router-jev", "router-retired-jev"].map(name => ({ ...routes[0], id: `route-${name}`, model_name: name, strategy: name === "router-jev" ? "jev" : "quality" }));
  // A disabled Jev route does not count against a classifier, as on the server.
  const retiredJev = { ...routes[0], id: "route-router-retired-jev-old", model_name: "router-retired-jev", strategy: "jev", status: "disabled" };
  return { ...emptyData(), models, routes: [...routes, ...classifierRoutes, retiredJev] };
}
function renderSavedEvaluator(saved: SemanticRoutingPolicy) {
  const save = vi.fn();
  render(<ModelRoutingPolicyEditor model={{ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(saved) } }} routes={routes.map(route => ({ ...route, strategy: "jev" }))} data={classifierData()} loading={false} draggedRouteID="" onDragStart={vi.fn()} onDragEnd={vi.fn()} onDrop={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onSave={save} />);
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
  it("configures a TokenHub model as the classifier and drops it when returning to TypeSafe", () => {
    const save = vi.fn();
    const data = classifierData();
    render(<ModelRoutingPolicyEditor model={{ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(policy) } }} routes={routes.map(route => ({ ...route, strategy: "jev" }))} data={data} loading={false} draggedRouteID="" onDragStart={vi.fn()} onDragEnd={vi.fn()} onDrop={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onSave={save} />);
    fireEvent.change(screen.getByLabelText("分类器"), { target: { value: "model" } });
    expect(screen.queryByLabelText("最低置信度")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
    const classifier = screen.getByLabelText("分类模型");
    expect(within(classifier).queryByRole("option", { name: "test-model" })).not.toBeInTheDocument();
    for (const unusable of ["router-disabled", "router-unrouted", "router-jev"]) expect(within(classifier).queryByRole("option", { name: unusable })).not.toBeInTheDocument();
    expect(within(classifier).getByRole("option", { name: "router-retired-jev" })).toBeInTheDocument();
    fireEvent.change(classifier, { target: { value: "router-small" } });
    fireEvent.change(screen.getByLabelText("分类超时（毫秒）"), { target: { value: "50" } });
    expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("分类超时（毫秒）"), { target: { value: "2500" } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save).toHaveBeenLastCalledWith(expect.anything(), expect.objectContaining({ semantic_routing: expect.objectContaining({ evaluator: "model", classifier_model: "router-small", classifier_timeout_ms: 2500 }) }));
    fireEvent.change(screen.getByLabelText("分类器"), { target: { value: "typesafe" } });
    expect(screen.getByLabelText("最低置信度")).toHaveValue(0.7);
    fireEvent.change(screen.getByLabelText("模型选择指令"), { target: { value: "Updated criteria." } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save).toHaveBeenCalledTimes(2);
    const saved = save.mock.lastCall?.[1].semantic_routing;
    expect(saved).not.toHaveProperty("evaluator");
    expect(saved).not.toHaveProperty("classifier_model");
    expect(saved).not.toHaveProperty("classifier_timeout_ms");
  });
  it("reads a saved model evaluator and rejects malformed classifier fields", () => {
    const saved = { ...policy, evaluator: "model", classifier_model: "router-small", classifier_timeout_ms: 2000 };
    expect(readSemanticRoutingPolicy({ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify(saved) } })).toMatchObject({ evaluator: "model", classifier_model: "router-small" });
    for (const invalid of [{ evaluator: "oracle" }, { classifier_model: 7 }, { classifier_timeout_ms: 1.5 }]) {
      expect(readSemanticRoutingPolicy({ ...model, metadata: { tokenhub_semantic_routing: JSON.stringify({ ...saved, ...invalid }) } })).toEqual({ mode: "off", min_confidence: 0.65 });
    }
  });
  it("reloads a saved model evaluator with its classifier and timeout", () => {
    const save = renderSavedEvaluator({ ...policy, evaluator: "model", classifier_model: "router-small", classifier_timeout_ms: 2000 });
    expect(screen.getByLabelText("分类器")).toHaveValue("model");
    expect(screen.getByLabelText("分类模型")).toHaveValue("router-small");
    expect(screen.getByLabelText("分类超时（毫秒）")).toHaveValue(2000);
    expect(screen.queryByLabelText("最低置信度")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("模型选择指令"), { target: { value: "Updated criteria." } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save).toHaveBeenCalledWith(expect.anything(), expect.objectContaining({ semantic_routing: expect.objectContaining({ evaluator: "model", classifier_model: "router-small", classifier_timeout_ms: 2000 }) }));
  });
  it("marks a saved classifier that is no longer usable and blocks saving until another is chosen", () => {
    renderSavedEvaluator({ ...policy, evaluator: "model", classifier_model: "router-jev" });
    expect(screen.getByLabelText("分类模型")).toHaveValue("router-jev");
    expect(screen.getByRole("option", { name: "router-jev（不可用）" })).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("所选分类模型已停用");
    fireEvent.change(screen.getByLabelText("模型选择指令"), { target: { value: "Updated criteria." } });
    expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("分类模型"), { target: { value: "router-small" } });
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "应用策略" })).toBeEnabled();
  });
});
