import type { PluginDescriptor, PluginMarketplacePlugin } from "../../features/admin/core/types";
import { test, expect, capture } from "./harness";
import type { MockAPI } from "./network";

const marketplaceEntry: PluginMarketplacePlugin = {
  installed: false, update_available: false,
  plugin: {
    id: "tokenhub.openai-codex", name: "Codex Marketplace Provider", version: "1.0.0", source: "marketplace",
    kinds: [], placements: [], capabilities: [],
    marketplace: { summary: "Synthetic marketplace provider for UI acceptance.", categories: ["provider", "subscription"], publisher: { id: "ui-fixture", name: "UI Fixture Publisher", verified: true } },
  },
};

function plugins(api: MockAPI, entries: PluginMarketplacePlugin[], website = "", installed: () => PluginDescriptor[] = () => []) {
  api.define("GET", "/api/admin/plugins", () => ({ json: { data: structuredClone(installed()) } }));
  api.respond("GET", "/api/admin/plugin-marketplace", { data: { available: true, plugins: entries } });
  api.respond("GET", "/api/admin/provider-adapters", { data: [] });
  api.respond("GET", "/api/admin/plugin-chain", { data: { hooks: [] } });
  api.respond("GET", "/api/admin/plugin-ui-manifest", { data: [] });
  api.respond("GET", "/api/admin/plugin-actions", { data: [] });
  api.respond("GET", "/api/admin/plugin-background-jobs", { data: [], runs: [] });
  api.respond("GET", "/api/admin/resources/settings", { data: [{ id: "cfg_gateway", name: "Gateway", fields: { plugin_marketplace_url: website } }] });
}

for (const mobile of [false, true]) {
  test(`plugins marketplace-only-details${mobile ? "-mobile" : ""}`, async ({ page, api }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    plugins(api, [marketplaceEntry]);
    await page.goto("/plugins");
    await page.getByRole("tab", { name: "浏览插件", exact: true }).click();
    const row = page.locator(".plugin-browse-row").filter({ hasText: "Codex Marketplace Provider" });
    await expect(row.getByText("Provider 集成", { exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "浏览插件市场", exact: true })).toHaveCount(0);
    await capture(page, testInfo, row, `plugins-browse${mobile ? "-mobile" : ""}`, "市场插件分类和操作");
    await row.getByRole("button", { name: "详情", exact: true }).click();
    await expect(page).toHaveURL(/\/plugins\/tokenhub\.openai-codex$/);
    await expect(page.getByRole("heading", { name: "Codex Marketplace Provider", exact: true })).toBeVisible();
    await expect(page.getByText("Synthetic marketplace provider for UI acceptance.", { exact: true }).first()).toBeVisible();
    await expect(page.locator(".plugin-overview-facts").getByText("未安装", { exact: true })).toBeVisible();
    expect(api.calls.some(call => call.path.endsWith("/detail"))).toBe(false);
    await capture(page, testInfo, page.locator(".plugin-detail-view"), `plugins-marketplace-detail${mobile ? "-mobile" : ""}`, "未安装插件详情", "viewport");
  });
}

test("plugins empty-marketplace-with-configured-website", async ({ page, api }, testInfo) => {
  plugins(api, [], "https://marketplace.example.test");
  await page.goto("/plugins");
  await page.getByRole("tab", { name: "浏览插件", exact: true }).click();
  await expect(page.getByText("暂无可安装插件", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "浏览插件市场", exact: true })).toHaveAttribute("href", "https://marketplace.example.test/");
  await capture(page, testInfo, page.locator(".plugin-manager-topbar"), "plugins-empty-marketplace", "空市场和已配置网站", "viewport");
});

