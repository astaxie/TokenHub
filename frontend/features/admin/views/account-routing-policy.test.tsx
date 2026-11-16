import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import type { AccountRoutingStrategy, Model, ModelRoute } from "../core/types";
import { ModelRoutingPolicyEditor } from "./model-routing-policy";

const model: Model = { id: "account-model", name: "account-model", family: "test", modality: "chat", status: "active" };
const routes: ModelRoute[] = [{ id: "account-route", model_name: model.name, provider_id: "provider", provider_model: "upstream", strategy: "quality", priority: 1, weight: 100, quality_score: 90, cost_score: 50, status: "active" }];

describe("Independent account routing", () => {
  it.each<AccountRoutingStrategy>(["quota_smart", "expiry_first", "least_used", "drain"])("saves %s without changing model selection", strategy => {
    const onSave = vi.fn();
    render(<ModelRoutingPolicyEditor model={model} routes={routes} data={emptyData()} loading={false} draggedRouteID="" onDragStart={vi.fn()} onDragEnd={vi.fn()} onDrop={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onSave={onSave} />);
    expect(screen.getByRole("button", { name: "应用策略" })).toBeDisabled();
    fireEvent.change(screen.getByLabelText("账号调度"), { target: { value: strategy } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(onSave).toHaveBeenCalledWith(model, expect.objectContaining({ strategy: "quality", account_strategy: strategy, routes: [{ route_id: "account-route", weight: 100, quality_score: 90, cost_score: 50 }] }));
  });

  it("loads saved account policy and allows restoring the existing order", () => {
    const onSave = vi.fn();
    const current = { ...model, metadata: { tokenhub_account_strategy: "drain" } };
    render(<ModelRoutingPolicyEditor model={current} routes={routes} data={emptyData()} loading={false} draggedRouteID="" onDragStart={vi.fn()} onDragEnd={vi.fn()} onDrop={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} onSave={onSave} />);
    expect(screen.getByLabelText("账号调度")).toHaveValue("drain");
    fireEvent.change(screen.getByLabelText("账号调度"), { target: { value: "default" } });
    fireEvent.click(screen.getByRole("button", { name: "应用策略" }));
    expect(onSave).toHaveBeenCalledWith(current, expect.objectContaining({ account_strategy: "default" }));
  });
});
