export type OAuthLoginResult = {
  code?: string;
  error?: string;
};

export type OAuthCallbackLocation = Pick<Location, "hash" | "search">;

export type PendingOAuthLogin = {
  baseURL: string;
  codeVerifier: string;
};

export type OAuthLoginStorage = Pick<Storage, "getItem" | "removeItem" | "setItem">;

type OAuthCrypto = Pick<Crypto, "getRandomValues" | "subtle">;

const oauthCodeVerifierBytes = 32;

export function parseOAuthLoginResult(location: OAuthCallbackLocation): OAuthLoginResult | null {
  const params = new URLSearchParams(location.hash.replace(/^#/, ""));
  const code = params.get("oauth_code")?.trim() ?? "";
  const error = params.get("oauth_error")?.trim() ?? "";
  return code || error ? { code, error } : null;
}

export type PendingOAuthLoginResult =
  | { status: "none" }
  | { status: "unexpected" }
  | { status: "ready"; baseURL: string; codeVerifier: string; result: OAuthLoginResult };

export function resolvePendingOAuthLoginResult(
  location: OAuthCallbackLocation,
  pendingLogin: PendingOAuthLogin | null,
): PendingOAuthLoginResult {
  const result = parseOAuthLoginResult(location);
  if (!result) return { status: "none" };
  const baseURL = pendingLogin?.baseURL.trim() ?? "";
  const codeVerifier = pendingLogin?.codeVerifier.trim() ?? "";
  if (!baseURL || !isOAuthCodeVerifier(codeVerifier)) return { status: "unexpected" };
  return { status: "ready", baseURL, codeVerifier, result };
}

export function readPendingOAuthLoginState(storageKey: string, storage: OAuthLoginStorage): PendingOAuthLogin | null {
  const raw = storage.getItem(storageKey);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as PendingOAuthLogin;
    if (!parsed.baseURL?.trim() || !isOAuthCodeVerifier(parsed.codeVerifier)) {
      storage.removeItem(storageKey);
      return null;
    }
    return parsed;
  } catch {
    storage.removeItem(storageKey);
    return null;
  }
}

export function savePendingOAuthLoginState(storageKey: string, pending: PendingOAuthLogin, storage: OAuthLoginStorage) {
  if (!pending.baseURL.trim() || !isOAuthCodeVerifier(pending.codeVerifier)) {
    throw new Error("Invalid pending OAuth login state");
  }
  storage.setItem(storageKey, JSON.stringify(pending));
}

export async function createOAuthLoginPKCE(cryptoProvider: OAuthCrypto = globalThis.crypto): Promise<{ codeVerifier: string; codeChallenge: string }> {
  if (!cryptoProvider?.getRandomValues || !cryptoProvider.subtle) {
    throw new Error("Web Crypto is unavailable");
  }
  const random = new Uint8Array(oauthCodeVerifierBytes);
  cryptoProvider.getRandomValues(random);
  const codeVerifier = base64URLEncode(random);
  const codeChallenge = await oauthCodeChallenge(codeVerifier, cryptoProvider);
  return { codeVerifier, codeChallenge };
}

export async function oauthCodeChallenge(codeVerifier: string, cryptoProvider: OAuthCrypto = globalThis.crypto): Promise<string> {
  if (!isOAuthCodeVerifier(codeVerifier) || !cryptoProvider?.subtle) {
    throw new Error("Invalid OAuth code verifier");
  }
  const digest = await cryptoProvider.subtle.digest("SHA-256", new TextEncoder().encode(codeVerifier));
  return base64URLEncode(new Uint8Array(digest));
}

export function buildOAuthLoginStartURL(
  baseURL: string,
  providerID: string,
  returnURL: string,
  codeChallenge?: string,
): string {
  const target = new URL(`${baseURL.replace(/\/$/, "")}/api/admin/auth/oauth/start`);
  target.searchParams.set("id", providerID);
  target.searchParams.set("return_url", returnURL);
  if (codeChallenge) {
    target.searchParams.set("code_challenge", codeChallenge);
    target.searchParams.set("code_challenge_method", "S256");
  }
  return target.toString();
}

function isOAuthCodeVerifier(value: string | undefined): value is string {
  return Boolean(value && value.length >= 43 && value.length <= 128 && /^[A-Za-z0-9._~-]+$/.test(value));
}

function base64URLEncode(value: Uint8Array): string {
  let binary = "";
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export type OAuthExchangeHTTPResult = {
  ok: boolean;
  status: number;
  body: string;
};

type OAuthFetch = (input: string | URL | Request, init?: RequestInit) => Promise<Response>;

let lastExchange: { key: string; request: Promise<OAuthExchangeHTTPResult> } | null = null;

export function exchangeOAuthLoginCode(
  baseURL: string,
  code: string,
  codeVerifier: string,
  fetcher: OAuthFetch = fetch,
): Promise<OAuthExchangeHTTPResult> {
  const normalizedBaseURL = baseURL.replace(/\/$/, "");
  const key = `${normalizedBaseURL}\n${code}\n${codeVerifier}`;
  if (lastExchange?.key === key) return lastExchange.request;

  const request = fetcher(`${normalizedBaseURL}/api/admin/auth/oauth/exchange`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ code, code_verifier: codeVerifier }),
  }).then(async (response) => ({
    ok: response.ok,
    status: response.status,
    body: await response.text(),
  }));
  lastExchange = { key, request };
  return request;
}