for (const action of ["install", "update", "uninstall", "rollback"] as const) {
  test(`plugins package-${action}-refresh`, async ({ page, api }, testInfo) => {
    const distribution = { download_url: "https://plugins.example.test/package.zip", checksum_sha256: "a".repeat(64) };
    const descriptor: PluginDescriptor = {
      id: "tokenhub.ui-package", name: "UI Lifecycle Package", version: "2.0.0", source: "local_file",
      status: "disabled", installed: true, kinds: ["extension"], placements: ["gateway_chain"], capabilities: [],
      rollback_available: true, rollback_version: "1.0.0",
      distribution: { license: "MIT" },
    };
    let installed = action === "install" ? [] : [descriptor];
    const entries: PluginMarketplacePlugin[] = [{
      plugin: { ...descriptor, source: "marketplace", version: "3.0.0", installed: false, distribution },
      installed: action !== "install", installed_version: descriptor.version, update_available: action === "update",
    }];
    plugins(api, entries, "", () => installed);
    const endpoint = action === "install" ? "/api/admin/plugins/install" : action === "uninstall" ? `/api/admin/plugin-packages/${descriptor.id}` : `/api/admin/plugins/${descriptor.id}/${action}`;
    api.define(action === "uninstall" ? "DELETE" : "POST", endpoint, input => {
      if (action === "install" || action === "update") expect(input.body).toMatchObject(distribution);
      else expect(input.body).toBeUndefined();
      const version = action === "rollback" ? "1.0.0" : "3.0.0";
      installed = action === "uninstall" ? [] : [{ ...descriptor, version, rollback_available: false }];
      entries[0].installed = installed.length > 0;
      entries[0].installed_version = version;
      entries[0].update_available = false;
      return { json: { data: { plugin: { ...descriptor, version }, plugin_id: descriptor.id, rollback_version: version, restart_required: false } } };
    });
    await page.goto("/plugins");
    if (action === "install") {
      await page.getByRole("button", { name: "安装本地插件", exact: true }).click();
      await page.getByLabel("下载 URL", { exact: true }).fill(distribution.download_url);
      await page.getByLabel("SHA-256 校验", { exact: true }).fill("a".repeat(64));
      await page.getByRole("button", { name: "安装插件", exact: true }).click();
    } else {
      const labels = { update: "更新", uninstall: "卸载", rollback: "回滚" };
      if (action === "update") await page.getByRole("button", { name: "可更新", exact: true }).click();
      await page.getByRole("button", { name: labels[action], exact: true }).click();
    }
    await expect.poll(() => api.calls.filter(call => call.method === "GET" && call.path === "/api/admin/plugins").length).toBe(2);
    if (action === "update") {
      await expect(page.getByRole("status")).toHaveText("插件已更新至 3.0.0");
      await expect(page.locator(".plugin-installed-row")).toHaveCount(0);
      await capture(page, testInfo, page.locator(".plugin-update-notice"), "package-update-filter-result", "更新筛选清空后仍保留操作结果");
      await page.getByRole("button", { name: "全部插件", exact: true }).click();
    }
    await page.getByRole("tab", { name: "已安装插件", exact: true }).click();
    const row = page.locator(".plugin-installed-row").filter({ hasText: descriptor.name });
    if (action === "uninstall") await expect(row).toHaveCount(0);
    else await expect(row).toContainText(action === "rollback" ? "1.0.0" : "3.0.0");
    await capture(page, testInfo, page.locator(".plugins-view"), `package-${action}-installed`, "插件操作后安装列表已刷新", "viewport");
    await page.getByRole("tab", { name: "浏览插件", exact: true }).click();
    const available = page.locator(".plugin-browse-row").filter({ hasText: descriptor.name });
    await expect(available).toHaveCount(action === "uninstall" ? 1 : 0);
  });
}

for (const mobile of [false, true]) {
  test(`plugins marketplace-update-error${mobile ? "-mobile" : ""}`, async ({ page, api }, testInfo) => {
    if (mobile) await page.setViewportSize({ width: 390, height: 844 });
    const descriptor: PluginDescriptor = {
      id: "review.update", name: "UI Update Package", version: "1.0.0", source: "local_file",
      status: "disabled", installed: true, kinds: ["sim"], placements: ["presentation"], capabilities: [],
      distribution: { license: "MIT" },
    };
    const distribution = { download_url: "https://plugins.example.test/update-2.zip", checksum_sha256: "b".repeat(64) };
    plugins(api, [{ plugin: { ...descriptor, version: "2.0.0", source: "marketplace", distribution }, installed: true, installed_version: "1.0.0", update_available: true }], "", () => [descriptor]);
    api.define("POST", "/api/admin/plugins/review.update/update", input => {
      expect(input.body).toEqual(distribution);
      return { status: 400, json: { error: { message: "Synthetic package signature verification failed", code: "plugin_signature_invalid" } } };
    });
    await page.goto("/plugins");
    const row = page.locator(".plugin-installed-row").filter({ hasText: descriptor.name });
    await expect(row.getByText("1.0.0", { exact: true })).toBeVisible();
    await row.getByRole("button", { name: "更新", exact: true }).click();
    await expect(row.getByRole("alert")).toContainText("Synthetic package signature verification failed");
    await expect(row.getByRole("button", { name: "更新", exact: true })).toBeEnabled();
    await capture(page, testInfo, row, `plugins-update-error${mobile ? "-mobile" : ""}`, "插件更新失败信息和重试操作");
  });
}

