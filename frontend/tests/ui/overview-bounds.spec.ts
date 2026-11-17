import { test, expect, capture } from "./harness";
import { model, shellResponses } from "./fixtures/shell";
import type { MockAPI } from "./network";

function installOverview(api: MockAPI, state = "ready") {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  const { models: _models, ...base } = overview;
  const payload = { ...base, summary: state === "preparing" || state === "error" ? { model_count: 300, active_model_count: 250 } : { ...(base.summary as object), model_count: 300, active_model_count: 250 }, summary_status: { state, processed: 1200 }, alerts: [{ id: "recent_alert", code: "quota_exceeded", message: "Synthetic recent alert", severity: "warning", created_at: "2026-09-07T01:00:00Z" }] };
  api.replaceResponse("GET", "/api/admin/overview", payload);
  api.respond("GET", "/api/admin/usage/timeseries", { data: [] });
  for (const path of ["plugins", "plugin-ui-manifest", "plugin-actions"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-chain", { data: { hooks: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  api.respond("GET", "/api/admin/resources/announcements", { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 } } }), () => undefined);
  api.define("GET", "/api/admin/model-search", () => ({ json: { data: [model] } }), (query) => expect(query.get("q")).toBe(model.name));
  return payload;
}

test("overview-bounds loads bounded data and searches models on demand", async ({ page, api }, info) => {
  installOverview(api);
  await page.goto("/overview");
  await expect(page.getByRole("heading", { name: "网关概览", exact: true })).toBeVisible();
  await expect(page.getByText("Synthetic recent alert")).toBeVisible();
  expect(api.calls.filter(call => call.path === "/api/admin/models")).toHaveLength(0);
  await page.locator(".topbar input").fill(model.name);
  await expect(page.getByRole("option", { name: new RegExp(model.name) })).toBeVisible();
  expect(api.calls.some(call => call.path === "/api/admin/model-search")).toBe(true);
  await page.locator(".topbar input").fill("");
  await capture(page, info, page.locator(".app-shell"), "overview-bounded", "总览：有界告警与按需模型搜索", "viewport");
});

test("overview-bounds keeps the page usable while preparing historical statistics", async ({ page, api }, info) => {
  const payload = installOverview(api, "preparing");
  await page.goto("/overview");
  await expect(page.getByRole("status")).toContainText("历史统计准备中");
  const requests = page.locator(".overview-metric-card").filter({ has: page.getByText("总请求", { exact: true }) });
  await expect(requests.locator(".metric-value")).toHaveText("—");
  await capture(page, info, page.locator(".app-shell"), "overview-preparing", "总览：历史统计准备进度", "viewport");
  api.replaceResponse("GET", "/api/admin/overview", { ...payload, summary: { request_count: 123, total_tokens: 45, estimated_cost_usd: 1 }, summary_status: { state: "ready" } });
  await expect(requests.locator(".metric-value")).toHaveText("123", { timeout: 10000 });
});

test("overview-bounds reports a summary failure independently", async ({ page, api }) => {
  installOverview(api, "error");
  await page.goto("/overview");
  await expect(page.getByRole("status")).toContainText("统计暂时不可用");
  await expect(page.getByText("Synthetic recent alert")).toBeVisible();
  await expect(page.getByRole("heading", { name: "网关概览", exact: true })).toBeVisible();
});

test("overview-bounds pages and filters historical alerts on the server", async ({ page, api }, info) => {
  api.define("GET", "/api/admin/alerts", (request) => {
    const page = Number(request.query.get("page"));
    return { json: { page, page_size: 50, total: 120, data: [{ id: `history_${page}`, code: `history_page_${page}`, message: "Synthetic historical alert", severity: "warning", created_at: "2026-09-07T01:00:00Z" }] } };
  }, query => { expect(query.get("page_size")).toBe("50"); expect(Number(query.get("page"))).toBeGreaterThan(0); });
  await page.goto("/alert-events");
  await expect(page.getByText("history_page_1", { exact: true })).toBeVisible();
  await page.getByTitle("下一页").click();
  await expect(page.getByText("history_page_2", { exact: true })).toBeVisible();
  await page.getByLabel("项目 ID").fill("project_test");
  await expect(page.getByText("history_page_1", { exact: true })).toBeVisible();
  expect(api.calls.at(-1)?.query.get("project_id")).toBe("project_test");
  await capture(page, info, page.locator(".app-shell"), "alerts-paged", "告警：服务端分页与项目筛选", "viewport");
});

test("overview-bounds renders metadata before a delayed breakdown completes", async ({ page, api }) => {
  installOverview(api);
  let release!: () => void;
  const blocked = new Promise<void>(resolve => { release = resolve; });
  api.replaceHandler("GET", "/api/admin/usage/breakdown", async () => {
    await blocked;
    return { json: { projects: [], models: [], members: [], providers: [], provider_resources: [], cost_centers: [] } };
  });
  try {
    await page.goto("/overview");
    await expect(page.getByRole("heading", { name: "网关概览", exact: true })).toBeVisible();
    await expect(page.locator(".overview-metric-card").filter({ has: page.getByText("总请求", { exact: true }) }).locator(".metric-value")).toHaveText("3");
  } finally { release(); }
});

test("overview-bounds hides expired totals while the next refresh is pending", async ({ page, api }) => {
  const payload = installOverview(api);
  let release!: () => void;
  const blocked = new Promise<void>(resolve => { release = resolve; });
  let calls = 0;
  api.replaceHandler("GET", "/api/admin/overview", async () => {
    calls++;
    if (calls > 1) await blocked;
    return { json: { ...payload, summary_status: { state: "stale", updated_at: "2026-09-07T01:59:01Z" } } };
  });
  try {
    await page.goto("/overview");
    const metric = page.locator(".overview-metric-card").filter({ has: page.getByText("总请求", { exact: true }) }).locator(".metric-value");
    await expect(metric).toHaveText("3");
    await expect(metric).toHaveText("—", { timeout: 5000 });
    await expect(page.locator(".overview-trend-panel .overview-panel-head strong")).toHaveText("—");
  } finally { release(); }
});

test("overview-bounds mobile alert filters fit the viewport", async ({ page, api }, info) => {
  await page.setViewportSize({ width: 390, height: 844 });
  api.define("GET", "/api/admin/alerts", () => ({ json: { page: 1, page_size: 50, total: 0, data: [] } }), () => undefined);
  await page.goto("/alert-events");
  await expect(page.getByLabel("项目 ID")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  await capture(page, info, page.locator(".app-shell"), "alerts-mobile", "告警：移动端筛选与空状态", "viewport");
});
