import { describe, expect, it } from "vitest";
import { providerTypeAuthModes, providerTypeModelDiscovery, providerTypeOptionsFromData, providerTypePreferredAuthMode, providerTypeRequiresAPIKey, providerTypeRouteProtocols, providerTypeSupportsCustomHeaders } from "./ui";
import { emptyData } from "../domain/catalog";
import { providerTypeLabelFromData } from "../domain/labels";

describe("providerTypeOptionsFromData", () => {
  it("includes provider types declared by plugins, catalog entries, providers, and the current form value", () => {
    const data = emptyData();
    data.providerAdapters = [
      {
        type: "mock",
        capabilities: ["chat"],
        plugin_id: "tokenhub.provider.mock",
        provider_policy: { route_protocols: ["chat"], supports_custom_headers: true },
      },
      {
        type: "openai",
        capabilities: ["chat"],
        plugin_id: "tokenhub.provider.openai",
        provider_policy: { route_protocols: ["chat"], supports_custom_headers: true },
      },
      {
        type: "openai_codex",
        capabilities: ["responses"],
        plugin_id: "tokenhub.provider.openai-codex",
        provider_policy: { route_protocols: ["responses"], supports_custom_headers: false },
      },
    ];
    data.plugins = [{
      id: "tokenhub.provider.kimi-subscription",
      name: "Kimi Subscription",
      version: "1.0.0",
      source: "local_file",
      kinds: ["provider"],
      placements: ["gateway_chain", "management_action"],
      capabilities: [
        { kind: "provider", name: "provider_type", subject: "kimi_subscription" },
        { kind: "gateway", name: "responses", subject: "kimi_subscription" },
      ],
    }];
    data.providerCatalog = [
      {
        id: "glm-subscription",
        name: "GLM Subscription",
        display_name: "GLM Subscription",
        type: "glm_subscription",
        models_count: 0,
        source: "plugin",
      },
      {
        id: "openai-codex-subscription",
        name: "OpenAI Codex Subscription",
        display_name: "OpenAI Codex Subscription",
        type: "openai_codex",
        models_count: 0,
        source: "plugin",
      },
    ];
    data.providers = [{
      id: "prv_existing",
      name: "Existing Subscription",
      type: "existing_subscription",
      base_url: "",
      priority: 10,
      status: "active",
      healthy: true,
    }];

    const options = providerTypeOptionsFromData(data, { type: "draft_subscription" });
    const values = options.map((option) => option.value);

    expect(values.slice(0, 3)).toEqual(["mock", "openai", "openai_codex"]);
    expect(values).toContain("kimi_subscription");
    expect(values).toContain("glm_subscription");
    expect(values).toContain("existing_subscription");
    expect(values).toContain("draft_subscription");
    expect(options.find((option) => option.value === "kimi_subscription")?.label).toBe("Kimi Subscription");
    expect(options.find((option) => option.value === "glm_subscription")?.label).toBe("GLM Subscription");
    expect(options.find((option) => option.value === "openai_codex")?.label).toBe("OpenAI Codex Subscription");
    expect(options.find((option) => option.value === "openai")?.label).toBe("OpenAI 官方");
  });

  it("uses legacy provider types only when no plugin provider source is loaded", () => {
    const fallback = providerTypeOptionsFromData(emptyData()).map((option) => option.value);
    expect(fallback).toContain("qwen");

    const data = emptyData();
    data.providerAdapters = [{
      type: "openai_compatible",
      capabilities: ["chat"],
      plugin_id: "tokenhub.provider.openai-compatible",
      provider_policy: { route_protocols: ["chat"], supports_custom_headers: true },
    }];

    const descriptorFirst = providerTypeOptionsFromData(data).map((option) => option.value);
    expect(descriptorFirst).toEqual(["openai_compatible"]);
  });

  it("keeps plugin provider discovery order instead of legacy provider precedence", () => {
    const data = emptyData();
    data.providerAdapters = [
      {
        type: "zeta_subscription",
        capabilities: ["responses"],
        plugin_id: "tokenhub.provider.zeta",
        provider_policy: { route_protocols: ["responses"], supports_custom_headers: false },
      },
      {
        type: "openai",
        capabilities: ["chat"],
        plugin_id: "tokenhub.provider.openai",
        provider_policy: { route_protocols: ["chat"], supports_custom_headers: true },
      },
    ];

    expect(providerTypeOptionsFromData(data).map((option) => option.value)).toEqual(["zeta_subscription", "openai"]);
  });

  it("carries provider header policy from adapter descriptors", () => {
    const data = emptyData();
    data.providerAdapters = [{
      type: "native_subscription",
      capabilities: ["responses"],
      plugin_id: "tokenhub.provider.native-subscription",
      provider_policy: {
        auth_modes: ["x-api-key", "bearer"],
        api_key_required: false,
        claude_code_attribution_default: "preserve",
        default_base_url: "https://native.example/v1/",
        default_catalog_provider_type: true,
        model_discovery: {
          path: "/native/models",
          auth: "query_param",
          api_key_query_param: "access_token",
          headers: { "x-native-version": "2026-01-01" },
        },
        route_protocols: ["native/responses"],
        supports_custom_headers: false,
      },
    }];

    const options = providerTypeOptionsFromData(data);

    expect(providerTypeSupportsCustomHeaders(options, "native_subscription")).toBe(false);
    expect(providerTypeAuthModes(options, "native_subscription")).toEqual(["bearer", "x-api-key"]);
    expect(providerTypePreferredAuthMode(options, "native_subscription")).toBe("x-api-key");
    expect(providerTypeRequiresAPIKey(options, "native_subscription")).toBe(false);
    expect(providerTypeRequiresAPIKey(options, "openai_compatible")).toBe(true);
    expect(providerTypeRouteProtocols(options, "native_subscription")).toEqual(["native/responses"]);
    expect(options.find((option) => option.value === "native_subscription")?.apiKeyRequired).toBe(false);
    expect(options.find((option) => option.value === "native_subscription")?.claudeCodeAttributionDefault).toBe("preserve");
    expect(options.find((option) => option.value === "native_subscription")?.defaultBaseURL).toBe("https://native.example/v1");
    expect(options.find((option) => option.value === "native_subscription")?.defaultCatalogProviderType).toBe(true);
    expect(providerTypeModelDiscovery(options, "native_subscription")).toEqual({
      path: "/native/models",
      auth: "query_param",
      apiKeyQueryParam: "access_token",
      headers: { "x-native-version": "2026-01-01" },
    });
    expect(providerTypeSupportsCustomHeaders(options, "openai_compatible")).toBe(true);
    expect(providerTypeSupportsCustomHeaders(options, "azure_openai")).toBe(true);
    expect(providerTypeSupportsCustomHeaders(options, "openai_codex")).toBe(true);
  });

  it("does not hard-code custom header support by built-in provider type", () => {
    const data = emptyData();
    data.providerAdapters = [
      {
        type: "openai_codex",
        capabilities: ["responses"],
        plugin_id: "tokenhub.provider.openai-codex",
        provider_policy: { route_protocols: ["responses"], supports_custom_headers: false },
      },
      {
        type: "azure_openai",
        capabilities: ["chat"],
        plugin_id: "tokenhub.provider.azure-openai",
        provider_policy: { route_protocols: ["chat"], supports_custom_headers: false },
      },
    ];

    const options = providerTypeOptionsFromData(data);

    expect(providerTypeSupportsCustomHeaders(options, "openai_codex")).toBe(false);
    expect(providerTypeSupportsCustomHeaders(options, "azure_openai")).toBe(false);
    expect(providerTypeSupportsCustomHeaders([], "openai_codex")).toBe(true);
    expect(providerTypeSupportsCustomHeaders([], "azure_openai")).toBe(true);
  });

  it("does not infer auth modes from built-in provider type names", () => {
    expect(providerTypeAuthModes([], "anthropic")).toEqual([]);
    expect(providerTypePreferredAuthMode([], "anthropic")).toBe("");
  });

  it("carries provider header policy from plugin capabilities", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.oauth-subscription",
      name: "OAuth Subscription",
      version: "1.0.0",
      source: "local_file",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [
        { kind: "provider", name: "responses", subject: "oauth_subscription" },
        { kind: "provider_policy", name: "supports_custom_headers", subject: "oauth_subscription", value: "false" },
        { kind: "provider_policy", name: "api_key_required", subject: "oauth_subscription", value: "false" },
        { kind: "provider_policy", name: "auth_mode", subject: "oauth_subscription", value: "oauth" },
        { kind: "provider_policy", name: "auth_mode", subject: "oauth_subscription", value: "personal_access_token" },
        { kind: "provider_policy", name: "route_protocol", subject: "oauth_subscription", value: "responses" },
        { kind: "provider_policy", name: "route_protocol", subject: "oauth_subscription", value: "images/generations" },
        { kind: "provider_policy", name: "claude_code_attribution_default", subject: "oauth_subscription", value: "strip" },
        { kind: "provider_policy", name: "default_base_url", subject: "oauth_subscription", value: "https://oauth.example/v1/" },
        { kind: "provider_policy", name: "default_catalog_provider_type", subject: "oauth_subscription", value: "true" },
        { kind: "provider_policy", name: "model_discovery_path", subject: "oauth_subscription", value: "/oauth/models" },
        { kind: "provider_policy", name: "model_discovery_auth", subject: "oauth_subscription", value: "query_param" },
        { kind: "provider_policy", name: "model_discovery_api_key_query_param", subject: "oauth_subscription", value: "access_token" },
        { kind: "provider_policy", name: "model_discovery_headers", subject: "oauth_subscription", value: "{\"x-oauth-version\":\"2026-01-01\"}" },
      ],
    }];

    const options = providerTypeOptionsFromData(data);

    expect(providerTypeSupportsCustomHeaders(options, "oauth_subscription")).toBe(false);
    expect(providerTypeRequiresAPIKey(options, "oauth_subscription")).toBe(false);
    expect(providerTypeAuthModes(options, "oauth_subscription")).toEqual(["oauth", "personal_access_token"]);
    expect(providerTypePreferredAuthMode(options, "oauth_subscription")).toBe("oauth");
    expect(providerTypeRouteProtocols(options, "oauth_subscription")).toEqual(["images/generations", "responses"]);
    const option = options.find((option) => option.value === "oauth_subscription");
    expect(option?.claudeCodeAttributionDefault).toBe("strip");
    expect(option?.defaultBaseURL).toBe("https://oauth.example/v1");
    expect(option?.defaultCatalogProviderType).toBe(true);
    expect(providerTypeModelDiscovery(options, "oauth_subscription")).toEqual({
      path: "/oauth/models",
      auth: "query_param",
      apiKeyQueryParam: "access_token",
      headers: { "x-oauth-version": "2026-01-01" },
    });
  });

  it("resolves provider type labels from catalog entries before plugin names", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.kimi",
      name: "Kimi Plugin",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [{ kind: "provider_type", name: "kimi_subscription" }],
    }];
    data.providerCatalog = [{
      id: "kimi",
      name: "Kimi Catalog",
      display_name: "Kimi Subscription",
      type: "kimi_subscription",
      models_count: 0,
      source: "plugin",
    }];

    expect(providerTypeLabelFromData(data, "kimi_subscription")).toBe("Kimi Subscription");
    expect(providerTypeLabelFromData(data, "unknown_subscription")).toBe("unknown_subscription");
  });

  it("uses plugin provider catalog capabilities for labels and defaults", () => {
    const data = emptyData();
    data.plugins = [{
      id: "tokenhub.provider.gm",
      name: "GM Plugin",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [
        { kind: "provider", name: "responses", subject: "gm_subscription" },
        {
          kind: "provider_catalog",
          name: "entry",
          subject: "gm_subscription",
          value: "{\"id\":\"gm\",\"name\":\"GM Catalog\",\"display_name\":\"GM Subscription\",\"type\":\"gm_subscription\",\"base_url\":\"https://gm.example/v1/\",\"models_count\":3}",
        },
      ],
    }];

    const options = providerTypeOptionsFromData(data);
    const option = options.find((item) => item.value === "gm_subscription");

    expect(providerTypeLabelFromData(data, "gm_subscription")).toBe("GM Subscription");
    expect(option?.label).toBe("GM Subscription");
    expect(option?.defaultBaseURL).toBe("https://gm.example/v1");
  });
});
