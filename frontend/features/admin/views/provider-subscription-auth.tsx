import { useEffect, useRef, useState } from "react";
import type { ApiContext, PluginActionDescriptor } from "../core/types";
import { parseProviderAccountOAuthResult, type ProviderAccountOAuthResult } from "../core/session";
import { providerPluginActionForCapability, runProviderCapabilityAction } from "../resources/provider-model-config";
import { tx } from "../i18n/runtime";
import { subscriptionAuthLabel } from "../i18n/subscription-auth-labels";
import { supportsLocalSubscriptionCallback } from "../domain/provider-local-callback";

type LoginMethod = { id: string; method_id?: string; label: string; type: string; prompts?: Array<{ key: string; label: string; type?: string; options?: Array<{ value: string; label: string }> }> };
type LoginSession = { session_id: string; state: string; registration_id?: string; auth_url?: string; url?: string; flow?: string; user_code?: string; poll_interval?: number; expires_at?: string; instructions?: string; local_listener?: boolean; callback_mode?: string };

export function supportsSubscriptionAuthorization(actions: PluginActionDescriptor[], providerType: string) {
  return providerPluginActionForCapability(actions, providerType, "oauth.start")?.metadata?.subscription_auth === "true";
}

export function supportsSubscriptionReauthorization(actions: PluginActionDescriptor[], providerType: string) {
  return supportsSubscriptionAuthorization(actions, providerType) || providerType === "openai_codex"
    && Boolean(providerPluginActionForCapability(actions, providerType, "oauth.start") && providerPluginActionForCapability(actions, providerType, "oauth.exchange"));
}

function validAuthorizationURL(raw: string, localListener?: boolean) {
  const url = new URL(raw);
  if (url.username || url.password) return false;
  if (url.protocol === "https:") return true;
  return localListener === true && url.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(url.hostname) && Boolean(url.port) && !url.hash;
}

function subscriptionAuthorizationError(error: unknown) {
  if (!(error instanceof Error)) return tx("账号授权失败");
  return error.message === "Subscription authorization failed or expired; start again"
    ? tx("未能取得账号凭据，授权可能失败或已过期。请重新打开授权并在网页完成登录；无需手动复制 Token。")
    : error.message;
}

const subscriptionCredentialKeys = ["session_json", "client_id", "client_secret", "access_token", "refresh_token", "id_token", "account_email", "account_id", "user_id", "organization_id", "plan_type", "token_type", "expires_at", "scopes"] as const;

export function subscriptionCredentialIdentity(providerType: string, catalogID: string, values: Record<string, string>) {
  return JSON.stringify([providerType, catalogID, values.auth_type || "", values.resource_type || "", ...subscriptionCredentialKeys.map(key => values[key] || "")]);
}

export function subscriptionCredentialValues(result: ProviderAccountOAuthResult, replace = false) {
  const values: Record<string, string> = replace ? { replace_credentials: "true" } : {};
  for (const key of subscriptionCredentialKeys) {
    if (result[key] || replace) values[key] = result[key] || "";
  }
  return values;
}

