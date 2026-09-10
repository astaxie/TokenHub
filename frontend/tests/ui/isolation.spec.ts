import { test, expect } from "@playwright/test";
import { MockAPI } from "./network";
import configuration from "./config.cjs";

for (const probe of [
  { name: "undeclared API", url: `${configuration.apiOrigin}/api/admin/not-declared`, method: "GET", error: "Missing UI fixture: GET /api/admin/not-declared" },
  { name: "undeclared method", url: `${configuration.apiOrigin}/api/admin/auth/identity-providers`, method: "DELETE", error: "Missing UI fixture: DELETE /api/admin/auth/identity-providers" },
  { name: "external network", url: "https://external-fixture.invalid/data", method: "GET", error: "Unexpected network request: GET https://external-fixture.invalid/data" },
  { name: "same-origin API fallback", url: `${configuration.frontendOrigin}/api/admin/not-declared`, method: "GET", error: `Unexpected network request: GET ${configuration.frontendOrigin}/api/admin/not-declared` },
]) {
  test(`isolation rejects ${probe.name}`, async ({ context, page }) => {
    const api = new MockAPI();
    api.respond("GET", "/api/admin/auth/identity-providers", { data: [] });
    await api.install(context);
    await page.goto("/");
    await expect(page.getByRole("button", { name: "登录控制台" })).toBeVisible();
    const result = await page.evaluate(async ({ url, method }) => {
      try { await fetch(url, { method }); return "forwarded"; }
      catch { return "blocked"; }
    }, probe);
    expect(result).toBe("blocked");
    expect(api.violations).toEqual([probe.error]);
    expect(() => api.assertClean()).toThrow(probe.error);
  });
}

test("isolation rejects malformed fixture payloads", async ({ context, page }) => {
  const api = new MockAPI();
  api.respond("GET", "/api/admin/auth/identity-providers", { data: [] });
  api.define("POST", "/api/admin/check-payload", input => {
    if (input.body !== "expected") throw new Error("Expected the declared payload");
    return { json: { ok: true } };
  });
  await api.install(context);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "登录控制台" })).toBeVisible();
  const status = await page.evaluate(async url => (await fetch(url, { method: "POST", headers: { "content-type": "application/json" }, body: "{}" })).status, `${configuration.apiOrigin}/api/admin/check-payload`);
  expect(status).toBe(500);
  expect(() => api.assertClean()).toThrow("Expected the declared payload");
});

test("isolation rejects WebSockets without connecting upstream", async ({ context, page }) => {
  const api = new MockAPI();
  api.respond("GET", "/api/admin/auth/identity-providers", { data: [] });
  await api.install(context);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "登录控制台" })).toBeVisible();
  await page.evaluate(() => { const socket = new WebSocket("wss://external-fixture.invalid/socket"); socket.onerror = () => {}; });
  await expect.poll(() => api.violations).toEqual(["Unexpected WebSocket: wss://external-fixture.invalid/socket"]);
  expect(() => api.assertClean()).toThrow("Unexpected WebSocket");
});

test("isolation requires an explicit query contract", async ({ context, page }) => {
  const api = new MockAPI();
  api.respond("GET", "/api/admin/auth/identity-providers", { data: [] });
  api.define("GET", "/api/admin/query-example", () => ({ json: { page: 1 } }), query => {
    expect([...query.entries()]).toEqual([["page", "1"]]);
  });
  await api.install(context);
  await page.goto("/");
  await expect(page.getByRole("button", { name: "登录控制台" })).toBeVisible();
  const valid = await page.evaluate(async url => (await fetch(url)).json(), `${configuration.apiOrigin}/api/admin/query-example?page=1`);
  expect(valid).toEqual({ page: 1 });
  api.assertClean();
  const status = await page.evaluate(async url => (await fetch(url)).status, `${configuration.apiOrigin}/api/admin/auth/identity-providers?undeclared=1`);
  expect(status).toBe(500);
  expect(() => api.assertClean()).toThrow("Unexpected query for GET /api/admin/auth/identity-providers");
});
