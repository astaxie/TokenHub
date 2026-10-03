import { MoreHorizontal } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
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
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState({ top: 0, left: 0 });

  useEffect(() => {
    if (!open) return;
    menu.current?.querySelector<HTMLButtonElement>("button:not(:disabled)")?.focus();
    function dismiss(event: PointerEvent) {
      const target = event.target as Element | null;
      if (menu.current?.contains(target) || trigger.current?.contains(target) || target?.closest(".statement-drawer")) return;
      setOpen(false);
    }
    function keydown(event: KeyboardEvent) {
      if (event.key !== "Escape" || (event.target instanceof Element && event.target.closest("dialog"))) return;
      setOpen(false);
      trigger.current?.focus();
    }
    function reposition() { setOpen(false); }
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", keydown);
    window.addEventListener("resize", reposition);
    return () => {
      document.removeEventListener("pointerdown", dismiss);
      document.removeEventListener("keydown", keydown);
      window.removeEventListener("resize", reposition);
    };
  }, [open]);

  function toggle() {
    if (!open && trigger.current) {
      const rect = trigger.current.getBoundingClientRect();
      setPosition({ left: Math.max(8, Math.min(rect.right - 192, window.innerWidth - 200)), top: Math.max(8, Math.min(rect.bottom + 6, window.innerHeight - 210)) });
    }
    setOpen(!open);
  }

  function run(action: () => void) {
    setOpen(false);
    action();
  }

  return (
    <div className="directory-row-actions model-management-actions">
      <button className="text-button" disabled={busy} onClick={() => onEdit(model)} type="button">{tx("编辑")}</button>
      <button aria-label={formatTranslationTemplate(tx("路由策略：{model}"), { model: model.name })} className="text-button" disabled={busy} onClick={() => onOpenRoutes(model)} type="button">{tx("路由策略")}</button>
      <button aria-controls={menuID} aria-expanded={open} aria-label={formatTranslationTemplate(tx("更多操作：{name}"), { name: model.name })} className="icon-button" disabled={busy} onClick={toggle} ref={trigger} type="button"><MoreHorizontal size={17} /></button>
      {typeof document !== "undefined" ? createPortal(
        <div aria-label={formatTranslationTemplate(tx("模型操作：{name}"), { name: model.name })} className="model-management-menu" hidden={!open} id={menuID} ref={menu} role="group" style={position}>
          <StatementLauncher api={api} side="tenant" model={model.name} onOpen={() => setOpen(false)} returnFocusRef={trigger} />
          <button className="text-button" disabled={busy || (publication !== "published" && activeRoutes === 0)} onClick={() => run(() => onPublish(model, publication !== "published"))} type="button">{tx(publication === "published" ? "下线" : "发布")}</button>
          <button className="danger-button" disabled={busy} onClick={() => run(() => onDelete(model))} type="button">{tx("删除")}</button>
        </div>, document.body,
      ) : null}
    </div>
  );
}
