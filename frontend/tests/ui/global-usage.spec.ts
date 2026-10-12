import { languageStorageKey, sessionStorageKey } from "../../features/admin/core/types";
import { capture, expect, test } from "./harness";
import { fixedTime, user } from "./fixtures/shell";
import { installUsageFixtures, usageCalls, usageCounts, usageReport } from "./fixtures/global-usage";
import type { MockResponse } from "./network";

const ranges = [
  { value: "today", label: "今日" }, { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" }, { value: "all", label: "全部" },
] as const;

for (const viewport of ["desktop", "mobile"] as const) {
  test(`global-usage range filtering ${viewport}`, async ({ page, api }, info) => {
    if (viewport === "mobile") await page.setViewportSize({ width: 390, height: 844 });
    installUsageFixtures(api);
    await page.goto("/usage");
    const usage = page.locator(".global-usage");
    await expect(page.locator(".page-context-chip")).toHaveCount(0);
    const group = usage.getByRole("group", { name: "用量时间范围" });
    await expect(group.getByRole("button")).toHaveCount(4);
    await expect(group.getByRole("button", { name: "今日", exact: true })).toHaveAttribute("aria-pressed", "true");
    for (const range of ranges) {
      await group.getByRole("button", { name: range.label, exact: true }).click();
      await expect(group.getByRole("button", { name: range.label, exact: true })).toHaveAttribute("aria-pressed", "true");
      await expect(usage.locator('.global-usage-stats [aria-label="请求数"]')).toContainText(String(usageCounts[range.value]));
      await expect(usage.getByText(`ui-${range.value}-model-with-a-long-enterprise-deployment-name`, { exact: true })).toBeVisible();
      await expect(usage.locator(".global-usage-cost")).toContainText(`$${(usageCounts[range.value] * 0.5).toFixed(2)}`);
      expect(usageCalls(api).at(-1)?.query.get("range")).toBe(range.value);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await usage.scrollIntoViewIfNeeded();
      await page.evaluate(() => window.scrollTo(0, 0));
      await capture(page, info, usage, `global-usage-${range.value}-${viewport}`, `全局用量：${range.label}统计与趋势`, "viewport");
    }
    if (viewport === "mobile") {
      await usage.locator(".global-usage-chart").scrollIntoViewIfNeeded();
      await capture(page, info, usage.locator(".global-usage-chart"), "global-usage-mobile-chart", "全局用量：移动端输入输出趋势", "viewport");
    }
    const count = usageCalls(api).length;
    await usage.getByRole("button", { name: "刷新", exact: true }).click();
    await expect.poll(() => usageCalls(api).length).toBe(count + 1);
    expect(usageCalls(api).at(-1)?.query.get("range")).toBe("all");
    expect(api.calls.filter(call => /\/usage\/(daily|breakdown|timeseries)$/.test(call.path))).toEqual([]);
  });
}

test("global-usage loading and late response preserve the selected range", async ({ page, api }, info) => {
  let release!: (response: MockResponse) => void;
  const delayed = new Promise<MockResponse>(resolve => { release = resolve; });
  installUsageFixtures(api, range => range === "today" ? delayed : { json: usageReport(range) });
  try {
    await page.goto("/usage");
    const usage = page.locator(".global-usage");
    await expect(usage.getByRole("status")).toContainText("正在加载用量");
    await expect(usage.locator(".global-usage-stats")).toHaveCount(0);
    await capture(page, info, usage, "global-usage-loading", "全局用量：请求完成前显示加载状态", "viewport");
    await usage.getByRole("button", { name: "7 天", exact: true }).click();
    await expect(usage.locator('.global-usage-stats [aria-label="请求数"]')).toContainText("17");
    release({ json: usageReport("today") });
    await expect(usage.getByRole("button", { name: "7 天", exact: true })).toHaveAttribute("aria-pressed", "true");
    await expect(usage.getByText("ui-today-model-with-a-long-enterprise-deployment-name", { exact: true })).toHaveCount(0);
    await expect(usage.locator(".global-usage-cost")).toContainText("$8.50");
  } finally {
    release({ json: usageReport("today") });
  }
});

test("global-usage failed range hides stale totals and can retry", async ({ page, api }, info) => {
  let failed = true;
  installUsageFixtures(api, range => range === "30d" && failed
    ? { status: 503, json: { error: { message: "Synthetic usage service unavailable" } } }
    : { json: usageReport(range) });
  await page.goto("/usage");
  const usage = page.locator(".global-usage");
  await expect(usage.locator(".global-usage-stats")).toBeVisible();
  await usage.getByRole("button", { name: "30 天", exact: true }).click();
  await expect(usage.getByRole("alert")).toContainText("用量统计加载失败");
  await expect(usage.locator(".global-usage-stats")).toHaveCount(0);
  await expect(usage.getByText("ui-today-model-with-a-long-enterprise-deployment-name", { exact: true })).toHaveCount(0);
  await capture(page, info, usage, "global-usage-error", "全局用量：切换失败时保留筛选，不显示旧范围总量", "viewport");
  failed = false;
  await usage.getByRole("button", { name: "重试", exact: true }).click();
  await expect(usage.getByRole("alert")).toHaveCount(0);
  await expect(usage.locator('.global-usage-stats [aria-label="请求数"]')).toContainText("43");
  expect(usageCalls(api).at(-1)?.query.get("range")).toBe("30d");
});

test("global-usage empty period retains its controls", async ({ page, api }, info) => {
  installUsageFixtures(api, range => ({ json: usageReport(range, true) }));
  await page.goto("/usage");
  const usage = page.locator(".global-usage");
  await expect(usage.locator('.global-usage-stats [aria-label="请求数"]')).toContainText("0");
  await expect(usage.getByText("此时间范围内暂无用量", { exact: true })).toBeVisible();
  await expect(usage.getByRole("button", { name: "7 天", exact: true })).toBeEnabled();
  await capture(page, info, usage, "global-usage-empty", "全局用量：无记录时仍可选择时间范围", "viewport");
});

test("global-usage refreshes the selected period every thirty seconds", async ({ page, api }) => {
  installUsageFixtures(api);
  await page.clock.install({ time: new Date(fixedTime) });
  await page.clock.pauseAt(new Date(new Date(fixedTime).getTime() + 1000));
  await page.goto("/usage");
  const usage = page.locator(".global-usage");
  await expect(usage.locator(".global-usage-stats")).toBeVisible();
  await usage.getByRole("button", { name: "7 天", exact: true }).click();
  await expect(usage.locator('.global-usage-stats [aria-label="请求数"]')).toContainText("17");
  const count = usageCalls(api).length;
  await page.clock.fastForward(30000);
  await expect.poll(() => usageCalls(api).length).toBe(count + 1);
  expect(usageCalls(api).at(-1)?.query.get("range")).toBe("7d");
});

for (const locale of [
  { language: "en", option: "English", group: "Usage time range", today: "Today", seven: "7 days", thirty: "30 days", all: "All", cost: "Estimated Cost" },
  { language: "ja", option: "日本語", group: "使用量の期間", today: "今日", seven: "7 日", thirty: "30 日", all: "すべて", cost: "推定コスト" },
] as const) {
  test(`global-usage locale ${locale.language}`, async ({ page, api }, info) => {
    if (locale.language === "ja") await page.setViewportSize({ width: 390, height: 844 });
    installUsageFixtures(api);
    await page.goto("/usage");
    await page.getByRole("button", { name: "界面语言", exact: true }).click();
    await page.getByRole("option", { name: locale.option, exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
    expect(await page.evaluate(key => localStorage.getItem(key), languageStorageKey)).toBe(locale.language);
    const usage = page.locator(".global-usage");
    const group = usage.getByRole("group", { name: locale.group });
    await expect(group.getByRole("button", { name: locale.today, exact: true })).toHaveAttribute("aria-pressed", "true");
    for (const label of [locale.seven, locale.thirty, locale.all]) await expect(group.getByRole("button", { name: label, exact: true })).toBeVisible();
    await group.getByRole("button", { name: locale.thirty, exact: true }).click();
    await expect(usage.locator(".global-usage-cost")).toContainText(locale.cost);
    await expect(usage.getByText("ui-30d-model-with-a-long-enterprise-deployment-name", { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.evaluate(() => window.scrollTo(0, 0));
    await capture(page, info, usage, `global-usage-${locale.language}`, "全局用量：多语言筛选、统计和趋势", "viewport");
  });
}

test.describe("global-usage personal scope", () => {
  test.use({ sessionUser: { ...user, role: "user" } });
  test("excludes provider dimensions", async ({ page, api }, info) => {
    installUsageFixtures(api);
    await page.goto("/usage");
    expect(await page.evaluate(key => JSON.parse(sessionStorage.getItem(key)!).user.role, sessionStorageKey)).toBe("user");
    const usage = page.locator(".global-usage");
    await expect(usage.locator(".global-usage-stats")).toBeVisible();
    await usage.getByRole("button", { name: "全部", exact: true }).click();
    await expect(usage.getByText("ui-all-model-with-a-long-enterprise-deployment-name", { exact: true })).toBeVisible();
    await expect(usage.getByRole("heading", { name: /供应商|资源账号|成本中心/, includeHidden: true })).toHaveCount(0);
    await capture(page, info, usage, "global-usage-personal", "个人用量：时间筛选保留权限范围", "viewport");
  });
});
