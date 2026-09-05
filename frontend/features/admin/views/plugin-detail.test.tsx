import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginDetailView } from "./plugin-detail";

const api = { baseURL: "http://localhost:8080", adminToken: "admin-token" };

function detailPayload(withPackage = true, capabilities: Array<{ kind: string; name: string; value?: string }> = [{ kind: "gateway", name: "request.filter", value: "{\"internal\":\"raw\"}" }]) {
  return {
    data: {
      plugin: {
        id: "example.detail",
        name: "Detail Example",
        version: "1.0.0",
        source: "local_file",
        status: "enabled",
        kinds: ["extension"],
        placements: ["gateway_chain"],
        capabilities,
        permissions: [{ kind: "data", name: "request", access: "read", sensitivity: "internal" }],
        loadable: true,
        compatibility: { plugin_api: "v1", manifest_schema_version: 1, core_version: "0.7.0", verdict: "compatible" },
        trust: { source: "local_file", verdict: "unverified", checksum_present: true, signature_present: false },
      },
      package: withPackage ? {
        file_count: 3,
        total_size: 72,
        files: [
          { path: "plugin.yaml", size: 40, kind: "manifest", viewable: true },
          { path: "src/main.go", size: 20, kind: "source", viewable: true },
          { path: "credentials.json", size: 12, kind: "configuration", viewable: false },
        ],
      } : undefined,
    },
  };
}

function appData() {
  const data = emptyData();
  data.pluginChain.hooks = [{
    plugin_id: "example.detail",
    hook_id: "request-filter",
    stage: "before_provider",
    priority: 10,
    failure_policy: "fail_closed",
    timeout_millis: 500,
    mandatory: false,
  }];
  data.pluginUI = [{
    plugin_id: "example.detail",
    id: "navigation-search",
    slot: "navigation.search",
    title: "导航搜索",
    schema: { type: "object", properties: { placeholder: { type: "string" } } },
  }];
  data.pluginActions = [{ plugin_id: "example.detail", action_id: "inspect", kind: "read", title: "检查", capability: "detail.inspect" }];
  data.pluginBackgroundJobs = [{ plugin_id: "example.detail", job_id: "refresh", schedule: "10m", max_concurrency: 1 }];
  return data;
}

function renderDetail(section: "overview" | "files" | "settings") {
  return render(
    <PluginDetailView
      api={api}
      data={appData()}
      pluginID="example.detail"
      section={section}
      onBack={vi.fn()}
      onNavigate={vi.fn()}
    />,
  );
}

