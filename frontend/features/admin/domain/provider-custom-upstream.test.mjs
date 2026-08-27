import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  customUpstreamConnectionKey,
  customUpstreamDiscoveryPayload,
  customUpstreamModelsAreCurrent,
  customUpstreamModelsVisible,
  defaultProviderTypeValue,
  providerAuthMode,
  providerConnectionTestRunAfterUpdate,
  providerTypeValue,
} = await importTypeScript(new URL("./provider-custom-upstream.ts", import.meta.url));

const bearerAnthropicValues = {
  name: "Bearer Anthropic",
  type: "anthropic",
  base_url: "https://anthropic.example.test/v1",
  api_key: "test-api-key",
  anthropic_auth_type: "bearer",
};

const anthropicProviderTypeOptions = [
  { value: "anthropic", label: "Anthropic", supportsCustomHeaders: true, authModes: ["bearer", "x-api-key"] },
];

test("create discovery sends the selected Anthropic auth mode", () => {
  assert.deepEqual(customUpstreamDiscoveryPayload(bearerAnthropicValues, "", "chat", {}, anthropicProviderTypeOptions), {
    provider_id: "",
    name: "Bearer Anthropic",
    type: "anthropic",
    base_url: "https://anthropic.example.test/v1",
    api_key: "test-api-key",
    anthropic_auth_type: "bearer",
    model_category: "chat",
  });
});

test("edit discovery sends the provider ID and selected Anthropic auth mode", () => {
  const values = { ...bearerAnthropicValues, api_key: "" };

  assert.deepEqual(customUpstreamDiscoveryPayload(values, "provider-anthropic", "all", {}, anthropicProviderTypeOptions), {
    provider_id: "provider-anthropic",
    name: "Bearer Anthropic",
    type: "anthropic",
    base_url: "https://anthropic.example.test/v1",
    api_key: "",
    anthropic_auth_type: "bearer",
    model_category: "all",
  });
});

test("discovery includes custom header payloads", () => {
  const headers = {
    headers: { "X-Tenant": "tenant-one" },
    sensitive_headers: ["X-Tenant"],
  };

  assert.deepEqual(customUpstreamDiscoveryPayload(bearerAnthropicValues, "", "chat", headers, anthropicProviderTypeOptions), {
    provider_id: "",
    name: "Bearer Anthropic",
    type: "anthropic",
    base_url: "https://anthropic.example.test/v1",
    api_key: "test-api-key",
    ...headers,
    anthropic_auth_type: "bearer",
    model_category: "chat",
  });
});

test("discovery omits auth mode when provider metadata does not declare modes", () => {
  assert.equal(providerAuthMode({ type: "anthropic" }), "");
  assert.equal(providerAuthMode({ type: "openai_compatible", anthropic_auth_type: "bearer" }), "");
});

test("Anthropic discovery default follows adapter auth modes", () => {
  const providerTypeOptions = [{ value: "anthropic", label: "Anthropic", supportsCustomHeaders: true, authModes: ["bearer"] }];

  assert.equal(providerAuthMode({ type: "anthropic" }, providerTypeOptions), "bearer");
  assert.deepEqual(customUpstreamDiscoveryPayload({ type: "anthropic" }, "", "chat", {}, providerTypeOptions), {
    provider_id: "",
    name: undefined,
    type: "anthropic",
    base_url: undefined,
    api_key: undefined,
    anthropic_auth_type: "bearer",
    model_category: "chat",
  });
});

test("plugin provider discovery follows descriptor auth modes", () => {
  const providerTypeOptions = [{ value: "subscription_provider", label: "Subscription Provider", supportsCustomHeaders: true, authModes: ["oauth", "x-api-key"] }];

  assert.equal(providerAuthMode({ type: "subscription_provider" }, providerTypeOptions), "x-api-key");
  assert.equal(providerAuthMode({ type: "subscription_provider", anthropic_auth_type: "oauth" }, providerTypeOptions), "oauth");
  assert.deepEqual(customUpstreamDiscoveryPayload({ type: "subscription_provider" }, "", "chat", {}, providerTypeOptions), {
    provider_id: "",
    name: undefined,
    type: "subscription_provider",
    base_url: undefined,
    api_key: undefined,
    anthropic_auth_type: "x-api-key",
    model_category: "chat",
  });
});

test("provider type defaults prefer plugin metadata before the legacy fallback", () => {
  const pluginOnlyProviderTypes = [{ value: "native_subscription", label: "Native Subscription", supportsCustomHeaders: false }];

  assert.equal(defaultProviderTypeValue(pluginOnlyProviderTypes), "native_subscription");
  assert.equal(providerTypeValue({}, pluginOnlyProviderTypes), "native_subscription");
  assert.equal(providerTypeValue({ type: "explicit_provider" }, pluginOnlyProviderTypes), "explicit_provider");
  assert.deepEqual(customUpstreamDiscoveryPayload({}, "", "chat", {}, pluginOnlyProviderTypes), {
    provider_id: "",
    name: undefined,
    type: "native_subscription",
    base_url: undefined,
    api_key: undefined,
    anthropic_auth_type: "",
    model_category: "chat",
  });
});

test("changing the Anthropic auth selector invalidates the custom model cache key", () => {
  const apiKeyConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    anthropic_auth_type: "x-api-key",
  }, anthropicProviderTypeOptions);
  const bearerConnection = customUpstreamConnectionKey(bearerAnthropicValues, anthropicProviderTypeOptions);

  assert.notEqual(apiKeyConnection, bearerConnection);
  assert.equal(customUpstreamModelsAreCurrent(1, apiKeyConnection, bearerConnection), false);
});

test("changing the provider type invalidates the custom model cache key", () => {
  const openAIConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    type: "openai_compatible",
  });
  const anthropicConnection = customUpstreamConnectionKey(bearerAnthropicValues);

  assert.notEqual(openAIConnection, anthropicConnection);
});

test("changing custom headers invalidates the custom model cache key", () => {
  const firstConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    custom_headers: `[{"name":"X-Tenant","value":"one"}]`,
  });
  const secondConnection = customUpstreamConnectionKey({
    ...bearerAnthropicValues,
    custom_headers: `[{"name":"X-Tenant","value":"two"}]`,
  });

  assert.notEqual(firstConnection, secondConnection);
});

test("connection inputs invalidate completed and in-flight connection tests", () => {
  const testedRun = 7;

  for (const key of ["base_url", "api_key", "type", "anthropic_auth_type", "custom_headers"]) {
    assert.equal(providerConnectionTestRunAfterUpdate(testedRun, key), testedRun + 1, key);
  }
  for (const key of ["name", "priority"]) {
    assert.equal(providerConnectionTestRunAfterUpdate(testedRun, key), testedRun, key);
  }
});

test("custom model discovery becomes visible in both create and edit flows", () => {
  assert.equal(customUpstreamModelsVisible("create", "connect", true, "models", 1), true);
  assert.equal(customUpstreamModelsVisible("edit", "models", false, "connect", 0), true);
  assert.equal(customUpstreamModelsVisible("edit", "advanced", false, "connect", 0), false);
});
