import { X } from "lucide-react";
import { createPortal } from "react-dom";
import type { AppData, Model } from "../core/types";
import { findProvider, modelRoutesFor, routeProjectScopeSummary } from "../domain/entities";
import { modelDisplayName } from "../domain/model-display-name";
import { modelEndpointProtocols } from "../domain/model-endpoints";
import { formatLocaleNumber, tx } from "../i18n/runtime";
import { useModalFocus } from "../shared/modal-focus";
import { StatusPill } from "../shared/ui";
import { ModelDirectoryPrice } from "./model-directory-price";

export function ModelDirectoryDetail({ model, data, readOnly, onClose }: { model: Model; data: AppData; readOnly: boolean; onClose: () => void }) {
  const focus = useModalFocus(onClose);
  const routes = readOnly ? [] : modelRoutesFor(model, data);
  const facts = [
    { label: "模型能力", values: model.capabilities ?? [] },
    { label: "支持接口协议", values: modelEndpointProtocols(model.metadata) },
    { label: "支持参数", values: model.supported_parameters ?? [] },
    { label: "输入模态", values: model.input_modalities ?? [] },
    { label: "输出模态", values: model.output_modalities ?? [] },
  ];

  return createPortal(
    <div className="modal-backdrop model-detail-backdrop" role="presentation">
      <div className="model-detail-dialog" role="dialog" aria-modal="true" aria-label={tx("模型详情")} {...focus}>
        <header className="model-detail-header">
          <div><span>{tx("模型详情")}</span><h2>{modelDisplayName(model.metadata, model.name)}</h2></div>
          <button className="icon-button" aria-label={tx("关闭")} onClick={onClose} type="button"><X size={20} /></button>
        </header>
        <div className="model-detail-body">
          <dl className="model-detail-facts">
            <div><dt>{tx("对外模型 ID")}</dt><dd>{model.name}</dd></div>
            <div><dt>{tx("能力")}</dt><dd>{model.modality || "chat"}</dd></div>
            <div><dt>{tx("系列")}</dt><dd>{model.family || "—"}</dd></div>
            <div><dt>{tx("上下文窗口")}</dt><dd>{model.context_window ? formatLocaleNumber(model.context_window) : "—"}</dd></div>
            <div className="model-detail-price"><dt>{tx("对外统一价")}</dt><dd><ModelDirectoryPrice model={model} /></dd></div>
          </dl>
          <section className="model-detail-capabilities" aria-label={tx("模型能力摘要")}>
            {facts.map((fact) => <div key={fact.label}><h3>{tx(fact.label)}</h3><div className="model-detail-tags">{fact.values.length ? fact.values.map((value, index) => <span key={`${value}-${index}`}>{value}</span>) : <span className="muted">—</span>}</div></div>)}
          </section>
          {!readOnly ? (
            <section className="model-detail-mappings" aria-label={tx("全部上游映射")}>
              <h3>{tx("全部上游映射")}</h3>
              {routes.length ? <ul>{routes.map((route) => {
                const provider = findProvider(data, route.provider_id);
                const resource = route.provider_resource_id ? data.providerResources.find((item) => item.id === route.provider_resource_id) : undefined;
                return <li key={route.id}>
                  <div className="model-detail-mapping-heading"><strong>{provider?.name || route.provider_id}</strong><StatusPill status={route.status} /></div>
                  <code>{route.provider_model}</code>
                  {route.provider_resource_id ? <small>{tx("资源实例")}: {resource?.name || route.provider_resource_id}</small> : null}
                  <small>{tx("项目作用域")}: {routeProjectScopeSummary(route, data)}</small>
                </li>;
              })}</ul> : <p className="muted">{tx("尚未映射 Provider")}</p>}
            </section>
          ) : null}
        </div>
      </div>
    </div>, document.body,
  );
}
