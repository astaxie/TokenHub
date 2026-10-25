import { useState } from "react";
import type { ProviderAccountOAuthResult } from "../core/session";
import type { ApiContext, PluginActionDescriptor, ProviderResource } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, providerResourcePayload, readAdminError } from "../resources/payloads";
import { ProviderSubscriptionAuth, subscriptionCredentialValues, supportsSubscriptionAuthorization } from "./provider-subscription-auth";

export function ProviderSubscriptionReauthorization({ api, actions, providerType, resource, onSaved }: {
  api: ApiContext;
  actions: PluginActionDescriptor[];
  providerType: string;
  resource?: ProviderResource;
  onSaved: () => Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  const [credentials, setCredentials] = useState<ProviderAccountOAuthResult | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);

  async function save() {
    if (!resource || !credentials) return;
    setBusy(true); setError("");
    const payload = providerResourcePayload({ resource_type: resource.resource_type, ...subscriptionCredentialValues(credentials, true) });
    try {
      const response = await adminFetch(api, `/api/admin/provider-resources/${encodeURIComponent(resource.id)}`, { method: "PATCH", body: JSON.stringify({ credentials: payload.credentials }) });
      if (!response.ok) throw new Error(await readAdminError(response, tx("更新账号凭据失败")));
      setCredentials(null); setOpen(false); setSaved(true);
      try { await onSaved(); }
      catch { setError(tx("账号凭据已保存，但页面刷新失败。请重新打开编辑器。")); }
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setError(reason instanceof Error ? reason.message : tx("更新账号凭据失败"));
    } finally { setBusy(false); }
  }

  if (!supportsSubscriptionAuthorization(actions, providerType)) return null;
  return <section className="provider-edit-section" aria-label={tx("更新订阅账号凭据")} onKeyDown={event => {
    if (event.key === "Enter" && event.target instanceof HTMLInputElement) event.preventDefault();
  }}>
    <div className="wizard-panel-head"><h3>{tx("更新订阅账号凭据")}</h3><p>{tx("重新登录或导入会话，保存后替换所选账号的旧凭据；调度配置保持不变。")}</p></div>
    {!resource ? <p>{tx("请在顶部账号列表中选择一个账号，再更新其凭据。")}</p> : <>
      <p>{resource.name}</p>
      {!open ? <button className="secondary-button" type="button" disabled={busy} onClick={() => { setOpen(true); setSaved(false); setError(""); }}>{tx("重新授权账号")}</button> : <>
        {credentials ? <p role="status">{tx("新凭据已准备好，请保存以完成更新。")}</p> : <ProviderSubscriptionAuth api={api} actions={actions} providerType={providerType} disabled={busy} onCredentials={result => {
          if (!result.access_token?.trim() && !result.session_json?.trim()) throw new Error(tx("未在回调结果中识别到 Token。"));
          setCredentials(result); setError("");
        }} />}
        <div className="provider-form-actions"><button className="button" type="button" disabled={busy || !credentials} onClick={() => { void save(); }}>{tx(busy ? "保存中" : "保存新账号凭据")}</button>
        <button className="secondary-button" type="button" disabled={busy} onClick={() => { setOpen(false); setCredentials(null); setError(""); }}>{tx("取消")}</button>
        </div>
      </>}
    </>}
    {saved ? <p role="status">{tx("账号凭据已更新。")}</p> : null}
    {error ? <p className="provider-quota-error" role="alert">{error}</p> : null}
  </section>;
}
