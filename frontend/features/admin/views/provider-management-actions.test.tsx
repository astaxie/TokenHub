import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ProviderManagementActions, type ProviderManagementAction } from "./provider-management-actions";

afterEach(() => vi.restoreAllMocks());

function layout(initialWidth: number) {
  let width = initialWidth;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    if (this.classList.contains("provider-management-actions")) this.style.columnGap = "10px";
    const measuredWidth = this.classList.contains("provider-management-actions") ? width
      : this.hasAttribute("data-measure-more") ? 60 : this.hasAttribute("data-measure-action") ? 50 : 0;
    return { width: measuredWidth, height: 26, top: 0, right: measuredWidth, bottom: 26, left: 0, x: 0, y: 0, toJSON: () => ({}) };
  });
  return (nextWidth: number) => { width = nextWidth; fireEvent(window, new Event("resize")); };
}

function actions(): ProviderManagementAction[] {
  return ["测试", "编辑", "配置路由", "删除"].map((label, index) => ({ id: String(index), label, onClick: vi.fn(), danger: index === 3 }));
}

describe("Provider management action overflow", () => {
  it("shows all actions when they fit and moves only overflow into More on resize", async () => {
    const resize = layout(240);
    const items = actions();
    const user = userEvent.setup();
    render(<ProviderManagementActions name="Fixture Provider" actions={items} />);
    expect(screen.queryByText("更多")).not.toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(4);
    const routing = screen.getByRole("button", { name: "配置路由" });
    routing.focus();
    resize(180);
    expect(document.querySelectorAll(".provider-management-actions > button")).toHaveLength(2);
    expect(screen.getByRole("button", { name: "配置路由" })).not.toBeVisible();
    const trigger = screen.getByText("更多").closest("summary")!;
    expect(trigger).toHaveFocus();
    await user.click(trigger);
    await user.click(screen.getByRole("button", { name: "删除" }));
    expect(items[3].onClick).toHaveBeenCalledOnce();
    expect(trigger.closest("details")).not.toHaveAttribute("open");
    expect(trigger).toHaveFocus();
    resize(240);
    expect(screen.queryByText("更多")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "配置路由" })).toHaveFocus();
    await user.click(screen.getByRole("button", { name: "配置路由" }));
    expect(items[2].onClick).toHaveBeenCalledOnce();
  });

  it("keeps every action reachable in a narrow container and closes the menu with Escape", async () => {
    layout(70);
    const user = userEvent.setup();
    render(<ProviderManagementActions name="Fixture Provider" actions={actions()} />);
    expect(document.querySelectorAll(".provider-management-actions > button")).toHaveLength(0);
    const trigger = screen.getByText("更多").closest("summary")!;
    await user.click(trigger);
    expect(screen.getAllByRole("button")).toHaveLength(4);
    screen.getByRole("button", { name: "编辑" }).focus();
    await user.keyboard("{Escape}");
    expect(trigger).toHaveFocus();
    expect(trigger.closest("details")).not.toHaveAttribute("open");
  });

  it("recomputes capacity when permissions remove actions", () => {
    layout(180);
    const items = actions();
    const { rerender } = render(<ProviderManagementActions name="Fixture Provider" actions={items} />);
    expect(screen.getByText("更多")).toBeVisible();
    rerender(<ProviderManagementActions name="Fixture Provider" actions={items.slice(0, 2)} />);
    expect(screen.queryByText("更多")).not.toBeInTheDocument();
    expect(screen.getAllByRole("button")).toHaveLength(2);
    rerender(<ProviderManagementActions name="Fixture Provider" actions={[]} />);
    expect(screen.queryAllByRole("button")).toHaveLength(0);
    expect(screen.queryByText("更多")).not.toBeInTheDocument();
  });
});
