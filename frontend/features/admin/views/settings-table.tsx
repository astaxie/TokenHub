import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Edit3, Info, Plus, Search, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { type AdminResource, type AdminUser, type APIKey, type AppData, type ModalState, type ResourceAction, type ResourceConfig, type SettingsTabKey, type ToolbarAction, type ViewKey } from "../core/types";
import { filterRows } from "../domain/catalog";
import { readPath, rowID, stringifyValue } from "../domain/entities";
import { formatNumber, formatTime } from "../domain/formatting";
import { settingsTabLabel } from "../domain/labels";
import { activeLanguage, type AppLanguage, countWithLabel, displayText, languageOptions, translatedCell, tx } from "../i18n/runtime";
import { defaultFormValues } from "../resources/payloads";
import { apiKeyStatusAction, APIKeyDownloadMenu, APIKeyStatusSwitch } from "../resources/project-key-config";
import { identityProviderConfig, roleConfig, systemSettingConfig } from "../resources/settings-config";
import { IdentityProviderEditModal } from "../shared/modals";
import { FieldInput, StatusPill } from "../shared/ui";
import { identityProviderInitialFormValues } from "../shell/auth";
import { CrudView } from "./crud-projects";

export function SettingsView({
  data,
  activeTab,
  language,
  onTabChange,
  onLanguageChange,
  onCreate,
  onEdit,
  onDelete,
  onAction,
  onToolbarAction,
}: {
  data: AppData;
  activeTab: SettingsTabKey;
  language: AppLanguage;
  onTabChange: (tab: SettingsTabKey) => void;
  onLanguageChange: (language: AppLanguage) => void;
  onCreate: (config: ResourceConfig<AdminResource>) => void;
  onEdit: (config: ResourceConfig<AdminResource>, item: AdminResource) => void;
  onDelete: (config: ResourceConfig<AdminResource>, item: AdminResource) => void;
  onAction: (action: ResourceAction<AdminResource>, item: AdminResource) => void;
  onToolbarAction: (action: ToolbarAction, items: AdminResource[]) => void;
}) {
  const configs = useMemo(() => [systemSettingConfig(), roleConfig(), identityProviderConfig()], []);
  const activeConfig = configs.find((config) => config.view === activeTab) ?? configs[0];
  const [queries, setQueries] = useState<Record<string, string>>({});
  const query = queries[activeConfig.view] ?? "";
  const allItems = activeConfig.list(data);
  const filteredItems = filterRows(allItems, query);
  const pagination = usePagination(filteredItems.length, `settings:${activeConfig.view}:${query}`);
  const pagedItems = useMemo(
    () => filteredItems.slice(pagination.startIndex, pagination.endIndex),
    [filteredItems, pagination.startIndex, pagination.endIndex],
  );

  return (
    <div className="settings-view">
      <div className="settings-tabs" role="tablist" aria-label={tx("系统设置分类")}>
        {configs.map((config) => (
          <button
            aria-selected={activeConfig.view === config.view}
            className={activeConfig.view === config.view ? "settings-tab active" : "settings-tab"}
            key={config.view}
            onClick={() => onTabChange(config.view as SettingsTabKey)}
            role="tab"
            type="button"
          >
            {settingsTabLabel(config.view as SettingsTabKey)}
          </button>
        ))}
      </div>
      {activeConfig.view === "settings" ? (
        <SystemSettingsPanel
          config={activeConfig}
          items={filteredItems as AdminResource[]}
          language={language}
          onLanguageChange={onLanguageChange}
          onEdit={(item) => onEdit(activeConfig, item)}
        />
      ) : (
        <CrudView
          config={activeConfig}
          data={data}
          items={pagedItems}
          totalItems={filteredItems.length}
          loading={false}
          query={query}
          pagination={pagination}
          categoryFilter="all"
          onCategoryFilter={() => undefined}
          onQuery={(value) => setQueries((current) => ({ ...current, [activeConfig.view]: value }))}
          onCreate={() => onCreate(activeConfig)}
          onEdit={(item) => onEdit(activeConfig, item)}
          onDelete={(item) => onDelete(activeConfig, item)}
          onAction={onAction}
          onToolbarAction={(action) => onToolbarAction(action, filteredItems)}
        />
      )}
    </div>
  );
}

