import type { Provider } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";

for (const mobile of [false, true]) {
for (const nativeCatalog of [false, true]) {
test(`providers embedding-settings-${mobile ? "mobile" : "desktop"}-${nativeCatalog ? "cohere" : "custom"}`, async ({ page, api }, testInfo) => {
  if (mobile) await page.setViewportSize({ width: 390, height: 844 });
  const provider: Provider = { id: "prv_ui_embedding", name: "UI Embedding", type: "openai-compatible", base_url: nativeCatalog ? "http://inference.example.test/v2" : "http://inference.example.test/v1", options: { catalog_id: nativeCatalog ? "cohere" : "custom" }, priority: 1, status: "active", healthy: true };
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "custom", name: "Custom Catalog", display_name: "Custom Provider", type: "openai-compatible", models_count: 0, source: "plugin" }] });
  api.respond("GET", "/api/admin/provider-catalog/custom", { data: { id: "custom", name: "Local Cluster", type: "openai-compatible", models_count: 0, models: [], source: "plugin" } });
  api.define("POST", "/api/admin/provider-catalog/custom", input => {
    expect(input.body).toMatchObject({ provider_id: provider.id, type: "openai-compatible", base_url: provider.base_url });
    return { json: { data: { id: "custom", name: "Custom Provider", type: "openai-compatible", models_count: 0, models: [], source: "builtin" } } };
  });
  api.respond("GET", "/api/admin/provider-adapters", { data: [{ type: "openai-compatible", capabilities: ["embeddings"], plugin_id: "tokenhub.provider.openai-compatible" }] });
  api.respond("GET", "/api/admin/plugins", { data: [{ id: "tokenhub.provider.openai-compatible", name: "Compatible Plugin", version: "1.0.0", source: "local_file", kinds: ["provider"], placements: ["gateway_chain"], capabilities: [{ kind: "provider_type", name: "openai-compatible" }] }] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "routing-rules", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) {
    api.respond("GET", `/api/admin/${path}`, { data: [] });
  }
  api.define("GET", "/api/admin/audit/requests", () => ({ json: {
    data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 },
    summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 },
  } }), query => { expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" }); });
  api.define("PATCH", "/api/admin/providers/prv_ui_embedding", input => {
    const payload = input.body as { catalog_id: string; preserve_catalog: boolean; options: Record<string, string> };
    expect(payload.catalog_id).toBe("custom");
    expect(payload.preserve_catalog).toBe(true);
    expect(payload.options).toMatchObject({ embedding_protocol: "tei", embedding_path: "/embed", embedding_spaces: '{"bge-m3":"verified-space"}' });
    return { json: { ...provider, options: { ...payload.options, catalog_id: provider.options?.catalog_id } } };
  });
  await page.goto("/providers");
  await page.getByRole("row").filter({ hasText: "UI Embedding" }).getByRole("button", { name: "编辑", exact: true }).click();
  const editor = page.locator(".provider-modal");
  await editor.getByRole("tab", { name: "高级", exact: true }).click();
  await editor.getByText("文本 Embedding 配置", { exact: true }).click();
  const section = editor.locator("details").filter({ has: page.locator("summary", { hasText: "文本 Embedding 配置" }) });
  await expect(section.locator(".retrieval-endpoint-preview code")).toHaveText(`${provider.base_url}${nativeCatalog ? "/embed" : "/embeddings"}`);
  await expect(editor.getByLabel("Embedding 接口路径", { exact: true })).toHaveValue("");
  await editor.getByRole("combobox", { name: "Embedding 协议", exact: true }).selectOption("tei");
  await expect(editor.getByLabel("Embedding 接口路径", { exact: true })).toHaveAttribute("placeholder", "/embed");
  await expect(section.locator(".retrieval-endpoint-preview code")).toHaveText(`${provider.base_url}/embed`);
  await editor.getByLabel("Embedding 接口路径", { exact: true }).fill("https://inference.example.test/embed");
  await expect(editor.getByLabel("Embedding 接口路径", { exact: true })).toHaveAttribute("aria-invalid", "true");
  await editor.getByRole("button", { name: "保存", exact: true }).click();
  await expect(editor).toBeVisible();
  await editor.getByLabel("Embedding 接口路径", { exact: true }).fill("/embed");
  await editor.getByRole("textbox", { name: "向量空间映射", exact: true }).fill("/embeddings");
  await expect(editor.getByRole("textbox", { name: "向量空间映射", exact: true })).toHaveAttribute("aria-invalid", "true");
  await editor.getByRole("button", { name: "保存", exact: true }).click();
  await expect(editor).toBeVisible();
  await editor.getByRole("textbox", { name: "向量空间映射", exact: true }).fill('{"bge-m3":"verified-space"}');
  await section.getByText("查看配置示例", { exact: true }).click();
  expect(await editor.evaluate(el => el.scrollWidth <= el.clientWidth + 1)).toBe(true);
  await capture(page, testInfo, editor, "embedding-provider-settings", "文本向量协议与兼容空间配置", "viewport");
  await section.locator(".retrieval-examples").scrollIntoViewIfNeeded();
  await capture(page, testInfo, editor, "connection-example", "完整地址配置示例", "viewport");
  await editor.getByRole("button", { name: "保存", exact: true }).click();
  await expect(editor).not.toBeVisible();
});

}

}
