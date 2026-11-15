import { expect, test } from "@playwright/test";
import e2eDefaults from "./config.cjs";

const backendURL = `http://127.0.0.1:${process.env.TOKENHUB_E2E_BACKEND_PORT ?? e2eDefaults.backendPort}`;
const upstreamURL = `http://127.0.0.1:${process.env.TOKENHUB_E2E_UPSTREAM_PORT ?? e2eDefaults.upstreamPort}`;

test("allowance edits preserve caps and saved header credentials through the real store", async ({
  page,
  request,
}) => {
  await page.goto("/");
  await page.getByLabel("账号 / 邮箱").fill(e2eDefaults.adminIdentity);
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.TOKENHUB_E2E_ADMIN_PASSWORD ?? e2eDefaults.adminPassword);
  await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible();
  const token = await page.evaluate(
    () => JSON.parse(sessionStorage.getItem("tokenhub.admin.session")!).token,
  );
  const headers = { authorization: `Bearer ${token}` };
  const created = await request.post(`${backendURL}/api/admin/providers`, {
    headers,
    data: {
      type: "openai",
      name: "E2E Allowance Editing",
      base_url: `${upstreamURL}/v1`,
      api_key: "synthetic-provider",
      create_routes: false,
    },
  });
  expect(created.ok()).toBe(true);
  const { provider } = await created.json();
  const added = await request.post(
    `${backendURL}/api/admin/provider-resources`,
    {
      headers,
      data: {
        provider_id: provider.id,
        name: "E2E Quota Header Account",
        resource_type: "api_key",
        status: "active",
        healthy: true,
      },
    },
  );
  expect(added.ok()).toBe(true);
  const resource = await added.json();
  const base = `${backendURL}/api/admin/provider-resources/${resource.id}/allowance`;
  const initial = await request.get(`${base}/config`, { headers });
  expect(initial.ok()).toBe(true);
  const config = await initial.json();
  const seeded = await request.put(`${base}/config`, {
    headers,
    data: {
      ...config,
      source: {
        enabled: false,
        url: `${upstreamURL}/quota/header-rename`,
        auth: "none",
        expression: "$remaining",
        headers: {
          "X-Quota-Token": "synthetic-original",
          "X-Keep": "synthetic-retained",
        },
      },
      policy: {
        ...config.policy,
        windows: { weekly: { percent: 70 }, monthly: { percent: 90 } },
      },
    },
  });
  expect(seeded.ok()).toBe(true);
  const seededConfig = await seeded.json();
  expect(seededConfig.source.headers).toEqual({
    "X-Quota-Token": "[redacted]",
    "X-Keep": "[redacted]",
  });
  await page.goto("/providers");
  await page.getByRole("button", { name: "额度与权益", exact: true }).click();
  const console = page.getByRole("region", { name: "额度与权益", exact: true });
  await console
    .getByRole("button", { name: /E2E Quota Header Account/ })
    .click();
  await console
    .getByRole("tab", { name: "采集与权益策略", exact: true })
    .click();
  await console.getByText("高级采集设置", { exact: true }).click();
  const headerNames = console.getByLabel("请求头名称", { exact: true });
  await expect(headerNames).toHaveCount(2);
  const headerIndex = (
    await headerNames.evaluateAll((inputs) =>
      inputs.map((input) => (input as HTMLInputElement).value),
    )
  ).indexOf("X-Quota-Token");
  expect(headerIndex).toBeGreaterThanOrEqual(0);
  await headerNames.nth(headerIndex).fill("X-New");
  await console
    .getByRole("button", { name: "保存额度配置", exact: true })
    .click();
  await expect(console.getByRole("alert")).toHaveText(
    "重命名已保存的请求头后，请重新输入值。",
  );
  expect(
    (await (await request.get(`${base}/config`, { headers })).json()).revision,
  ).toBe(seededConfig.revision);
  await console
    .getByLabel("请求头值", { exact: true })
    .nth(headerIndex)
    .fill("synthetic-replacement");
  await console
    .getByRole("button", { name: "使用与权益", exact: true })
    .click();
  const names = console.getByLabel("窗口名称", { exact: true });
  await expect(names).toHaveCount(2);
  const weeklyIndex = (
    await names.evaluateAll((inputs) =>
      inputs.map((input) => (input as HTMLInputElement).value),
    )
  ).indexOf("weekly");
  expect(weeklyIndex).toBeGreaterThanOrEqual(0);
  await names.nth(weeklyIndex).fill(" MONTHLY ");
  await console
    .getByRole("button", { name: "保存额度配置", exact: true })
    .click();
  await expect(console.getByRole("alert")).toHaveText("窗口名称不能重复。");
  const retained = await (
    await request.get(`${base}/config`, { headers })
  ).json();
  expect(retained.policy.windows).toEqual({
    weekly: { percent: 70 },
    monthly: { percent: 90 },
  });
  await names.nth(weeklyIndex).fill("daily");
  await console.getByRole("button", { name: "额度采集", exact: true }).click();
  await console.getByText("高级采集设置", { exact: true }).click();
  await console.getByLabel("启用通用额度采集").check();
  const saved = page.waitForResponse(
    (response) =>
      response.url() === `${base}/config` &&
      response.request().method() === "PUT",
  );
  await console
    .getByRole("button", { name: "保存额度配置", exact: true })
    .click();
  expect((await saved).ok()).toBe(true);
  const persisted = await (
    await request.get(`${base}/config`, { headers })
  ).json();
  expect(persisted.policy.windows).toEqual({
    daily: { percent: 70 },
    monthly: { percent: 90 },
  });
  expect(persisted.source.headers).toEqual({
    "X-New": "[redacted]",
    "X-Keep": "[redacted]",
  });
  const refreshed = await request.post(`${base}/refresh`, { headers });
  expect(refreshed.ok()).toBe(true);
  expect(await refreshed.text()).toContain("12.5");
});

