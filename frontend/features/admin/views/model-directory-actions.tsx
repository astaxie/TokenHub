import { MoreHorizontal } from "lucide-react";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { type ApiContext, type Model } from "../core/types";
import { type ModelPublicationState } from "../domain/model-directory";
import { formatTranslationTemplate, tx } from "../i18n/runtime";
import { StatementLauncher } from "./billing-statements";

export function ModelDirectoryActions({ api, model, busy, publication, activeRoutes, onOpenRoutes, onEdit, onDelete, onPublish }: {
  api: ApiContext;
  model: Model;
  busy: boolean;
  publication: ModelPublicationState;
  activeRoutes: number;
  onOpenRoutes: (model: Model) => void;
  onEdit: (model: Model) => void;
  onDelete: (model: Model) => void;
  onPublish: (model: Model, published: boolean) => void;
}) {
  const menuID = useId();
  const container = useRef<HTMLDivElement>(null);
  const measurement = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const statementReturnFocus = useRef<HTMLElement | null>(null);
  const pendingFocus = useRef<string | null>(null);
  const capacity = useRef(2);
  const [visibleCount, setVisibleCount] = useState(2);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({ top: 0, left: 0 });
  const labels = [tx("编辑"), tx("路由策略"), tx("下游费用对账单"), tx(publication === "published" ? "下线" : "发布"), tx("删除")];
  const measurementKey = JSON.stringify(labels);

  useLayoutEffect(() => {
    const root = container.current;
    const sizing = measurement.current;
    if (!root || !sizing) return;
    const measure = () => {
      const available = root.getBoundingClientRect().width;
      if (available <= 0) return;
      const widths = Array.from(sizing.querySelectorAll<HTMLElement>("[data-measure-action]")).map(item => item.getBoundingClientRect().width);
      const moreWidth = sizing.querySelector<HTMLElement>("[data-measure-more]")?.getBoundingClientRect().width ?? 0;
      const gap = Number.parseFloat(getComputedStyle(root).columnGap) || 0;
      let count = widths.length;
      if (widths.reduce((sum, width) => sum + width, 0) + gap * Math.max(0, count - 1) > available) {
        count = 0;
        let used = moreWidth;
        while (count < widths.length && used + gap + widths[count] <= available) {
          used += gap + widths[count];
          count++;
        }
      }
      if (capacity.current === count) return;
      const focused = document.activeElement;
      pendingFocus.current = focused instanceof HTMLElement && (root.contains(focused) || menu.current?.contains(focused))
        ? focused.dataset.actionId === "more" ? menu.current?.querySelector<HTMLButtonElement>("button[data-action-id]")?.dataset.actionId ?? "more" : focused.dataset.actionId ?? null
        : null;
      capacity.current = count;
      setOpen(false);
      setVisibleCount(count);
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(root);
    for (const item of sizing.children) observer?.observe(item);
    window.addEventListener("resize", measure);
    return () => { observer?.disconnect(); window.removeEventListener("resize", measure); };
  }, [measurementKey]);

  useLayoutEffect(() => {
    const findInline = (id: string) => container.current?.querySelector<HTMLButtonElement>(`button[data-action-id="${id}"]`);
    statementReturnFocus.current = findInline("statement") ?? trigger.current;
    if (pendingFocus.current) {
      const id = pendingFocus.current;
      const target = id === "more" ? trigger.current ?? findInline("statement") : findInline(id) ?? trigger.current;
      target?.focus();
      pendingFocus.current = null;
    }
  }, [visibleCount]);

  useLayoutEffect(() => {
    if (!open) return;
    function reposition(event?: Event) {
      if (!trigger.current || !menu.current || (event?.target instanceof Node && menu.current.contains(event.target))) return;
      const rect = trigger.current.getBoundingClientRect();
      const panel = menu.current.getBoundingClientRect();
      setPosition({
        left: Math.max(8, Math.min(rect.right - panel.width, window.innerWidth - panel.width - 8)),
        top: Math.max(8, Math.min(rect.bottom + 6, window.innerHeight - panel.height - 8)),
      });
    }
    reposition();
    window.addEventListener("scroll", reposition, true);
    return () => window.removeEventListener("scroll", reposition, true);
  }, [open, visibleCount, measurementKey]);

  useEffect(() => {
    if (!open) return;
    menu.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
    function dismiss(event: PointerEvent) {
      const target = event.target as Element | null;
      if (menu.current?.contains(target) || trigger.current?.contains(target) || target?.closest(".statement-drawer")) return;
      setOpen(false);
    }
    function close() {
      if (menu.current?.contains(document.activeElement)) trigger.current?.focus();
      setOpen(false);
    }
    function keydown(event: KeyboardEvent) {
      if (event.key !== "Escape" || (event.target instanceof Element && event.target.closest("dialog"))) return;
      setOpen(false);
      trigger.current?.focus();
    }
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", keydown);
    window.addEventListener("resize", close);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      document.removeEventListener("keydown", keydown);
      window.removeEventListener("resize", close);
    };
  }, [open]);

  return <StatementLauncher api={api} side="tenant" model={model.name} returnFocusRef={statementReturnFocus} renderTrigger={openStatement => {
    const actions = [
      { id: "edit", label: labels[0], disabled: busy, onClick: () => onEdit(model) },
      { id: "routes", label: labels[1], disabled: busy, onClick: () => onOpenRoutes(model), ariaLabel: formatTranslationTemplate(tx("路由策略：{model}"), { model: model.name }) },
      { id: "statement", label: labels[2], disabled: false, onClick: openStatement },
      { id: "publication", label: labels[3], disabled: busy || (publication !== "published" && activeRoutes === 0), onClick: () => onPublish(model, publication !== "published") },
      { id: "delete", label: labels[4], disabled: busy, onClick: () => onDelete(model) },
    ];
    const renderAction = (action: typeof actions[number], overflow = false) => <button aria-label={action.ariaLabel} className={`text-button${action.id === "delete" ? " danger" : ""}`} data-action-id={action.id} disabled={action.disabled} key={action.id} onClick={() => {
      if (overflow) { setOpen(false); trigger.current?.focus(); }
      action.onClick();
    }} type="button">{action.label}</button>;
    return <div className="directory-row-actions model-management-actions" ref={container}>
      {actions.slice(0, visibleCount).map(action => renderAction(action))}
      {visibleCount < actions.length ? <button aria-controls={menuID} aria-expanded={open} aria-label={formatTranslationTemplate(tx("更多操作：{name}"), { name: model.name })} className="icon-button" data-action-id="more" onClick={() => setOpen(current => !current)} ref={trigger} type="button"><MoreHorizontal size={17} /></button> : null}
      {typeof document !== "undefined" ? createPortal(
        <div aria-label={formatTranslationTemplate(tx("模型操作：{name}"), { name: model.name })} className="model-management-menu" hidden={!open || visibleCount === actions.length} id={menuID} ref={menu} role="group" style={position}>
          {actions.slice(visibleCount).map(action => renderAction(action, true))}
        </div>, document.body,
      ) : null}
      <div className="model-management-actions-measure" aria-hidden="true" inert><div ref={measurement}>
        {actions.map(action => <button className="text-button" data-measure-action data-label={action.label} key={action.id} tabIndex={-1} type="button" />)}
        <button className="icon-button" data-measure-more tabIndex={-1} type="button"><MoreHorizontal size={17} /></button>
      </div></div>
    </div>;
  }} />;
}
