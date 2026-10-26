import type { Model, ModelRoute, Provider, ProviderModel } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { shellResponses, user } from "./fixtures/shell";
import type { MockAPI } from "./network";

const provider: Provider = { id: "provider_import_ui", name: "UI Import Provider", type: "mock", priority: 1, status: "active", healthy: true };
const model = (id: string, upstream: string): ProviderModel => ({
  id, provider_id: provider.id, upstream_model: upstream, status: "active", call_supported: true,
  category: "custom", family: "ui", modality: "chat", context_window: 32000,
  input_price_usd_per_1m: 3, output_price_usd_per_1m: 6, capabilities: ["chat", "tools"],
  supported_parameters: ["temperature"], metadata: { pricing_status: "configured" },
});
const first = model("inventory_import_ui_a", "ui/vendor-chat-a:fast");
const second = model("inventory_import_ui_b", "ui/vendor-chat-b");
const conflict = model("inventory_import_ui_existing", "ui-already-public");
const unsupported = { ...model("inventory_import_ui_unsupported", "ui-unsupported"), call_supported: false };
const inactive = { ...model("inventory_import_ui_inactive", "ui-inactive"), status: "disabled" };
const existing: Model = { id: "external_import_ui_existing", name: conflict.upstream_model, family: "ui", modality: "chat", status: "active", input_price_usd_per_1m: 99, metadata: { directory_role: "external" } };
const reference: Model = { id: "reference_import_ui", name: first.upstream_model, family: "ui", modality: "chat", status: "active", metadata: { source: "tokenhub-standard-catalog" } };

function install(api: MockAPI, inventory: ProviderModel[], models: Model[] = [reference, existing], selectedProvider = provider) {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  const storedModels = structuredClone(models);
  const storedInventory = structuredClone(inventory);
  const storedRoutes: ModelRoute[] = [];
  api.respond("GET", "/api/admin/routing-rules", { data: storedRoutes });
  api.respond("GET", "/api/admin/provider-catalog", { data: [] });
  function refresh() {
    api.replaceResponse("GET", "/api/admin/overview", { ...overview, models: storedModels, providers: [selectedProvider] });
    api.replaceResponse("GET", "/api/admin/provider-models", { data: storedInventory });
    api.replaceResponse("GET", "/api/admin/routing-rules", { data: storedRoutes });
  }
  refresh();
  function create(body: unknown) {
    const request = body as Model & { create_only: boolean; routes: ModelRoute[] };
    const source = storedInventory.find(item => item.upstream_model === request.name);
    expect(source, "Published names must equal imported upstream IDs").toBeDefined();
    expect(request).toMatchObject({ create_only: true, name: source!.upstream_model, modality: "chat", family: "ui", context_window: 32000, status: "active", capabilities: ["chat", "tools"] });
    expect(request.routes).toHaveLength(1);
    expect(request.routes[0]).toMatchObject({ provider_id: selectedProvider.id, provider_model: source!.upstream_model, status: "active", weight: 100, project_scope: "all" });
    const created: Model = { ...request, id: `external_${source!.id}`, metadata: { ...request.metadata, directory_role: "external" } };
    const index = storedModels.findIndex(item => item.name === request.name);
    if (index >= 0) storedModels[index] = created;
    else storedModels.push(created);
    storedRoutes.push({ ...request.routes[0], id: `route_${source!.id}`, model_name: request.name, priority: 1 });
    refresh();
    return { status: 201, json: created };
  }
  return { create, refresh, storedModels, storedInventory, storedRoutes };
}

