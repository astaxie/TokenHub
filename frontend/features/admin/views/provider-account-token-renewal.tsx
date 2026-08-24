import { useState } from "react";
import type { ApiContext, ProviderResource } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";

export function ProviderAccountTokenRenewal({ api, resource, onRenewed }: { api: ApiContext; resource: ProviderResource; onRenewed: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function renew() {
    setBusy(true);
    setError("");
    try {
      const response = await adminFetch(api, `/api/admin/provider-resources/${encodeURIComponent(resource.id)}/refresh-token`, { method: "POST" });
      if (!response.ok) throw new Error(await readAdminError(response, tx("续租 Token")));
      await onRenewed();
    } catch (reason) {
      if (!isAuthExpiredError(reason)) setError(reason instanceof Error ? reason.message : tx("续租 Token 失败"));
    } finally {
      setBusy(false);
    }
  }

  if (resource.credential_summary?.has_refresh_token !== "true") return null;
  const reauthorizationRequired = resource.credential_summary?.oauth_reauthorization_required === "true";
  return <><button className="secondary-button" disabled={busy} onClick={() => void renew()} title={tx("使用保存的 refresh token 续租账号访问 Token")} type="button">{tx(busy ? "续租中" : "续租 Token")}</button>{reauthorizationRequired || error ? <p className="provider-quota-error">{reauthorizationRequired ? tx("OpenAI/Codex 账号会话已失效，请重新进行账号授权。") : error}</p> : null}</>;
}
