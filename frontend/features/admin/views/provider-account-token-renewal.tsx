import { useState } from "react";
import type { ApiContext, PluginActionDescriptor, ProviderResource } from "../core/types";
import { formatTranslationTemplate, tx } from "../i18n/runtime";
import { isAuthExpiredError } from "../resources/payloads";
import { providerResourceCredentialRefreshActionForProviderType, runProviderResourceCredentialRefreshPluginAction } from "../resources/provider-model-config";
import { subscriptionAccountNeedsLogin } from "./provider-subscription-reauthorization";

export function ProviderAccountTokenRenewal({
  api,
  pluginActions,
  providerType,
  resource,
  onRenewed,
  onReauthorize,
}: {
  api: ApiContext;
  pluginActions: PluginActionDescriptor[];
  providerType: string;
  resource: ProviderResource;
  onRenewed: () => Promise<void>;
  onReauthorize?: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const action = providerResourceCredentialRefreshActionForProviderType(pluginActions, resource, providerType);

  async function renew() {
    if (!action) return;
    setBusy(true);
    setError("");
    try {
      await runProviderResourceCredentialRefreshPluginAction(api, resource, action);
      await onRenewed();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setError(reason instanceof Error ? reason.message : tx("续租 Token 失败"));
    } finally {
      setBusy(false);
    }
  }

  if (!action && !onReauthorize) return null;
  const reauthorizationRequired = subscriptionAccountNeedsLogin(resource);
  return <>{action ? <button className="secondary-button" disabled={busy} onClick={() => void renew()} title={tx("使用保存的 refresh token 续租账号访问 Token")} type="button">{tx(busy ? "续租中" : "续租 Token")}</button> : null}{onReauthorize ? <button className={reauthorizationRequired || error ? "button" : "secondary-button"} disabled={busy} onClick={onReauthorize} aria-label={formatTranslationTemplate(tx("重新登录：{name}"), { name: resource.name })} type="button">{tx("重新登录")}</button> : null}{reauthorizationRequired || error ? <p className="provider-quota-error">{reauthorizationRequired ? tx("账号 Token 已过期或会话已失效，请重新登录后保存新凭据。") : error}</p> : null}</>;
}