for (const mobile of [false, true]) {
  const viewport = mobile ? "mobile" : "desktop";
  test(`models provider-import-one-to-one-${viewport}`, async ({ page, api }, info) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const fixture = install(api, [first, second, conflict, unsupported, inactive]);
    api.define("POST", "/api/admin/models", request => {
      expect(request.body).toMatchObject({ input_price_usd_per_1m: 1, output_price_usd_per_1m: 2 });
      return fixture.create(request.body);
    });
    await page.goto("/models");
    await page.getByRole("button", { name: "从 Provider 引入", exact: true }).click();
    const dialog = page.getByRole("dialog", { name: "从 Provider 引入", exact: true });
    await expect(dialog).toBeFocused();
    await dialog.getByRole("combobox", { name: "选择 Provider", exact: true }).selectOption(provider.id);
    await expect(dialog.getByRole("checkbox", { name: first.upstream_model, exact: true })).toBeEnabled();
    await expect(dialog.getByRole("checkbox", { name: conflict.upstream_model, exact: true })).toBeDisabled();
    await expect(dialog.getByRole("checkbox", { name: unsupported.upstream_model, exact: true })).toHaveCount(0);
    await expect(dialog.getByRole("checkbox", { name: inactive.upstream_model, exact: true })).toHaveCount(0);
    await dialog.getByRole("checkbox", { name: second.upstream_model, exact: true }).check();
    await dialog.getByLabel("搜索 Provider 模型", { exact: true }).fill("chat-a");
    await dialog.getByRole("checkbox", { name: "全选可引入模型", exact: true }).check();
    await dialog.getByLabel("搜索 Provider 模型", { exact: true }).fill("");
    await expect(dialog.getByRole("checkbox", { name: first.upstream_model, exact: true })).toBeChecked();
    await expect(dialog.getByRole("checkbox", { name: second.upstream_model, exact: true })).toBeChecked();
    await capture(page, info, dialog, `provider-import-selection-${viewport}`, "从现有 Provider 选择模型：同名冲突不可覆盖", "viewport");
    await dialog.getByLabel("统一输入价格", { exact: true }).fill("1");
    await dialog.getByLabel("统一输出价格", { exact: true }).fill("2");
    await dialog.getByRole("button", { name: "应用到所选模型", exact: true }).click();
    await dialog.getByRole("region", { name: second.upstream_model, exact: true }).getByLabel("输出价格（USD / 1M tokens）", { exact: true }).scrollIntoViewIfNeeded();
    await capture(page, info, dialog, `provider-import-pricing-${viewport}`, "批量设置对外价格并预览一一对应模型", "viewport");
    await dialog.getByRole("button", { name: "引入所选模型", exact: true }).click();
    await expect.poll(() => fixture.storedRoutes.length).toBe(2);
    expect(api.calls.filter(call => call.method === "POST").map(call => (call.body as Model).name).sort()).toEqual([first.upstream_model, second.upstream_model].sort());
    expect(fixture.storedModels.find(item => item.name === existing.name)).toEqual(existing);
    await expect(dialog).toBeVisible();
    await dialog.getByRole("status").scrollIntoViewIfNeeded();
    await capture(page, info, dialog, `provider-import-results-${viewport}`, "逐项显示模型引入结果", "viewport");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), "Import controls must not overflow the viewport").toBe(true);
    await dialog.getByRole("button", { name: "关闭", exact: true }).last().click();
    await expect(page.locator(".model-directory-table").getByText(first.upstream_model, { exact: true }).first()).toBeVisible();
  });
}

