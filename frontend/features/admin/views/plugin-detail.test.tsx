import { fireEvent, render, screen, waitFor } from "@testing-library/react";
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
    const previewRequests = fetchMock.mock.calls.filter(([input]) => String(input).includes("/file?")).length;
    fireEvent.click(blocked);
    await waitFor(() => expect(fetchMock.mock.calls.filter(([input]) => String(input).includes("/file?")).length).toBe(previewRequests));
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
