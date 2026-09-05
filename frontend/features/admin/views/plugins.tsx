import { Boxes, Clock3, Download, ExternalLink, GitBranch, Layers3, PackageOpen, Save, Search, Settings2, ShieldCheck } from "lucide-react";
import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from "react";
import { type ApiContext, type AppData, type GatewayHookDescriptor, type PluginBackgroundJobDescriptor, type PluginDescriptor } from "../core/types";
import { pluginActionKey, pluginBackgroundJobKey } from "../domain/plugin-actions";
import {
  pluginExtensionCategories,
  pluginMarketplaceWebsiteURL,
  pluginStatusFilters,
  type PluginExtensionCategoryKey,
  type PluginManagerTabKey,
  type PluginStatusFilterKey,
} from "../domain/plugin-management";
import { pluginManagerDisplayState, pluginManagerLifecycleState, type PluginManagerDisplayState } from "../domain/plugin-manager";
import { localizedCapabilityTitle, localizedContributionTitle, localizedPluginName } from "../domain/plugin-localization";
import { type PluginDetailSection } from "../domain/plugin-detail-route";
import { type PluginPermissionDiffPreviewPayload } from "../domain/plugin-permission-diff";
import { simRegistryFromPlugins, type SIMRegistry, type SIMShellLayout, type SIMThemeTokens } from "../domain/sim-registry";
import { resolveSIMSelection, type SIMSelectionPreference } from "../domain/sim-selection";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { StatusPill } from "../shared/ui";
import { emptyInstallDraft, PluginInstallFields, pluginInstallRequestBody, type PluginInstallDraft } from "./plugin-install-form";
import { PluginDeleteControl, PluginLifecycleControl, pluginWithLifecycleDraft, type PluginDeleteDraft, type PluginRollbackDraft, type PluginStateDraft } from "./plugin-manager-controls";
import { PluginManagerHeader } from "./plugin-manager-header";
import { emptyPermissionPreviewDraft, PluginPermissionDiffPreview, type PluginPermissionDiffPreviewDraft } from "./plugin-permission-diff-preview";

type PluginUpdateDraft = {
  busy: boolean;
  error: string;
  result: string;
};