test("models provider-import-subscription-partial-retry", async ({ page, api }, info) => {
  const subscriptionProvider: Provider = { ...provider, type: "workbuddy_subscription" };
  const inventory = [first, second].map(item => ({ ...item, input_price_usd_per_1m: undefined, output_price_usd_per_1m: undefined, metadata: { pricing_status: "unverified", billing_mode: "subscription", fixture_marker: "preserve" } }));
  const fixture = install(api, inventory, [], subscriptionProvider);
  for (const item of inventory) {
    api.define("PATCH", `/api/admin/provider-models/${item.id}`, request => {
      expect(request.body).toMatchObject({ metadata: { pricing_status: "configured", billing_mode: "subscription", fixture_marker: "preserve" } });
      Object.assign(fixture.storedInventory.find(model => model.id === item.id)!, request.body);
      fixture.refresh();
      return { json: fixture.storedInventory.find(model => model.id === item.id) };
    });
  }
  let failSecond = true;
  let release: () => void = () => {};
  const pending = new Promise<void>(resolve => { release = resolve; });
  api.define("POST", "/api/admin/models", async request => {
    const payload = request.body as Model;
    expect(payload).toMatchObject({ input_price_usd_per_1m: 0, output_price_usd_per_1m: 0 });
    if (payload.name === first.upstream_model) await pending;
    if (payload.name === second.upstream_model && failSecond) {
      failSecond = false;
      return { status: 503, json: { error: { message: "Synthetic temporary publication failure" } } };
    }
    return fixture.create(request.body);
  });
  await page.goto("/models");
  await page.getByRole("button", { name: "从 Provider 引入", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "从 Provider 引入", exact: true });
  await dialog.getByRole("combobox", { name: "选择 Provider", exact: true }).selectOption(provider.id);
  await dialog.getByRole("checkbox", { name: "全选可引入模型", exact: true }).check();
  await dialog.getByLabel("统一输入价格", { exact: true }).fill("0");
  await dialog.getByLabel("统一输出价格", { exact: true }).fill("0");
  await dialog.getByRole("button", { name: "应用到所选模型", exact: true }).click();
  expect(api.calls.filter(call => call.method !== "GET")).toHaveLength(0);
  const confirm = dialog.getByRole("checkbox", { name: "确认所选订阅模型的按量成本为 0（订阅费用另行核算）", exact: true });
  await expect(confirm).not.toBeChecked();
  await expect(dialog.getByRole("button", { name: "引入所选模型", exact: true })).toBeDisabled();
  await confirm.check();
  await capture(page, info, dialog, "provider-import-subscription-confirm", "订阅按量成本与对外价格分别确认", "viewport");
  await dialog.getByRole("button", { name: "引入所选模型", exact: true }).click();
  await expect.poll(() => api.calls.filter(call => call.method === "POST").length).toBe(1);
  await expect(dialog.getByRole("combobox", { name: "选择 Provider", exact: true })).toBeDisabled();
  await expect(dialog.getByRole("button", { name: "关闭", exact: true }).last()).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(dialog).toBeVisible();
  release();
  await expect(dialog.getByText(/Synthetic temporary publication failure/)).toBeVisible();
  await dialog.getByText(/Synthetic temporary publication failure/).scrollIntoViewIfNeeded();
  await expect(dialog).toBeVisible();
  await capture(page, info, dialog, "provider-import-partial-failure", "部分引入成功：保留输入并允许只重试失败项", "viewport");
  await dialog.getByRole("button", { name: "引入所选模型", exact: true }).click();
  await expect.poll(() => fixture.storedRoutes.length).toBe(2);
  expect(api.calls.filter(call => call.method === "POST").map(call => (call.body as Model).name)).toEqual([first.upstream_model, second.upstream_model, second.upstream_model]);
  for (const item of inventory) expect(api.calls.filter(call => call.method === "PATCH" && call.path.endsWith(item.id))).toHaveLength(1);
});

test.describe("models provider-import-read-only", () => {
  test.use({ sessionUser: { ...user, role: "user" } });
  test("hides publication controls for a regular user", async ({ page, api }) => {
    install(api, [first]);
    await page.goto("/models");
    await expect(page.getByRole("heading", { name: "对外模型目录", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "从 Provider 引入", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "新建对外模型", exact: true })).toHaveCount(0);
    expect(api.calls.every(call => call.method === "GET")).toBe(true);
  });
});

for (const locale of [
  { language: "en" as const, title: "Import from Provider", all: "Select all importable models", input: "Shared input price", output: "Shared output price", apply: "Apply to selected models", submit: "Import selected models" },
  { language: "ja" as const, title: "Provider から取り込む", all: "取り込み可能なモデルをすべて選択", input: "共通入力価格", output: "共通出力価格", apply: "選択したモデルに適用", submit: "選択したモデルを取り込む" },
]) {
  test.describe(`models provider-import-localized-${locale.language}`, () => {
    test.use({ sessionLanguage: locale.language });
    test("keeps localized mobile pricing controls usable", async ({ page, api }, info) => {
      await page.setViewportSize({ width: 390, height: 844 });
      install(api, [first], []);
      await page.goto("/models");
      await page.getByRole("button", { name: locale.title, exact: true }).click();
      const dialog = page.getByRole("dialog", { name: locale.title, exact: true });
      await dialog.getByRole("checkbox", { name: locale.all, exact: true }).check();
      await dialog.getByLabel(locale.input, { exact: true }).fill("1");
      await dialog.getByLabel(locale.output, { exact: true }).fill("2");
      await dialog.getByRole("button", { name: locale.apply, exact: true }).click();
      await expect(dialog.getByRole("button", { name: locale.submit, exact: true })).toBeEnabled();
      await dialog.getByRole("region", { name: first.upstream_model, exact: true }).scrollIntoViewIfNeeded();
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      await capture(page, info, dialog, `provider-import-${locale.language}-mobile`, "多语言手机端模型映射与价格预览", "viewport");
    });
  });
}
