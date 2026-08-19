import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "../domain/typescript-test-loader.mjs";

const { parseProviderAccountOAuthResult } = await importTypeScript(new URL("./session-oauth.ts", import.meta.url));

test("login OAuth callback with bare code and state is not misread as a provider account callback", () => {
  const source = "https://console.example.com/?code=dingtalk-code&state=login-oauth-state";
  assert.equal(parseProviderAccountOAuthResult(source, false), null);
});

test("login OAuth error callback is not misread as a provider account callback", () => {
  const source = "https://console.example.com/?error=access_denied&state=login-oauth-state";
  assert.equal(parseProviderAccountOAuthResult(source, false), null);
});

test("marked provider account callback with authorization code is detected", () => {
  const source = "https://console.example.com/?provider_account_oauth=1&provider_account_oauth_session_id=session-1&provider_account_oauth_state=st&code=provider-code";
  const result = parseProviderAccountOAuthResult(source, false);
  assert.ok(result);
  assert.equal(result.authorization_code, "provider-code");
  assert.equal(result.session_id, "session-1");
});

test("marked provider account callback with access token is detected", () => {
  const source = "https://console.example.com/?provider_account_oauth=1&account_access_token=tok&account_email=a%40example.com";
  const result = parseProviderAccountOAuthResult(source, false);
  assert.ok(result);
  assert.equal(result.access_token, "tok");
  assert.equal(result.account_email, "a@example.com");
});

test("marked provider account error callback is detected", () => {
  const source = "https://console.example.com/?provider_account_oauth=1&error=denied";
  const result = parseProviderAccountOAuthResult(source, false);
  assert.ok(result);
  assert.equal(result.error, "denied");
});

test("unmarked callback with provider-specific token name is still detected", () => {
  const source = "https://console.example.com/?account_access_token=tok";
  const result = parseProviderAccountOAuthResult(source, false);
  assert.ok(result);
  assert.equal(result.access_token, "tok");
});

test("manual paste mode (allowGenericTokenNames) accepts a bare code", () => {
  const source = "https://console.example.com/?code=generic-code";
  const result = parseProviderAccountOAuthResult(source, true);
  assert.ok(result);
  assert.equal(result.authorization_code, "generic-code");
});

test("unrelated URL without OAuth parameters returns null", () => {
  const source = "https://console.example.com/overview";
  assert.equal(parseProviderAccountOAuthResult(source, false), null);
});
