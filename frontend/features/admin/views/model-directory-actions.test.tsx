import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";
import type { Model } from "../core/types";
import { setActiveLanguage } from "../i18n/runtime";
import { ModelDirectoryActions } from "./model-directory-actions";

const model: Model = { id: "fixture_model", name: "fixture-model", family: "test", modality: "chat", status: "active" };
const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, "showModal");
afterEach(() => {
  if (originalShowModal) Object.defineProperty(HTMLDialogElement.prototype, "showModal", originalShowModal);
  else Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal");
});

function layout(initialWidth: number, actionWidth: (label: string) => number = () => 50) {
  let width = initialWidth;
  let notifyResize: ResizeObserverCallback | undefined;
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: ResizeObserverCallback) { notifyResize = callback; }
    observe() {}
    disconnect() {}
  });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
    if (this.classList.contains("model-management-actions")) this.style.columnGap = "10px";
    const measuredWidth = this.classList.contains("model-management-actions") ? width
      : this.hasAttribute("data-measure-more") ? 30 : this.hasAttribute("data-measure-action") ? actionWidth(this.dataset.label ?? "") : 50;
    return { width: measuredWidth, height: 26, top: 0, right: measuredWidth, bottom: 26, left: 0, x: 0, y: 0, toJSON: () => ({}) };
  });
  return (nextWidth: number, observerOnly = false) => {
    width = nextWidth;
    if (observerOnly) act(() => notifyResize?.([], {} as ResizeObserver));
    else fireEvent(window, new Event("resize"));
  };
}

function setup(overrides: Partial<ComponentProps<typeof ModelDirectoryActions>> = {}) {
  const props: ComponentProps<typeof ModelDirectoryActions> = {
    api: { baseURL: "", adminToken: "test" }, model, busy: false, publication: "published", activeRoutes: 1,
    onOpenRoutes: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), onPublish: vi.fn(), ...overrides,
  };
  const view = render(<ModelDirectoryActions {...props} />);
  return { props, rerender: (next: Partial<typeof props> = {}) => view.rerender(<ModelDirectoryActions {...props} {...next} />) };
}

function container() { return document.querySelector<HTMLDivElement>(".model-management-actions")!; }
function inlineIDs() { return Array.from(container().querySelectorAll<HTMLButtonElement>(":scope > button")).map(button => button.dataset.actionId); }
function more() { return screen.getByRole("button", { name: /更多操作|More actions|その他の操作/ }); }

