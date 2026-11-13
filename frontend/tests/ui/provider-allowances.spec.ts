import { test, expect, capture } from "./harness";
import type { Locator } from "@playwright/test";
import { installAllowances } from "./fixtures/provider-allowances";

async function positionSegment(target: Locator) {
  await target.evaluate((element) => {
    const previous = element.style.scrollMarginTop;
    element.style.scrollMarginTop = "80px";
    element.scrollIntoView({ block: "start", behavior: "instant" });
    element.style.scrollMarginTop = previous;
  });
}

for (const mobile of [false, true]) {
  test(`provider allowances complete console ${mobile ? "mobile" : "desktop"}`, async ({
    page,
    api,
  }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    installAllowances(api);
    await page.goto("/providers");
    await page.getByRole("button", { name: "额度与权益", exact: true }).click();
    const console = page.getByRole("region", {
      name: "额度与权益",
      exact: true,
    });
    await console.getByRole("button", { name: /Synthetic Fusecode/ }).click();
    await expect(console.getByRole("progressbar")).toHaveCount(2);
    const mode = mobile ? "mobile" : "desktop";
    await positionSegment(console.locator(".allowance-summary"));
    await capture(
      page,
      testInfo,
      console,
      `allowance-dashboard-${mode}`,
      "额度总览与账号状态",
      "viewport",
    );
    await positionSegment(console.locator(".allowance-windows"));
    await capture(
      page,
      testInfo,
      console,
      `allowance-windows-${mode}`,
      "余额与模型周期窗口",
      "viewport",
    );
    await positionSegment(console.locator(".allowance-entitlement").last());
    await capture(
      page,
      testInfo,
      console,
      `allowance-entitlements-${mode}`,
      "积分与逐张重置卡到期时间",
      "viewport",
    );
    for (const name of [
      "Synthetic Unknown",
      "Synthetic Failed Query",
      "Synthetic Exhausted",
    ]) {
      await console.getByRole("button", { name: new RegExp(name) }).click();
      await expect(console.locator(".allowance-notice")).toContainText(
        name === "Synthetic Unknown"
          ? "额度未知"
          : name === "Synthetic Failed Query"
            ? "查询失败"
            : "额度耗尽",
      );
      await positionSegment(console.locator(".allowance-notice"));
      await capture(
        page,
        testInfo,
        console,
        `allowance-state-${name.split(" ").at(-1)}-${mode}`,
        "未知、查询失败与耗尽分别显示",
        "viewport",
      );
    }
    await console.getByRole("button", { name: /Synthetic Fusecode/ }).click();
    await console.getByRole("tab", { name: "额度历史", exact: true }).click();
    await expect(
      console.getByRole("img", { name: "额度变化趋势" }),
    ).toBeVisible();
    await positionSegment(console.locator(".allowance-history"));
    await capture(
      page,
      testInfo,
      console,
      `allowance-history-${mode}`,
      "额度历史趋势与余额可用天数估算",
      "viewport",
    );
    await console
      .getByRole("tab", { name: "采集与权益策略", exact: true })
      .click();
    await expect(
      console.getByRole("textbox", { name: "余额表达式", exact: true }).first(),
    ).toHaveValue(/\$remaining/);
    await positionSegment(
      console.getByRole("combobox", { name: "额度预设", exact: true }),
    );
    await capture(
      page,
      testInfo,
      console,
      `allowance-source-${mode}`,
      "Fusecode 通用采集与独立凭据",
      "viewport",
    );
    await positionSegment(
      console.getByRole("textbox", { name: "余额表达式", exact: true }).first(),
    );
    await capture(
      page,
      testInfo,
      console,
      `allowance-expression-${mode}`,
      "Magpie 余额表达式与共享钱包配置",
      "viewport",
    );
    await positionSegment(console.getByText("额度请求头", { exact: true }));
    await capture(
      page,
      testInfo,
      console,
      `allowance-headers-${mode}`,
      "加密额度请求头与结构化窗口入口",
      "viewport",
    );
    await positionSegment(console.getByText("结构化额度窗口", { exact: true }));
    await capture(
      page,
      testInfo,
      console,
      `allowance-rules-${mode}`,
      "结构化窗口与模型范围配置",
      "viewport",
    );
    await positionSegment(
      console.getByRole("textbox", {
        name: "适用模型（逗号分隔）",
        exact: true,
      }),
    );
    await capture(
      page,
      testInfo,
      console,
      `allowance-model-scope-${mode}`,
      "窗口重置、模型范围与额度池",
      "viewport",
    );
    await console
      .getByRole("button", { name: "使用与权益", exact: true })
      .click();
    await expect(
      console.getByLabel("周额度耗尽且无获准候选时自动使用重置卡"),
    ).not.toBeChecked();
    await positionSegment(console.getByText("账号使用上限", { exact: true }));
    await capture(
      page,
      testInfo,
      console,
      `allowance-caps-${mode}`,
      "全局与窗口使用上限",
      "viewport",
    );
    await positionSegment(console.getByText("积分与重置卡", { exact: true }));
    await capture(
      page,
      testInfo,
      console,
      `allowance-reset-policy-${mode}`,
      "积分使用顺序与默认关闭的自动重置",
      "viewport",
    );
    await positionSegment(console.getByText("额度告警", { exact: true }));
    await expect(console.getByLabel("Synthetic Operations")).toBeChecked();
    await capture(
      page,
      testInfo,
      console,
      `allowance-alert-policy-${mode}`,
      "高用量、低余额、重置提醒与通知渠道",
      "viewport",
    );
    await positionSegment(
      console.getByText("手动余额与补充额度", { exact: true }),
    );
    await capture(
      page,
      testInfo,
      console,
      `allowance-wallet-policy-${mode}`,
      "手动余额、定期补充与到期额度",
      "viewport",
    );
    await positionSegment(
      console.getByRole("button", { name: "添加补充额度", exact: true }),
    );
    await capture(
      page,
      testInfo,
      console,
      `allowance-grants-${mode}`,
      "定期补充、余额结转与到期额度",
      "viewport",
    );
    await console
      .getByRole("button", { name: "保存额度配置", exact: true })
      .click();
    await expect
      .poll(() => api.calls.filter((call) => call.method === "PUT").length)
      .toBe(1);
    await console
      .getByRole("tab", { name: "权益与告警记录", exact: true })
      .click();
    await expect(console.locator(".allowance-events article")).toHaveCount(2);
    await positionSegment(console.locator(".allowance-events"));
    await capture(
      page,
      testInfo,
      console,
      `allowance-events-${mode}`,
      "权益执行与告警记录",
      "viewport",
    );
    const payload = api.calls.find((call) => call.method === "PUT")!.body;
    expect(payload).toMatchObject({
      revision: "synthetic-revision",
      policy: {
        reset: { on_exhausted: false, before_expiry: false },
        alerts: { channel_ids: ["ui-quota-channel"] },
      },
    });
    expect(api.calls.filter((call) => call.method === "POST")).toHaveLength(0);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    ).toBeTruthy();
  });
}

