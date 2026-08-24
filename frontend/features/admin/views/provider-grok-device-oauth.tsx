import { Copy, ExternalLink } from "lucide-react";
import { useState } from "react";
import type { ApiContext } from "../core/types";
import { clearPendingProviderAccountOAuthSession, savePendingProviderAccountOAuthSession, type ProviderAccountOAuthResult } from "../core/session";
import { copyText } from "../domain/clipboard";
import { tx } from "../i18n/runtime";
import { adminFetch, isAuthExpiredError, readAdminError } from "../resources/payloads";

export type GrokDeviceOAuthStart = {
  session_id: string;
  state: string;
  user_code: string;
  verification_uri: string;
  verification_uri_complete?: string;
  expires_at: string;
  interval_seconds: number;
};

export function useGrokDeviceOAuth({
  api,
  onAuthorized,
  setBusy,
  setError,
  setStatus,
}: {
  api: ApiContext;
  onAuthorized: (result: ProviderAccountOAuthResult, message: string) => Promise<void>;
  setBusy: (value: boolean) => void;
  setError: (value: string) => void;
  setStatus: (value: string) => void;
}) {
  const [device, setDevice] = useState<GrokDeviceOAuthStart | null>(null);
  const [open, setOpen] = useState(false);

  async function start() {
    setBusy(true);
    try {
      const resp = await adminFetch(api, "/api/admin/provider-account-oauth/xai/start-device", { method: "POST", body: JSON.stringify({}) });
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("开始 Super Grok 授权")));
      const generated = (await resp.json()) as GrokDeviceOAuthStart;
      savePendingProviderAccountOAuthSession({ session_id: generated.session_id, state: generated.state });
      setDevice(generated);
      setOpen(true);
      setStatus(tx("请在 xAI 页面输入用户码完成 Super Grok 授权。"));
      setError("");
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      const errorMessage = err instanceof Error ? err.message : tx("开始 Super Grok 授权失败");
      setStatus(errorMessage);
      setError(errorMessage);
    } finally {
      setBusy(false);
    }
  }

  async function poll() {
    if (!device) return;
    setBusy(true);
    try {
      const resp = await adminFetch(api, "/api/admin/provider-account-oauth/xai/poll", {
        method: "POST",
        body: JSON.stringify({ session_id: device.session_id, state: device.state }),
      });
      if (resp.status === 202) {
        setStatus(tx("正在等待 Super Grok 授权..."));
        return;
      }
      if (!resp.ok) throw new Error(await readAdminError(resp, tx("查询 Super Grok 授权")));
      const tokenInfo = (await resp.json()) as ProviderAccountOAuthResult;
      clearPendingProviderAccountOAuthSession();
      setOpen(false);
      await onAuthorized(tokenInfo, tx("已完成 Super Grok 授权。"));
    } catch (err) {
      if (isAuthExpiredError(err)) return;
      const errorMessage = err instanceof Error ? err.message : tx("查询 Super Grok 授权失败");
      setStatus(errorMessage);
      setError(errorMessage);
    } finally {
      setBusy(false);
    }
  }

  return { device, open, poll, setOpen, start };
}

export function ProviderGrokDeviceOAuthModal({
  open,
  busy,
  error,
  device,
  status,
  onClose,
  onPoll,
}: {
  open: boolean;
  busy: boolean;
  error: string;
  device: GrokDeviceOAuthStart | null;
  status: string;
  onClose: () => void;
  onPoll: () => void;
}) {
  if (!open || !device) return null;
  const verificationURL = device.verification_uri_complete || device.verification_uri;
  return (
    <div className="modal-backdrop" role="presentation">
      <div aria-labelledby="grok-device-oauth-title" aria-modal="true" className="confirm-modal" role="dialog">
        <h3 id="grok-device-oauth-title">{tx("授权 Super Grok 账号")}</h3>
        <p>{tx("在 xAI 页面确认设备码。完成后 TokenHub 会自动换取订阅 Token。")}</p>
        <div className="provider-account-detail-grid">
          <div className="provider-quota-metric">
            <span>{tx("用户码")}</span>
            <strong>{device.user_code}</strong>
          </div>
        </div>
        {status ? <p>{tx(status)}</p> : null}
        {error ? <p className="field-error">{tx(error)}</p> : null}
        <div className="modal-actions">
          <button onClick={() => window.open(verificationURL, "_blank", "noopener,noreferrer")} type="button">
            <ExternalLink size={14} /> {tx("打开授权页")}
          </button>
          <button onClick={async () => { await copyText(device.user_code); }} type="button">
            <Copy size={14} /> {tx("复制用户码")}
          </button>
          <button disabled={busy} onClick={onPoll} type="button">{tx("刷新授权状态")}</button>
          <button disabled={busy} onClick={onClose} type="button">{tx("取消")}</button>
        </div>
      </div>
    </div>
  );
}
