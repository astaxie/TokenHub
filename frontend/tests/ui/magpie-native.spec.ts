import { test, expect, capture } from "./harness";
import {
  allowanceConfig,
  installAllowances,
} from "./fixtures/provider-allowances";

for (const mobile of [false, true]) {
  test(`spend cap keeps the selected wallet timezone ${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    installAllowances(api);
    await page.goto("/providers");
    await page.getByRole("button", { name: "额度与权益", exact: true }).click();
    const console = page.getByRole("region", { name: "额度与权益", exact: true });
    await console.getByRole("button", { name: /Synthetic Fusecode/ }).click();
    await console.getByRole("tab", { name: "采集与权益策略", exact: true }).click();
    await console.getByRole("button", { name: "使用与权益", exact: true }).click();
    await console.getByLabel("余额与开销时区", { exact: true }).fill("America/New_York");
    await console.getByLabel("限制此 API Key 的开销", { exact: true }).check();
    await expect(console.getByLabel("余额与开销时区", { exact: true })).toHaveValue("America/New_York");
    await console.getByText("余额补充", { exact: true }).evaluate((element) => element.scrollIntoView({ block: "start" }));
    await capture(page, testInfo, console, `spend-cap-timezone-${mobile ? "mobile" : "desktop"}`, "开销上限沿用所选时区", "viewport");
    await console.getByRole("button", { name: "保存额度配置", exact: true }).click();
    await expect.poll(() => api.calls.filter((call) => call.method === "PUT").length).toBe(1);
    expect(api.calls.find((call) => call.method === "PUT")?.body).toMatchObject({ policy: { wallet: { timezone: "America/New_York" }, spend_cap: { timezone: "America/New_York" } } });
  });
  test(`Magpie native quota migration ${mobile ? "mobile" : "desktop"}`, async ({
    page,
    api,
  }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    installAllowances(api);
    const imported = allowanceConfig("quota-account-0");
    imported.source = {
      enabled: true,
      auth: "magpie",
      refresh_seconds: 60,
      headers: {},
    };
    imported.clear_token = true;
    imported.policy = {
      cap: { percent: 70 },
      windows: { weekly: { percent: 100 } },
      cap_stop: true,
      cap_careful: true,
      credits: "last",
      spend_cap: { usd: 20, period: "month", timezone: "Asia/Shanghai" },
      reset: {
        on_exhausted: false,
        before_expiry: true,
        lead_seconds: 300,
        weekly_limit: 1,
      },
      alerts: {
        enabled: true,
        used_percent: 80,
        low_balance: 5,
        unused_before_reset_seconds: 172800,
      },
      wallet: {
        enabled: true,
        balance: -5,
        unit: "CNY",
        as_of: "2026-09-01T00:00:00Z",
        refill: { every: "month", day: 31 },
        timezone: "Asia/Shanghai",
        carry_over: false,
        grants: [
          {
            id: "synthetic-grant",
            amount: 20,
            until: "2026-12-31",
            at: "2026-09-01T00:00:00Z",
          },
        ],
      },
    };
    api.respond(
      "POST",
      "/api/admin/provider-resources/quota-account-0/allowance/import-magpie",
      imported,
    );
    await page.goto("/providers");
    await page.getByRole("button", { name: "额度与权益", exact: true }).click();
    const console = page.getByRole("region", {
      name: "额度与权益",
      exact: true,
    });
    await console.getByRole("button", { name: /Synthetic Fusecode/ }).click();
    await console
      .getByRole("tab", { name: "采集与权益策略", exact: true })
      .click();
    await console.getByText("导入 Magpie 额度配置", { exact: true }).click();
    await console.getByLabel("Magpie 供应商 JSON", { exact: true }).fill(
      JSON.stringify(
        {
          providers: [
            {
              id: "codex",
              accountCaps: { "one@example.test": 70, "two@example.test": 80 },
              capStop: true,
              capCareful: true,
            },
          ],
        },
        null,
        2,
      ),
    );
    await console
      .getByLabel("Magpie 设置 JSON（可选）", { exact: true })
      .fill(JSON.stringify({ usageAlert: 80, resetReminder: 48 }));
    await console
      .getByLabel("Magpie 账号或 Key ID", { exact: true })
      .fill("one@example.test");
    const mode = mobile ? "mobile" : "desktop";
    await capture(
      page,
      testInfo,
      console,
      `magpie-native-import-${mode}`,
      "原生配置文件与账号选择",
      "viewport",
    );
    await console
      .getByRole("button", { name: "填入额度配置", exact: true })
      .click();
    await console
      .getByRole("button", { name: "使用与权益", exact: true })
      .click();
    await expect(
      console.getByLabel("所有账号达到限额时停止，不转到后备供应商"),
    ).toBeChecked();
    await expect(
      console.getByRole("combobox", { name: "开销周期", exact: true }),
    ).toHaveValue("month");
    await console
      .getByText("开销上限", { exact: true })
      .evaluate((element) => element.scrollIntoView({ block: "start" }));
    await capture(
      page,
      testInfo,
      console,
      `magpie-native-caps-${mode}`,
      "原生开销上限与停止、重查规则",
      "viewport",
    );
    await expect(
      console.getByRole("combobox", { name: "补充周期", exact: true }),
    ).toHaveValue("month");
    await expect(console.getByLabel("每月补充日", { exact: true })).toHaveValue(
      "31",
    );
    await console
      .getByText("余额补充", { exact: true })
      .evaluate((element) => element.scrollIntoView({ block: "start" }));
    await capture(
      page,
      testInfo,
      console,
      `magpie-native-calendar-${mode}`,
      "每月指定日补充与时区",
      "viewport",
    );
    await expect(
      console.getByLabel("额度重置前提醒（小时）", { exact: true }),
    ).toHaveValue("48");
    await expect(console.getByLabel("起始余额", { exact: true })).toHaveValue(
      "-5",
    );
    await expect(
      console.getByLabel("补充额度到期时间", { exact: true }),
    ).toHaveValue("2026-12-31");
    await console
      .getByText("手动余额与补充额度", { exact: true })
      .evaluate((element) => element.scrollIntoView({ block: "start" }));
    await capture(
      page,
      testInfo,
      console,
      `magpie-native-wallet-${mode}`,
      "人民币负余额与日期到期赠额",
      "viewport",
    );
    await console
      .getByRole("button", { name: "保存额度配置", exact: true })
      .click();
    await expect
      .poll(() => api.calls.filter((call) => call.method === "PUT").length)
      .toBe(1);
    expect(api.calls.find((call) => call.method === "PUT")?.body).toMatchObject(
      { policy: imported.policy },
    );
    expect(
      api.calls.find((call) => call.path.endsWith("/import-magpie"))?.body,
    ).toMatchObject({
      account_ref: "one@example.test",
      settings: { resetReminder: 48 },
    });
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBeTruthy();
  });
}
