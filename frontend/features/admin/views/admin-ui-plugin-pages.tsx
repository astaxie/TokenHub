import { Play, Puzzle } from "lucide-react";
import { useState } from "react";
import { type AdminUIContribution, type ApiContext, type AppData } from "../core/types";
import { compactNumber, formatMoney, formatNumber } from "../domain/formatting";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";

export type PluginNavPage = {
  key: string;
  pluginID: string;
  id: string;
  title: string;
  description: string;
  contribution: AdminUIContribution;
};

type PluginPageField = {
  name: string;
  type: "metric" | "text" | "code_viewer";
  label: string;
  source?: string;
  value?: string | number | boolean;
  format?: "compact" | "money_usd" | "number" | "percent" | string;
  help?: string;
};

type PageState = {
  busy: boolean;
  error: string;
  result: string;
};

export function PluginPageView({
  activePageKey,
  api,
  data,
  onSelectPage,
}: {
  activePageKey: string;
  api: ApiContext;
  data: AppData;
  onSelectPage: (key: string) => void;
}) {
  const pages = pluginNavPages(data.pluginUI);
  const selected = pages.find((page) => page.key === activePageKey) ?? pages[0];
  const fields = selected ? pluginPageFields(selected.contribution) : [];
  const actionKeys = new Set(data.pluginActions.map((action) => pluginActionKey(action.plugin_id, action.action_id)));
  const [states, setStates] = useState<Record<string, PageState>>({});

  if (!selected) {
    return (
      <section className="section">
        <div className="section-body">
          <p className="empty-state">{tx("暂无界面贡献")}</p>
        </div>
      </section>
    );
  }

  const state = states[selected.key] ?? emptyPageState();
  const registered = !selected.contribution.action || actionKeys.has(pluginActionKey(selected.pluginID, selected.contribution.action));

  function updateState(key: string, patch: Partial<PageState>) {
    setStates((current) => ({ ...current, [key]: { ...(current[key] ?? emptyPageState()), ...patch } }));
  }

  async function runPageAction(page: PluginNavPage) {
    if (!page.contribution.action) return;
    updateState(page.key, { busy: true, error: "", result: "" });
    try {
      const resp = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(page.pluginID)}/actions/${encodeURIComponent(page.contribution.action)}`, {
        method: "POST",
        body: JSON.stringify({ source: "nav.section", contribution_id: page.id }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("插件面板执行失败")));
      const payload = await resp.json();
      updateState(page.key, { busy: false, result: JSON.stringify(redactPluginPageResult(payload), null, 2) });
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      updateState(page.key, { busy: false, error: reason instanceof Error ? reason.message : tx("插件面板执行失败") });
    }
  }

  return (
    <div className="plugin-page-view">
      <aside className="plugin-page-list">
        {pages.map((page) => (
          <button className={page.key === selected.key ? "active" : ""} key={page.key} onClick={() => onSelectPage(page.key)} type="button">
            <Puzzle size={15} />
            <span>{page.title}</span>
          </button>
        ))}
      </aside>
      <section className="section plugin-page-panel">
        <div className="section-header">
          <h2>{selected.title}</h2>
        </div>
        <div className="section-body">
          <div className="plugin-page-heading">
            <div>
              <p className="eyebrow">{selected.pluginID}</p>
              <span>{selected.description || selected.id}</span>
            </div>
            {selected.contribution.action ? (
              <button className="secondary-button" disabled={state.busy || !registered} onClick={() => void runPageAction(selected)} type="button">
                <Play size={14} />
                {tx(state.busy ? "执行中" : "执行插件面板")}
              </button>
            ) : null}
          </div>
          {!registered ? <p className="provider-quota-error">{tx("该插件动作尚未注册。")}</p> : null}
          {fields.length > 0 ? (
            <div className="system-settings-plugin-grid">
              {fields.map((field) => <PluginPageFieldView data={data} field={field} key={field.name} />)}
            </div>
          ) : null}
          {state.error ? <p className="provider-quota-error">{state.error}</p> : null}
          {state.result ? <pre className="plugin-action-result">{state.result}</pre> : null}
        </div>
      </section>
    </div>
  );
}

function PluginPageFieldView({ data, field }: { data: AppData; field: PluginPageField }) {
  const value = pluginPageFieldValue(data, field);
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

export function pluginNavPages(contributions: AdminUIContribution[]): PluginNavPage[] {
  return contributions
    .filter((contribution) => contribution.slot === "nav.section")
    .map((contribution) => ({
      key: pluginNavPageKey(contribution),
      pluginID: contribution.plugin_id,
      id: contribution.id,
      title: contribution.title || contribution.id,
      description: schemaString(contribution.schema?.description),
      contribution,
    }));
}

export function pluginPageFields(contribution: AdminUIContribution): PluginPageField[] {
  const rawFields = contribution.schema?.fields;
  if (!Array.isArray(rawFields)) return [];
  return rawFields.flatMap((raw) => {
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return [];
    const field = raw as Record<string, unknown>;
    const type = pluginPageFieldType(schemaString(field.type));
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

export function pluginPageFieldValue(data: AppData, field: PluginPageField) {
  const rawValue = field.value ?? pluginPageSourceValue(data, field.source);
  if (rawValue === undefined || rawValue === null || rawValue === "") return "-";
  if (field.format === "money_usd") return `$${formatMoney(Number(rawValue) || 0)}`;
  if (field.format === "compact") return compactNumber(Number(rawValue) || 0);
  if (field.format === "percent") return `${formatNumber(Number(rawValue) || 0)}%`;
  if (typeof rawValue === "number") return formatNumber(rawValue);
  if (typeof rawValue === "boolean") return rawValue ? "true" : "false";
  if (field.type === "code_viewer" && typeof rawValue === "object") return JSON.stringify(rawValue, null, 2);
  return String(rawValue);
}

export function pluginNavPageKey(contribution: AdminUIContribution) {
  return `${contribution.plugin_id}:${contribution.id}`;
}

function pluginPageFieldType(type: string): PluginPageField["type"] | "" {
  switch (type) {
    case "metric":
    case "text":
    case "code_viewer":
      return type;
    default:
      return "";
  }
}

function pluginPageSourceValue(data: AppData, source?: string) {
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

function pluginActionKey(pluginID: string, actionID?: string) {
  return `${pluginID}:${actionID ?? ""}`;
}

function emptyPageState(): PageState {
  return { busy: false, error: "", result: "" };
}

function redactPluginPageResult(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(redactPluginPageResult);
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).map(([key, child]) =>
    sensitivePluginPageResultKey(key) ? [key, "[redacted]"] : [key, redactPluginPageResult(child)],
  ));
}

function sensitivePluginPageResultKey(key: string) {
  const normalized = key.toLowerCase();
  return normalized.includes("token") || normalized.includes("secret") || normalized.includes("credential") || normalized.includes("api_key");
}

function schemaString(value: unknown) {
  return typeof value === "string" ? value.trim() : "";
}

function schemaScalar(value: unknown) {
  return typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? value : undefined;
}
