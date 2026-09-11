export type ProviderAccountOAuthResult = {
  access_token?: string;
  refresh_token?: string;
  id_token?: string;
  session_id?: string;
  state?: string;
  account_email?: string;
  account_id?: string;
  organization_id?: string;
  plan_type?: string;
  token_type?: string;
  expires_at?: string;
  scopes?: string;
  authorization_code?: string;
  error?: string;
};

export type ProviderAccountOAuthGenerateResponse = {
  auth_url: string;
  session_id: string;
  state: string;
  redirect_uri: string;
  expires_at: string;
};

export function parseProviderAccountOAuthResult(source: string, allowGenericTokenNames = false): ProviderAccountOAuthResult | null {
  const raw = source.trim();
  if (!raw) return null;
  const candidates: string[] = [];
  try {
    const url = new URL(raw);
    const search = url.search.replace(/^\?/, "");
    const hash = url.hash.replace(/^#/, "");
    candidates.push(search);
    candidates.push(hash);
    candidates.push([search, hash].filter(Boolean).join("&"));
  } catch {
    candidates.push(raw.replace(/^[?#]/, ""));
  }
  for (const candidate of candidates) {
    if (!candidate || !candidate.includes("=")) continue;
    const params = new URLSearchParams(candidate);
    const marked = allowGenericTokenNames || params.get("provider_account_oauth") === "1" || params.get("tokenhub_provider_account") === "1";
    const result: ProviderAccountOAuthResult = {};
    result.access_token = firstParam(params, marked ? ["account_access_token", "provider_access_token", "access_token", "token"] : ["account_access_token", "provider_access_token"]);
    result.refresh_token = firstParam(params, marked ? ["account_refresh_token", "refresh_token"] : ["account_refresh_token"]);
    result.id_token = firstParam(params, marked ? ["account_id_token", "id_token"] : ["account_id_token"]);
    result.session_id = firstParam(params, ["provider_account_oauth_session_id", "account_oauth_session_id", "session_id"]);
    result.state = firstParam(params, ["provider_account_oauth_state", "account_oauth_state", "state"]);
    result.error = firstParam(params, ["provider_account_oauth_error", "oauth_error", "error"]);
    result.account_email = firstParam(params, ["account_email", "email", "login", "username"]);
    result.account_id = firstParam(params, ["account_id", "sub", "user_id"]);
    result.organization_id = firstParam(params, ["organization_id", "org_id"]);
    result.plan_type = firstParam(params, ["plan_type", "plan"]);
    result.token_type = firstParam(params, ["token_type"]);
    result.expires_at = firstParam(params, ["expires_at", "token_expires_at"]);
    result.scopes = firstParam(params, ["scope", "scopes"]);
    result.authorization_code = firstParam(params, ["code", "authorization_code"]);
    if (marked && result.error) return result;
    if (result.access_token || result.refresh_token || result.id_token) return result;
    if (marked && result.authorization_code) return result;
  }
  return null;
}

export function firstParam(params: URLSearchParams, keys: string[]) {
  for (const key of keys) {
    const value = params.get(key)?.trim();
    if (value) return value;
  }
  return "";
}
