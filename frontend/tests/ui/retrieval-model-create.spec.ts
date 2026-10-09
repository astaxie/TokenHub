import { test, expect, capture } from "./harness";
import { shellResponses } from "./fixtures/shell";

for (const modality of ["rerank", "embedding"] as const) {
  test(`models create-${modality}-pricing`, async ({ page, api }, testInfo) => {
    const model = { name: `ui-${modality}`, family: "test", category: "other", modality, status: "active", metadata: { source: "tokenhub-standard-catalog", search_unit_price_usd: "0.003", retrieval_pricing_confirmed: "true" } };
    const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
    api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: [model], providers: [{ id: "p", name: "UI Provider", type: "mock", priority: 1, status: "active" }] });
    api.replaceResponse("GET", "/api/admin/provider-models", { data: [{ id: "pm", provider_id: "p", upstream_model: model.name, modality, status: "active", call_supported: true }] });
    for (const path of ["routing-rules", "provider-catalog"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
    let saved = false;
    api.define("POST", "/api/admin/models", input => {
      const body = input.body as { metadata: Record<string,string>; routes: unknown[] };
      expect(body.metadata.retrieval_pricing_confirmed).toBe("true");
      if (modality === "rerank") expect(body.metadata.search_unit_price_usd).toBe("0.004");
      expect(body.routes).toHaveLength(1);
      saved = true;
      return { status: 201, json: { ...model, ...body } };
    });
    await page.goto("/models");
    await page.getByRole("button", { name: "新建对外模型", exact: true }).click();
    const editor = page.locator(".model-create-modal");
    await editor.locator(".model-create-template").filter({ hasText: model.name }).click();
    await editor.getByRole("button", { name: "下一步：选择 Provider 模型" }).click();
    await expect(editor.getByRole("button", { name: "高级模型设置" })).toHaveAttribute("aria-expanded", "false");
    await expect(editor.getByLabel("对外模型 ID", { exact: true })).toBeVisible();
    await expect(editor.getByLabel("系列", { exact: true })).not.toBeVisible();
    await editor.getByText("UI Provider", { exact: false }).first().click();
    const confirmation = editor.getByRole("radiogroup", { name: "确认检索模型收费配置（包含免费价格）", exact: true });
    await expect(confirmation).toBeVisible();
    if (modality === "rerank") {
      const price = editor.getByLabel("搜索单元价格 USD/次", { exact: true });
      await expect(price).toHaveValue("0.003");
      await price.fill("0.004");
    } else {
      await expect(editor.getByLabel("搜索单元价格 USD/次", { exact: true })).toHaveCount(0);
    }
    await editor.locator(".model-create-pricing-note").scrollIntoViewIfNeeded();
    await capture(page, testInfo, editor, `create-${modality}-pricing`, "检索模型创建定价配置", "viewport");
    await editor.getByRole("button", { name: "创建对外模型", exact: true }).click();
    await expect(editor).not.toBeVisible();
    expect(saved).toBe(true);
  });
}
