import { Boxes, Layers3, PlugZap, ShieldCheck } from "lucide-react";
import { type ReactNode } from "react";
import { type AppData, type PluginDescriptor } from "../core/types";
import { tx } from "../i18n/runtime";
import { StatusPill } from "../shared/ui";

export function PluginsView({ data }: { data: AppData }) {
  const plugins = data.plugins;
  const providerCapabilities = plugins.reduce(
    (count, plugin) => count + plugin.capabilities.filter((capability) => capability.kind === "provider").length,
    0,
  );
  const gatewayPlugins = plugins.filter((plugin) => plugin.placements.includes("gateway_chain")).length;
  const uiPlugins = plugins.filter((plugin) => plugin.placements.includes("presentation") || plugin.kinds.includes("admin_ui") || plugin.kinds.includes("sim")).length;
  const uiContributions = data.pluginUI;

  return (
    <div className="plugins-view">
      <div className="metric-grid">
        <PluginMetric icon={<PlugZap size={18} />} label={tx("已注册插件")} value={plugins.length} />
        <PluginMetric icon={<Boxes size={18} />} label={tx("Provider 能力")} value={providerCapabilities} />
        <PluginMetric icon={<Layers3 size={18} />} label={tx("链路插件")} value={gatewayPlugins} />
        <PluginMetric icon={<ShieldCheck size={18} />} label={tx("界面插件")} value={uiPlugins} />
      </div>

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
                    <th>{tx("类型")}</th>
                    <th>{tx("运行位置")}</th>
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
                      <td>{plugin.kinds.map(pluginKindLabel).join(", ")}</td>
                      <td>{plugin.placements.map(pluginPlacementLabel).join(", ")}</td>
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
                      <td>{contribution.action || "-"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      </section>
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

function pluginSourceLabel(source: string) {
  if (source === "built_in") return tx("内置");
  if (source === "marketplace") return tx("插件市场");
  if (source === "local_file") return tx("本地文件");
  return source;
}

function mandatoryLabel(mandatory: boolean) {
  return mandatory ? tx("强制") : tx("可选");
}
