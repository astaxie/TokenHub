import { Boxes, Clock3, Download, ExternalLink, GitBranch, Layers3, MousePointerClick, Palette, PanelsTopLeft, PlugZap, Power, PowerOff, Save, ShieldCheck, Trash2 } from "lucide-react";
import { type FormEvent, type ReactNode, Fragment, useEffect, useMemo, useState } from "react";
import { type ApiContext, type AppData, type PluginActionDescriptor, type PluginBackgroundJobDescriptor, type PluginDescriptor, type PluginMarketplacePlugin } from "../core/types";
import { pluginActionInputDefaults, pluginActionKey, pluginActionPayload, pluginBackgroundJobKey, pluginBackgroundJobPayload, redactPluginActionResult } from "../domain/plugin-actions";
import { pluginManagerDisplayState, type PluginManagerDisplayState } from "../domain/plugin-manager";
import { pluginMarketplaceDisplay, type PluginMarketplaceDisplayState } from "../domain/plugin-marketplace";
import { simRegistryFromPlugins, type SIMRegistry, type SIMShellLayout, type SIMThemeTokens } from "../domain/sim-registry";
import { resolveSIMSelection, type SIMSelectionPreference } from "../domain/sim-selection";
import { languageLocale, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { StatusPill } from "../shared/ui";
import { PluginActionRunner, PluginBackgroundJobRunner } from "./plugin-action-runner";

type ActionDraft = {
  values: Record<string, string | boolean>;
  busy: boolean;
  error: string;
  result: string;
};

type PluginStateDraft = {
  status?: string;
  busy?: boolean;
  error?: string;
  restartRequired?: boolean;
};

type PluginInstallDraft = {
  downloadURL: string;
  checksumSHA256: string;
  replace: boolean;
  enable: boolean;
  busy: boolean;
  error: string;
  result: string;
};

type PluginUpdateDraft = {
  busy: boolean;
  error: string;
  result: string;
};

type PluginDeleteDraft = {
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
  const [actionDrafts, setActionDrafts] = useState<Record<string, ActionDraft>>({});
  const [pluginStateDrafts, setPluginStateDrafts] = useState<Record<string, PluginStateDraft>>({});
  const [pluginUpdateDrafts, setPluginUpdateDrafts] = useState<Record<string, PluginUpdateDraft>>({});
  const [pluginDeleteDrafts, setPluginDeleteDrafts] = useState<Record<string, PluginDeleteDraft>>({});
  const [backgroundJobDrafts, setBackgroundJobDrafts] = useState<Record<string, PluginBackgroundJobDraft>>({});
  const [marketplaceDrafts, setMarketplaceDrafts] = useState<Record<string, PluginInstallDraft>>({});
  const [installDraft, setInstallDraft] = useState<PluginInstallDraft>(emptyInstallDraft());
  const simRegistry = useMemo(() => simRegistryFromPlugins(plugins), [plugins]);
  const simSelection = useMemo(
    () => resolveSIMSelection({ plugins, preference: simSelectionPreference, themeMode: theme }),
    [plugins, simSelectionPreference, theme],
  );
  const [simSelectionDraft, setSIMSelectionDraft] = useState<SIMSelectionPreference>(simSelection.preference);
  const providerCapabilities = plugins.reduce(
    (count, plugin) => count + (plugin.capabilities ?? []).filter((capability) => capability.kind === "provider").length,
    0,
  );
  const gatewayPlugins = plugins.filter((plugin) => plugin.placements?.includes("gateway_chain")).length;
  const uiPlugins = plugins.filter((plugin) => plugin.placements?.includes("presentation") || plugin.kinds?.includes("admin_ui") || plugin.kinds?.includes("sim")).length;
  const uiContributions = data.pluginUI;
  const pluginActions = data.pluginActions;
  const backgroundJobs = data.pluginBackgroundJobs;
  const themeContributions = uiContributions.filter((contribution) => contribution.slot === "theme.tokens");
  const layoutContributions = uiContributions.filter((contribution) => contribution.slot === "layout.preset");
  const simPlugins = useMemo(() => simSelectionPlugins(plugins, simRegistry), [plugins, simRegistry]);
  const themeOptions = useMemo(() => simSelectionThemeOptions(simRegistry.themeTokens, theme), [simRegistry.themeTokens, theme]);
  const layoutOptions = useMemo(() => simSelectionLayoutOptions(simRegistry.shellLayouts), [simRegistry.shellLayouts]);
  const backgroundRuns = new Map(data.pluginBackgroundRuns.map((run) => [pluginBackgroundJobKey(run.plugin_id, run.job_id), run]));
  const pluginActionKeys = new Set(pluginActions.map((action) => pluginActionKey(action.plugin_id, action.action_id)));
  const marketplaceEntries = data.pluginMarketplace.map((item) => ({
    item,
    display: pluginMarketplaceDisplay(item, { locale: languageLocale() }),
  }));
  const activeSIMPlugin = simPlugins.find((plugin) => plugin.id === simSelection.activeSIMPluginID);
  const actionDraft = (action: PluginActionDescriptor) => actionDrafts[pluginActionKey(action.plugin_id, action.action_id)] ?? emptyActionDraft(action);
  const pluginStateDraft = (plugin: PluginDescriptor) => pluginStateDrafts[plugin.id] ?? {};
  const pluginUpdateDraft = (plugin: PluginDescriptor) => pluginUpdateDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const pluginDeleteDraft = (plugin: PluginDescriptor) => pluginDeleteDrafts[plugin.id] ?? { busy: false, error: "", result: "" };
  const marketplaceInstallDraft = (plugin: PluginDescriptor) => marketplaceDrafts[plugin.id] ?? emptyInstallDraft();
  const backgroundJobDraft = (job: PluginBackgroundJobDescriptor) => backgroundJobDrafts[pluginBackgroundJobKey(job.plugin_id, job.job_id)] ?? emptyBackgroundJobDraft(job);

  useEffect(() => {
    setSIMSelectionDraft(simSelection.preference);
  }, [
    simSelection.preference,
    simSelection.preference.layoutID,
    simSelection.preference.layoutKey,
    simSelection.preference.simPluginID,
    simSelection.preference.themeID,
    simSelection.preference.themeKey,
  ]);

  function updateActionValue(action: PluginActionDescriptor, field: string, value: string | boolean) {
    const key = pluginActionKey(action.plugin_id, action.action_id);
    setActionDrafts((drafts) => ({
      ...drafts,
      [key]: {
        ...emptyActionDraft(action),
        ...drafts[key],
        values: { ...(drafts[key]?.values ?? pluginActionInputDefaults(action)), [field]: value },
        error: "",
      },
    }));
  }

  async function executeAction(action: PluginActionDescriptor, event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const key = pluginActionKey(action.plugin_id, action.action_id);
    const draft = actionDraft(action);
    setActionDrafts((drafts) => ({ ...drafts, [key]: { ...draft, busy: true, error: "", result: "" } }));
    try {
      const response = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(action.plugin_id)}/actions/${encodeURIComponent(action.action_id)}`, {
        method: "POST",
        body: JSON.stringify(pluginActionPayload(action, draft.values)),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("执行插件动作")));
      const payload = await response.json();
      setActionDrafts((drafts) => ({ ...drafts, [key]: { ...draft, busy: false, error: "", result: JSON.stringify(redactPluginActionResult(payload), null, 2) } }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setActionDrafts((drafts) => ({
        ...drafts,
        [key]: { ...draft, busy: false, error: reason instanceof Error ? reason.message : tx("执行插件动作失败"), result: "" },
      }));
    }
  }

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
        body: JSON.stringify({
          download_url: installDraft.downloadURL,
          checksum_sha256: installDraft.checksumSHA256,
          replace: installDraft.replace,
          enable: installDraft.enable,
        }),
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

  async function installMarketplacePlugin(item: PluginMarketplacePlugin) {
    const plugin = item.plugin;
    const distribution = plugin.distribution;
    const draft = marketplaceDrafts[plugin.id] ?? emptyInstallDraft();
    setMarketplaceDrafts((drafts) => ({
      ...drafts,
      [plugin.id]: { ...draft, busy: true, error: "", result: "" },
    }));
    try {
      if (!distribution?.download_url || !distribution.checksum_sha256) {
        throw new Error(tx("无下载来源"));
      }
      const response = await adminFetch(api, "/api/admin/plugins/install", {
        method: "POST",
        body: JSON.stringify({
          download_url: distribution.download_url,
          checksum_sha256: distribution.checksum_sha256,
          replace: false,
          enable: false,
        }),
      });
      if (!response.ok) throw new Error(await readAdminError(response, tx("安装插件")));
      const payload = await response.json() as { data?: { plugin?: { id?: string }; restart_required?: boolean } };
      const pluginID = payload.data?.plugin?.id ?? plugin.id;
      setMarketplaceDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          ...draft,
          busy: false,
          error: "",
          result: `${pluginID} · ${payload.data?.restart_required ? tx("插件安装完成，重启后生效") : tx("插件安装完成")}`,
        },
      }));
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      setMarketplaceDrafts((drafts) => ({
        ...drafts,
        [plugin.id]: {
          ...draft,
          busy: false,
          error: reason instanceof Error ? reason.message : tx("安装插件失败"),
          result: "",
        },
      }));
    }
  }

  function applySIMSelection(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    onSIMSelectionPreferenceChange?.(simSelectionDraft);
  }

  return (
    <div className="plugins-view">
      <div className="metric-grid">
        <PluginMetric icon={<PlugZap size={18} />} label={tx("已注册插件")} value={plugins.length} />
        <PluginMetric icon={<Boxes size={18} />} label={tx("Provider 能力")} value={providerCapabilities} />
        <PluginMetric icon={<Layers3 size={18} />} label={tx("链路插件")} value={gatewayPlugins} />
        <PluginMetric icon={<ShieldCheck size={18} />} label={tx("界面插件")} value={uiPlugins} />
        <PluginMetric icon={<Palette size={18} />} label={tx("主题贡献")} value={themeContributions.length} />
        <PluginMetric icon={<PanelsTopLeft size={18} />} label={tx("布局预设")} value={layoutContributions.length} />
        <PluginMetric icon={<MousePointerClick size={18} />} label={tx("插件动作")} value={pluginActions.length} />
        <PluginMetric icon={<Clock3 size={18} />} label={tx("后台任务")} value={backgroundJobs.length} />
      </div>

      <section className="section" data-plugin-manager-section="install">
        <div className="section-header">
          <h2>{tx("安装插件包")}</h2>
        </div>
        <div className="section-body">
          <form className="plugin-action-runner" onSubmit={installPlugin}>
            <label className="plugin-action-field">
              <span>{tx("下载 URL")}</span>
              <input
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setInstallDraft((draft) => ({ ...draft, downloadURL: value }));
                }}
                required
                type="url"
                value={installDraft.downloadURL}
              />
            </label>
            <label className="plugin-action-field">
              <span>{tx("SHA-256 校验")}</span>
              <input
                onChange={(event) => {
                  const value = event.currentTarget.value;
                  setInstallDraft((draft) => ({ ...draft, checksumSHA256: value }));
                }}
                required
                value={installDraft.checksumSHA256}
              />
            </label>
            <label className="plugin-action-field">
              <span>{tx("允许替换")}</span>
              <input
                checked={installDraft.replace}
                onChange={(event) => {
                  const checked = event.currentTarget.checked;
                  setInstallDraft((draft) => ({ ...draft, replace: checked }));
                }}
                type="checkbox"
              />
            </label>
            <label className="plugin-action-field">
              <span>{tx("安装后启用")}</span>
              <input
                checked={installDraft.enable}
                onChange={(event) => {
                  const checked = event.currentTarget.checked;
                  setInstallDraft((draft) => ({ ...draft, enable: checked }));
                }}
                type="checkbox"
              />
            </label>
            <button className="secondary-button plugin-action-button" disabled={installDraft.busy} type="submit">
              <Download size={14} />
              <span>{tx(installDraft.busy ? "安装中" : "安装插件")}</span>
            </button>
            {installDraft.error ? <p className="provider-quota-error">{installDraft.error}</p> : null}
            {installDraft.result ? <p className="empty-state">{installDraft.result}</p> : null}
          </form>
        </div>
      </section>

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
                          <PluginLifecycleControl plugin={plugin} lifecycle={lifecycle} draft={pluginStateDraft(plugin)} onUpdate={updatePluginState} />
                        </td>
                        <td>{plugin.kinds.map(pluginKindLabel).join(", ")}</td>
                        <td>{plugin.placements.map(pluginPlacementLabel).join(", ")}</td>
                        <td>
                          <DistributionMetadata
                            lifecycle={lifecycle}
                            plugin={plugin}
                            draft={pluginUpdateDraft(plugin)}
                            onUpdate={updatePlugin}
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

      <section className="section" data-plugin-manager-section="marketplace">
        <div className="section-header">
          <h2>{tx("插件市场")}</h2>
        </div>
        <div className="section-body">
          {data.pluginMarketplaceSourceURL ? (
            <p className="muted">
              {tx("市场")} <a href={data.pluginMarketplaceSourceURL} rel="noreferrer" target="_blank">{data.pluginMarketplaceSourceURL}</a>
            </p>
          ) : null}
          {data.pluginMarketplaceError ? <p className="provider-quota-error">{data.pluginMarketplaceError}</p> : null}
          {!data.pluginMarketplaceAvailable ? (
            <p className="empty-state">{tx("暂无插件")}</p>
          ) : data.pluginMarketplace.length === 0 ? (
            <p className="empty-state">{tx("暂无插件")}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{tx("插件")}</th>
                    <th>{tx("状态")}</th>
                    <th>{tx("分发")}</th>
                    <th>{tx("安装")}</th>
                  </tr>
                </thead>
                <tbody>
                  {marketplaceEntries.map(({ item, display }) => (
                    <Fragment key={item.plugin.id}>
                      <tr>
                        <td>
                          <PluginTitle plugin={item.plugin} />
                        </td>
                        <td>
                          <MarketplaceStatusCell item={item} display={display} />
                        </td>
                        <td>
                          <DistributionMetadata plugin={item.plugin} draft={marketplaceInstallDraft(item.plugin)} onUpdate={updatePlugin} showUpdate={false} />
                        </td>
                        <td>
                          <MarketplaceInstallControl
                            item={item}
                            draft={marketplaceInstallDraft(item.plugin)}
                            updateDraft={pluginUpdateDraft(item.plugin)}
                            onInstall={installMarketplacePlugin}
                            onUpdate={updatePlugin}
                          />
                        </td>
                      </tr>
                      <tr>
                        <td colSpan={4}>
                          <MarketplaceDetails display={display} />
                        </td>
                      </tr>
                    </Fragment>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>

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

      <section className="section" data-plugin-manager-section="ui-contributions">
        <div className="section-header">
          <h2>{tx("界面贡献清单")}</h2>
        </div>
        <div className="section-body">
          {uiContributions.length === 0 ? (
            <p className="empty-state">{tx("暂无界面贡献")}</p>
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
                          <strong>{contribution.title || contribution.id}</strong>
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
      </section>

      <section className="section" data-plugin-manager-section="sim-contributions">
        <div className="section-header">
          <h2>{tx("SIM 与主题贡献")}</h2>
        </div>
        <div className="section-body">
          {themeContributions.length === 0 && layoutContributions.length === 0 ? (
            <p className="empty-state">{tx("暂无 SIM 贡献")}</p>
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
                          <strong>{contribution.title || contribution.id}</strong>
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

      <section className="section" data-plugin-manager-section="sim-selection">
        <div className="section-header">
          <h2>{tx("SIM 选择面板")}</h2>
        </div>
        <div className="section-body">
          {simPlugins.length === 0 && themeOptions.length === 0 && layoutOptions.length === 0 ? (
            <p className="empty-state">{tx("暂无可选 SIM")}</p>
          ) : (
            <form className="plugin-action-runner" onSubmit={applySIMSelection}>
              <div className="stacked-cell">
                <strong>{tx("当前生效")}</strong>
                <span>
                  {activeSIMPlugin ? activeSIMPlugin.name || activeSIMPlugin.id : tx("自动选择")} ·{" "}
                  {simSelection.theme.capability ? simSelection.theme.capability.title : tx("自动选择")} ·{" "}
                  {simSelection.layout.capability ? simSelection.layout.capability.title : tx("自动选择")}
                </span>
              </div>

              <label className="plugin-action-field">
                <span>{tx("SIM 插件")}</span>
                <select
                  onChange={(event) => {
                    const nextPluginID = event.currentTarget.value;
                    setSIMSelectionDraft((draft) => {
                      const nextTheme = preferredSIMTheme(themeOptions, nextPluginID, draft.themeKey);
                      const nextLayout = preferredSIMLayout(layoutOptions, nextPluginID, draft.layoutKey);
                      return {
                        simPluginID: nextPluginID,
                        themeKey: nextTheme?.key ?? "",
                        themeID: nextTheme?.id ?? "",
                        layoutKey: nextLayout?.key ?? "",
                        layoutID: nextLayout?.id ?? "",
                      };
                    });
                  }}
                  value={simSelectionDraft.simPluginID}
                >
                  <option value="">{tx("自动选择")}</option>
                  {simPlugins.map((plugin) => (
                    <option key={plugin.id} value={plugin.id}>
                      {plugin.name || plugin.id}{plugin.version ? ` · ${plugin.version}` : ""}
                    </option>
                  ))}
                </select>
              </label>

              <label className="plugin-action-field">
                <span>{tx("主题 Token")}</span>
                <select
                  onChange={(event) => {
                    const nextTheme = themeOptions.find((option) => option.key === event.currentTarget.value);
                    setSIMSelectionDraft((draft) => ({
                      ...draft,
                      themeKey: nextTheme?.key ?? "",
                      themeID: nextTheme?.id ?? "",
                    }));
                  }}
                  value={simSelectionDraft.themeKey}
                >
                  <option value="">{tx("自动选择")}</option>
                  {themeOptions.map((option) => (
                    <option key={option.key} value={option.key}>
                      {option.title} · {option.pluginName}
                    </option>
                  ))}
                </select>
              </label>

              <label className="plugin-action-field">
                <span>{tx("布局预设")}</span>
                <select
                  onChange={(event) => {
                    const nextLayout = layoutOptions.find((option) => option.key === event.currentTarget.value);
                    setSIMSelectionDraft((draft) => ({
                      ...draft,
                      layoutKey: nextLayout?.key ?? "",
                      layoutID: nextLayout?.id ?? "",
                    }));
                  }}
                  value={simSelectionDraft.layoutKey}
                >
                  <option value="">{tx("自动选择")}</option>
                  {layoutOptions.map((option) => (
                    <option key={option.key} value={option.key}>
                      {option.title} · {option.pluginName}
                    </option>
                  ))}
                </select>
              </label>

              <button className="secondary-button plugin-action-button" disabled={!onSIMSelectionPreferenceChange} type="submit">
                <Save size={14} />
                <span>{tx("应用选择")}</span>
              </button>
            </form>
          )}
        </div>
      </section>

      <section className="section" data-plugin-manager-section="actions">
        <div className="section-header">
          <h2>{tx("动作清单")}</h2>
        </div>
        <div className="section-body">
          {pluginActions.length === 0 ? (
            <p className="empty-state">{tx("暂无插件动作")}</p>
          ) : (
            <div className="table-wrap">
              <table>
                <thead>
                  <tr>
                    <th>{tx("插件")}</th>
                    <th>{tx("动作 ID")}</th>
                    <th>{tx("动作类型")}</th>
                    <th>{tx("能力标识")}</th>
                    <th>{tx("标题")}</th>
                    <th>{tx("执行")}</th>
                  </tr>
                </thead>
                <tbody>
                  {pluginActions.map((action) => (
                    <tr key={`${action.plugin_id}:${action.action_id}`}>
                      <td>{action.plugin_id}</td>
                      <td>{action.action_id}</td>
                      <td>{pluginActionKindLabel(action.kind)}</td>
                      <td>{action.capability || action.subject || "-"}</td>
                      <td>{action.title || "-"}</td>
                      <td>
                        <PluginActionRunner
                          action={action}
                          draft={actionDraft(action)}
                          labels={{
                            submit: tx("执行"),
                            submitting: tx("执行中"),
                            unsupportedSchema: tx("暂不支持复杂输入 Schema"),
                          }}
                          onChange={updateActionValue}
                          onSubmit={executeAction}
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>

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
                            <span>{job.title || "-"}</span>
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
  return (
    <div className="stacked-cell">
      <strong>{plugin.name || plugin.id}</strong>
      <span>{plugin.id} · {plugin.version || tx("内置")}</span>
    </div>
  );
}

function PluginLifecycleControl({
  plugin,
  lifecycle,
  draft,
  onUpdate,
}: {
  plugin: PluginDescriptor;
  lifecycle: PluginManagerDisplayState;
  draft: PluginStateDraft;
  onUpdate: (plugin: PluginDescriptor, status: string) => void;
}) {
  const effectiveLifecycle = draft.status ? pluginManagerDisplayState({ plugin: { ...plugin, status: draft.status } }) : lifecycle;
  const status = effectiveLifecycle.status;
  const nextStatus = status === "disabled" ? "enabled" : "disabled";
  const canUpdate = plugin.source !== "built_in" && !effectiveLifecycle.mandatory && (status === "enabled" || status === "disabled");
  return (
    <div className="stacked-cell" data-plugin-manager-control="lifecycle">
      <StatusPill status={effectiveLifecycle.pillStatus} label={tx(effectiveLifecycle.labelKey)} />
      {canUpdate ? (
        <button
          className="secondary-button compact-button"
          disabled={Boolean(draft.busy)}
          onClick={() => onUpdate(plugin, nextStatus)}
          title={tx(nextStatus === "enabled" ? "启用插件" : "禁用插件")}
          type="button"
        >
          {nextStatus === "enabled" ? <Power size={14} /> : <PowerOff size={14} />}
          <span>{tx(draft.busy ? "更新中" : nextStatus === "enabled" ? "启用" : "禁用")}</span>
        </button>
      ) : null}
      {draft.restartRequired || effectiveLifecycle.restartRequired ? <span>{tx("重启后生效")}</span> : null}
      {draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
    </div>
  );
}

function CapabilityList({ plugin }: { plugin: PluginDescriptor }) {
  const visible = plugin.capabilities.slice(0, 6);
  const remaining = plugin.capabilities.length - visible.length;
  return (
    <div className="tag-list">
      {visible.map((capability) => (
        <span className="tag" key={`${capability.kind}:${capability.subject ?? ""}:${capability.name}`}>
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
  showUpdate = true,
}: {
  plugin: PluginDescriptor;
  lifecycle?: PluginManagerDisplayState;
  draft: PluginUpdateDraft;
  onUpdate: (plugin: PluginDescriptor) => void;
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
      {showUpdate && draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      {showUpdate && draft.result ? <span>{draft.result}</span> : null}
      <span>{distribution.license ? `${tx("许可证")} ${distribution.license}` : tx("未声明许可证")}</span>
      {distribution.checksum_sha256 ? <span>{tx("SHA-256")} {shortChecksum(distribution.checksum_sha256)}</span> : null}
    </div>
  );
}

function MarketplaceStatusCell({
  item,
  display,
}: {
  item: PluginMarketplacePlugin;
  display: PluginMarketplaceDisplayState;
}) {
  return (
    <div className="stacked-cell">
      <StatusPill status={item.installed ? "enabled" : "disabled"} label={item.installed ? tx("已安装") : tx("未安装")} />
      {item.installed_version ? <span>{tx("已安装版本")} {item.installed_version}</span> : null}
      {item.update_available ? <span>{tx("更新可用")}</span> : null}
      <StatusPill status={statusPillStatus(display.compatibility.tone)} label={tx(display.compatibility.labelKey)} />
      {display.compatibility.reasonCode ? <span>{display.compatibility.reasonCode}</span> : null}
      {display.compatibility.badges.length > 0 ? (
        <div className="tag-list">
          {display.compatibility.badges.map((badge) => (
            <a className="tag" href={badge.url || undefined} key={`${display.id}:${badge.id}`} rel="noreferrer" target="_blank">
              <StatusPill status={statusPillStatus(badge.tone)} label={badge.label} />
            </a>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function MarketplaceDetails({ display }: { display: PluginMarketplaceDisplayState }) {
  return (
    <div className="stacked-cell">
      {display.summary ? <span>{display.summary}</span> : null}
      {display.categories.length > 0 ? (
        <div className="tag-list">
          {display.categories.map((category) => (
            <span className="tag" key={`${display.id}:${category}`}>{category}</span>
          ))}
        </div>
      ) : null}
      <div className="tag-list">
        <StatusPill status={display.publisher.verified ? "ok" : "pending"} label={tx(display.publisher.verificationLabelKey)} />
        <span className="tag">{display.publisher.name}</span>
        <StatusPill status={statusPillStatus(display.trust.tone)} label={tx(display.trust.labelKey)} />
        {display.latestReleaseNote ? <span className="tag">{display.latestReleaseNote.version || display.latestReleaseNote.title}</span> : null}
      </div>
      {display.latestReleaseNote?.notes ? <span>{display.latestReleaseNote.notes}</span> : null}
      {display.screenshots.length > 0 ? (
        <div className="tag-list">
          {display.screenshots.slice(0, 3).map((screenshot) => (
            <a href={screenshot.url} key={`${display.id}:${screenshot.url}`} rel="noreferrer" target="_blank" title={screenshot.caption || screenshot.alt}>
              <img
                alt={screenshot.alt}
                height={54}
                src={screenshot.thumbnailURL}
                style={{ borderRadius: 4, display: "block", height: 54, objectFit: "cover", width: 96 }}
                width={96}
              />
            </a>
          ))}
        </div>
      ) : null}
      {display.advisories.length > 0 ? (
        <div className="tag-list">
          {display.advisories.map((advisory) => (
            <a className="tag" href={advisory.url || undefined} key={`${display.id}:${advisory.id}`} rel="noreferrer" target="_blank">
              <StatusPill status={advisory.tone} label={tx(advisory.labelKey)} />
              <span>{advisory.title}</span>
            </a>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function MarketplaceInstallControl({
  item,
  draft,
  updateDraft,
  onInstall,
  onUpdate,
}: {
  item: PluginMarketplacePlugin;
  draft: PluginInstallDraft;
  updateDraft: PluginUpdateDraft;
  onInstall: (item: PluginMarketplacePlugin) => void;
  onUpdate: (plugin: PluginDescriptor) => void;
}) {
  if (item.installed && !item.update_available) {
    return (
      <div className="stacked-cell">
        <StatusPill status="enabled" label={tx("已安装")} />
        {draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      </div>
    );
  }
  if (item.installed && item.update_available) {
    return (
      <div className="stacked-cell">
        <button
          className="secondary-button compact-button"
          disabled={updateDraft.busy}
          onClick={() => onUpdate(item.plugin)}
          type="button"
        >
          <Download size={13} />
          <span>{tx(updateDraft.busy ? "更新中" : "更新")}</span>
        </button>
        {updateDraft.error ? <span className="provider-quota-error">{updateDraft.error}</span> : null}
        {updateDraft.result ? <span>{updateDraft.result}</span> : null}
      </div>
    );
  }
  return (
    <div className="stacked-cell">
      <button
        className="secondary-button compact-button"
        disabled={draft.busy}
        onClick={() => onInstall(item)}
        type="button"
      >
        <Download size={13} />
        <span>{tx(draft.busy ? "安装中" : "安装插件")}</span>
      </button>
      {draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      {draft.result ? <span>{draft.result}</span> : null}
    </div>
  );
}

function PluginDeleteControl({
  plugin,
  lifecycle,
  draft,
  onDelete,
}: {
  plugin: PluginDescriptor;
  lifecycle: PluginManagerDisplayState;
  draft: PluginDeleteDraft;
  onDelete: (plugin: PluginDescriptor) => void;
}) {
  if (!lifecycle.actions.uninstall.available) {
    return (
      <div className="stacked-cell" data-plugin-manager-control="delete">
        <span className="muted">-</span>
      </div>
    );
  }
  return (
    <div className="stacked-cell" data-plugin-manager-control="delete">
      <button
        className="danger-button compact-button"
        disabled={draft.busy}
        onClick={() => onDelete(plugin)}
        title={tx("卸载插件")}
        type="button"
      >
        <Trash2 size={13} />
        <span>{tx(draft.busy ? "卸载中" : "卸载")}</span>
      </button>
      {draft.error ? <span className="provider-quota-error">{draft.error}</span> : null}
      {draft.result ? <span>{draft.result}</span> : null}
    </div>
  );
}

function shortChecksum(value: string) {
  return value.length > 16 ? `${value.slice(0, 12)}...${value.slice(-4)}` : value;
}

function statusPillStatus(tone: string) {
  if (tone === "warn") return "pending";
  return tone;
}

function pluginKindLabel(kind: string) {
  if (kind === "provider") return tx("Provider");
  if (kind === "admin_ui") return tx("Admin UI");
  if (kind === "sim") return tx("SIM");
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

function pluginActionKindLabel(kind: string) {
  if (kind === "read") return tx("读取");
  if (kind === "test") return tx("测试");
  if (kind === "mutate") return tx("变更");
  if (kind === "external_redirect") return tx("外部跳转");
  if (kind === "import_export") return tx("导入导出");
  return kind;
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

function simSelectionPlugins(plugins: readonly PluginDescriptor[], registry: SIMRegistry) {
  const capabilityPluginIDs = new Set([...registry.themeTokens, ...registry.shellLayouts].map((capability) => capability.pluginID));
  return plugins.flatMap((plugin) => {
    const hasSIMKind = plugin.kinds?.includes("sim");
    const hasSIMCapability = capabilityPluginIDs.has(plugin.id);
    if (!hasSIMKind && !hasSIMCapability) return [];
    return [{
      id: plugin.id,
      name: plugin.name || plugin.id,
      version: plugin.version || "",
    }];
  });
}

function simSelectionThemeOptions(themeTokens: readonly SIMThemeTokens[], theme: "light" | "dark") {
  return themeTokens
    .filter((token) => token.payload.mode === "all" || token.payload.mode === theme)
    .map((token) => ({
      key: token.key,
      id: token.id,
      title: token.title || token.id,
      pluginID: token.pluginID,
      pluginName: token.pluginName || token.pluginID,
      pluginVersion: token.pluginVersion || "",
    }));
}

function simSelectionLayoutOptions(layouts: readonly SIMShellLayout[]) {
  return layouts.map((layout) => ({
    key: layout.key,
    id: layout.id,
    title: layout.title || layout.id,
    pluginID: layout.pluginID,
    pluginName: layout.pluginName || layout.pluginID,
    pluginVersion: layout.pluginVersion || "",
  }));
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

function emptyActionDraft(action: PluginActionDescriptor): ActionDraft {
  return { values: pluginActionInputDefaults(action), busy: false, error: "", result: "" };
}

function emptyBackgroundJobDraft(job: PluginBackgroundJobDescriptor): PluginBackgroundJobDraft {
  return { values: pluginActionInputDefaults(job), busy: false, error: "", result: "" };
}

function emptyInstallDraft(): PluginInstallDraft {
  return { downloadURL: "", checksumSHA256: "", replace: false, enable: false, busy: false, error: "", result: "" };
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
