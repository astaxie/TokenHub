import { Play, Puzzle } from "lucide-react";
import { useState, type FormEvent } from "react";
import { type AdminUIContribution, type ApiContext, type AppData, type PluginDescriptor } from "../core/types";
import { adminUIPageRegistry, type AdminUIPluginPageEntry } from "../domain/admin-ui-pages";
import { adminUIActionKey, adminUIFieldValue, adminUIFields, adminUIPluginPageKey, adminUIPluginPages, redactAdminUIResult, type AdminUIField } from "../domain/admin-ui-registry";
import { pluginActionInputDefaults, pluginActionKey, pluginActionPayload, redactPluginActionResult } from "../domain/plugin-actions";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";
import { PluginActionRunner } from "./plugin-action-runner";

export type PluginNavPage = AdminUIPluginPageEntry;
type PluginPageField = AdminUIField;

type PageState = {
  values: Record<string, string | boolean>;
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
  const pages = pluginNavPages(data.pluginUI, data.plugins);
  const selected = pages.find((page) => page.key === activePageKey) ?? pages[0];
  const fields = selected ? pluginPageFields(selected.contribution) : [];
  const actionKeys = new Set(data.pluginActions.map((action) => adminUIActionKey(action.plugin_id, action.action_id)));
  const [states, setStates] = useState<Record<string, PageState>>({});
  const selectedAction = selected?.contribution.action ? data.pluginActions.find((action) => pluginActionKey(action.plugin_id, action.action_id) === adminUIActionKey(selected.pluginID, selected.contribution.action)) : undefined;

  if (!selected) {
    return (
      <section className="section">
        <div className="section-body">
          <p className="empty-state">{tx("暂无界面贡献")}</p>
        </div>
      </section>
    );
  }

  const state = states[selected.key] ?? emptyPageState(selectedAction);
  const registered = !selected.contribution.action || Boolean(selectedAction) || actionKeys.has(adminUIActionKey(selected.pluginID, selected.contribution.action));

  function updateState(key: string, patch: Partial<PageState>, action?: typeof selectedAction) {
    setStates((current) => {
      const base = current[key] ?? emptyPageState(action);
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

  function updateActionValue(page: PluginNavPage, action: NonNullable<typeof selectedAction>, field: string, value: string | boolean) {
    updateState(page.key, { values: { [field]: value }, error: "" }, action);
  }

  async function runPageAction(page: PluginNavPage, action: NonNullable<typeof selectedAction>, event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    updateState(page.key, { busy: true, error: "", result: "" }, action);
    try {
      const resp = await adminFetch(api, `/api/admin/plugins/${encodeURIComponent(page.pluginID)}/actions/${encodeURIComponent(action.action_id)}`, {
        method: "POST",
        body: JSON.stringify({
          source: "nav.section",
          contribution_id: page.id,
          ...pluginActionPayload(action, state.values),
        }),
      });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("插件面板执行失败")));
      const payload = await resp.json();
      updateState(page.key, { busy: false, result: JSON.stringify(redactAdminUIResult(redactPluginActionResult(payload)), null, 2) }, action);
    } catch (reason) {
      if (isAuthExpiredError(reason)) return;
      updateState(page.key, { busy: false, error: reason instanceof Error ? reason.message : tx("插件面板执行失败") }, action);
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
      <section
        className="section plugin-page-panel"
        data-page-density={selected.template?.density}
        data-page-frame={selected.template?.frame}
        data-page-layout={selected.template?.layout}
        data-page-region={selected.template?.region}
        data-page-template={selected.template?.id}
        data-page-template-source={selected.template?.source}
      >
        <div className="section-header">
          <h2>{selected.title}</h2>
        </div>
        <div className="section-body">
          <div className="plugin-page-heading">
            <div>
              <p className="eyebrow">{selected.pluginID}</p>
              <span>{selected.description || selected.id}</span>
            </div>
            {selected.contribution.action && selectedAction ? (
              <PluginActionRunner
                action={selectedAction}
                draft={state}
                labels={{
                  submit: tx("执行插件面板"),
                  submitting: tx("执行中"),
                  unsupportedSchema: tx("该插件动作尚未注册。"),
                }}
                onChange={(action, field, value) => updateActionValue(selected, action, field, value)}
                onSubmit={(action, event) => void runPageAction(selected, action, event)}
              />
            ) : selected.contribution.action ? (
              <button className="secondary-button" disabled={true} type="button">
                <Play size={14} />
                {tx("执行插件面板")}
              </button>
            ) : null}
          </div>
          {!registered ? <p className="provider-quota-error">{tx("该插件动作尚未注册。")}</p> : null}
          {fields.length > 0 ? (
            <div className="system-settings-plugin-grid">
              {fields.map((field) => <PluginPageFieldView data={data} field={field} key={field.name} />)}
            </div>
          ) : null}
        </div>
      </section>
    </div>
  );
}

function PluginPageFieldView({ data, field }: { data: AppData; field: PluginPageField }) {
  const value = adminUIFieldValue(data, field);
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

export function pluginNavPages(contributions: AdminUIContribution[], plugins?: PluginDescriptor[]): PluginNavPage[] {
  if (plugins) return adminUIPageRegistry({ pluginUI: contributions, plugins });
  return adminUIPluginPages(contributions);
}

export function pluginPageFields(contribution: AdminUIContribution): PluginPageField[] {
  return adminUIFields(contribution);
}

export function pluginPageFieldValue(data: AppData, field: PluginPageField) {
  return adminUIFieldValue(data, field);
}

export function pluginNavPageKey(contribution: AdminUIContribution) {
  return adminUIPluginPageKey(contribution);
}

function emptyPageState(action?: typeof undefined | { input_schema?: Record<string, unknown> }) {
  return { values: action ? pluginActionInputDefaults(action) : {}, busy: false, error: "", result: "" };
}
