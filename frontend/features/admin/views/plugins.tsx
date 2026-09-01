import { Boxes, Clock3, Download, ExternalLink, GitBranch, Layers3, PlugZap, Save, ShieldCheck, Upload } from "lucide-react";
import { type FormEvent, type ReactNode, useMemo, useState } from "react";
import { type ApiContext, type AppData, type PluginBackgroundJobDescriptor, type PluginDescriptor } from "../core/types";
import { pluginActionInputDefaults, pluginActionKey, pluginBackgroundJobKey, pluginBackgroundJobPayload, redactPluginActionResult } from "../domain/plugin-actions";
import { pluginManagerTabs, pluginMarketplaceWebsiteURL, type PluginManagerTabKey } from "../domain/plugin-management";
import { pluginManagerDisplayState, type PluginManagerDisplayState } from "../domain/plugin-manager";
import { localizedCapabilityTitle, localizedContributionTitle, localizedPluginName } from "../domain/plugin-localization";
import { type PluginPermissionDiffPreviewPayload } from "../domain/plugin-permission-diff";
import { simRegistryFromPlugins, type SIMRegistry, type SIMShellLayout, type SIMThemeTokens } from "../domain/sim-registry";
import { resolveSIMSelection, type SIMSelectionPreference } from "../domain/sim-selection";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { StatusPill } from "../shared/ui";
import { PluginBackgroundJobRunner } from "./plugin-action-runner";
import { emptyInstallDraft, PluginInstallDialog, pluginInstallRequestBody, type PluginInstallDraft } from "./plugin-install-form";
import { PluginDeleteControl, PluginLifecycleControl, type PluginDeleteDraft, type PluginRollbackDraft, type PluginStateDraft } from "./plugin-manager-controls";
import { emptyPermissionPreviewDraft, PluginPermissionDiffPreview, type PluginPermissionDiffPreviewDraft } from "./plugin-permission-diff-preview";

type PluginUpdateDraft = {
  busy: boolean;
  error: string;
  result: string;
};

type PluginBackgroundJobDraft = {
  values: Record<string, string | boolean>;
  busy: boolean;
  error: string;
  result: string;
};

