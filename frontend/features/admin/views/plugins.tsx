import { Boxes, Clock3, Download, ExternalLink, Layers3, PackageOpen, Search, Settings2, ShieldCheck } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { type ApiContext, type AppData, type PluginDescriptor } from "../core/types";
import {
  pluginExtensionCategories,
  pluginMarketplaceWebsiteURL,
  pluginStatusFilters,
  type PluginExtensionCategoryKey,
  type PluginManagerTabKey,
  type PluginStatusFilterKey,
} from "../domain/plugin-management";
import { pluginManagerDisplayState, pluginManagerDistributionReady, pluginManagerLifecycleState } from "../domain/plugin-manager";
import { localizedPluginName } from "../domain/plugin-localization";
import { type PluginDetailSection } from "../domain/plugin-detail-route";
import { type PluginPermissionDiffPreviewPayload } from "../domain/plugin-permission-diff";
import { formatTranslationTemplate, languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { PaginationControls, usePagination } from "../shared/pagination";
import { StatusPill } from "../shared/ui";
import { emptyInstallDraft, PluginInstallFields, pluginInstallRequestBody, type PluginInstallDraft } from "./plugin-install-form";
import { PluginDeleteControl, PluginLifecycleControl, pluginWithLifecycleDraft, type PluginDeleteDraft, type PluginRollbackDraft, type PluginStateDraft } from "./plugin-manager-controls";
import { PluginManagerHeader } from "./plugin-manager-header";
import { emptyPermissionPreviewDraft, type PluginPermissionDiffPreviewDraft } from "./plugin-permission-diff-preview";

type PluginUpdateDraft = {
  busy: boolean;
  error: string;
  result: string;
};

export function PluginsView({
  api,
  data,
  onReload,
  onSelectPlugin,
  activeTab: controlledActiveTab,
  onActiveTabChange,
}: {
  api: ApiContext;
  data: AppData;
  onReload?: () => Promise<void>;
  onSelectPlugin?: (pluginID: string, section?: PluginDetailSection) => void;
  activeTab?: PluginManagerTabKey;
  onActiveTabChange?: (tab: PluginManagerTabKey) => void;
}) {
  const plugins = data.plugins;
  const [pluginStateDrafts, setPluginStateDrafts] = useState<Record<string, PluginStateDraft>>({});
  const [pluginUpdateDrafts, setPluginUpdateDrafts] = useState<Record<string, PluginUpdateDraft>>({});
  const [pluginDeleteDrafts, setPluginDeleteDrafts] = useState<Record<string, PluginDeleteDraft>>({});
  const [pluginRollbackDrafts, setPluginRollbackDrafts] = useState<Record<string, PluginRollbackDraft>>({});
  const [installDraft, setInstallDraft] = useState<PluginInstallDraft>(emptyInstallDraft());
  const installPreviewKey = JSON.stringify([installDraft.source, installDraft.downloadURL, installDraft.checksumSHA256]);
  const [installPreviewResult, setInstallPreviewResult] = useState<PluginPermissionDiffPreviewDraft & { key: string }>({ ...emptyPermissionPreviewDraft(), key: "" });
  const installPermissionPreview = installPreviewResult.key === installPreviewKey ? installPreviewResult : emptyPermissionPreviewDraft();
  const installPreviewRequest = useRef(0);
  const [localActiveTab, setLocalActiveTab] = useState<PluginManagerTabKey>("installed");
  const activeTab = controlledActiveTab ?? localActiveTab;
  const [activeExtensionCategory, setActiveExtensionCategory] = useState<"all" | PluginExtensionCategoryKey>("all");
  const [statusFilter, setStatusFilter] = useState<PluginStatusFilterKey>("all");
  const [pluginQuery, setPluginQuery] = useState("");
  const pluginsWithSettings = useMemo(
    () => new Set(plugins.filter((plugin) => plugin.has_settings).map((plugin) => plugin.id)),
    [plugins],
  );
  const locale = languageLocale();
  const marketplaceWebsiteURL = pluginMarketplaceWebsiteURL(data);
  const marketplaceByPluginID = useMemo(() => new Map(data.pluginMarketplace.map((entry) => [entry.plugin.id, entry])), [data.pluginMarketplace]);
  // The console never polls the plugin list, so an enable or disable that has been
  // accepted by the server is only visible through its draft until the reload lands.
  const effectivePlugins = useMemo(
    () => plugins.map((plugin) => pluginWithLifecycleDraft(plugin, pluginStateDrafts[plugin.id] ?? {})),
    [plugins, pluginStateDrafts],
  );
  const installedPlugins = useMemo(
    () => effectivePlugins.filter((plugin) => pluginManagerDisplayState({ plugin }).installed),
    [effectivePlugins],
  );
  const availablePlugins = useMemo(
    () => {
      const available = new Map(effectivePlugins
        .filter((plugin) => !pluginManagerDisplayState({ plugin }).installed)
        .map((plugin) => [plugin.id, plugin]));
      for (const entry of data.pluginMarketplace) {
        if (!entry.installed && !available.has(entry.plugin.id)) {
          available.set(entry.plugin.id, { ...entry.plugin, installed: false, available: true });
        }
      }
      return Array.from(available.values()).sort((left, right) => localizedPluginName(left, locale).localeCompare(localizedPluginName(right, locale), locale));
    },
    [data.pluginMarketplace, effectivePlugins, locale],
  );
  const categoryCounts = useMemo(() => ({
    providerPlugins: installedPlugins.filter((plugin) => pluginExtensionCategory(plugin) === "provider").length,
    chainInjectionPlugins: installedPlugins.filter((plugin) => pluginExtensionCategory(plugin) === "chain").length,
    uiTemplatePlugins: installedPlugins.filter((plugin) => pluginExtensionCategory(plugin) === "ui").length,
    backgroundJobPlugins: installedPlugins.filter((plugin) => pluginExtensionCategory(plugin) === "jobs").length,
  }), [installedPlugins]);
  const pluginCounts = useMemo(() => ({
    all: installedPlugins.length,
    enabled: installedPlugins.filter((plugin) => pluginManagerDisplayState({ plugin }).enabled).length,
    disabled: installedPlugins.filter((plugin) => !pluginManagerDisplayState({ plugin }).enabled).length,
    setup: installedPlugins.filter((plugin) => pluginManagerDisplayState({ plugin }).setupRequired).length,
    updates: installedPlugins.filter((plugin) => pluginManagerDisplayState({ plugin, marketplace: marketplaceByPluginID.get(plugin.id) }).actions.update.available).length,
  }), [installedPlugins, marketplaceByPluginID]);
  const filteredPlugins = useMemo(() => {
    const normalizedQuery = pluginQuery.trim().toLocaleLowerCase(locale);
    return installedPlugins.filter((plugin) => {
      const lifecycle = pluginManagerDisplayState({ plugin, marketplace: marketplaceByPluginID.get(plugin.id) });
      const matchesStatus = statusFilter === "all"
        || (statusFilter === "enabled" && lifecycle.enabled)
        || (statusFilter === "disabled" && !lifecycle.enabled)
        || (statusFilter === "setup" && lifecycle.setupRequired)
        || (statusFilter === "updates" && lifecycle.actions.update.available);
      const matchesCategory = activeExtensionCategory === "all" || pluginExtensionCategory(plugin) === activeExtensionCategory;
      if (!matchesStatus || !matchesCategory || !normalizedQuery) return matchesStatus && matchesCategory;
      const searchable = [
        localizedPluginName(plugin, locale),
        plugin.id,
        plugin.version,
        ...plugin.kinds,
        ...plugin.capabilities.map((capability) => `${capability.kind} ${capability.name}`),
      ].join(" ").toLocaleLowerCase(locale);
      return searchable.includes(normalizedQuery);
    });
  }, [activeExtensionCategory, installedPlugins, locale, marketplaceByPluginID, pluginQuery, statusFilter]);
  const installedPagination = usePagination(filteredPlugins.length, `${activeExtensionCategory}:${statusFilter}:${pluginQuery}`);
  const paginatedPlugins = useMemo(
    () => filteredPlugins.slice(installedPagination.startIndex, installedPagination.endIndex),
    [filteredPlugins, installedPagination.endIndex, installedPagination.startIndex],
  );
  const hiddenUpdateResults = Object.entries(pluginUpdateDrafts).filter(([pluginID, draft]) =>
    (draft.error || draft.result) && !paginatedPlugins.some((plugin) => plugin.id === pluginID),
  );
  const availablePagination = usePagination(availablePlugins.length, activeTab);
  const paginatedAvailablePlugins = useMemo(
    () => availablePlugins.slice(availablePagination.startIndex, availablePagination.endIndex),
    [availablePagination.endIndex, availablePagination.startIndex, availablePlugins],
  );
  useEffect(() => {
    setInstallPreviewResult({ ...emptyPermissionPreviewDraft(), key: installPreviewKey });
    return () => { installPreviewRequest.current += 1; };
  }, [installPreviewKey]);
  // A draft only covers the gap between a state change and the reloaded list. Once the
  // server reports the status the draft was holding, the descriptor owns both the status
  // and the restart flag again. A draft that still disagrees is kept, so a failed reload
  // cannot revert the row.
  useEffect(() => {
    setPluginStateDrafts((drafts) => {
      const settled = Object.keys(drafts).filter((pluginID) => {
        const status = drafts[pluginID].status;
        const plugin = plugins.find((item) => item.id === pluginID);
        return Boolean(status) && plugin !== undefined && pluginManagerLifecycleState(plugin).rawStatus === status;
      });
      if (settled.length === 0) return drafts;
      const next = { ...drafts };
      for (const pluginID of settled) next[pluginID] = { ...next[pluginID], status: undefined, restartRequired: false };
      return next;
    });
  }, [plugins]);
  const pluginStateDraft = (plugin: PluginDescriptor) => pluginStateDrafts[plugin.id] ?? {};
  const pluginUpdateDraft = (plugin: PluginDescriptor) => pluginUpdateDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginDeleteDraft = (plugin: PluginDescriptor) => pluginDeleteDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginRollbackDraft = (plugin: PluginDescriptor) => pluginRollbackDrafts[plugin.id] ?? { busy: false, error: "", result: "" };

  function selectManagerTab(tab: PluginManagerTabKey) {
    if (tab === "installed") setActiveExtensionCategory("all");
    setLocalActiveTab(tab);
    onActiveTabChange?.(tab);
  }

  async function reloadPlugins() {
    try {
      await onReload?.();
    } catch {
      // The console reports load failures; an accepted mutation still stands.
    }
  }

  async function updatePluginState(plugin: PluginDescriptor, status: string) {
    const current = pluginStateDraft(plugin);
    // The row shows the requested status while the request is in flight; the catch below
    // restores the draft the click started from, so a rejected request rolls it back.
    setPluginStateDrafts((drafts) => ({
      ...drafts,
      [plugin.id]: { ...current, status, busy: true, error: "", restartRequired: false },
    }));
    try {
      const response = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(plugin.id)}/state`, {
        method: "PATCH",
        body: JSON.stringify({ status }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("更新插件状态")));
      const payload = await response.json() as { data?: { status?: string; restart_required?: boolean } };
      setPluginStateDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: { status: payload.data?.status ?? status, busy: true, error: "", restartRequired: Boolean(payload.data?.restart_required) },
      }));
      // The row stays busy until the reloaded list arrives, so a second click cannot race
      // the refetch. A reload that fails leaves the draft in place rather than reverting.
      await reloadPlugins();
      setPluginStateDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: { ...(drafts[plugin.id] ?? {}), busy: false },
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setPluginStateDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: { ...current, busy: false, error: reason instanceof Error ? reason.message : tx("更新插件状态失败") },
      }));
    }
  }

  async function installPlugin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setInstallDraft((draft) => ({ ...draft, busy: true, error: "", result: "" }));
    try {
      const response = await adminFetch(api, "/api/admin/plugins/install", {
        method: "POST",
        body: pluginInstallRequestBody(installDraft),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("安装插件")));
      const payload = await response.json() as { data?: { plugin?: { id?: string }; restart_required?: boolean } };
      await reloadPlugins();
      const pluginID = payload.data?.plugin?.id ?? tx("插件");
      setInstallDraft((draft) => ({
        ...draft,
        busy: false,
        error: "",
        result: payload.data?.restart_required
          ? formatTranslationTemplate(tx("{plugin} 安装完成，重启后生效"), { plugin: pluginID })
          : formatTranslationTemplate(tx("{plugin} 安装完成"), { plugin: pluginID }),
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setInstallDraft((draft) => ({
        ...draft,
        busy: false,
        error: reason instanceof Error ? reason.message : tx("安装插件失败"),
        result: "",
      }));
    }
  }

  async function previewInstallPluginPermissions() {
    const request = ++installPreviewRequest.current;
    const setPreview = (draft: PluginPermissionDiffPreviewDraft) => {
      if (request === installPreviewRequest.current) setInstallPreviewResult({ ...draft, key: installPreviewKey });
    };
    setPreview({ busy: true, error: "", preview: null });
    try {
      const response = await adminFetch(api, "/api/admin/plugins/permission-diff", {
        method: "POST",
        body: JSON.stringify({
          download_url: installDraft.downloadURL,
          checksum_sha256: installDraft.checksumSHA256,
        }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("预览权限")));
      const payload = await response.json() as { data?: PluginPermissionDiffPreviewPayload };
      setPreview({ busy: false, error: "", preview: payload.data ?? null });
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setPreview({
        busy: false,
        error: reason instanceof Error ? reason.message : tx("权限预览失败"),
        preview: null,
      });
    }
  }

  async function updatePlugin(plugin: PluginDescriptor) {
    setPluginUpdateDrafts((drafts) => ({
      ...drafts,
      [plugin.id]: { ...(drafts[plugin.id] ?? { busy: false, error: "", result: "" }), busy: true, error: "", result: "" },
    }));
    try {
      const distribution = marketplaceByPluginID.get(plugin.id)?.plugin.distribution ?? plugin.distribution;
      const response = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(plugin.id)}/update`, {
        method: "POST",
        body: distribution?.download_url && distribution.checksum_sha256
          ? JSON.stringify({
            download_url: distribution.download_url,
            checksum_sha256: distribution.checksum_sha256,
            ...(distribution.signature_url ? { signature_url: distribution.signature_url } : {}),
            ...(distribution.signature_key_id ? { signature_key_id: distribution.signature_key_id } : {}),
          })
          : undefined,
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("更新插件包")));
      const payload = await response.json() as { data?: { plugin?: { version?: string; name?: string }; restart_required?: boolean } };
      await reloadPlugins();
      const label = payload.data?.plugin?.version ?? tx("插件");
      setPluginUpdateDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          busy: false,
          error: "",
          result: payload.data?.restart_required
            ? formatTranslationTemplate(tx("插件已更新至 {version}，重启后生效"), { version: label })
            : formatTranslationTemplate(tx("插件已更新至 {version}"), { version: label }),
        },
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setPluginUpdateDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          busy: false,
          error: reason instanceof Error ? reason.message : tx("更新插件失败"),
          result: "",
        },
      }));
    }
  }

  async function deletePlugin(plugin: PluginDescriptor) {
    const current = pluginDeleteDraft(plugin);
    setPluginDeleteDrafts((drafts) => ({
      ...drafts,
      [plugin.id]: { ...current, busy: true, error: "", result: "" },
    }));
    try {
      const response = await adminFetch(api, `/api/admin/plugin-packages/${encodeURIComponent(plugin.id)}`, {
        method: "DELETE",
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("卸载插件")));
      const payload = await response.json() as { data?: { plugin_id?: string; restart_required?: boolean } };
      await reloadPlugins();
      const pluginID = payload.data?.plugin_id ?? plugin.id;
      setPluginDeleteDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          busy: false,
          error: "",
          result: payload.data?.restart_required
            ? formatTranslationTemplate(tx("插件 {plugin} 已卸载，重启后生效"), { plugin: pluginID })
            : formatTranslationTemplate(tx("插件 {plugin} 已卸载"), { plugin: pluginID }),
        },
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setPluginDeleteDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          ...current,
          busy: false,
          error: reason instanceof Error ? reason.message : tx("卸载插件失败"),
          result: "",
        },
      }));
    }
  }

  async function rollbackPlugin(plugin: PluginDescriptor) {
    const current = pluginRollbackDraft(plugin);
    setPluginRollbackDrafts((drafts) => ({
      ...drafts,
      [plugin.id]: { ...current, busy: true, error: "", result: "" },
    }));
    try {
      const response = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(plugin.id)}/rollback`, {
        method: "POST",
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("回滚插件")));
      const payload = await response.json() as { data?: { plugin?: { version?: string }; rollback_version?: string; restart_required?: boolean } };
      await reloadPlugins();
      const rollbackVersion = payload.data?.rollback_version ?? payload.data?.plugin?.version ?? plugin.version ?? plugin.id;
      setPluginRollbackDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          busy: false,
          error: "",
          result: payload.data?.restart_required
            ? formatTranslationTemplate(tx("插件已回滚至 {version}，重启后生效"), { version: rollbackVersion })
            : formatTranslationTemplate(tx("插件已回滚至 {version}"), { version: rollbackVersion }),
        },
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setPluginRollbackDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          ...current,
          busy: false,
          error: reason instanceof Error ? reason.message : tx("回滚插件失败"),
          result: "",
        },
      }));
    }
  }

  return (
    <div className="plugins-view">
      <PluginManagerHeader activeTab={activeTab} marketplaceWebsiteURL={marketplaceWebsiteURL} onTabChange={selectManagerTab} />

      {activeTab === "installed" ? (
        <div className="plugin-extension-workspace">
          <aside className="plugin-extension-nav" aria-label={tx("已安装插件")} role="tablist">
            <div className="plugin-extension-nav-heading">
              <strong>{tx("已安装插件")}</strong>
              <span>{installedPlugins.length}</span>
            </div>
            <button
              aria-label={tx("全部插件")}
              aria-selected={activeExtensionCategory === "all"}
              className={activeExtensionCategory === "all" ? "active" : ""}
              onClick={() => setActiveExtensionCategory("all")}
              role="tab"
              type="button"
            >
              <PackageOpen size={16} aria-hidden="true" />
              <span>{tx("全部插件")}</span>
              <strong>{installedPlugins.length}</strong>
            </button>
            {pluginExtensionCategories.map((category) => (
              <button
                aria-label={tx(category.label)}
                aria-selected={activeExtensionCategory === category.key}
                className={activeExtensionCategory === category.key ? "active" : ""}
                key={category.key}
                onClick={() => setActiveExtensionCategory(category.key)}
                role="tab"
                type="button"
              >
                {extensionCategoryIcon(category.key)}
                <span>{tx(category.label)}</span>
                <strong>{extensionCategoryCount(category.key, categoryCounts)}</strong>
              </button>
            ))}
          </aside>
          <div className="plugin-extension-content">
      {(
      <section className="section plugin-installed-section" data-plugin-manager-section="registry">
        <div className="section-header plugin-installed-header">
          <div>
            <h2>{tx("已安装插件")}</h2>
            <span>{installedPlugins.length}</span>
          </div>
          <label className="plugin-search-field">
            <Search size={15} aria-hidden="true" />
            <input
              aria-label={tx("搜索插件")}
              onChange={(event) => setPluginQuery(event.currentTarget.value)}
              placeholder={tx("搜索插件名称或 ID")}
              type="search"
              value={pluginQuery}
            />
          </label>
        </div>
        <div className="plugin-status-filters" role="group" aria-label={tx("插件状态筛选")}>
          {pluginStatusFilters.map((filter) => (
            <button
              aria-label={tx(filter.label)}
              aria-pressed={statusFilter === filter.key}
              className={statusFilter === filter.key ? "active" : ""}
              key={filter.key}
              onClick={() => setStatusFilter(filter.key)}
              type="button"
            >
              <span>{tx(filter.label)}</span>
              <strong>{pluginCounts[filter.key]}</strong>
            </button>
          ))}
        </div>
        <div className="section-body">
          {hiddenUpdateResults.map(([pluginID, draft]) => (
            <div className="stacked-cell plugin-update-notice" key={pluginID}>
              <strong>{localizedPluginName(effectivePlugins.find((plugin) => plugin.id === pluginID), locale) || pluginID}</strong>
              {draft.error ? <span className="provider-quota-error" role="alert">{draft.error}</span> : null}
              {draft.result ? <span role="status">{draft.result}</span> : null}
            </div>
          ))}
          {filteredPlugins.length === 0 ? (
            <div className="plugin-installed-empty">
              <span className="plugin-installed-empty-icon" aria-hidden="true">
                <PackageOpen size={20} />
              </span>
              <span>{tx("暂无插件")}</span>
            </div>
          ) : (
            <>
              <div className="plugin-installed-list">
                  {paginatedPlugins.map((plugin) => {
                    const lifecycle = pluginManagerDisplayState({ plugin, marketplace: marketplaceByPluginID.get(plugin.id) });
                    return (
                      <article className={`plugin-installed-row${!lifecycle.enabled ? " disabled" : ""}`} key={plugin.id}>
                        <div className="plugin-installed-main">
                          <InstalledPluginTitle plugin={plugin} onSelect={onSelectPlugin} />
                        </div>
                        <div className="plugin-installed-state">
                          <PluginLifecycleControl
                            allowBuiltInUpdates
                            draft={pluginStateDraft(plugin)}
                            lifecycle={lifecycle}
                            onRollback={rollbackPlugin}
                            onUpdate={updatePluginState}
                            plugin={plugin}
                            rollbackDraft={pluginRollbackDraft(plugin)}
                          />
                          {lifecycle.setupRequired ? <StatusPill status="warning" label={tx("待配置")} /> : null}
                          {lifecycle.inUse ? <StatusPill status="active" label={tx("使用中")} /> : null}
                        </div>
                        <div className="plugin-installed-actions">
                          {onSelectPlugin ? (
                            <>
                              <button className="secondary-button compact-button" onClick={() => onSelectPlugin(plugin.id)} type="button">
                                <PackageOpen size={14} aria-hidden="true" />
                                <span>{tx("详情")}</span>
                              </button>
                              {plugin.has_settings || pluginsWithSettings.has(plugin.id) ? (
                                <button className="secondary-button compact-button" onClick={() => onSelectPlugin(plugin.id, "settings")} type="button">
                                  <Settings2 size={14} aria-hidden="true" />
                                  <span>{tx("设置")}</span>
                                </button>
                              ) : null}
                            </>
                          ) : null}
                          {lifecycle.actions.update.available || pluginUpdateDraft(plugin).error || pluginUpdateDraft(plugin).result ? (
                            <div className="stacked-cell" data-plugin-manager-control="update">
                              {lifecycle.actions.update.available ? <button className="secondary-button compact-button" disabled={pluginUpdateDraft(plugin).busy} onClick={() => updatePlugin(plugin)} type="button">
                                <Download size={14} aria-hidden="true" />
                                <span>{tx(pluginUpdateDraft(plugin).busy ? "更新中" : "更新")}</span>
                              </button> : null}
                              {pluginUpdateDraft(plugin).error ? <span className="provider-quota-error" role="alert">{pluginUpdateDraft(plugin).error}</span> : null}
                              {pluginUpdateDraft(plugin).result ? <span role="status">{pluginUpdateDraft(plugin).result}</span> : null}
                            </div>
                          ) : null}
                          {lifecycle.actions.uninstall.available ? (
                            <PluginDeleteControl
                              lifecycle={lifecycle}
                              plugin={plugin}
                              draft={pluginDeleteDraft(plugin)}
                              onDelete={deletePlugin}
                            />
                          ) : null}
                        </div>
                      </article>
                    );
                  })}
              </div>
              <PaginationControls pagination={installedPagination} totalItems={filteredPlugins.length} />
            </>
          )}
        </div>
      </section>
      )}

          </div>
        </div>
      ) : null}

      {activeTab === "install" ? (
        <div className="plugin-browse-workspace">
        <section className="section plugin-installed-section" data-plugin-manager-section="browse">
          <div className="section-header">
            <div>
              <h2>{tx("浏览插件")}</h2>
              <span>{availablePlugins.length}</span>
            </div>
            {marketplaceWebsiteURL ? <a className="secondary-button plugin-marketplace-link" href={marketplaceWebsiteURL} rel="noreferrer" target="_blank">
              <ExternalLink size={14} aria-hidden="true" />
              <span>{tx("浏览插件市场")}</span>
            </a> : null}
          </div>
          <div className="section-body">
            {availablePlugins.length === 0 ? (
              <div className="plugin-installed-empty">
                <span className="plugin-installed-empty-icon" aria-hidden="true"><PackageOpen size={20} /></span>
                <span>{tx("暂无可安装插件")}</span>
              </div>
            ) : (
              <>
                <div className="plugin-installed-list">
                  {paginatedAvailablePlugins.map((plugin) => (
                    <article className="plugin-installed-row plugin-browse-row" key={plugin.id}>
                      <div className="plugin-installed-main"><InstalledPluginTitle plugin={plugin} onSelect={onSelectPlugin} /></div>
                      <div className="plugin-installed-state"><StatusPill status="unknown" label={tx("未安装")} /></div>
                      <div className="plugin-installed-actions">
                        {onSelectPlugin ? (
                          <button className="secondary-button compact-button" onClick={() => onSelectPlugin(plugin.id)} type="button">
                            <PackageOpen size={14} aria-hidden="true" /><span>{tx("详情")}</span>
                          </button>
                        ) : null}
                        {plugin.source === "built_in" || pluginManagerDistributionReady(plugin) ? (
                          <button
                            className="primary-button compact-button"
                            disabled={installDraft.busy}
                            onClick={() => plugin.source === "built_in"
                              ? updatePluginState(plugin, "enabled")
                              : setInstallDraft((draft) => ({ ...draft, source: "url", packageFile: null, error: "", result: "", downloadURL: plugin.distribution?.download_url ?? "", checksumSHA256: plugin.distribution?.checksum_sha256 ?? "" }))}
                            type="button"
                          >
                            <Download size={14} aria-hidden="true" /><span>{tx(plugin.source === "built_in" ? "安装" : "准备安装")}</span>
                          </button>
                        ) : null}
                      </div>
                    </article>
                  ))}
                </div>
                <PaginationControls pagination={availablePagination} totalItems={availablePlugins.length} />
              </>
            )}
          </div>
        </section>
        <section className="section plugin-install-center" data-plugin-manager-section="install">
          <div className="section-header">
            <div><h2>{tx("手动安装")}</h2><span>{tx("URL 或 ZIP 插件包")}</span></div>
          </div>
          <div className="section-body">
            <PluginInstallFields
              draft={installDraft}
              onInstall={installPlugin}
              onPermissionPreview={previewInstallPluginPermissions}
              permissionPreviewDraft={installPermissionPreview}
              setDraft={setInstallDraft}
            />
          </div>
        </section>
        </div>
      ) : null}
    </div>
  );
}

function extensionCategoryIcon(category: PluginExtensionCategoryKey) {
  if (category === "provider") return <Boxes size={16} aria-hidden="true" />;
  if (category === "chain") return <Layers3 size={16} aria-hidden="true" />;
  if (category === "ui") return <ShieldCheck size={16} aria-hidden="true" />;
  return <Clock3 size={16} aria-hidden="true" />;
}

function pluginExtensionCategory(plugin: PluginDescriptor): PluginExtensionCategoryKey {
  const categories = new Set(plugin.marketplace?.categories?.map((category) => category.trim().toLowerCase()) ?? []);
  if (categories.has("provider") || categories.has("provider_integration")) return "provider";
  if (categories.has("request_pipeline") || categories.has("gateway_chain")) return "chain";
  if (categories.has("ui_template") || categories.has("admin_ui") || categories.has("sim")) return "ui";
  if (plugin.category === "provider_integration" || plugin.kinds.includes("provider")) return "provider";
  if (plugin.category === "request_pipeline" || plugin.placements.includes("gateway_chain")) return "chain";
  if (plugin.category === "ui_template" || plugin.kinds.includes("sim") || plugin.kinds.includes("admin_ui") || plugin.placements.includes("presentation")) return "ui";
  return "jobs";
}

function pluginExtensionCategoryLabel(category: PluginExtensionCategoryKey) {
  return pluginExtensionCategories.find((item) => item.key === category)?.label ?? "自动化";
}

function pluginListSummary(plugin: PluginDescriptor) {
  const declared = plugin.summary?.trim() || plugin.description?.trim();
  if (declared && declared !== plugin.name && declared !== plugin.id) return declared;
  const category = pluginExtensionCategory(plugin);
  if (category === "provider") return tx("连接并管理此模型服务。配置在 Provider 页面完成。");
  if (category === "chain") return tx("在请求处理流程中执行此插件提供的操作。");
  if (category === "ui") return tx("为 TokenHub 管理后台提供界面和布局能力。");
  return tx("在后台执行此插件提供的自动化任务。");
}

function extensionCategoryCount(
  category: PluginExtensionCategoryKey,
  counts: { providerPlugins: number; chainInjectionPlugins: number; uiTemplatePlugins: number; backgroundJobPlugins: number },
) {
  if (category === "provider") return counts.providerPlugins;
  if (category === "chain") return counts.chainInjectionPlugins;
  if (category === "ui") return counts.uiTemplatePlugins;
  return counts.backgroundJobPlugins;
}

function InstalledPluginTitle({ plugin, onSelect }: { plugin: PluginDescriptor; onSelect?: (pluginID: string) => void }) {
  const name = plugin.name || plugin.id;
  const version = plugin.version && plugin.version !== "built-in" ? plugin.version : "";
  const category = pluginExtensionCategoryLabel(pluginExtensionCategory(plugin));
  const summary = pluginListSummary(plugin);
  return (
    <div className="plugin-title-cell plugin-installed-title-cell">
      <div className="plugin-installed-title-line" title={plugin.id}>
        {onSelect ? (
          <button className="plugin-title-link" type="button" onClick={() => onSelect(plugin.id)} aria-label={formatTranslationTemplate(tx("查看插件 {name} 的详情"), { name })}>
            {name}
          </button>
        ) : (
          <strong className="plugin-title-name">{name}</strong>
        )}
        <div className="plugin-installed-meta">
        <span className="plugin-installed-label">{tx(category)}</span>
        <span className="plugin-installed-label source">{pluginSourceLabel(plugin.source)}</span>
        {version ? <span className="plugin-installed-label version">{version}</span> : null}
        </div>
      </div>
      <p className="plugin-installed-summary">{summary}</p>
    </div>
  );
}

function pluginSourceLabel(source: string) {
  if (source === "built_in") return tx("内置");
  if (source === "marketplace") return tx("插件市场");
  if (source === "local_file") return tx("本地文件");
  return source;
}
