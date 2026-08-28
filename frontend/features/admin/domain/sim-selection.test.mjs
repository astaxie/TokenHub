import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  resolveSIMSelection,
  simSelectionPreference,
} = await importTypeScript(new URL("./sim-selection.ts", import.meta.url));

test("SIM selection resolves saved preference keys before defaults", () => {
  const selection = resolveSIMSelection({
    themeMode: "dark",
    preference: {
      activeSIMPluginID: "tokenhub.sim.enterprise",
      theme: { key: "tokenhub.sim.enterprise:theme_tokens:night" },
      layout: { activeKey: "tokenhub.sim.enterprise:shell_layout:ops" },
    },
    plugins: [
      simPlugin("tokenhub.sim.default", [
        themeCapability("base", { mode: "dark", default: true, order: 1, priority: 50 }),
        layoutCapability("base-shell", { default: true, order: 1, priority: 50 }),
      ]),
      simPlugin("tokenhub.sim.enterprise", [
        themeCapability("night", { mode: "dark", order: 20 }),
        layoutCapability("ops", { order: 20 }),
      ]),
    ],
  });

  assert.equal(selection.activeSIMPluginID, "tokenhub.sim.enterprise");
  assert.equal(selection.theme.capability?.key, "tokenhub.sim.enterprise:theme_tokens:night");
  assert.equal(selection.theme.source, "preference");
  assert.equal(selection.theme.matchedBy, "key");
  assert.equal(selection.theme.fallback, false);
  assert.equal(selection.layout.capability?.key, "tokenhub.sim.enterprise:shell_layout:ops");
  assert.equal(selection.layout.source, "preference");
  assert.deepEqual(selection.warnings, []);
});

test("SIM selection accepts legacy snake case preference payloads and scopes ID matches to the active SIM", () => {
  const selection = resolveSIMSelection({
    themeMode: "light",
    preference: JSON.stringify({
      sim_plugin_id: "tokenhub.sim.enterprise",
      theme_id: "light",
      layout_id: "shell",
    }),
    plugins: [
      simPlugin("tokenhub.sim.default", [
        themeCapability("light", { order: 1, priority: 10 }),
        layoutCapability("shell", { order: 1, priority: 10 }),
      ]),
      simPlugin("tokenhub.sim.enterprise", [
        themeCapability("light", { order: 20 }),
        layoutCapability("shell", { order: 20 }),
      ]),
    ],
  });

  assert.equal(selection.preference.simPluginID, "tokenhub.sim.enterprise");
  assert.equal(selection.theme.capability?.pluginID, "tokenhub.sim.enterprise");
  assert.equal(selection.theme.matchedBy, "id");
  assert.equal(selection.layout.capability?.pluginID, "tokenhub.sim.enterprise");
  assert.equal(selection.layout.matchedBy, "id");
  assert.equal(selection.warnings.some((warning) => warning.code === "ambiguous_theme_id"), false);
  assert.equal(selection.warnings.some((warning) => warning.code === "ambiguous_layout_id"), false);
});

test("SIM selection falls back from missing preferences to active SIM defaults before global defaults", () => {
  const selection = resolveSIMSelection({
    themeMode: "dark",
    preference: {
      active_sim_plugin_id: "tokenhub.sim.enterprise",
      active_theme_key: "tokenhub.sim.enterprise:theme_tokens:missing",
      active_layout_key: "tokenhub.sim.enterprise:shell_layout:missing",
    },
    plugins: [
      simPlugin("tokenhub.sim.default", [
        themeCapability("default-dark", { mode: "dark", default: true, order: 1, priority: 100 }),
        layoutCapability("default-shell", { default: true, order: 1, priority: 100 }),
      ]),
      simPlugin("tokenhub.sim.enterprise", [
        themeCapability("enterprise-dark", { mode: "dark", default: true, order: 30 }),
        layoutCapability("enterprise-shell", { default: true, order: 30 }),
      ]),
    ],
  });

  assert.equal(selection.activeSIMPluginID, "tokenhub.sim.enterprise");
  assert.equal(selection.theme.capability?.id, "enterprise-dark");
  assert.equal(selection.theme.source, "plugin_preference");
  assert.equal(selection.theme.fallback, true);
  assert.equal(selection.layout.capability?.id, "enterprise-shell");
  assert.equal(selection.layout.source, "plugin_preference");
  assert.equal(selection.warnings.some((warning) => warning.code === "missing_preferred_theme"), true);
  assert.equal(selection.warnings.some((warning) => warning.code === "missing_preferred_layout"), true);
});