export function PluginsView({
  api,
  data,
  simSelectionPreference,
  onSIMSelectionPreferenceChange,
  theme = "light",
}: {
  api: ApiContext;
  data: AppData;
  simSelectionPreference?: unknown;
  onSIMSelectionPreferenceChange?: (preference: SIMSelectionPreference) => void;
  theme?: "light" | "dark";
}) {
  const plugins = data.plugins;
  const [pluginStateDrafts, setPluginStateDrafts] = useState<Record<string, PluginStateDraft>>({});
  const [pluginUpdateDrafts, setPluginUpdateDrafts] = useState<Record<string, PluginUpdateDraft>>({});
  const [pluginDeleteDrafts, setPluginDeleteDrafts] = useState<Record<string, PluginDeleteDraft>>({});
  const [pluginRollbackDrafts, setPluginRollbackDrafts] = useState<Record<string, PluginRollbackDraft>>({});
  const [backgroundJobDrafts, setBackgroundJobDrafts] = useState<Record<string, PluginBackgroundJobDraft>>({});
  const [installPermissionPreview, setInstallPermissionPreview] = useState<PluginPermissionDiffPreviewDraft>(emptyPermissionPreviewDraft());
  const [pluginPermissionPreviews, setPluginPermissionPreviews] = useState<Record<string, PluginPermissionDiffPreviewDraft>>({});
  const [installDraft, setInstallDraft] = useState<PluginInstallDraft>(emptyInstallDraft());
  const [installDialogOpen, setInstallDialogOpen] = useState(false);
  const [activeTab, setActiveTab] = useState<PluginManagerTabKey>("registry");
  const simRegistry = useMemo(() => simRegistryFromPlugins(plugins), [plugins]);
  const simSelection = useMemo(
    () => resolveSIMSelection({ plugins, preference: simSelectionPreference, themeMode: theme }),
    [plugins, simSelectionPreference, theme],
  );
  const [selectedSIMPluginID, setSelectedSIMPluginID] = useState("");
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
  const backgroundRuns = new Map(data.pluginBackgroundRuns.map((run) => [pluginBackgroundJobKey(run.plugin_id, run.job_id), run]));
  const pluginActionKeys = new Set(pluginActions.map((action) => pluginActionKey(action.plugin_id, action.action_id)));
  const marketplaceWebsiteURL = pluginMarketplaceWebsiteURL(data);
  const providerPlugins = plugins.filter((plugin) => plugin.kinds?.includes("provider")).length;
  const chainInjectionPlugins = new Set(data.pluginChain.hooks.map((hook) => hook.plugin_id)).size;
  const uiTemplatePlugins = plugins.filter((plugin) => plugin.kinds?.includes("sim")).length;
  const backgroundJobPlugins = new Set(backgroundJobs.map((job) => job.plugin_id)).size;
  const activeSIMPlugin = simPlugins.find((plugin) => plugin.id === simSelection.activeSIMPluginID);
  const selectedSIMTemplate =
    simTemplates.find((template) => template.id === selectedSIMPluginID) ??
    simTemplates.find((template) => template.id === simSelection.activeSIMPluginID) ??
    simTemplates[0];
  const pluginStateDraft = (plugin: PluginDescriptor) => pluginStateDrafts[plugin.id] ?? {};
  const pluginUpdateDraft = (plugin: PluginDescriptor) => pluginUpdateDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginDeleteDraft = (plugin: PluginDescriptor) => pluginDeleteDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginRollbackDraft = (plugin: PluginDescriptor) => pluginRollbackDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginPermissionPreviewDraft = (plugin: PluginDescriptor) => pluginPermissionPreviews[plugin.id] ?? emptyPermissionPreviewDraft();
  const backgroundJobDraft = (job: PluginBackgroundJobDescriptor) => backgroundJobDrafts[pluginBackgroundJobKey(job.plugin_id, job.job_id)] ?? emptyBackgroundJobDraft(job);

  function updateBackgroundJobValue(job: PluginBackgroundJobDescriptor, field: string, value: string | boolean) {
    const key = pluginBackgroundJobKey(job.plugin_id, job.job_id);
    setBackgroundJobDrafts((drafts) => ({
      ...drafts,
      [key]: {
        ...emptyBackgroundJobDraft(job),
        ...drafts[key],
        values: { ...(drafts[key]?.values ?? pluginActionInputDefaults(job)), [field]: value },
        error: "",
      },
    }));
  }

  async function runBackgroundJob(job: PluginBackgroundJobDescriptor, event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const key = pluginBackgroundJobKey(job.plugin_id, job.job_id);
    const draft = backgroundJobDraft(job);
    setBackgroundJobDrafts((drafts) => ({ ...drafts, [key]: { ...draft, busy: true, error: "", result: "" } }));
    try {
      const response = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(job.plugin_id)}/background-jobs/${encodeURIComponent(job.job_id)}/run`, {
        method: "POST",
        body: JSON.stringify(pluginBackgroundJobPayload(job, draft.values)),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("运行后台任务")));
      const payload = await response.json();
      setBackgroundJobDrafts((drafts) => ({
        ...drafts,
        [key]: { ...draft, busy: false, error: "", result: JSON.stringify(redactPluginActionResult(payload), null, 2) },
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setBackgroundJobDrafts((drafts) => ({
        ...drafts,
        [key]: { ...draft, busy: false, error: reason instanceof Error ? reason.message : tx("运行后台任务失败"), result: "" },
      }));
    }
  }

  async function updatePluginState(plugin: PluginDescriptor, status: string) {
    const current = pluginStateDraft(plugin);
    setPluginStateDrafts((drafts) => ({
      ...drafts,
      [plugin.id]: { ...current, busy: true, error: "", restartRequired: false },
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
        [plugin.id]: { status: payload.data?.status ?? status, busy: false, error: "", restartRequired: Boolean(payload.data?.restart_required) },
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
      <div className="plugin-manager-topbar">
        <div className="plugin-manager-tabs settings-tabs" role="tablist" aria-label={tx("插件管理模块")}>
          {pluginManagerTabs.map((tab) => (
            <button
              aria-selected={activeTab === tab.key}
              className={activeTab === tab.key ? "settings-tab active" : "settings-tab"}
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              role="tab"
              type="button"
            >
              {tx(tab.label)}
            </button>
          ))}
        </div>
        <div className="plugin-manager-actions">
          <a className="secondary-button plugin-marketplace-link" href={marketplaceWebsiteURL} rel="noreferrer" target="_blank">
            <ExternalLink size={14} />
            <span>{tx("插件市场")}</span>
          </a>
          <button className="secondary-button plugin-local-install-button" onClick={() => setInstallDialogOpen(true)} type="button">
            <Upload size={14} />
            <span>{tx("安装本地插件")}</span>
          </button>
        </div>
      </div>

      {installDialogOpen ? (
        <PluginInstallDialog
          draft={installDraft}
          onClose={() => setInstallDialogOpen(false)}
          onInstall={installPlugin}
          onPermissionPreview={previewInstallPluginPermissions}
          permissionPreviewDraft={installPermissionPreview}
          setDraft={setInstallDraft}
        />
      ) : null}

      <div className="metric-grid">
        <PluginMetric icon={<PlugZap size={18} />} label={tx("已注册插件")} value={plugins.length} />
        <PluginMetric icon={<Boxes size={18} />} label={tx("Provider 插件")} value={providerPlugins} />
        <PluginMetric icon={<Layers3 size={18} />} label={tx("链路注入插件")} value={chainInjectionPlugins} />
        <PluginMetric icon={<ShieldCheck size={18} />} label={tx("界面模板插件")} value={uiTemplatePlugins} />
        <PluginMetric icon={<Clock3 size={18} />} label={tx("后台任务插件")} value={backgroundJobPlugins} />
      </div>

      {activeTab === "registry" ? (
        <>
      <section className="section" data-plugin-manager-section="registry">
        <div className="section-header">
          <h2>{tx("插件注册表")}</h2>
        </div>
        <div className="section-body">
          {plugins.length === 0 ? (
            <p className="empty-state">{tx("暂无插件")}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{tx("插件")}</th>
                    <th>{tx("来源")}</th>
                    <th>{tx("状态")}</th>
                    <th>{tx("类型")}</th>
                    <th>{tx("运行位置")}</th>
                    <th>{tx("分发")}</th>
                    <th>{tx("能力")}</th>
                    <th>{tx("操作")}</th>
                  </tr>
                </thead>
                <tbody>
                  {plugins.map((plugin) => {
                    const lifecycle = pluginManagerDisplayState({ plugin });
                    return (
                      <tr key={plugin.id}>
                        <td>
                          <PluginTitle plugin={plugin} />
                        </td>
                        <td>
                          <StatusPill status={plugin.source} label={pluginSourceLabel(plugin.source)} />
                        </td>
                        <td>
                          <PluginLifecycleControl
                            draft={pluginStateDraft(plugin)}
                            lifecycle={lifecycle}
                            onRollback={rollbackPlugin}
                            onUpdate={updatePluginState}
                            plugin={plugin}
                            rollbackDraft={pluginRollbackDraft(plugin)}
                          />
                        </td>
                        <td>{plugin.kinds.map(pluginKindLabel).join(", ")}</td>
                        <td>{plugin.placements.map(pluginPlacementLabel).join(", ")}</td>
                        <td>
                          <DistributionMetadata
                            lifecycle={lifecycle}
                            plugin={plugin}
                            draft={pluginUpdateDraft(plugin)}
                            onUpdate={updatePlugin}
                            onPreview={(target) => previewPluginPermissions(target, "update")}
                            previewDraft={pluginPermissionPreviewDraft(plugin)}
                          />
                        </td>
                        <td>
                          <CapabilityList plugin={plugin} />
                        </td>
                        <td>
                          <PluginDeleteControl
                            lifecycle={lifecycle}
                            plugin={plugin}
                            draft={pluginDeleteDraft(plugin)}
                            onDelete={deletePlugin}
                          />
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
        </>
      ) : null}

      {activeTab === "chain" ? (
      <section className="section" data-plugin-manager-section="chain-hooks">
        <div className="section-header">
          <h2>{tx("链路注入计划")}</h2>
        </div>
        <div className="section-body">
          {data.pluginChain.hooks.length === 0 ? (
            <p className="empty-state">{tx("暂无链路 Hook")}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{tx("阶段")}</th>
                    <th>{tx("Hook")}</th>
                    <th>{tx("插件")}</th>
                    <th>{tx("适用对象")}</th>
                    <th>{tx("策略")}</th>
                    <th>{tx("读")}</th>
                    <th>{tx("写")}</th>
                  </tr>
                </thead>
                <tbody>
                  {data.pluginChain.hooks.map((hook) => (
                    <tr key={`${hook.plugin_id}:${hook.hook_id}`}>
                      <td>{hook.stage}</td>
                      <td>
                        <div className="stacked-cell">
                          <strong>{hook.hook_id}</strong>
                          <span>{mandatoryLabel(hook.mandatory)} · {hook.priority}</span>
                        </div>
                      </td>
                      <td>{hook.plugin_id}</td>
                      <td>{hook.subject || tx("未声明")}</td>
                      <td>{hook.failure_policy}</td>
                      <td>{hook.reads?.join(", ") || "-"}</td>
                      <td>{hook.writes?.join(", ") || "-"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>
      ) : null}

      {activeTab === "ui" ? (
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
              <div className="sim-template-list" aria-label={tx("界面模板列表")}>
                {simTemplates.map((template) => {
                  const isSelected = selectedSIMTemplate?.id === template.id;
                  const isActive = simSelection.activeSIMPluginID === template.id;
                  return (
                    <button
                      key={template.id}
                      aria-pressed={isSelected}
                      className={isSelected ? "sim-template-item active" : "sim-template-item"}
                      onClick={() => setSelectedSIMPluginID(template.id)}
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
                    </button>
                  );
                })}
              </div>

              {selectedSIMTemplate ? (
                <aside className="sim-template-detail-panel">
                  <div className="stacked-cell">
                    <strong>{tx("默认模板")}</strong>
                    <span>
                      {activeSIMPlugin ? localizedPluginName(activeSIMPlugin, locale) : tx("自动选择")} ·{" "}
                      {simSelection.theme.capability ? localizedCapabilityTitle(simSelection.theme.capability, locale) : tx("自动选择")} ·{" "}
                      {simSelection.layout.capability ? localizedCapabilityTitle(simSelection.layout.capability, locale) : tx("自动选择")}
                    </span>
                  </div>

                  <div className="sim-template-detail-title">
                    <strong>{selectedSIMTemplate.name}</strong>
                    <span>{selectedSIMTemplate.id}</span>
                  </div>

                  <div className="sim-template-detail-grid">
                    <div>
                      <span>{tx("版本")}</span>
                      <strong>{selectedSIMTemplate.version || tx("未声明")}</strong>
                    </div>
                    <div>
                      <span>{tx("主题")}</span>
                      <strong>{selectedSIMTemplate.theme?.title ?? tx("自动选择")}</strong>
                    </div>
                    <div>
                      <span>{tx("布局")}</span>
                      <strong>{selectedSIMTemplate.layout?.title ?? tx("自动选择")}</strong>
                    </div>
                  </div>

                  <button
                    className="secondary-button plugin-action-button"
                    disabled={!onSIMSelectionPreferenceChange || simSelection.activeSIMPluginID === selectedSIMTemplate.id}
                    onClick={() => setSIMTemplateDefault(selectedSIMTemplate.id)}
                    type="button"
                  >
                    <Save size={14} />
                    <span>{simSelection.activeSIMPluginID === selectedSIMTemplate.id ? tx("当前默认") : tx("设为默认模板")}</span>
                  </button>
                </aside>
              ) : null}
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

      {activeTab === "jobs" ? (
        <section className="section" data-plugin-manager-section="background-jobs">
        <div className="section-header">
          <h2>{tx("后台任务清单")}</h2>
        </div>
        <div className="section-body">
          {backgroundJobs.length === 0 ? (
            <p className="empty-state">{tx("暂无后台任务")}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{tx("插件")}</th>
                    <th>{tx("任务 ID")}</th>
                    <th>{tx("调度")}</th>
                    <th>{tx("能力标识")}</th>
                    <th>{tx("最大并发")}</th>
                    <th>{tx("最近运行")}</th>
                    <th>{tx("重试")}</th>
                    <th>{tx("操作")}</th>
                  </tr>
                </thead>
                <tbody>
                  {backgroundJobs.map((job) => {
                    const run = backgroundRuns.get(pluginBackgroundJobKey(job.plugin_id, job.job_id));
                    return (
                      <tr key={`${job.plugin_id}:${job.job_id}`}>
                        <td>{job.plugin_id}</td>
                        <td>
                          <div className="stacked-cell">
                            <strong>{job.job_id}</strong>
                            <span>{localizedContributionTitle({ ...job, id: job.job_id }, locale) || "-"}</span>
                          </div>
                        </td>
                        <td>{job.schedule}</td>
                        <td>{job.capability || job.subject || "-"}</td>
                        <td>{job.max_concurrency}</td>
                        <td>{backgroundJobRunLabel(run)}</td>
                        <td>{backgroundJobRetryLabel(job.retry)}</td>
                        <td>
                          <PluginBackgroundJobRunner
                            draft={backgroundJobDraft(job)}
                            job={job}
                            labels={{
                              submit: tx("运行任务"),
                              submitting: tx("运行中"),
                              unsupportedSchema: tx("暂不支持复杂输入 Schema"),
                            }}
                            onChange={updateBackgroundJobValue}
                            onSubmit={runBackgroundJob}
                          />
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
  );
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

function PluginMetric({ icon, label, value }: { icon: ReactNode; label: ReactNode; value: number }) {
  return (
    <section className="metric-card">
      <div className="metric-icon" aria-hidden="true">{icon}</div>
      <span>{label}</span>
      <strong>{value}</strong>
    </section>
  );
}

function PluginTitle({ plugin }: { plugin: PluginDescriptor }) {
  const locale = languageLocale();
  return (
    <div className="stacked-cell">
      <strong>{localizedPluginName(plugin, locale)}</strong>
      <span>{plugin.id} · {plugin.version || tx("内置")}</span>
    </div>
  );
}

function CapabilityList({ plugin }: { plugin: PluginDescriptor }) {
  const visible = plugin.capabilities.slice(0, 6);
  const remaining = plugin.capabilities.length - visible.length;
  return (
    <div className="tag-list">
      {visible.map((capability, index) => (
        <span className="tag" key={`${plugin.id}:${index}:${capability.kind}:${capability.subject ?? ""}:${capability.name}:${capability.value ?? ""}`}>
          {capability.subject ? `${capability.subject}:` : ""}{capability.name}
        </span>
      ))}
      {remaining > 0 ? <span className="tag">+{remaining}</span> : null}
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

function emptyBackgroundJobDraft(job: PluginBackgroundJobDescriptor): PluginBackgroundJobDraft {
  return { values: pluginActionInputDefaults(job), busy: false, error: "", result: "" };
}

function pluginSourceLabel(source: string) {
  if (source === "built_in") return tx("内置");
  if (source === "marketplace") return tx("插件市场");
  if (source === "local_file") return tx("本地文件");
  return source;
}

function mandatoryLabel(mandatory: boolean) {
  return mandatory ? tx("强制") : tx("可选");
}