test("plugins permission-preview-invalidated-by-candidate", async ({ page, api }, testInfo) => {
  plugins(api, []);
  api.define("POST", "/api/admin/plugins/permission-diff", input => {
    expect(input.body).toEqual({ download_url: "https://plugins.example.test/a.zip", checksum_sha256: "a".repeat(64) });
    return { json: { data: {
      plugin_id: "review.preview-a", candidate_version: "1.0.0", operation: "install",
      permission_diff: { available: true, verdict: "allow", highest_sensitivity: "public", summary: { added: 0, removed: 0, unchanged: 0, changed_sensitivity: 0 } },
      trust: { verdict: "trusted" }, compatibility: { verdict: "compatible" },
    } } };
  });
  await page.goto("/plugins");
  await page.getByRole("tab", { name: "浏览插件", exact: true }).click();
  await page.getByLabel("下载 URL", { exact: true }).fill("https://plugins.example.test/a.zip");
  await page.getByLabel("SHA-256 校验", { exact: true }).fill("a".repeat(64));
  await page.getByRole("button", { name: "预览权限", exact: true }).click();
  await expect(page.getByText("候选版本：1.0.0", { exact: true })).toBeVisible();
  await page.getByLabel("下载 URL", { exact: true }).fill("https://plugins.example.test/b.zip");
  await page.getByLabel("SHA-256 校验", { exact: true }).fill("b".repeat(64));
  await expect(page.locator("[data-plugin-permission-diff-result]")).toHaveCount(0);
  await capture(page, testInfo, page.locator(".plugin-install-preview-panel"), "plugins-preview-invalidated", "变更插件包后旧权限校验结果已清除");
});

test("plugins marketplace-prepare-replaces-upload", async ({ page, api }, testInfo) => {
  const distribution = { download_url: "https://plugins.example.test/selected.zip", checksum_sha256: "c".repeat(64) };
  const descriptor = { ...marketplaceEntry.plugin, id: "review.selected", name: "UI Selected Package", distribution };
  let installed: PluginDescriptor[] = [];
  const entries = [{ plugin: descriptor, installed: false, update_available: false }];
  plugins(api, entries, "", () => installed);
  api.define("POST", "/api/admin/plugins/install", input => {
    expect(input.body).toEqual({ ...distribution, replace: false, enable: false });
    installed = [{ ...descriptor, source: "local_file", installed: true, status: "disabled", distribution: { license: "MIT" } }];
    entries[0].installed = true;
    return { json: { data: { plugin: { id: descriptor.id }, restart_required: false } } };
  });
  await page.goto("/plugins");
  await page.getByRole("tab", { name: "浏览插件", exact: true }).click();
  await page.getByRole("tab", { name: "上传 ZIP", exact: true }).click();
  await page.getByLabel("插件 ZIP 包", { exact: true }).setInputFiles({ name: "previous.zip", mimeType: "application/zip", buffer: Buffer.from("Synthetic upload fixture") });
  await expect(page.getByText("previous.zip", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "准备安装", exact: true }).click();
  await expect(page.getByRole("tab", { name: "URL 安装", exact: true })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByLabel("下载 URL", { exact: true })).toHaveValue(distribution.download_url);
  await capture(page, testInfo, page.locator(".plugin-install-source-panel"), "plugins-prepare-url", "市场插件准备操作已切换至对应下载地址");
  await page.getByRole("button", { name: "安装插件", exact: true }).click();
  await expect(page.getByText("review.selected 安装完成", { exact: true })).toBeVisible();
});