export function PluginsView({
  api,
  data,
  simSelectionPreference,
  onSIMSelectionPreferenceChange,
  onReload,
  onSelectPlugin,
  activeTab: controlledActiveTab,
  onActiveTabChange,
  theme = "light",
}: {
  api: ApiContext;
  data: AppData;
  simSelectionPreference?: unknown;
  onSIMSelectionPreferenceChange?: (preference: SIMSelectionPreference) => void;
  onReload?: () => Promise<void>;
  onSelectPlugin?: (pluginID: string, section?: PluginDetailSection) => void;
  activeTab?: PluginManagerTabKey;
  onActiveTabChange?: (tab: PluginManagerTabKey) => void;
  theme?: "light" | "dark";
}) {
  const plugins = data.plugins;
  const [pluginStateDrafts, setPluginStateDrafts] = useState<Record<string, PluginStateDraft>>({});
  const [pluginUpdateDrafts, setPluginUpdateDrafts] = useState<Record<string, PluginUpdateDraft>>({});
  const [pluginDeleteDrafts, setPluginDeleteDrafts] = useState<Record<string, PluginDeleteDraft>>({});
  const [pluginRollbackDrafts, setPluginRollbackDrafts] = useState<Record<string, PluginRollbackDraft>>({});
  const [installPermissionPreview, setInstallPermissionPreview] = useState<PluginPermissionDiffPreviewDraft>(emptyPermissionPreviewDraft());
  const [pluginPermissionPreviews, setPluginPermissionPreviews] = useState<Record<string, PluginPermissionDiffPreviewDraft>>({});
  const [installDraft, setInstallDraft] = useState<PluginInstallDraft>(emptyInstallDraft());
  const [localActiveTab, setLocalActiveTab] = useState<PluginManagerTabKey>("installed");
  const activeTab = controlledActiveTab ?? localActiveTab;
  const [activeExtensionCategory, setActiveExtensionCategory] = useState<"all" | PluginExtensionCategoryKey>("all");
  const [statusFilter, setStatusFilter] = useState<PluginStatusFilterKey>("all");
  const [pluginQuery, setPluginQuery] = useState("");
  const simRegistry = useMemo(() => simRegistryFromPlugins(plugins), [plugins]);
  const pluginsWithSettings = useMemo(() => new Set(simRegistry.themeTokens.map((theme) => theme.pluginID)), [simRegistry.themeTokens]);
  const simSelection = useMemo(
    () => resolveSIMSelection({ plugins, preference: simSelectionPreference, themeMode: theme }),
    [plugins, simSelectionPreference, theme],
  );
  const uiContributions = data.pluginUI;
  const pluginActions = data.pluginActions;
  const backgroundJobs = data.pluginBackgroundJobs;
  const locale = languageLocale();
  const themeContributions = uiContributions.filter((contribution) => contribution.slot === "theme.tokens");
  const layoutContributions = uiContributions.filter((contribution) => contribution.slot === "layout.preset");
  const simPlugins = useMemo(() => simSelectionPlugins(plugins, simRegistry, locale), [plugins, simRegistry, locale]);
  const themeOptions = useMemo(() => simSelectionThemeOptions(simRegistry.themeTokens, plugins, theme, locale), [simRegistry.themeTokens, plugins, theme, locale]);
  const layoutOptions = useMemo(() => simSelectionLayoutOptions(simRegistry.shellLayouts, plugins, locale), [simRegistry.shellLayouts, plugins, locale]);
  const simTemplates = useMemo(() => simTemplateOptions(simPlugins, themeOptions, layoutOptions), [simPlugins, themeOptions, layoutOptions]);
  const backgroundRuns = useMemo(() => new Map(data.pluginBackgroundRuns.map((run) => [pluginBackgroundJobKey(run.plugin_id, run.job_id), run])), [data.pluginBackgroundRuns]);
  const backgroundJobPluginList = useMemo(() => backgroundJobPluginSummaries(plugins, backgroundJobs, backgroundRuns, locale), [plugins, backgroundJobs, backgroundRuns, locale]);
  const pluginActionKeys = new Set(pluginActions.map((action) => pluginActionKey(action.plugin_id, action.action_id)));
  const marketplaceWebsiteURL = pluginMarketplaceWebsiteURL(data);
  const providerPluginList = plugins.filter((plugin) => plugin.kinds?.includes("provider"));
  const providerPlugins = providerPluginList.length;
  const chainPluginList = useMemo(() => chainPluginSummaries(plugins, data.pluginChain.hooks, locale), [plugins, data.pluginChain.hooks, locale]);
  const chainInjectionPlugins = chainPluginList.length;
  const uiTemplatePlugins = plugins.filter((plugin) => plugin.kinds?.includes("sim")).length;
  const backgroundJobPlugins = backgroundJobPluginList.length;
  // The console never polls the plugin list, so an enable or disable that has been
  // accepted by the server is only visible through its draft until the reload lands.
  const effectivePlugins = useMemo(
    () => plugins.map((plugin) => pluginWithLifecycleDraft(plugin, pluginStateDrafts[plugin.id] ?? {})),
    [plugins, pluginStateDrafts],
  );
  const pluginCounts = useMemo(() => ({
    all: effectivePlugins.length,
    enabled: effectivePlugins.filter((plugin) => pluginManagerDisplayState({ plugin }).status !== "disabled").length,
    disabled: effectivePlugins.filter((plugin) => pluginManagerDisplayState({ plugin }).status === "disabled").length,
    updates: effectivePlugins.filter((plugin) => pluginManagerDisplayState({ plugin }).actions.update.available).length,
  }), [effectivePlugins]);
  const filteredPlugins = useMemo(() => {
    const normalizedQuery = pluginQuery.trim().toLocaleLowerCase(locale);
    return effectivePlugins.filter((plugin) => {
      const lifecycle = pluginManagerDisplayState({ plugin });
      const matchesStatus = statusFilter === "all"
        || (statusFilter === "enabled" && lifecycle.status !== "disabled")
        || (statusFilter === "disabled" && lifecycle.status === "disabled")
        || (statusFilter === "updates" && lifecycle.actions.update.available);
      if (!matchesStatus || !normalizedQuery) return matchesStatus;
      const searchable = [
        localizedPluginName(plugin, locale),
        plugin.id,
        plugin.version,
        ...plugin.kinds,
        ...plugin.capabilities.map((capability) => `${capability.kind} ${capability.name}`),
      ].join(" ").toLocaleLowerCase(locale);
      return searchable.includes(normalizedQuery);
    });
  }, [effectivePlugins, locale, pluginQuery, statusFilter]);
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
  const activeSIMPlugin = simPlugins.find((plugin) => plugin.id === simSelection.activeSIMPluginID);
  const pluginStateDraft = (plugin: PluginDescriptor) => pluginStateDrafts[plugin.id] ?? {};
  const pluginUpdateDraft = (plugin: PluginDescriptor) => pluginUpdateDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginDeleteDraft = (plugin: PluginDescriptor) => pluginDeleteDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginRollbackDraft = (plugin: PluginDescriptor) => pluginRollbackDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginPermissionPreviewDraft = (plugin: PluginDescriptor) => pluginPermissionPreviews[plugin.id] ?? emptyPermissionPreviewDraft();

  function selectManagerTab(tab: PluginManagerTabKey) {
    if (tab === "installed") setActiveExtensionCategory("all");
    setLocalActiveTab(tab);
    onActiveTabChange?.(tab);
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
      try {
        await onReload?.();
      } catch {
        // The console reports its own load failures; the accepted status still stands.
      }
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
      const pluginID = payload.data?.plugin?.id ?? tx("插件");
      setInstallDraft((draft) => ({
        ...draft,
        busy: false,
        error: "",
        result: `${pluginID} · ${payload.data?.restart_required ? tx("插件安装完成，重启后生效") : tx("插件安装完成")}`,
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
    setInstallPermissionPreview({ busy: true, error: "", preview: null });
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
      setInstallPermissionPreview({ busy: false, error: "", preview: payload.data ?? null });
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setInstallPermissionPreview({
        busy: false,
        error: reason instanceof Error ? reason.message : tx("权限预览失败"),
        preview: null,
      });
    }
  }

  async function previewPluginPermissions(plugin: PluginDescriptor, operation: "install" | "update") {
    const current = pluginPermissionPreviewDraft(plugin);
    setPluginPermissionPreviews((drafts) => ({
      ...drafts,
      [plugin.id]: { ...current, busy: true, error: "", preview: null },
    }));
    try {
      const distribution = plugin.distribution;
      if (!distribution?.download_url || !distribution.checksum_sha256) {
        throw new Error(tx("无下载来源"));
      }
      const path = operation === "update"
        ? `/api/admin/plugins/${encodeURIComponent(plugin.id)}/permission-diff`
        : "/api/admin/plugins/permission-diff";
      const response = await adminFetch(api, path, {
        method: "POST",
        body: JSON.stringify({
          download_url: distribution.download_url,
          checksum_sha256: distribution.checksum_sha256,
        }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("预览权限")));
      const payload = await response.json() as { data?: PluginPermissionDiffPreviewPayload };
      setPluginPermissionPreviews((drafts) => ({
        ...drafts,
        [plugin.id]: { busy: false, error: "", preview: payload.data ?? null },
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setPluginPermissionPreviews((drafts) => ({
        ...drafts,
        [plugin.id]: {
          ...current,
          busy: false,
          error: reason instanceof Error ? reason.message : tx("权限预览失败"),
          preview: null,
        },
      }));
    }
  }

  async function updatePlugin(plugin: PluginDescriptor) {
    setPluginUpdateDrafts((drafts) => ({
      ...drafts,
      [plugin.id]: { ...(drafts[plugin.id] ?? { busy: false, error: "", result: "" }), busy: true, error: "", result: "" },
    }));
    try {
      const distribution = plugin.distribution;
      const response = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(plugin.id)}/update`, {
        method: "POST",
        body: distribution?.download_url && distribution.checksum_sha256
          ? JSON.stringify({
            download_url: distribution.download_url,
            checksum_sha256: distribution.checksum_sha256,
          })
          : undefined,
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("更新插件包")));
      const payload = await response.json() as { data?: { plugin?: { version?: string; name?: string }; restart_required?: boolean } };
      const label = payload.data?.plugin?.version ?? tx("插件");
      setPluginUpdateDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          busy: false,
          error: "",
          result: `${label} · ${payload.data?.restart_required ? tx("插件更新完成，重启后生效") : tx("插件更新完成")}`,
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
      const pluginID = payload.data?.plugin_id ?? plugin.id;
      setPluginDeleteDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          busy: false,
          error: "",
          result: `${pluginID} · ${payload.data?.restart_required ? tx("插件卸载完成，重启后生效") : tx("插件卸载完成")}`,
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
      const rollbackVersion = payload.data?.rollback_version ?? payload.data?.plugin?.version ?? plugin.version ?? plugin.id;
      setPluginRollbackDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          busy: false,
          error: "",
          result: `${rollbackVersion} · ${payload.data?.restart_required ? tx("插件回滚完成，重启后生效") : tx("插件回滚完成")}`,
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

  function setSIMTemplateDefault(pluginID: string) {
    const nextTheme = preferredSIMTheme(themeOptions, pluginID, simSelection.preference.themeKey);
    const nextLayout = preferredSIMLayout(layoutOptions, pluginID, simSelection.preference.layoutKey);
    onSIMSelectionPreferenceChange?.({
      simPluginID: pluginID,
      themeKey: nextTheme?.key ?? "",
      themeID: nextTheme?.id ?? "",
      layoutKey: nextLayout?.key ?? "",
      layoutID: nextLayout?.id ?? "",
    });
  }

  return (
    <div className="plugins-view">
      <PluginManagerHeader activeTab={activeTab} marketplaceWebsiteURL={marketplaceWebsiteURL} onTabChange={selectManagerTab} />

      {activeTab === "installed" ? (
        <div className="plugin-extension-workspace">
          <aside className="plugin-extension-nav" aria-label={tx("已安装插件")} role="tablist">
            <div className="plugin-extension-nav-heading">
              <strong>{tx("已安装插件")}</strong>
              <span>{effectivePlugins.length}</span>
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
              <strong>{effectivePlugins.length}</strong>
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
                <strong>{extensionCategoryCount(category.key, { providerPlugins, chainInjectionPlugins, uiTemplatePlugins, backgroundJobPlugins })}</strong>
              </button>
            ))}
          </aside>
          <div className="plugin-extension-content">
      {activeExtensionCategory === "all" ? (
      <section className="section plugin-installed-section" data-plugin-manager-section="registry">
        <div className="section-header plugin-installed-header">
          <div>
            <h2>{tx("已安装插件")}</h2>
            <span>{plugins.length}</span>
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
          {filteredPlugins.length === 0 ? (
            <p className="empty-state">{tx("暂无插件")}</p>
          ) : (
            <div className="plugin-installed-list">
                  {filteredPlugins.map((plugin) => {
                    const lifecycle = pluginManagerDisplayState({ plugin });
                    return (
                      <article className={`plugin-installed-row${lifecycle.status === "disabled" ? " disabled" : ""}`} key={plugin.id}>
                        <div className="plugin-installed-main">
                          <PluginTitle plugin={plugin} onSelect={onSelectPlugin} />
                          <div className="plugin-installed-taxonomy">
                            <TagList values={[...plugin.kinds.map(pluginKindLabel), ...plugin.placements.map(pluginPlacementLabel)]} />
                          </div>
                        </div>
                        <div className="plugin-installed-source">
                          <StatusPill status={plugin.source} label={pluginSourceLabel(plugin.source)} />
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
                        </div>
                        <div className="plugin-installed-distribution">
                          {plugin.distribution ? (
                            <DistributionMetadata
                              lifecycle={lifecycle}
                              plugin={plugin}
                              draft={pluginUpdateDraft(plugin)}
                              onUpdate={updatePlugin}
                              onPreview={(target) => previewPluginPermissions(target, "update")}
                              previewDraft={pluginPermissionPreviewDraft(plugin)}
                            />
                          ) : null}
                        </div>
                        <div className="plugin-installed-actions">
                          {onSelectPlugin ? (
                            <>
                              <button className="secondary-button compact-button" onClick={() => onSelectPlugin(plugin.id)} type="button">
                                <PackageOpen size={14} aria-hidden="true" />
                                <span>{tx("详情")}</span>
                              </button>
                              {pluginsWithSettings.has(plugin.id) ? (
                                <button className="secondary-button compact-button" onClick={() => onSelectPlugin(plugin.id, "settings")} type="button">
                                  <Settings2 size={14} aria-hidden="true" />
                                  <span>{tx("设置")}</span>
                                </button>
                              ) : null}
                            </>
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
          )}
        </div>
      </section>
      ) : null}

      {activeExtensionCategory === "provider" ? (
        <section className="section" data-plugin-manager-section="provider">
          <div className="section-header">
            <h2>{tx("Provider 插件清单")}</h2>
          </div>
          <div className="section-body">
            {providerPluginList.length === 0 ? (
              <p className="empty-state">{tx("暂无 Provider 插件")}</p>
            ) : (
              <div className="plugin-type-list">
                {providerPluginList.map((plugin) => {
                  const lifecycle = pluginManagerDisplayState({ plugin });
                  return (
                    <article className="plugin-type-row" key={plugin.id}>
                      <div className="plugin-type-main">
                        <PluginTitle plugin={plugin} onSelect={onSelectPlugin} />
                        <div className="plugin-type-meta">
                          <StatusPill status={plugin.source} label={pluginSourceLabel(plugin.source)} />
                          <span className="plugin-type-capability-count">
                            <span>{tx("能力")}</span>
                            <strong>{plugin.capabilities.length}</strong>
                          </span>
                        </div>
                      </div>
                      <div className="plugin-type-state">
                        <PluginLifecycleControl
                          allowBuiltInUpdates
                          draft={pluginStateDraft(plugin)}
                          lifecycle={lifecycle}
                          onRollback={rollbackPlugin}
                          onUpdate={updatePluginState}
                          plugin={plugin}
                          rollbackDraft={pluginRollbackDraft(plugin)}
                        />
                      </div>
                      <div className="plugin-type-actions">
                        {onSelectPlugin ? (
                          <>
                            <button className="secondary-button compact-button" onClick={() => onSelectPlugin(plugin.id)} type="button">
                              <PackageOpen size={14} aria-hidden="true" />
                              <span>{tx("详情")}</span>
                            </button>
                            {pluginsWithSettings.has(plugin.id) ? (
                              <button className="secondary-button compact-button" onClick={() => onSelectPlugin(plugin.id, "settings")} type="button">
                                <Settings2 size={14} aria-hidden="true" />
                                <span>{tx("设置")}</span>
                              </button>
                            ) : null}
                          </>
                        ) : null}
                      </div>
                    </article>
                  );
                })}
              </div>
            )}
          </div>
        </section>
      ) : null}

      {activeExtensionCategory === "chain" ? (
        <section className="section" data-plugin-manager-section="chain-plugins">
          <div className="section-header">
            <h2>{tx("链路注入插件清单")}</h2>
          </div>
          <div className="section-body">
            {chainPluginList.length === 0 ? (
              <p className="empty-state">{tx("暂无链路注入插件")}</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>{tx("插件")}</th>
                      <th>{tx("来源")}</th>
                      <th>{tx("状态")}</th>
                      <th>{tx("注入点")}</th>
                      <th>{tx("注入阶段")}</th>
                      <th>{tx("适用对象")}</th>
                      <th>{tx("失败策略")}</th>
                      <th>{tx("读写数据")}</th>
                      <th>{tx("操作")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {chainPluginList.map((item) => {
                      const plugin = item.plugin;
                      const lifecycle = plugin ? pluginManagerDisplayState({ plugin }) : undefined;
                      return (
                        <tr key={item.pluginID}>
                          <td>{plugin ? <PluginTitle plugin={plugin} onSelect={onSelectPlugin} /> : <span>{item.pluginID}</span>}</td>
                          <td>
                            {plugin ? (
                              <StatusPill status={plugin.source} label={pluginSourceLabel(plugin.source)} />
                            ) : (
                              <span className="muted">{tx("未声明")}</span>
                            )}
                          </td>
                          <td>
                            {plugin && lifecycle ? (
                              <PluginLifecycleControl
                                draft={pluginStateDraft(plugin)}
                                lifecycle={lifecycle}
                                onRollback={rollbackPlugin}
                                onUpdate={updatePluginState}
                                plugin={plugin}
                                rollbackDraft={pluginRollbackDraft(plugin)}
                              />
                            ) : (
                              <span className="muted">-</span>
                            )}
                          </td>
                          <td>
                            <div className="stacked-cell">
                              <strong>{item.hooks.length}</strong>
                              <span>
                                {tx("强制")} {item.mandatoryHooks} · {tx("可选")} {item.hooks.length - item.mandatoryHooks}
                              </span>
                            </div>
                          </td>
                          <td><TagList values={item.stages} /></td>
                          <td><TagList values={item.subjects} emptyLabel={tx("未声明")} /></td>
                          <td><TagList values={item.failurePolicies} /></td>
                          <td>
                            <div className="stacked-cell">
                              <span>{tx("读")} {item.reads.join(", ") || "-"}</span>
                              <span>{tx("写")} {item.writes.join(", ") || "-"}</span>
                            </div>
                          </td>
                          <td>
                            {plugin && lifecycle ? (
                              <PluginDeleteControl
                                lifecycle={lifecycle}
                                plugin={plugin}
                                draft={pluginDeleteDraft(plugin)}
                                onDelete={deletePlugin}
                              />
                            ) : (
                              <span className="muted">-</span>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </section>
      ) : null}

      {activeExtensionCategory === "ui" ? (
        <>
      <section className="section" data-plugin-manager-section="template-selection">
        <div className="section-header">
          <h2>{tx("界面模板选择面板")}</h2>
        </div>
        <div className="section-body">
          {simTemplates.length === 0 ? (
            <p className="empty-state">{tx("暂无可选界面模板")}</p>
          ) : (
            <div className="sim-template-selection-panel">
              <div className="sim-template-active-summary">
                <span>{tx("当前默认")}</span>
                <strong>{activeSIMPlugin ? localizedPluginName(activeSIMPlugin, locale) : tx("自动选择")}</strong>
                <small>
                  {simSelection.theme.capability ? localizedCapabilityTitle(simSelection.theme.capability, locale) : tx("自动选择")} ·{" "}
                  {simSelection.layout.capability ? localizedCapabilityTitle(simSelection.layout.capability, locale) : tx("自动选择")}
                </small>
              </div>
              <div className="sim-template-list" aria-label={tx("界面模板列表")}>
                {simTemplates.map((template) => {
                  const isActive = simSelection.activeSIMPluginID === template.id;
                  return (
                    <div className={isActive ? "sim-template-item-row active" : "sim-template-item-row"} key={template.id}>
                      <button
                        aria-label={`${tx(pluginsWithSettings.has(template.id) ? "配置界面模板" : "查看插件详情")} ${template.name}`}
                        className="sim-template-item"
                        onClick={() => onSelectPlugin?.(template.id, pluginsWithSettings.has(template.id) ? "settings" : "overview")}
                        type="button"
                      >
                        <span className="sim-template-item-main">
                          <strong>{template.name}</strong>
                          <span>{template.id}{template.version ? ` · ${template.version}` : ""}</span>
                        </span>
                        <span className="tag-list">
                          {isActive ? <span className="tag">{tx("当前默认")}</span> : null}
                          <span className="tag">{template.theme?.title ?? tx("自动选择")}</span>
                          <span className="tag">{template.layout?.title ?? tx("自动选择")}</span>
                        </span>
                        <span className="sim-template-open-label">
                          {pluginsWithSettings.has(template.id) ? <Settings2 size={14} aria-hidden="true" /> : <PackageOpen size={14} aria-hidden="true" />}
                          {tx(pluginsWithSettings.has(template.id) ? "打开配置" : "详情")}
                        </span>
                      </button>
                      <button
                        className="secondary-button sim-template-default-button"
                        disabled={!onSIMSelectionPreferenceChange || isActive}
                        onClick={() => setSIMTemplateDefault(template.id)}
                        type="button"
                      >
                        <Save size={14} aria-hidden="true" />
                        <span>{isActive ? tx("当前默认") : tx("设为默认模板")}</span>
                      </button>
                    </div>
                  );
                })}
              </div>
            </div>
          )}
        </div>
      </section>

      <section className="section" data-plugin-manager-section="template-contributions">
        <div className="section-header">
          <h2>{tx("界面模板与主题贡献")}</h2>
        </div>
        <div className="section-body">
          {themeContributions.length === 0 && layoutContributions.length === 0 ? (
            <p className="empty-state">{tx("暂无界面模板贡献")}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{tx("类型")}</th>
                    <th>{tx("贡献")}</th>
                    <th>{tx("插件")}</th>
                    <th>{tx("模式")}</th>
                    <th>{tx("Token")}</th>
                    <th>{tx("布局密度")}</th>
                  </tr>
                </thead>
                <tbody>
                  {[...themeContributions, ...layoutContributions].map((contribution) => (
                    <tr key={`${contribution.plugin_id}:${contribution.slot}:${contribution.id}`}>
                      <td>{presentationContributionTypeLabel(contribution.slot)}</td>
                      <td>
                        <div className="stacked-cell">
                          <strong>{localizedContributionTitle(contribution, locale)}</strong>
                          <span>{contribution.id}</span>
                        </div>
                      </td>
                      <td>{contribution.plugin_id}</td>
                      <td>{themeModeLabel(contribution.schema?.mode)}</td>
                      <td>{themeTokenCount(contribution.schema?.tokens)}</td>
                      <td>{layoutDensityLabel(contribution.schema?.preset)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>

      <details className="plugin-developer-details" data-plugin-manager-section="ui-contributions">
        <summary>
          <span>{tx("开发者信息")}</span>
          <strong>{tx("UI 插槽注册")}</strong>
        </summary>
        <div className="plugin-developer-details-body">
          {uiContributions.length === 0 ? (
            <p className="empty-state">{tx("暂无 UI 插槽注册")}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{tx("插槽")}</th>
                    <th>{tx("贡献")}</th>
                    <th>{tx("插件")}</th>
                    <th>{tx("Provider 类型")}</th>
                    <th>{tx("动作")}</th>
                  </tr>
                </thead>
                <tbody>
                  {uiContributions.map((contribution) => (
                    <tr key={`${contribution.plugin_id}:${contribution.slot}:${contribution.id}`}>
                      <td>{contribution.slot}</td>
                      <td>
                        <div className="stacked-cell">
                          <strong>{localizedContributionTitle(contribution, locale)}</strong>
                          <span>{contribution.id}</span>
                        </div>
                      </td>
                      <td>{contribution.plugin_id}</td>
                      <td>{contribution.provider_types?.join(", ") || "-"}</td>
                      <td>
                        <ContributionAction action={contribution.action} registered={pluginActionKeys.has(pluginActionKey(contribution.plugin_id, contribution.action))} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </details>
        </>
      ) : null}

      {activeExtensionCategory === "jobs" ? (
        <section className="section" data-plugin-manager-section="background-jobs">
          <div className="section-header">
            <h2>{tx("后台任务插件清单")}</h2>
          </div>
          <div className="section-body">
            {backgroundJobPluginList.length === 0 ? (
              <p className="empty-state">{tx("暂无后台任务插件")}</p>
            ) : (
              <div className="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>{tx("插件")}</th>
                      <th>{tx("来源")}</th>
                      <th>{tx("状态")}</th>
                      <th>{tx("任务数量")}</th>
                      <th>{tx("调度")}</th>
                      <th>{tx("能力标识")}</th>
                      <th>{tx("最大并发")}</th>
                      <th>{tx("最近运行")}</th>
                      <th>{tx("重试")}</th>
                      <th>{tx("操作")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {backgroundJobPluginList.map((item) => {
                      const plugin = item.plugin;
                      const lifecycle = plugin ? pluginManagerDisplayState({ plugin }) : undefined;
                      return (
                        <tr key={item.pluginID}>
                          <td>{plugin ? <PluginTitle plugin={plugin} onSelect={onSelectPlugin} /> : <span>{item.pluginID}</span>}</td>
                          <td>
                            {plugin ? (
                              <StatusPill status={plugin.source} label={pluginSourceLabel(plugin.source)} />
                            ) : (
                              <span className="muted">{tx("未声明")}</span>
                            )}
                          </td>
                          <td>
                            {plugin && lifecycle ? (
                              <PluginLifecycleControl
                                draft={pluginStateDraft(plugin)}
                                lifecycle={lifecycle}
                                onRollback={rollbackPlugin}
                                onUpdate={updatePluginState}
                                plugin={plugin}
                                rollbackDraft={pluginRollbackDraft(plugin)}
                              />
                            ) : (
                              <span className="muted">-</span>
                            )}
                          </td>
                          <td><strong>{item.jobs.length}</strong></td>
                          <td><TagList values={item.schedules} /></td>
                          <td><TagList values={item.capabilities} /></td>
                          <td>{item.maxConcurrency}</td>
                          <td><TagList values={item.recentRuns} emptyLabel={tx("未运行")} /></td>
                          <td><TagList values={item.retries} /></td>
                          <td>
                            {plugin && lifecycle ? (
                              <PluginDeleteControl
                                lifecycle={lifecycle}
                                plugin={plugin}
                                draft={pluginDeleteDraft(plugin)}
                                onDelete={deletePlugin}
                              />
                            ) : (
                              <span className="muted">-</span>
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            )}
          </div>
        </section>
      ) : null}
          </div>
        </div>
      ) : null}

      {activeTab === "install" ? (
        <section className="section plugin-install-center" data-plugin-manager-section="install">
          <div className="section-header">
            <div>
              <h2>{tx("安装插件")}</h2>
              <span>{tx("URL 或 ZIP 插件包")}</span>
            </div>
            <a className="secondary-button plugin-marketplace-link" href={marketplaceWebsiteURL} rel="noreferrer" target="_blank">
              <ExternalLink size={14} aria-hidden="true" />
              <span>{tx("浏览插件市场")}</span>
            </a>
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

function extensionCategoryCount(
  category: PluginExtensionCategoryKey,
  counts: { providerPlugins: number; chainInjectionPlugins: number; uiTemplatePlugins: number; backgroundJobPlugins: number },
) {
  if (category === "provider") return counts.providerPlugins;
  if (category === "chain") return counts.chainInjectionPlugins;
  if (category === "ui") return counts.uiTemplatePlugins;
  return counts.backgroundJobPlugins;
}

function ContributionAction({ action, registered }: { action?: string; registered: boolean }) {
  if (!action) return <>-</>;
  return (
    <div className="stacked-cell">
      <strong>{action}</strong>
      <span>{registered ? tx("动作已注册") : tx("动作未注册")}</span>
    </div>
  );
}

function PluginTitle({ plugin, onSelect }: { plugin: PluginDescriptor; onSelect?: (pluginID: string) => void }) {
  const locale = languageLocale();
  const name = localizedPluginName(plugin, locale);
  return (
    <div className="plugin-title-cell">
      {onSelect ? (
        <button className="plugin-title-link" type="button" onClick={() => onSelect(plugin.id)} aria-label={`${tx("查看插件详情")} ${name}`}>
          {name}
        </button>
      ) : (
        <strong className="plugin-title-name">{name}</strong>
      )}
      <div className="plugin-title-meta">
        <span className="plugin-title-id" title={plugin.id}>{plugin.id}</span>
        <span className="plugin-title-version">{plugin.version || tx("内置")}</span>
      </div>
    </div>
  );
}

function TagList({ values, emptyLabel = "-" }: { values: string[]; emptyLabel?: string }) {
  if (values.length === 0) return <span className="muted">{emptyLabel}</span>;
  return (
    <div className="tag-list">
      {values.map((value) => <span className="tag" key={value}>{value}</span>)}
    </div>
  );
}

function DistributionMetadata({
  plugin,
  lifecycle,
  draft,
  onUpdate,
  onPreview,
  previewDraft,
  showUpdate = true,
}: {
  plugin: PluginDescriptor;
  lifecycle?: PluginManagerDisplayState;
  draft: PluginUpdateDraft;
  onUpdate: (plugin: PluginDescriptor) => void;
  onPreview?: (plugin: PluginDescriptor) => void;
  previewDraft?: PluginPermissionDiffPreviewDraft;
  showUpdate?: boolean;
}) {
  const distribution = plugin.distribution;
  if (!distribution) return <span className="muted">{tx("未声明")}</span>;
  const effectiveLifecycle = lifecycle ?? pluginManagerDisplayState({ plugin });
  const candidates: Array<{ href?: string; label: string; icon: ReactNode }> = [
    { href: distribution.marketplace_url, label: tx("市场"), icon: <ExternalLink size={13} /> },
    { href: distribution.repository_url, label: tx("仓库"), icon: <GitBranch size={13} /> },
    { href: distribution.download_url, label: tx("下载"), icon: <Download size={13} /> },
    { href: distribution.homepage_url, label: tx("主页"), icon: <ExternalLink size={13} /> },
    { href: distribution.signature_url, label: tx("签名"), icon: <ShieldCheck size={13} /> },
  ];
  const links = candidates.flatMap((link) => (link.href ? [{ ...link, href: link.href }] : []));
  return (
    <div className="stacked-cell" data-plugin-manager-control="distribution">
      {links.length > 0 ? (
        <div className="tag-list">
          {links.map((link) => (
            <a className="tag" href={link.href} key={`${plugin.id}:${link.label}`} rel="noreferrer" target="_blank">
              {link.icon}
              <span>{link.label}</span>
            </a>
          ))}
        </div>
      ) : (
        <span className="muted">{tx("无下载来源")}</span>
      )}
      {showUpdate && effectiveLifecycle.actions.update.available ? (
        <button className="secondary-button compact-button" disabled={draft.busy} onClick={() => onUpdate(plugin)} type="button">
          <Download size={13} />
          <span>{tx(draft.busy ? "更新中" : "更新")}</span>
        </button>
      ) : null}
      {previewDraft && onPreview ? (
        <PluginPermissionDiffPreview
          disabled={!distribution.download_url || !distribution.checksum_sha256}
          draft={previewDraft}
          onPreview={() => onPreview(plugin)}
        />
      ) : null}
      {showUpdate && draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      {showUpdate && draft.result ? <span>{draft.result}</span> : null}
      <span>{distribution.license ? `${tx("许可证")} ${distribution.license}` : tx("未声明许可证")}</span>
      {distribution.checksum_sha256 ? <span>{tx("SHA-256")} {shortChecksum(distribution.checksum_sha256)}</span> : null}
    </div>
  );
}

function shortChecksum(value: string) {
  return value.length > 16 ? `${value.slice(0, 12)}...${value.slice(-4)}` : value;
}

function pluginKindLabel(kind: string) {
  if (kind === "provider") return tx("Provider");
  if (kind === "admin_ui") return tx("Admin UI");
  if (kind === "sim") return tx("界面模板");
  if (kind === "extension") return tx("Extension");
  return kind;
}

function pluginPlacementLabel(placement: string) {
  if (placement === "presentation") return tx("Presentation");
  if (placement === "gateway_chain") return tx("Gateway Chain");
  if (placement === "background") return tx("Background");
  if (placement === "management_action") return tx("Management Action");
  return placement;
}

function presentationContributionTypeLabel(slot: string) {
  if (slot === "theme.tokens") return tx("主题 Token");
  if (slot === "layout.preset") return tx("布局预设");
  return slot;
}

function themeModeLabel(value: unknown) {
  if (value === "light") return tx("浅色");
  if (value === "dark") return tx("深色");
  if (value === "all") return tx("全部");
  return "-";
}

function themeTokenCount(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return "-";
  return String(Object.keys(value).length);
}

function layoutDensityLabel(value: unknown) {
  if (!value || typeof value !== "object" || Array.isArray(value)) return "-";
  const density = (value as { density?: unknown }).density;
  if (density === "compact") return tx("紧凑");
  if (density === "spacious") return tx("宽松");
  if (density === "comfortable") return tx("舒适");
  return "-";
}

function backgroundJobRetryLabel(retry?: { max_attempts?: number; backoff_millis?: number }) {
  if (!retry?.max_attempts) return "-";
  return retry.backoff_millis ? `${retry.max_attempts} / ${retry.backoff_millis}ms` : String(retry.max_attempts);
}

function backgroundJobRunLabel(run?: { status: string; attempts: number }) {
  if (!run) return tx("未运行");
  return `${backgroundJobStatusLabel(run.status)} / ${run.attempts}`;
}

function backgroundJobStatusLabel(status: string) {
  if (status === "succeeded") return tx("成功");
  if (status === "failed") return tx("失败");
  if (status === "skipped") return tx("跳过");
  return status;
}

type SIMSelectionCapabilityOption = {
  key: string;
  id: string;
  title: string;
  pluginID: string;
  pluginName: string;
  pluginVersion: string;
};

type SIMSelectionPluginOption = {
  id: string;
  name: string;
  version: string;
};

type SIMTemplateOption = SIMSelectionPluginOption & {
  theme?: SIMSelectionCapabilityOption;
  layout?: SIMSelectionCapabilityOption;
};

function simSelectionPlugins(plugins: readonly PluginDescriptor[], registry: SIMRegistry, locale: string): SIMSelectionPluginOption[] {
  const capabilityPluginIDs = new Set([...registry.themeTokens, ...registry.shellLayouts].map((capability) => capability.pluginID));
  return plugins.flatMap((plugin) => {
    const hasSIMKind = plugin.kinds?.includes("sim");
    const hasSIMCapability = capabilityPluginIDs.has(plugin.id);
    if (!hasSIMKind && !hasSIMCapability) return [];
    return [{
      id: plugin.id,
      name: localizedPluginName(plugin, locale),
      version: plugin.version || "",
    }];
  });
}

function simTemplateOptions(
  plugins: readonly SIMSelectionPluginOption[],
  themeOptions: SIMSelectionCapabilityOption[],
  layoutOptions: SIMSelectionCapabilityOption[],
): SIMTemplateOption[] {
  return plugins.map((plugin) => ({
    ...plugin,
    theme: preferredSIMTheme(themeOptions, plugin.id, ""),
    layout: preferredSIMLayout(layoutOptions, plugin.id, ""),
  }));
}

function simSelectionThemeOptions(themeTokens: readonly SIMThemeTokens[], plugins: readonly PluginDescriptor[], theme: "light" | "dark", locale: string) {
  const pluginNames = localizedPluginNameMap(plugins, locale);
  return themeTokens
    .filter((token) => token.payload.mode === "all" || token.payload.mode === theme)
    .map((token) => ({
      key: token.key,
      id: token.id,
      title: localizedCapabilityTitle(token, locale),
      pluginID: token.pluginID,
      pluginName: pluginNames.get(token.pluginID) || token.pluginName || token.pluginID,
      pluginVersion: token.pluginVersion || "",
    }));
}

function simSelectionLayoutOptions(layouts: readonly SIMShellLayout[], plugins: readonly PluginDescriptor[], locale: string) {
  const pluginNames = localizedPluginNameMap(plugins, locale);
  return layouts.map((layout) => ({
    key: layout.key,
    id: layout.id,
    title: localizedCapabilityTitle(layout, locale),
    pluginID: layout.pluginID,
    pluginName: pluginNames.get(layout.pluginID) || layout.pluginName || layout.pluginID,
    pluginVersion: layout.pluginVersion || "",
  }));
}

function localizedPluginNameMap(plugins: readonly PluginDescriptor[], locale: string) {
  return new Map(plugins.map((plugin) => [plugin.id, localizedPluginName(plugin, locale)]));
}

function preferredSIMTheme(options: SIMSelectionCapabilityOption[], pluginID: string, currentKey: string) {
  return options.find((option) => option.key === currentKey && option.pluginID === pluginID) ??
    options.find((option) => option.pluginID === pluginID && option.id) ??
    options.find((option) => option.pluginID === pluginID);
}

function preferredSIMLayout(options: SIMSelectionCapabilityOption[], pluginID: string, currentKey: string) {
  return options.find((option) => option.key === currentKey && option.pluginID === pluginID) ??
    options.find((option) => option.pluginID === pluginID && option.id) ??
    options.find((option) => option.pluginID === pluginID);
}

function pluginSourceLabel(source: string) {
  if (source === "built_in") return tx("内置");
  if (source === "marketplace") return tx("插件市场");
  if (source === "local_file") return tx("本地文件");
  return source;
}

type ChainPluginSummary = {
  pluginID: string;
  plugin?: PluginDescriptor;
  hooks: GatewayHookDescriptor[];
  stages: string[];
  subjects: string[];
  failurePolicies: string[];
  reads: string[];
  writes: string[];
  mandatoryHooks: number;
};

type BackgroundJobPluginSummary = {
  pluginID: string;
  plugin?: PluginDescriptor;
  jobs: PluginBackgroundJobDescriptor[];
  schedules: string[];
  capabilities: string[];
  retries: string[];
  recentRuns: string[];
  maxConcurrency: number;
};

type BackgroundJobRunSummary = {
  status: string;
  attempts: number;
};

function chainPluginSummaries(plugins: PluginDescriptor[], hooks: GatewayHookDescriptor[], locale: string): ChainPluginSummary[] {
  const pluginsByID = new Map(plugins.map((plugin) => [plugin.id, plugin]));
  const summaries = new Map<string, ChainPluginSummary>();
  hooks.forEach((hook) => {
    const pluginID = hook.plugin_id.trim();
    if (!pluginID) return;
    const current = summaries.get(pluginID) ?? {
      pluginID,
      plugin: pluginsByID.get(pluginID),
      hooks: [],
      stages: [],
      subjects: [],
      failurePolicies: [],
      reads: [],
      writes: [],
      mandatoryHooks: 0,
    };
    current.hooks.push(hook);
    current.stages = uniqueStrings([...current.stages, hook.stage]);
    current.subjects = uniqueStrings([...current.subjects, hook.subject ?? ""]);
    current.failurePolicies = uniqueStrings([...current.failurePolicies, hook.failure_policy]);
    current.reads = uniqueStrings([...current.reads, ...(hook.reads ?? [])]);
    current.writes = uniqueStrings([...current.writes, ...(hook.writes ?? [])]);
    current.mandatoryHooks += hook.mandatory ? 1 : 0;
    summaries.set(pluginID, current);
  });
  return Array.from(summaries.values()).sort((first, second) => {
    const firstName = first.plugin ? localizedPluginName(first.plugin, locale) : first.pluginID;
    const secondName = second.plugin ? localizedPluginName(second.plugin, locale) : second.pluginID;
    return firstName.localeCompare(secondName, locale);
  });
}

function backgroundJobPluginSummaries(
  plugins: PluginDescriptor[],
  jobs: PluginBackgroundJobDescriptor[],
  runs: Map<string, BackgroundJobRunSummary>,
  locale: string,
): BackgroundJobPluginSummary[] {
  const pluginsByID = new Map(plugins.map((plugin) => [plugin.id, plugin]));
  const summaries = new Map<string, BackgroundJobPluginSummary>();
  jobs.forEach((job) => {
    const pluginID = job.plugin_id.trim();
    if (!pluginID) return;
    const current = summaries.get(pluginID) ?? {
      pluginID,
      plugin: pluginsByID.get(pluginID),
      jobs: [],
      schedules: [],
      capabilities: [],
      retries: [],
      recentRuns: [],
      maxConcurrency: 0,
    };
    current.jobs.push(job);
    current.schedules = uniqueStrings([...current.schedules, job.schedule]);
    current.capabilities = uniqueStrings([...current.capabilities, job.capability ?? job.subject ?? ""]);
    const retry = backgroundJobRetryLabel(job.retry);
    current.retries = uniqueStrings([...current.retries, retry === "-" ? "" : retry]);
    const run = runs.get(pluginBackgroundJobKey(job.plugin_id, job.job_id));
    current.recentRuns = uniqueStrings([...current.recentRuns, run ? backgroundJobRunLabel(run) : ""]);
    current.maxConcurrency = Math.max(current.maxConcurrency, job.max_concurrency || 0);
    summaries.set(pluginID, current);
  });
  return Array.from(summaries.values()).sort((first, second) => {
    const firstName = first.plugin ? localizedPluginName(first.plugin, locale) : first.pluginID;
    const secondName = second.plugin ? localizedPluginName(second.plugin, locale) : second.pluginID;
    return firstName.localeCompare(secondName, locale);
  });
}

function uniqueStrings(values: string[]) {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean))).sort();
}
