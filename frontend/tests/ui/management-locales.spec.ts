import type { Page } from "@playwright/test";
import { languageStorageKey, type AdapterDescriptor, type ModelRoute, type PluginDescriptor, type Provider, type ProviderCatalogEntry, type ProviderModel } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import { model, shellResponses } from "./fixtures/shell";
import type { MockAPI } from "./network";

const direct: ProviderCatalogEntry = { id: "ui-locale-api", name: "UI Locale API", display_name: "UI Locale API", type: "openai_compatible", base_url: "https://locale.example.test/v1", models_count: 1, categories: ["openai"], source: "ui-fixture" };
const account: ProviderCatalogEntry = { id: "ui-locale-account", name: "UI Locale Subscription", display_name: "UI Locale Subscription", type: "ui_locale_account", base_url: "https://account.example.test", models_count: 0, source: "plugin" };
const provider: Provider = { id: "prv_ui_locale", name: "UI Locale Provider", type: direct.type, base_url: direct.base_url, priority: 1, status: "active", healthy: true, options: { catalog_id: direct.id } };
const inventory: ProviderModel = { id: "pm_ui_locale", provider_id: provider.id, upstream_model: "ui-locale-chat", status: "active", call_supported: true };
const route: ModelRoute = { id: "route_ui_locale", model_name: model.name, provider_id: provider.id, provider_model: inventory.upstream_model, priority: 1, weight: 100, strategy: "priority_weighted", status: "active" };
const plugin: PluginDescriptor = { id: "ui.locale-account", name: account.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: "ui_locale_account_resource", subject: account.type }] };
const adapters: AdapterDescriptor[] = [
  { type: direct.type, capabilities: ["chat"], provider_policy: { api_key_required: true, supports_custom_headers: true, default_catalog_provider_type: true } },
  { type: account.type, capabilities: ["chat"], provider_policy: { credentials_scope: "resource", supports_custom_headers: false } },
];

function installManagementFixtures(api: MockAPI) {
  const overview = shellResponses().get("GET /api/admin/overview") as Record<string, unknown>;
  api.replaceResponse("GET", "/api/admin/overview", { ...overview, providers: [provider], models: [model] });
  api.replaceResponse("GET", "/api/admin/provider-models", { data: [inventory] });
  api.respond("GET", "/api/admin/providers", { data: [provider] });
  api.respond("GET", "/api/admin/routing-rules", { data: [route] });
  api.respond("GET", "/api/admin/provider-catalog", { data: [direct, account] });
  api.respond("GET", `/api/admin/provider-catalog/${direct.id}`, { data: { ...direct, models: [{ id: inventory.upstream_model, name: "UI Locale Chat", category: "openai", type: "chat" }] } });
  api.respond("GET", "/api/admin/provider-adapters", { data: adapters });
  api.respond("GET", "/api/admin/plugins", { data: [plugin] });
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: [] } });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  for (const path of ["provider-resources", "audit/events", "providers/monitoring", "plugin-ui-manifest", "plugin-actions"]) api.respond("GET", `/api/admin/${path}`, { data: [] });
  api.define("GET", "/api/admin/audit/requests", () => ({ json: { data: [], pagination: { page: 1, page_size: 20, total: 0, total_pages: 0 }, summary: { all: 0, ok: 0, error: 0, average_latency_ms: 0 } } }), query => {
    expect(Object.fromEntries(query)).toEqual({ page: "1", page_size: "20", status: "all", q: "" });
  });
}

const locales = [
  { language: "en", option: "English", mobile: false, add: "Add provider", choose: "Choose a provider", apis: "Provider APIs", accounts: "Subscriptions and accounts", custom: "Custom provider", connected: "Connected", advanced: "Advanced connection settings", save: "Add provider and import models", close: "Close", imported: "Imported Models", published: "Published", edit: "Edit", price: "Unified External Price", configure: "Settings", routeMethod: "Routing method", routeSources: "Model sources", routeDialog: "Configure Model Routes", fixedRatio: "Fixed Ratio" },
  { language: "ja", option: "日本語", mobile: true, add: "プロバイダーを追加", choose: "プロバイダーを選択", apis: "プロバイダー API", accounts: "サブスクリプションとアカウント", custom: "カスタムプロバイダー", connected: "接続済み", advanced: "詳細な接続設定", save: "プロバイダーを追加してモデルを取り込む", close: "閉じる", imported: "取り込み済みモデル", published: "公開済み", edit: "編集", price: "統一外部価格", configure: "設定", routeMethod: "ルーティング方式", routeSources: "モデルの接続先", routeDialog: "モデルルートを設定", fixedRatio: "固定比率" },
] as const;

