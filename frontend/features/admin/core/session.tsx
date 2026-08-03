import { type AdminUser, oauthBaseURLStorageKey, sessionStorageKey } from "./types";
import { parseProviderAccountOAuthResult, type ProviderAccountOAuthGenerateResponse, type ProviderAccountOAuthResult } from "./session-oauth";

export type { ProviderAccountOAuthGenerateResponse, ProviderAccountOAuthResult } from "./session-oauth";
export { parseProviderAccountOAuthResult } from "./session-oauth";

export type SavedSession = {
  baseURL: string;
  token: string;
  user: AdminUser;
  expiresAt: string;
};

export type OAuthLoginResult = {
  token?: string;
  expiresAt?: string;
  error?: string;
};

export function readOAuthLoginResult(): OAuthLoginResult | null {
  if (typeof window === "undefined") return null;
  const sources = [window.location.hash.replace(/^#/, ""), window.location.search.replace(/^\?/, "")];
  for (const source of sources) {
    if (!source) continue;
    const params = new URLSearchParams(source);
    const token = params.get("oauth_token") ?? "";
    const error = params.get("oauth_error") ?? "";
    if (token || error) {
      return {
        token,
        error,
        expiresAt: params.get("oauth_expires_at") ?? undefined,
      };
    }
  }
  return null;
}

export const providerAccountOAuthStorageKey = "tokenhub_provider_account_oauth_result";

export const providerAccountOAuthSessionStorageKey = "tokenhub_provider_account_oauth_session";

export function providerAccountOAuthCallbackURL() {
  if (typeof window === "undefined") return "";
  const url = new URL(window.location.href);
  url.hash = "";
  url.search = "";
  url.searchParams.set("provider_account_oauth", "1");
  return url.toString();
}

export function readProviderAccountOAuthResultFromLocation() {
  if (typeof window === "undefined") return null;
  const search = window.location.search.replace(/^\?/, "");
  const hash = window.location.hash.replace(/^#/, "");
  const sources = [search, hash, [search, hash].filter(Boolean).join("&")];
  for (const source of sources) {
    const result = parseProviderAccountOAuthResult(source, false);
    if (result) return result;
  }
  return null;
}

export function clearProviderAccountOAuthResultFromLocation() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  let changed = false;
  for (const key of [
    "provider_account_oauth",
    "tokenhub_provider_account",
    "provider_account_oauth_session_id",
    "account_oauth_session_id",
    "session_id",
    "provider_account_oauth_state",
    "account_oauth_state",
    "provider_account_oauth_error",
    "oauth_error",
    "error",
    "account_access_token",
    "provider_access_token",
    "account_refresh_token",
    "account_id_token",
    "account_email",
    "email",
    "login",
    "username",
    "account_id",
    "sub",
    "user_id",
    "organization_id",
    "org_id",
    "plan_type",
    "plan",
    "authorization_code",
    "code",
  ]) {
    if (url.searchParams.has(key)) {
      url.searchParams.delete(key);
      changed = true;
    }
  }
  if (url.hash) {
    const hashParams = new URLSearchParams(url.hash.replace(/^#/, ""));
    let hashChanged = false;
    for (const key of [
      "provider_account_oauth",
      "tokenhub_provider_account",
      "provider_account_oauth_session_id",
      "account_oauth_session_id",
      "session_id",
      "provider_account_oauth_state",
      "account_oauth_state",
      "provider_account_oauth_error",
      "oauth_error",
      "error",
      "access_token",
      "refresh_token",
      "id_token",
      "account_access_token",
      "account_refresh_token",
      "account_id_token",
      "account_email",
      "account_id",
      "email",
      "login",
      "username",
      "sub",
      "user_id",
      "organization_id",
      "org_id",
      "plan_type",
      "plan",
      "code",
      "authorization_code",
    ]) {
      if (hashParams.has(key)) {
        hashParams.delete(key);
        hashChanged = true;
      }
    }
    if (hashChanged) {
      const nextHash = hashParams.toString();
      url.hash = nextHash ? `#${nextHash}` : "";
      changed = true;
    }
  }
  if (changed) {
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  }
}

export function savePendingProviderAccountOAuthResult(result: ProviderAccountOAuthResult) {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(providerAccountOAuthStorageKey, JSON.stringify(result));
}

export function savePendingProviderAccountOAuthSession(result: Pick<ProviderAccountOAuthResult, "session_id" | "state">) {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(providerAccountOAuthSessionStorageKey, JSON.stringify(result));
}

export function readPendingProviderAccountOAuthSession() {
  if (typeof window === "undefined") return null;
  const raw = window.sessionStorage.getItem(providerAccountOAuthSessionStorageKey);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as Pick<ProviderAccountOAuthResult, "session_id" | "state">;
  } catch {
    return null;
  }
}

export function clearPendingProviderAccountOAuthSession() {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(providerAccountOAuthSessionStorageKey);
}

export function hasPendingProviderAccountOAuthResult() {
  if (typeof window === "undefined") return false;
  return Boolean(window.sessionStorage.getItem(providerAccountOAuthStorageKey));
}

export function consumePendingProviderAccountOAuthResult() {
  if (typeof window === "undefined") return null;
  const raw = window.sessionStorage.getItem(providerAccountOAuthStorageKey);
  if (!raw) return null;
  window.sessionStorage.removeItem(providerAccountOAuthStorageKey);
  try {
    return JSON.parse(raw) as ProviderAccountOAuthResult;
  } catch {
    return null;
  }
}

export function clearOAuthLoginResult() {
  if (typeof window === "undefined") return;
  const url = new URL(window.location.href);
  let changed = false;
  for (const key of ["oauth_token", "oauth_expires_at", "oauth_error"]) {
    if (url.searchParams.has(key)) {
      url.searchParams.delete(key);
      changed = true;
    }
  }
  if (url.hash) {
    const hashParams = new URLSearchParams(url.hash.replace(/^#/, ""));
    let hashChanged = false;
    for (const key of ["oauth_token", "oauth_expires_at", "oauth_error"]) {
      if (hashParams.has(key)) {
        hashParams.delete(key);
        hashChanged = true;
      }
    }
    if (hashChanged) {
      const nextHash = hashParams.toString();
      url.hash = nextHash ? `#${nextHash}` : "";
      changed = true;
    }
  }
  if (changed) {
    window.history.replaceState(window.history.state, "", `${url.pathname}${url.search}${url.hash}`);
  }
}

export function isOAuthAuthorizationResponse() {
  if (typeof window === "undefined") return false;
  const params = new URLSearchParams(window.location.search.replace(/^\?/, ""));
  return Boolean(params.get("state") && (params.get("code") || params.get("error")));
}

export function isProviderAccountOAuthAuthorizationResponse() {
  if (typeof window === "undefined") return false;
  const params = new URLSearchParams(window.location.search.replace(/^\?/, ""));
  return params.get("provider_account_oauth") === "1" || params.has("provider_account_oauth_session_id");
}

export function forwardOAuthAuthorizationResponse(baseURL: string) {
  if (isProviderAccountOAuthAuthorizationResponse()) return false;
  if (typeof window === "undefined" || !isOAuthAuthorizationResponse()) return false;
  const target = new URL(`${baseURL.replace(/\/$/, "")}/api/admin/auth/oauth/callback`);
  target.search = window.location.search;
  window.location.replace(target.toString());
  return true;
}

export function readPendingOAuthBaseURL() {
  if (typeof window === "undefined") return null;
  return window.sessionStorage.getItem(oauthBaseURLStorageKey);
}

export function savePendingOAuthBaseURL(baseURL: string) {
  if (typeof window === "undefined") return;
  window.sessionStorage.setItem(oauthBaseURLStorageKey, baseURL);
}

export function clearPendingOAuthBaseURL() {
  if (typeof window === "undefined") return;
  window.sessionStorage.removeItem(oauthBaseURLStorageKey);
}

export function readSavedSession(): SavedSession | null {
  if (typeof window === "undefined") return null;
  const raw = window.localStorage.getItem(sessionStorageKey);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as SavedSession;
    if (!parsed.token || !parsed.user || new Date(parsed.expiresAt).getTime() <= Date.now()) {
      window.localStorage.removeItem(sessionStorageKey);
      return null;
    }
    return parsed;
  } catch {
    window.localStorage.removeItem(sessionStorageKey);
    return null;
  }
}

export function saveSession(session: SavedSession) {
  if (typeof window === "undefined") return;
  window.localStorage.setItem(sessionStorageKey, JSON.stringify(session));
}

export function clearSavedSession() {
  if (typeof window === "undefined") return;
  window.localStorage.removeItem(sessionStorageKey);
}
