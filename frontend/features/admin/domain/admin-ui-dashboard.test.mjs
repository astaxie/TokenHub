import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  activeDashboardComposition,
  dashboardCardRegistry,
  dashboardCompositionsFromData,
} = await importTypeScript(new URL("./admin-ui-dashboard.ts", import.meta.url));

test("dashboard registry preserves legacy card order without a composition", () => {
  const pluginUI = [
    dashboardCard("tokenhub.admin.metrics", "requests", "Requests"),
    dashboardCard("tokenhub.admin.metrics", "cost", "Cost"),
  ];

  assert.deepEqual(dashboardCardRegistry({ pluginUI }).map((entry) => entry.contribution.id), ["requests", "cost"]);
});

test("dashboard registry applies SIM dashboard composition order and metadata", () => {
  const pluginUI = [
    dashboardCard("tokenhub.admin.metrics", "cost", "Cost"),
    dashboardCard("tokenhub.admin.metrics", "requests", "Requests"),
  ];
  const plugins = [{
    id: "tokenhub.sim.operations",
    name: "Operations SIM",
    version: "1.0.0",
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

  const entries = dashboardCardRegistry({ plugins, pluginUI });

  assert.deepEqual(entries.map((entry) => entry.contribution.id), ["requests", "cost"]);
  assert.equal(entries[0].composition.id, "operations");
  assert.equal(entries[0].composition.layout, "operations");
  assert.equal(entries[0].slot.region, "main");
  assert.equal(entries[0].slot.size, "wide");
});

test("dashboard registry ignores unsafe and unknown composition card ids", () => {
  const pluginUI = [
    dashboardCard("tokenhub.admin.metrics", "requests", "Requests"),
    dashboardCard("tokenhub.admin.metrics", "cost", "Cost"),
  ];
  const pluginUIWithComposition = [
    ...pluginUI,
    {
      plugin_id: "tokenhub.sim.enterprise",
      id: "ops-dashboard",
      slot: "dashboard.composition",
      title: "Ops Dashboard",
      schema: {
        composition: {
          id: "ops",
          cards: [
            { contribution_id: "https://example.test/card", order: 1 },
            { contribution_id: "missing", order: 2 },
            { contribution_id: "cost", order: 3 },
          ],
        },
      },
    },
  ];

  assert.deepEqual(dashboardCardRegistry({ pluginUI: pluginUIWithComposition }).map((entry) => entry.contribution.id), ["cost"]);
});

test("dashboard registry prefers default composition deterministically", () => {
  const pluginUI = [
    dashboardCard("tokenhub.admin.metrics", "requests", "Requests"),
    dashboardCard("tokenhub.admin.metrics", "cost", "Cost"),
  ];
  const compositions = [
    {
      plugin_id: "tokenhub.sim.first",
      id: "first",
      slot: "dashboard.composition",
      schema: { composition: { id: "first", order: 1, cards: [{ contribution_id: "requests" }] } },
    },
    {
      plugin_id: "tokenhub.sim.default",
      id: "default",
      slot: "dashboard.composition",
      schema: { composition: { id: "default", default: true, order: 100, cards: [{ contribution_id: "cost" }] } },
    },
  ];

  assert.equal(activeDashboardComposition({ pluginUI: [...pluginUI, ...compositions] }).id, "default");
  assert.deepEqual(dashboardCompositionsFromData({ pluginUI: [...pluginUI, ...compositions] }).map((composition) => composition.id), ["default", "first"]);
});

function dashboardCard(pluginID, id, title) {
  return {
    plugin_id: pluginID,
    id,
    slot: "dashboard.card",
    title,
    schema: {
      fields: [{ name: id, type: "metric", label: title, value: 1 }],
    },
  };
}
