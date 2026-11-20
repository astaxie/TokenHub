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
  api.replaceHandler("GET", "/api/admin/usage/breakdown", () => ({
    status: 503, json: { error: { code: "overview_preparing", message: "Historical statistics are preparing" } },
  }));
  await page.goto("/overview");
  await expect(page.getByRole("status")).toContainText("历史统计准备中");
  const requests = page.locator(".overview-metric-card").filter({ has: page.getByText("总请求", { exact: true }) });
  await expect(requests.locator(".metric-value")).toHaveText("—");
  await expect(page.locator(".overview-share-panel .compact-empty")).toHaveText("历史统计准备中");
  await expect(page.locator(".overview-donut strong")).toHaveText("—");
  await capture(page, info, page.locator(".app-shell"), "overview-preparing", "总览：历史统计准备进度", "viewport");
  api.replaceResponse("GET", "/api/admin/overview", { ...payload, summary: { request_count: 123, total_tokens: 45, estimated_cost_usd: 1 }, summary_status: { state: "ready" } });
  api.replaceResponse("GET", "/api/admin/usage/breakdown", { models: [], providers: [], members: [], projects: [], cost_centers: [] });
  await expect(requests.locator(".metric-value")).toHaveText("123", { timeout: 10000 });
  await expect(page.locator(".overview-donut strong")).toHaveText("$0");
});

test("overview-bounds reports a summary failure independently", async ({ page, api }) => {
  installOverview(api, "error");
  await page.goto("/overview");
  await expect(page.getByRole("status")).toContainText("统计暂时不可用");
  await expect(page.getByText("Synthetic recent alert")).toBeVisible();
  await expect(page.getByRole("heading", { name: "网关概览", exact: true })).toBeVisible();
});

test("overview-bounds calculates provider shares against all providers", async ({ page, api }) => {
  installOverview(api);
  const providers = [40, 30, 20, 10].map(cost => ({ id: `provider-${cost}`, estimated_cost_usd: cost, request_count: 1 }));
  api.replaceResponse("GET", "/api/admin/usage/breakdown", { models: [], providers, projects: [], members: [], cost_centers: [] });
  await page.goto("/overview");
  await expect(page.locator(".overview-donut strong")).toHaveText("$100");
  await expect(page.locator(".overview-share-row strong")).toHaveText(["40%", "30%", "20%"]);
});

test("overview-bounds lets an in-flight preparation refresh complete", async ({ page, api }) => {
  const payload = installOverview(api, "preparing");
  let reads = 0;
  let release!: () => void;
  const blocked = new Promise<void>(resolve => { release = resolve; });
  api.replaceHandler("GET", "/api/admin/overview", async () => {
    if (++reads === 1) return { json: payload };
    await blocked;
    return { json: { ...payload, summary: { request_count: 123 }, summary_status: { state: "ready" } } };
  });
  try {
    await page.clock.install();
    await page.goto("/overview");
    await expect(page.getByRole("status")).toContainText("历史统计准备中");
    await page.clock.runFor(3000);
    await expect.poll(() => reads).toBe(2);
    await page.clock.runFor(3000);
    expect(reads).toBe(2);
    release();
    const requests = page.locator(".overview-metric-card").filter({ has: page.getByText("总请求", { exact: true }) });
    await expect(requests.locator(".metric-value")).toHaveText("123");
  } finally { release(); }
});

test("overview-bounds clears classifications after a breakdown failure", async ({ page, api }) => {
  const payload = installOverview(api);
  let overviews = 0;
  let breakdowns = 0;
  api.replaceHandler("GET", "/api/admin/overview", () => ++overviews === 1
    ? { json: { ...payload, summary_status: { state: "stale", updated_at: "2026-09-07T01:59:01Z" } } }
    : { json: { ...payload, summary: { request_count: 123 }, summary_status: { state: "ready" } } });
  api.replaceHandler("GET", "/api/admin/usage/breakdown", () => ++breakdowns === 1
    ? { json: { models: [{ id: "old-model-statistics", request_count: 42 }], providers: [], provider_resources: [], projects: [], members: [], cost_centers: [] } }
    : { status: 503, json: { error: { code: "temporarily_unavailable", message: "Breakdown failed" } } });
  api.replaceHandler("GET", "/api/admin/audit/requests", () => ({
    json: { data: [{ id: "recent", model: "only-in-recent-logs", status_code: 200 }], pagination: { page: 1, total: 1 } },
  }), () => undefined);
  await page.goto("/overview");
  const previous = page.getByText("old-model-statistics");
  await expect(previous).toBeVisible();
  await expect(page.getByText("Breakdown failed")).toBeVisible();
  await expect(previous).toHaveCount(0);
  await expect(page.locator(".overview-top-list")).not.toContainText("only-in-recent-logs");
  await expect(page.locator(".overview-donut strong")).toHaveText("—");
});

