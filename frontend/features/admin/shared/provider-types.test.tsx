import { describe, expect, it } from "vitest";
import { providerTypeOptionsFromData, providerTypeSupportsCustomHeaders } from "./ui";
import { emptyData } from "../domain/catalog";

describe("providerTypeOptionsFromData", () => {
  it("includes provider types declared by plugins, catalog entries, providers, and the current form value", () => {
    const data = emptyData();
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
    data.providerCatalog = [{
      id: "glm-subscription",
      name: "GLM Subscription",
      display_name: "GLM Subscription",
      type: "glm_subscription",
      models_count: 0,
      source: "plugin",
    }];
    data.providers = [{
      id: "prv_existing",
      name: "Existing Subscription",
      type: "existing_subscription",
      base_url: "",
      priority: 10,
      status: "active",
      healthy: true,
    }];

    const options = providerTypeOptionsFromData(data, { type: "draft_subscription" }).map((option) => option.value);

    expect(options.slice(0, 3)).toEqual(["mock", "openai", "openai_codex"]);
    expect(options).toContain("kimi_subscription");
    expect(options).toContain("glm_subscription");
    expect(options).toContain("existing_subscription");
    expect(options).toContain("draft_subscription");
  });

  it("carries provider header policy from adapter descriptors", () => {
    const data = emptyData();
    data.providerAdapters = [{
      type: "native_subscription",
      capabilities: ["responses"],
      plugin_id: "tokenhub.provider.native-subscription",
      provider_policy: {
        route_protocols: ["native/responses"],
        supports_custom_headers: false,
      },
    }];

    const options = providerTypeOptionsFromData(data);

    expect(providerTypeSupportsCustomHeaders(options, "native_subscription")).toBe(false);
    expect(providerTypeSupportsCustomHeaders(options, "openai_compatible")).toBe(true);
    expect(providerTypeSupportsCustomHeaders(options, "azure_openai")).toBe(false);
    expect(providerTypeSupportsCustomHeaders(options, "openai_codex")).toBe(false);
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
      ],
    }];

    const options = providerTypeOptionsFromData(data);

    expect(providerTypeSupportsCustomHeaders(options, "oauth_subscription")).toBe(false);
  });
});
