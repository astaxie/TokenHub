import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  buildOAuthLoginStartURL,
  createOAuthLoginPKCE,
  exchangeOAuthLoginCode,
  oauthCodeChallenge,
  parseOAuthLoginResult,
  readPendingOAuthLoginState,
  resolvePendingOAuthLoginResult,
  savePendingOAuthLoginState,
} = await importTypeScript(new URL("./oauth-login.ts", import.meta.url));

const codeVerifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk";

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key) => values.get(key) ?? null,
    removeItem: (key) => values.delete(key),
    setItem: (key, value) => values.set(key, value),
    value: (key) => values.get(key) ?? null,
  };
}

test("ignores legacy administrator bearer tokens in the URL", () => {
  assert.equal(parseOAuthLoginResult({
    hash: "",
    search: "?oauth_token=long-lived-admin-token&oauth_expires_at=2099-01-01T00%3A00%3A00Z",
  }), null);
  assert.equal(parseOAuthLoginResult({
    hash: "#oauth_token=long-lived-admin-token",
    search: "",
  }), null);
});

test("accepts a one-time OAuth code only from the fragment", () => {
  assert.deepEqual(parseOAuthLoginResult({
    hash: "#oauth_code=single-use-code",
    search: "?oauth_code=query-code",
  }), { code: "single-use-code", error: "" });
  assert.equal(parseOAuthLoginResult({
    hash: "",
    search: "?oauth_code=query-code",
  }), null);
});

test("accepts OAuth errors only from the fragment", () => {
  assert.deepEqual(parseOAuthLoginResult({
    hash: "#oauth_error=provider_error",
    search: "",
  }), { code: "", error: "provider_error" });
  assert.equal(parseOAuthLoginResult({
    hash: "",
    search: "?oauth_error=attacker-controlled",
  }), null);
});

test("requires a pending login in the same tab before consuming a callback", () => {
  const location = { hash: "#oauth_code=single-use-code", search: "" };
  assert.deepEqual(resolvePendingOAuthLoginResult(location, null), { status: "unexpected" });
  assert.deepEqual(resolvePendingOAuthLoginResult(location, { baseURL: "https://api.example.test/", codeVerifier }), {
    status: "ready",
    baseURL: "https://api.example.test/",
    codeVerifier,
    result: { code: "single-use-code", error: "" },
  });
});

test("creates an S256 PKCE pair and OAuth start URL", async () => {
  assert.equal(await oauthCodeChallenge(codeVerifier, webcrypto), "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM");
  const pair = await createOAuthLoginPKCE(webcrypto);
  assert.match(pair.codeVerifier, /^[A-Za-z0-9_-]{43}$/);
  assert.equal(pair.codeChallenge, await oauthCodeChallenge(pair.codeVerifier, webcrypto));

  const startURL = new URL(buildOAuthLoginStartURL(
    "https://api.example.test/",
    "idp-example",
    "https://console.example.test/overview",
    pair.codeChallenge,
  ));
  assert.equal(startURL.searchParams.get("id"), "idp-example");
  assert.equal(startURL.searchParams.get("return_url"), "https://console.example.test/overview");
  assert.equal(startURL.searchParams.get("code_challenge"), pair.codeChallenge);
  assert.equal(startURL.searchParams.get("code_challenge_method"), "S256");
});

test("stores only a valid PKCE-bound pending login", () => {
  const storageKey = "tokenhub.admin.oauth.base_url";
  const storage = memoryStorage({ [storageKey]: "https://legacy.example.test" });
  assert.equal(readPendingOAuthLoginState(storageKey, storage), null);
  assert.equal(storage.value(storageKey), null);

  const pending = { baseURL: "https://api.example.test", codeVerifier };
  savePendingOAuthLoginState(storageKey, pending, storage);
  assert.deepEqual(readPendingOAuthLoginState(storageKey, storage), pending);
});

test("exchanges a code once with its PKCE verifier", async () => {
  const calls = [];
  const fetcher = async (input, init) => {
    calls.push({ input, init });
    return new Response(JSON.stringify({ token: "session-token" }), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  };

  const first = exchangeOAuthLoginCode("https://api.example.test/", "single-use-code", codeVerifier, fetcher);
  const second = exchangeOAuthLoginCode("https://api.example.test/", "single-use-code", codeVerifier, fetcher);
  const [firstResult, secondResult] = await Promise.all([first, second]);

  assert.equal(calls.length, 1);
  assert.equal(calls[0].input, "https://api.example.test/api/admin/auth/oauth/exchange");
  assert.equal(calls[0].init.method, "POST");
  assert.equal(calls[0].init.credentials, undefined);
  assert.equal(calls[0].init.body, JSON.stringify({ code: "single-use-code", code_verifier: codeVerifier }));
  assert.deepEqual(firstResult, secondResult);
});
