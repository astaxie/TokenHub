import { useEffect, useState } from "react";
import type { APIKey } from "../core/types";
import { accessProtocols, apiKeyAccessConfig, apiKeyPlaceholder, type AccessProtocol } from "../domain/api-key-access";
import { copyText } from "../domain/clipboard";
import { tx } from "../i18n/runtime";
import { issuedKeyCloseCountdownLabel } from "./ui";
import { useModalFocus } from "./modal-focus";

const accessEvent = "tokenhub-use-key";

export function openAPIKeyAccess(key: APIKey) {
  window.dispatchEvent(new CustomEvent<APIKey>(accessEvent, { detail: key }));
}

export function APIKeyAccessDialogs({ baseURL, issuedKey, onCloseIssuedKey }: {
  baseURL: string; issuedKey: string; onCloseIssuedKey: () => void;
}) {
  const [selected, setSelected] = useState<APIKey | null>(null);
  useEffect(() => {
    const open = (event: Event) => setSelected((event as CustomEvent<APIKey>).detail);
    window.addEventListener(accessEvent, open);
    return () => window.removeEventListener(accessEvent, open);
  }, []);
  if (!issuedKey && !selected) return null;
  return <APIKeyAccessModal key={issuedKey ? "issued" : selected!.id} baseURL={baseURL} secret={issuedKey}
    apiKey={issuedKey ? undefined : selected!} onClose={() => { setSelected(null); onCloseIssuedKey(); }} />;
}

export function APIKeyAccessModal({ baseURL, secret = "", apiKey, onClose }: {
  baseURL: string; secret?: string; apiKey?: APIKey; onClose: () => void;
}) {
  const [protocol, setProtocol] = useState<AccessProtocol>("chat");
  const [model, setModel] = useState("");
  const [countdown, setCountdown] = useState(secret ? 3 : 0);
  const focus = useModalFocus(secret ? undefined : onClose);
  const config = apiKeyAccessConfig(baseURL, protocol, secret, model);
  const unusable = apiKey && (apiKey.status !== "active" || (apiKey.expires_at && Date.parse(apiKey.expires_at) <= Date.now()));

  useEffect(() => {
    if (countdown <= 0) return;
    const timer = window.setTimeout(() => setCountdown(current => current - 1), 1000);
    return () => window.clearTimeout(timer);
  }, [countdown]);

  return <div className="modal-backdrop" role="presentation">
    <div className="api-key-access-modal" role="dialog" aria-modal="true" aria-labelledby="key-access-title" {...focus}>
      <header className="api-key-access-header">
        <h2 id="key-access-title">{tx("使用 API Key")}</h2>
        <p>{secret ? tx("新 Key 已生成。复制接入信息，即可配置客户端。") : apiKey?.name}</p>
      </header>
      <div className="api-key-access-body">
        <p className="api-key-access-notice">{secret
          ? tx("请现在复制并保存完整 Key。关闭此窗口后无法再次查看。")
          : tx("完整 Key 不再展示。请使用创建时保存的 Key，替换示例中的 YOUR_TOKENHUB_API_KEY。")}</p>
        {unusable ? <p className="api-key-access-warning" role="status">{tx("此 Key 已停用、吊销或过期，不能用于发起请求。")}</p> : null}
        <label className="field"><span>{tx("接入协议")}</span>
          <select value={protocol} onChange={event => setProtocol(event.target.value as AccessProtocol)}>
            {accessProtocols.map(item => <option key={item.id} value={item.id}>{item.label}</option>)}
          </select>
        </label>
        <CopyField label={tx("接入地址（Base URL）")} value={config.base} copyLabel={tx("复制地址")} />
        <CopyField label={secret ? tx("完整 Key") : tx("API Key 占位符")} value={secret || apiKeyPlaceholder} copyLabel={secret ? tx("复制 Key") : tx("复制占位符")} />
        {apiKey ? <p className="api-key-access-hint">{tx("当前 Key 标识")} <code>{apiKey.key_prefix}…{apiKey.key_suffix}</code></p> : null}
        <p className="api-key-access-hint">{tx("认证请求头")} <code>{config.authHeader}{config.authHeader === "Authorization" ? ": Bearer <API_KEY>" : ": <API_KEY>"}</code></p>
        <details className="api-key-access-example">
          <summary>{tx("请求示例（cURL）")}</summary>
          <label className="field"><span>{tx("模型 ID")}</span><input value={model} placeholder={tx("填写此 Key 可用的模型 ID")} onChange={event => setModel(event.target.value)} /></label>
          <p>{tx("协议支持不代表模型可用。请填写符合项目和 Key 权限的模型 ID，再运行示例。")}</p>
          <CopyField label={tx("请求地址")} value={config.endpoint} copyLabel={tx("复制请求地址")} />
          <CopyField label="cURL" value={config.example} copyLabel={tx("复制示例")} multiline />
        </details>
      </div>
      <footer className="api-key-access-footer">
        <button className="button" disabled={countdown > 0} onClick={onClose} type="button">{countdown > 0 ? issuedKeyCloseCountdownLabel(countdown) : secret ? tx("我已保存，关闭") : tx("关闭")}</button>
      </footer>
    </div>
  </div>;
}

function CopyField({ label, value, copyLabel, multiline = false }: { label: string; value: string; copyLabel: string; multiline?: boolean }) {
  const [result, setResult] = useState<{ value: string; ok: boolean } | null>(null);
  const current = result?.value === value ? result : null;
  return <div className="api-key-access-field">
    <label className="field"><span>{label}</span>{multiline
      ? <textarea aria-label={label} readOnly value={value} rows={6} onFocus={event => event.currentTarget.select()} spellCheck={false} />
      : <input aria-label={label} readOnly value={value} onFocus={event => event.currentTarget.select()} spellCheck={false} />}</label>
    <button className="secondary-button" type="button" onClick={async () => setResult({ value, ok: await copyText(value) })}>{copyLabel}</button>
    {current ? <span role="status" className="api-key-access-copy-status">{current.ok ? tx("已复制") : tx("复制失败，请选中文本手动复制。")}</span> : null}
  </div>;
}
