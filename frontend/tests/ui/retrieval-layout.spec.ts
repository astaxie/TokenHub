import type { Model, ModelRoute, Provider, ProviderModel, RequestDetail } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { fixedTime, project, shellResponses } from "./fixtures/shell";

const provider: Provider = { id: "p", name: "Retrieval UI Provider", type: "mock", status: "active", healthy: true, priority: 1 };
const models: Model[] = ["embedding", "rerank"].map(modality => ({ id: modality, name: `ui-bge-${modality}`, family: "bge", modality, status: "active", input_price_usd_per_1m: 9, embedding_price_usd_per_1m: 0.5, metadata: { directory_role: "external", ...(modality === "rerank" ? { search_unit_price_usd: "0.003" } : {}) } }));
const inventory: ProviderModel[] = models.map(model => ({ id: `pm-${model.modality}`, provider_id: "p", upstream_model: `bge-${model.modality}-v2-with-a-long-deployment-name`, modality: model.modality, status: "active", call_supported: true, input_price_usd_per_1m: 0.2, cache_read_price_usd_per_1m: 0.7, output_price_usd_per_1m: 0.8, metadata: { keep: "unchanged" } }));
const routes: ModelRoute[] = models.map((model, i) => ({ id: `r${i}`, model_name: model.name, provider_id: "p", provider_model: inventory[i].upstream_model, status: "active", weight: 100, priority: 1 }));

for (const mobile of [false, true]) {
  test(`retrieval-layout directory-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
    api.replaceResponse("GET", "/api/admin/overview", { ...overview, models, providers: [provider] });
    api.replaceResponse("GET", "/api/admin/provider-models", { data: inventory });
    api.respond("GET", "/api/admin/routing-rules", { data: routes });
    api.respond("GET", "/api/admin/provider-catalog", { data: [] });
    await page.goto("/models");
    const table = page.locator(".model-directory-table");
    await expect(table.getByRole("row").filter({ hasText: "ui-bge-embedding" })).toContainText("$0.500000");
    await expect(table.getByRole("row").filter({ hasText: "ui-bge-rerank" })).toContainText("$0.003000");
    await expect(table).not.toContainText("0 ctx");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    if (!mobile) expect(await page.locator(".model-directory-table-wrap").evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await capture(page, info, table, `directory-${mobile ? "mobile" : "desktop"}`, "检索模型价格与操作布局", "viewport");
  });

  test(`retrieval-layout audit-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    api.respond("GET", "/api/admin/audit/events", { data: [] });
    api.respond("GET", "/api/admin/api-keys", { data: [] });
    const log = { id: "log", request_id: "req-layout", project_id: project.id, api_key_id: "key", model: "ui-bge-rerank", provider_id: "p", provider_model: "bge-reranker-v2-m3", status_code: 200, latency_ms: 70, total_tokens: 53, created_at: fixedTime };
    const detail: RequestDetail = { log, usage: [], attempts: [], payload: { id: "payload", request_id: log.request_id, created_at: fixedTime, request_body: JSON.stringify({ query: "capital", documents: ["Paris"] }), response_body: JSON.stringify({ results: [{ index: 0, relevance_score: 0.99, document: { text: "Paris is the capital of France." } }] }, null, 2), request_truncated: false, response_truncated: false } };
    api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [log], pagination: { page: 1, page_size: 20, total: 1, total_pages: 1 }, summary: { all: 1, ok: 1, error: 0, average_latency_ms: 70 } } }), query => expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" }));
    api.respond("GET", "/api/admin/audit/requests/req-layout", detail);
    await page.goto("/audit");
    const panel = page.locator(".request-detail-panel");
    await expect(panel.getByRole("tabpanel", { name: "Response" })).toContainText("Paris is the capital");
    await panel.getByRole("tab", { name: "Request", exact: true }).click();
    await expect(panel.getByRole("tabpanel", { name: "Request" })).toContainText("documents");
    await panel.getByRole("tab", { name: "Response", exact: true }).click();
    await panel.locator("details").first().locator("summary").click();
    await expect(panel.getByText("客户端 IP", { exact: true })).toBeVisible();
    await panel.locator("details").first().locator("summary").click();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    if (mobile) await panel.scrollIntoViewIfNeeded();
    else await page.evaluate(() => window.scrollTo(0,0));
    await capture(page, info, panel, `audit-${mobile ? "mobile" : "desktop"}`, "响应优先的请求详情", "viewport");
  });
}

