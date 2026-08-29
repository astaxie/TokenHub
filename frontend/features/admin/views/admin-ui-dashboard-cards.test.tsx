import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { type AdminUIContribution } from "../core/types";
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

  it("orders cards with SIM dashboard composition metadata", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.sim.operations",
      name: "Operations SIM",
      version: "built-in",
      source: "built_in",
      kinds: ["sim"],
      placements: ["presentation"],
      capabilities: [{
        kind: "sim",
        name: "dashboard_composition",
        subject: "operations",
        value: JSON.stringify({
          id: "operations",
          layout: "operations",
          cards: [
            { contribution_id: "requests", region: "main", size: "wide", order: 10 },
            { contribution_id: "cost", region: "side", size: "medium", order: 20 },
          ],
        }),
      }],
    }];
    data.pluginUI = [
      dashboardCard("cost", "Cost", 20),
      dashboardCard("requests", "Requests", 10),
    ];

    render(<AdminUIDashboardCards data={data} />);

    expect(screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent)).toEqual(["Requests", "Cost"]);
    expect(screen.getByRole("heading", { name: "Requests" }).closest("article")).toHaveAttribute("data-dashboard-region", "main");
    expect(screen.getByRole("heading", { name: "Requests" }).closest("article")).toHaveAttribute("data-dashboard-size", "wide");
    expect(screen.getByRole("heading", { name: "Cost" }).closest("article")).toHaveAttribute("data-dashboard-region", "side");
    expect(pluginStyles()).toContain(".overview-plugin-cards[data-dashboard-composition]");
    expect(pluginStyles()).toContain('.admin-ui-dashboard-card[data-dashboard-size="wide"]');
    expect(pluginStyles()).toContain('.admin-ui-dashboard-card[data-dashboard-region="side"]');
  });

  it("ignores unknown card ids in dashboard composition metadata", () => {
    const data = emptyData();
    data.pluginUI = [
      dashboardCard("visible", "Visible Card", 1),
      dashboardCard("hidden", "Hidden Card", 2),
      {
        plugin_id: "tokenhub.sim.enterprise",
        id: "ops-dashboard",
        slot: "dashboard.composition",
        schema: {
          composition: {
            id: "ops",
            cards: [
              { contribution_id: "missing", order: 10 },
              { contribution_id: "visible", order: 20 },
            ],
          },
        },
      },
    ];

    render(<AdminUIDashboardCards data={data} />);

    expect(screen.getByRole("heading", { name: "Visible Card" })).toBeInTheDocument();
    expect(screen.queryByText("Hidden Card")).not.toBeInTheDocument();
    expect(screen.queryByText("missing")).not.toBeInTheDocument();
  });

  it("falls back to legacy dashboard card order when no composition exists", () => {
    const data = emptyData();
    data.pluginUI = [
      dashboardCard("first", "First Card", 1),
      dashboardCard("second", "Second Card", 2),
    ];

    render(<AdminUIDashboardCards data={data} />);

    expect(screen.getAllByRole("heading", { level: 2 }).map((heading) => heading.textContent)).toEqual(["First Card", "Second Card"]);
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

function dashboardCard(id: string, title: string, value: number): AdminUIContribution {
  return {
    plugin_id: "tokenhub.admin.metrics",
    id,
    slot: "dashboard.card",
    title,
    schema: {
      fields: [{ name: id, type: "metric", label: title, value }],
    },
  };
}

function pluginStyles() {
  return readFileSync(resolve("app/styles/redesign/plugins.css"), "utf8");
}
