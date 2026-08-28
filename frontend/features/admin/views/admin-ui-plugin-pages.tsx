import { Play, Puzzle } from "lucide-react";
import { useState } from "react";
import { type AdminUIContribution, type ApiContext, type AppData } from "../core/types";
import { adminUIActionKey, adminUIFieldValue, adminUIFields, adminUIPluginPageKey, adminUIPluginPages, redactAdminUIResult, type AdminUIField, type AdminUIPluginPage } from "../domain/admin-ui-registry";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";

export type PluginNavPage = AdminUIPluginPage;
type PluginPageField = AdminUIField;

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
  const actionKeys = new Set(data.pluginActions.map((action) => adminUIActionKey(action.plugin_id, action.action_id)));
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
  const registered = !selected.contribution.action || actionKeys.has(adminUIActionKey(selected.pluginID, selected.contribution.action));

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
      updateState(page.key, { busy: false, result: JSON.stringify(redactAdminUIResult(payload), null, 2) });
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

export function pluginNavPages(contributions: AdminUIContribution[]): PluginNavPage[] {
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

function emptyPageState(): PageState {
  return { busy: false, error: "", result: "" };
}