describe("PluginDetailView", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("leads with plain-language value and keeps implementation details collapsed", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload()), { status: 200 })));

    const onNavigate = vi.fn();
    render(
      <PluginDetailView api={api} data={appData()} pluginID="example.detail" section="overview" onBack={vi.fn()} onNavigate={onNavigate} />,
    );

    expect(await screen.findByRole("heading", { name: "Detail Example" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "主要功能" })).toBeInTheDocument();
    expect(screen.getByText("在模型请求经过网关时执行额外的处理逻辑。")).toBeInTheDocument();
    expect(screen.getByText("请求处理")).toBeInTheDocument();
    expect(screen.queryByText("未提供插件说明。")).not.toBeInTheDocument();
    expect(screen.queryByText("request-filter")).not.toBeVisible();
    expect(screen.queryByText("导航搜索")).not.toBeVisible();
    expect(screen.queryByText("refresh")).not.toBeVisible();
    expect(screen.queryByText("实现清单")).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("开发者信息"));
    expect(screen.getByText("3 个文件 · 72 B")).toBeVisible();
    expect(screen.getByText("request-filter")).toBeVisible();
    expect(screen.getByText("在请求发送给模型服务之前运行。")).toBeVisible();
    expect(screen.getByText("导航搜索")).toBeVisible();
    expect(screen.getByText("refresh")).toBeVisible();
    expect(screen.queryByText('{"internal":"raw"}')).not.toBeInTheDocument();
    expect(screen.queryByRole("tab", { name: "设置" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /导航搜索/ })).not.toBeInTheDocument();
    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("explains provider UI features instead of showing an empty description", async () => {
    const payload = detailPayload(false, []);
    payload.data.plugin.id = "tokenhub.admin.core-provider";
    payload.data.plugin.name = "TokenHub Core Provider Settings";
    payload.data.plugin.kinds = ["admin_ui"];
    payload.data.plugin.placements = ["presentation"];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 })));
    const data = appData();
    data.pluginUI = [
      { plugin_id: "tokenhub.admin.core-provider", id: "advanced", slot: "provider.form.section", title: "Advanced settings" },
      { plugin_id: "tokenhub.admin.core-provider", id: "resource", slot: "provider.resource.panel", title: "Resource settings" },
    ];

    render(
      <PluginDetailView api={api} data={data} pluginID="tokenhub.admin.core-provider" section="overview" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );

    expect(await screen.findByRole("heading", { name: "TokenHub Core Provider Settings" })).toBeInTheDocument();
    expect(screen.getByText("扩展 Provider 的连接设置和账号资源页面，让管理员直接管理插件提供的高级选项。")).toBeVisible();
    expect(screen.getByText("Provider 高级设置")).toBeVisible();
    expect(screen.getByText("账号资源设置")).toBeVisible();
    expect(screen.queryByText("未提供插件说明。")).not.toBeInTheDocument();
  });

  it("does not claim that catalog-only provider plugins process requests", async () => {
    const payload = detailPayload(false, [{ kind: "provider_catalog", name: "entry", value: "{}" }]);
    payload.data.plugin.id = "tokenhub.provider-catalog.requesty";
    payload.data.plugin.name = "Requesty";
    payload.data.plugin.kinds = ["provider"];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 })));

    render(
      <PluginDetailView api={api} data={appData()} pluginID={payload.data.plugin.id} section="overview" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );

    expect(await screen.findByRole("heading", { name: "Requesty" })).toBeInTheDocument();
    expect(screen.getByText("模型服务接入")).toBeVisible();
    expect(screen.queryByText("请求处理")).not.toBeInTheDocument();
  });

  it("keeps the plugin manager header available on secondary pages", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload()), { status: 200 })));
    const onBack = vi.fn();
    const onSelectManagerTab = vi.fn();

    const { container } = render(
      <PluginDetailView
        api={api}
        data={appData()}
        pluginID="example.detail"
        section="overview"
        onBack={onBack}
        onNavigate={vi.fn()}
        onSelectManagerTab={onSelectManagerTab}
      />,
    );

    expect(await screen.findByRole("heading", { name: "Detail Example" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "已安装插件" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByRole("link", { name: "插件市场" })).toHaveAttribute("href", "https://plugins.betokenhub.com");
    expect(container.querySelector(".plugin-detail-surface")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "安装插件" }));
    expect(onSelectManagerTab).toHaveBeenCalledWith("install");
    fireEvent.click(screen.getByRole("tab", { name: "已安装插件" }));
    expect(onBack).toHaveBeenCalledTimes(1);
  });

  it("uses marketplace description, publisher, and update state when available", async () => {
    const payload = detailPayload();
    payload.data.plugin.status = "disabled";
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 })));
    const data = appData();
    data.pluginMarketplace = [{
      installed: true,
      installed_version: "1.0.0",
      update_available: true,
      plugin: {
        ...payload.data.plugin,
        version: "1.1.0",
        source: "marketplace",
        status: "enabled",
        marketplace: {
          summary: "Removes sensitive fields before requests leave TokenHub.",
          publisher: { id: "example", name: "Example Labs", verified: true },
        },
      },
    }];

    render(<PluginDetailView api={api} data={data} pluginID="example.detail" section="overview" onBack={vi.fn()} onNavigate={vi.fn()} />);

    expect(await screen.findByText("Removes sensitive fields before requests leave TokenHub.")).toBeVisible();
    expect(screen.getByText("Example Labs")).toBeVisible();
    expect(screen.getByText("有新版本可用")).toBeVisible();
    expect(screen.getByText("已禁用")).toBeVisible();
  });

  it("lists package files and previews safe source files", async () => {
    const fetchMock = vi.fn().mockImplementation((input: string | URL | Request) => {
      const url = String(input);
      if (url.includes("/file?")) {
        return Promise.resolve(new Response(JSON.stringify({ data: { path: "src/main.go", size: 20, kind: "source", content: "package main\n" } }), { status: 200 }));
      }
      return Promise.resolve(new Response(JSON.stringify(detailPayload()), { status: 200 }));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderDetail("files");
    const source = await screen.findByRole("button", { name: /src\/main.go/ });
    fireEvent.click(source);

    expect(await screen.findByText("package main")).toBeInTheDocument();
    const blocked = screen.getByRole("button", { name: /credentials.json/ });
    expect(blocked).toBeDisabled();
    const previewed = fetchMock.mock.calls.map(([input]) => String(input)).filter((url) => url.includes("/file?"));
    expect(previewed.some((url) => url.includes(`path=${encodeURIComponent("credentials.json")}`))).toBe(false);
  });

  it("redirects old settings links when the plugin has no editable settings", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload()), { status: 200 })));
    const onNavigate = vi.fn();

    render(<PluginDetailView api={api} data={appData()} pluginID="example.detail" section="settings" onBack={vi.fn()} onNavigate={onNavigate} />);

    expect(await screen.findByRole("heading", { name: "Detail Example" })).toBeInTheDocument();
    expect(onNavigate).toHaveBeenCalledWith("example.detail", "overview");
    expect(screen.queryByRole("tab", { name: "设置" })).not.toBeInTheDocument();
    expect(screen.queryByText("权限声明")).not.toBeInTheDocument();
    expect(screen.queryByText(/placeholder/)).not.toBeInTheDocument();
  });

  it("shows settings only when the plugin declares editable theme values", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload()), { status: 200 })));
    const data = appData();
    data.plugins = [{
      ...detailPayload().data.plugin,
      capabilities: [{ kind: "sim", name: "theme_tokens", value: JSON.stringify({ id: "light", tokens: { accent: "#2563eb" } }) }],
    }];

    render(<PluginDetailView api={api} data={data} pluginID="example.detail" section="settings" onBack={vi.fn()} onNavigate={vi.fn()} />);

    expect(await screen.findByRole("tab", { name: "设置" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("主题色")).toBeInTheDocument();
  });

  it("keeps a direct settings link while registry data is still loading", async () => {
    const payload = detailPayload(true, [{ kind: "sim", name: "theme_tokens", value: JSON.stringify({ id: "light", tokens: { accent: "#2563eb" } }) }]);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(payload), { status: 200 })));
    const onNavigate = vi.fn();

    render(<PluginDetailView api={api} data={emptyData()} pluginID="example.detail" section="settings" onBack={vi.fn()} onNavigate={onNavigate} />);

    expect(await screen.findByRole("tab", { name: "设置" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("主题色")).toBeInTheDocument();
    expect(onNavigate).not.toHaveBeenCalled();
  });

  it("hides the files tab and redirects old file links when no package exists", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload(false)), { status: 200 })));
    const onNavigate = vi.fn();

    render(<PluginDetailView api={api} data={appData()} pluginID="example.detail" section="files" onBack={vi.fn()} onNavigate={onNavigate} />);

    expect(await screen.findByRole("heading", { name: "Detail Example" })).toBeInTheDocument();
    expect(onNavigate).toHaveBeenCalledWith("example.detail", "overview");
    expect(screen.queryByRole("tab", { name: "文件" })).not.toBeInTheDocument();
    expect(screen.queryByText("该内置插件没有独立安装包。")).not.toBeInTheDocument();
  });
});

