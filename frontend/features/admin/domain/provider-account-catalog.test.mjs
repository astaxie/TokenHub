import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  accountProviderCatalogOptionsFromPlugins,
  directProviderCatalogOptions,
} = await importTypeScript(new URL("./provider-account-catalog.ts", import.meta.url));

test("account Provider catalog options come from plugin resource type capabilities", () => {
  const catalog = [
    { id: "kimi", name: "Kimi", display_name: "Kimi Subscription", type: "kimi_subscription", models_count: 0, source: "plugin" },
    { id: "openai", name: "OpenAI", display_name: "OpenAI", type: "openai", models_count: 0, source: "built_in" },
  ];
  const plugins = [{
    id: "tokenhub.provider.kimi",
    name: "Kimi",
    version: "1.0.0",
    source: "marketplace",
    kinds: ["provider"],
    placements: ["gateway_chain"],
    capabilities: [
      { kind: "provider_type", name: "kimi_subscription" },
      { kind: "provider_resource_type", name: "kimi_oauth_account", subject: "kimi_subscription" },
    ],
  }];

  const accountCatalog = accountProviderCatalogOptionsFromPlugins(catalog, plugins);

  assert.deepEqual(accountCatalog.map((entry) => entry.id), ["kimi"]);
  assert.deepEqual(directProviderCatalogOptions(catalog, accountCatalog).map((entry) => entry.id), ["openai"]);
});

test("account Provider catalog options keep the legacy Codex fallback only when needed", () => {
  assert.deepEqual(accountProviderCatalogOptionsFromPlugins([], []).map((entry) => entry.id), ["openai-codex"]);

  const plugins = [{
    id: "tokenhub.provider.kimi",
    name: "Kimi",
    version: "1.0.0",
    source: "marketplace",
    kinds: ["provider"],
    placements: ["gateway_chain"],
    capabilities: [
      { kind: "provider_resource_type", name: "kimi_oauth_account", subject: "kimi_subscription" },
    ],
  }];
  assert.deepEqual(accountProviderCatalogOptionsFromPlugins([], plugins), []);
});