test("provider allowances save conflict retains form", async ({
  page,
  api,
}, testInfo) => {
  installAllowances(api, { saveFailure: true });
  await page.goto("/providers");
  await page.getByRole("button", { name: "额度与权益", exact: true }).click();
  const console = page.getByRole("region", { name: "额度与权益", exact: true });
  await console.getByRole("button", { name: /Synthetic Fusecode/ }).click();
  await console
    .getByRole("tab", { name: "采集与权益策略", exact: true })
    .click();
  await console
    .getByRole("textbox", { name: "余额表达式", exact: true })
    .first()
    .fill("$data.balance");
  await console
    .getByRole("button", { name: "保存额度配置", exact: true })
    .click();
  await expect(console.getByRole("alert")).toContainText(
    "Synthetic configuration changed",
  );
  await expect(
    console.getByRole("textbox", { name: "余额表达式", exact: true }).first(),
  ).toHaveValue("$data.balance");
  await capture(
    page,
    testInfo,
    console,
    "allowance-save-conflict",
    "保存冲突保留编辑内容",
    "viewport",
  );
});

for (const mobile of [false, true]) {
  test(`provider allowances cloud credentials ${mobile ? "mobile" : "desktop"}`, async ({
    page,
    api,
  }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    installAllowances(api);
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
    await console
      .getByRole("combobox", { name: "额度预设", exact: true })
      .selectOption("tencent");
    await expect(
      console.getByRole("combobox", { name: "认证方式", exact: true }),
    ).toHaveValue("tencent");
    await console
      .getByRole("textbox", { name: "云账号 AccessKey ID", exact: true })
      .fill("synthetic-cloud-access-id");
    await console
      .getByRole("textbox", { name: "云账号区域", exact: true })
      .fill("ap-guangzhou");
    await console
      .getByRole("textbox", { name: "套餐 TeamId", exact: true })
      .fill("synthetic-team");
    await console
      .getByLabel("独立额度令牌", { exact: true })
      .fill("synthetic-cloud-secret");
    const mode = mobile ? "mobile" : "desktop";
    await positionSegment(
      console.getByRole("combobox", { name: "额度预设", exact: true }),
    );
    await capture(
      page,
      testInfo,
      console,
      `allowance-cloud-source-${mode}`,
      "云套餐预设与签名认证",
      "viewport",
    );
    await positionSegment(
      console.getByRole("textbox", {
        name: "云账号 AccessKey ID",
        exact: true,
      }),
    );
    await capture(
      page,
      testInfo,
      console,
      `allowance-cloud-credentials-${mode}`,
      "云账号区域、套餐隔离与独立密钥",
      "viewport",
    );
    await console
      .getByRole("button", { name: "保存额度配置", exact: true })
      .click();
    await expect
      .poll(() => api.calls.filter((call) => call.method === "PUT").length)
      .toBe(1);
    expect(api.calls.find((call) => call.method === "PUT")?.body).toMatchObject(
      {
        source: {
          preset: "tencent",
          auth: "tencent",
          access_key_id: "synthetic-cloud-access-id",
          region: "ap-guangzhou",
          plan_id: "synthetic-team",
        },
        token: "synthetic-cloud-secret",
      },
    );
    const savedSource = (
      api.calls.find((call) => call.method === "PUT")?.body as {
        source: Record<string, unknown>;
      }
    ).source;
    expect(savedSource.headers).toBeUndefined();
    expect(savedSource.expression).toBeUndefined();
    expect(savedSource.windows).toBeUndefined();
    expect(api.calls.filter((call) => call.method === "POST")).toHaveLength(0);
  });
}