export function ProviderSubscriptionAuth({ api, actions, providerType, disabled, organizationID, resourceID, onCredentials }: {
  api: ApiContext;
  actions: PluginActionDescriptor[];
  providerType: string;
  disabled?: boolean;
  organizationID?: string;
  resourceID?: string;
  onCredentials: (credentials: ProviderAccountOAuthResult) => void | Promise<void>;
}) {
  const [methods, setMethods] = useState<LoginMethod[]>([]);
  const [methodID, setMethodID] = useState("");
  const [inputs, setInputs] = useState<Record<string, string>>({});
  const [session, setSession] = useState<LoginSession | null>(null);
  const [callback, setCallback] = useState("");
  const [busy, setBusy] = useState(false);
  const [polling, setPolling] = useState(false);
  const exchangeInFlight = useRef<object | null>(null);
  const [error, setError] = useState("");
  const [complete, setComplete] = useState(false);
  const [importedSession, setImportedSession] = useState("");
  const generation = useRef(0);
  const activeSession = useRef<LoginSession | null>(null);
  const registrationID = useRef("");
  const onCredentialsRef = useRef(onCredentials);
  useEffect(() => { onCredentialsRef.current = onCredentials; }, [onCredentials]);
  const startAction = providerPluginActionForCapability(actions, providerType, "oauth.start");
  const exchangeAction = providerPluginActionForCapability(actions, providerType, "oauth.exchange");
  const methodsAction = providerPluginActionForCapability(actions, providerType, "oauth.methods");
  const cancelAction = providerPluginActionForCapability(actions, providerType, "oauth.cancel");
  const opaqueSession = startAction?.metadata?.subscription_session_json === "true";

  useEffect(() => () => {
    const pending = activeSession.current;
    activeSession.current = null;
    registrationID.current = "";
    exchangeInFlight.current = null;
    if (pending && cancelAction) void runProviderCapabilityAction(api, providerType, "oauth.cancel", cancelAction, { session_id: pending.session_id, state: pending.state }, tx("取消")).catch(() => {});
  }, [api, cancelAction, providerType, resourceID]);

  function cancelSession(clearRegistration = true) {
    const pending = activeSession.current;
    generation.current += 1;
    activeSession.current = null;
    if (clearRegistration) registrationID.current = "";
    setSession(null); setCallback(""); setInputs({}); setBusy(false); setPolling(false); exchangeInFlight.current = null;
    if (pending && cancelAction) void runProviderCapabilityAction(api, providerType, "oauth.cancel", cancelAction, { session_id: pending.session_id, state: pending.state }, tx("取消")).catch(() => {});
  }

  useEffect(() => {
    const current = ++generation.current;
    if (methodsAction) {
      void runProviderCapabilityAction<{ methods: LoginMethod[] }>(api, providerType, "oauth.methods", methodsAction, {}, tx("加载登录方式"))
        .then((result) => {
          if (generation.current !== current) return;
          setMethods(result.methods ?? []);
          setMethodID(result.methods?.[0]?.method_id || result.methods?.[0]?.id || "");
        }).catch(() => { if (generation.current === current) setError(tx("登录方式加载失败，可手动导入订阅会话。")); });
    }
    return () => { generation.current += 1; };
  }, [api, methodsAction, providerType]);

  useEffect(() => {
    if (busy || !session || !exchangeAction || !["device_code", "auto"].includes(session.flow ?? "")) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;
    let interval = Math.max(2, session.poll_interval ?? 5);
    const deadline = session.expires_at ? Date.parse(session.expires_at) : Date.now() + 10 * 60_000;
    const poll = async () => {
      if (cancelled) return;
      if (exchangeInFlight.current) { timer = setTimeout(() => { void poll(); }, interval * 1000); return; }
      if (Date.now() >= deadline) { setError(tx("授权已过期，请重新登录。")); activeSession.current = null; setSession(null); return; }
      const operation = {}; exchangeInFlight.current = operation; setPolling(true);
      try {
        const result = await runProviderCapabilityAction<ProviderAccountOAuthResult>(api, providerType, "oauth.exchange", exchangeAction, { session_id: session.session_id, state: session.state, code: "" }, tx("账号授权换取 Token"));
        if (cancelled) return;
        if (result.status === "pending" || result.status === "slow_down") {
          interval = Math.max(interval, result.poll_interval ?? (result.status === "slow_down" ? interval + 5 : interval));
          timer = setTimeout(() => { void poll(); }, interval * 1000);
          return;
        }
        activeSession.current = null;
        await onCredentialsRef.current(result);
        if (!cancelled) { setComplete(true); setSession(null); }
      } catch (err) {
        if (!cancelled) {
          setError(subscriptionAuthorizationError(err));
          const pending = activeSession.current;
          activeSession.current = null; setSession(null); setCallback("");
          if (pending && cancelAction) void runProviderCapabilityAction(api, providerType, "oauth.cancel", cancelAction, { session_id: pending.session_id, state: pending.state }, tx("取消")).catch(() => {});
        }
      } finally {
        if (exchangeInFlight.current === operation) { exchangeInFlight.current = null; setPolling(false); }
      }
    };
    timer = setTimeout(() => { void poll(); }, interval * 1000);
    return () => { cancelled = true; clearTimeout(timer); };
  }, [api, busy, cancelAction, exchangeAction, providerType, session]);

  async function start() {
    if (!startAction) return;
    setBusy(true); setError(""); setComplete(false);
    const current = generation.current;
    try {
      const automaticCallback = supportsLocalSubscriptionCallback(providerType, api.baseURL, typeof window === "undefined" ? undefined : window.location.href);
      const result = await runProviderCapabilityAction<LoginSession & ProviderAccountOAuthResult>(api, providerType, "oauth.start", startAction, { method_id: methodID, inputs, key: inputs.key, organization_id: organizationID, redirect_uri: startAction.metadata?.oauth_redirect_uri, ...(automaticCallback ? { automatic_callback: true } : {}), ...(providerType === "chatgpt-subscription" ? { resource_id: resourceID, ...(registrationID.current ? { registration_id: registrationID.current } : {}) } : {}) }, tx("生成账号授权地址"));
      if (generation.current !== current) {
        if (result.session_id && cancelAction) void runProviderCapabilityAction(api, providerType, "oauth.cancel", cancelAction, { session_id: result.session_id, state: result.state }, tx("取消")).catch(() => {});
        return;
      }
      setInputs({});
      if (providerType === "chatgpt-subscription" && typeof result.registration_id === "string") registrationID.current = result.registration_id;
      if (result.session_json || result.access_token) { await onCredentialsRef.current(result); setComplete(true); return; }
      const authURL = result.auth_url || result.url || "";
      if (authURL && !validAuthorizationURL(authURL, result.local_listener)) throw new Error(tx("授权地址无效。"));
      activeSession.current = { ...result, flow: result.flow ?? "authorization_code", auth_url: authURL };
      setSession(activeSession.current);
      if (authURL) window.open(authURL, "_blank", "noopener,noreferrer");
    } catch (err) {
      if (generation.current === current) setError(subscriptionAuthorizationError(err));
    } finally { if (generation.current === current) setBusy(false); }
  }

  async function exchange() {
    if (!session || !exchangeAction || exchangeInFlight.current) return;
    const operation = {}; exchangeInFlight.current = operation;
    setBusy(true); setError("");
    const current = generation.current;
    try {
      const parsed = parseProviderAccountOAuthResult(callback, true);
      const result = await runProviderCapabilityAction<ProviderAccountOAuthResult>(api, providerType, "oauth.exchange", exchangeAction, {
        session_id: session.session_id, state: opaqueSession ? session.state : parsed?.state || session.state,
        code: parsed?.authorization_code || callback.trim(), client_id: parsed?.client_id,
        callback_url: callback.trim(),
      }, tx("账号授权换取 Token"));
      if (generation.current !== current) return;
      if (result.status === "pending" || result.status === "slow_down") return;
      activeSession.current = null;
      await onCredentialsRef.current(result);
      setCallback(""); setSession(null); setComplete(true);
    } catch (err) {
      if (generation.current === current) {
        if (!opaqueSession && session.flow === "authorization_code") cancelSession(false);
        setError(subscriptionAuthorizationError(err));
      }
    }
    finally { if (exchangeInFlight.current === operation) exchangeInFlight.current = null; if (generation.current === current) setBusy(false); }
  }

  async function importSession() {
    if (!exchangeAction) return;
    setBusy(true); setError("");
    const current = generation.current;
    try {
      const parsed: unknown = JSON.parse(importedSession);
      if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) throw new Error(tx("订阅会话必须是 JSON 对象。"));
      const result = await runProviderCapabilityAction<ProviderAccountOAuthResult>(api, providerType, "oauth.exchange", exchangeAction, { mode: "import", session_json: JSON.stringify(parsed) }, tx("导入订阅会话"));
      if (generation.current !== current) return;
      await onCredentialsRef.current(result);
      setImportedSession(""); setComplete(true);
    } catch (err) { if (generation.current === current) setError(err instanceof Error ? err.message : tx("账号授权失败")); }
    finally { if (generation.current === current) setBusy(false); }
  }

  const method = methods.find((item) => (item.method_id || item.id) === methodID);
  return <div className="provider-account-auth-wide">
    {methods.length > 1 ? <label className="field"><span>{tx("登录方式")}</span><select value={methodID} disabled={disabled || busy || Boolean(session)} onChange={(event) => { setMethodID(event.target.value); setInputs({}); }}>{methods.map((item) => <option key={item.method_id || item.id} value={item.method_id || item.id}>{subscriptionAuthLabel(providerType, item.label)}</option>)}</select></label> : null}
    {method?.prompts?.map((prompt) => <label className="field" key={prompt.key}><span>{subscriptionAuthLabel(providerType, prompt.label)}</span>{prompt.type === "select" ? <select disabled={disabled || busy || Boolean(session)} value={inputs[prompt.key] ?? ""} onChange={(event) => setInputs((current) => ({ ...current, [prompt.key]: event.target.value }))}><option value="">{tx("请选择")}</option>{prompt.options?.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select> : <input type={prompt.type === "password" ? "password" : "text"} autoComplete={prompt.type === "password" ? "new-password" : "off"} disabled={disabled || busy || Boolean(session)} value={inputs[prompt.key] ?? ""} onChange={(event) => setInputs((current) => ({ ...current, [prompt.key]: event.target.value }))} />}</label>)}
    <button className="button provider-authorization-button" type="button" disabled={disabled || busy || Boolean(session) || (Boolean(methodsAction) && methods.length === 0)} onClick={() => { void start(); }}>{tx(busy ? "授权中" : "打开授权")}</button>
    {session ? <div role="status">
      {session.auth_url ? <p><a href={session.auth_url} target="_blank" rel="noopener noreferrer">{tx("打开授权页面")}</a></p> : null}
      {session.local_listener ? <p>{tx("此登录页运行在 TokenHub 服务器本机；远程使用时需转发对应端口。")}</p> : null}
      {session.instructions ? <p>{session.instructions}</p> : null}
      {providerType === "openai_codex" ? <p>{tx("授权完成后，即使 localhost 页面显示无法访问，也请复制地址栏中的完整 callback URL 粘贴到这里。")}</p> : null}
      {session.user_code ? <p>{tx("设备授权码")} <strong>{session.user_code}</strong></p> : null}
      {session.flow === "auto" && session.callback_mode === "none" ? <p>{tx("请在供应商网页完成登录后返回这里。TokenHub 会自动回填账号凭据，无需粘贴授权码。回填后请保存账号资源。")}</p> : ["device_code", "auto"].includes(session.flow ?? "") ? <p>{tx("请在授权页面完成登录，TokenHub 会自动检查结果。")}</p> : null}
      {session.flow !== "device_code" && session.callback_mode !== "none" ? <label className="field"><span>{tx("回调结果")}</span><textarea value={callback} onChange={(event) => setCallback(event.target.value)} /><button className="secondary-button" type="button" disabled={busy || polling || !callback.trim()} onClick={() => { void exchange(); }}>{tx("完成授权")}</button></label> : null}
      <button className="secondary-button" type="button" onClick={() => cancelSession()}>{tx("取消")}</button>
    </div> : null}
    {opaqueSession ? <details><summary>{tx("导入订阅会话")}</summary><label className="field"><span>{tx("订阅会话 JSON")}</span><textarea value={importedSession} onChange={(event) => setImportedSession(event.target.value)} /><small>{tx("仅粘贴本人授权的完整会话 JSON；将加密保存，保存后不会再次显示。")}</small></label><button className="secondary-button" type="button" disabled={disabled || busy || Boolean(session) || !importedSession.trim()} onClick={() => { void importSession(); }}>{tx("导入订阅会话")}</button></details> : null}
    {complete ? <div role="status"><p>{tx("已回填账号 Token")}</p><p>{tx("账号凭据已回填，请保存账号资源以完成接入。")}</p></div> : null}
    {error ? <p role="alert">{error}</p> : null}
  </div>;
}
