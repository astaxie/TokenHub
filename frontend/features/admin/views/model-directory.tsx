"use client";

import { AlertTriangle, Boxes, ChevronRight, CircleCheck, CircleDashed, Database, Link2, Plus, RefreshCw, Search, Server, Settings2, X } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { type ApiContext, type AppData, type Model, type ModelRoute, type Provider, type ProviderCatalogEntry, type ProviderCatalogModel, type ProviderModel, type ResourceConfig } from "../core/types";
import { modelCategory, modelCategoryLabel, priceMetric } from "../domain/catalog";
import { findProvider, modelRoutesFor } from "../domain/entities";
import { candidateModels, externalModels, filterExternalModels, filterProviderModels, isCustomModelAlias, modelPublicationState, modelRuntimeState, providerModelRoutes, type ModelPublicationState } from "../domain/model-directory";
import { compactNumber } from "../domain/formatting";
import { providerTypeLabel } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { DataSection, StatusPill } from "../shared/ui";
import { ModelBrandIcon } from "./model-catalog";

type DirectoryTab = "external" | "upstream" | "templates";

export function ModelDirectoryView({
  api,
  config,
  data,
  loading,
  readOnly = false,
  onReload,
  onEditModel,
  onDeleteModel,
  onEditRoute,
  onDeleteRoute,
  onRestoreDefaults,
}: {
  api: ApiContext;
  config: ResourceConfig<Model>;
  data: AppData;
  loading: boolean;
  readOnly?: boolean;
  onReload: () => Promise<void> | void;
  onEditModel: (model: Model) => void;
  onDeleteModel: (model: Model) => void;
  onEditRoute: (route: ModelRoute) => void;
  onDeleteRoute: (route: ModelRoute) => void;
  onRestoreDefaults: () => void;
}) {
  const [tab, setTab] = useState<DirectoryTab>("external");
  const [publication, setPublication] = useState<"all" | ModelPublicationState>(readOnly ? "all" : "published");
  const [query, setQuery] = useState("");
  const [providerID, setProviderID] = useState("");
  const [importOpen, setImportOpen] = useState(false);
  const [importQuery, setImportQuery] = useState("");
  const [mappingPreset, setMappingPreset] = useState<{ providerModel?: ProviderModel; externalModel?: Model } | null>(null);
  const [detailModel, setDetailModel] = useState<Model | null>(null);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");

  const publishedModels = useMemo(() => externalModels(data, readOnly), [data, readOnly]);
  const filteredExternal = useMemo(
    () => filterExternalModels(publishedModels, data, publication, query, providerID),
    [data, providerID, publication, publishedModels, query],
  );
  const filteredUpstream = useMemo(
    () => filterProviderModels(data.providerModels, data, query, providerID),
    [data, providerID, query],
  );
  const templates = useMemo(() => {
    const normalized = query.trim().toLowerCase();
    return candidateModels(data)
      .filter((model) => !normalized || [model.name, model.family, model.modality, ...(model.capabilities ?? [])].join(" ").toLowerCase().includes(normalized))
      .slice(0, 160);
  }, [data, query]);
  const stats = useMemo(() => modelDirectoryStats(publishedModels, data), [data, publishedModels]);

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

  const tabs: Array<{ key: DirectoryTab; label: string; count: number }> = readOnly
    ? [{ key: "external", label: "可用模型", count: publishedModels.length }]
    : [
        { key: "external", label: "对外模型", count: publishedModels.length },
        { key: "upstream", label: "Provider 上游模型", count: data.providerModels.length },
        { key: "templates", label: "候选模板库", count: candidateModels(data).length },
      ];

  return (
    <DataSection title={config.eyebrow}>
      <div className="model-directory">
        <header className="model-directory-hero">
          <div>
            <p className="eyebrow">{tx("模型发布中心")}</p>
            <h2>{tx("管理对外模型与真实上游映射")}</h2>
            <span>{tx("默认只展示客户端可见的模型；Provider 库存和全量候选目录分别管理。")}</span>
          </div>
          {!readOnly ? (
            <div className="model-directory-hero-actions">
              <button className="secondary-button" onClick={() => setMappingPreset({})} type="button">
                <Link2 size={16} />
                {tx("新建对外模型")}
              </button>
              <button className="button" onClick={() => { setImportQuery(""); setImportOpen(true); }} type="button">
                <Plus size={16} />
                {tx("从 Provider 引入")}
              </button>
            </div>
          ) : null}
        </header>

        {!readOnly ? <ModelDirectoryStats stats={stats} /> : null}
        {notice ? <div className="inline-notice success"><CircleCheck size={15} />{notice}</div> : null}
        {error ? <div className="inline-notice error"><AlertTriangle size={15} />{error}</div> : null}

        <div className="model-directory-tabs" role="tablist" aria-label={tx("模型目录视图")}>
          {tabs.map((item) => (
            <button
              aria-selected={tab === item.key}
              className={tab === item.key ? "active" : ""}
              key={item.key}
              onClick={() => setTab(item.key)}
              role="tab"
              type="button"
            >
              <span>{tx(item.label)}</span>
              <em>{item.count}</em>
            </button>
          ))}
        </div>

        <div className="model-directory-toolbar">
          <div className="search-box model-directory-search">
            <Search size={16} />
            <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索对外模型、Provider 或上游模型")} />
          </div>
          {!readOnly && tab !== "templates" ? (
            <select aria-label={tx("筛选 Provider")} value={providerID} onChange={(event) => setProviderID(event.target.value)}>
              <option value="">{tx("全部 Provider")}</option>
              {data.providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
            </select>
          ) : null}
          {!readOnly && tab === "external" ? (
            <div className="model-publication-filter" role="group" aria-label={tx("发布状态")}>
              {(["published", "draft", "disabled", "all"] as const).map((state) => (
                <button className={publication === state ? "active" : ""} key={state} onClick={() => setPublication(state)} type="button">
                  {tx(publicationLabel(state))}
                </button>
              ))}
            </div>
          ) : null}
          {!readOnly && tab === "templates" ? (
            <button className="secondary-button" onClick={onRestoreDefaults} type="button">
              <RefreshCw size={15} />{tx("恢复候选模板")}
            </button>
          ) : null}
        </div>

        {tab === "external" ? (
          <ExternalModelsTable
            data={data}
            models={filteredExternal}
            readOnly={readOnly}
            busy={busy || loading}
            onDetails={setDetailModel}
            onEdit={onEditModel}
            onDelete={onDeleteModel}
            onPublish={setPublished}
          />
        ) : tab === "upstream" ? (
          <ProviderModelsTable
            data={data}
            models={filteredUpstream}
            onMap={(providerModel) => setMappingPreset({ providerModel })}
          />
        ) : (
          <CandidateTemplateTable models={templates} onImport={(model) => { setImportQuery(model.name); setImportOpen(true); }} />
        )}
      </div>

      {importOpen ? (
        <ProviderImportModal
          api={api}
          data={data}
          initialQuery={importQuery}
          onClose={() => setImportOpen(false)}
          onManual={() => {
            setImportOpen(false);
            setMappingPreset({});
          }}
          onSaved={async (message) => {
            setImportOpen(false);
            setNotice(message);
            await onReload();
          }}
        />
      ) : null}
      {mappingPreset ? (
        <ModelMappingModal
          api={api}
          data={data}
          preset={mappingPreset}
          onClose={() => setMappingPreset(null)}
          onSaved={async (message) => {
            setMappingPreset(null);
            setNotice(message);
            await onReload();
          }}
        />
      ) : null}
      {detailModel ? (
        <ModelMappingDrawer
          data={data}
          model={detailModel}
          onAdd={() => setMappingPreset({ externalModel: detailModel })}
          onClose={() => setDetailModel(null)}
          onDeleteRoute={onDeleteRoute}
          onEditRoute={onEditRoute}
        />
      ) : null}
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

function ExternalModelsTable({ data, models, readOnly, busy, onDetails, onEdit, onDelete, onPublish }: {
  data: AppData;
  models: Model[];
  readOnly: boolean;
  busy: boolean;
  onDetails: (model: Model) => void;
  onEdit: (model: Model) => void;
  onDelete: (model: Model) => void;
  onPublish: (model: Model, published: boolean) => void;
}) {
  if (models.length === 0) {
    return (
      <div className="model-directory-empty">
        <Boxes size={28} />
        <strong>{tx(readOnly ? "当前没有可见模型" : "当前范围没有对外模型")}</strong>
        <span>{tx(readOnly ? "请联系管理员发布模型并授予 API Key 访问范围。" : "可以从 Provider 引入模型，或手工创建一个对外映射。")}</span>
      </div>
    );
  }
  return (
    <div className="model-directory-table-wrap">
      <table className="model-directory-table">
        <thead><tr><th>{tx("对外模型")}</th><th>{tx("类型与能力")}</th>{!readOnly ? <><th>{tx("真实上游映射")}</th><th>{tx("发布")}</th><th>{tx("线路")}</th></> : <th>{tx("可用状态")}</th>}<th>{tx("目录计价")}</th>{!readOnly ? <th>{tx("操作")}</th> : null}</tr></thead>
        <tbody>
          {models.map((model) => {
            const routes = modelRoutesFor(model, data);
            const activeRoutes = routes.filter((route) => route.status === "active");
            const primary = activeRoutes[0] ?? routes[0];
            const provider = primary ? findProvider(data, primary.provider_id) : undefined;
            const publication = modelPublicationState(model, data);
            const runtime = modelRuntimeState(model, data);
            const customAlias = isCustomModelAlias(model, routes);
            return (
              <tr key={model.name}>
                <td>
                  <div className="directory-model-name">
                    <ModelBrandIcon category={modelCategory(model)} label={modelCategoryLabel(modelCategory(model))} />
                    <div><strong>{model.name}</strong>{!readOnly ? <span>{customAlias ? tx("自定义别名") : tx("同名 1:1")}</span> : null}</div>
                  </div>
                </td>
                <td><strong>{model.modality || "chat"}</strong><span>{compactNumber(model.context_window || 0)} ctx · {(model.capabilities ?? []).slice(0, 2).join(" / ") || model.family || "-"}</span></td>
                {!readOnly ? <>
                  <td>
                    {primary ? <button className="mapping-summary" onClick={() => onDetails(model)} type="button"><span>{provider?.name || primary.provider_id}</span><strong>{primary.provider_model}</strong>{routes.length > 1 ? <em>+{routes.length - 1}</em> : null}<ChevronRight size={14} /></button> : <span className="muted">{tx("尚未映射 Provider")}</span>}
                  </td>
                  <td><StatusPill status={publication === "published" ? "active" : "disabled"} label={tx(publicationLabel(publication))} /></td>
                  <td><RuntimeStatus state={runtime} active={activeRoutes.length} total={routes.length} /></td>
                </> : <td><StatusPill status="active" label={tx("当前账号可用")} /></td>}
                <td><strong>{priceMetric(model.input_price_usd_per_1m)}</strong><span>{tx("输入")} · {priceMetric(model.output_price_usd_per_1m)} {tx("输出")}</span></td>
                {!readOnly ? (
                  <td><div className="directory-row-actions"><button className="text-button" onClick={() => onDetails(model)} type="button">{tx("管理映射")}</button><button className="text-button" onClick={() => onEdit(model)} type="button">{tx("编辑")}</button><button className="text-button" disabled={busy || (publication !== "published" && activeRoutes.length === 0)} onClick={() => onPublish(model, publication !== "published")} type="button">{tx(publication === "published" ? "下线" : "发布")}</button><button className="danger-button" onClick={() => onDelete(model)} type="button">{tx("删除")}</button></div></td>
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

function ProviderModelsTable({ data, models, onMap }: { data: AppData; models: ProviderModel[]; onMap: (model: ProviderModel) => void }) {
  if (models.length === 0) {
    return <div className="model-directory-empty"><Server size={28} /><strong>{tx("尚未引入 Provider 上游模型")}</strong><span>{tx("请先从已添加的 Provider 引入模型，之后才能创建对外映射。")}</span></div>;
  }
  return (
    <div className="model-directory-table-wrap">
      <table className="model-directory-table provider-model-table">
        <thead><tr><th>Provider</th><th>{tx("上游模型")}</th><th>{tx("能力")}</th><th>{tx("映射到对外模型")}</th><th>{tx("库存状态")}</th><th>{tx("操作")}</th></tr></thead>
        <tbody>{models.map((model) => {
          const provider = findProvider(data, model.provider_id);
          const mappings = providerModelRoutes(model, data);
          return <tr key={model.id}>
            <td><strong>{provider?.name || model.provider_id}</strong><span>{providerTypeLabel(provider?.type)}</span></td>
            <td><strong>{model.upstream_model}</strong><span>{model.display_name || model.canonical_name || model.source || "-"}</span></td>
            <td><strong>{model.modality || "chat"}</strong><span>{compactNumber(model.context_window || 0)} ctx · {(model.capabilities ?? []).slice(0, 2).join(" / ") || "-"}</span></td>
            <td>{mappings.length > 0 ? <div className="mapped-model-chips">{mappings.slice(0, 3).map((route) => <span key={route.id}>{route.model_name}</span>)}{mappings.length > 3 ? <em>+{mappings.length - 3}</em> : null}</div> : <span className="muted">{tx("尚未发布")}</span>}</td>
            <td><StatusPill status={model.status} label={tx(model.source === "existing-route" ? "历史路由" : "已引入")} /></td>
            <td><button className="text-button" onClick={() => onMap(model)} type="button">{tx(mappings.length ? "新增别名/映射" : "发布/映射")}</button></td>
          </tr>;
        })}</tbody>
      </table>
    </div>
  );
}

function CandidateTemplateTable({ models, onImport }: { models: Model[]; onImport: (model: Model) => void }) {
  return (
    <div className="candidate-template-panel">
      <div className="candidate-template-note"><Database size={17} /><div><strong>{tx("这里只是候选元数据，不代表已经接入或可调用")}</strong><span>{tx("选择 Provider 引入后，才会进入上游模型库存；创建启用映射后，才会成为对外模型。")}</span></div></div>
      {models.length === 0 ? <div className="model-directory-empty"><Boxes size={28} /><strong>{tx("没有匹配的候选模板")}</strong></div> : (
        <div className="template-card-grid">{models.map((model) => (
          <article key={model.name}><div><ModelBrandIcon category={modelCategory(model)} label={modelCategoryLabel(modelCategory(model))} /><span>{modelCategoryLabel(modelCategory(model))}</span></div><h3>{model.name}</h3><p>{model.modality || "chat"} · {compactNumber(model.context_window || 0)} ctx</p><small>{(model.capabilities ?? []).slice(0, 3).join(" · ") || model.family}</small><button className="secondary-button" onClick={() => onImport(model)} type="button">{tx("选择 Provider 引入")}</button></article>
        ))}</div>
      )}
    </div>
  );
}

function ProviderImportModal({ api, data, initialQuery, onClose, onManual, onSaved }: { api: ApiContext; data: AppData; initialQuery: string; onClose: () => void; onManual: () => void; onSaved: (message: string) => Promise<void> | void }) {
  const [providerID, setProviderID] = useState(data.providers[0]?.id ?? "");
  const [catalog, setCatalog] = useState<ProviderCatalogEntry | null>(null);
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [externalNames, setExternalNames] = useState<Record<string, string>>({});
  const [publish, setPublish] = useState(true);
  const [query, setQuery] = useState(initialQuery);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const provider = data.providers.find((item) => item.id === providerID);

  useEffect(() => {
    if (!provider) return;
    const catalogID = provider.options?.catalog_id || data.providerCatalog.find((entry) => entry.id === provider.id)?.id || data.providerCatalog.find((entry) => entry.id === provider.type)?.id;
    setCatalog(null);
    setSelected({});
    setExternalNames({});
    if (!catalogID) {
      setError(tx("该 Provider 没有可加载的目录模板，请在 Provider 页面同步或登记上游模型。"));
      return;
    }
    setBusy(true);
    setError("");
    adminFetch(api, `/api/admin/provider-catalog/${encodeURIComponent(catalogID)}`)
      .then(async (resp) => {
        if (!resp.ok) throw new Error(await readAdminError(resp, tx("加载 Provider 模型")));
        const payload = (await resp.json()) as { data: ProviderCatalogEntry };
        setCatalog(payload.data);
      })
      .catch((err) => setError(err instanceof Error ? err.message : tx("Provider 模型加载失败")))
      .finally(() => setBusy(false));
  }, [api, data.providerCatalog, provider, providerID]);

  const models = (catalog?.models ?? []).filter((model) => !query.trim() || JSON.stringify(model).toLowerCase().includes(query.trim().toLowerCase())).slice(0, 200);
  const selectedModels = (catalog?.models ?? []).filter((model) => selected[model.id]);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!providerID || selectedModels.length === 0) {
      setError(tx("请至少选择一个 Provider 上游模型。"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const resp = await adminFetch(api, "/api/admin/provider-models/import", { method: "POST", body: JSON.stringify({ provider_id: providerID, publish, models: selectedModels, external_names: externalNames }) });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("引入 Provider 模型")));
      const result = (await resp.json()) as { imported_models: number; created_models: number; created_routes: number };
      await onSaved(`${tx("已引入")} ${result.imported_models} ${tx("个上游模型")} · ${tx("新建对外模型")} ${result.created_models} · ${tx("新建映射")} ${result.created_routes}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("引入失败"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="modal-backdrop" role="presentation"><form className="modal provider-model-import-modal" onSubmit={submit}>
      <div className="modal-header"><div><p className="eyebrow">{tx("从 Provider 引入")}</p><h2>{tx("选择真实上游模型")}</h2></div><button className="icon-button" onClick={onClose} type="button"><X size={18} /></button></div>
      <div className="modal-body">
        {error ? <div className="inline-notice error"><AlertTriangle size={15} />{error}</div> : null}
        <label className="field"><span>Provider</span><select value={providerID} onChange={(event) => setProviderID(event.target.value)} required><option value="">{tx("请选择 Provider")}</option>{data.providers.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
        <div className="provider-import-mode"><div><strong>{tx("发布方式")}</strong><span>{tx("选中的模型默认创建同名对外模型，也可以逐个修改名称。")}</span></div><label><input checked={publish} onChange={(event) => setPublish(event.target.checked)} type="checkbox" />{tx("同时创建并发布对外模型")}</label></div>
        <div className="search-box"><Search size={15} /><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索上游模型")} /></div>
        <div className="provider-import-models">
          {busy && !catalog ? <div className="empty">{tx("正在加载模型目录...")}</div> : models.length === 0 ? <div className="empty">{tx("没有可选择的上游模型")}</div> : models.map((model) => {
            const checked = selected[model.id] === true;
            return <label className={checked ? "selected" : ""} key={model.id}><input checked={checked} onChange={(event) => { setSelected((current) => ({ ...current, [model.id]: event.target.checked })); if (event.target.checked) setExternalNames((current) => ({ ...current, [model.id]: current[model.id] || model.canonical_name || model.id })); }} type="checkbox" /><div><strong>{model.display_name || model.name || model.id}</strong><span>{model.id} · {model.type || "chat"} · {compactNumber(model.context_window || 0)} ctx</span>{checked && publish ? <input aria-label={tx("对外模型 ID")} value={externalNames[model.id] || ""} onChange={(event) => setExternalNames((current) => ({ ...current, [model.id]: event.target.value }))} onClick={(event) => event.stopPropagation()} /> : null}</div></label>;
          })}
        </div>
      </div>
      <div className="modal-actions"><button className="secondary-button" onClick={onManual} type="button">{tx("创建自定义映射")}</button><button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button><button className="button" disabled={busy || selectedModels.length === 0} type="submit">{busy ? tx("保存中") : tx(publish ? "引入并发布" : "仅引入")}</button></div>
    </form></div>
  );
}

function ModelMappingModal({ api, data, preset, onClose, onSaved }: { api: ApiContext; data: AppData; preset: { providerModel?: ProviderModel; externalModel?: Model }; onClose: () => void; onSaved: (message: string) => Promise<void> | void }) {
  const fixedExternal = preset.externalModel;
  const initialProviderModel = preset.providerModel;
  const [target, setTarget] = useState<"new" | "existing">(fixedExternal ? "existing" : "new");
  const [externalName, setExternalName] = useState(fixedExternal?.name || initialProviderModel?.canonical_name || initialProviderModel?.upstream_model || "");
  const [displayName, setDisplayName] = useState(initialProviderModel?.display_name || "");
  const [existingName, setExistingName] = useState(fixedExternal?.name || externalModels(data)[0]?.name || "");
  const [providerID, setProviderID] = useState(initialProviderModel?.provider_id || data.providers[0]?.id || "");
  const [upstreamModel, setUpstreamModel] = useState(initialProviderModel?.upstream_model || "");
  const [modality, setModality] = useState(initialProviderModel?.modality || "chat");
  const [contextWindow, setContextWindow] = useState(String(initialProviderModel?.context_window || ""));
  const [publish, setPublish] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const providerModels = data.providerModels.filter((model) => model.provider_id === providerID);
  const selectedProviderModel = providerModels.find((model) => model.upstream_model === upstreamModel);
  const finalExternalName = target === "existing" ? existingName : externalName.trim();
  const mismatch = finalExternalName && upstreamModel && normalizeVisibleModelName(finalExternalName) !== normalizeVisibleModelName(upstreamModel);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!providerID || !upstreamModel.trim() || !finalExternalName) {
      setError(tx("请填写对外模型、Provider 和上游模型。"));
      return;
    }
    if (!selectedProviderModel) {
      setError(tx("请先从 Provider 引入上游模型，再创建对外映射。"));
      return;
    }
    setBusy(true);
    setError("");
    try {
      const route = { model_name: finalExternalName, provider_id: providerID, provider_model: upstreamModel.trim(), status: "active", priority: 0, weight: 100, quality_score: 60, cost_score: 60, strategy: "balanced" };
      const path = target === "existing" ? "/api/admin/routing-rules" : "/api/admin/models";
      const body = target === "existing" ? route : {
        id: finalExternalName,
        name: finalExternalName,
        category: selectedProviderModel?.category || "custom",
        family: selectedProviderModel?.family || "custom",
        modality,
        context_window: Number(contextWindow) || selectedProviderModel?.context_window || 0,
        input_price_usd_per_1m: selectedProviderModel?.input_price_usd_per_1m || 0,
        cache_read_price_usd_per_1m: selectedProviderModel?.cache_read_price_usd_per_1m || 0,
        output_price_usd_per_1m: selectedProviderModel?.output_price_usd_per_1m || 0,
        capabilities: selectedProviderModel?.capabilities || [modality],
        supported_parameters: selectedProviderModel?.supported_parameters || [],
        metadata: { source: "manual-external", display_name: displayName, provider_id: providerID, upstream_model: upstreamModel.trim() },
        status: publish ? "active" : "disabled",
        routes: [route],
      };
      const resp = await adminFetch(api, path, { method: "POST", body: JSON.stringify(body) });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("保存模型映射")));
      await onSaved(target === "existing" ? tx("已添加新的 Provider 映射") : tx(publish ? "对外模型已创建并发布" : "对外模型已保存为草稿"));
    } catch (err) {
      setError(err instanceof Error ? err.message : tx("保存失败"));
    } finally {
      setBusy(false);
    }
  }

  return <div className="modal-backdrop" role="presentation"><form className="modal model-mapping-modal" onSubmit={submit}>
    <div className="modal-header"><div><p className="eyebrow">{tx("手工创建映射")}</p><h2>{fixedExternal ? fixedExternal.name : tx("定义对外模型和真实上游")}</h2></div><button className="icon-button" onClick={onClose} type="button"><X size={18} /></button></div>
    <div className="modal-body">
      {error ? <div className="inline-notice error"><AlertTriangle size={15} />{error}</div> : null}
      {!fixedExternal ? <div className="mapping-target-toggle"><button className={target === "new" ? "active" : ""} onClick={() => setTarget("new")} type="button">{tx("创建新对外模型")}</button><button className={target === "existing" ? "active" : ""} onClick={() => setTarget("existing")} type="button">{tx("映射到已有模型")}</button></div> : null}
      {target === "new" ? <div className="form-grid two"><label className="field"><span>{tx("对外模型 ID")} *</span><input value={externalName} onChange={(event) => setExternalName(event.target.value)} required /></label><label className="field"><span>{tx("显示名称")}</span><input value={displayName} onChange={(event) => setDisplayName(event.target.value)} /></label></div> : <label className="field"><span>{tx("已有对外模型")} *</span><select value={existingName} onChange={(event) => setExistingName(event.target.value)} required>{externalModels(data).map((model) => <option key={model.name} value={model.name}>{model.name}</option>)}</select></label>}
      <div className="mapping-chain-editor"><div><span>{tx("客户端请求")}</span><strong>{finalExternalName || "model"}</strong></div><ChevronRight /><div><span>Provider</span><strong>{findProvider(data, providerID)?.name || tx("请选择")}</strong></div><ChevronRight /><div><span>{tx("上游模型")}</span><strong>{upstreamModel || "provider model"}</strong></div></div>
      <div className="form-grid two"><label className="field"><span>Provider *</span><select value={providerID} onChange={(event) => { setProviderID(event.target.value); setUpstreamModel(""); }} required>{data.providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select></label><label className="field"><span>{tx("已引入的上游模型")} *</span><select disabled={providerModels.length === 0} value={upstreamModel} onChange={(event) => setUpstreamModel(event.target.value)} required><option value="">{tx("请选择上游模型")}</option>{providerModels.map((model) => <option key={model.id} value={model.upstream_model}>{model.upstream_model}{model.display_name && model.display_name !== model.upstream_model ? ` · ${model.display_name}` : ""}</option>)}</select><small>{providerModels.length ? tx("这里只能选择已引入当前 Provider 的上游模型。") : tx("该 Provider 暂无已引入模型，请先从 Provider 引入。")}</small></label></div>
      {target === "new" ? <div className="form-grid two"><label className="field"><span>{tx("模型类型")}</span><select value={modality} onChange={(event) => setModality(event.target.value)}><option value="chat">Chat</option><option value="embedding">Embedding</option><option value="image">Image</option><option value="audio">Audio</option><option value="rerank">Rerank</option></select></label><label className="field"><span>{tx("上下文窗口")}</span><input min="0" type="number" value={contextWindow} onChange={(event) => setContextWindow(event.target.value)} /></label></div> : null}
      {mismatch ? <div className="mapping-mismatch-warning"><AlertTriangle size={18} /><div><strong>{tx("对外名称与真实上游不同")}</strong><span>{finalExternalName} → {findProvider(data, providerID)?.name || providerID} / {upstreamModel}。{tx("请求日志和审计会同时记录两者。")}</span></div></div> : null}
      {target === "new" ? <label className="publish-check"><input checked={publish} onChange={(event) => setPublish(event.target.checked)} type="checkbox" /><div><strong>{tx("创建后立即发布")}</strong><span>{tx("关闭后保存为草稿，映射会保留但不会出现在 /v1/models。")}</span></div></label> : null}
    </div>
    <div className="modal-actions"><button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button><button className="button" disabled={busy || !selectedProviderModel} type="submit">{busy ? tx("保存中") : tx(target === "existing" ? "添加映射" : publish ? "创建并发布" : "保存草稿")}</button></div>
  </form></div>;
}

function ModelMappingDrawer({ data, model, onAdd, onClose, onEditRoute, onDeleteRoute }: { data: AppData; model: Model; onAdd: () => void; onClose: () => void; onEditRoute: (route: ModelRoute) => void; onDeleteRoute: (route: ModelRoute) => void }) {
  const routes = modelRoutesFor(model, data);
  return <div className="model-drawer-backdrop" onMouseDown={(event) => { if (event.currentTarget === event.target) onClose(); }} role="presentation"><aside className="model-mapping-drawer" role="dialog" aria-modal="true">
    <header><div><p className="eyebrow">{tx("对外模型映射")}</p><h2>{model.name}</h2><span>{tx("客户端模型名保持不变，可以替换或增加真实上游线路。")}</span></div><button className="icon-button" onClick={onClose} type="button"><X size={18} /></button></header>
    <div className="drawer-state-row"><StatusPill status={modelPublicationState(model, data) === "published" ? "active" : "disabled"} label={tx(publicationLabel(modelPublicationState(model, data)))} /><RuntimeStatus state={modelRuntimeState(model, data)} active={routes.filter((route) => route.status === "active").length} total={routes.length} /></div>
    <section><div className="drawer-section-head"><div><strong>{tx("上游线路")}</strong><span>{routes.length <= 1 ? tx("单线路保持简单；新增第二条后再配置主备和分流。") : tx("按优先级和权重执行故障转移与分流。")}</span></div><button className="secondary-button" onClick={onAdd} type="button"><Plus size={15} />{tx("添加线路")}</button></div>
      {routes.length === 0 ? <div className="model-directory-empty compact"><Link2 size={24} /><strong>{tx("尚未配置 Provider 映射")}</strong></div> : <div className="drawer-route-list">{routes.map((route, index) => { const provider = findProvider(data, route.provider_id); return <article key={route.id}><div className="route-role">{index === 0 ? tx("主线路") : route.priority === routes[0]?.priority ? tx("参与分流") : tx("故障备用")}</div><div className="route-chain"><strong>{provider?.name || route.provider_id}</strong><ChevronRight size={14} /><strong>{route.provider_model}</strong></div><div className="route-meta"><StatusPill status={route.status} /><span>P{route.priority} · W{route.weight} · {route.strategy || "balanced"}</span></div><div className="directory-row-actions"><button className="text-button" onClick={() => onEditRoute(route)} type="button">{tx("编辑")}</button><button className="danger-button" onClick={() => onDeleteRoute(route)} type="button">{tx("删除")}</button></div></article>; })}</div>}
    </section>
    <section className="drawer-contract"><strong>{tx("能力与计价口径")}</strong><div><span>{tx("能力")}</span><b>{(model.capabilities ?? []).join(" / ") || model.modality || "-"}</b></div><div><span>{tx("上下文")}</span><b>{compactNumber(model.context_window || 0)}</b></div><div><span>{tx("目录计价")}</span><b>{priceMetric(model.input_price_usd_per_1m)} / {priceMetric(model.output_price_usd_per_1m)}</b></div><small>{tx("自定义别名映射时，这些是 TokenHub 的对外声明；真实上游成本应以命中线路为准。")}</small></section>
  </aside></div>;
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

function normalizeVisibleModelName(value: string) {
  const slash = value.lastIndexOf("/");
  return (slash >= 0 ? value.slice(slash + 1) : value).trim().toLowerCase().replaceAll("_", "-");
}
