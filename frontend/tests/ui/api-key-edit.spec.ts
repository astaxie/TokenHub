import type { Page } from "@playwright/test";
import type { APIKey } from "../../features/admin/core/types";
import { capture, expect, test } from "./harness";
import { model, project, user } from "./fixtures/shell";
import type { MockAPI } from "./network";

const original: APIKey = {
  id: "key_ui_edit", name: "UI Editable Key", project_id: project.id, owner_user_id: user.id, group: "test",
  model_access_mode: "restricted", allowed_models: ["retired-model", "ui-review-model"],
  status: "active", key_prefix: "sk_ui", key_suffix: "3456", limits: { daily_tokens: 1000, max_concurrency: 2 },
};
const expectedPatch = {
  name: original.name, group: "test", owner_user_id: user.id, status: "active", model_access_mode: "restricted",
  allowed_models: ["ui-review-model", "replacement-model"], ip_allowlist: [], rate_limit_rpm: null, token_limit_tpm: null,
  limits: { daily_requests: 0, monthly_requests: 0, daily_tokens: 2000, monthly_tokens: 0, daily_cost_usd: 0, monthly_cost_usd: 0, max_concurrency: 2 },
};

function setup(api: MockAPI, options: { key?: APIKey; patch?: typeof expectedPatch; fail?: boolean } = {}) {
  const key = structuredClone(options.key ?? original);
  const patch = options.patch ?? expectedPatch;
  api.replaceResponse("GET", "/api/admin/overview", {
    projects: [project], models: [model, { ...model, id: "mdl_replacement", name: "replacement-model" }],
    providers: [], provider_resources: [], alerts: [],
  });
  api.define("GET", "/api/admin/api-keys", () => ({ json: { data: [structuredClone(key)] } }));
  api.respond("GET", "/api/admin/users", { data: [user] });
  api.respond("GET", "/api/admin/resources/teams", { data: [] });
  api.respond("GET", "/api/admin/resources/project-members", { data: [] });
  api.define("PATCH", `/api/admin/api-keys/${key.id}`, input => {
    expect(input.body).toEqual(patch);
    if (options.fail) return { status: 503, json: { error: { message: "Synthetic key update unavailable" } } };
    Object.assign(key, structuredClone(patch));
    return { json: structuredClone(key) };
  });
  return key;
}

async function openEditor(page: Page) {
  await page.goto("/api-keys");
  const row = page.getByRole("row").filter({ hasText: original.name });
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  const modal = page.locator("form.modal");
  await expect(modal.getByRole("heading", { name: "Key 管理" })).toBeVisible();
  return { row, modal };
}

test("api-key-edit updates allowlist and quota in place and reloads saved values", async ({ page, api }, testInfo) => {
  setup(api);
  const { row, modal } = await openEditor(page);
  await expect(modal.getByRole("combobox", { name: /模型访问模式/ })).toHaveValue("restricted");
  const allowed = modal.getByRole("textbox", { name: /模型允许列表/ });
  await expect(allowed).toHaveValue("retired-model, ui-review-model");
  await allowed.fill("ui-review-model, replacement-model");
  await allowed.scrollIntoViewIfNeeded();
  await capture(page, testInfo, modal, "key-edit-model-access", "编辑已有 Key：调整模型允许列表，保留原 Key", "viewport");
  await modal.getByRole("spinbutton", { name: "日 Token", exact: true }).fill("2000");
  await modal.getByRole("button", { name: "保存", exact: true }).click();
  await expect(modal).toHaveCount(0);
  await expect(row).toContainText("ui-review-model, replacement-model");
  await expect(row).toContainText("sk_ui...3456");
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  await expect(allowed).toHaveValue("ui-review-model, replacement-model");
  await expect(modal.getByRole("spinbutton", { name: "日 Token", exact: true })).toHaveValue("2000");
  await modal.getByRole("button", { name: "取消", exact: true }).click();
  expect(api.calls.filter(call => call.method !== "GET")).toEqual([
    expect.objectContaining({ method: "PATCH", path: `/api/admin/api-keys/${original.id}`, body: expectedPatch }),
  ]);
});

test("api-key-edit clears the restricted allowlist without switching to inheritance", async ({ page, api }, testInfo) => {
  const patch = { ...expectedPatch, allowed_models: [], limits: { ...expectedPatch.limits, daily_tokens: 1000 } };
  setup(api, { patch });
  const { row, modal } = await openEditor(page);
  await modal.getByRole("textbox", { name: /模型允许列表/ }).fill("");
  await expect(modal).toContainText("restricted 且留空表示禁止此 Key 访问任何模型。");
  await modal.getByRole("button", { name: "保存", exact: true }).click();
  await expect(modal).toHaveCount(0);
  await expect(row).toContainText("禁止全部模型");
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  await expect(modal.getByRole("combobox", { name: /模型访问模式/ })).toHaveValue("restricted");
  const allowed = modal.getByRole("textbox", { name: /模型允许列表/ });
  await expect(allowed).toHaveValue("");
  await allowed.scrollIntoViewIfNeeded();
  await capture(page, testInfo, modal, "key-edit-deny-all", "restricted 空列表：禁止此 Key 访问全部模型", "viewport");
  expect(api.calls.filter(call => call.method !== "GET")).toHaveLength(1);
});

test("api-key-edit restores project inheritance explicitly", async ({ page, api }) => {
  const patch = { ...expectedPatch, model_access_mode: "inherit", allowed_models: [], limits: { ...expectedPatch.limits, daily_tokens: 1000 } };
  setup(api, { key: { ...original, allowed_models: [] }, patch });
  const { row, modal } = await openEditor(page);
  await modal.getByRole("combobox", { name: /模型访问模式/ }).selectOption("inherit");
  await expect(modal.getByRole("textbox", { name: /模型允许列表/ })).toHaveCount(0);
  await modal.getByRole("button", { name: "保存", exact: true }).click();
  await expect(modal).toHaveCount(0);
  await expect(row).toContainText("继承上级范围");
  await row.getByRole("button", { name: "编辑", exact: true }).click();
  await expect(modal.getByRole("combobox", { name: /模型访问模式/ })).toHaveValue("inherit");
  expect(api.calls.filter(call => call.method !== "GET")).toHaveLength(1);
});

test("api-key-edit failed save preserves the draft and existing key", async ({ page, api }, testInfo) => {
  const key = setup(api, { fail: true });
  const { row, modal } = await openEditor(page);
  await modal.getByRole("textbox", { name: /模型允许列表/ }).fill("ui-review-model, replacement-model");
  await modal.getByRole("spinbutton", { name: "日 Token", exact: true }).fill("2000");
  await modal.getByRole("button", { name: "保存", exact: true }).click();
  await expect(modal.getByRole("alert")).toContainText("Synthetic key update unavailable");
  await expect(modal.getByRole("alert")).toBeVisible();
  await expect(modal).toBeVisible();
  await expect(modal.getByRole("textbox", { name: /模型允许列表/ })).toHaveValue("ui-review-model, replacement-model");
  await expect(modal.getByRole("button", { name: "保存", exact: true })).toBeEnabled();
  expect(key).toEqual(original);
  await modal.getByRole("button", { name: "取消", exact: true }).click();
  await expect(row).toContainText("retired-model, ui-review-model");
  await capture(page, testInfo, page.locator(".app-shell"), "key-edit-save-failure", "保存失败：显示错误并保留原有模型权限", "viewport");
  expect(api.calls.filter(call => call.method !== "GET")).toHaveLength(1);
});