test("SIM selection uses deterministic default and ordered fallbacks without saved preferences", () => {
  const withDefaults = resolveSIMSelection({
    themeMode: "dark",
    plugins: [
      simPlugin("tokenhub.sim.low", [
        themeCapability("late-default", { mode: "dark", default: true, order: 30, priority: 100 }),
        layoutCapability("late-layout", { default: true, order: 30, priority: 100 }),
      ]),
      simPlugin("tokenhub.sim.high", [
        themeCapability("early-default", { mode: "dark", default: true, order: 10, priority: 1 }),
        layoutCapability("early-layout", { default: true, order: 10, priority: 1 }),
      ]),
    ],
  });
  const withoutDefaults = resolveSIMSelection({
    themeMode: "dark",
    plugins: [
      simPlugin("tokenhub.sim.beta", [
        themeCapability("beta", { mode: "dark", order: 20 }),
        layoutCapability("beta-layout", { order: 20 }),
      ]),
      simPlugin("tokenhub.sim.alpha", [
        themeCapability("alpha", { mode: "dark", order: 10 }),
        layoutCapability("alpha-layout", { order: 10 }),
      ]),
    ],
  });

  assert.equal(withDefaults.theme.capability?.id, "early-default");
  assert.equal(withDefaults.theme.source, "default");
  assert.equal(withDefaults.layout.capability?.id, "early-layout");
  assert.equal(withoutDefaults.theme.capability?.id, "alpha");
  assert.equal(withoutDefaults.theme.source, "first_available");
  assert.equal(withoutDefaults.layout.capability?.id, "alpha-layout");
});

test("SIM selection reports ambiguous ID preferences and duplicate capability keys", () => {
  const selection = resolveSIMSelection({
    themeMode: "dark",
    preference: {
      themeID: "shared",
      layoutID: "shared-shell",
    },
    plugins: [
      simPlugin("tokenhub.sim.beta", [
        themeCapability("shared", { mode: "dark", order: 10, priority: 1 }),
        layoutCapability("shared-shell", { order: 10, priority: 1 }),
      ]),
      simPlugin("tokenhub.sim.alpha", [
        themeCapability("shared", { mode: "dark", order: 10, priority: 10 }),
        layoutCapability("shared-shell", { order: 10, priority: 10 }),
        themeCapability("collision", { mode: "dark", order: 30, priority: 1 }),
        themeCapability("collision", { mode: "dark", order: 30, priority: 50 }),
      ]),
    ],
  });

  assert.equal(selection.theme.capability?.key, "tokenhub.sim.alpha:theme_tokens:shared");
  assert.equal(selection.theme.matchedBy, "id");
  assert.equal(selection.layout.capability?.key, "tokenhub.sim.alpha:shell_layout:shared-shell");
  assert.equal(selection.warnings.some((warning) => warning.code === "ambiguous_theme_id" && warning.selected === "tokenhub.sim.alpha:theme_tokens:shared"), true);
  assert.equal(selection.warnings.some((warning) => warning.code === "ambiguous_layout_id" && warning.selected === "tokenhub.sim.alpha:shell_layout:shared-shell"), true);
  assert.equal(selection.warnings.some((warning) => warning.code === "duplicate_capability_key" && warning.key === "tokenhub.sim.alpha:theme_tokens:collision"), true);
});

test("SIM selection tolerates older backend payloads and malformed capability values", () => {
  const warnings = [];
  assert.deepEqual(simSelectionPreference("{bad json", warnings), {
    simPluginID: "",
    themeKey: "",
    themeID: "",
    layoutKey: "",
    layoutID: "",
  });
  assert.equal(warnings[0].code, "malformed_preference");

  const selection = resolveSIMSelection({
    preference: 42,
    plugins: [
      {
        id: "tokenhub.legacy.no-kinds",
        name: "Legacy SIM",
        capabilities: [
          { kind: "sim", name: "theme_tokens", value: "{bad json" },
          { kind: "sim", name: "theme_tokens", value: JSON.stringify({ tokens: {} }) },
          { kind: "sim", name: "shell_layout", value: JSON.stringify({ id: "legacy-shell", default: true }) },
          { kind: "provider", name: "theme_tokens", value: JSON.stringify({ tokens: { accent: "#2563eb" } }) },
        ],
      },
      {
        id: "tokenhub.old-empty",
        name: "Old Empty Plugin",
        capabilities: "not-an-array",
      },
    ],
  });

  assert.equal(selection.activeSIMPluginID, "tokenhub.legacy.no-kinds");
  assert.equal(selection.theme.source, "none");
  assert.equal(selection.theme.capability, undefined);
  assert.equal(selection.layout.capability?.id, "legacy-shell");
  assert.equal(selection.layout.source, "default");
  assert.equal(selection.warnings.some((warning) => warning.code === "malformed_preference"), true);
});

function simPlugin(id, capabilities) {
  return {
    id,
    name: id,
    version: "1.0.0",
    kinds: ["sim"],
    capabilities,
  };
}

function themeCapability(id, payload = {}) {
  return {
    kind: "sim",
    name: "theme_tokens",
    subject: id,
    value: JSON.stringify({
      id,
      mode: "all",
      tokens: { accent: `#${idColor(id)}` },
      ...payload,
    }),
  };
}

function layoutCapability(id, payload = {}) {
  return {
    kind: "sim",
    name: "shell_layout",
    subject: id,
    value: JSON.stringify({
      id,
      layout: { density: "comfortable" },
      ...payload,
    }),
  };
}

function idColor(id) {
  let hash = 0;
  for (const char of id) hash = (hash * 31 + char.charCodeAt(0)) & 0xffffff;
  return hash.toString(16).padStart(6, "0");
}
