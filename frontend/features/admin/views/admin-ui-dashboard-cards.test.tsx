import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { AdminUIDashboardCards, dashboardMetricFields, dashboardMetricValue } from "./admin-ui-dashboard-cards";

describe("AdminUIDashboardCards", () => {
  it("renders dashboard cards declared by Admin UI plugins", () => {
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
    data.pluginChain.hooks = [{
      plugin_id: "tokenhub.extension.privacy",
      hook_id: "privacy.pre",
      stage: "privacy_pre",
      priority: 100,
      failure_policy: "fail_closed",
      timeout_millis: 1000,
      mandatory: true,
    }];
    data.pluginUI = [
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "overview",
        slot: "dashboard.card",
        title: "Plugin Ecosystem",
        schema: {
          fields: [
            { name: "plugins", type: "metric", label: "Registered plugins", source: "plugins.length", format: "number" },
            { name: "hooks", type: "metric", label: "Gateway hooks", source: "pluginChain.hooks.length", format: "number" },
            { name: "static", type: "metric", label: "Compatibility", value: "v1" },
          ],
        },
      },
      {
        plugin_id: "tokenhub.provider.openai-codex",
        id: "setup",
        slot: "provider.form.section",
        title: "Provider Setup",
      },
    ];

    render(<AdminUIDashboardCards data={data} />);

    expect(screen.getByText("Plugin Ecosystem")).toBeInTheDocument();
    expect(screen.getByText("Registered plugins")).toBeInTheDocument();
    expect(screen.getByText("Gateway hooks")).toBeInTheDocument();
    expect(screen.getByText("Compatibility")).toBeInTheDocument();
    expect(screen.getAllByText("1")).toHaveLength(2);
    expect(screen.getByText("v1")).toBeInTheDocument();
    expect(screen.queryByText("Provider Setup")).not.toBeInTheDocument();
  });

  it("parses only metric fields and formats dynamic values", () => {
    const data = emptyData();
    data.summary.estimated_cost_usd = 12.345678;
    const fields = dashboardMetricFields({
      plugin_id: "tokenhub.admin.billing",
      id: "cost",
      slot: "dashboard.card",
      schema: {
        fields: [
          { name: "cost", type: "metric", label: "Cost", source: "summary.estimated_cost_usd", format: "money_usd" },
          { name: "ignored", type: "table", label: "Ignored" },
        ],
      },
    });

    expect(fields).toHaveLength(1);
    expect(dashboardMetricValue(data, fields[0])).toBe("$12.35");
  });
});
