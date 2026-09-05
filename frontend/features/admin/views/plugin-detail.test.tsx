import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { PluginDetailView } from "./plugin-detail";

const api = { baseURL: "http://localhost:8080", adminToken: "admin-token" };

function detailPayload(withPackage = true) {
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
        capabilities: [{ kind: "gateway", name: "request.filter" }],
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

  it("renders package and implementation summaries", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload()), { status: 200 })));

    const onNavigate = vi.fn();
    render(
      <PluginDetailView api={api} data={appData()} pluginID="example.detail" section="overview" onBack={vi.fn()} onNavigate={onNavigate} />,
    );

    expect(await screen.findByRole("heading", { name: "Detail Example" })).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("request-filter")).toBeInTheDocument();
    expect(screen.getByText("导航搜索")).toBeInTheDocument();
    expect(screen.getByText("refresh")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /导航搜索/ }));
    expect(onNavigate).toHaveBeenCalledWith("example.detail", "settings");
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

  it("shows declared permissions and UI configuration schemas", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload()), { status: 200 })));

    renderDetail("settings");

    expect(await screen.findByText("request")).toBeInTheDocument();
    expect(screen.getByText("data · read · internal")).toBeInTheDocument();
    expect(screen.getAllByText("navigation.search").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/placeholder/).length).toBeGreaterThan(0);
  });

  it("explains that built-in plugins have no standalone package", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify(detailPayload(false)), { status: 200 })));

    renderDetail("files");

    expect(await screen.findByText("该内置插件没有独立安装包。")).toBeInTheDocument();
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
