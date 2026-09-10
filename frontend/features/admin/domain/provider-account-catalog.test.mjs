import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  accountProviderCatalogEntryFromProvider,
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

test("account Provider catalog options include resource-scoped credential policy", () => {
  const catalog = [
    { id: "gm", name: "GM", display_name: "GM Subscription", type: "gm_subscription", models_count: 0, source: "plugin" },
    { id: "openai", name: "OpenAI", display_name: "OpenAI", type: "openai", models_count: 0, source: "built_in" },
  ];
  const plugins = [{
    id: "tokenhub.provider.gm",
    name: "GM",
    version: "1.0.0",
    source: "marketplace",
    kinds: ["provider"],
    placements: ["gateway_chain"],
    capabilities: [
      { kind: "provider_type", name: "gm_subscription" },
      { kind: "provider_policy", name: "credentials_scope", subject: "gm_subscription", value: "resource" },
    ],
  }];

  const accountCatalog = accountProviderCatalogOptionsFromPlugins(catalog, plugins);

  assert.deepEqual(accountCatalog.map((entry) => entry.id), ["gm"]);
  assert.deepEqual(directProviderCatalogOptions(catalog, accountCatalog).map((entry) => entry.id), ["openai"]);
});

test("account Provider catalog options include adapter resource-scoped credential policy", () => {
  const catalog = [
    { id: "kimi", name: "Kimi", display_name: "Kimi Subscription", type: "kimi_subscription", models_count: 0, source: "plugin" },
    { id: "openai", name: "OpenAI", display_name: "OpenAI", type: "openai", models_count: 0, source: "built_in" },
  ];
  const adapters = [{
    type: "kimi_subscription",
    capabilities: ["chat"],
    plugin_id: "tokenhub.provider.kimi",
    provider_policy: {
      supports_custom_headers: true,
      credentials_scope: "resource",
    },
  }];

  const accountCatalog = accountProviderCatalogOptionsFromPlugins(catalog, [], adapters);

  assert.deepEqual(accountCatalog.map((entry) => entry.id), ["kimi"]);
  assert.deepEqual(directProviderCatalogOptions(catalog, accountCatalog).map((entry) => entry.id), ["openai"]);
});

test("account Provider catalog options are empty without plugin resource type capabilities", () => {
  assert.deepEqual(accountProviderCatalogOptionsFromPlugins([], []).map((entry) => entry.id), []);

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

test("account Provider catalog entry can be synthesized from an existing Provider", () => {
  assert.deepEqual(accountProviderCatalogEntryFromProvider({
    id: "prv_existing",
    name: "Existing Account Pool",
    type: "kimi_subscription",
    base_url: "https://kimi.example/api",
    status: "active",
    healthy: true,
    priority: 10,
    options: { catalog_id: "kimi-subscription", model_category: "chat" },
  }), {
    id: "kimi-subscription",
    name: "Existing Account Pool",
    display_name: "Existing Account Pool",
    type: "kimi_subscription",
    base_url: "https://kimi.example/api",
    categories: ["chat"],
    category_counts: {},
    models_count: 0,
    source: "provider",
  });
});
