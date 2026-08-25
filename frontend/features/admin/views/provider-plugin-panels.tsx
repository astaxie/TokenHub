import { Play } from "lucide-react";
import { useMemo, useState } from "react";
import { type AdminUIContribution, type ApiContext, type PluginActionDescriptor, type Provider, type ProviderResource } from "../core/types";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";

type PanelField = {
  name: string;
  type: "metric" | "text" | "code_viewer";
  label: string;
  source?: string;
  value?: string | number | boolean;
  format?: "compact" | "money_usd" | "number" | "percent" | string;
  help?: string;
};

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
    () => contributions
      .filter((contribution) =>
        contribution.slot === "provider.resource.panel" &&
        (contribution.provider_types?.length ? contribution.provider_types.includes(provider.type) : true),
      )
      .map((contribution) => ({ contribution, fields: providerPanelFields(contribution) }))
      .filter((panel) => panel.fields.length > 0 || Boolean(panel.contribution.action)),
    [contributions, provider.type],
  );
  const actionKeys = useMemo(() => new Set(actions.map((action) => actionKey(action.plugin_id, action.action_id))), [actions]);
  const activeResources = resources.filter((resource) => resource.status === "active");
  const [states, setStates] = useState<Record<string, PanelState>>({});
  if (panels.length === 0) return null;

  function stateFor(contribution: AdminUIContribution): PanelState {
    return states[contributionKey(contribution)] ?? defaultPanelState(activeResources, resources);
  }

  function updateState(contribution: AdminUIContribution, patch: Partial<PanelState>) {
    setStates((current) => {
      const key = contributionKey(contribution);
      return { ...current, [key]: { ...(current[key] ?? defaultPanelState(activeResources, resources)), ...patch } };
    });
  }

  async function runPanel(contribution: AdminUIContribution) {
    const state = stateFor(contribution);
    if (!contribution.action || !state.resourceID) return;
    updateState(contribution, { busy: true, error: "", result: "" });
    try {
      const resp = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(contribution.plugin_id)}/actions/${encodeURIComponent(contribution.action)}`, {
        method: "POST",
        body: JSON.stringify({ provider_id: provider.id, resource_id: state.resourceID, refresh: true }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("插件面板执行失败")));
      const payload = await resp.json();
      updateState(contribution, { busy: false, result: JSON.stringify(redactPanelResult(payload), null, 2) });
    } catch (err) {
      updateState(contribution, { busy: false, error: err instanceof Error ? err.message : tx("插件面板执行失败") });
    }
  }

  return (
    <section className="provider-quota-panel provider-plugin-panels">
      <div className="wizard-panel-head">
        <h3>{tx("插件面板")}</h3>
      </div>
      <div className="provider-quota-list">
        {panels.map(({ contribution, fields }) => {
          const state = stateFor(contribution);
          const selectedResource = resources.find((resource) => resource.id === state.resourceID);
          const registered = !contribution.action || actionKeys.has(actionKey(contribution.plugin_id, contribution.action));
          return (
            <article className="provider-quota-card" key={contributionKey(contribution)}>
              <div className="provider-quota-card-head">
                <div className="provider-quota-account">
                  <span>{contribution.title || contribution.id}</span>
                  <strong>{contribution.plugin_id}</strong>
                </div>
                <div className="provider-quota-card-actions">
                  {resources.length > 0 ? (
                    <select disabled={state.busy} value={state.resourceID} onChange={(event) => updateState(contribution, { resourceID: event.target.value, error: "", result: "" })}>
                      {resources.map((resource) => <option key={resource.id} value={resource.id}>{resource.name || resource.id}</option>)}
                    </select>
                  ) : null}
                  {contribution.action ? (
                    <button className="secondary-button" disabled={state.busy || !registered || !state.resourceID} onClick={() => void runPanel(contribution)} type="button">
                      <Play size={14} />
                      {tx(state.busy ? "执行中" : "执行插件面板")}
                    </button>
                  ) : null}
                </div>
              </div>
              {fields.length > 0 ? (
                <div className="system-settings-plugin-fields">
                  {fields.map((field) => (
                    <ProviderPanelFieldView
                      context={{ provider, resource: selectedResource, resources }}
                      field={field}
                      key={field.name}
                    />
                  ))}
                </div>
              ) : null}
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

function ProviderPanelFieldView({
  context,
  field,
}: {
  context: ProviderPanelContext;
  field: PanelField;
}) {
  const value = providerPanelFieldValue(context, field);
  if (field.type === "code_viewer") {
    return (
      <div className="system-settings-plugin-field code">
        <span>{field.label}</span>
        <pre>{value}</pre>
      </div>
    );
  }
  return (
    <div className={`system-settings-plugin-field ${field.type}`}>
      <span>{field.label}</span>
      <strong>{value}</strong>
      {field.help ? <small>{field.help}</small> : null}
    </div>
  );
}

export function providerPanelFields(contribution: AdminUIContribution): PanelField[] {
  const rawFields = contribution.schema?.fields;
  if (!Array.isArray(rawFields)) return [];
  return rawFields.flatMap((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
    const field = raw as Record<string, unknown>;
    const type = providerPanelFieldType(schemaString(field.type));
    const name = schemaString(field.name);
    if (!type || !name) return [];
    return [{
      name,
      type,
      label: schemaString(field.label) || name,
      source: schemaString(field.source),
      value: schemaScalar(field.value),
      format: schemaString(field.format),
      help: schemaString(field.help),
    }];
  });
}

type ProviderPanelContext = {
  provider: Provider;
  resource?: ProviderResource;
  resources: ProviderResource[];
};

export function providerPanelFieldValue(context: ProviderPanelContext, field: PanelField) {
  const rawValue = field.value ?? providerPanelSourceValue(context, field.source);
  if (rawValue === undefined || rawValue === null || rawValue === "") return "-";
  if (field.format === "money_usd") return `$${formatMoney(Number(rawValue) || 0)}`;
  if (field.format === "compact") return compactNumber(Number(rawValue) || 0);
  if (field.format === "percent") return `${formatNumber(Number(rawValue) || 0)}%`;
  if (typeof rawValue === "number") return formatNumber(rawValue);
  if (typeof rawValue === "boolean") return rawValue ? "true" : "false";
  if (field.type === "code_viewer" && typeof rawValue === "object") return JSON.stringify(rawValue, null, 2);
  return String(rawValue);
}

function providerPanelFieldType(type: string): PanelField["type"] | "" {
  switch (type) {
    case "metric":
    case "text":
    case "code_viewer":
      return type;
    default:
      return "";
  }
}

function providerPanelSourceValue(context: ProviderPanelContext, source?: string) {
  if (!source) return undefined;
  const segments = source.split(".").map((segment) => segment.trim()).filter(Boolean);
  let current: unknown = context;
  for (const segment of segments) {
    if (segment === "length" && Array.isArray(current)) {
      current = current.length;
      continue;
    }
    if (Array.isArray(current) && arrayIndexPattern.test(segment)) {
      current = current[Number(segment)];
      continue;
    }
    if (!current || typeof current !== "object" || Array.isArray(current)) return undefined;
    current = (current as Record<string, unknown>)[segment];
  }
  return current;
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

const arrayIndexPattern = /^\d+$/;

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaScalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : undefined;
}
