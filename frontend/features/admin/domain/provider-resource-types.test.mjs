import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  isProviderAccountResourceForData,
  isProviderAccountResourceTypeForData,
} = await importTypeScript(new URL("./provider-resource-types.ts", import.meta.url));

function dataWithPluginResourceType() {
  return {
    providers: [{ id: "prv_kimi", type: "kimi_subscription" }],
    providerAdapters: [{
      type: "kimi_subscription",
      resource_types: [{ type: "kimi_subscription_account" }],
    }],
    plugins: [],
  };
}

test("provider account resource type checks follow plugin metadata when it exists", () => {
  const data = dataWithPluginResourceType();

  assert.equal(isProviderAccountResourceTypeForData(data, "kimi_subscription", "kimi_subscription_account"), true);
  assert.equal(isProviderAccountResourceTypeForData(data, "kimi_subscription", "plugin_cache_bucket"), false);
  assert.equal(isProviderAccountResourceTypeForData(data, "kimi_subscription", "api_key"), false);
  assert.equal(isProviderAccountResourceForData(data, {
    provider_id: "prv_kimi",
    resource_type: "kimi_subscription_account",
  }), true);
});

test("provider account resource type checks reject undeclared types for plugin providers", () => {
  const data = {
    providers: [{ id: "prv_plain", type: "plain_provider" }],
    providerAdapters: [{
      type: "plain_provider",
      resource_types: [],
    }],
    plugins: [],
  };

  assert.equal(isProviderAccountResourceTypeForData(data, "plain_provider", "plain_account"), false);
  assert.equal(isProviderAccountResourceForData(data, {
    provider_id: "prv_plain",
    resource_type: "plain_account",
  }), false);
});

test("provider account resource type checks reject undeclared types for plugin provider capabilities", () => {
  const data = {
    providers: [{ id: "prv_plain", type: "plain_provider" }],
    providerAdapters: [],
    plugins: [{
      capabilities: [{ kind: "provider_type", name: "plain_provider" }],
    }],
  };

  assert.equal(isProviderAccountResourceTypeForData(data, "plain_provider", "plain_account"), false);
});

test("provider account resource type checks preserve legacy resources without metadata", () => {
  const data = { providers: [], providerAdapters: [], plugins: [] };

  assert.equal(isProviderAccountResourceTypeForData(data, "", "legacy_account"), true);
  assert.equal(isProviderAccountResourceTypeForData(data, "", "api_key"), false);
});