for (const mobile of [false, true]) {
test(`retrieval-layout inventory-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({width:390,height:844});
  const provider: Provider = { id: "prv_ui_embedding", name: "UI Embedding", type: "mock", base_url: "http://inference.example.test", priority: 1, status: "active", healthy: true };
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "local", name: "Mock Catalog", display_name: "Mock Provider", type: "mock", models_count: 0, source: "plugin" }] });
  api.respond("GET", "/api/admin/provider-catalog/local", { data: { id: "local", name: "Local Cluster", type: "mock", models_count: 0, models: [], source: "plugin" } });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "mock", capabilities: ["embeddings"], plugin_id: "tokenhub.provider.mock" }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: "tokenhub.provider.mock", name: "Mock Plugin", version: "1.0.0", source: "local_file", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [{ kind: "provider_type", name: "mock" }] }] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) {
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  }
  api.define("GET", "/api/admin/audit/requests", () => ({ json: {
    data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 },
    summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 },
  } }), query => { expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" }); });
  const imported = [{ id: "pm-embedding", provider_id: provider.id, upstream_model: "bge-m3", modality: "embedding", status: "active", input_price_usd_per_1m: 0, cache_read_price_usd_per_1m: 0.7, output_price_usd_per_1m: 0.8, metadata: { keep: "unchanged" } }, { id: "pm-rerank", provider_id: provider.id, upstream_model: "bge-reranker-v2-m3", modality: "rerank", status: "active", input_price_usd_per_1m: 0.2 }];
  api.replaceResponse("GET", "/api/admin/provider-models", { data: imported });
  api.define("PATCH", "/api/admin/provider-models/pm-embedding", input => {
    expect(input.body).toMatchObject({ input_price_usd_per_1m: 0.4, cache_read_price_usd_per_1m: 0.7, output_price_usd_per_1m: 0.8, metadata: { keep: "unchanged" } });
    imported[0].input_price_usd_per_1m = 0.4;
    api.replaceResponse("GET", "/api/admin/provider-models", {data:imported});
    return { json: { ...imported[0], ...(input.body as object) } };
  });
  await page.goto("/providers");
  await page.getByRole("row").filter({ hasText: "UI Embedding" }).getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.locator(".provider-modal");
  await editor.getByRole("tab", { name: "模型", exact: true }).click();
  const inventory = editor.locator(".provider-model-inventory");
  await expect(inventory.getByText("缓存读成本 USD/1M", { exact: true })).toHaveCount(0);
  await expect(inventory.getByText("输入成本尚未确认（不阻断已有线路）", { exact: true })).toBeVisible();
  await capture(page, testInfo, editor, `unconfirmed-cost-${mobile ? "mobile" : "desktop"}`, "零输入成本未确认提示", "viewport");
  await inventory.getByLabel("输入成本 USD/1M: bge-m3", { exact: true }).fill("0.4");
  await inventory.locator(".retrieval-cost-row").filter({ hasText: "bge-m3" }).getByRole("button", { name: "保存成本", exact: true }).click();
  await expect(inventory).toContainText("渠道成本价已保存");
  await expect(inventory.locator(".retrieval-cost-row").filter({hasText:"bge-m3"}).getByRole("button",{name:"保存成本",exact:true})).toBeEnabled();
  expect(await inventory.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  await capture(page, testInfo, editor, `inventory-${mobile ? "mobile" : "desktop"}`, "检索模型紧凑成本配置", "viewport");
});

}

test("retrieval-layout distinguishes free and unknown prices", async ({ page, api }, info) => {
  const freeModels: Model[] = [
    { ...models[0], id: "free", name: "confirmed-free", embedding_price_usd_per_1m: 0, metadata: { directory_role: "external", retrieval_pricing_confirmed: "true" } },
    { ...models[0], id: "unknown", name: "unconfirmed-price", embedding_price_usd_per_1m: 0, metadata: { directory_role: "external" } },
  ];
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: freeModels, providers: [provider] });
  api.respond("GET", "/api/admin/routing-rules", { data: freeModels.map((model, index) => ({ ...routes[0], id: `free-route-${index}`, model_name: model.name })) });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  await page.goto("/models");
  const table = page.locator(".model-directory-table");
  await expect(table.getByRole("row").filter({ hasText: "confirmed-free" })).toContainText("$0.000000/Mt");
  await expect(table.getByRole("row").filter({ hasText: "unconfirmed-price" })).toContainText("$-");
  await capture(page, info, table, "free-vs-unknown", "明确免费与未配置单价", "viewport");
});

for (const mobile of [false, true]) {
  test(`retrieval-layout rejected-route-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    api.respond("GET", "/api/admin/audit/events", { data: [] });
    api.respond("GET", "/api/admin/api-keys", { data: [] });
    const log = { id: "rejected", request_id: "req-rejected", project_id: project.id, api_key_id: "key", model: "ui-embedding", status_code: 501, error_code: "provider_capability_not_supported", latency_ms: 10, created_at: fixedTime };
    const message = "No eligible embedding route remains. The upstream inventory model type is not embedding.";
    const detail: RequestDetail = { log, usage: [], attempts: [], payload: { id: "payload", request_id: log.request_id, created_at: fixedTime, request_body: "{}", response_body: JSON.stringify({ error: { message, details: { stage: "route_selection", upstream_attempted: false } } }), request_truncated: false, response_truncated: false } };
    api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [log], pagination: { page: 1, page_size: 20, total: 1, total_pages: 1 }, summary: { all: 1, ok: 0, error: 1, average_latency_ms: 10 } } }), query => expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" }));
    api.respond("GET", "/api/admin/audit/requests/req-rejected", detail);
    await page.goto("/audit");
    const panel = page.locator(".request-detail-panel");
    await expect(panel.getByRole("alert")).toContainText(message);
    expect(await panel.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    expect(await panel.getByRole("alert").evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
    await expect(panel.locator(".request-usage-total")).not.toContainText("$0.000000");
    await expect(panel.getByRole("alert")).toContainText("请求在路由检查阶段被拒绝，尚未发送到上游。");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await panel.getByRole("alert").scrollIntoViewIfNeeded();
    await capture(page, info, panel, `rejected-${mobile ? "mobile" : "desktop"}`, "路由前拒绝原因直接显示", "viewport");
  });
}
