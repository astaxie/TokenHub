import type { BrowserContext, Page } from "@playwright/test";
import { oauthBaseURLStorageKey, sessionStorageKey, type AdminUser } from "../../features/admin/core/types";
import configuration from "./config.cjs";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

const verifier = "ui-oauth-verifier-".padEnd(43, "x");
const callbackCode = "ui-oauth-exchange-code";
const exchangePath = "/api/admin/auth/oauth/exchange";
const oauthUser: AdminUser = {
  id: "usr_ui_oidc", username: "ui-oidc-user", name: "UI OIDC User",
  email: "ui-oidc-user@example.test", role: "user", status: "active",
};

test.use({ sessionUser: null });

test.beforeEach(async ({ api }) => {
  api.respond("GET", "/api/admin/auth/identity-providers", { data: [{
    id: "idp_ui_keycloak", name: "UI Keycloak", display_name: "UI Keycloak",
    provider_type: "oidc", icon_key: "keycloak",
  }] });
});

async function seedPendingLogin(context: BrowserContext) {
  await context.addInitScript(({ key, pending }) => {
    window.sessionStorage.setItem(key, JSON.stringify(pending));
  }, { key: oauthBaseURLStorageKey, pending: { baseURL: configuration.apiOrigin, codeVerifier: verifier } });
}

async function expectCallbackCleared(page: Page) {
  await expect(page).toHaveURL(`${configuration.frontendOrigin}/overview`);
  expect(await page.evaluate(key => sessionStorage.getItem(key), oauthBaseURLStorageKey)).toBeNull();
}

function installOverviewFixtures(api: MockAPI) {
  api.respond("GET", "/api/admin/usage/timeseries", { data: [] });
  api.respond("GET", "/api/admin/plugins", { data: [] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-chain", { data: { hooks: [] } });
  api.respond("GET", "/api/admin/plugin-ui-manifest", { data: [] });
  api.respond("GET", "/api/admin/plugin-actions", { data: [] });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  api.respond("GET", "/api/admin/resources/announcements", { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({
    json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 } },
  }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
}

test("oauth-login provider error remains visible after callback cleanup", async ({ context, page, api }, info) => {
  await seedPendingLogin(context);
  await page.goto("/overview#oauth_error=provider_error");
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
  await expect(page.locator(".login-error")).toHaveText("OAuth 登录失败（provider_error），请重试或联系管理员。");
  await expectCallbackCleared(page);
  await expect(page.getByRole("link", { name: "使用 UI Keycloak 登录" })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(api.calls.filter(call => call.path === exchangePath)).toHaveLength(0);
  await capture(page, info, page.locator(".login-card"), "oauth-provider-error", "SSO 登录失败：回调清理后仍显示错误");
});

for (const locale of [
  { language: "en" as const, message: "OAuth sign-in failed (provider_error). Try again or contact your administrator." },
  { language: "ja" as const, message: "OAuth ログインに失敗しました（provider_error）。再試行するか、管理者にお問い合わせください。" },
  { language: "ru" as const, message: "Ошибка входа через OAuth (provider_error). Повторите попытку или обратитесь к администратору." },
]) {
  test.describe(`oauth-login provider error in ${locale.language}`, () => {
    test.use({ sessionLanguage: locale.language });
    test("uses the selected language for the persisted failure", async ({ context, page, api }, info) => {
      await seedPendingLogin(context);
      await page.goto("/overview#oauth_error=provider_error");
      await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
      await expect(page.locator(".login-error")).toHaveText(locale.message);
      await expectCallbackCleared(page);
      expect(api.calls.filter(call => call.path === exchangePath)).toHaveLength(0);
      await capture(page, info, page.locator(".login-card"), `oauth-provider-error-${locale.language}`, "SSO 登录失败：多语言提示");
    });
  });
}

test("oauth-login unknown provider error uses a safe fallback without reflecting callback text", async ({ context, page, api }) => {
  await seedPendingLogin(context);
  const callbackError = "Untrusted callback text <img src=x onerror=alert(1)>";
  await page.goto(`/overview#oauth_error=${encodeURIComponent(callbackError)}`);
  await expect(page.locator(".login-error")).toHaveText("OAuth 登录失败（unknown_error），请重试或联系管理员。");
  await expect(page.locator("body")).not.toContainText(callbackError);
  await expectCallbackCleared(page);
  expect(api.calls.filter(call => call.path === exchangePath)).toHaveLength(0);
});

test("oauth-login successful exchange opens the personal console without a provider dialog", async ({ context, page, api }, info) => {
  await seedPendingLogin(context);
  installOverviewFixtures(api);
  api.define("POST", exchangePath, request => {
    expect(request.body).toEqual({ code: callbackCode, code_verifier: verifier });
    return { json: { token: "ui-oidc-session", user: oauthUser, expires_at: "2099-01-01T00:00:00Z" } };
  });
  await page.goto(`/overview#oauth_code=${callbackCode}`);
  await expect(page.locator(".app-shell")).toBeVisible();
  await expect(page.getByRole("heading", { name: "我的 AI 调用监控" })).toBeVisible();
  await expectCallbackCleared(page);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.locator(".login-card")).toHaveCount(0);
  expect(api.calls.filter(call => call.path === exchangePath)).toHaveLength(1);
  expect(await page.evaluate(key => JSON.parse(sessionStorage.getItem(key)!), sessionStorageKey)).toEqual({
    baseURL: configuration.apiOrigin, token: "ui-oidc-session", user: oauthUser, expiresAt: "2099-01-01T00:00:00Z",
  });
  await capture(page, info, page.locator(".app-shell"), "oauth-personal-console", "SSO 登录成功：进入个人控制台", "viewport");
});

test("oauth-login unexpected callback stays visible and does not exchange a code", async ({ page, api }) => {
  await page.goto(`/overview#oauth_code=${callbackCode}`);
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
  await expect(page.locator(".login-error")).toHaveText("OAuth 登录失败");
  await expectCallbackCleared(page);
  expect(api.calls.filter(call => call.path === exchangePath)).toHaveLength(0);
  expect(await page.evaluate(key => sessionStorage.getItem(key), sessionStorageKey)).toBeNull();
});

test("oauth-login rejected exchange displays its error without opening the console", async ({ context, page, api }) => {
  await seedPendingLogin(context);
  api.define("POST", exchangePath, request => {
    expect(request.body).toEqual({ code: callbackCode, code_verifier: verifier });
    return { status: 400, json: { error: { code: "invalid_oauth_code", message: "Synthetic OAuth exchange expired" } } };
  });
  await page.goto(`/overview#oauth_code=${callbackCode}`);
  await expect(page.locator(".login-error")).toHaveText("Synthetic OAuth exchange expired");
  await expectCallbackCleared(page);
  await expect(page.locator(".app-shell")).toHaveCount(0);
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(api.calls.filter(call => call.path === exchangePath)).toHaveLength(1);
  expect(await page.evaluate(key => sessionStorage.getItem(key), sessionStorageKey)).toBeNull();
});
