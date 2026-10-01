import type { Provider } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";

for (const mobile of [false, true]) {
test(`providers rerank-settings-${mobile ? "mobile" : "desktop"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  const provider: Provider = { id: "prv_ui_rerank", name: "UI Rerank", type: "openai-compatible", base_url: "http://inference.example.test/v2", priority: 1, status: "active", healthy: true, options: { catalog_id: "custom", embedding_protocol: "tei" } };
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "custom", name: "Custom Catalog", display_name: "Custom Provider", type: "openai-compatible", models_count: 0, source: "plugin" }] });
  api.respond("GET", "/api/admin/provider-catalog/custom", { data: { id: "custom", name: "Local Cluster", type: "openai-compatible", models_count: 0, models: [], source: "plugin" } });
  api.define("POST", "/api/admin/provider-catalog/custom", input => {
    expect(input.body).toMatchObject({ provider_id: provider.id, type: "openai-compatible", base_url: provider.base_url });
    return { json: { data: { id: "custom", name: "Custom Provider", type: "openai-compatible", models_count: 0, models: [], source: "builtin" } } };
  });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "openai-compatible", capabilities: ["rerank"], plugin_id: "tokenhub.provider.openai-compatible" }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: "tokenhub.provider.openai-compatible", name: "Compatible Plugin", version: "1.0.0", source: "local_file", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [{ kind: "provider_type", name: "openai-compatible" }] }] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => { expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" }); });
  api.define("PATCH", "/api/admin/providers/prv_ui_rerank", input => {
    const payload=input.body as {options:Record<string,string>};
    expect(payload.options).toMatchObject({embedding_protocol:"tei",rerank_protocol:"cohere",rerank_path:"/rerank"});
    return {json:{...provider,options:payload.options}};
  });
  await page.goto("/providers");
  await page.getByRole("row").filter({hasText:"UI Rerank"}).getByRole("button",{name:"编辑",exact:true}).click();
  const editor=page.locator(".provider-modal");
  await editor.getByRole("tab",{name:"高级",exact:true}).click();
  await editor.getByText("文本重排配置",{exact:true}).click();
  const section = editor.locator("details").filter({ has: page.locator("summary", { hasText: "文本重排配置" }) });
  await expect(section.locator(".retrieval-endpoint-preview code")).toHaveText("http://inference.example.test/v2/rerank");
  await expect(section.locator("code").filter({ hasText: /^jina$/ })).toBeVisible();
  await editor.getByRole("combobox",{name:"重排协议",exact:true}).selectOption("qwen");
  await expect(editor.getByLabel("重排接口路径", { exact: true })).toHaveAttribute("placeholder", "/reranks");
  await editor.getByRole("combobox",{name:"重排协议",exact:true}).selectOption("cohere");
  await editor.getByLabel("重排接口路径",{exact:true}).fill("/rerank");
  await section.getByText("查看配置示例", { exact: true }).click();
  expect(await editor.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  await capture(page,testInfo,editor,"rerank-provider-settings","重排渠道协议配置","viewport");
  await section.locator(".retrieval-examples").scrollIntoViewIfNeeded();
  await capture(page, testInfo, editor, "connection-example", "完整地址配置示例", "viewport");
  await editor.getByRole("button",{name:"保存",exact:true}).click();
  await expect(editor).not.toBeVisible();
});

}
