import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  parseSIMCapability,
  simCapabilitiesFromPlugins,
  simRegistryFromPlugins,
} = await importTypeScript(new URL("./sim-registry.ts", import.meta.url));

test("SIM registry parses known SIM capability payloads", () => {
  const registry = simRegistryFromPlugins([
    {
      id: "tokenhub.sim.enterprise",
      name: "Enterprise SIM",
      version: "1.2.3",
      capabilities: [
        {
          kind: "sim",
          name: "theme_tokens",
          subject: "enterprise-dark",
          value: JSON.stringify({
            id: "enterprise-dark",
            title: "Enterprise Dark",
            mode: "dark",
            default: true,
            order: 30,
            priority: 5,
            tokens: {
              accent: " #2563eb ",
              bg: "#020617",
              unsafeNumber: 123,
            },
          }),
        },
        {
          kind: "sim",
          name: "shell_layout",
          subject: "ops-shell",
          value: JSON.stringify({
            id: "ops-shell",
            title: "Ops Shell",
            order: 20,
            layout: { density: "compact", navigation: "rail" },
          }),
        },
        {
          kind: "sim",
          name: "page_template",
          value: JSON.stringify({
            id: "provider-quota",
            title: "Provider Quota",
            target: "providers",
            template: { kind: "metrics" },
          }),
        },
        {
          kind: "sim",
          name: "dashboard_composition",
          value: JSON.stringify({
            id: "ops-overview",
            title: "Ops Overview",
            sections: [{ id: "traffic", cards: ["requests", "cache"] }],
          }),
        },
      ],
    },
  ]);

  assert.equal(registry.all.length, 4);
  assert.equal(registry.themeTokens[0].key, "tokenhub.sim.enterprise:theme_tokens:enterprise-dark");
  assert.equal(registry.themeTokens[0].pluginName, "Enterprise SIM");
  assert.equal(registry.themeTokens[0].pluginVersion, "1.2.3");
  assert.equal(registry.themeTokens[0].payload.mode, "dark");
  assert.equal(registry.themeTokens[0].payload.default, true);
  assert.deepEqual(registry.themeTokens[0].payload.tokens, {
    accent: "#2563eb",
    bg: "#020617",
  });
  assert.equal(registry.shellLayouts[0].payload.layout.density, "compact");
  assert.equal(registry.pageTemplates[0].payload.target, "providers");
  assert.deepEqual(registry.dashboardCompositions[0].payload.sections, [{ id: "traffic", cards: ["requests", "cache"] }]);
});

test("SIM registry ignores malformed or unsupported capability values safely", () => {
  const plugin = { id: "tokenhub.sim.bad", capabilities: [] };

  assert.equal(parseSIMCapability(plugin, { kind: "provider", name: "theme_tokens", value: "{}" }), null);
  assert.equal(parseSIMCapability(plugin, { kind: "sim", name: "unknown", value: "{}" }), null);
  assert.equal(parseSIMCapability(plugin, { kind: "sim", name: "theme_tokens", value: "{bad json" }), null);
  assert.equal(parseSIMCapability(plugin, { kind: "sim", name: "theme_tokens", value: "[]" }), null);
  assert.equal(parseSIMCapability(plugin, { kind: "sim", name: "theme_tokens", value: JSON.stringify({ tokens: {} }) }), null);

  const registry = simRegistryFromPlugins([
    {
      id: "tokenhub.sim.bad",
      capabilities: [
        { kind: "sim", name: "theme_tokens", value: JSON.stringify({ tokens: { accent: "#2563eb" } }) },
        { kind: "sim", name: "theme_tokens", value: "not-json" },
        { kind: "sim", name: "shell_layout", value: ["not", "json", "object"] },
        { kind: "admin_ui", name: "theme.tokens", value: JSON.stringify({ tokens: { accent: "#2563eb" } }) },
        null,
      ],
    },
    { id: "tokenhub.no-capabilities" },
  ]);

  assert.equal(registry.all.length, 1);
  assert.equal(registry.themeTokens.length, 1);
  assert.equal(registry.shellLayouts.length, 0);
});

test("SIM registry exposes deterministic ordering", () => {
  const plugins = [
    {
      id: "tokenhub.sim.beta",
      capabilities: [
        {
          kind: "sim",
          name: "page_template",
          value: JSON.stringify({ id: "late", order: 20 }),
        },
        {
          kind: "sim",
          name: "theme_tokens",
          value: JSON.stringify({ id: "same-order-low-priority", order: 10, priority: 1, tokens: { accent: "#111111" } }),
        },
      ],
    },
    {
      id: "tokenhub.sim.alpha",
      capabilities: [
        {
          kind: "sim",
          name: "shell_layout",
          value: JSON.stringify({ id: "first", order: 5 }),
        },
        {
          kind: "sim",
          name: "theme_tokens",
          value: JSON.stringify({ id: "same-order-high-priority", order: 10, priority: 10, tokens: { accent: "#222222" } }),
        },
        {
          kind: "sim",
          name: "dashboard_composition",
          value: JSON.stringify({ id: "default-order" }),
        },
      ],
    },
  ];

  assert.deepEqual(simCapabilitiesFromPlugins(plugins).map((capability) => capability.id), [
    "first",
    "same-order-high-priority",
    "same-order-low-priority",
    "late",
    "default-order",
  ]);
});