test("overview-bounds hides failed trend data while keeping a refreshed lifetime count", async ({ page, api }) => {
  const payload = installOverview(api);
  let overviews = 0;
  let reads = 0;
  api.replaceHandler("GET", "/api/admin/overview", () => ++overviews === 1
    ? { json: { ...payload, summary_status: { state: "stale", updated_at: "2026-09-07T01:59:02Z" } } }
    : { json: { ...payload, summary: { request_count: 1000 }, summary_status: { state: "ready" } } });
  api.replaceHandler("GET", "/api/admin/usage/timeseries", () => ++reads === 1
    ? { json: { data: [{ date: "2026-09-07", request_count: 123, total_tokens: 4, estimated_cost_usd: 1 }] } }
    : { status: 503, json: { error: { message: "Trend unavailable" } } });
  await page.goto("/overview");
  const trend = page.locator(".overview-trend-panel .overview-panel-head strong");
  await expect(trend).toHaveText("123");
  await expect(page.getByText("Trend unavailable")).toBeVisible();
  await expect(trend).toHaveText("—");
  await expect(page.locator(".overview-metric-card").filter({ has: page.getByText("总请求", { exact: true }) }).locator(".metric-value")).toHaveText("1,000");
});

test("overview-bounds searches for new models after loading the directory", async ({ page, api }) => {
  installOverview(api);
  api.respond("GET", "/api/admin/routing-rules", { data: [] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  const created = { ...model, id: "mdl_remote", name: "model-created-remotely" };
  api.replaceHandler("GET", "/api/admin/model-search", () => ({ json: { data: [created] } }), (query) => expect(query.get("q")).toBe(created.name));
  await page.goto("/models");
  await expect(page.getByText("可用模型", { exact: true }).locator("..").getByText("1", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "平台总览", exact: true }).click();
  await expect(page.getByText("Synthetic recent alert")).toBeVisible();
  await page.locator(".topbar input").fill(created.name);
  await expect(page.getByRole("option", { name: new RegExp(created.name) })).toBeVisible();
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
  await page.getByLabel("搜索告警").fill("quota_80%");
  await expect.poll(() => api.calls.at(-1)?.query.get("q")).toBe("quota_80%");
  await capture(page, info, page.locator(".app-shell"), "alerts-paged", "告警：服务端分页与项目筛选", "viewport");
});

test("overview-bounds prevents repeat alert delivery while the first send is pending", async ({ page, api }) => {
  api.define("GET", "/api/admin/alerts", () => ({ json: { total: 1, page: 1, page_size: 50,
    data: [{ id: "send-once", code: "quota_event", message: "Synthetic alert", severity: "warning" }] } }), () => undefined);
  let release!: () => void;
  const held = new Promise<void>(resolve => { release = resolve; });
  let sends = 0;
  api.define("POST", "/api/admin/alerts/send-once/deliver", async () => {
    sends++;
    await held;
    return { json: sends === 1 ? { status: "success" } : { status: "failed", error: "Synthetic channel failure" } };
  });
  try {
    await page.goto("/alert-events");
    const send = page.getByRole("button", { name: "发送", exact: true });
    await send.click();
    await expect(send).toBeDisabled();
    await send.evaluate(button => (button as HTMLButtonElement).click());
    release();
    await expect(page.getByText("已发送告警 quota_event")).toBeVisible();
    await expect(send).toBeEnabled();
    expect(sends).toBe(1);
    await send.click();
    await expect(page.getByText("Synthetic channel failure")).toBeVisible();
    await expect(page.getByText("已发送告警 quota_event")).toHaveCount(0);
  } finally { release(); }
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

test("overview-bounds reloads a valid alert page when retained history shrinks", async ({ page, api }) => {
  let total = 51;
  api.define("GET", "/api/admin/alerts", request => {
    const cursor = Number(request.query.get("page"));
    const data = cursor === 1 || total > 50
      ? [{ id: String(cursor), code: `history_page_${cursor}`, severity: "warning", message: "Synthetic alert" }]
      : [];
    return { json: { page: cursor, page_size: 50, total, data } };
  }, query => expect(query.get("page_size")).toBe("50"));
  await page.goto("/alert-events");
  await expect(page.getByText("history_page_1", { exact: true })).toBeVisible();
  await page.getByTitle("下一页").click();
  await expect(page.getByText("history_page_2", { exact: true })).toBeVisible();
  total = 1;
  await page.locator(".alert-history-filters").getByRole("button", { name: "刷新" }).click();
  await expect(page.getByText("history_page_1", { exact: true })).toBeVisible();
  expect(api.calls.at(-1)?.query.get("page")).toBe("1");
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
