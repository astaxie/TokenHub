import { AlertCircle, Check, Copy, Send } from "lucide-react";
import type { RefObject } from "react";
import { tx } from "../i18n/runtime";
import { isGrokAccountCatalog } from "./provider-account-catalog";

const openAIAccountOAuthRedirectURI = "http://localhost:1455/auth/callback";

export function ProviderAccountCreateAuth({
  accountNameInputRef,
  accountOAuthBusy,
  accountOAuthCallback,
  accountResourceNameConflict,
  accountTokenReady,
  accountTokenItems,
  catalogID,
  name,
  onAuthorize,
  onCallbackChange,
  onCopyCallbackURL,
  onNameChange,
  onParseCallback,
}: {
  accountNameInputRef: RefObject<HTMLInputElement | null>;
  accountOAuthBusy: boolean;
  accountOAuthCallback: string;
  accountResourceNameConflict: boolean;
  accountTokenReady: boolean;
  accountTokenItems: string[];
  catalogID: string;
  name: string;
  onAuthorize: () => void;
  onCallbackChange: (value: string) => void;
  onCopyCallbackURL: () => void;
  onNameChange: (value: string) => void;
  onParseCallback: () => void;
}) {
  const grok = isGrokAccountCatalog(catalogID);
  return (
    <>
      <div className="provider-account-inline-head">
        <strong>{tx("账号授权")}</strong>
        <span>{tx(grok
          ? "使用 Super Grok 设备码授权账号；TokenHub 会换取并保存订阅 Token。"
          : "使用 OpenAI/Codex OAuth 授权账号；TokenHub 会在后端换取并保存账号 Token。")}</span>
      </div>
      <div className="provider-account-auth-grid">
        <label className={`field provider-account-auth-wide provider-account-name-field${accountResourceNameConflict ? " conflict" : ""}`}>
          <span>{tx("账号资源名称")}</span>
          <input
            aria-invalid={accountResourceNameConflict}
            aria-describedby={accountResourceNameConflict ? "provider-account-name-conflict" : undefined}
            ref={accountNameInputRef}
            value={name}
            onChange={(event) => onNameChange(event.target.value)}
            required
          />
          {accountResourceNameConflict ? (
            <div className="provider-account-name-conflict" id="provider-account-name-conflict" role="alert">
              <AlertCircle aria-hidden="true" size={18} />
              <div>
                <strong>{tx("账号资源名称已存在，请立即修改名称")}</strong>
                <span>{tx("当前名称已被其他账号资源占用。请先修改为新的唯一名称，再继续授权。")}</span>
              </div>
            </div>
          ) : (
            <small>{tx("账号资源名称需全局唯一，用于在资源池中区分账号。")}</small>
          )}
        </label>
        <label className="field provider-account-auth-wide">
          <span>{tx(grok ? "Super Grok 授权" : "OpenAI/Codex 授权")}</span>
          <div className="field-action-row">
            {grok ? null : <input readOnly value={openAIAccountOAuthRedirectURI} />}
            <button className="secondary-button" onClick={onAuthorize} type="button" disabled={accountOAuthBusy}>
              <Send size={14} />
              {tx(accountOAuthBusy ? "授权中" : "打开授权")}
            </button>
          </div>
          <small>{tx(grok
            ? "点击后由后端开始 xAI 设备码授权。在 xAI 页面确认用户码；无需 localhost 回调。"
            : "点击后由后端生成授权地址。OpenAI 固定回调到 localhost:1455；无需该端口实际启动服务。")}</small>
        </label>
        {grok ? null : (
          <label className="field provider-account-auth-wide">
            <span>{tx("回调结果")}</span>
            <textarea
              value={accountOAuthCallback}
              onChange={(event) => onCallbackChange(event.target.value)}
              placeholder="http://localhost:1455/auth/callback?code=...&state=..."
            />
            <small>{tx("授权完成后，即使 localhost 页面显示无法访问，也请复制地址栏中的完整 callback URL 粘贴到这里。")}</small>
          </label>
        )}
        <div className="provider-account-auth-actions">
          {grok ? null : (
            <>
              <button className="secondary-button" onClick={onParseCallback} type="button">
                <Check size={14} />
                {tx("解析回填")}
              </button>
              <button className="secondary-button" onClick={onCopyCallbackURL} type="button">
                <Copy size={14} />
                {tx("复制固定回调地址")}
              </button>
            </>
          )}
          <div className={accountTokenReady ? "provider-account-token-status ready" : "provider-account-token-status"}>
            {accountTokenReady ? <Check size={15} /> : <AlertCircle size={15} />}
            <span>{tx(accountTokenReady ? "已回填账号 Token" : "等待授权回填")}</span>
            {accountTokenItems.map((item) => <em key={item}>{tx(item)}</em>)}
          </div>
        </div>
      </div>
    </>
  );
}
