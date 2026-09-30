import { useState } from "react";
import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Model, ModelRoute } from "../core/types";
import { emptyData } from "../domain/catalog";
import { routeConfig } from "../resources/provider-model-config";
import { RouteConfigurationDialog } from "./route-management";
import { EditModal } from "./settings-table";

const model: Model = { id: "ui-model", name: "ui-model", family: "test", modality: "chat", status: "active" };
const route: ModelRoute = { id: "ui-route", model_name: model.name, provider_id: "ui-provider", provider_model: "ui-upstream", priority: 1, weight: 100, quality_score: 50, cost_score: 50, strategy: "priority_only", status: "active" };

function NestedHarness({ loading = false, error = "" }: { loading?: boolean; error?: string }) {
  const [routeOpen, setRouteOpen] = useState(true);
  const [childOpen, setChildOpen] = useState(false);
  return <>
    <button type="button">Background action</button>
    {routeOpen ? <RouteConfigurationDialog model={model} models={[model]} loading={false} onClose={() => setRouteOpen(false)} onSelect={vi.fn()} onCreate={() => setChildOpen(true)}>{() => <button type="button" onClick={() => setChildOpen(true)}>Edit test route</button>}</RouteConfigurationDialog> : null}
    {childOpen ? <EditModal state={{ config: routeConfig(), item: route }} data={emptyData()} api={{ baseURL: "http://localhost:8080", adminToken: "synthetic" }} loading={loading} submitError={error} onClose={() => setChildOpen(false)} onSave={vi.fn()} /> : null}
  </>;
}

describe("Nested route editor focus", () => {
  it("focuses and traps the child editor and restores its trigger after Escape", async () => {
    const user = userEvent.setup();
    vi.spyOn(HTMLElement.prototype, "getClientRects").mockImplementation(() => [{ width: 1, height: 1 }] as unknown as DOMRectList);
    render(<NestedHarness />);
    const parent = screen.getByRole("dialog", { name: "配置模型路由" });
    expect(parent).toHaveFocus();
    const edit = within(parent).getByRole("button", { name: "Edit test route" });
    await user.click(edit);
    const child = screen.getByRole("dialog", { name: "路由策略" });
    expect(child).toHaveFocus();
    screen.getByRole("button", { name: "Background action" }).focus();
    expect(child).toHaveFocus();
    const save = within(child).getByRole("button", { name: "保存" });
    save.focus();
    await user.tab();
    expect(within(child).getByTitle("关闭")).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "路由策略" })).not.toBeInTheDocument();
    expect(parent).toBeInTheDocument();
    expect(edit).toHaveFocus();
  });

  it("keeps a saving child open and displays its save error inside the child", async () => {
    const user = userEvent.setup();
    const view = render(<NestedHarness loading error="Synthetic route save failed" />);
    await user.click(screen.getByRole("button", { name: "Edit test route" }));
    const child = screen.getByRole("dialog", { name: "路由策略" });
    expect(within(child).getByRole("alert")).toHaveTextContent("Synthetic route save failed");
    await user.keyboard("{Escape}");
    expect(child).toBeInTheDocument();
    expect(within(child).getByRole("button", { name: "取消" })).toBeDisabled();
    view.rerender(<NestedHarness error="Synthetic route save failed" />);
    fireEvent.keyDown(child, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: "路由策略" })).not.toBeInTheDocument();
    expect(screen.getByRole("dialog", { name: "配置模型路由" })).toBeInTheDocument();
  });

  it("recovers focus after disabling the save button silently blurs it", async () => {
    const user = userEvent.setup();
    const view = render(<NestedHarness />);
    await user.click(screen.getByRole("button", { name: "Edit test route" }));
    const child = screen.getByRole("dialog", { name: "路由策略" });
    fireEvent.change(within(child).getByRole("spinbutton", { name: /^流量权重/ }), { target: { value: "25" } });
    const save = within(child).getByRole("button", { name: "保存" });
    save.focus();
    save.blur();
    expect(document.body).toHaveFocus();
    view.rerender(<NestedHarness loading />);
    expect(child).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog", { name: "放弃路由更改？" })).not.toBeInTheDocument();
    view.rerender(<NestedHarness error="Synthetic route save failed" />);
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog", { name: "放弃路由更改？" })).toHaveFocus();
  });

  it.each(["Escape", "close button", "cancel button"])("confirms dirty route dismissal from %s and keeps drafts when canceled", async (action) => {
    const user = userEvent.setup();
    render(<NestedHarness error="Synthetic route save failed" />);
    const parent = screen.getByRole("dialog", { name: "配置模型路由" });
    const edit = within(parent).getByRole("button", { name: "Edit test route" });
    await user.click(edit);
    const child = screen.getByRole("dialog", { name: "路由策略" });
    const weight = within(child).getByRole("spinbutton", { name: /^流量权重/ });
    fireEvent.change(weight, { target: { value: "25" } });
    if (action === "Escape") await user.keyboard("{Escape}");
    else if (action === "close button") await user.click(within(child).getByTitle("关闭"));
    else await user.click(within(child).getByRole("button", { name: "取消" }));
    const confirmation = screen.getByRole("dialog", { name: "放弃路由更改？" });
    expect(confirmation).toHaveFocus();
    parent.focus();
    expect(confirmation).toHaveFocus();
    await user.keyboard("{Escape}");
    expect(confirmation).not.toBeInTheDocument();
    expect(child).toBeInTheDocument();
    expect(child.contains(document.activeElement)).toBe(true);
    expect(weight).toHaveValue(25);
    expect(within(child).getByRole("alert")).toHaveTextContent("Synthetic route save failed");
    await user.click(within(child).getByRole("button", { name: "取消" }));
    await user.click(screen.getByRole("button", { name: "放弃更改" }));
    expect(child).not.toBeInTheDocument();
    expect(parent).toBeInTheDocument();
    expect(edit).toHaveFocus();
  });

  it("closes immediately after a route edit is reverted", async () => {
    const user = userEvent.setup();
    render(<NestedHarness />);
    await user.click(screen.getByRole("button", { name: "Edit test route" }));
    const child = screen.getByRole("dialog", { name: "路由策略" });
    const weight = within(child).getByRole("spinbutton", { name: /^流量权重/ });
    fireEvent.change(weight, { target: { value: "25" } });
    fireEvent.change(weight, { target: { value: "100" } });
    await user.keyboard("{Escape}");
    expect(child).not.toBeInTheDocument();
    expect(screen.queryByRole("dialog", { name: "放弃路由更改？" })).not.toBeInTheDocument();
  });
});
