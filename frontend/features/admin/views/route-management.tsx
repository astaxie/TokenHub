import { Plus, Settings2, X } from "lucide-react";
import { useState, type ReactNode } from "react";
import type { AppData, Model } from "../core/types";
import { modelRoutesFor, routeProjectScopeSummary } from "../domain/entities";
import { modelRuntimeState } from "../domain/model-directory";
import { routeSummarySources, routeSummaryStrategy } from "../domain/route-summary";
import { countRatioWithUnit, formatLocaleNumber, formatTranslationTemplate, tx } from "../i18n/runtime";
import { useModalFocus } from "../shared/modal-focus";
import { ConfirmDialog, StatusPill } from "../shared/ui";

export function RouteSummaryTable({ models, data, loading, onConfigure }: {
  models: Model[];
  data: AppData;
  loading: boolean;
  onConfigure: (model: Model) => void;
}) {
  return (
    <div className="route-summary-scroll">
      <table className="route-summary-table">
        <thead><tr><th>{tx("对外模型")}</th><th>{tx("路由方式")}</th><th>{tx("模型来源")}</th><th>{tx("状态")}</th><th>{tx("操作")}</th></tr></thead>
        <tbody>{models.map((model) => {
          const routes = modelRoutesFor(model, data);
          const active = routes.filter((route) => route.status === "active").length;
          const sources = routeSummarySources(routes, data);
          const runtime = modelRuntimeState(model, data);
          const restricted = routes.filter((route) => route.project_scope === "include" || route.project_scope === "exclude");
          return (
            <tr key={model.name}>
              <td><strong>{model.name}</strong><small>{model.modality || "chat"}</small></td>
              <td data-label={tx("路由方式")}>{routeSummaryStrategy(routes)}</td>
              <td data-label={tx("模型来源")}><span title={sources.join(", ")}>{sources.slice(0, 2).join(", ") || "—"}{sources.length > 2 ? ` +${formatLocaleNumber(sources.length - 2)}` : ""}</span><small>{countRatioWithUnit(active, routes.length, "条启用线路", "active route", "件の有効ルート")}</small>{restricted.length ? <small title={restricted.map((route) => routeProjectScopeSummary(route, data)).join("; ")}>{tx("含项目范围限制")}</small> : null}</td>
              <td><StatusPill status={active && model.status === "active" ? "active" : "disabled"} label={!routes.length ? tx("未配置") : active && model.status === "active" ? tx("已启用") : tx("已停用")} />{active > 0 && model.status === "active" && (runtime === "unavailable" || runtime === "degraded") ? <small className="route-summary-warning">{tx(runtime === "degraded" ? "部分线路异常" : "线路需检查")}</small> : null}</td>
              <td><button aria-label={formatTranslationTemplate(tx("配置路由：{model}"), { model: model.name })} className="secondary-button" disabled={loading} onClick={() => onConfigure(model)} type="button"><Settings2 size={14} />{tx("配置")}</button></td>
            </tr>
          );
        })}</tbody>
      </table>
    </div>
  );
}

export function RouteConfigurationDialog({ model, models, loading, error, onClose, onSelect, onCreate, children }: {
  model: Model;
  models: Model[];
  loading: boolean;
  error?: string;
  onClose: () => void;
  onSelect: (model: Model) => void;
  onCreate: () => void;
  children: (controls: { onDirtyChange: (dirty: boolean) => void; guard: (action: () => void) => void; revision: number }) => ReactNode;
}) {
  const [dirty, setDirty] = useState(false);
  const [revision, setRevision] = useState(0);
  const [pending, setPending] = useState<(() => void) | null>(null);
  function guard(action: () => void) {
    if (loading) return;
    if (dirty) setPending(() => action);
    else action();
  }
  const focus = useModalFocus(() => guard(onClose));
  return (
    <>
      <div className="modal-backdrop route-configuration-backdrop" role="presentation">
        <div className="route-configuration-dialog" role="dialog" aria-modal="true" aria-label={tx("配置模型路由")} {...focus}>
          <header className="route-configuration-header">
            <div><span>{tx("配置模型路由")}</span><h2>{model.name}</h2></div>
            <button className="icon-button" aria-label={tx("关闭")} disabled={loading} onClick={() => guard(onClose)} type="button"><X size={20} /></button>
          </header>
          <div className="route-configuration-toolbar">
            <label>{tx("对外模型")}<select aria-label={tx("切换路由模型")} value={model.name} disabled={loading} onChange={(event) => { const next = models.find((item) => item.name === event.target.value); if (next) guard(() => onSelect(next)); }}>{models.map((item) => <option key={item.name} value={item.name}>{item.name}</option>)}</select></label>
            <button className="secondary-button" disabled={loading} onClick={() => guard(onCreate)} type="button"><Plus size={15} />{tx("添加线路")}</button>
          </div>
          {error ? <p className="route-configuration-error" role="alert">{error}</p> : null}
          <div className="route-configuration-body">{children({ onDirtyChange: setDirty, guard, revision })}</div>
        </div>
      </div>
      {pending ? <ConfirmDialog title="放弃路由更改？" message="尚未应用的路由更改将丢失。" confirmLabel="放弃更改" loading={false} onCancel={() => setPending(null)} onConfirm={() => { const action = pending; setPending(null); setDirty(false); setRevision((value) => value + 1); action(); }} /> : null}
    </>
  );
}
