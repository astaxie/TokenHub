"use client";

import { AlertTriangle, Boxes, CircleCheck, Plus, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { type ApiContext, type AppData, type Model, type ResourceConfig } from "../core/types";
import { modelCategory, modelCategoryLabel } from "../domain/catalog";
import { findProvider, modelRoutesFor } from "../domain/entities";
import { modelDirectorySubtitle, modelDisplayName } from "../domain/model-display-name";
import { externalModels, filterExternalModels, isCustomModelAlias, modelPublicationState, modelRuntimeState, type ModelPublicationState } from "../domain/model-directory";
import { compactNumber } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection, StatusPill } from "../shared/ui";
import { ModelBrandIcon } from "./model-catalog";
import { ModelGovernanceEmptyState } from "./model-governance-empty-state";
import { ModelDirectoryActions } from "./model-directory-actions";
import { ModelDirectoryDetail } from "./model-directory-detail";
import { ModelDirectoryPrice } from "./model-directory-price";

export function ModelDirectoryView({
  api,
  config,
  data,
  loading,
  readOnly = false,
  onReload,
  onCreateModel,
  onOpenProviders,
  onOpenRoutes,
  onEditModel,
  onDeleteModel,
}: {
  api: ApiContext;
  config: ResourceConfig<Model>;
  data: AppData;
  loading: boolean;
  readOnly?: boolean;
  onReload: () => Promise<void> | void;
  onCreateModel: () => void;
  onOpenProviders: () => void;
  onOpenRoutes: (model?: Model) => void;
  onEditModel: (model: Model) => void;
  onDeleteModel: (model: Model) => void;
}) {
  const [publication, setPublication] = useState<"all" | ModelPublicationState>(readOnly ? "all" : "published");
  const [query, setQuery] = useState("");
  const [providerID, setProviderID] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [detailName, setDetailName] = useState("");

  const publishedModels = useMemo(() => externalModels(data, readOnly), [data, readOnly]);
  const filteredExternal = useMemo(
    () => filterExternalModels(publishedModels, data, publication, query, providerID),
    [data, providerID, publication, publishedModels, query],
  );
  const hasImportedProviderModels = data.providers.length > 0 && data.providerModels.length > 0;
  const detailModel = publishedModels.find((model) => model.name === detailName);

  async function setPublished(model: Model, published: boolean) {
    setBusy(true);
    setError("");
    try {
      const resp = await adminFetch(api, `/api/admin/models/${encodeURIComponent(model.name)}`, {
        method: "PATCH",
        body: JSON.stringify({ ...model, status: published ? "active" : "disabled" }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx(published ? "发布模型" : "下线模型")));
      setNotice(tx(published ? "模型已发布" : "模型已下线，映射线路已保留"));
      await onReload();
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("操作失败"));
    } finally {
      setBusy(false);
    }
  }

  if (!loading && publishedModels.length === 0) {
    if (readOnly) {
      return (
        <DataSection title={config.eyebrow}>
          <ModelGovernanceEmptyState
            stage="models"
            title="当前没有可见模型"
            description="请联系管理员发布模型并授予 API Key 访问范围。"
          />
        </DataSection>
      );
    }
    return (
      <DataSection title={config.eyebrow}>
        <ModelGovernanceEmptyState
          stage={hasImportedProviderModels ? "models" : "providers"}
          title={hasImportedProviderModels ? "还没有对外模型" : "先引入可用的 Provider 模型"}
          description={hasImportedProviderModels
            ? "从内置模型目录中挑选对外模型，再选择已引入的 Provider 模型并设置统一对外价格。"
            : "先在 Provider 渠道添加上游服务并选择要引入的模型；Provider 模型价格用于记录真实成本与审计。"}
          actionLabel={hasImportedProviderModels ? "新建对外模型" : "前往 Provider 渠道"}
          onAction={hasImportedProviderModels ? onCreateModel : onOpenProviders}
        />
      </DataSection>
    );
  }

  return (
    <DataSection title={config.eyebrow}>
      <div className="model-directory">
        <header className="model-management-header">
          <div>
            <h2>{tx("对外模型目录")}</h2>
            <span>{tx("这里的模型和价格面向客户端统一生效，不随实际命中的 Provider 改变。")}</span>
          </div>
          {!readOnly ? (
              <button className="button" onClick={onCreateModel} type="button">
                <Plus size={16} />
                {tx("新建对外模型")}
              </button>
          ) : null}
        </header>

        {notice ? <div className="inline-notice success"><CircleCheck size={15} />{notice}</div> : null}
        {error ? <div className="inline-notice error"><AlertTriangle size={15} />{error}</div> : null}

        <div className="model-directory-toolbar">
          <div className="search-box model-directory-search">
            <Search size={16} />
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索对外模型、Provider 或上游模型")} />
          </div>
          {!readOnly ? (
            <select aria-label={tx("筛选 Provider")} value={providerID} onChange={(event) => setProviderID(event.target.value)}>
              <option value="">{tx("全部 Provider")}</option>
              {data.providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
            </select>
          ) : null}
          {!readOnly ? (
            <div className="model-publication-filter" role="group" aria-label={tx("发布状态")}>
              {(["published", "draft", "disabled", "all"] as const).map((state) => (
                <button className={publication === state ? "active" : ""} key={state} onClick={() => setPublication(state)} type="button">
                  {tx(publicationLabel(state))}
                </button>
              ))}
            </div>
          ) : null}
        </div>

        <ExternalModelsTable
          api={api}
          data={data}
          models={filteredExternal}
          readOnly={readOnly}
          busy={busy || loading}
          onOpenRoutes={onOpenRoutes}
          onEdit={onEditModel}
          onDelete={onDeleteModel}
          onPublish={setPublished}
          onOpenDetails={(model) => setDetailName(model.name)}
        />
        {detailModel ? <ModelDirectoryDetail model={detailModel} data={data} readOnly={readOnly} onClose={() => setDetailName("")} /> : null}
      </div>
    </DataSection>
  );
}

function ExternalModelsTable({ api, data, models, readOnly, busy, onOpenRoutes, onEdit, onDelete, onPublish, onOpenDetails }: {
  api: ApiContext;
  data: AppData;
  models: Model[];
  readOnly: boolean;
  busy: boolean;
  onOpenRoutes: (model?: Model) => void;
  onEdit: (model: Model) => void;
  onDelete: (model: Model) => void;
  onPublish: (model: Model, published: boolean) => void;
  onOpenDetails: (model: Model) => void;
}) {
  if (models.length === 0) {
    return (
      <div className="model-directory-empty">
        <Boxes size={28} />
        <strong>{tx(readOnly ? "当前没有可见模型" : "当前范围没有对外模型")}</strong>
        <span>{tx(readOnly ? "请联系管理员发布模型并授予 API Key 访问范围。" : "请创建对外模型、选择已引入的 Provider 模型并设置统一价格；创建后可在路由策略中细调流量。")}</span>
      </div>
    );
  }
  return (
    <div className="model-directory-table-wrap">
      <table className={`model-directory-table model-management-table${readOnly ? " is-read-only" : ""}`}>
        <thead><tr><th>{tx("对外模型")}</th><th>{tx("类型与能力")}</th>{!readOnly ? <><th>{tx("真实上游映射")}</th><th>{tx("状态")}</th></> : <th>{tx("可用状态")}</th>}<th>{tx("对外统一价")}</th>{!readOnly ? <th>{tx("操作")}</th> : null}</tr></thead>
        <tbody>
          {models.map((model) => {
            const routes = modelRoutesFor(model, data);
            const activeRoutes = routes.filter((route) => route.status === "active");
            const primary = activeRoutes[0] ?? routes[0];
            const provider = primary ? findProvider(data, primary.provider_id) : undefined;
            const publication = modelPublicationState(model, data);
            const runtime = modelRuntimeState(model, data);
            const customAlias = isCustomModelAlias(model, routes);
            const title = modelDisplayName(model.metadata, model.name);
            const subtitle = modelDirectorySubtitle(model.name, title, !readOnly ? tx(customAlias ? "自定义别名" : "同名 1:1") : "");
            const category = modelCategory(model, data);
            const categoryLabel = modelCategoryLabel(category, data);
            const capabilities = model.capabilities ?? [];
            return (
              <tr key={model.name}>
                <td>
                  <div className="directory-model-name">
                    <ModelBrandIcon category={category} label={categoryLabel} data={data} />
                    <div><button className="text-button model-detail-trigger" aria-label={`${tx("查看模型详情")}: ${title}`} onClick={() => onOpenDetails(model)} type="button">{title}</button>{subtitle ? <span>{subtitle}</span> : null}</div>
                  </div>
                </td>
                <td><strong>{model.modality || "chat"}</strong><span>{model.context_window ? `${compactNumber(model.context_window)} ctx` : capabilities.slice(0, 2).join(" / ") || model.family || "-"}</span></td>
                {!readOnly ? <>
                  <td>
                    {primary ? <div className="mapping-summary"><span>{provider?.name || primary.provider_id}</span><strong>{primary.provider_model}</strong>{routes.length > 1 ? <em>+{routes.length - 1}</em> : null}</div> : <span className="muted">{tx("尚未映射 Provider")}</span>}
                  </td>
                  <td><div className="model-management-status"><StatusPill status={publication === "published" ? "active" : "disabled"} label={tx(publicationLabel(publication))} /><RuntimeStatus state={runtime} active={activeRoutes.length} total={routes.length} /></div></td>
                </> : <td><StatusPill status="active" label={tx("当前账号可用")} /></td>}
                <td><ModelDirectoryPrice model={model} /></td>
                {!readOnly ? (
                  <td><ModelDirectoryActions api={api} model={model} busy={busy} publication={publication} activeRoutes={activeRoutes.length} onOpenRoutes={onOpenRoutes} onEdit={onEdit} onDelete={onDelete} onPublish={onPublish} /></td>
                ) : null}
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function RuntimeStatus({ state, active, total }: { state: ReturnType<typeof modelRuntimeState>; active: number; total: number }) {
  const config = {
    healthy: { label: "正常", status: "healthy" },
    degraded: { label: "部分异常", status: "warning" },
    unavailable: { label: "全部异常", status: "down" },
    unmapped: { label: "未映射", status: "disabled" },
  }[state];
  return <div className="runtime-status"><StatusPill status={config.status} label={tx(config.label)} /><span>{active}/{total} {tx("条启用")}</span></div>;
}

function publicationLabel(state: "all" | ModelPublicationState) {
  return { all: "全部", published: "已发布", draft: "草稿/待映射", disabled: "已下线" }[state];
}
