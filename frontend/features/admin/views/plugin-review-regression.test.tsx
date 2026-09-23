import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { setActiveLanguage } from "../i18n/runtime";
import { PluginDetailView } from "./plugin-detail";
import { PluginsView } from "./plugins";
import { dashboardMetricValue } from "./admin-ui-dashboard-cards";
import { reportTemplateFieldValue } from "./admin-ui-report-templates";
import { routePanelFieldValue } from "./admin-ui-route-detail-panels";
import { settingsPanelFieldValue } from "./admin-ui-settings-panels";
import { ProviderPluginPanels, providerPanelFieldValue } from "./provider-plugin-panels";
import { QuotaMetric } from "./provider-account-ui";

const api = { baseURL: "http://localhost:8080", adminToken: "test" };
const provider = { id: "provider", name: "Provider", type: "mock", status: "active", healthy: true, priority: 1 };
const route = { id: "route", model_name: "model", provider_id: "provider", provider_model: "upstream", status: "active", priority: 1, weight: 1 };

function marketplaceData() {
  const data = emptyData();
  data.pluginMarketplaceAvailable = true;
  data.pluginMarketplace = [{ installed: false, update_available: false, plugin: {
    id: "tokenhub.openai-codex", name: "Codex Marketplace", version: "1.0.0", source: "marketplace",
    kinds: [], placements: [], capabilities: [],
    marketplace: { summary: "Marketplace provider", categories: ["provider", "subscription"] },
  } }];
  return data;
}

afterEach(() => { setActiveLanguage("zh-CN"); vi.unstubAllGlobals(); });

describe("Plugin review regressions", () => {
  it("opens marketplace-only details without requesting an installed package", async () => {
    const data = marketplaceData();
    const fetch = vi.fn(); vi.stubGlobal("fetch", fetch);
    render(<PluginDetailView api={api} data={data} pluginID="tokenhub.openai-codex" section="overview" onBack={vi.fn()} onNavigate={vi.fn()} />);
    expect(await screen.findByRole("heading", { name: "Codex Marketplace" })).toBeVisible();
    expect(screen.getByText("Marketplace provider")).toBeVisible();
    expect(screen.getByText("未安装", { exact: true })).toBeVisible();
    expect(fetch).not.toHaveBeenCalled();
  });

  it("classifies channel-index provider categories without descriptor kinds", () => {
    const { container } = render(<PluginsView api={api} data={marketplaceData()} />);
    fireEvent.click(screen.getByRole("tab", { name: "浏览插件" }));
    const row = container.querySelector('[data-plugin-id="tokenhub.openai-codex"]') ?? screen.getByText("Codex Marketplace").closest("article");
    expect(row).toBeTruthy();
    expect(within(row as HTMLElement).getByText("Provider 集成")).toBeVisible();
    expect(within(row as HTMLElement).queryByRole("button", { name: "准备安装" })).not.toBeInTheDocument();
  });

  it("formats contributions using Chinese and Japanese compact notation", () => {
    const data = emptyData();
    for (const language of ["zh-CN", "ja"] as const) {
      setActiveLanguage(language);
      const field = { name: "count", type: "metric" as const, label: "Count", value: 12000, format: "compact" };
      const values = [dashboardMetricValue(data, field), reportTemplateFieldValue(data, field), settingsPanelFieldValue(data, field), routePanelFieldValue(data, route, field), providerPanelFieldValue({ provider, resources: [] }, field)];
      for (const value of values) expect(value).toBe("1.2万");
    }
  });

  it("keeps provider values verbatim when they match translation keys", () => {
    setActiveLanguage("en");
    render(<QuotaMetric label="模型" value="模型" />);
    expect(screen.getByText("Model")).toBeVisible();
    expect(screen.getByText("模型")).toBeVisible();
  });

  it("dispatches colon-containing action identities to their own endpoints", async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ data: { status: "ok" } }), { status: 200 })));
    vi.stubGlobal("fetch", fetch);
    const pairs = [["a:b", "c"], ["a", "b:c"]];
    render(<ProviderPluginPanels api={api} provider={provider} resources={[{ id: "resource", provider_id: "provider", name: "Resource", resource_type: "mock", status: "active", healthy: true, priority: 1, weight: 1 }]} contributions={pairs.map(([plugin_id, action], index) => ({ plugin_id, action, id: "panel", slot: "provider.resource.panel", title: `Panel ${index}`, provider_types: ["mock"] }))} actions={pairs.map(([plugin_id, action_id]) => ({ plugin_id, action_id, kind: "read", capability: "inspect", subject: "mock" }))} />);
    const buttons = screen.getAllByRole("button", { name: /执行插件面板/ });
    fireEvent.click(buttons[0]);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    fireEvent.click(buttons[1]);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    expect(fetch.mock.calls.map(([url]) => url)).toEqual(["http://localhost:8080/api/admin/plugins/a%3Ab/actions/c", "http://localhost:8080/api/admin/plugins/a/actions/b%3Ac"]);
  });
});
