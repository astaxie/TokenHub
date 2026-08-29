import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { type AdminUIContribution } from "../core/types";
import { emptyData } from "../domain/catalog";
import { PluginPageView, pluginNavPages, pluginPageFieldValue, pluginPageFields } from "./admin-ui-plugin-pages";

describe("PluginPageView", () => {
  it("renders nav section contributions as plugin pages and runs actions", async () => {
    const user = userEvent.setup();
    const data = emptyData();
    data.pluginUI = [
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "ecosystem-page",
        slot: "nav.section",
        title: "Plugin Ecosystem",
        action: "ecosystem.inspect",
        schema: {
          description: "Inspect plugins.",
          fields: [
            { name: "plugins", type: "metric", label: "Registered plugins", source: "plugins.length", format: "number" },
            { name: "runtime", type: "text", label: "Runtime", value: "v1" },
          ],
        },
      },
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "settings",
        slot: "settings.panel",
        title: "Plugin Settings",
      },
    ];
    data.plugins = [{
      id: "tokenhub.admin.plugin-ecosystem",
      name: "TokenHub Plugin Ecosystem Dashboard",
      version: "built-in",
      source: "built_in",
      kinds: ["admin_ui"],
      placements: ["presentation"],
      capabilities: [],
    }];
    data.pluginActions = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      action_id: "ecosystem.inspect",
      kind: "read",
      capability: "ecosystem.inspect",
      subject: "plugin_ecosystem",
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { status: "ok", token: "secret-plugin-token" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <PluginPageView
        activePageKey="tokenhub.admin.plugin-ecosystem:ecosystem-page"
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        data={data}
        onSelectPage={vi.fn()}
      />,
    );

    expect(screen.getAllByText("Plugin Ecosystem")).toHaveLength(2);
    expect(screen.getByText("Registered plugins")).toBeInTheDocument();
    expect(screen.getByText("Runtime")).toBeInTheDocument();
    expect(screen.queryByText("Plugin Settings")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /执行插件面板/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.admin.plugin-ecosystem/actions/ecosystem.inspect");
    expect(JSON.parse(String(init.body))).toEqual({ source: "nav.section", contribution_id: "ecosystem-page" });
    await waitFor(() => expect(screen.getByText(/\[redacted\]/)).toBeInTheDocument());
    expect(screen.queryByText("secret-plugin-token")).not.toBeInTheDocument();
  });

  it("collects page action inputs through the shared runner", async () => {
    const user = userEvent.setup();
    const data = emptyData();
    data.pluginUI = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      id: "ecosystem-page",
      slot: "nav.section",
      title: "Plugin Ecosystem",
      action: "ecosystem.configure",
      schema: { description: "Configure the plugin ecosystem." },
    }];
    data.plugins = [{
      id: "tokenhub.admin.plugin-ecosystem",
      name: "TokenHub Plugin Ecosystem Dashboard",
      version: "built-in",
      source: "built_in",
      kinds: ["admin_ui"],
      placements: ["presentation"],
      capabilities: [],
    }];
    data.pluginActions = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      action_id: "ecosystem.configure",
      kind: "mutate",
      capability: "ecosystem.configure",
      subject: "plugin_ecosystem",
      input_schema: {
        type: "object",
        required: ["name"],
        properties: {
          name: { type: "string" },
          enabled: { type: "boolean" },
        },
      },
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { status: "ok", access_token: "secret-plugin-token" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <PluginPageView
        activePageKey="tokenhub.admin.plugin-ecosystem:ecosystem-page"
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        data={data}
        onSelectPage={vi.fn()}
      />,
    );

    await user.type(screen.getByRole("textbox", { name: /name/ }), "beta");
    await user.click(screen.getByRole("checkbox", { name: /enabled/ }));
    await user.click(screen.getByRole("button", { name: /执行插件面板/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.admin.plugin-ecosystem/actions/ecosystem.configure");
    expect(JSON.parse(String(init.body))).toEqual({
      source: "nav.section",
      contribution_id: "ecosystem-page",
      name: "beta",
      enabled: true,
    });
    await waitFor(() => expect(screen.getByText(/\[redacted\]/)).toBeInTheDocument());
    expect(screen.queryByText("secret-plugin-token")).not.toBeInTheDocument();
  });

  it("parses plugin nav pages and formats page fields", () => {
    const data = emptyData();
    data.summary.request_count = 1200;
    const contributions: AdminUIContribution[] = [{
      plugin_id: "tokenhub.admin.runtime",
      id: "runtime",
      slot: "nav.section",
      title: "Runtime",
      schema: {
        description: "Runtime status",
        fields: [
          { name: "requests", type: "metric", label: "Requests", source: "summary.request_count", format: "compact" },
          { name: "raw", type: "code_viewer", label: "Raw", source: "summary" },
        ],
      },
    }];
    const pages = pluginNavPages(contributions);
    const fields = pluginPageFields(contributions[0]);

    expect(pages[0]).toMatchObject({ key: "tokenhub.admin.runtime:runtime", title: "Runtime", description: "Runtime status" });
    expect(fields).toHaveLength(2);
    expect(pluginPageFieldValue(data, fields[0])).toBe("1.20K");
    expect(pluginPageFieldValue(data, fields[1])).toContain("request_count");
  });

  it("applies SIM page template metadata to selected plugin pages", () => {
    const data = emptyData();
    data.pluginUI = [
      {
        plugin_id: "tokenhub.admin.runtime",
        id: "runtime",
        slot: "nav.section",
        title: "Runtime",
        schema: {
          description: "Runtime status",
          fields: [{ name: "requests", type: "metric", label: "Requests", value: 42 }],
        },
      },
    ];
    data.plugins = [{
      id: "tokenhub.sim.enterprise",
      name: "Enterprise SIM",
      version: "1.0.0",
      source: "built_in",
      kinds: ["sim"],
      placements: ["presentation"],
      capabilities: [{
        kind: "sim",
        name: "page_template",
        subject: "runtime-page",
        value: JSON.stringify({
          id: "runtime-template",
          target: "runtime",
          plugin_id: "tokenhub.admin.runtime",
          layout: "metrics",
          region: "operations",
          density: "compact",
          frame: "tool",
        }),
      }],
    }];

    const { container } = render(
      <PluginPageView
        activePageKey="tokenhub.admin.runtime:runtime"
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        data={data}
        onSelectPage={vi.fn()}
      />,
    );

    const panel = container.querySelector(".plugin-page-panel");
    expect(panel).toHaveAttribute("data-page-template", "runtime-template");
    expect(panel).toHaveAttribute("data-page-template-source", "sim");
    expect(panel).toHaveAttribute("data-page-layout", "metrics");
    expect(panel).toHaveAttribute("data-page-region", "operations");
    expect(panel).toHaveAttribute("data-page-density", "compact");
    expect(panel).toHaveAttribute("data-page-frame", "tool");
    expect(pluginStyles()).toContain('.plugin-page-panel[data-page-template-source="sim"]');
    expect(pluginStyles()).toContain('.plugin-page-panel[data-page-layout="metrics"] .system-settings-plugin-grid');
    expect(screen.getByText("Requests")).toBeInTheDocument();
  });

  it("falls back when no page template exists", () => {
    const data = emptyData();
    data.pluginUI = [{
      plugin_id: "tokenhub.admin.runtime",
      id: "runtime",
      slot: "nav.section",
      title: "Runtime",
      schema: { fields: [{ name: "runtime", type: "text", label: "Runtime", value: "ready" }] },
    }];

    const { container } = render(
      <PluginPageView
        activePageKey="tokenhub.admin.runtime:runtime"
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        data={data}
        onSelectPage={vi.fn()}
      />,
    );

    const panel = container.querySelector(".plugin-page-panel");
    expect(panel).not.toHaveAttribute("data-page-template");
    expect(screen.getAllByText("Runtime").length).toBeGreaterThan(0);
    expect(screen.getByText("ready")).toBeInTheDocument();
  });

  it("ignores unknown and malformed page templates", () => {
    const data = emptyData();
    data.pluginUI = [
      {
        plugin_id: "tokenhub.admin.runtime",
        id: "runtime",
        slot: "nav.section",
        title: "Runtime",
      },
      {
        plugin_id: "tokenhub.admin.template-pack",
        id: "unsafe-template",
        slot: "page.template",
        title: "Unsafe Template",
        schema: {
          template: {
            contribution_id: "../runtime",
            plugin_id: "tokenhub.admin.runtime",
            layout: "grid",
          },
        },
      },
      {
        plugin_id: "tokenhub.admin.template-pack",
        id: "unknown-template",
        slot: "page.template",
        title: "Unknown Template",
        schema: {
          template: {
            contribution_id: "missing-page",
            plugin_id: "tokenhub.admin.runtime",
            layout: "grid",
          },
        },
      },
    ];
    data.plugins = [{
      id: "tokenhub.sim.bad",
      name: "Bad SIM",
      version: "1.0.0",
      source: "built_in",
      kinds: ["sim"],
      placements: ["presentation"],
      capabilities: [
        { kind: "sim", name: "page_template", value: JSON.stringify({ target: "runtime", slot: "core:external", layout: "split" }) },
        { kind: "sim", name: "page_template", value: JSON.stringify({ target: "runtime", layout: "https://example.com" }) },
      ],
    }];

    const { container } = render(
      <PluginPageView
        activePageKey="tokenhub.admin.runtime:runtime"
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        data={data}
        onSelectPage={vi.fn()}
      />,
    );

    const panel = container.querySelector(".plugin-page-panel");
    expect(panel).not.toHaveAttribute("data-page-template");
    expect(screen.queryByText("Unsafe Template")).not.toBeInTheDocument();
    expect(screen.queryByText("Unknown Template")).not.toBeInTheDocument();
  });
});

function pluginStyles() {
  return readFileSync(resolve("app/styles/redesign/plugins.css"), "utf8");
}
