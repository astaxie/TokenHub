import { Play, Route } from "lucide-react";
import { useState } from "react";
import { type AdminUIContribution, type ApiContext, type AppData, type ModelRoute } from "../core/types";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";

type RoutePanelField = {
  name: string;
  type: "metric" | "text" | "code_viewer";
  label: string;
  source?: string;
  value?: string | number | boolean;
  format?: "compact" | "money_usd" | "number" | "percent" | string;
  help?: string;
};

type PanelState = {
  busy: boolean;
  error: string;
  result: string;
};

export function AdminUIRouteDetailPanels({
  api,
  data,
  route,
}: {
  api: ApiContext;
  data: AppData;
  route: ModelRoute;
}) {
  const panels = data.pluginUI
    .filter((contribution) => contribution.slot === "route.detail.panel")
    .map((contribution) => ({ contribution, fields: routePanelFields(contribution) }))
    .filter((panel) => panel.fields.length > 0 || Boolean(panel.contribution.action));
  const actionKeys = new Set(data.pluginActions.map((action) => pluginActionKey(action.plugin_id, action.action_id)));
  const [states, setStates] = useState<Record<string, PanelState>>({});

  if (panels.length === 0) return null;

  function stateFor(contribution: AdminUIContribution): PanelState {
    return states[routePanelKey(contribution)] ?? emptyPanelState();
  }

  function updateState(contribution: AdminUIContribution, patch: Partial<PanelState>) {
    setStates((current) => {
      const key = routePanelKey(contribution);
      return { ...current, [key]: { ...(current[key] ?? emptyPanelState()), ...patch } };
    });
  }

  async function runPanelAction(contribution: AdminUIContribution) {
    if (!contribution.action) return;
    updateState(contribution, { busy: true, error: "", result: "" });
    try {
      const resp = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(contribution.plugin_id)}/actions/${encodeURIComponent(contribution.action)}`, {
        method: "POST",
        body: JSON.stringify({ source: "route.detail.panel", contribution_id: contribution.id, route_id: route.id }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("插件面板执行失败")));
      const payload = await resp.json();
      updateState(contribution, { busy: false, result: JSON.stringify(redactRoutePanelResult(payload), null, 2) });
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      updateState(contribution, { busy: false, error: reason instanceof Error ? reason.message : tx("插件面板执行失败") });
    }
  }

  return (
    <section className="route-detail-plugin-section">
      <div className="wizard-panel-head">
        <h3>{tx("插件面板")}</h3>
      </div>
      <div className="system-settings-plugin-grid">
        {panels.map(({ contribution, fields }) => {
          const state = stateFor(contribution);
          const registered = !contribution.action || actionKeys.has(pluginActionKey(contribution.plugin_id, contribution.action));
          return (
            <article className="system-settings-plugin-panel" key={routePanelKey(contribution)}>
              <div className="system-settings-plugin-panel-head">
                <span><Route size={16} /></span>
                <div>
                  <h3>{contribution.title || contribution.id}</h3>
                  <p>{contribution.plugin_id}</p>
                </div>
              </div>
              {fields.length > 0 ? (
                <div className="system-settings-plugin-fields">
                  {fields.map((field) => <RoutePanelFieldView data={data} field={field} key={field.name} route={route} />)}
                </div>
              ) : null}
              {!registered ? <p className="provider-quota-error">{tx("该插件动作尚未注册。")}</p> : null}
              {contribution.action ? (
                <button className="secondary-button" disabled={state.busy || !registered} onClick={() => void runPanelAction(contribution)} type="button">
                  <Play size={14} />
                  {tx(state.busy ? "执行中" : "执行插件面板")}
                </button>
              ) : null}
              {state.error ? <p className="provider-quota-error">{state.error}</p> : null}
              {state.result ? <pre className="plugin-action-result">{state.result}</pre> : null}
            </article>
          );
        })}
      </div>
    </section>
  );
}

function RoutePanelFieldView({ data, field, route }: { data: AppData; field: RoutePanelField; route: ModelRoute }) {
  const value = routePanelFieldValue(data, route, field);
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

export function routePanelFields(contribution: AdminUIContribution): RoutePanelField[] {
  const rawFields = contribution.schema?.fields;
  if (!Array.isArray(rawFields)) return [];
  return rawFields.flatMap((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
    const field = raw as Record<string, unknown>;
    const type = routePanelFieldType(schemaString(field.type));
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

export function routePanelFieldValue(data: AppData, route: ModelRoute, field: RoutePanelField) {
  const rawValue = field.value ?? routePanelSourceValue({ data, route }, field.source);
  if (rawValue === undefined || rawValue === null || rawValue === "") return "-";
  if (field.format === "money_usd") return `$${formatMoney(Number(rawValue) || 0)}`;
  if (field.format === "compact") return compactNumber(Number(rawValue) || 0);
  if (field.format === "percent") return `${formatNumber(Number(rawValue) || 0)}%`;
  if (typeof rawValue === "number") return formatNumber(rawValue);
  if (typeof rawValue === "boolean") return rawValue ? "true" : "false";
  if (field.type === "code_viewer" && typeof rawValue === "object") return JSON.stringify(rawValue, null, 2);
  return String(rawValue);
}

function routePanelFieldType(type: string): RoutePanelField["type"] | "" {
  switch (type) {
    case "metric":
    case "text":
    case "code_viewer":
      return type;
    default:
      return "";
  }
}

function routePanelSourceValue(root: { data: AppData; route: ModelRoute }, source?: string) {
  if (!source) return undefined;
  const segments = source.split(".").map((segment) => segment.trim()).filter(Boolean);
  let current: unknown = root;
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

const arrayIndexPattern = /^\d+$/;

function routePanelKey(contribution: AdminUIContribution) {
  return `${contribution.plugin_id}:${contribution.slot}:${contribution.id}`;
}

function pluginActionKey(pluginID: string, actionID?: string) {
  return `${pluginID}:${actionID ?? ""}`;
}

function emptyPanelState(): PanelState {
  return { busy: false, error: "", result: "" };
}

function redactRoutePanelResult(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactRoutePanelResult);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) =>
    sensitiveRoutePanelResultKey(key) ? [key, "[redacted]"] : [key, redactRoutePanelResult(child)],
  ));
}

function sensitiveRoutePanelResultKey(key: string) {
  const normalized = key.toLowerCase();
  return normalized.includes("token") || normalized.includes("secret") || normalized.includes("credential") || normalized.includes("api_key");
}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaScalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : undefined;
}
