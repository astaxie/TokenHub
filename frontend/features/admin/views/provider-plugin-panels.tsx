import { Play } from "lucide-react";
import { useMemo, useState, type FormEvent } from "react";
import { type AdminUIContribution, type ApiContext, type PluginActionDescriptor, type Provider, type ProviderResource } from "../core/types";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { pluginActionInputDefaults, pluginActionKey, pluginActionPayload, redactPluginActionResult } from "../domain/plugin-actions";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
import { PluginActionRunner } from "./plugin-action-runner";
import { ProviderResourceSystemPromptTransformFields } from "./provider-editor-sections";

type PanelField = {
  name: string;
  type: "metric" | "text" | "code_viewer";
  label: string;
  source?: string;
  value?: string | number | boolean;
  format?: "compact" | "money_usd" | "number" | "percent" | string;
  help?: string;
};

type PanelLayout = "resource_system_prompt_transform";

type PanelState = {
  values: Record<string, string | boolean>;
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
  onSaved = noopSaved,
  handledContributionKeys = [],
}: {
  api: ApiContext;
  provider: Provider;
  resources: ProviderResource[];
  contributions: AdminUIContribution[];
  actions: PluginActionDescriptor[];
  onSaved?: () => Promise<void>;
  handledContributionKeys?: string[];
}) {
  const handledContributions = useMemo(() => new Set(handledContributionKeys), [handledContributionKeys]);
  const panels = useMemo(
    () => contributions
      .filter((contribution) =>
        contribution.slot === "provider.resource.panel" &&
        (contribution.provider_types?.length ? contribution.provider_types.includes(provider.type) : true),
      )
      .filter((contribution) => !handledContributions.has(providerPanelContributionKey(contribution)))
      .map((contribution) => ({ contribution, fields: providerPanelFields(contribution), layout: providerPanelLayout(contribution), resources: providerPanelResources(contribution, resources, provider.id) }))
      .filter((panel) => panel.layout || panel.fields.length > 0 || Boolean(panel.contribution.action)),
    [contributions, handledContributions, provider.id, provider.type, resources],
  );
  const layoutPanels = panels.filter((panel) => panel.layout);
  const genericPanels = panels.filter((panel) => !panel.layout);
  const actionMap = useMemo(() => new Map(actions.map((action) => [pluginActionKey(action.plugin_id, action.action_id), action] as const)), [actions]);
  const [states, setStates] = useState<Record<string, PanelState>>({});
  if (panels.length === 0) return null;

  function stateFor(contribution: AdminUIContribution, panelResources: ProviderResource[], action?: PluginActionDescriptor): PanelState {
    return states[providerPanelContributionKey(contribution)] ?? defaultPanelState(panelResources, action);
  }

  function updateState(contribution: AdminUIContribution, panelResources: ProviderResource[], patch: Partial<PanelState>, action?: PluginActionDescriptor) {
    setStates((current) => {
      const key = providerPanelContributionKey(contribution);
      const base = current[key] ?? defaultPanelState(panelResources, action);
      return {
        ...current,
        [key]: {
          ...base,
          ...patch,
          values: patch.values ? { ...base.values, ...patch.values } : base.values,
        },
      };
    });
  }

  function updatePanelValue(contribution: AdminUIContribution, panelResources: ProviderResource[], action: PluginActionDescriptor, field: string, value: string | boolean) {
    updateState(contribution, panelResources, { values: { [field]: value }, error: "" }, action);
  }

  async function runPanel(contribution: AdminUIContribution, panelResources: ProviderResource[], action: PluginActionDescriptor, event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const state = stateFor(contribution, panelResources, action);
    if (!state.resourceID) return;
    updateState(contribution, panelResources, { busy: true, error: "", result: "" }, action);
    try {
      const resp = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(action.plugin_id)}/actions/${encodeURIComponent(action.action_id)}`, {
        method: "POST",
        body: JSON.stringify({
          provider_id: provider.id,
          resource_id: state.resourceID,
          refresh: true,
          ...pluginActionPayload(action, state.values),
        }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("插件面板执行失败")));
      const payload = await resp.json();
      updateState(contribution, panelResources, { busy: false, result: JSON.stringify(redactPluginActionResult(redactPanelResult(payload)), null, 2) }, action);
    } catch (err) {
      updateState(contribution, panelResources, { busy: false, error: err instanceof Error ? err.message : tx("插件面板执行失败") }, action);
    }
  }

  return (
    <>
      {layoutPanels.map(({ contribution, layout, resources: panelResources }) => (
        layout === "resource_system_prompt_transform"
          ? <ProviderResourceSystemPromptTransformFields api={api} key={providerPanelContributionKey(contribution)} providerID={provider.id} resources={panelResources} onSaved={onSaved} />
          : null
      ))}
      {genericPanels.length > 0 ? (
        <section className="provider-quota-panel provider-plugin-panels">
          <div className="wizard-panel-head">
            <h3>{tx("插件面板")}</h3>
          </div>
          <div className="provider-quota-list">
            {genericPanels.map(({ contribution, fields, resources: panelResources }) => {
              const action = contribution.action ? actionMap.get(pluginActionKey(contribution.plugin_id, contribution.action)) : undefined;
              const state = stateFor(contribution, panelResources, action);
              const selectedResource = panelResources.find((resource) => resource.id === state.resourceID);
              const registered = !contribution.action || Boolean(action);
              return (
                <article className="provider-quota-card" key={providerPanelContributionKey(contribution)}>
                  <div className="provider-quota-card-head">
                    <div className="provider-quota-account">
                      <span>{contribution.title || contribution.id}</span>
                      <strong>{contribution.plugin_id}</strong>
                    </div>
                    <div className="provider-quota-card-actions">
                      {panelResources.length > 0 ? (
                        <select disabled={state.busy} value={state.resourceID} onChange={(event) => updateState(contribution, panelResources, { resourceID: event.target.value, error: "", result: "" }, action)}>
                          {panelResources.map((resource) => <option key={resource.id} value={resource.id}>{resource.name || resource.id}</option>)}
                        </select>
                      ) : null}
                      {contribution.action && action ? (
                        <PluginActionRunner
                          action={action}
                          draft={state}
                          labels={{
                            submit: tx("执行插件面板"),
                            submitting: tx("执行中"),
                            unsupportedSchema: tx("该插件动作尚未注册。"),
                          }}
                          onChange={(descriptor, field, value) => updatePanelValue(contribution, panelResources, descriptor, field, value)}
                          onSubmit={(descriptor, event) => void runPanel(contribution, panelResources, descriptor, event)}
                        />
                      ) : contribution.action ? (
                        <button className="secondary-button" disabled={true} type="button">
                          <Play size={14} />
                          {tx("执行插件面板")}
                        </button>
                      ) : null}
                    </div>
                  </div>
                  {fields.length > 0 ? (
                    <div className="system-settings-plugin-fields">
                      {fields.map((field) => (
                        <ProviderPanelFieldView
                          context={{ provider, resource: selectedResource, resources: panelResources }}
                          field={field}
                          key={field.name}
                        />
                      ))}
                    </div>
                  ) : null}
                  {!registered ? <p className="provider-quota-error">{tx("该插件动作尚未注册。")}</p> : null}
                </article>
              );
            })}
          </div>
        </section>
      ) : null}
    </>
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

function providerPanelLayout(contribution: AdminUIContribution): PanelLayout | undefined {
  const layout = schemaString(contribution.schema?.layout);
  return layout === "resource_system_prompt_transform" ? layout : undefined;
}

type ProviderPanelContext = {
  provider: Provider;
  resource?: ProviderResource;
  resources: ProviderResource[];
};

export function providerPanelResources(contribution: AdminUIContribution, resources: ProviderResource[], providerID: string) {
  return resources.filter((resource) =>
    resource.provider_id === providerID &&
    (!contribution.resource_types?.length || contribution.resource_types.includes(resource.resource_type)),
  );
}

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

function defaultPanelState(resources: ProviderResource[], action?: PluginActionDescriptor): PanelState {
  const activeResources = resources.filter((resource) => resource.status === "active");
  return {
    values: action ? pluginActionInputDefaults(action) : {},
    resourceID: activeResources[0]?.id ?? resources[0]?.id ?? "",
    busy: false,
    error: "",
    result: "",
  };
}

export function providerPanelContributionKey(panel: AdminUIContribution) {
  return `${panel.plugin_id}:${panel.id}:${panel.action ?? ""}`;
}

export function providerQuotaPanelSelection(
  contributions: AdminUIContribution[],
  providerType: string,
  resources: ProviderResource[],
  actionsByResourceID: Record<string, PluginActionDescriptor>,
) {
  const selections = resources.flatMap((resource) => {
    const action = actionsByResourceID[resource.id];
    const contribution = action ? providerQuotaPanelContribution(contributions, providerType, resource.resource_type, action) : undefined;
    return action && contribution ? [{ action, contribution, resource }] : [];
  });
  const handledContributionKeys = Array.from(new Set(selections.map((selection) => providerPanelContributionKey(selection.contribution))));
  return {
    firstAction: selections[0]?.action,
    firstContribution: selections[0]?.contribution,
    handledContributionKeys,
    resources: selections.map((selection) => selection.resource),
  };
}

export function providerQuotaPanelDescription(contribution: AdminUIContribution) {
  const description = contribution.schema?.description;
  return typeof description === "string" ? description.trim() : "";
}

function providerQuotaPanelContribution(
  contributions: AdminUIContribution[],
  providerType: string,
  resourceType: string,
  action: PluginActionDescriptor,
) {
  return contributions.find((contribution) =>
    contribution.slot === "provider.resource.panel" &&
    contribution.plugin_id === action.plugin_id &&
    contribution.action === action.action_id &&
    (!contribution.provider_types?.length || contribution.provider_types.includes(providerType)) &&
    (!contribution.resource_types?.length || contribution.resource_types.includes(resourceType)),
  );
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

async function noopSaved() {}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaScalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : undefined;
}