export function LanguagePreferenceRow({
  language,
  onChange,
}: {
  language: AppLanguage;
  onChange: (language: AppLanguage) => void;
}) {
  const current = languageOptions.find((option) => option.value === language) ?? languageOptions[0];
  return (
    <div className="system-settings-preference">
      <div>
        <p className="eyebrow">{tx("控制台偏好")}</p>
        <strong>{tx("界面语言")}</strong>
        <span>{tx("选择控制台显示语言，偏好会保存在当前浏览器。")}</span>
      </div>
      <div className="language-card-control">
        <small>{tx("当前语言")}: {languageOptionLabel(current, language)}</small>
        <LanguageSwitcher language={language} onChange={onChange} />
      </div>
    </div>
  );
}

export function LanguageSwitcher({
  language,
  onChange,
  className,
}: {
  language: AppLanguage;
  onChange: (language: AppLanguage) => void;
  className?: string;
}) {
  return (
    <div className={className ? `language-switcher ${className}` : "language-switcher"} role="radiogroup" aria-label={tx("界面语言")}>
      {languageOptions.map((option) => (
        <button
          aria-checked={language === option.value}
          className={language === option.value ? "active" : ""}
          key={option.value}
          onClick={() => onChange(option.value)}
          role="radio"
          type="button"
        >
          <span>{languageOptionLabel(option, language)}</span>
        </button>
      ))}
    </div>
  );
}

export function languageOptionLabel(option: { label: string; nativeLabel: string }, language: AppLanguage) {
  return language === "en" ? option.label : option.nativeLabel;
}

export function SystemSettingsPanel({
  config,
  items,
  language,
  onLanguageChange,
  onEdit,
}: {
  config: ResourceConfig<AdminResource>;
  items: AdminResource[];
  language: AppLanguage;
  onLanguageChange: (language: AppLanguage) => void;
  onEdit: (item: AdminResource) => void;
}) {
  const current = items.find((item) => item.id === "cfg_gateway") ?? items[0];
  const settingFields = config.fields.filter((field) => !["name", "description", "status"].includes(field.key));
  return (
    <section className="section system-settings-section">
      <div className="section-header">
        <h2>{tx(config.eyebrow)}</h2>
      </div>
      <div className="section-body">
        <LanguagePreferenceRow language={language} onChange={onLanguageChange} />
        <div className="system-settings-intro">
          <div>
            <p className="eyebrow">{tx("全局平台范围")}</p>
            <h3>{tx("网关基础设置")}</h3>
            <p>{tx("这里维护 TokenHub 运行时读取的一组全局默认值；它不是配置模板，也不需要新增多条记录。")}</p>
          </div>
          {current ? (
            <button className="button" onClick={() => onEdit(current)} type="button">
              <Edit3 size={16} />
              {tx("编辑配置")}
            </button>
          ) : null}
        </div>
        {current ? (
          <>
            <div className="system-settings-summary">
              {settingFields.map((field) => (
                <div className="system-setting-item" key={field.key}>
                  <span>{tx(field.label)}</span>
                  <strong>{settingValue(current, field.key)}</strong>
                  {field.help ? <small>{tx(field.help)}</small> : null}
                </div>
              ))}
            </div>
            <div className="system-settings-meta">
              <div>
                <span>{tx("当前配置记录")}</span>
                <code>{current.id}</code>
                <StatusPill status={current.status} />
              </div>
              <small>{tx("更新时间")}: {formatTime(current.updated_at ?? "")}</small>
            </div>
            {items.length > 1 ? (
              <div className="system-settings-note" role="note">
                <Info size={16} />
                <span>{tx("检测到多条系统设置记录；运行时优先读取 cfg_gateway。请编辑当前配置，不需要新增同类记录。")}</span>
              </div>
            ) : null}
          </>
        ) : (
          <div className="resource-empty">
            <div className="resource-empty-icon">
              <Search size={18} />
            </div>
            <strong>{tx("未找到基础设置")}</strong>
            <span>{tx("系统启动时会初始化默认网关配置；请刷新数据或检查初始化脚本。")}</span>
          </div>
        )}
      </div>
    </section>
  );
}

