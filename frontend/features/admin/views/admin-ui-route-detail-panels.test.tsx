import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { type ModelRoute } from "../core/types";
import { emptyData } from "../domain/catalog";
import { AdminUIRouteDetailPanels, routePanelFieldValue, routePanelFields } from "./admin-ui-route-detail-panels";

const route: ModelRoute = {
  id: "route_001",
  model_name: "gpt-5",
  provider_id: "prv_openai",
  provider_model: "gpt-5-2026-08",
  status: "active",
  priority: 10,
  weight: 1,
};

describe("AdminUIRouteDetailPanels", () => {
  it("renders route detail panel contributions and runs registered actions", async () => {
    const user = userEvent.setup();
    const data = emptyData();
    data.pluginUI = [
      {
        plugin_id: "tokenhub.extension.route-inspector",
        id: "route-context",
        slot: "route.detail.panel",
        title: "Route Inspector",
        action: "route.inspect",
        schema: {
          fields: [
            { name: "model", type: "text", label: "External model", source: "route.model_name" },
            { name: "provider_model", type: "text", label: "Provider model", source: "route.provider_model" },
          ],
        },
      },
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "runtime",
        slot: "settings.panel",
        title: "Plugin Runtime",
      },
    ];
    data.pluginActions = [{
      plugin_id: "tokenhub.extension.route-inspector",
      action_id: "route.inspect",
      kind: "read",
      capability: "route.inspect",
      subject: "route",
    }];
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { status: "ok", credential: "secret-route-token" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(<AdminUIRouteDetailPanels api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }} data={data} route={route} />);

    expect(screen.getByText("Route Inspector")).toBeInTheDocument();
    expect(screen.getByText("gpt-5")).toBeInTheDocument();
    expect(screen.getByText("gpt-5-2026-08")).toBeInTheDocument();
    expect(screen.queryByText("Plugin Runtime")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /执行插件面板/ }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.extension.route-inspector/actions/route.inspect");
    expect(JSON.parse(String(init.body))).toEqual({ source: "route.detail.panel", contribution_id: "route-context", route_id: "route_001" });
    await waitFor(() => expect(screen.getByText(/\[redacted\]/)).toBeInTheDocument());
    expect(screen.queryByText("secret-route-token")).not.toBeInTheDocument();
  });

  it("formats route and app data fields", () => {
    const data = emptyData();
    data.routes = [route];
    const fields = routePanelFields({
      plugin_id: "tokenhub.extension.route-inspector",
      id: "route-context",
      slot: "route.detail.panel",
      schema: {
        fields: [
          { name: "model", type: "text", label: "External model", source: "route.model_name" },
          { name: "routes", type: "metric", label: "Routes", source: "data.routes.length", format: "number" },
          { name: "raw", type: "code_viewer", label: "Raw route", source: "route" },
        ],
      },
    });

    expect(fields).toHaveLength(3);
    expect(routePanelFieldValue(data, route, fields[0])).toBe("gpt-5");
    expect(routePanelFieldValue(data, route, fields[1])).toBe("1");
    expect(routePanelFieldValue(data, route, fields[2])).toContain("route_001");
  });
});
