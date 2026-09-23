import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Model, ModelRoute, SemanticRoutingPolicy } from "../core/types";
import { emptyData } from "../domain/catalog";
import { RouteModelCard } from "./model-catalog";
import { initialJevPolicy } from "./semantic-routing-policy";

const routes: ModelRoute[] = [0, 1, 2].map(index => ({ id: `r${index}`, model_name: "auto-chat", provider_id: `p${index}`, provider_model: `upstream-${index}`, priority: 1, weight: 100, status: "active", strategy: "jev" }));
const policy: SemanticRoutingPolicy = { mode: "enforce", min_confidence: 0.7, instructions: "Choose by task.", default_candidate_id: "r1", candidates: [routes[0], routes[2], routes[1]].map(route => ({ id: route.id, provider_id: route.provider_id, provider_model: route.provider_model, criteria: `Tasks for ${route.provider_model}` })) };
const model: Model = { id: "m", name: "auto-chat", family: "test", modality: "chat", status: "active", metadata: { tokenhub_semantic_routing: JSON.stringify(policy) } };

describe("Jev candidate reconciliation after route edits", () => {
  it.each([
    { provider_model: "replacement" },
    { provider_id: "replacement-provider" },
  ])("refreshes current option data while retaining order: %j", change => {
    const updated = routes.map(route => route.id === "r1" ? { ...route, ...change } : route);
    const target = updated[1];
    const data = { ...emptyData(), providerModels: [{ id: "catalog", provider_id: target.provider_id, upstream_model: target.provider_model, status: "active", metadata: { routing_description: "Replacement model tasks" } }] };
    const reconciled = initialJevPolicy(model, updated, data);
    expect(reconciled.candidates).toEqual([policy.candidates![0], policy.candidates![1], { id: "r1", provider_id: target.provider_id, provider_model: target.provider_model, criteria: "Replacement model tasks" }]);
    expect(reconciled.default_candidate_id).toBe("r1");
  });

  it.each([
    { provider_model: "replacement" },
    { provider_id: "replacement-provider" },
  ])("refreshes the mounted editor before a confidence-only save: %j", change => {
    const save = vi.fn();
    const updated = routes.map(route => route.id === "r1" ? { ...route, ...change } : route);
    const target = updated[1];
    const data = { ...emptyData(), providerModels: [{ id: "catalog", provider_id: target.provider_id, upstream_model: target.provider_model, status: "active", metadata: { routing_description: "Replacement model tasks" } }] };
    const card = (currentRoutes: ModelRoute[]) => <RouteModelCard model={model} data={{ ...data, routes: currentRoutes }} loading={false} draggedRouteID="" onDragStart={vi.fn()} onDragEnd={vi.fn()} onDrop={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onCreate={vi.fn()} onSavePolicy={save} />;
    const view = render(card(routes));
    expect(screen.getByLabelText("upstream-1 · p1 的适用条件")).toHaveValue("Tasks for upstream-1");
    view.rerender(card(updated));
    expect(screen.getByLabelText(`${target.provider_model} · ${target.provider_id} 的适用条件`)).toHaveValue("Replacement model tasks");
    expect(screen.getByRole("button", { name: "应用策略" })).toBeEnabled();
    fireEvent.change(screen.getByLabelText("最低置信度"), { target: { value: "0.8" } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save).toHaveBeenCalledWith(model, expect.objectContaining({ semantic_routing: { ...initialJevPolicy(model, updated, data), min_confidence: 0.8 } }));
    expect(save.mock.calls[0][1].semantic_routing.candidates.map((candidate: { provider_model: string }) => candidate.provider_model)).toEqual(["upstream-0", "upstream-2", target.provider_model]);
  });

  it("allows replacement criteria to be entered without toggling the candidate", () => {
    const updated = routes.map(route => route.id === "r1" ? { ...route, provider_model: "replacement" } : route);
    const save = vi.fn();
    render(<RouteModelCard model={model} data={{ ...emptyData(), routes: updated }} loading={false} draggedRouteID="" onDragStart={vi.fn()} onDragEnd={vi.fn()} onDrop={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onCreate={vi.fn()} onSavePolicy={save} />);
    expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("replacement · p1 的适用条件"), { target: { value: "Replacement tasks" } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(save.mock.calls[0][1].semantic_routing.candidates[2]).toEqual({ id: "r1", provider_id: "p1", provider_model: "replacement", criteria: "Replacement tasks" });
  });
});
