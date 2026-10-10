import { test, expect, capture } from "./harness";
import { shellResponses } from "./fixtures/shell";

for (const viewport of [{ width: 1440, height: 1000 }, { width: 390, height: 844 }]) {
  test(`models catalog-notices ${viewport.width}`, async ({ page, api }, info) => {
    await page.setViewportSize(viewport);
    const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
    const common = { category: "other", family: "test", modality: "chat", status: "active", context_window: 1000000 };
    const models = [
      { ...common, name: "ui-retired-model", metadata: { source: "tokenhub-standard-catalog", lifecycle_status: "retired", replacement_model: "ui-current-model", shutdown_at: "2026-08-01T00:00:00Z" } },
      { ...common, name: "ui-preview-model", metadata: { source: "tokenhub-standard-catalog", availability: "preview", call_support: "unsupported", pricing_status: "unverified" } },
    ];
    api.replaceResponse("GET", "/api/admin/overview", { ...overview, models, providers: [{ id: "p", name: "UI Provider", type: "mock", priority: 1, status: "active" }] });
    api.replaceResponse("GET", "/api/admin/provider-models", { data: [{ id: "pm", provider_id: "p", upstream_model: "ui-current-model", modality: "chat", status: "active", call_supported: true }] });
    for (const path of ["routing-rules", "provider-catalog"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
    await page.goto("/models");
    await page.getByRole("button", { name: "新建对外模型", exact: true }).click();
    const editor = page.locator(".model-create-modal");
    await expect(editor.getByText("官方入口已停用；请选择替代型号。")).toBeVisible();
    await expect(editor.getByText("替代型号：ui-current-model")).toBeVisible();
    await expect(editor.getByText("仅目录收录，当前尚未支持此接口调用。")).toBeVisible();
    await expect(editor.getByText("成本尚未配置；目录零值不代表免费。")).toBeVisible();
    await capture(page, info, editor, `catalog-notices-${viewport.width}`, "模型目录显示停用、预览和调用边界", "viewport");
  });
}
