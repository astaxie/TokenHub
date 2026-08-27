import { describe, expect, it } from "vitest";
import { emptyData, providerCategories } from "./catalog";
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
});
