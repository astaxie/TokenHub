import { useState } from "react";
import type { ApiContext, PluginActionDescriptor, ProviderResource } from "../core/types";
import { tx } from "../i18n/runtime";
import { isAuthExpiredError } from "../resources/payloads";
import { providerResourceCredentialRefreshActionForProviderType, runProviderResourceCredentialRefreshPluginAction } from "../resources/provider-model-config";

export function ProviderAccountTokenRenewal({
  api,
  pluginActions,
  providerType,
  resource,
  onRenewed,
}: {
  api: ApiContext;
  pluginActions: PluginActionDescriptor[];
  providerType: string;
  resource: ProviderResource;
  onRenewed: () => Promise<void>;
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

  if (!action) return null;
  const reauthorizationRequired = resource.credential_summary?.oauth_reauthorization_required === "true";
  return <><button className="secondary-button" disabled={busy} onClick={() => void renew()} title={tx("使用保存的 refresh token 续租账号访问 Token")} type="button">{tx(busy ? "续租中" : "续租 Token")}</button>{reauthorizationRequired || error ? <p className="provider-quota-error">{reauthorizationRequired ? tx("账号会话已失效，请重新进行账号授权。") : error}</p> : null}</>;
}
