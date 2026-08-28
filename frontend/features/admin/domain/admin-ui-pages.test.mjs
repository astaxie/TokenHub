import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  adminUIPageRegistry,
  adminUIPageTemplatesFromData,
} = await importTypeScript(new URL("./admin-ui-pages.ts", import.meta.url));

test("admin UI page registry preserves legacy nav section pages without templates", () => {
  const pages = adminUIPageRegistry({
    pluginUI: [
      {
        plugin_id: "tokenhub.admin.runtime",
        id: "runtime",
        slot: "nav.section",
        title: "Runtime",
        schema: { description: "Runtime status" },
      },
      {
        plugin_id: "tokenhub.admin.runtime",
        id: "settings",
        slot: "settings.panel",
        title: "Settings",
      },
    ],
  });

  assert.equal(pages.length, 1);
  assert.equal(pages[0].key, "tokenhub.admin.runtime:runtime");
  assert.equal(pages[0].title, "Runtime");
  assert.equal(pages[0].description, "Runtime status");
  assert.equal(pages[0].template, undefined);
});

test("admin UI page registry applies SIM page template metadata to nav pages", () => {
  const pages = adminUIPageRegistry({
    pluginUI: [
      {
        plugin_id: "tokenhub.admin.plugin-ecosystem",
        id: "ecosystem-page",
        slot: "nav.section",
        title: "Plugin Ecosystem",
      },
    ],
    plugins: [
      {
        id: "tokenhub.sim.enterprise",
        name: "Enterprise SIM",
        version: "1.0.0",
        capabilities: [
          {
            kind: "sim",
            name: "page_template",
            value: JSON.stringify({
              id: "ecosystem-template",
              target: "ecosystem-page",
              plugin_id: "tokenhub.admin.plugin-ecosystem",
              layout: "split",
              region: "operations",
              density: "compact",
              frame: "tool",
              default: true,
            }),
          },
        ],
      },
    ],
  });

  assert.equal(pages.length, 1);
  assert.deepEqual({
    id: pages[0].template?.id,
    source: pages[0].template?.source,
    targetPageID: pages[0].template?.targetPageID,
    layout: pages[0].template?.layout,
    region: pages[0].template?.region,
    density: pages[0].template?.density,
    frame: pages[0].template?.frame,
  }, {
    id: "ecosystem-template",
    source: "sim",
    targetPageID: "ecosystem-page",
    layout: "split",
    region: "operations",
    density: "compact",
    frame: "tool",
  });
});

test("admin UI page registry applies page template admin UI contributions", () => {
  const pages = adminUIPageRegistry({
    pluginUI: [
      {
        plugin_id: "tokenhub.admin.runtime",
        id: "runtime",
        slot: "nav.section",
        title: "Runtime",
      },
      {
        plugin_id: "tokenhub.admin.template-pack",
        id: "runtime-template",
        slot: "page.template",
        title: "Runtime Template",
        schema: {
          template: {
            contribution_id: "runtime",
            plugin_id: "tokenhub.admin.runtime",
            layout: "metrics",
            region: "status",
          },
        },
      },
    ],
  });

  assert.equal(pages[0].template?.source, "admin_ui");
  assert.equal(pages[0].template?.layout, "metrics");
  assert.equal(pages[0].template?.region, "status");
});

test("admin UI page registry ignores unknown or malformed page templates", () => {
  const pages = adminUIPageRegistry({
    pluginUI: [
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
        schema: {
          template: {
            contribution_id: "../runtime",
            plugin_id: "tokenhub.admin.runtime",
            layout: "script",
            region: "main/<script>",
          },
        },
      },
      {
        plugin_id: "tokenhub.admin.template-pack",
        id: "unknown-template",
        slot: "page.template",
        schema: {
          template: {
            contribution_id: "missing-page",
            plugin_id: "tokenhub.admin.runtime",
            layout: "grid",
          },
        },
      },
    ],
    plugins: [
      {
        id: "tokenhub.sim.bad",
        capabilities: [
          { kind: "sim", name: "page_template", value: JSON.stringify({ target: "runtime", slot: "external:url", layout: "split" }) },
          { kind: "sim", name: "page_template", value: JSON.stringify({ target: "runtime", layout: "https://example.com" }) },
        ],
      },
    ],
  });

  assert.equal(pages.length, 1);
  assert.equal(pages[0].template, undefined);

  const templates = adminUIPageTemplatesFromData({
    pluginUI: [
      {
        plugin_id: "tokenhub.admin.template-pack",
        id: "malformed-template",
        slot: "page.template",
        schema: { template: { contribution_id: "../runtime" } },
      },
    ],
  });
  assert.equal(templates.length, 0);
});
