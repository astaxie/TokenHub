import { Play, SlidersHorizontal } from "lucide-react";
import { useState } from "react";
import { type AdminUIContribution, type ApiContext, type AppData } from "../core/types";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";

type SettingsPanelField = {
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

export function AdminUISettingsPanels({
  api,
  data,
}: {
  api: ApiContext;
  data: AppData;
}) {
  const panels = data.pluginUI
    .filter((contribution) => contribution.slot === "settings.panel")
    .map((contribution) => ({ contribution, fields: settingsPanelFields(contribution) }))
    .filter((panel) => panel.fields.length > 0 || Boolean(panel.contribution.action));
  const actionKeys = new Set(data.pluginActions.map((action) => pluginActionKey(action.plugin_id, action.action_id)));
  const [states, setStates] = useState<Record<string, PanelState>>({});

  if (panels.length === 0) return null;

  function stateFor(contribution: AdminUIContribution): PanelState {
    return states[settingsPanelKey(contribution)] ?? emptyPanelState();
  }

  function updateState(contribution: AdminUIContribution, patch: Partial<PanelState>) {
    setStates((current) => {
      const key = settingsPanelKey(contribution);
      return { ...current, [key]: { ...(current[key] ?? emptyPanelState()), ...patch } };
    });
  }

  async function runPanelAction(contribution: AdminUIContribution) {
    if (!contribution.action) return;
    updateState(contribution, { busy: true, error: "", result: "" });
    try {
      const resp = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(contribution.plugin_id)}/actions/${encodeURIComponent(contribution.action)}`, {
        method: "POST",
        body: JSON.stringify({ source: "settings.panel", contribution_id: contribution.id }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("插件面板执行失败")));
      const payload = await resp.json();
      updateState(contribution, { busy: false, result: JSON.stringify(redactSettingsPanelResult(payload), null, 2) });
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      updateState(contribution, { busy: false, error: reason instanceof Error ? reason.message : tx("插件面板执行失败") });
    }
  }

  return (
    <section className="section system-settings-plugin-section">
      <div className="section-header">
        <h2>{tx("插件面板")}</h2>
      </div>
      <div className="section-body system-settings-plugin-grid">
        {panels.map(({ contribution, fields }) => {
          const state = stateFor(contribution);
          const registered = !contribution.action || actionKeys.has(pluginActionKey(contribution.plugin_id, contribution.action));
          return (
            <article className="system-settings-plugin-panel" key={settingsPanelKey(contribution)}>
              <div className="system-settings-plugin-panel-head">
                <span><SlidersHorizontal size={16} /></span>
                <div>
                  <h3>{contribution.title || contribution.id}</h3>
                  <p>{contribution.plugin_id}</p>
                </div>
              </div>
              {fields.length > 0 ? (
                <div className="system-settings-plugin-fields">
                  {fields.map((field) => <SettingsPanelFieldView data={data} field={field} key={field.name} />)}
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

function SettingsPanelFieldView({ data, field }: { data: AppData; field: SettingsPanelField }) {
  const value = settingsPanelFieldValue(data, field);
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

export function settingsPanelFields(contribution: AdminUIContribution): SettingsPanelField[] {
  const rawFields = contribution.schema?.fields;
  if (!Array.isArray(rawFields)) return [];
  return rawFields.flatMap((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
    const field = raw as Record<string, unknown>;
    const type = settingsPanelFieldType(schemaString(field.type));
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

export function settingsPanelFieldValue(data: AppData, field: SettingsPanelField) {
  const rawValue = field.value ?? settingsPanelSourceValue(data, field.source);
  if (rawValue === undefined || rawValue === null || rawValue === "") return "-";
  if (field.format === "money_usd") return `$${formatMoney(Number(rawValue) || 0)}`;
  if (field.format === "compact") return compactNumber(Number(rawValue) || 0);
  if (field.format === "percent") return `${formatNumber(Number(rawValue) || 0)}%`;
  if (typeof rawValue === "number") return formatNumber(rawValue);
  if (typeof rawValue === "boolean") return rawValue ? "true" : "false";
  if (field.type === "code_viewer" && typeof rawValue === "object") return JSON.stringify(rawValue, null, 2);
  return String(rawValue);
}

function settingsPanelFieldType(type: string): SettingsPanelField["type"] | "" {
  switch (type) {
    case "metric":
    case "text":
    case "code_viewer":
      return type;
    default:
      return "";
  }
}

function settingsPanelSourceValue(data: AppData, source?: string) {
  if (!source) return undefined;
  const segments = source.split(".").map((segment) => segment.trim()).filter(Boolean);
  let current: unknown = data;
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

function settingsPanelKey(contribution: AdminUIContribution) {
  return `${contribution.plugin_id}:${contribution.slot}:${contribution.id}`;
}

function pluginActionKey(pluginID: string, actionID?: string) {
  return `${pluginID}:${actionID ?? ""}`;
}

function emptyPanelState(): PanelState {
  return { busy: false, error: "", result: "" };
}

function redactSettingsPanelResult(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactSettingsPanelResult);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) =>
    sensitiveSettingsPanelResultKey(key) ? [key, "[redacted]"] : [key, redactSettingsPanelResult(child)],
  ));
}

function sensitiveSettingsPanelResultKey(key: string) {
  const normalized = key.toLowerCase();
  return normalized.includes("token") || normalized.includes("secret") || normalized.includes("credential") || normalized.includes("api_key");
}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaScalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : undefined;
}
