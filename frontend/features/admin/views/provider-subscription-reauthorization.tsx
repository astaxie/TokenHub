import { useState } from "react";
import type { ProviderAccountOAuthResult } from "../core/session";
import type { ApiContext, PluginActionDescriptor, ProviderResource } from "../core/types";
import { formatTranslationTemplate, tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, providerResourcePayload, readAdminError } from "../resources/payloads";
import { ProviderSubscriptionAuth, subscriptionCredentialValues, supportsSubscriptionAuthorization } from "./provider-subscription-auth";

export function subscriptionAccountNeedsLogin(resource: ProviderResource) {
  const expiresAt = Date.parse(resource.credential_summary?.token_expires_at ?? "");
  return resource.credential_summary?.oauth_reauthorization_required === "true" || Number.isFinite(expiresAt) && expiresAt <= Date.now();
}

export function ProviderSubscriptionReauthorization({ api, actions, providerType, resource, resources = [], initiallyOpen = false, onSelectAccount, onTest, onSaved }: {
  api: ApiContext;
  actions: PluginActionDescriptor[];
  providerType: string;
  resource?: ProviderResource;
  resources?: ProviderResource[];
  initiallyOpen?: boolean;
  onSelectAccount?: (resourceID: string) => void;
  onTest?: () => void;
  onSaved: () => Promise<void>;
}) {
  const [open, setOpen] = useState(initiallyOpen);
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
  return <section className="provider-edit-section provider-subscription-reauthorization" aria-label={tx("更新订阅账号凭据")} onKeyDown={event => {
    if (event.key === "Enter" && event.target instanceof HTMLInputElement) event.preventDefault();
  }}>
    <div className="wizard-panel-head"><h3>{tx("更新订阅账号凭据")}</h3><p>{tx("重新登录或导入会话，保存后替换所选账号的旧凭据；调度配置保持不变。")}</p></div>
    {!resource ? <>
      <p>{tx("请选择要重新登录的订阅账号。")}</p>
      {resources.map(account => <div className="provider-subscription-login-account" key={account.id}>
        <div><strong>{account.name}</strong>{subscriptionAccountNeedsLogin(account) ? <p className="provider-quota-error">{tx("账号 Token 已过期或会话已失效，请重新登录后保存新凭据。")}</p> : null}</div>
        <button className="button" type="button" disabled={!onSelectAccount} aria-label={formatTranslationTemplate(tx("重新登录：{name}"), { name: account.name })} onClick={() => onSelectAccount?.(account.id)}>{tx("重新登录")}</button>
      </div>)}
    </> : <>
      <p>{resource.name}</p>
      {!saved && subscriptionAccountNeedsLogin(resource) ? <p className="provider-quota-error">{tx("账号 Token 已过期或会话已失效，请重新登录后保存新凭据。")}</p> : null}
      {!open ? <button className="button provider-authorization-button" type="button" disabled={busy} onClick={() => { setOpen(true); setSaved(false); setError(""); }}>{tx("重新登录")}</button> : <>
        {credentials ? <p role="status">{tx("新凭据已准备好，请保存以完成更新。")}</p> : <ProviderSubscriptionAuth key={resource.id} api={api} actions={actions} providerType={providerType} resourceID={resource.id} disabled={busy} onCredentials={result => {
          if (!result.access_token?.trim() && !result.session_json?.trim()) throw new Error(tx("未在回调结果中识别到 Token。"));
          setCredentials(result); setError("");
        }} />}
        <div className="provider-form-actions"><button className="button" type="button" disabled={busy || !credentials} onClick={() => { void save(); }}>{tx(busy ? "保存中" : "保存新账号凭据")}</button>
        <button className="secondary-button" type="button" disabled={busy} onClick={() => { setOpen(false); setCredentials(null); setError(""); }}>{tx("取消")}</button>
        </div>
      </>}
    </>}
    {saved ? <><p role="status">{tx("账号凭据已更新。")}</p>{onTest ? <div className="provider-form-actions"><p>{tx("可测试此账号，确认新 Token 可用并恢复账号健康状态。")}</p><button className="secondary-button" type="button" onClick={onTest}>{tx("测试账号")}</button></div> : null}</> : null}
    {error ? <p className="provider-quota-error" role="alert">{error}</p> : null}
  </section>;
}
