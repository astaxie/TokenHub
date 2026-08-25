import { Play } from "lucide-react";
import { useMemo, useState } from "react";
import { type AdminUIContribution, type ApiContext, type PluginActionDescriptor, type Provider, type ProviderResource } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";

type PanelState = {
  resourceID: string;
  busy: boolean;
  error: string;
  result: string;
};

export function ProviderPluginPanels({
  api,
  provider,
  resources,
  contributions,
  actions,
}: {
  api: ApiContext;
  provider: Provider;
  resources: ProviderResource[];
  contributions: AdminUIContribution[];
  actions: PluginActionDescriptor[];
}) {
  const panels = useMemo(
    () => contributions.filter((contribution) =>
      contribution.slot === "provider.resource.panel" &&
      (contribution.provider_types?.length ? contribution.provider_types.includes(provider.type) : true) &&
      contribution.action,
    ),
    [contributions, provider.type],
  );
  const actionKeys = useMemo(() => new Set(actions.map((action) => actionKey(action.plugin_id, action.action_id))), [actions]);
  const activeResources = resources.filter((resource) => resource.status === "active");
  const [states, setStates] = useState<Record<string, PanelState>>({});
  if (panels.length === 0) return null;

  function stateFor(panel: AdminUIContribution): PanelState {
    return states[contributionKey(panel)] ?? defaultPanelState(activeResources, resources);
  }

  function updateState(panel: AdminUIContribution, patch: Partial<PanelState>) {
    setStates((current) => {
      const key = contributionKey(panel);
      return { ...current, [key]: { ...(current[key] ?? defaultPanelState(activeResources, resources)), ...patch } };
    });
  }

  async function runPanel(panel: AdminUIContribution) {
    const state = stateFor(panel);
    if (!panel.action || !state.resourceID) return;
    updateState(panel, { busy: true, error: "", result: "" });
    try {
      const resp = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(panel.plugin_id)}/actions/${encodeURIComponent(panel.action)}`, {
        method: "POST",
        body: JSON.stringify({ resource_id: state.resourceID, refresh: true }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("插件面板执行失败")));
      const payload = await resp.json();
      updateState(panel, { busy: false, result: JSON.stringify(redactPanelResult(payload), null, 2) });
    } catch (err) {
      updateState(panel, { busy: false, error: err instanceof Error ? err.message : tx("插件面板执行失败") });
    }
  }

  return (
    <section className="provider-quota-panel provider-plugin-panels">
      <div className="wizard-panel-head">
        <h3>{tx("插件面板")}</h3>
      </div>
      <div className="provider-quota-list">
        {panels.map((panel) => {
          const state = stateFor(panel);
          const registered = actionKeys.has(actionKey(panel.plugin_id, panel.action));
          return (
            <article className="provider-quota-card" key={contributionKey(panel)}>
              <div className="provider-quota-card-head">
                <div className="provider-quota-account">
                  <span>{panel.title || panel.id}</span>
                  <strong>{panel.plugin_id}</strong>
                </div>
                <div className="provider-quota-card-actions">
                  <select disabled={state.busy || resources.length === 0} value={state.resourceID} onChange={(event) => updateState(panel, { resourceID: event.target.value, error: "", result: "" })}>
                    {resources.map((resource) => <option key={resource.id} value={resource.id}>{resource.name || resource.id}</option>)}
                  </select>
                  <button className="secondary-button" disabled={state.busy || !registered || !state.resourceID} onClick={() => void runPanel(panel)} type="button">
                    <Play size={14} />
                    {tx(state.busy ? "执行中" : "执行插件面板")}
                  </button>
                </div>
              </div>
              {!registered ? <p className="provider-quota-error">{tx("该插件动作尚未注册。")}</p> : null}
              {state.error ? <p className="provider-quota-error">{state.error}</p> : null}
              {state.result ? <pre className="plugin-action-result">{state.result}</pre> : null}
            </article>
          );
        })}
      </div>
    </section>
  );
}

function defaultPanelState(activeResources: ProviderResource[], resources: ProviderResource[]): PanelState {
  return { resourceID: activeResources[0]?.id ?? resources[0]?.id ?? "", busy: false, error: "", result: "" };
}

function contributionKey(panel: AdminUIContribution) {
  return `${panel.plugin_id}:${panel.id}:${panel.action ?? ""}`;
}

function actionKey(pluginID: string, actionID?: string) {
  return `${pluginID}:${actionID ?? ""}`;
}

function redactPanelResult(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactPanelResult);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) =>
    sensitivePanelResultKey(key) ? [key, "[redacted]"] : [key, redactPanelResult(child)],
  ));
}

function sensitivePanelResultKey(key: string) {
  const normalized = key.toLowerCase();
  return normalized.includes("token") || normalized.includes("secret") || normalized.includes("credential") || normalized.includes("api_key");
}
