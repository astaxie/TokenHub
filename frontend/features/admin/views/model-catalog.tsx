// Shared catalog tabs, route management, and model brand icons.
// The model directory is rendered by model-directory.tsx.
import { Boxes, Gauge, Plus, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { type AppData, type Model, type ModelRoute, type ModelRoutePolicy, type ResourceConfig, type ViewKey } from "../core/types";
import { modelCatalogFilterLabel, modelCategory, modelCategoryInitial, modelCategoryLabel, modelCategoryTabs, notificationChannelTabs } from "../domain/catalog";
import { filterRouteModels, modelIsInDirectory, modelRoutesFor, reorderRoutes, routeModelCategories } from "../domain/entities";
import { countRatioWithUnit, tx } from "../i18n/runtime";
import { DataSection, StatusPill } from "../shared/ui";
import { modelBrandIconSource } from "./database-model-pricing";
import { ModelRoutingPolicyEditor, modelRoutePolicySignature } from "./model-routing-policy";
import { ModelGovernanceEmptyState } from "./model-governance-empty-state";
import { RouteConfigurationDialog, RouteSummaryTable } from "./route-management";

export function ModelCategoryTabs({
  data,
  view,
  active,
  onChange,
}: {
  data: AppData;
  view: ViewKey;
  active: string;
  onChange: (value: string) => void;
}) {
  const tabs = modelCategoryTabs(data, view);
  if (tabs.length <= 1) return null;
  return (
    <div className="category-tabs" role="tablist" aria-label={tx("模型分类")}>
      {tabs.map((tab) => (
        <button
          className={active === tab.key ? "category-tab active" : "category-tab"}
          key={tab.key}
          onClick={() => onChange(tab.key)}
          type="button"
        >
          <span>{tx(tab.label)}</span>
          <em>{tab.count}</em>
        </button>
      ))}
    </div>
  );
}

export function NotificationChannelTabs({
  data,
  active,
  onChange,
}: {
  data: AppData;
  active: string;
  onChange: (value: string) => void;
}) {
  const tabs = notificationChannelTabs(data);
  return (
    <div className="category-tabs" role="tablist" aria-label={tx("通知渠道类型")}>
      {tabs.map((tab) => (
        <button
          aria-selected={active === tab.key}
          className={active === tab.key ? "category-tab active" : "category-tab"}
          key={tab.key}
          onClick={() => onChange(tab.key)}
          role="tab"
          type="button"
        >
          <span>{tab.label}</span>
          <em>{tab.count}</em>
        </button>
      ))}
    </div>
  );
}

export function RouteStrategyView({
  config,
  data,
  initialQuery = "",
  loading,
  error,
  onClearError,
  onCreate,
  onOpenModels,
  onOpenProviders,
  onEdit,
  onDelete,
  onReorder,
  onSavePolicy,
}: {
  config: ResourceConfig<ModelRoute>;
  data: AppData;
  initialQuery?: string;
  loading: boolean;
  error?: string;
  onClearError?: () => void;
  onCreate: (model: Model) => void;
  onOpenModels: () => void;
  onOpenProviders: () => void;
  onEdit: (item: ModelRoute) => void;
  onDelete: (item: ModelRoute) => void;
  onReorder: (model: Model, routes: ModelRoute[]) => void;
  onSavePolicy: (model: Model, policy: ModelRoutePolicy) => void;
}) {
  const [category, setCategory] = useState("all");
  const [scope, setScope] = useState<"configured" | "all">(initialQuery ? "all" : "configured");
  const [query, setQuery] = useState(initialQuery);
  const [draggedRouteID, setDraggedRouteID] = useState("");
  const [selectedModelName, setSelectedModelName] = useState("");
  const selectedModel = data.models.find((model) => model.name === selectedModelName);
  const categories = routeModelCategories(data);
  useEffect(() => {
    setQuery(initialQuery);
    if (initialQuery) setScope("all");
  }, [initialQuery]);
  const filtered = useMemo(
    () => filterRouteModels(data, category, scope, query),
    [data, category, scope, query],
  );
  const configuredCount = data.models.filter((model) => modelRoutesFor(model, data).length > 0).length;
  const directoryModelCount = data.models.filter((model) => modelIsInDirectory(model, data)).length;
  const activeRouteCount = data.routes.filter((route) => route.status === "active").length;
  const firstDirectoryModel = data.models.find((model) => modelIsInDirectory(model, data));
  const deepLinkedDirectoryModel = Boolean(initialQuery.trim() && data.models.some((model) => model.name === initialQuery.trim() && modelIsInDirectory(model, data)));
  const hasImportedProviderModels = data.providers.length > 0 && data.providerModels.length > 0;
  const emptyStage = !hasImportedProviderModels ? "providers" : directoryModelCount === 0 ? "models" : "routes";

  if (!loading && !selectedModel && !deepLinkedDirectoryModel && (directoryModelCount === 0 || data.routes.length === 0)) {
    const title = emptyStage === "providers"
      ? "先引入可用的 Provider 模型"
      : emptyStage === "models"
        ? "先创建一个对外模型"
        : "还没有路由策略";
    const description = emptyStage === "providers"
      ? "先在 Provider 渠道添加上游服务并选择要引入的模型；Provider 模型价格用于记录真实成本与审计。"
      : emptyStage === "models"
        ? "从内置模型目录中挑选对外模型，选择已引入的 Provider 模型，并设置统一对外价格。"
        : "为对外模型添加 Provider 线路，并设置优先级、权重与流量策略。路由不会改变统一对外价格。";
    const actionLabel = emptyStage === "providers" ? "前往 Provider 渠道" : emptyStage === "models" ? "前往模型目录" : "为模型添加路由";
    const onAction: () => void = emptyStage === "providers"
      ? onOpenProviders
      : emptyStage === "models"
        ? onOpenModels
        : () => {
            if (firstDirectoryModel) onCreate(firstDirectoryModel);
          };
    return (
      <DataSection title={config.eyebrow}>
        <ModelGovernanceEmptyState
          stage={emptyStage}
          title={title}
          description={description}
          actionLabel={actionLabel}
          onAction={onAction}
        />
      </DataSection>
    );
  }

  return (
    <DataSection title={config.eyebrow}>
      <div className="route-management">
          <div className="model-filterbar">
            <div className="model-capability-tabs" role="tablist" aria-label={tx("路由显示范围")}>
              <button
                aria-selected={scope === "configured"}
                className={scope === "configured" ? "model-capability-tab active" : "model-capability-tab"}
                onClick={() => setScope("configured")}
                role="tab"
                type="button"
              >
                <Gauge size={14} />
                <span>{tx("已配置")}</span>
                <em>{configuredCount}</em>
              </button>
              <button
                aria-selected={scope === "all"}
                className={scope === "all" ? "model-capability-tab active" : "model-capability-tab"}
                onClick={() => setScope("all")}
                role="tab"
                type="button"
              >
                <Boxes size={14} />
                <span>{tx("全部模型")}</span>
                <em>{directoryModelCount}</em>
              </button>
            </div>
            <div className="model-catalog-actions">
              <select className="route-category-filter" aria-label={tx("模型分类")} value={category} onChange={(event) => setCategory(event.target.value)}>
                {categories.map((item) => <option key={item.key} value={item.key}>{tx(item.label)}</option>)}
              </select>
              <div className="search-box model-search">
                <Search size={16} />
                <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索模型或 Provider")} />
              </div>
            </div>
          </div>

          <div className="model-catalog-summary">
            <span>{tx(modelCatalogFilterLabel(categories, category))}</span>
            <strong>{filtered.length}</strong>
            <em>{tx("个模型")} · {countRatioWithUnit(activeRouteCount, data.routes.length, "条启用线路", "active route", "件の有効ルート")}</em>
          </div>

          {filtered.length === 0 ? (
            <div className="empty model-catalog-empty">{tx("没有匹配的模型路由")}</div>
          ) : (
            <RouteSummaryTable models={filtered} data={data} loading={loading} onConfigure={(model) => { onClearError?.(); setSelectedModelName(model.name); }} />
          )}
      </div>
      {selectedModel ? (
        <RouteConfigurationDialog
          key={selectedModel.name}
          model={selectedModel}
          models={data.models.filter((model) => modelIsInDirectory(model, data))}
          loading={loading}
          error={error}
          onClose={() => setSelectedModelName("")}
          onSelect={(model) => { onClearError?.(); setSelectedModelName(model.name); }}
          onCreate={() => onCreate(selectedModel)}
        >
          {({ onDirtyChange, guard, revision }) => {
            const routes = modelRoutesFor(selectedModel, data);
            return routes.length === 0 ? <div className="empty route-empty">{tx("该统一模型还没有 Provider 线路")}</div> : (
              <ModelRoutingPolicyEditor
                key={`${revision}:${modelRoutePolicySignature(routes)}:${selectedModel.metadata?.tokenhub_semantic_routing ?? ""}`}
                model={selectedModel}
                routes={routes}
                data={data}
                loading={loading}
                draggedRouteID={draggedRouteID}
                onDragStart={setDraggedRouteID}
                onDragEnd={() => setDraggedRouteID("")}
                onDrop={(targetRouteID) => {
                  const reordered = reorderRoutes(routes, draggedRouteID, targetRouteID);
                  setDraggedRouteID("");
                  if (reordered !== routes) onReorder(selectedModel, reordered);
                }}
                onEdit={(route) => guard(() => { onClearError?.(); onEdit(route); })}
                onDelete={(route) => guard(() => { onClearError?.(); onDelete(route); })}
                onSave={onSavePolicy}
                onDirtyChange={onDirtyChange}
              />
            );
          }}
        </RouteConfigurationDialog>
      ) : null}
    </DataSection>
  );
}

export function RouteModelCard({
  model,
  data,
  loading,
  draggedRouteID,
  onDragStart,
  onDragEnd,
  onDrop,
  onEdit,
  onDelete,
  onCreate,
  onSavePolicy,
}: {
  model: Model;
  data: AppData;
  loading: boolean;
  draggedRouteID: string;
  onDragStart: (routeID: string) => void;
  onDragEnd: () => void;
  onDrop: (targetRouteID: string) => void;
  onEdit: (route: ModelRoute) => void;
  onDelete: (route: ModelRoute) => void;
  onCreate: () => void;
  onSavePolicy: (model: Model, policy: ModelRoutePolicy) => void;
}) {
  const routes = modelRoutesFor(model, data);
  const activeRoutes = routes.filter((route) => route.status === "active");
  const category = modelCategory(model, data);
  const label = modelCategoryLabel(category, data);
  return (
    <article className="route-model-card">
      <div className="route-model-head">
        <div>
          <div className="model-card-brand compact">
            <span>{modelCategoryInitial(category, label)}</span>
            <div>
              <em>{label}</em>
              <strong>{model.modality || "chat"}</strong>
            </div>
          </div>
          <h2>{model.name}</h2>
        </div>
        <div className="route-model-stats">
          <div className="route-model-actions">
            <StatusPill status={routes.length > 0 ? "active" : "disabled"} label={routes.length > 0 ? `${activeRoutes.length}/${routes.length} ${tx("启用")}` : tx("未配置")} />
            <button className="secondary-button route-model-add" disabled={loading} onClick={onCreate} type="button">
              <Plus size={15} />
              {tx("添加线路")}
            </button>
          </div>
          <span>{tx("模型级路由策略")}</span>
        </div>
      </div>

      {routes.length === 0 ? (
        <div className="empty route-empty">{tx("该统一模型还没有 Provider 线路")}</div>
      ) : (
        <ModelRoutingPolicyEditor
          key={`${modelRoutePolicySignature(routes)}:${model.metadata?.tokenhub_semantic_routing ?? ""}`}
          model={model}
          routes={routes}
          data={data}
          loading={loading}
          draggedRouteID={draggedRouteID}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDrop={onDrop}
          onEdit={onEdit}
          onDelete={onDelete}
          onSave={onSavePolicy}
        />
      )}
    </article>
  );
}

export function ModelBrandIcon({ category, label, compact = false, data }: { category: string; label: string; compact?: boolean; data?: Pick<AppData, "plugins" | "providerAdapters"> }) {
  const source = modelBrandIconSource(category, data);
  const className = `model-brand-icon${compact ? " compact" : ""}${source ? "" : " fallback"}`;
  if (source) {
    return (
      <span aria-label={label} className={className} title={label}>
        <img alt="" src={source} />
      </span>
    );
  }
  return (
    <span aria-label={label} className={className} title={label}>
      <Boxes size={18} />
    </span>
  );
}
