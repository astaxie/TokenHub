import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type PluginDescriptor } from "../core/types";
import { emptyData } from "../domain/catalog";
import { pluginMarketplaceWebsiteURL } from "../domain/plugin-management";
import { setActiveLanguage } from "../i18n/runtime";
import { PluginsView } from "./plugins";

const api = { baseURL: "http://localhost:8080", adminToken: "admin-token" };

describe("PluginsView", () => {
  afterEach(() => {
    setActiveLanguage("zh-CN");
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  for (const locale of ["en", "ja", "zh-CN"] as const) {
    it(`keeps canonical template names in the ${locale} installed list`, () => {
      const data = emptyData();
      data.plugins = [plugin("tokenhub.sim.enterprise", "Enterprise SIM", "ui_template", ["sim"], ["presentation"], {
        localizations: {
          "en-US": { name: "Localized Enterprise SIM" },
          "zh-CN": { name: "企业 SIM" },
          "ja-JP": { name: "エンタープライズ SIM" },
        },
      })];
      setActiveLanguage(locale);
      render(<PluginsView api={api} data={data} />);
      expect(screen.getByText("Enterprise SIM")).toBeInTheDocument();
      for (const localized of ["Localized Enterprise SIM", "企业 SIM", "エンタープライズ SIM"]) {
        expect(screen.queryByText(localized)).not.toBeInTheDocument();
      }
    });
  }

  it("shows the four categories beside one unified installed list", () => {
    const data = emptyData();
    data.plugins = [
      plugin("tokenhub.provider.kimi", "Kimi Provider", "provider_integration", ["provider"], ["gateway_chain"]),
      plugin("tokenhub.pipeline.privacy", "Privacy Filter", "request_pipeline", ["extension"], ["gateway_chain"]),
      plugin("tokenhub.ui.enterprise", "Enterprise UI", "ui_template", ["sim"], ["presentation"]),
      plugin("tokenhub.automation.quota", "Quota Refresh", "automation", ["extension"], ["background"]),
    ];

    render(<PluginsView api={api} data={data} />);

    expect(screen.queryByText("扩展类型")).not.toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "全部插件" })).toHaveTextContent("4");
    expect(screen.getByRole("tab", { name: "Provider 集成" })).toHaveTextContent("1");
    expect(screen.getByRole("tab", { name: "请求链路" })).toHaveTextContent("1");
    expect(screen.getByRole("tab", { name: "UI 模板" })).toHaveTextContent("1");
    expect(screen.getByRole("tab", { name: "自动化" })).toHaveTextContent("1");
    expect(screen.queryByText("Provider 插件清单")).not.toBeInTheDocument();
    expect(screen.queryByText("链路注入插件清单")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Provider 集成" }));
    expect(screen.getByText("Kimi Provider")).toBeInTheDocument();
    expect(screen.queryByText("Privacy Filter")).not.toBeInTheDocument();
  });

  it("keeps row identity compact and moves technical IDs to details", () => {
    const data = emptyData();
    data.plugins = [plugin("tokenhub.provider.kimi", "Kimi Provider", "provider_integration", ["provider"], ["gateway_chain"], {
      version: "1.2.3",
      summary: "Connect TokenHub to Kimi.",
      distribution: {
        marketplace_url: "https://plugins.example/kimi",
        download_url: "https://plugins.example/kimi.zip",
        checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        license: "Apache-2.0",
      },
    })];

    render(<PluginsView api={api} data={data} />);

    const row = screen.getByText("Kimi Provider").closest(".plugin-installed-row");
    expect(row).not.toBeNull();
    expect(within(row as HTMLElement).getByText("Provider 集成")).toBeInTheDocument();
    expect(within(row as HTMLElement).getByText("内置")).toBeInTheDocument();
    expect(within(row as HTMLElement).getByText("1.2.3")).toBeInTheDocument();
    expect(within(row as HTMLElement).getByText("Connect TokenHub to Kimi.")).toBeInTheDocument();
    expect(row).not.toHaveTextContent("tokenhub.provider.kimi");
    expect(row).not.toHaveTextContent("Apache-2.0");
    expect(row).not.toHaveTextContent("0123456789abcdef");
  });

  it("shows settings only for plugins that declare settings", () => {
    const onSelectPlugin = vi.fn();
    const data = emptyData();
    data.plugins = [
      plugin("example.settings", "Settings Example", "ui_template", ["sim"], ["presentation"], { has_settings: true }),
      plugin("example.no-settings", "No Settings Example", "automation", ["extension"], ["background"]),
    ];

    render(<PluginsView api={api} data={data} onSelectPlugin={onSelectPlugin} />);

    expect(screen.getAllByRole("button", { name: "设置" })).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "设置" }));
    expect(onSelectPlugin).toHaveBeenCalledWith("example.settings", "settings");
  });

  it("filters by lifecycle state and search query with a padded empty state", () => {
    const data = emptyData();
    data.plugins = [
      plugin("example.enabled", "Enabled Example", "provider_integration", ["provider"], [], { status: "enabled" }),
      plugin("example.disabled", "Disabled Example", "automation", ["extension"], ["background"], { status: "disabled" }),
    ];

    render(<PluginsView api={api} data={data} />);

    fireEvent.click(screen.getByRole("button", { name: "已禁用" }));
    expect(screen.getByText("Disabled Example")).toBeInTheDocument();
    expect(screen.queryByText("Enabled Example")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "全部插件" }));
    fireEvent.change(screen.getByRole("searchbox", { name: "搜索插件" }), { target: { value: "missing" } });
    expect(screen.getByText("暂无插件").closest(".plugin-installed-empty")).toBeInTheDocument();
  });

  it("paginates the unified list and resets when a category changes", () => {
    const data = emptyData();
    data.plugins = Array.from({ length: 25 }, (_, index) => plugin(
      `example.provider-${String(index + 1).padStart(2, "0")}`,
      `Provider ${String(index + 1).padStart(2, "0")}`,
      "provider_integration",
      ["provider"],
      [],
    ));

    render(<PluginsView api={api} data={data} />);

    expect(screen.getByText("Provider 01")).toBeInTheDocument();
    expect(screen.queryByText("Provider 21")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTitle("下一页"));
    expect(screen.getByText("Provider 21")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("tab", { name: "Provider 集成" }));
    expect(screen.getByText("Provider 01")).toBeInTheDocument();
  });

  it("installs a verified plugin package from a URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { id: "tokenhub.example" }, restart_required: false },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={api} data={emptyData()} />);
    fireEvent.click(screen.getByRole("button", { name: "安装本地插件" }));
    fireEvent.change(screen.getByLabelText("下载 URL"), { target: { value: "https://plugins.example/plugin.zip" } });
    fireEvent.change(screen.getByLabelText("SHA-256 校验"), { target: { value: "a".repeat(64) } });
    fireEvent.click(screen.getByRole("button", { name: "安装插件" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/install");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      download_url: "https://plugins.example/plugin.zip",
      checksum_sha256: "a".repeat(64),
      replace: false,
      enable: false,
    });
    expect(await screen.findByText("tokenhub.example 安装完成")).toBeInTheDocument();
  });

  it("updates a marketplace plugin through the unified row action", async () => {
    const data = emptyData();
    data.plugins = [plugin("tokenhub.marketplace.kimi", "Marketplace Kimi", "provider_integration", ["provider"], [], {
      source: "marketplace",
      distribution: {
        download_url: "https://plugins.example/kimi.zip",
        checksum_sha256: "b".repeat(64),
      },
    })];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin: { version: "1.1.0" }, restart_required: false },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={api} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "更新" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.marketplace.kimi/update");
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual({
      download_url: "https://plugins.example/kimi.zip",
      checksum_sha256: "b".repeat(64),
    });
  });

  it("uninstalls a local plugin through the unified row action", async () => {
    const data = emptyData();
    data.plugins = [plugin("tokenhub.local.privacy", "Local Privacy", "request_pipeline", ["extension"], ["gateway_chain"], {
      source: "local_file",
    })];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { plugin_id: "tokenhub.local.privacy", restart_required: false },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);

    render(<PluginsView api={api} data={data} />);
    fireEvent.click(screen.getByRole("button", { name: "卸载" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugin-packages/tokenhub.local.privacy");
    expect(init.method).toBe("DELETE");
    expect(await screen.findByText("插件 tokenhub.local.privacy 已卸载")).toBeInTheDocument();
  });

  it("shows available catalog entries in Browse without mixing them into Installed", () => {
    const data = emptyData();
    data.pluginMarketplace = [{
      installed: false,
      update_available: false,
      plugin: plugin("tokenhub.provider.requesty", "Requesty", "provider_integration", ["provider"], [], {
        source: "marketplace",
        installed: false,
        available: true,
        distribution: { download_url: "https://plugins.example/requesty.zip", checksum_sha256: "c".repeat(64) },
      }),
    }];

    render(<PluginsView api={api} activeTab="install" data={data} />);

    expect(screen.getByRole("tab", { name: "浏览插件" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("Requesty")).toBeInTheDocument();
    expect(screen.getByText("未安装")).toBeInTheDocument();
  });

  it("uses only safe configured marketplace URLs", () => {
    const data = emptyData();
    data.resources.settings = [{
      id: "cfg_gateway",
      kind: "settings",
      name: "Gateway",
      status: "active",
      fields: { plugin_marketplace_url: "https://plugins.example/custom" },
    }];

    render(<PluginsView api={api} data={data} />);

    expect(screen.getByRole("link", { name: "插件市场" })).toHaveAttribute("href", "https://plugins.example/custom");
    data.resources.settings[0].fields = { plugin_marketplace_url: "javascript:alert(1)" };
    expect(pluginMarketplaceWebsiteURL(data)).toBe("https://plugins.thinkinai.xyz");
  });
});

function plugin(
  id: string,
  name: string,
  category: PluginDescriptor["category"],
  kinds: string[],
  placements: string[],
  overrides: Partial<PluginDescriptor> = {},
): PluginDescriptor {
  return {
    id,
    name,
    version: "1.0.0",
    source: "built_in",
    status: "enabled",
    category,
    kinds,
    placements,
    capabilities: [],
    ...overrides,
  };
}