function settingValue(item: AdminResource, key: string) {
  return stringifyValue(item.fields?.[key]) || "-";
}

export function APIKeyFlowHint({ data }: { data: AppData }) {
  return (
    <div className="workflow-hint">
      <div>
        <strong>{tx("Key 归属逻辑")}</strong>
        <span>{tx("内部应用配置项目下发放的 Key；额度、模型白名单、用量和成本都会归属到该项目。")}</span>
      </div>
      <div className="workflow-hint-stats">
        <span>{countWithLabel(data.projects.length, "个项目")}</span>
        <span>{countWithLabel(data.keys.length, "个 Key")}</span>
      </div>
    </div>
  );
}

export function RouteStrategyHint({ data }: { data: AppData }) {
  const activeRoutes = data.routes.filter((route) => route.status === "active").length;
  return (
    <div className="workflow-hint">
      <div>
        <strong>{tx("模型路由器")}</strong>
        <span>{tx("平衡模式综合权重、质量和成本；质量优先会先选高质量线路；成本优先会先选低成本线路。调用失败时会按候选顺序自动回退。")}</span>
      </div>
      <div className="workflow-hint-stats">
        <span>{activeRoutes} {tx("条启用线路")}</span>
        <span>{data.providers.filter((provider) => provider.status === "active").length} Provider</span>
      </div>
    </div>
  );
}

export function EntityTable<T>({
  config,
  data,
  apiBaseURL,
  items,
  loading = false,
  query = "",
  onCreate,
  onEdit,
  onDelete,
  onAction,
  onRowClick,
  selectedRowID,
  currentUser = null,
}: {
  config: ResourceConfig<T>;
  data: AppData;
  apiBaseURL?: string;
  items: T[];
  loading?: boolean;
  query?: string;
  onCreate?: () => void;
  onEdit: (item: T) => void;
  onDelete: (item: T) => void;
  onAction: (action: ResourceAction<T>, item: T) => void;
  onRowClick?: (item: T) => void;
  selectedRowID?: string;
  currentUser?: AdminUser | null;
}) {
  if (loading && items.length === 0) {
    return <TableSkeleton columns={Math.max(3, config.columns.length + 1)} rows={5} />;
  }
  if (items.length === 0) {
    return <ResourceEmptyState config={config} query={query} onCreate={onCreate} />;
  }
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {config.columns.map((column) => (
              <th key={column.key}>{tx(column.label)}</th>
            ))}
            <th>{tx("操作")}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr
              className={`${onRowClick ? "clickable-row" : ""} ${selectedRowID === rowID(item) ? "selected-row" : ""}`}
              key={rowID(item)}
              onClick={onRowClick ? () => onRowClick(item) : undefined}
            >
              {config.columns.map((column) => (
                <td key={column.key}>
                  {config.view === "api-keys" && column.key === "status" ? (
                    <APIKeyStatusSwitch
                      item={item as APIKey}
                      onToggle={(nextStatus) => onAction(apiKeyStatusAction(nextStatus) as unknown as ResourceAction<T>, item)}
                    />
                  ) : column.render ? (
                    translatedCell(column.render(item, data))
                  ) : (
                    displayCellValue(readPath(item, column.key))
                  )}
                </td>
              ))}
              <td>
                <div className="row-actions" onClick={(event) => event.stopPropagation()}>
                  {config.view === "api-keys" && apiBaseURL ? <APIKeyDownloadMenu baseURL={apiBaseURL} data={data} item={item as APIKey} /> : null}
                  {(config.actions ?? [])
                    .filter((action) => action.visible?.(item) ?? true)
                    .map((action) => (
                      <button
                        className="text-button"
                        key={action.label}
                        onClick={() => onAction(action, item)}
                        title={tx(action.title ?? action.label)}
                        type="button"
                      >
                        {tx(action.label)}
                      </button>
                    ))}
                  {config.update ? (
                    <button className="text-button" onClick={() => onEdit(item)} type="button">
                      {tx("编辑")}
                    </button>
                  ) : null}
                  {config.remove && (config.canRemove?.(item, currentUser) ?? true) ? (
                    <button className="danger-button" onClick={() => onDelete(item)} type="button" title={tx("删除")}>
                      <Trash2 size={15} />
                    </button>
                  ) : null}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function displayCellValue(value: unknown) {
  if (typeof value === "string") return displayText(value);
  return value as React.ReactNode;
}

export function ResourceEmptyState<T>({
  config,
  query,
  onCreate,
}: {
  config: ResourceConfig<T>;
  query: string;
  onCreate?: () => void;
}) {
  const copy = resourceEmptyCopy(config.view, Boolean(query.trim()));
  return (
    <div className="resource-empty">
      <div className="resource-empty-icon">
        <Search size={18} />
      </div>
      <strong>{tx(copy.title)}</strong>
      <span>{tx(copy.description)}</span>
      {onCreate && !query.trim() ? (
        <button className="button" onClick={onCreate} type="button">
          <Plus size={16} />
          {tx(config.createLabel ?? "新增")}
        </button>
      ) : null}
    </div>
  );
}

export function resourceEmptyCopy(view: ViewKey, filtered: boolean) {
  if (filtered) {
    return {
      title: "没有匹配结果",
      description: "清空搜索或换一个关键词再试。",
    };
  }
  switch (view) {
    case "providers":
      return {
        title: "还没有 Provider",
        description: "先接入上游服务商，再为模型配置路由。",
      };
    case "routes":
      return {
        title: "还没有模型路由",
        description: "路由决定模型请求会被转发到哪个 Provider。",
      };
    case "projects":
      return {
        title: "还没有项目空间",
        description: "项目是 Key、额度、成员和成本归属的基本单元。",
      };
    case "api-keys":
      return {
        title: "还没有 Key",
        description: "为项目发放 Key 后，业务应用才能调用网关。",
      };
    case "users":
      return {
        title: "还没有用户",
        description: "可以手动创建，也可以从 CSV 批量导入。",
      };
    case "identity-providers":
      return {
        title: "还没有身份源",
        description: "接入企业 OAuth/OIDC 后，用户可以使用 SSO 登录。",
      };
    default:
      return {
        title: "暂无数据",
        description: "当前视图还没有可展示的记录。",
      };
  }
}

export function TableSkeleton({ columns, rows }: { columns: number; rows: number }) {
  return (
    <div className="table-wrap skeleton-table" aria-busy="true">
      {Array.from({ length: rows }).map((_, rowIndex) => (
        <div className="skeleton-row" key={rowIndex} style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}>
          {Array.from({ length: columns }).map((__, columnIndex) => (
            <span key={columnIndex} />
          ))}
        </div>
      ))}
    </div>
  );
}

