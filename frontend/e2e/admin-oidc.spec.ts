import { expect, test, type APIRequestContext } from "@playwright/test";
import e2eDefaults from "./config.cjs";

const backendURL = `http://127.0.0.1:${process.env.TOKENHUB_E2E_BACKEND_PORT ?? e2eDefaults.backendPort}`;
const upstreamURL = `http://127.0.0.1:${process.env.TOKENHUB_E2E_UPSTREAM_PORT ?? e2eDefaults.upstreamPort}`;

async function createIdentityProvider(request: APIRequestContext, scenario = "") {
  const login = await request.post(`${backendURL}/api/admin/auth/login`, {
    data: {
      identity: e2eDefaults.adminIdentity,
      password: process.env.TOKENHUB_E2E_ADMIN_PASSWORD ?? e2eDefaults.adminPassword,
    },
  });
  expect(login.ok()).toBe(true);
  const admin = await login.json();
  const headers = { authorization: `Bearer ${admin.token}` };
  const created = await request.post(`${backendURL}/api/admin/resources/identity-providers`, {
    headers,
    data: {
      name: "E2E Keycloak",
      status: "active",
      fields: {
        provider_type: "oidc",
        icon_key: "keycloak",
        login_label: "E2E Keycloak",
        issuer_url: `${upstreamURL}/oidc`,
        client_id: "e2e-oidc-client",
        client_secret: "e2e-oidc-secret",
        authorize_url: `${upstreamURL}/oidc/authorize${scenario ? `?scenario=${scenario}` : ""}`,
        token_url: `${upstreamURL}/oidc/token`,
        userinfo_url: `${upstreamURL}/oidc/userinfo`,
        redirect_uri: `${backendURL}/api/admin/auth/oauth/callback`,
        scopes: "openid profile email",
        default_role: "user",
      },
    },
  });
  expect(created.ok()).toBe(true);
  const provider = await created.json();
  return { provider, headers };
}

async function removeIdentityProvider(request: APIRequestContext, provider: { id: string }, headers: Record<string, string>) {
  const deleted = await request.delete(`${backendURL}/api/admin/resources/identity-providers/${provider.id}`, { headers });
  expect(deleted.ok()).toBe(true);
}

test("OIDC users can sign in again after signing out", async ({ page, request }) => {
  const { provider, headers } = await createIdentityProvider(request);
  try {
    await page.goto("/overview");
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const exchange = page.waitForResponse(response => response.url() === `${backendURL}/api/admin/auth/oauth/exchange`);
      await page.getByRole("link", { name: "使用 E2E Keycloak 登录" }).click();
      expect((await exchange).ok()).toBe(true);
      await expect(page.locator(".app-shell")).toBeVisible();
      await expect(page).toHaveURL(/\/overview$/);
      const session = await page.evaluate(() => JSON.parse(window.sessionStorage.getItem("tokenhub.admin.session") ?? "null"));
      expect(session?.user.email).toBe("e2e.oidc.user@example.test");
      expect(session?.user.role).toBe("user");
      await page.getByTitle("退出登录").click();
      await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
      expect(await page.evaluate(() => window.sessionStorage.getItem("tokenhub.admin.session"))).toBeNull();
    }
  } finally {
    await removeIdentityProvider(request, provider, headers);
  }
});

test("OIDC provider errors remain visible and users can retry sign-in", async ({ page, request }) => {
  const { provider, headers } = await createIdentityProvider(request, "deny");
  try {
    await page.goto("/overview");
    await page.getByRole("link", { name: "使用 E2E Keycloak 登录" }).click();
    await expect(page.locator(".login-error")).toHaveText("OAuth 登录失败（provider_error），请重试或联系管理员。");
    await expect(page).toHaveURL(/\/overview$/);
    expect(await page.evaluate(() => window.sessionStorage.getItem("tokenhub.admin.session"))).toBeNull();

    const updated = await request.patch(`${backendURL}/api/admin/resources/identity-providers/${provider.id}`, {
      headers,
      data: { fields: { ...provider.fields, authorize_url: `${upstreamURL}/oidc/authorize` } },
    });
    expect(updated.ok()).toBe(true);
    await page.getByRole("link", { name: "使用 E2E Keycloak 登录" }).click();
    await expect(page.locator(".app-shell")).toBeVisible();
    await expect(page).toHaveURL(/\/overview$/);
  } finally {
    await removeIdentityProvider(request, provider, headers);
  }
});

for (const scenario of [
  { name: "exchange code without pending login", suffix: "#oauth_code=unsolicited-exchange-code" },
  { name: "forged authorization callback", suffix: "?code=forged-authorization-code&state=forged-state" },
]) {
  test(`OIDC rejects ${scenario.name} with a visible error`, async ({ page }) => {
    const authenticationRequests: string[] = [];
    page.on("request", request => {
      if (/\/api\/admin\/auth\/oauth\/(?:callback|exchange)/.test(request.url())) authenticationRequests.push(request.url());
    });
    await page.goto(`/overview${scenario.suffix}`);
    await expect(page.locator(".login-error")).toHaveText("OAuth 登录失败");
    await expect(page).toHaveURL(/\/overview$/);
    expect(await page.evaluate(() => window.sessionStorage.getItem("tokenhub.admin.session"))).toBeNull();
    expect(authenticationRequests).toEqual([]);
  });
}
