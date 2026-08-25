import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { AdminUISettingsPanels, settingsPanelFieldValue, settingsPanelFields } from "./admin-ui-settings-panels";

describe("AdminUISettingsPanels", () => {
  it("renders settings panel contributions and runs registered actions", async () => {
    const user = userEvent.setup();
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.admin.plugin-ecosystem",
      name: "TokenHub Plugin Ecosystem Dashboard",
      version: "built-in",
      source: "built_in",
      kinds: ["admin_ui"],
      placements: ["presentation"],
      capabilities: [],
    }];
    data.pluginUI = [
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "runtime",
        slot: "settings.panel",
        title: "Plugin Runtime",
        action: "runtime.inspect",
        schema: {
          fields: [
            { name: "schema", type: "text", label: "Admin UI schema", value: "v1" },
            { name: "plugins", type: "metric", label: "Registered plugins", source: "plugins.length", format: "number" },
          ],
        },
      },
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "overview",
        slot: "dashboard.card",
        title: "Plugin Ecosystem",
      },
    ];
    data.pluginActions = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      action_id: "runtime.inspect",
      kind: "read",
      capability: "runtime.inspect",
      subject: "plugin_ecosystem",
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { status: "ok", api_key: "secret-key" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(<AdminUISettingsPanels api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} />);

    expect(screen.getByText("Plugin Runtime")).toBeInTheDocument();
    expect(screen.getByText("Admin UI schema")).toBeInTheDocument();
    expect(screen.getByText("Registered plugins")).toBeInTheDocument();
    expect(screen.queryByText("Plugin Ecosystem")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /执行插件面板/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.admin.plugin-ecosystem/actions/runtime.inspect");
    expect(JSON.parse(String(init.body))).toEqual({ source: "settings.panel", contribution_id: "runtime" });
    await waitFor(() => expect(screen.getByText(/\[redacted\]/)).toBeInTheDocument());
    expect(screen.queryByText("secret-key")).not.toBeInTheDocument();
  });

  it("formats text, metric, and code viewer fields", () => {
    const data = emptyData();
    data.summary.estimated_cost_usd = 3.125;
    data.resources.settings = [{ id: "cfg_gateway", kind: "settings", name: "Gateway", status: "active", fields: { mode: "active" } }];
    const fields = settingsPanelFields({
      plugin_id: "tokenhub.admin.runtime",
      id: "settings",
      slot: "settings.panel",
      schema: {
        fields: [
          { name: "mode", type: "text", label: "Mode", source: "resources.settings.0.fields.mode" },
          { name: "cost", type: "metric", label: "Cost", source: "summary.estimated_cost_usd", format: "money_usd" },
          { name: "raw", type: "code_viewer", label: "Raw", source: "resources.settings.0" },
        ],
      },
    });

    expect(fields).toHaveLength(3);
    expect(settingsPanelFieldValue(data, fields[0])).toBe("active");
    expect(settingsPanelFieldValue(data, fields[1])).toBe("$3.13");
    expect(settingsPanelFieldValue(data, fields[2])).toContain("cfg_gateway");
  });
});