export function resultCountLabel(totalItems: number, query: string) {
  return query.trim() ? `${formatNumber(totalItems)} ${tx("条匹配")}` : `${formatNumber(totalItems)} ${tx("条记录")}`;
}

export type PaginationState = {
  page: number;
  pageSize: number;
  pageCount: number;
  startIndex: number;
  endIndex: number;
  setPage: (page: number) => void;
  setPageSize: (pageSize: number) => void;
};

export const pageSizeOptions = [10, 20, 50, 100];

export function usePagination(totalItems: number, resetKey: string): PaginationState {
  const [page, setPageState] = useState(1);
  const [pageSize, setPageSizeState] = useState(20);
  const pageCount = Math.max(1, Math.ceil(totalItems / pageSize));

  useEffect(() => {
    setPageState(1);
  }, [resetKey]);

  useEffect(() => {
    if (page > pageCount) {
      setPageState(pageCount);
    }
  }, [page, pageCount]);

  const safePage = Math.min(page, pageCount);
  const startIndex = totalItems === 0 ? 0 : (safePage - 1) * pageSize;
  const endIndex = Math.min(startIndex + pageSize, totalItems);

  return {
    page: safePage,
    pageSize,
    pageCount,
    startIndex,
    endIndex,
    setPage: (nextPage) => setPageState(Math.min(Math.max(nextPage, 1), pageCount)),
    setPageSize: (nextPageSize) => {
      setPageSizeState(nextPageSize);
      setPageState(1);
    },
  };
}