describe("Model directory action overflow", () => {
  it("shows every action when it fits and moves only overflow into More on resize", async () => {
    const resize = layout(300);
    const user = userEvent.setup();
    const { props } = setup();
    expect(inlineIDs()).toEqual(["edit", "routes", "statement", "publication", "delete"]);
    expect(screen.queryByRole("button", { name: /更多操作/ })).not.toBeInTheDocument();
    screen.getByRole("button", { name: "删除" }).focus();
    resize(160);
    expect(inlineIDs()).toEqual(["edit", "routes", "more"]);
    expect(more()).toHaveFocus();
    expect(screen.queryByRole("button", { name: "删除" })).not.toBeInTheDocument();
    await user.click(more());
    const menu = screen.getByRole("group", { name: "模型操作：fixture-model" });
    expect(within(menu).getAllByRole("button").map(button => button.textContent)).toEqual(["下游费用对账单", "下线", "删除"]);
    await user.click(within(menu).getByRole("button", { name: "删除" }));
    expect(props.onDelete).toHaveBeenCalledWith(model);
    expect(menu).not.toBeVisible();
    expect(more()).toHaveFocus();
    resize(300);
    expect(inlineIDs()).toEqual(["edit", "routes", "statement", "publication", "delete"]);
    expect(screen.getByRole("button", { name: "下游费用对账单" })).toHaveFocus();
    await user.click(screen.getByRole("button", { name: "编辑" }));
    await user.click(screen.getByRole("button", { name: "路由策略：fixture-model" }));
    await user.click(screen.getByRole("button", { name: "下线" }));
    expect(props.onEdit).toHaveBeenCalledWith(model);
    expect(props.onOpenRoutes).toHaveBeenCalledWith(model);
    expect(props.onPublish).toHaveBeenCalledWith(model, false);
  });

  it("keeps all operations reachable in a narrow container and restores keyboard focus", async () => {
    layout(35);
    const user = userEvent.setup();
    setup();
    expect(inlineIDs()).toEqual(["more"]);
    more().focus();
    await user.keyboard("{Enter}");
    expect(screen.getByRole("button", { name: "编辑" })).toHaveFocus();
    expect(screen.getByRole("group", { name: "模型操作：fixture-model" }).querySelectorAll("button")).toHaveLength(5);
    await user.keyboard("{Escape}");
    expect(more()).toHaveFocus();
    expect(screen.queryByRole("group", { name: "模型操作：fixture-model" })).not.toBeInTheDocument();
  });

  it("closes the menu on observed width changes without reopening it after shrinking again", async () => {
    const resize = layout(160);
    const user = userEvent.setup();
    setup();
    await user.click(more());
    expect(screen.getByRole("button", { name: "下游费用对账单" })).toHaveFocus();
    resize(300, true);
    expect(screen.getByRole("button", { name: "下游费用对账单" })).toHaveFocus();
    expect(screen.queryByRole("group", { name: "模型操作：fixture-model" })).not.toBeInTheDocument();
    resize(160, true);
    expect(more()).toHaveFocus();
    expect(more()).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("group", { name: "模型操作：fixture-model" })).not.toBeInTheDocument();
  });

  it("preserves publication restrictions and busy actions in both layouts", async () => {
    const resize = layout(300);
    const user = userEvent.setup();
    const { props, rerender } = setup({ publication: "draft", activeRoutes: 0 });
    expect(screen.getByRole("button", { name: "发布" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "发布" }));
    expect(props.onPublish).not.toHaveBeenCalled();
    rerender({ publication: "draft", activeRoutes: 1 });
    await user.click(screen.getByRole("button", { name: "发布" }));
    expect(props.onPublish).toHaveBeenCalledWith(model, true);
    rerender({ busy: true });
    for (const name of ["编辑", "路由策略：fixture-model", "发布", "删除"]) expect(screen.getByRole("button", { name })).toBeDisabled();
    resize(35);
    await user.click(more());
    for (const name of ["编辑", "路由策略：fixture-model", "发布", "删除"]) expect(screen.getByRole("button", { name })).toBeDisabled();
    expect(screen.getByRole("button", { name: "下游费用对账单" })).toBeEnabled();
  });

  it("recalculates overflow when translated labels change width without resizing", () => {
    layout(280, label => label.length * 6 + 20);
    const { rerender } = setup();
    expect(inlineIDs()).toHaveLength(5);
    setActiveLanguage("en");
    rerender();
    expect(inlineIDs()).toEqual(["edit", "routes", "more"]);
    expect(more()).toHaveAccessibleName("More actions: fixture-model");
    setActiveLanguage("zh-CN");
    rerender();
    expect(inlineIDs()).toEqual(["edit", "routes", "statement", "publication", "delete"]);
    expect(screen.queryByRole("button", { name: /更多操作/ })).not.toBeInTheDocument();
  });

  it("preserves an open billing draft across both layout changes and restores current focus", async () => {
    const resize = layout(300);
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function(this: HTMLDialogElement) { this.setAttribute("open", ""); } });
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ data: [] }))));
    vi.stubGlobal("fetch", fetchMock);
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByRole("button", { name: "下游费用对账单" }));
    const draft = screen.getByLabelText("客户名称");
    await user.type(draft, "Fixture Customer");
    resize(160, true);
    expect(screen.getByRole("dialog", { name: "费用对账单" })).toBeVisible();
    expect(screen.getByLabelText("客户名称")).toBe(draft);
    expect(draft).toHaveValue("Fixture Customer");
    await user.click(screen.getByRole("button", { name: "关闭" }));
    expect(more()).toHaveFocus();
    await user.click(more());
    await user.click(screen.getByRole("button", { name: "下游费用对账单" }));
    const nextDraft = screen.getByLabelText("客户名称");
    await user.type(nextDraft, "Another Fixture Customer");
    resize(300, true);
    expect(screen.getByLabelText("客户名称")).toBe(nextDraft);
    expect(nextDraft).toHaveValue("Another Fixture Customer");
    await user.click(screen.getByRole("button", { name: "关闭" }));
    expect(screen.getByRole("button", { name: "下游费用对账单" })).toHaveFocus();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});
