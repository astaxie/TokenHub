import { AlertCircle, Check, Copy, KeyRound, Send } from "lucide-react";
import { useState } from "react";
import type { ProviderResource } from "../core/types";
import { copyText } from "../domain/clipboard";
import { tx } from "../i18n/runtime";

export { ProviderAccountTokenRenewal } from "./provider-account-token-renewal";

export type ProviderAccountOAuthAction = "copy" | "open";

export async function launchProviderAccountAuthorization(action: ProviderAccountOAuthAction, authURL: string) {
  if (action === "open") {
    window.open(authURL, "_blank", "noopener,noreferrer");
    return;
  }
  if (!await copyText(authURL)) throw new Error(tx("复制授权链接失败，请允许浏览器访问剪贴板后重试。"));
}

export type OpenAIQuotaWindow = {
  used_percent: number;
  limit_window_seconds: number;
  reset_after_seconds: number;
  reset_at: number;
};

export function QuotaMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="provider-quota-metric">
      <span>{tx(label)}</span>
      <strong>{tx(value)}</strong>
    </div>
  );
}

export function ProviderAccountDetails({ resource }: { resource: ProviderResource }) {
  const summary = resource.credential_summary ?? {};
  const options = resource.options ?? {};
  const rawItems: Array<[string, string | undefined]> = [
    ["资源 ID", resource.id],
    ["账号邮箱", summary.account_email],
    ["账号 ID", summary.account_id],
    ["用户 ID", summary.user_id],
    ["组织 ID", summary.organization_id],
    ["套餐", summary.plan_type],
    ["生图能力", formatImageGenerationCapability(options.image_generation_capability)],
    ["生图能力检查时间", formatProviderAccountDate(options.image_generation_capability_checked_at)],
    ["认证方式", summary.auth_type],
    ["Token 类型", summary.token_type || options.token_type],
    ["Token 过期时间", formatProviderAccountDate(summary.token_expires_at || options.token_expires_at)],
    ["授权范围", summary.scopes || options.scopes],
    ["Refresh Token", summary.has_refresh_token === "true" ? "已配置" : "未配置"],
    ["资源状态", resource.status],
    ["健康状态", resource.healthy ? "健康" : "异常"],
    ["资源组", resource.group],
    ["Base URL", resource.base_url],
    ["区域", resource.region],
    ["环境", resource.environment],
    ["优先级", String(resource.priority)],
    ["权重", String(resource.weight)],
    ["每分钟请求限制", resource.rate_limit_rpm ? String(resource.rate_limit_rpm) : "-"],
    ["每分钟 Token 限制", resource.token_limit_tpm ? String(resource.token_limit_tpm) : "-"],
    ["最大并发", resource.max_concurrency ? String(resource.max_concurrency) : "-"],
    ["连续失败次数", String(resource.failure_count ?? 0)],
    ["冷却截止时间", formatProviderAccountDate(resource.cooldown_until)],
    ["最后使用时间", formatProviderAccountDate(resource.last_used_at)],
    ["最后检查时间", formatProviderAccountDate(resource.last_checked_at)],
    ["创建时间", formatProviderAccountDate(resource.created_at)],
    ["更新时间", formatProviderAccountDate(resource.updated_at)],
  ];
  const items = rawItems.filter(([, value]) => value !== undefined && value !== "");
  return (
    <div className="provider-account-detail-grid">
      {items.map(([label, value]) => <QuotaMetric key={label} label={label} value={value || "-"} />)}
    </div>
  );
}

export function formatImageGenerationCapability(value?: string) {
  if (value === "supported") return "支持";
  if (value === "unsupported") return "不支持";
  return "未检测";
}

export function formatImageGenerationCapabilityTag(value?: string) {
  if (value === "supported") return "支持生图";
  if (value === "unsupported") return "不支持生图";
  return "生图未检测";
}

export function providerResourceAccountLabel(resource: ProviderResource) {
  return resource.credential_summary?.account_email || resource.credential_summary?.account_id || resource.name;
}

export function formatProviderAccountDate(value?: string) {
  if (!value) return "-";
  const timestamp = Date.parse(value);
  return Number.isNaN(timestamp) ? value : new Date(timestamp).toLocaleString();
}

export function formatQuotaPercent(value: number) {
  return Number.isFinite(value) ? String(Math.round(value * 10) / 10) : "0";
}

export function quotaUsagePercent(window?: OpenAIQuotaWindow) {
  if (!window || !Number.isFinite(window.used_percent)) return 0;
  return Math.min(100, Math.max(0, window.used_percent));
}

export function quotaWindowResetLabel(window?: OpenAIQuotaWindow) {
  if (!window) return "-";
  if (window.reset_at > 0) return new Date(window.reset_at * 1000).toLocaleString();
  if (window.reset_after_seconds > 0) {
    const minutes = Math.ceil(window.reset_after_seconds / 60);
    return minutes >= 60 ? `${Math.ceil(minutes / 60)} ${tx("小时后")}` : `${minutes} ${tx("分钟后")}`;
  }
  return "-";
}