for (const state of ["empty", "failure"] as const)
  test(`provider allowances ${state}`, async ({ page, api }, testInfo) => {
    installAllowances(api, {
      empty: state === "empty",
      loadFailure: state === "failure",
    });
    await page.goto("/providers");
    await page.getByRole("button", { name: "额度与权益", exact: true }).click();
    const console = page.getByRole("region", {
      name: "额度与权益",
      exact: true,
    });
    if (state === "failure")
      await expect(console.getByRole("alert")).toContainText(
        "Synthetic quota cache unavailable",
      );
    else await expect(console.getByText("没有符合条件的账号。")).toBeVisible();
    await capture(
      page,
      testInfo,
      console,
      `allowance-${state}`,
      "额度空状态与读取失败",
      "viewport",
    );
  });

for (const mobile of [false, true]) {
  test(`provider allowance editing ${mobile ? "mobile" : "desktop"}`, async ({
    page,
    api,
  }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    installAllowances(api);
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
    const mode = mobile ? "mobile" : "desktop";
    await console
      .getByRole("button", { name: "添加请求头", exact: true })
      .click();
    const headerNames = console.getByLabel("请求头名称", { exact: true });
    await headerNames.nth(1).fill("X-Second");
    await console
      .getByLabel("请求头值", { exact: true })
      .nth(1)
      .fill("synthetic-second");
    await headerNames.first().fill("");
    await headerNames.first().pressSequentially("X-New");
    await expect(headerNames.first()).toHaveValue("X-New");
    await expect(headerNames.nth(1)).toHaveValue("X-Second");
    await positionSegment(console.getByText("额度请求头", { exact: true }));
    await capture(
      page,
      testInfo,
      console,
      `allowance-edit-headers-${mode}`,
      "请求头逐字改名与行顺序",
      "viewport",
    );
    const models = console.getByLabel("适用模型（逗号分隔）", { exact: true });
    const excluded = console.getByLabel("排除模型（逗号分隔）", {
      exact: true,
    });
    await models.fill("");
    await models.pressSequentially("gpt-*, claude-*,");
    await excluded.pressSequentially("gpt-mini*, claude-haiku*,");
    await expect(models).toHaveValue("gpt-*, claude-*,");
    await expect(excluded).toHaveValue("gpt-mini*, claude-haiku*,");
    await positionSegment(models);
    await capture(
      page,
      testInfo,
      console,
      `allowance-edit-models-${mode}`,
      "模型范围保留逗号并在保存时拆分",
      "viewport",
    );
    await console
      .getByRole("button", { name: "使用与权益", exact: true })
      .click();
    await console
      .getByRole("button", { name: "添加窗口限额", exact: true })
      .click();
    const capNames = console.getByLabel("窗口名称", { exact: true });
    await capNames.nth(1).fill("monthly");
    await console.getByLabel("窗口比例上限", { exact: true }).nth(1).fill("90");
    await capNames.first().fill("");
    await capNames.first().pressSequentially("monthly");
    await capNames.first().fill("");
    await capNames.first().pressSequentially("daily");
    await expect(capNames.first()).toHaveValue("daily");
    await expect(capNames.nth(1)).toHaveValue("monthly");
    await positionSegment(console.getByText("账号使用上限", { exact: true }));
    await capture(
      page,
      testInfo,
      console,
      `allowance-edit-caps-${mode}`,
      "窗口限额逐字改名并保留原值",
      "viewport",
    );
    await console
      .getByRole("button", { name: "额度采集", exact: true })
      .click();
    await expect(headerNames.first()).toHaveValue("X-New");
    await console
      .getByRole("button", { name: "保存额度配置", exact: true })
      .click();
    await expect
      .poll(() => api.calls.filter((call) => call.method === "PUT").length)
      .toBe(1);
    expect(api.calls.find((call) => call.method === "PUT")?.body).toMatchObject(
      {
        source: {
          headers: { "X-New": "[redacted]", "X-Second": "synthetic-second" },
          windows: [
            {
              models: ["gpt-*", "claude-*"],
              excluded_models: ["gpt-mini*", "claude-haiku*"],
            },
          ],
        },
        policy: {
          windows: {
            daily: { percent: 85, amount: 75 },
            monthly: { percent: 90 },
          },
        },
      },
    );
    expect(api.calls.filter((call) => call.method === "POST")).toHaveLength(0);
  });
}
