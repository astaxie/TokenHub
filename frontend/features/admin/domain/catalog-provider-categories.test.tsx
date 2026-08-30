import { describe, expect, it } from "vitest";
import { canonicalModelNameForUI, catalogModelCategoryOptions, emptyData, modelCategory, modelCategoryInitial, modelCategoryLabel, modelCategoryTabs, providerCategories, providerEntrySupportsCategory } from "./catalog";
import type { Provider } from "../core/types";

describe("providerCategories", () => {
  it("uses account provider catalog metadata for plugin account resources", () => {
    const provider: Provider = {
      id: "prv_kimi",
      name: "Kimi",
      type: "kimi_subscription",
      status: "active",
      healthy: true,
      priority: 1,
    };
    const data = emptyData();
    data.providers = [provider];
    data.providerCatalog = [{
      id: "kimi-subscription",
      name: "Kimi Subscription",
      display_name: "Kimi Subscription",
      type: "kimi_subscription",
      categories: ["kimi"],
      models_count: 0,
      source: "plugin",
    }];
    data.providerAdapters = [{
      type: "kimi_subscription",
      capabilities: [],
      resource_types: [{ type: "kimi_subscription_account" }],
      provider_policy: { supports_custom_headers: true, credentials_scope: "resource" },
    }];
    data.providerResources = [{
      id: "rsrc_kimi",
      provider_id: provider.id,
      name: "Kimi Account",
      resource_type: "kimi_subscription_account",
      status: "active",
      healthy: true,
      priority: 1,
      weight: 100,
    }];

    expect(providerCategories(provider, data)).toEqual(["kimi"]);
  });

  it("uses provider catalog categories before type-name inference", () => {
    const provider: Provider = {
      id: "prv_vendor",
      name: "Vendor",
      type: "opaque_subscription_provider",
      status: "active",
      healthy: true,
      priority: 1,
    };
    const data = emptyData();
    data.providers = [provider];
    data.providerCatalog = [{
      id: "vendor-subscription",
      name: "Vendor Subscription",
      display_name: "Vendor Subscription",
      type: "opaque_subscription_provider",
      categories: ["kimi"],
      models_count: 0,
      source: "plugin",
    }];

    expect(providerCategories(provider, data)).toEqual(["kimi"]);
  });

  it("does not infer plugin provider categories from provider type names", () => {
    const provider: Provider = {
      id: "prv_opaque",
      name: "Opaque",
      type: "anthropic_gemini_codex_proxy",
      status: "active",
      healthy: true,
      priority: 1,
    };
    const data = emptyData();
    data.providers = [provider];

    expect(providerCategories(provider, data)).toEqual(["custom"]);
  });

  it("derives category initials from labels without provider-specific branches", () => {
    expect(modelCategoryInitial("opaque", "Acme")).toBe("A");
    expect(modelCategoryInitial("opaque", "")).toBe("O");
  });

  it("uses adapter model category metadata declared by provider plugins", () => {
    const provider: Provider = {
      id: "prv_acme",
      name: "Acme",
      type: "opaque_subscription_provider",
      status: "active",
      healthy: true,
      priority: 1,
    };
    const data = emptyData();
    data.providers = [provider];
    data.models = [{
      id: "mdl_acme",
      name: "opaquev2",
      family: "",
      modality: "chat",
      status: "active",
    }];
    data.providerCatalog = [{
      id: "acme",
      name: "Acme",
      display_name: "Acme",
      type: "opaque_subscription_provider",
      categories: ["acme"],
      category_counts: { acme: 1 },
      models_count: 1,
      source: "plugin",
      models: [{ id: "opaquev2", name: "opaquev2", display_name: "Opaque Vendor Reasoner" }],
    }];
    data.providerAdapters = [{
      type: "opaque_subscription_provider",
      capabilities: [],
      provider_policy: {
        supports_custom_headers: true,
        model_categories: [{
          key: "acme",
          label: "Acme",
          order: 25,
          aliases: ["opaque"],
          canonical_prefixes: ["opaque"],
        }],
      },
    }];

    expect(providerCategories(provider, data)).toEqual(["acme"]);
    expect(modelCategory(data.models[0], data)).toBe("acme");
    expect(modelCategoryLabel("acme", data)).toBe("Acme");
    expect(canonicalModelNameForUI("opaquev2", undefined, data)).toBe("opaque-v2");
    expect(catalogModelCategoryOptions(data.providerCatalog, data)[0]).toMatchObject({ key: "acme", label: "Acme", count: 1 });
    expect(providerEntrySupportsCategory(data.providerCatalog[0], "acme", data)).toBe(true);
    expect(modelCategoryTabs(data, "models")[1]).toMatchObject({ key: "acme", label: "Acme", count: 1 });
  });
});
