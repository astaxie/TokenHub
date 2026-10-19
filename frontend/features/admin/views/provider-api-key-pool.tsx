import { useState } from "react";
import type { ApiContext, FieldConfig, Provider, ProviderResource } from "../core/types";
import { providerResourceAPIKeyType } from "../domain/provider-resource-types";
import { formatTranslationTemplate, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, providerResourcePayload, providerResourceToForm, providerResourceUpdatePayload, readAdminError } from "../resources/payloads";
import { StatusPill } from "../shared/ui";
import { ProviderInlineField } from "./provider-editor-fields";

const schedulingFields: FieldConfig[] = [
  { key: "priority", label: "优先级", type: "number" },
  { key: "weight", label: "权重", type: "number" },
  { key: "rate_limit_rpm", label: "RPM 限制", type: "number" },
  { key: "token_limit_tpm", label: "TPM 限制", type: "number" },
  { key: "max_concurrency", label: "最大并发", type: "number" },
];

export function ProviderAPIKeyPool({ api, provider, resources, onSaved }: {
  api: ApiContext;
  provider: Provider;
  resources: ProviderResource[];
  onSaved: () => Promise<void> | void;
}) {
  const keys = resources.filter((resource) => resource.provider_id === provider.id && resource.resource_type === providerResourceAPIKeyType);
  const [draft, setDraft] = useState<Record<string, string> | null>(null);
  const [editing, setEditing] = useState<ProviderResource | null>(null);
  const [deleting, setDeleting] = useState<ProviderResource | null>(null);
  const [inheritKey, setInheritKey] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  function openEditor(resource?: ProviderResource) {
    setEditing(resource ?? null);
    setDeleting(null);
    setInheritKey(false);
    setError("");
    setNotice("");
    setDraft(resource ? providerResourceToForm(resource, provider.options) : {
      provider_id: provider.id, resource_type: providerResourceAPIKeyType, name: "", api_key: "",
      base_url: "", group: "default", status: "active", healthy: "true", priority: "1", weight: "100",
      rate_limit_rpm: "0", token_limit_tpm: "0", max_concurrency: "0",
    });
  }

  function update(key: string, value: string) {
    setDraft((current) => current ? { ...current, [key]: value } : current);
    setError("");
  }

  async function mutate(method: "POST" | "PATCH" | "DELETE" | "enable" | "disable", resource: ProviderResource | null, payload?: Record<string, unknown>) {
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const bulk = method === "enable" || method === "disable";
      const path = `/api/admin/provider-resources${bulk ? "/bulk" : resource ? `/${encodeURIComponent(resource.id)}` : ""}`;
      const body = bulk ? { action: method, ids: [resource?.id] } : payload;
      const response = await adminFetch(api, path, { method: bulk ? "POST" : method, ...(body ? { body: JSON.stringify(body) } : {}) });
      if (!response.ok) throw new Error(await readAdminError(response, tx("保存 API Key 资源")));
      if (bulk) {
        const result = await response.json() as { success?: number; failed?: number; errors?: string[] };
        if (result.success !== 1 || (result.failed ?? 0) !== 0 || result.errors?.length) {
          throw new Error(result.errors?.[0] || tx("保存失败"));
        }
      }
      setDraft(null);
      setEditing(null);
      setDeleting(null);
      await onSaved();
      setNotice(tx("API Key 资源已更新。"));
    } catch (cause) {
      if (!isAuthExpiredError(cause)) setError(cause instanceof Error ? cause.message : tx("保存失败"));
    } finally {
      setBusy(false);
    }
  }

  function save() {
    if (!draft || busy) return;
    if (!draft.name?.trim()) { setError(tx("请填写 API Key 名称。")); return; }
    if (resources.some((resource) => resource.id !== editing?.id && resource.name === draft.name.trim())) {
      setError(tx("API Key 名称已存在，请使用唯一名称。")); return;
    }
    if (!editing && !inheritKey && !draft.api_key?.trim()) { setError(tx("请填写 API Key，或选择使用 Provider 已保存的密钥。")); return; }
    if (schedulingFields.some((field) => {
      const value = Number(draft[field.key] || "0");
      if (field.key === "priority" && editing?.priority === 0 && draft.priority?.trim() && value === 0) return false;
      return !Number.isSafeInteger(value) || value < (field.key === "priority" || field.key === "weight" ? 1 : 0);
    })) { setError(tx("优先级和权重须为正整数，限流和并发须为非负整数。")); return; }
    const values = { ...draft, name: draft.name.trim(), api_key: inheritKey ? "" : draft.api_key };
    void mutate(editing ? "PATCH" : "POST", editing, editing ? updatePayload(editing, values) : providerResourcePayload(values));
  }

  return (
    <section aria-label={tx("API Key 资源池")} className="provider-key-pool">
      <div className="provider-key-pool-head">
        <div><h3>{tx("API Key 资源池")}</h3><p>{tx("同一 Provider 可添加多个上游 API Key，共享模型与路由，分别配置权重和限流。")}</p></div>
        <button className="secondary-button" disabled={busy || Boolean(draft)} onClick={() => openEditor()} type="button">{tx("添加 API Key")}</button>
      </div>
      <p className="provider-key-pool-guidance">{tx("有可用资源时优先使用资源池；没有符合条件的资源时会回退到 Provider 默认密钥。若要默认密钥也参与池内分配，请将其添加为资源。")}</p>
      <p className="provider-key-pool-guidance">{tx("资源操作立即保存；上方 Provider 连接信息仍需单独保存。")}</p>
      {keys.length === 0 ? <p className="empty compact-empty">{tx("尚未添加 API Key 资源，当前使用 Provider 默认密钥。")}</p> : (
        <ul className="provider-key-pool-list">
          {keys.map((resource) => (
            <li key={resource.id}>
              <div className="provider-key-pool-name"><strong>{resource.name}</strong><div><StatusPill status={resource.status} /><StatusPill status={resource.healthy ? "healthy" : "down"} /></div></div>
              <div className="provider-key-pool-actions">
                <button className="secondary-button" disabled={busy || Boolean(draft)} onClick={() => openEditor(resource)} type="button">{tx("编辑")}</button>
                <button className="secondary-button" disabled={busy || Boolean(draft)} onClick={() => void mutate(resource.status === "active" ? "disable" : "enable", resource)} type="button">{tx(resource.status === "active" ? "停用" : "启用")}</button>
                <button className="danger-button" disabled={busy || Boolean(draft)} onClick={() => { setDeleting(resource); setDraft(null); setError(""); setNotice(""); }} type="button">{tx("删除")}</button>
              </div>
            </li>
          ))}
        </ul>
      )}
      {deleting ? <div className="provider-key-pool-confirm" role="group" aria-label={tx("删除 API Key 资源")}>
        <p>{formatTranslationTemplate(tx("删除资源「{name}」后，绑定它的路由将改为自动选择其他资源或 Provider 默认连接。若要保留绑定，请停用此资源。"), { name: deleting.name })}</p>
        <div className="provider-key-pool-actions">
          <button className="danger-button" disabled={busy} onClick={() => void mutate("DELETE", deleting)} type="button">{tx("确认删除")}</button>
          <button className="secondary-button" disabled={busy} onClick={() => setDeleting(null)} type="button">{tx("取消")}</button>
        </div>
      </div> : null}
      {draft ? <fieldset className="provider-key-pool-editor" disabled={busy} onKeyDown={(event) => {
        if (event.key === "Enter" && event.target instanceof HTMLInputElement) { event.preventDefault(); save(); }
      }}>
        <legend>{tx(editing ? "编辑 API Key 资源" : "添加 API Key 资源")}</legend>
        <div className="provider-key-pool-fields">
          <ProviderInlineField field={{ key: "name", label: "API Key 名称" }} value={draft.name} values={draft} onChange={(value) => update("name", value)} />
          {!inheritKey ? <ProviderInlineField field={{ key: "api_key", label: "API Key", type: "password", autoComplete: "new-password", help: editing ? "留空表示不修改现有 Key；填写新值才会覆盖。" : "密钥加密保存，保存后不会再次显示。" }} value={draft.api_key} values={draft} onChange={(value) => update("api_key", value)} /> : null}
        </div>
        {!editing ? <label className="checkbox-line"><input type="checkbox" checked={inheritKey} onChange={(event) => { setInheritKey(event.target.checked); update("api_key", ""); }} /><span>{tx("使用 Provider 已保存的密钥")}</span></label> : null}
        <details className="provider-key-pool-scheduling">
          <summary>{tx("调度与限流设置")}</summary>
          <p>{tx("优先级从 1 开始，数值越小越优先；同优先级按路由策略与权重分配。限流和并发填 0 表示不限制。")}</p>
          {editing?.priority === 0 ? <p>{tx("此资源的优先级 0 表示未指定，调度时按 9999 处理；可保留原值，或改为正整数。")}</p> : null}
          <div className="provider-key-pool-fields">
            <ProviderInlineField field={{ key: "base_url", label: "Base URL", help: "留空继承 Provider 地址。" }} value={draft.base_url} values={draft} onChange={(value) => update("base_url", value)} />
            {schedulingFields.map((field) => <ProviderInlineField key={field.key} field={field} value={draft[field.key]} values={draft} onChange={(value) => update(field.key, value)} />)}
          </div>
        </details>
        <div className="provider-key-pool-actions">
          <button className="button" disabled={busy} onClick={save} type="button">{tx(busy ? "保存中..." : "保存 API Key 资源")}</button>
          <button className="secondary-button" disabled={busy} onClick={() => { setDraft(null); setError(""); }} type="button">{tx("取消")}</button>
        </div>
      </fieldset> : null}
      {error ? <p className="provider-key-pool-error" role="alert">{error}</p> : null}
      {notice ? <p role="status">{notice}</p> : null}
    </section>
  );
}

function updatePayload(resource: ProviderResource, values: Record<string, string>) {
  const payload = providerResourceUpdatePayload({ ...providerResourceToForm(resource), ...values });
  // This editor owns credentials and scheduling only. Keep advanced settings
  // untouched, including masked headers and plugin-specific options.
  delete payload.headers;
  delete payload.sensitive_headers;
  delete payload.options;
  return payload;
}
