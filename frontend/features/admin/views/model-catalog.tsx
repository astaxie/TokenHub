import { AlertCircle, Boxes, ChevronDown, CircleHelp, Gauge, GripVertical, Plus, RefreshCw, Search, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { type AppData, type Model, type ModelRoute, type Provider, type ResourceAction, type ResourceConfig, type ViewKey } from "../core/types";
import { filterModelCatalog, hasThirdPartyRoute, modelCapabilityLabel, modelCatalogCapabilityTabs, modelCatalogCategories, modelCatalogFilterLabel, modelCategory, modelCategoryInitial, modelCategoryLabel, modelCategoryTabs, notificationChannelTabs, priceMetric } from "../domain/catalog";
import { filterRouteModels, findProvider, isCodexSubscriptionImageModel, modelRoutesFor, reorderRoutes, routeModelCategories } from "../domain/entities";
import { activeRouteCount, formatNumber, modelAvailabilitySummary, modelCatalogEmptyText, modelCategoryRank, routeStrategyLabel } from "../domain/formatting";
import { providerTypeLabel } from "../domain/labels";
import { tx } from "../i18n/runtime";
import { DataSection, ModelRouteProviders, StatusPill } from "../shared/ui";
import { clampNumber } from "./crud-projects";
import { modelBrandIconSource, modelCatalogMoney, modelCatalogPriceBaseline, modelCatalogPriceRow, modelCatalogPriceValue, modelDisplayTitle, modelEstimatedMonthlyCost } from "./database-model-pricing";
import { PaginationControls, RouteStrategyHint, usePagination } from "./settings-table";

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

export function ModelCatalogView({
  config,
  data,
  readOnly = false,
  onCreate,
  onEdit,
  onDelete,
  onBulkDelete,
  onRestoreDefaults,
  onAction,
}: {
  config: ResourceConfig<Model>;
  data: AppData;
  readOnly?: boolean;
  onCreate: () => void;
  onEdit: (item: Model) => void;
  onDelete: (item: Model) => void;
  onBulkDelete: (items: Model[]) => void;
  onRestoreDefaults: () => void;
  onAction: (action: ResourceAction<Model>, item: Model) => void;
}) {
  const [category, setCategory] = useState("all");
  const [capability, setCapability] = useState("all");
  const [query, setQuery] = useState("");
  const categories = modelCatalogCategories(data);
  const capabilities = modelCatalogCapabilityTabs(data);
  const filtered = useMemo(
    () => filterModelCatalog(data.models, data, category, capability, query),
    [data, category, capability, query],
  );

  return (
    <DataSection title={config.eyebrow}>
      <div className="model-catalog model-catalog-table-mode">
        <section className="model-catalog-main">
          <div className="model-category-strip">
            <div className="model-category-strip-head">
              <strong>{tx("模型大类")}</strong>
              <span>{data.models.length} {tx("个模型")}</span>
            </div>
            <div className="model-category-tabs" role="tablist" aria-label={tx("模型大类")}>
              {categories.map((item) => (
                <button
                  aria-selected={category === item.key}
                  className={category === item.key ? "model-category-tab active" : "model-category-tab"}
                  key={item.key}
                  onClick={() => setCategory(item.key)}
                  role="tab"
                  type="button"
                >
                  <ModelBrandIcon compact category={item.key} label={tx(item.label)} />
                  <strong>{tx(item.label)}</strong>
                  <em>{item.count}</em>
                </button>
              ))}
            </div>
          </div>

          <div className="model-filterbar">
            <div className="model-capability-tabs" role="tablist" aria-label={tx("模型能力筛选")}>
              {capabilities.map((item) => (
                <button
                  aria-selected={capability === item.key}
                  className={capability === item.key ? "model-capability-tab active" : "model-capability-tab"}
                  key={item.key}
                  onClick={() => setCapability(item.key)}
                  role="tab"
                type="button"
              >
                <item.icon size={14} />
                <span>{tx(item.label)}</span>
                <em>{item.count}</em>
              </button>
              ))}
            </div>
            <div className="model-catalog-actions">
              <div className="search-box model-search">
                <Search size={16} />
                <input
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder={tx("搜索模型名称或 ID")}
                />
              </div>
              {!readOnly ? (
                <button className="button" onClick={onCreate} type="button">
                  <Plus size={17} />
                  {tx(config.createLabel ?? "新增模型")}
                </button>
              ) : null}
              {!readOnly ? (
                <button className="secondary-button" onClick={onRestoreDefaults} type="button">
                  <RefreshCw size={16} />
                  {tx("恢复出厂目录")}
                </button>
              ) : null}
            </div>
          </div>

          <div className="model-catalog-summary">
            <span>{tx(modelCatalogFilterLabel(categories, category))}</span>
            <strong>{filtered.length}</strong>
            <em>{tx("个匹配模型")}</em>
          </div>
          <div className="model-availability-note">
            <AlertCircle size={15} />
            <span>{tx(readOnly ? "普通用户看到的是当前账号可见的模型；实际调用还会受项目 Key 白名单和项目权限限制。" : "可用模型需要同时满足：模型目录启用、至少一条路由启用、Provider 或账号资源健康。")}</span>
          </div>

          {filtered.length === 0 ? (
            <div className="empty model-catalog-empty">
              {modelCatalogEmptyText(data, readOnly, query)}
            </div>
          ) : (
            <ModelCatalogPriceTable
              models={filtered}
              data={data}
              readOnly={readOnly}
              actions={readOnly ? [] : config.actions ?? []}
              onAction={onAction}
              onEdit={readOnly ? undefined : onEdit}
              onDelete={readOnly ? undefined : onDelete}
              onBulkDelete={readOnly ? undefined : onBulkDelete}
            />
          )}
        </section>
      </div>
    </DataSection>
  );
}

export type ModelCatalogPriceSortKey = "default" | "name" | "input" | "output" | "cache" | "context" | "monthly" | "index";

export type ModelCatalogPriceSortDirection = "asc" | "desc";

export type ModelCatalogPriceSort = {
  key: ModelCatalogPriceSortKey;
  direction: ModelCatalogPriceSortDirection;
};

export type ModelCatalogPriceRowData = ReturnType<typeof modelCatalogPriceRow>;

export function ModelCatalogPriceTable({
  models,
  data,
  readOnly,
  actions,
  onAction,
  onEdit,
  onDelete,
  onBulkDelete,
}: {
  models: Model[];
  data: AppData;
  readOnly: boolean;
  actions: ResourceAction<Model>[];
  onAction: (action: ResourceAction<Model>, item: Model) => void;
  onEdit?: (item: Model) => void;
  onDelete?: (item: Model) => void;
  onBulkDelete?: (items: Model[]) => void;
}) {
  const [sort, setSort] = useState<ModelCatalogPriceSort>({ key: "default", direction: "asc" });
  const [selectedModelNames, setSelectedModelNames] = useState<Set<string>>(() => new Set());
  const defaultSorted = modelCatalogPriceSortedModels(models);
  const baseline = modelCatalogPriceBaseline(defaultSorted);
  const rows = modelCatalogSortRows(defaultSorted.map((model) => modelCatalogPriceRow(model, data, readOnly, baseline)), sort);
  const paginationResetKey = `model-catalog:${sort.key}:${sort.direction}:${models.map((model) => model.name).join("|")}`;
  const pagination = usePagination(rows.length, paginationResetKey);
  const pagedRows = rows.slice(pagination.startIndex, pagination.endIndex);
  const maxIndex = Math.max(1, ...rows.map((row) => row.priceIndex || 0));
  const pageModelNames = pagedRows.map((row) => row.model.name);
  const pageSelectableCount = pageModelNames.length;
  const allPageSelected = pageSelectableCount > 0 && pageModelNames.every((name) => selectedModelNames.has(name));
  const somePageSelected = pageModelNames.some((name) => selectedModelNames.has(name));
  const selectedModels = rows.map((row) => row.model).filter((model) => selectedModelNames.has(model.name));

  useEffect(() => {
    const visibleNames = new Set(models.map((model) => model.name));
    setSelectedModelNames((current) => {
      const next = new Set<string>();
      current.forEach((name) => {
        if (visibleNames.has(name)) next.add(name);
      });
      return next.size === current.size ? current : next;
    });
  }, [models]);

  function toggleModelSelection(name: string, checked: boolean) {
    setSelectedModelNames((current) => {
      const next = new Set(current);
      if (checked) {
        next.add(name);
      } else {
        next.delete(name);
      }
      return next;
    });
  }

  function togglePageSelection(checked: boolean) {
    setSelectedModelNames((current) => {
      const next = new Set(current);
      for (const name of pageModelNames) {
        if (checked) {
          next.add(name);
        } else {
          next.delete(name);
        }
      }
      return next;
    });
  }

  return (
    <>
      {!readOnly ? (
        <div className="model-bulk-toolbar">
          <span>{selectedModels.length > 0 ? `${selectedModels.length} ${tx("个已选")}` : tx("未选择模型")}</span>
          <div>
            <button className="secondary-button" disabled={selectedModels.length === 0} onClick={() => setSelectedModelNames(new Set())} type="button">
              {tx("清除选择")}
            </button>
            <button className="danger-button model-bulk-delete" disabled={selectedModels.length === 0 || !onBulkDelete} onClick={() => onBulkDelete?.(selectedModels)} type="button">
              <Trash2 size={15} />
              {tx("批量删除")}
            </button>
          </div>
        </div>
      ) : null}
      <div className="model-price-table-wrap">
        <table className={readOnly ? "model-price-table" : "model-price-table admin selectable"}>
          <thead>
            <tr>
              {!readOnly ? (
                <th className="model-price-select">
                  <input
                    aria-label={tx("全选")}
                    checked={allPageSelected}
                    ref={(input) => {
                      if (input) input.indeterminate = somePageSelected && !allPageSelected;
                    }}
                    onChange={(event) => togglePageSelection(event.currentTarget.checked)}
                    type="checkbox"
                  />
                </th>
              ) : null}
              <th aria-sort={modelCatalogSortAria(sort, "name")}>
                <ModelCatalogSortHeader label="模型" sortKey="name" sort={sort} onSort={setSort} />
              </th>
              <th>{tx("类型")}</th>
              <th aria-sort={modelCatalogSortAria(sort, "input")}>
                <ModelCatalogSortHeader label="输入价" sortKey="input" sort={sort} onSort={setSort} />
              </th>
              <th aria-sort={modelCatalogSortAria(sort, "output")}>
                <ModelCatalogSortHeader label="输出价" sortKey="output" sort={sort} onSort={setSort} />
              </th>
              <th aria-sort={modelCatalogSortAria(sort, "cache")}>
                <ModelCatalogSortHeader label="缓存读" sortKey="cache" sort={sort} onSort={setSort} />
              </th>
              <th aria-sort={modelCatalogSortAria(sort, "context")}>
                <ModelCatalogSortHeader label="上下文" sortKey="context" sort={sort} onSort={setSort} />
              </th>
              <th aria-sort={modelCatalogSortAria(sort, "monthly")}>
                <ModelCatalogSortHeader label="估算月成本" sortKey="monthly" sort={sort} onSort={setSort} />
              </th>
              <th aria-sort={modelCatalogSortAria(sort, "index")}>
                <ModelCatalogSortHeader label="价格指数" sortKey="index" sort={sort} onSort={setSort} />
              </th>
              <th>{tx("来源")}</th>
              {!readOnly ? <th>{tx("操作")}</th> : null}
            </tr>
          </thead>
          <tbody>
            {pagedRows.map((row) => {
              const visibleActions = actions.filter((action) => !action.visible || action.visible(row.model));
              const selected = selectedModelNames.has(row.model.name);
              const rowClassName = [
                row.availability.tone === "blocked" && !readOnly ? "unrouted" : "",
                selected ? "selected-model-row" : "",
              ].filter(Boolean).join(" ") || undefined;
              return (
                <tr className={rowClassName} key={row.model.name}>
                  {!readOnly ? (
                    <td className="model-price-select">
                      <input
                        aria-label={row.model.name}
                        checked={selected}
                        onChange={(event) => toggleModelSelection(row.model.name, event.currentTarget.checked)}
                        type="checkbox"
                      />
                    </td>
                  ) : null}
                <td>
                  <div className="model-price-name">
                    <ModelBrandIcon category={row.category} label={row.categoryLabel} />
                    <div>
                      <strong>{modelDisplayTitle(row.model)}</strong>
                      <span>{row.categoryLabel} · {row.model.name}</span>
                    </div>
                  </div>
                </td>
                <td>
                  <span className={`model-type-badge ${row.typeTone}`}>{tx(row.typeLabel)}</span>
                </td>
                <td><strong className="model-price-number">{modelCatalogPriceValue(row.inputPrice)}</strong></td>
                <td><strong className="model-price-number output">{modelCatalogPriceValue(row.outputPrice)}</strong></td>
                <td>
                  <span
                    aria-label={`${modelCatalogPriceValue(row.cacheReadPrice)}. ${row.cacheReadPriceHint}`}
                    className={`model-cache-price ${row.cacheReadPriceSource}`}
                    data-tooltip={row.cacheReadPriceHint}
                    tabIndex={0}
                  >
                    <strong className={row.cacheReadPrice ? "model-price-number" : "model-price-number muted"}>
                      {modelCatalogPriceValue(row.cacheReadPrice)}
                    </strong>
                    <CircleHelp aria-hidden="true" size={13} />
                  </span>
                </td>
                <td>
                  <div className="model-context-cell">
                    <strong>{row.contextLabel}</strong>
                    <span>{row.contextDetail}</span>
                  </div>
                </td>
                <td><strong className="model-monthly-cost">{row.monthlyCost > 0 ? `$${modelCatalogMoney(row.monthlyCost)}` : "-"}</strong></td>
                <td>
                  <div className="model-price-index">
                    <span>
                      <i style={{ width: row.priceIndex > 0 ? `${clampNumber((row.priceIndex / maxIndex) * 100, 8, 100)}%` : "0%" }} />
                    </span>
                    <strong>{row.priceIndex > 0 ? `${row.priceIndex.toFixed(2)}x` : "-"}</strong>
                  </div>
                </td>
                <td>
                  <div className="model-source-cell">
                    <span className={`model-source-pill ${row.sourceTone}`}>{tx(row.sourceLabel)}</span>
                    <small>{row.availability.activeRoutes}/{row.availability.totalRoutes} {tx("启用路由")}</small>
                  </div>
                </td>
                {!readOnly ? (
                  <td>
                    <div className="model-row-actions">
                      {visibleActions.map((action) => (
                        <button className="text-button" key={action.label} onClick={() => onAction(action, row.model)} type="button">
                          {tx(action.label)}
                        </button>
                      ))}
                      {onEdit ? <button className="text-button" onClick={() => onEdit(row.model)} type="button">{tx("编辑")}</button> : null}
                      {onDelete ? (
                        <button className="danger-button" onClick={() => onDelete(row.model)} title={tx("删除")} type="button">
                          <Trash2 size={15} />
                        </button>
                      ) : null}
                    </div>
                  </td>
                ) : null}
              </tr>
            );
          })}
          </tbody>
        </table>
      </div>
      <PaginationControls pagination={pagination} totalItems={rows.length} />
    </>
  );
}

export function RouteStrategyView({
  config,
  data,
  loading,
  onCreate,
  onEdit,
  onDelete,
  onReorder,
}: {
  config: ResourceConfig<ModelRoute>;
  data: AppData;
  loading: boolean;
  onCreate: () => void;
  onEdit: (item: ModelRoute) => void;
  onDelete: (item: ModelRoute) => void;
  onReorder: (model: Model, routes: ModelRoute[]) => void;
}) {
  const [category, setCategory] = useState("all");
  const [scope, setScope] = useState<"configured" | "all">("configured");
  const [query, setQuery] = useState("");
  const [draggedRouteID, setDraggedRouteID] = useState("");
  const categories = routeModelCategories(data);
  const filtered = useMemo(
    () => filterRouteModels(data, category, scope, query),
    [data, category, scope, query],
  );
  const configuredCount = data.models.filter((model) => modelRoutesFor(model, data).length > 0).length;
  const activeRouteCount = data.routes.filter((route) => route.status === "active").length;

  return (
    <DataSection title={config.eyebrow}>
      <RouteStrategyHint data={data} />
      <div className="route-matrix">
        <aside className="model-catalog-sidebar">
          <div className="model-catalog-sidebar-head">
            <strong>{tx("统一模型")}</strong>
            <span>{configuredCount} {tx("个已配置路由")}</span>
          </div>
          <div className="model-provider-list">
            {categories.map((item) => (
              <button
                className={category === item.key ? "model-provider-filter active" : "model-provider-filter"}
                key={item.key}
                onClick={() => setCategory(item.key)}
                type="button"
              >
                <span className="model-provider-icon">{modelCategoryInitial(item.key, item.label)}</span>
                <strong>{tx(item.label)}</strong>
                <em>{item.count}</em>
              </button>
            ))}
          </div>
        </aside>

        <section className="model-catalog-main">
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
                <em>{data.models.length}</em>
              </button>
            </div>
            <div className="model-catalog-actions">
              <div className="search-box model-search">
                <Search size={16} />
                <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={tx("搜索模型或 Provider")} />
              </div>
              <button className="button" onClick={onCreate} type="button">
                <Plus size={17} />
                {tx(config.createLabel ?? "新增路由")}
              </button>
            </div>
          </div>

          <div className="model-catalog-summary">
            <span>{tx(modelCatalogFilterLabel(categories, category))}</span>
            <strong>{filtered.length}</strong>
            <em>{tx("个模型")} · {activeRouteCount}/{data.routes.length} {tx("条启用线路")}</em>
          </div>

          {filtered.length === 0 ? (
            <div className="empty model-catalog-empty">{tx("没有匹配的模型路由")}</div>
          ) : (
            <div className="route-model-list">
              {filtered.map((model) => (
                <RouteModelCard
                  key={model.name}
                  model={model}
                  data={data}
                  loading={loading}
                  draggedRouteID={draggedRouteID}
                  onDragStart={setDraggedRouteID}
                  onDragEnd={() => setDraggedRouteID("")}
                  onDrop={(targetRouteID) => {
                    const routes = modelRoutesFor(model, data);
                    const reordered = reorderRoutes(routes, draggedRouteID, targetRouteID);
                    setDraggedRouteID("");
                    if (reordered !== routes) onReorder(model, reordered);
                  }}
                  onEdit={onEdit}
                  onDelete={onDelete}
                />
              ))}
            </div>
          )}
        </section>
      </div>
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
}) {
  const routes = modelRoutesFor(model, data);
  const activeRoutes = routes.filter((route) => route.status === "active");
  const category = modelCategory(model);
  return (
    <article className="route-model-card">
      <div className="route-model-head">
        <div>
          <div className="model-card-brand compact">
            <span>{modelCategoryInitial(category, modelCategoryLabel(category))}</span>
            <div>
              <em>{modelCategoryLabel(category)}</em>
              <strong>{model.modality || "chat"}</strong>
            </div>
          </div>
          <h2>{model.name}</h2>
        </div>
        <div className="route-model-stats">
          <StatusPill status={routes.length > 0 ? "active" : "disabled"} label={routes.length > 0 ? `${activeRoutes.length}/${routes.length} ${tx("启用")}` : tx("未配置")} />
          <span>{tx("按上到下顺序调用")}</span>
        </div>
      </div>

      {routes.length === 0 ? (
        <div className="empty route-empty">{tx("该统一模型还没有 Provider 线路")}</div>
      ) : (
        <div className="route-order-list">
          {routes.map((route, index) => (
            <RouteProviderRow
              key={route.id}
              route={route}
              index={index}
              provider={findProvider(data, route.provider_id)}
              dragging={draggedRouteID === route.id}
              loading={loading}
              onDragStart={() => onDragStart(route.id)}
              onDragEnd={onDragEnd}
              onDrop={() => onDrop(route.id)}
              onEdit={() => onEdit(route)}
              onDelete={() => onDelete(route)}
            />
          ))}
        </div>
      )}
    </article>
  );
}

export function RouteProviderRow({
  route,
  provider,
  index,
  dragging,
  loading,
  onDragStart,
  onDragEnd,
  onDrop,
  onEdit,
  onDelete,
}: {
  route: ModelRoute;
  provider?: Provider;
  index: number;
  dragging: boolean;
  loading: boolean;
  onDragStart: () => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  return (
    <div
      className={dragging ? "route-provider-row dragging" : "route-provider-row"}
      draggable={!loading}
      onDragStart={(event) => {
        event.dataTransfer.effectAllowed = "move";
        onDragStart();
      }}
      onDragOver={(event) => {
        event.preventDefault();
        event.dataTransfer.dropEffect = "move";
      }}
      onDragEnd={onDragEnd}
      onDrop={(event) => {
        event.preventDefault();
        onDrop();
      }}
    >
      <button className="route-drag-handle" disabled={loading} title={tx("拖动调整调用顺序")} type="button">
        <GripVertical size={15} />
      </button>
      <div className="route-order-badge">{index === 0 ? tx("主") : index + 1}</div>
      <div className="route-provider-main">
        <strong>{provider?.name || route.provider_id}</strong>
        <span>{providerTypeLabel(provider?.type)} · {provider?.base_url || tx("未配置 Base URL")}</span>
      </div>
      <div className="route-upstream-model">
        <strong>{route.provider_model}</strong>
        <span>{routeStrategyLabel(route.strategy)} · P{route.priority} · W{route.weight}</span>
      </div>
      <StatusPill status={route.status} />
      <div className="route-row-actions">
        <button className="text-button" onClick={onEdit} type="button">{tx("编辑")}</button>
        <button className="danger-button" onClick={onDelete} title={tx("删除")} type="button">
          <Trash2 size={15} />
        </button>
      </div>
    </div>
  );
}

export function ModelCatalogCard({
  model,
  data,
  readOnly = false,
  actions,
  onAction,
  onEdit,
  onDelete,
}: {
  model: Model;
  data: AppData;
  readOnly?: boolean;
  actions: ResourceAction<Model>[];
  onAction: (action: ResourceAction<Model>, item: Model) => void;
  onEdit?: (item: Model) => void;
  onDelete?: (item: Model) => void;
}) {
  const category = modelCategory(model);
  const availability = modelAvailabilitySummary(model, data, readOnly);
  const routeCount = availability.activeRoutes;
  const hasConfiguredRoute = availability.activeRoutes > 0;
  const codexSubscriptionImage = isCodexSubscriptionImageModel(model);
  const cardClassName = !readOnly && availability.tone === "blocked" ? "model-card unrouted" : "model-card";
  return (
    <article className={cardClassName}>
      <div className="model-card-head">
        <div className="model-card-brand">
          <span>{modelCategoryInitial(category, modelCategoryLabel(category))}</span>
          <div>
            <em>{modelCategoryLabel(category)}</em>
            <strong>{model.modality || "chat"}</strong>
          </div>
        </div>
        <StatusPill status={model.status} />
      </div>

      <h2>{model.name}</h2>

      <div className="model-card-tags">
        <span>{modelCapabilityLabel(model)}</span>
        {readOnly ? (
          <span className="official">{tx(availability.label)}</span>
        ) : (
          <>
            <span className={hasConfiguredRoute ? undefined : "unrouted-tag"}>
              {codexSubscriptionImage
                ? (hasConfiguredRoute ? `${routeCount} ${tx("个生图账号")}` : tx("暂无可生图账号"))
                : (hasConfiguredRoute ? `${routeCount} ${tx("条线路")}` : tx("未配置线路"))}
            </span>
            {hasThirdPartyRoute(model, data) ? <span className="third">{tx("三方资源")}</span> : <span className="official">{tx("官方资源")}</span>}
          </>
        )}
      </div>

      <div className={`model-availability ${availability.tone}`}>
        <strong>{tx(availability.label)}</strong>
        <span>{tx(availability.detail)}</span>
      </div>

      <div className="model-card-pricing">
        <ModelMetric label="输入" value={priceMetric(model.input_price_usd_per_1m)} muted={!model.input_price_usd_per_1m} />
        <ModelMetric label="输出" value={priceMetric(model.output_price_usd_per_1m)} muted={!model.output_price_usd_per_1m} />
        <ModelMetric label="上下文" value={model.context_window ? formatNumber(model.context_window) : "-"} />
        <ModelMetric label="Embedding" value={priceMetric(model.embedding_price_usd_per_1m)} muted={!model.embedding_price_usd_per_1m} />
      </div>

      {!readOnly ? (
        <div className="model-card-routes">
          <ModelRouteProviders model={model} data={data} />
        </div>
      ) : null}

      {actions.length > 0 || onEdit || onDelete ? (
        <div className="model-card-actions">
          {actions.map((action) => (
            <button className="text-button" key={action.label} onClick={() => onAction(action, model)} type="button">
              {tx(action.label)}
            </button>
          ))}
          {onEdit ? <button className="text-button" onClick={() => onEdit(model)} type="button">{tx("编辑")}</button> : null}
          {onDelete ? (
            <button className="danger-button" onClick={() => onDelete(model)} title={tx("删除")} type="button">
              <Trash2 size={15} />
            </button>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

export function ModelMetric({ label, value, muted }: { label: string; value: string; muted?: boolean }) {
  return (
    <div className={muted ? "model-metric muted" : "model-metric"}>
      <strong>{value}</strong>
      <span>{tx(label)}</span>
    </div>
  );
}

export function ModelBrandIcon({ category, label, compact = false }: { category: string; label: string; compact?: boolean }) {
  const source = modelBrandIconSource(category);
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

export function ModelCatalogSortHeader({
  label,
  sortKey,
  sort,
  onSort,
}: {
  label: string;
  sortKey: Exclude<ModelCatalogPriceSortKey, "default">;
  sort: ModelCatalogPriceSort;
  onSort: (sort: ModelCatalogPriceSort) => void;
}) {
  const active = sort.key === sortKey;
  return (
    <button
      className={active ? `model-sort-button active ${sort.direction}` : "model-sort-button"}
      onClick={() => onSort(modelCatalogNextSort(sort, sortKey))}
      title={tx("点击排序")}
      type="button"
    >
      <span>{tx(label)}</span>
      <ChevronDown aria-hidden="true" className="model-sort-icon" size={13} />
    </button>
  );
}

export function modelCatalogNextSort(current: ModelCatalogPriceSort, key: Exclude<ModelCatalogPriceSortKey, "default">): ModelCatalogPriceSort {
  if (current.key === key) {
    return { key, direction: current.direction === "asc" ? "desc" : "asc" };
  }
  return { key, direction: modelCatalogDefaultSortDirection(key) };
}

export function modelCatalogDefaultSortDirection(key: ModelCatalogPriceSortKey): ModelCatalogPriceSortDirection {
  if (key === "name" || key === "input" || key === "output" || key === "cache" || key === "monthly" || key === "index") return "asc";
  return "desc";
}

export function modelCatalogSortAria(sort: ModelCatalogPriceSort, key: ModelCatalogPriceSortKey) {
  if (sort.key !== key) return "none";
  return sort.direction === "asc" ? "ascending" : "descending";
}

export function modelCatalogPriceSortedModels(models: Model[]) {
  return models.slice().sort((left, right) => {
    const leftCost = modelEstimatedMonthlyCost(left);
    const rightCost = modelEstimatedMonthlyCost(right);
    const leftMissingPrice = leftCost <= 0 ? 1 : 0;
    const rightMissingPrice = rightCost <= 0 ? 1 : 0;
    return leftMissingPrice - rightMissingPrice
      || leftCost - rightCost
      || modelCategoryRank(left) - modelCategoryRank(right)
      || left.name.localeCompare(right.name);
  });
}

export function modelCatalogSortRows(rows: ModelCatalogPriceRowData[], sort: ModelCatalogPriceSort) {
  if (sort.key === "default") return rows;
  const direction = sort.direction === "asc" ? 1 : -1;
  return rows
    .map((row, index) => ({ row, index }))
    .sort((left, right) => {
      const compared = modelCatalogCompareSortValues(
        modelCatalogSortValue(left.row, sort.key),
        modelCatalogSortValue(right.row, sort.key),
        direction,
      );
      return compared || left.index - right.index;
    })
    .map((item) => item.row);
}

export function modelCatalogSortValue(row: ModelCatalogPriceRowData, key: ModelCatalogPriceSortKey) {
  switch (key) {
    case "name":
      return modelDisplayTitle(row.model).toLowerCase();
    case "input":
      return row.inputPrice || undefined;
    case "output":
      return row.outputPrice || undefined;
    case "cache":
      return row.cacheReadPrice || undefined;
    case "context":
      return row.model.context_window || undefined;
    case "monthly":
      return row.monthlyCost || undefined;
    case "index":
      return row.priceIndex || undefined;
    default:
      return undefined;
  }
}

export function modelCatalogCompareSortValues(left: string | number | undefined, right: string | number | undefined, direction: number) {
  const leftMissing = left === undefined || left === "";
  const rightMissing = right === undefined || right === "";
  if (leftMissing && rightMissing) return 0;
  if (leftMissing) return 1;
  if (rightMissing) return -1;
  if (typeof left === "string" || typeof right === "string") {
    return String(left).localeCompare(String(right)) * direction;
  }
  return (left - right) * direction;
}
