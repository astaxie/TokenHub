"use client";

import { AlertTriangle, Boxes, CircleCheck, CircleDashed, Link2, Plus, Search } from "lucide-react";
import { useMemo, useState } from "react";
import { type ApiContext, type AppData, type Model, type ResourceConfig } from "../core/types";
import { modelCategory, modelCategoryLabel, priceMetric } from "../domain/catalog";
import { findProvider, modelRoutesFor } from "../domain/entities";
import { modelDirectorySubtitle, modelDisplayName } from "../domain/model-display-name";
import { modelMetadataFacts } from "../domain/model-endpoints";
import { externalModels, filterExternalModels, isCustomModelAlias, modelPublicationState, modelRuntimeState, type ModelPublicationState } from "../domain/model-directory";
import { compactNumber } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection, StatusPill } from "../shared/ui";
import { ModelBrandIcon } from "./model-catalog";
import { ModelGovernanceEmptyState, ModelGovernanceFlow } from "./model-governance-empty-state";

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

  const publishedModels = useMemo(() => externalModels(data, readOnly), [data, readOnly]);
  const filteredExternal = useMemo(
    () => filterExternalModels(publishedModels, data, publication, query, providerID),
    [data, providerID, publication, publishedModels, query],
  );
  const stats = useMemo(() => modelDirectoryStats(publishedModels, data), [data, publishedModels]);
  const hasImportedProviderModels = data.providers.length > 0 && data.providerModels.length > 0;

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
            ? "从内置的 178 个模型中挑选对外模型，再选择已引入的 Provider 模型并设置统一对外价格。"
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
        <header className="model-directory-hero">
          <div>
            <p className="eyebrow">{tx("对外模型目录")}</p>
            <h2>{tx("统一模型名称、能力与对外价格")}</h2>
            <span>{tx("这里的模型和价格面向客户端统一生效，不随实际命中的 Provider 改变。")}</span>
          </div>
          {!readOnly ? (
            <div className="model-directory-hero-actions">
              <button className="secondary-button" onClick={onCreateModel} type="button">
                <Plus size={16} />
                {tx("新建对外模型")}
              </button>
              <button className="button" onClick={() => onOpenRoutes()} type="button">
                <Link2 size={16} />
                {tx("路由策略")}
              </button>
            </div>
          ) : null}
        </header>

        {!readOnly ? <ModelDirectoryStats stats={stats} /> : null}
        {!readOnly ? <ModelGovernanceFlow /> : null}
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
          data={data}
          models={filteredExternal}
          readOnly={readOnly}
          busy={busy || loading}
          onOpenRoutes={onOpenRoutes}
          onEdit={onEditModel}
          onDelete={onDeleteModel}
          onPublish={setPublished}
        />
      </div>
    </DataSection>
  );
}

function ModelDirectoryStats({ stats }: { stats: ReturnType<typeof modelDirectoryStats> }) {
  const items = [
    { label: "已发布", value: stats.published, icon: CircleCheck, tone: "healthy" },
    { label: "草稿/待映射", value: stats.draft, icon: CircleDashed, tone: "draft" },
    { label: "正常可用", value: stats.healthy, icon: Link2, tone: "healthy" },
    { label: "线路异常", value: stats.issues, icon: AlertTriangle, tone: "warning" },
  ];
  return (
    <div className="model-directory-stats">
      {items.map((item) => (
        <div className={item.tone} key={item.label}>
          <item.icon size={17} />
          <span>{tx(item.label)}</span>
          <strong>{item.value}</strong>
        </div>
      ))}
    </div>
  );
}

function ExternalModelsTable({ data, models, readOnly, busy, onOpenRoutes, onEdit, onDelete, onPublish }: {
  data: AppData;
  models: Model[];
  readOnly: boolean;
  busy: boolean;
  onOpenRoutes: (model?: Model) => void;
  onEdit: (model: Model) => void;
  onDelete: (model: Model) => void;
  onPublish: (model: Model, published: boolean) => void;
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
      <table className="model-directory-table">
        <thead><tr><th>{tx("对外模型")}</th><th>{tx("类型与能力")}</th>{!readOnly ? <><th>{tx("真实上游映射")}</th><th>{tx("发布")}</th><th>{tx("线路")}</th></> : <th>{tx("可用状态")}</th>}<th>{tx("对外统一价")}</th>{!readOnly ? <th>{tx("操作")}</th> : null}</tr></thead>
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
            const capabilities = model.capabilities ?? [];
            const supportedParameters = model.supported_parameters ?? [];
            const facts = modelMetadataFacts(model.metadata, capabilities, supportedParameters);
            return (
              <tr key={model.name}>
                <td>
                  <div className="directory-model-name">
                    <ModelBrandIcon category={modelCategory(model)} label={modelCategoryLabel(modelCategory(model))} />
                    <div><strong>{title}</strong>{subtitle ? <span>{subtitle}</span> : null}</div>
                  </div>
                </td>
                <td><strong>{model.modality || "chat"}</strong><span>{compactNumber(model.context_window || 0)} ctx · {capabilities.slice(0, 2).join(" / ") || model.family || "-"}</span>{facts.map((fact) => <div key={fact.kind}><small>{tx({ protocols: "支持接口协议", parameters: "支持参数", capabilities: "模型能力" }[fact.kind])}: {fact.values.join(" / ")}</small></div>)}</td>
                {!readOnly ? <>
                  <td>
                    {primary ? <div className="mapping-summary"><span>{provider?.name || primary.provider_id}</span><strong>{primary.provider_model}</strong>{routes.length > 1 ? <em>+{routes.length - 1}</em> : null}</div> : <span className="muted">{tx("尚未映射 Provider")}</span>}
                  </td>
                  <td><StatusPill status={publication === "published" ? "active" : "disabled"} label={tx(publicationLabel(publication))} /></td>
                  <td><RuntimeStatus state={runtime} active={activeRoutes.length} total={routes.length} /></td>
                </> : <td><StatusPill status="active" label={tx("当前账号可用")} /></td>}
                <td><strong>{priceMetric(model.input_price_usd_per_1m)}</strong><span>{tx("输入")} · {priceMetric(model.output_price_usd_per_1m)} {tx("输出")}</span></td>
                {!readOnly ? (
                  <td><div className="directory-row-actions"><button aria-label={`${tx("路由策略")}: ${model.name}`} className="text-button" onClick={() => onOpenRoutes(model)} type="button">{tx("路由策略")}</button><button className="text-button" onClick={() => onEdit(model)} type="button">{tx("编辑")}</button><button className="text-button" disabled={busy || (publication !== "published" && activeRoutes.length === 0)} onClick={() => onPublish(model, publication !== "published")} type="button">{tx(publication === "published" ? "下线" : "发布")}</button><button className="danger-button" onClick={() => onDelete(model)} type="button">{tx("删除")}</button></div></td>
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

function modelDirectoryStats(models: Model[], data: AppData) {
  return models.reduce((stats, model) => {
    const publication = modelPublicationState(model, data);
    const runtime = modelRuntimeState(model, data);
    if (publication === "published") stats.published += 1;
    if (publication === "draft") stats.draft += 1;
    if (runtime === "healthy") stats.healthy += 1;
    if (runtime === "degraded" || runtime === "unavailable") stats.issues += 1;
    return stats;
  }, { published: 0, draft: 0, healthy: 0, issues: 0 });
}

function publicationLabel(state: "all" | ModelPublicationState) {
  return { all: "全部", published: "已发布", draft: "草稿/待映射", disabled: "已下线" }[state];
}