export function PaginationControls({
  pagination,
  totalItems,
}: {
  pagination: PaginationState;
  totalItems: number;
}) {
  if (totalItems <= pageSizeOptions[0]) return null;
  return (
    <div className="pagination">
      <div className="pagination-summary">
        {activeLanguage === "zh-CN"
          ? `第 ${pagination.startIndex + 1}-${pagination.endIndex} 条，共 ${totalItems} 条`
          : activeLanguage === "ja"
            ? `${pagination.startIndex + 1}-${pagination.endIndex} / ${totalItems} 件`
            : `${pagination.startIndex + 1}-${pagination.endIndex} of ${totalItems}`}
      </div>
      <div className="pagination-controls">
        <label className="page-size">
          <span>{tx("每页")}</span>
          <select
            value={pagination.pageSize}
            onChange={(event) => pagination.setPageSize(Number(event.target.value))}
          >
            {pageSizeOptions.map((option) => (
              <option key={option} value={option}>{option}</option>
            ))}
          </select>
        </label>
        <div className="page-buttons">
          <button
            type="button"
            title={tx("第一页")}
            onClick={() => pagination.setPage(1)}
            disabled={pagination.page <= 1}
          >
            <ChevronsLeft size={15} />
          </button>
          <button
            type="button"
            title={tx("上一页")}
            onClick={() => pagination.setPage(pagination.page - 1)}
            disabled={pagination.page <= 1}
          >
            <ChevronLeft size={15} />
          </button>
          <span>{pagination.page} / {pagination.pageCount}</span>
          <button
            type="button"
            title={tx("下一页")}
            onClick={() => pagination.setPage(pagination.page + 1)}
            disabled={pagination.page >= pagination.pageCount}
          >
            <ChevronRight size={15} />
          </button>
          <button
            type="button"
            title={tx("最后一页")}
            onClick={() => pagination.setPage(pagination.pageCount)}
            disabled={pagination.page >= pagination.pageCount}
          >
            <ChevronsRight size={15} />
          </button>
        </div>
      </div>
    </div>
  );
}

export function EditModal<T>({
  state,
  data,
  currentUser,
  loading,
  onClose,
  onSave,
}: {
  state: ModalState<T>;
  data: AppData;
  currentUser?: AdminUser | null;
  loading: boolean;
  onClose: () => void;
  onSave: (values: Record<string, string>) => void;
}) {
  const initial = {
    ...(state.item ? state.config.toForm?.(state.item) ?? {} : defaultFormValues(state.config, data, currentUser)),
    ...(state.initialValues ?? {}),
  };
  const [values, setValues] = useState<Record<string, string>>(
    state.config.view === "identity-providers" ? identityProviderInitialFormValues(initial, !state.item) : initial,
  );

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSave(values);
  }

  if (state.config.view === "identity-providers") {
    return (
      <IdentityProviderEditModal
        state={state as unknown as ModalState<AdminResource>}
        data={data}
        currentUser={currentUser}
        values={values}
        setValues={setValues}
        loading={loading}
        onClose={onClose}
        onSave={onSave}
      />
    );
  }

  return (
    <div className="modal-backdrop" role="presentation">
      <form className="modal" onSubmit={submit}>
        <div className="modal-header">
          <div>
            <p className="eyebrow">{state.item ? tx("编辑") : tx("新增")}</p>
            <h2>{tx(state.config.title)}</h2>
          </div>
          <button className="icon-button" onClick={onClose} type="button" title={tx("关闭")}>×</button>
        </div>
        <div className="modal-body">
          {state.config.fields.filter((field) => field.visible?.(values) ?? true).map((field) => (
            <FieldInput
              key={field.key}
              field={field}
              data={data}
              currentUser={currentUser}
              values={values}
              value={values[field.key] ?? ""}
              editing={Boolean(state.item)}
              onChange={(value) => setValues((prev) => ({
                ...prev,
                [field.key]: value,
                ...(state.config.view === "routes" && field.key === "provider_id" && prev.provider_id !== value ? { provider_model: "" } : {}),
              }))}
            />
          ))}
        </div>
        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("取消")}</button>
          <button className="button" disabled={loading} type="submit">{tx("保存")}</button>
        </div>
      </form>
    </div>
  );
}