export function ProviderOAuthNoticeModal({
  open,
  busy,
  error,
  onClose,
  onConfirm,
  onCopy,
}: {
  open: boolean;
  busy: boolean;
  error: string;
  onClose: () => void;
  onConfirm: () => Promise<void>;
  onCopy: () => Promise<void>;
}) {
  const [busyAction, setBusyAction] = useState<ProviderAccountOAuthAction | null>(null);
  const actionBusy = busy || busyAction !== null;
  async function runAction(action: ProviderAccountOAuthAction) {
    setBusyAction(action);
    try {
      await (action === "copy" ? onCopy() : onConfirm());
    } finally {
      setBusyAction(null);
    }
  }
  if (!open) return null;
  return (
    <div className="modal-backdrop provider-oauth-notice-backdrop" role="presentation">
      <div aria-labelledby="provider-oauth-notice-title" aria-modal="true" className="confirm-modal provider-oauth-notice-modal" role="dialog">
        <div className="provider-oauth-notice-heading">
          <div className="provider-oauth-notice-icon" aria-hidden="true"><AlertCircle size={20} /></div>
          <div>
            <p className="eyebrow">{tx("打开授权页前请确认")}</p>
            <h2 id="provider-oauth-notice-title">{tx("授权完成后需要复制地址并回填")}</h2>
          </div>
        </div>
        <div className="provider-oauth-notice-callout" role="note">
          <strong>{tx("请不要关闭 TokenHub 的当前页面")}</strong>
          <span>{tx("登录和授权完成后，请复制浏览器地址栏中的完整 localhost callback URL，再返回此处粘贴回填。")}</span>
        </div>
        <ol className="provider-oauth-notice-steps">
          <li>{tx("在即将打开的页面中登录 OpenAI/Codex 并完成授权。")}</li>
          <li>{tx("授权后 localhost 页面可能显示无法访问，这是正常现象。")}</li>
          <li>{tx("复制地址栏中的完整地址，返回 TokenHub 粘贴并确认回填。")}</li>
        </ol>
        {error ? <p className="provider-oauth-notice-error" role="alert">{error}</p> : null}
        <div className="modal-actions">
          <button className="secondary-button" disabled={actionBusy} onClick={onClose} type="button">{tx("取消")}</button>
          <button className="secondary-button" disabled={actionBusy} onClick={() => void runAction("copy")} type="button">
            <Copy size={15} />
            {tx(busyAction === "copy" ? "正在复制授权链接" : "我知道了，复制授权链接")}
          </button>
          <button className="button" disabled={actionBusy} onClick={() => void runAction("open")} type="button">
            <Send size={15} />
            {tx(busyAction === "open" ? "正在打开授权页" : "我知道了，打开授权页")}
          </button>
        </div>
      </div>
    </div>
  );
}

export function ProviderOAuthCallbackModal({
  open,
  busy,
  value,
  error,
  onValueChange,
  onClose,
  onConfirm,
}: {
  open: boolean;
  busy: boolean;
  value: string;
  error: string;
  onValueChange: (value: string) => void;
  onClose: () => void;
  onConfirm: () => void;
}) {
  if (!open) return null;
  return (
    <div className="modal-backdrop provider-oauth-callback-backdrop" role="presentation">
      <div aria-labelledby="provider-oauth-callback-title" aria-modal="true" className="confirm-modal provider-oauth-callback-modal" role="dialog">
        <div className="provider-oauth-callback-heading">
          <div className="provider-oauth-callback-icon" aria-hidden="true"><KeyRound size={18} /></div>
          <div>
            <p className="eyebrow">{tx("OpenAI/Codex 授权")}</p>
            <h2 id="provider-oauth-callback-title">{tx("粘贴授权回调地址")}</h2>
          </div>
        </div>
        <p>{tx("完成登录和授权后，请复制浏览器地址栏中的完整 localhost 地址，并粘贴到下方。")}</p>
        <label className="field">
          <span>{tx("登录后的 localhost 地址")}</span>
          <textarea
            autoFocus
            onChange={(event) => onValueChange(event.target.value)}
            placeholder="http://localhost:1455/auth/callback?code=...&state=..."
            value={value}
          />
          <small>{tx("localhost 页面显示无法访问是正常现象；请复制地址栏中的完整地址，不要只复制授权 code。")}</small>
        </label>
        {error ? <p className="provider-oauth-callback-error" role="alert">{error}</p> : null}
        <div className="modal-actions">
          <button className="secondary-button" onClick={onClose} type="button">{tx("稍后填写")}</button>
          <button className="button" disabled={busy} onClick={onConfirm} type="button">
            <Check size={15} />
            {tx(busy ? "处理中" : "确认并回填")}
          </button>
        </div>
      </div>
    </div>
  );
}