test("native Magpie files stage quota policies and query balance before saving", async ({
  page,
  request,
}) => {
  await page.goto("/");
  await page.getByLabel("账号 / 邮箱").fill(e2eDefaults.adminIdentity);
  await page
    .getByLabel("密码", { exact: true })
    .fill(process.env.TOKENHUB_E2E_ADMIN_PASSWORD ?? e2eDefaults.adminPassword);
  await page.getByRole("button", { name: "登录控制台" }).click();
  await expect(page.locator(".app-shell")).toBeVisible();
  const token = await page.evaluate(
    () => JSON.parse(sessionStorage.getItem("tokenhub.admin.session")!).token,
  );
  const headers = { authorization: `Bearer ${token}` };
  const created = await request.post(`${backendURL}/api/admin/providers`, {
    headers,
    data: {
      type: "openai",
      name: "E2E Magpie Native",
      base_url: `${upstreamURL}/v1`,
      create_routes: false,
    },
  });
  expect(created.ok()).toBe(true);
  const { provider } = await created.json();
  const added = await request.post(
    `${backendURL}/api/admin/provider-resources`,
    {
      headers,
      data: {
        provider_id: provider.id,
        name: "E2E Magpie Account",
        resource_type: "api_key",
        api_key: "synthetic-native-key",
        status: "active",
        healthy: true,
      },
    },
  );
  expect(added.ok()).toBe(true);
  const resource = await added.json();
  const base = `${backendURL}/api/admin/provider-resources/${resource.id}/allowance`;
  const before = await (
    await request.get(`${base}/config`, { headers })
  ).json();
  await page.goto("/providers");
  await page.getByRole("button", { name: "额度与权益", exact: true }).click();
  const console = page.getByRole("region", { name: "额度与权益", exact: true });
  await console.getByRole("button", { name: /E2E Magpie Account/ }).click();
  await console
    .getByRole("tab", { name: "采集与权益策略", exact: true })
    .click();
  await console.getByText("导入 Magpie 额度配置", { exact: true }).click();
  await console
    .getByRole("textbox", { name: "Magpie 供应商 JSON", exact: true })
    .fill(
      JSON.stringify({
        providers: [
          {
            id: "native-relay",
            balanceURL: `${upstreamURL}/quota/header-rename`,
            balancePath: "$remaining",
            headers: {
              "X-New": "synthetic-replacement",
              "X-Keep": "synthetic-retained",
            },
            accountCaps: { "legacy-key": 70 },
            accountWindowCaps: { "legacy-key": { weekly: 100 } },
            keyOwnBalances: {
              "legacy-key": {
                usd: 50,
                at: "2026-01-01T00:00:00Z",
                refill: { every: "month", day: 31 },
              },
            },
          },
        ],
      }),
    );
  await console
    .getByRole("textbox", { name: "Magpie 设置 JSON（可选）", exact: true })
    .fill(JSON.stringify({ usageAlert: 80, resetReminder: 48 }));
  await console
    .getByRole("button", { name: "填入额度配置", exact: true })
    .click();
  await expect(
    console.getByRole("textbox", { name: "余额字段", exact: true }),
  ).toHaveValue("$remaining");
  await console.getByRole("button", { name: "查询余额", exact: true }).click();
  await expect(
    console.getByLabel("余额查询结果", { exact: true }),
  ).toContainText("$12.50");
  expect(
    (await (await request.get(`${base}/config`, { headers })).json()).revision,
  ).toBe(before.revision);
  await console
    .getByRole("button", { name: "使用与权益", exact: true })
    .click();
  await expect(
    console.getByRole("combobox", { name: "补充周期", exact: true }),
  ).toHaveValue("month");
  await expect(console.getByLabel("每月补充日", { exact: true })).toHaveValue(
    "31",
  );
  await expect(
    console.getByLabel("额度重置前提醒（小时）", { exact: true }),
  ).toHaveValue("48");
  const saving = page.waitForResponse(
    (response) =>
      response.url() === `${base}/config` &&
      response.request().method() === "PUT",
  );
  await console
    .getByRole("button", { name: "保存额度配置", exact: true })
    .click();
  expect((await saving).ok()).toBe(true);
  const saved = await (await request.get(`${base}/config`, { headers })).json();
  expect(saved.policy).toMatchObject({
    cap: { percent: 70 },
    windows: { weekly: { percent: 100 } },
    wallet: { balance: 50, refill: { every: "month", day: 31 } },
    alerts: { used_percent: 80, unused_before_reset_seconds: 172800 },
  });
});
