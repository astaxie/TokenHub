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
});
