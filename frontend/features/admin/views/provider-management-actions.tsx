import { MoreHorizontal } from "lucide-react";
import { useLayoutEffect, useRef, useState } from "react";
import { formatTranslationTemplate, tx } from "../i18n/runtime";

export type ProviderManagementAction = {
  id: string;
  label: string;
  title?: string;
  danger?: boolean;
  onClick: () => void;
};

export function ProviderManagementActions({ name, actions }: { name: string; actions: ProviderManagementAction[] }) {
  const container = useRef<HTMLDivElement>(null);
  const measurement = useRef<HTMLDivElement>(null);
  const menu = useRef<HTMLDetailsElement>(null);
  const restoreFocus = useRef<{ actionId?: string; fromMore: boolean } | null>(null);
  const [capacity, setCapacity] = useState(actions.length);
  const [menuAbove, setMenuAbove] = useState(false);
  const visibleCount = Math.min(capacity, actions.length);
  const moreLabel = tx("更多");
  const measurementKey = JSON.stringify([moreLabel, actions.map(({ id, label }) => [id, label])]);

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
      const focused = document.activeElement;
      restoreFocus.current = null;
      if (focused instanceof HTMLElement && root.contains(focused)) {
        const fromMore = focused.tagName === "SUMMARY";
        restoreFocus.current = { fromMore, actionId: fromMore ? menu.current?.querySelector<HTMLButtonElement>("button[data-action-id]")?.dataset.actionId : focused.dataset.actionId };
      }
      setCapacity(current => current === count ? current : count);
    };
    measure();
    const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    observer?.observe(root);
    for (const item of sizing.children) observer?.observe(item);
    window.addEventListener("resize", measure);
    return () => { observer?.disconnect(); window.removeEventListener("resize", measure); };
  }, [measurementKey]);

  useLayoutEffect(() => {
    if (!restoreFocus.current) return;
    const { actionId, fromMore } = restoreFocus.current;
    const button = Array.from(container.current?.querySelectorAll<HTMLButtonElement>("button[data-action-id]") ?? []).find(item => item.dataset.actionId === actionId);
    if ((fromMore && menu.current) || button?.closest("details:not([open])")) menu.current?.querySelector("summary")?.focus();
    else button?.focus();
    restoreFocus.current = null;
  }, [visibleCount]);

  const renderAction = (action: ProviderManagementAction, overflow = false) => <button className={`text-button${action.danger ? " danger" : ""}`} data-action-id={action.id} key={action.id} title={action.title} type="button" onClick={() => {
    if (overflow && menu.current) {
      menu.current.open = false;
      menu.current.querySelector("summary")?.focus();
    }
    action.onClick();
  }}>{action.label}</button>;

  return <div className="provider-management-actions" ref={container}>
    {actions.slice(0, visibleCount).map(action => renderAction(action))}
    {visibleCount < actions.length ? <details className="provider-management-more" data-side={menuAbove ? "above" : "below"} ref={menu} onToggle={event => {
      const element = event.currentTarget;
      const trigger = element.querySelector("summary")?.getBoundingClientRect();
      const panel = element.querySelector("div")?.getBoundingClientRect();
      setMenuAbove(Boolean(element.open && trigger && panel && trigger.bottom + panel.height + 6 > window.innerHeight && trigger.top >= panel.height + 6));
    }} onKeyDown={event => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.currentTarget.open = false;
        event.currentTarget.querySelector("summary")?.focus();
      }
    }}><summary aria-label={formatTranslationTemplate(tx("更多操作：{name}"), { name })}><MoreHorizontal size={17} /><span>{moreLabel}</span></summary><div>
      {actions.slice(visibleCount).map(action => renderAction(action, true))}
    </div></details> : null}
    <div className="provider-management-actions-measure" aria-hidden="true" inert><div ref={measurement}>
      {actions.map(action => <button className="text-button" data-measure-action data-label={action.label} key={action.id} tabIndex={-1} type="button" />)}
      <span className="provider-management-more-label" data-measure-more><MoreHorizontal size={17} /><span data-label={moreLabel} /></span>
    </div></div>
  </div>;
}
