import { Boxes, Clock3, Download, ExternalLink, GitBranch, Layers3, MousePointerClick, Palette, PanelsTopLeft, Play, PlugZap, Power, PowerOff, ShieldCheck } from "lucide-react";
import { type FormEvent, type ReactNode, useState } from "react";
import { type ApiContext, type AppData, type PluginActionDescriptor, type PluginDescriptor } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { StatusPill } from "../shared/ui";

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

export function PluginsView({ api, data }: { api: ApiContext; data: AppData }) {
  const plugins = data.plugins;
  const [actionDrafts, setActionDrafts] = useState<Record<string, ActionDraft>>({});
  const [pluginStateDrafts, setPluginStateDrafts] = useState<Record<string, PluginStateDraft>>({});
  const [installDraft, setInstallDraft] = useState<PluginInstallDraft>(emptyInstallDraft());
  const providerCapabilities = plugins.reduce(
    (count, plugin) => count + plugin.capabilities.filter((capability) => capability.kind === "provider").length,
    0,
  );
  const gatewayPlugins = plugins.filter((plugin) => plugin.placements.includes("gateway_chain")).length;
  const uiPlugins = plugins.filter((plugin) => plugin.placements.includes("presentation") || plugin.kinds.includes("admin_ui") || plugin.kinds.includes("sim")).length;
  const uiContributions = data.pluginUI;
  const pluginActions = data.pluginActions;
  const backgroundJobs = data.pluginBackgroundJobs;
  const themeContributions = uiContributions.filter((contribution) => contribution.slot === "theme.tokens");
  const layoutContributions = uiContributions.filter((contribution) => contribution.slot === "layout.preset");
  const backgroundRuns = new Map(data.pluginBackgroundRuns.map((run) => [pluginBackgroundJobKey(run.plugin_id, run.job_id), run]));
  const pluginActionKeys = new Set(pluginActions.map((action) => pluginActionKey(action.plugin_id, action.action_id)));
  const actionDraft = (action: PluginActionDescriptor) => actionDrafts[pluginActionKey(action.plugin_id, action.action_id)] ?? emptyActionDraft(action);
  const pluginStateDraft = (plugin: PluginDescriptor) => pluginStateDrafts[plugin.id] ?? {};

  function updateActionValue(action: PluginActionDescriptor, field: string, value: string | boolean) {
    const key = pluginActionKey(action.plugin_id, action.action_id);
    setActionDrafts((drafts) => ({
      ...drafts,
      [key]: {
        ...emptyActionDraft(action),
        ...drafts[key],
        values: { ...(drafts[key]?.values ?? emptyActionValues(action)), [field]: value },
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
        body: JSON.stringify(actionPayload(action, draft.values)),
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

      <section className="section">
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

      <section className="section">
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
                  </tr>
                </thead>
                <tbody>
                  {plugins.map((plugin) => (
                    <tr key={plugin.id}>
                      <td>
                        <PluginTitle plugin={plugin} />
                      </td>
                      <td>
                        <StatusPill status={plugin.source} label={pluginSourceLabel(plugin.source)} />
                      </td>
                      <td>
                        <PluginStatusControl plugin={plugin} draft={pluginStateDraft(plugin)} onUpdate={updatePluginState} />
                      </td>
                      <td>{plugin.kinds.map(pluginKindLabel).join(", ")}</td>
                      <td>{plugin.placements.map(pluginPlacementLabel).join(", ")}</td>
                      <td>
                        <DistributionMetadata plugin={plugin} />
                      </td>
                      <td>
                        <CapabilityList plugin={plugin} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>

      <section className="section">
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

      <section className="section">
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

      <section className="section">
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

      <section className="section">
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

      <section className="section">
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

function PluginActionRunner({
  action,
  draft,
  onChange,
  onSubmit,
}: {
  action: PluginActionDescriptor;
  draft: ActionDraft;
  onChange: (action: PluginActionDescriptor, field: string, value: string | boolean) => void;
  onSubmit: (action: PluginActionDescriptor, event: FormEvent<HTMLFormElement>) => void;
}) {
  const fields = actionInputFields(action);
  const unsupported = action.input_schema && !actionInputSchemaSupported(action.input_schema);
  return (
    <form className="plugin-action-runner" onSubmit={(event) => onSubmit(action, event)}>
      {unsupported ? <p className="empty-state">{tx("暂不支持复杂输入 Schema")}</p> : null}
      {fields.map((field) => (
        <label className="plugin-action-field" key={field.name}>
          <span>{field.name}{field.required ? " *" : ""}</span>
          {field.type === "boolean" ? (
            <input
              checked={Boolean(draft.values[field.name])}
              onChange={(event) => onChange(action, field.name, event.currentTarget.checked)}
              type="checkbox"
            />
          ) : (
            <input
              onChange={(event) => onChange(action, field.name, event.currentTarget.value)}
              required={field.required}
              type={field.type === "number" || field.type === "integer" ? "number" : "text"}
              value={String(draft.values[field.name] ?? "")}
            />
          )}
        </label>
      ))}
      <button className="secondary-button plugin-action-button" disabled={draft.busy || Boolean(unsupported)} type="submit">
        <Play size={14} />
        <span>{tx(draft.busy ? "执行中" : "执行")}</span>
      </button>
      {draft.error ? <p className="provider-quota-error">{draft.error}</p> : null}
      {draft.result ? <pre className="plugin-action-result">{draft.result}</pre> : null}
    </form>
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

function pluginActionKey(pluginID: string, actionID?: string) {
  return `${pluginID}:${actionID ?? ""}`;
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

function PluginStatusControl({
  plugin,
  draft,
  onUpdate,
}: {
  plugin: PluginDescriptor;
  draft: PluginStateDraft;
  onUpdate: (plugin: PluginDescriptor, status: string) => void;
}) {
  const status = draft.status ?? plugin.status ?? "enabled";
  const nextStatus = status === "disabled" ? "enabled" : "disabled";
  const canUpdate = plugin.source !== "built_in";
  return (
    <div className="stacked-cell">
      <StatusPill status={status} label={pluginStatusLabel(status)} />
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
      {draft.restartRequired ? <span>{tx("重启后生效")}</span> : null}
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

function DistributionMetadata({ plugin }: { plugin: PluginDescriptor }) {
  const distribution = plugin.distribution;
  if (!distribution) return <span className="muted">{tx("未声明")}</span>;
  const candidates: Array<{ href?: string; label: string; icon: ReactNode }> = [
    { href: distribution.marketplace_url, label: tx("市场"), icon: <ExternalLink size={13} /> },
    { href: distribution.repository_url, label: tx("仓库"), icon: <GitBranch size={13} /> },
    { href: distribution.download_url, label: tx("下载"), icon: <Download size={13} /> },
    { href: distribution.homepage_url, label: tx("主页"), icon: <ExternalLink size={13} /> },
    { href: distribution.signature_url, label: tx("签名"), icon: <ShieldCheck size={13} /> },
  ];
  const links = candidates.flatMap((link) => (link.href ? [{ ...link, href: link.href }] : []));
  return (
    <div className="stacked-cell">
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

function pluginBackgroundJobKey(pluginID: string, jobID: string) {
  return `${pluginID}:${jobID}`;
}

type ActionInputField = {
  name: string;
  type: "string" | "boolean" | "number" | "integer";
  required: boolean;
};

function emptyActionDraft(action: PluginActionDescriptor): ActionDraft {
  return { values: emptyActionValues(action), busy: false, error: "", result: "" };
}

function emptyInstallDraft(): PluginInstallDraft {
  return { downloadURL: "", checksumSHA256: "", replace: false, enable: false, busy: false, error: "", result: "" };
}

function emptyActionValues(action: PluginActionDescriptor) {
  const values: Record<string, string | boolean> = {};
  for (const field of actionInputFields(action)) {
    values[field.name] = field.type === "boolean" ? false : "";
  }
  return values;
}

function actionInputFields(action: PluginActionDescriptor): ActionInputField[] {
  const schema = action.input_schema;
  if (!schema || schema.type !== "object") return [];
  const required = new Set(Array.isArray(schema.required) ? schema.required.filter((item): item is string => typeof item === "string") : []);
  const properties = schema.properties;
  if (!properties || typeof properties !== "object" || Array.isArray(properties)) return [];
  return Object.entries(properties).flatMap(([name, value]) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) return [];
    const type = (value as { type?: unknown }).type;
    if (type !== "string" && type !== "boolean" && type !== "number" && type !== "integer") return [];
    return [{ name, type, required: required.has(name) }];
  });
}

function actionInputSchemaSupported(schema: Record<string, unknown>) {
  if (schema.type !== "object") return false;
  const properties = schema.properties;
  if (!properties || typeof properties !== "object" || Array.isArray(properties)) return true;
  return Object.values(properties).every((value) => {
    if (!value || typeof value !== "object" || Array.isArray(value)) return false;
    const type = (value as { type?: unknown }).type;
    return type === "string" || type === "boolean" || type === "number" || type === "integer";
  });
}

function actionPayload(action: PluginActionDescriptor, values: Record<string, string | boolean>) {
  const payload: Record<string, string | number | boolean> = {};
  for (const field of actionInputFields(action)) {
    const value = values[field.name];
    if (field.type === "boolean") {
      payload[field.name] = Boolean(value);
      continue;
    }
    if (field.type === "number" || field.type === "integer") {
      if (value === "") continue;
      payload[field.name] = Number(value);
      continue;
    }
    if (typeof value === "string" && value !== "") {
      payload[field.name] = value;
    }
  }
  return payload;
}

function redactPluginActionResult(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactPluginActionResult);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) => {
    if (sensitivePluginResultKey(key)) return [key, "[redacted]"];
    return [key, redactPluginActionResult(child)];
  }));
}

function sensitivePluginResultKey(key: string) {
  const normalized = key.toLowerCase();
  return normalized === "access_token" || normalized === "refresh_token" || normalized === "id_token" || normalized.includes("secret") || normalized === "credentials" || normalized === "credential" || normalized === "credential_blob" || normalized === "api_key" || normalized.endsWith("_api_key");
}

function pluginSourceLabel(source: string) {
  if (source === "built_in") return tx("内置");
  if (source === "marketplace") return tx("插件市场");
  if (source === "local_file") return tx("本地文件");
  return source;
}

function pluginStatusLabel(status?: string) {
  if (!status || status === "enabled") return tx("已启用");
  if (status === "disabled") return tx("已禁用");
  return status;
}

function mandatoryLabel(mandatory: boolean) {
  return mandatory ? tx("强制") : tx("可选");
}