describe("PluginDetailView file preview isolation", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function packageWithFiles(files: Array<{ path: string; size: number; kind: string; viewable: boolean }>) {
    return { file_count: files.length, total_size: files.reduce((total, file) => total + file.size, 0), files };
  }

  function stubDetailFetch(previewStatus: number) {
    const packages: Record<string, ReturnType<typeof packageWithFiles>> = {
      "example.detail": packageWithFiles([{ path: "src/main.go", size: 20, kind: "source", viewable: true }]),
      "example.locked": packageWithFiles([{ path: "credentials.json", size: 12, kind: "configuration", viewable: false }]),
    };
    return vi.fn().mockImplementation((input: string | URL | Request) => {
      const url = String(input);
      if (url.includes("/file?")) {
        return Promise.resolve(new Response(JSON.stringify({ data: { path: "src/main.go", size: 20, kind: "source", content: "package main\n" } }), { status: previewStatus }));
      }
      const pluginID = url.includes("example.locked") ? "example.locked" : "example.detail";
      const payload = detailPayload();
      payload.data.plugin.id = pluginID;
      payload.data.package = packages[pluginID];
      return Promise.resolve(new Response(JSON.stringify(payload), { status: 200 }));
    });
  }

  it("drops a failed preview when the next plugin has nothing to preview", async () => {
    vi.stubGlobal("fetch", stubDetailFetch(500));

    const { rerender } = render(
      <PluginDetailView api={api} data={appData()} pluginID="example.detail" section="files" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );
    expect(await screen.findByRole("alert")).toBeInTheDocument();

    rerender(
      <PluginDetailView api={api} data={appData()} pluginID="example.locked" section="files" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );

    expect(await screen.findByText("选择一个文件查看内容")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByText("正在读取插件文件")).not.toBeInTheDocument();
  });

  it("does not let a preview started for one plugin land on the next", async () => {
    let deliverPreview = (_content: string) => undefined as void;
    const preview = new Promise<Response>((resolve) => {
      deliverPreview = (content: string) => resolve(new Response(JSON.stringify({ data: { path: "src/main.go", size: 20, kind: "source", content } }), { status: 200 }));
    });
    const detailFetch = stubDetailFetch(200);
    vi.stubGlobal("fetch", vi.fn().mockImplementation((input: string | URL | Request, init?: RequestInit) => {
      return String(input).includes("/file?") ? preview : detailFetch(input, init);
    }));

    const { rerender } = render(
      <PluginDetailView api={api} data={appData()} pluginID="example.detail" section="files" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );
    expect(await screen.findByText("正在读取插件文件")).toBeInTheDocument();

    rerender(
      <PluginDetailView api={api} data={appData()} pluginID="example.locked" section="files" onBack={vi.fn()} onNavigate={vi.fn()} />,
    );
    expect(await screen.findByText("选择一个文件查看内容")).toBeInTheDocument();

    await act(async () => {
      deliverPreview("package main\n");
      await preview;
    });

    expect(screen.queryByText("package main")).not.toBeInTheDocument();
    expect(screen.getByText("选择一个文件查看内容")).toBeInTheDocument();
  });
});
