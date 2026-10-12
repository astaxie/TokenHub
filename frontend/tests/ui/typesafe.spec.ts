import { test, expect, capture } from "./harness";
import { model } from "./fixtures/shell";

test("typesafe decision modality and input-only pricing can be configured", async ({ page, api }, testInfo) => {
  let saved = false;
  api.respond("GET", "/api/admin/routing-rules", { data: [] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [{ id: "typesafe", name: "TypeSafe", type: "typesafe", models_count: 3, source: "plugin" }] });
  api.define("PATCH", `/api/admin/models/${model.name}`, input => {
    expect(input.body).toMatchObject({ name: model.name, modality: "decision", capabilities: ["systemone"], input_price_usd_per_1m: 0.042, output_price_usd_per_1m: 0 });

    saved = true;
    return { json: { data: model } };
  });
  await page.goto("/models");
  await page.getByRole("group", { name: "发布状态" }).getByRole("button", { name: "全部", exact: true }).click();
  const row = page.getByRole("row").filter({ hasText: model.name });
  await expect(row).toBeVisible();
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  const dialog = page.locator("form.modal");
  await dialog.getByRole("combobox", { name: "能力", exact: true }).selectOption({ label: "决策" });
  await expect(dialog.getByRole("combobox", { name: "能力", exact: true })).toHaveValue("decision");
  await dialog.getByRole("combobox", { name: "模型类型", exact: true }).selectOption("custom");
  await dialog.getByLabel("系列", { exact: true }).fill("jev");
  await dialog.getByLabel("能力标签，逗号分隔", { exact: true }).fill("systemone");
  await dialog.getByLabel("显示名称", { exact: true }).fill("Jev Support Decisions");
  await dialog.getByLabel(/^对外输入价 USD\/1M/).fill("0.042");
  await dialog.getByLabel("对外输出价 USD/1M", { exact: true }).fill("0");
  await expect(dialog.getByLabel("对外输出价 USD/1M", { exact: true })).toHaveValue("0");
  await capture(page, testInfo, dialog, "typesafe-decision-model", "Jev 决策模型：独立能力与免费输出", "viewport");
  await dialog.getByRole("button", { name: "保存", exact: true }).click();
  await expect(dialog).toBeHidden();
  expect(saved).toBe(true);
});