async function openLocalizedPage(page: Page, path: string, locale: typeof locales[number]) {
  await page.goto(path);
  await page.getByRole("button", { name: /^(界面语言|Interface Language|表示言語)$/ }).click();
  await page.getByRole("option", { name: locale.option, exact: true }).click();
  await expect(page.locator("html")).toHaveAttribute("lang", locale.language);
  expect(await page.evaluate(key => localStorage.getItem(key), languageStorageKey)).toBe(locale.language);
}

for (const locale of locales) {
  test(`management-locales ${locale.language}-${locale.mobile ? "mobile" : "desktop"}`, async ({ page, api }, info) => {
    if (locale.mobile) await page.setViewportSize({ width: 390, height: 844 });
    installManagementFixtures(api);
    await openLocalizedPage(page, "/providers", locale);
    await expect(page.locator(`.provider-management-table td[data-label="${locale.imported}"]`)).toBeVisible();
    await capture(page, info, page.locator(".provider-channel-list"), `management-providers-${locale.language}`, "多语言：供应商简洁列表", "viewport");
    await page.getByRole("button", { name: locale.add, exact: true }).first().click();
    const editor = page.locator("form.provider-modal");
    await expect(editor.getByRole("heading", { name: locale.choose, exact: true })).toBeVisible();
    await expect(editor.getByRole("heading", { name: locale.apis, exact: true })).toBeVisible();
    await expect(editor.getByRole("heading", { name: locale.accounts, exact: true })).toBeVisible();
    await expect(editor.getByRole("button", { name: new RegExp(locale.custom) })).toBeEnabled();
    const connected = editor.getByRole("button", { name: /UI Locale API/ });
    await expect(connected).toContainText(locale.connected);
    await expect(connected).toBeEnabled();
    await capture(page, info, editor, `management-picker-${locale.language}`, "多语言：供应商品牌入口", "viewport");
    await connected.click();
    await expect(editor.getByLabel("API Key", { exact: true })).toBeVisible();
    await expect(editor.getByRole("switch")).toHaveCount(1);
    await expect(editor.getByText(locale.advanced, { exact: true })).toBeVisible();
    await expect(editor.locator("details.provider-onboarding-advanced")).not.toHaveAttribute("open", "");
    await expect(editor.getByRole("button", { name: locale.save, exact: true })).toBeVisible();
    await capture(page, info, editor, `management-connection-${locale.language}`, "多语言：连接与模型选择", "viewport");
    await editor.getByTitle(locale.close, { exact: true }).click();

    await openLocalizedPage(page, "/models", locale);
    const models = page.locator(".model-directory-table");
    await expect(models.getByRole("columnheader")).toHaveCount(6);
    await expect(models.getByRole("columnheader", { name: locale.price, exact: true })).toHaveCount(1);
    expect(await models.getByRole("columnheader").evaluateAll(headers => headers.every(header => {
      const text = document.createRange();
      text.selectNodeContents(header);
      const cell = header.getBoundingClientRect();
      return Array.from(text.getClientRects()).every(line => line.left >= cell.left && line.right <= cell.right + 1);
    })), "Localized model headers must fit inside their own columns").toBe(true);
    await expect(models.getByText(locale.published, { exact: true })).toBeVisible();
    await expect(models.getByRole("button", { name: locale.edit, exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await capture(page, info, page.locator(".model-directory"), `management-models-${locale.language}`, "多语言：模型简洁列表", "viewport");

    await openLocalizedPage(page, "/routes", locale);
    const routes = page.locator(".route-summary-table");
    await expect(routes.getByRole("columnheader", { includeHidden: true })).toHaveCount(5);
    await expect(routes.locator(`td[data-label="${locale.routeMethod}"]`)).toBeVisible();
    await expect(routes.locator(`td[data-label="${locale.routeSources}"]`)).toBeVisible();
    await expect(routes.getByText(model.name, { exact: true })).toBeVisible();
    await expect(routes.getByText(locale.fixedRatio, { exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    if (locale.mobile) {
      await expect(routes.getByRole("button", { name: locale.configure, exact: true })).toBeInViewport({ ratio: 1 });
      expect(await page.locator(".route-summary-scroll").evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    }
    await capture(page, info, page.locator(".route-management"), `management-routes-${locale.language}`, "多语言：路由摘要列表", "viewport");
    await routes.getByRole("button", { name: locale.configure, exact: true }).click();
    const dialog = page.getByRole("dialog", { name: locale.routeDialog, exact: true });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("tab")).toHaveCount(7);
    await expect(dialog.getByRole("tab", { name: locale.fixedRatio, exact: true })).toHaveAttribute("aria-selected", "true");
    await capture(page, info, dialog, `management-route-editor-${locale.language}`, "多语言：路由编辑与全部策略", "viewport");
  });
}
