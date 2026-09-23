import { afterEach, describe, expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { providerDisplayBaseURL, providerDisplayType } from "../domain/entities";
import { providerTypeLabel, providerTypeLabelFromData } from "../domain/labels";
import { setActiveLanguage } from "../i18n/runtime";
import { providerTypeOptionsFromData } from "./ui";

afterEach(() => setActiveLanguage("en"));

describe("local provider display labels", () => {
  it.each([
    ["zh-CN", "本地服务"],
    ["en", "Local Provider"],
    ["ja", "ローカルサービス"],
  ] as const)("localizes visible labels in %s while preserving the provider type", (language, label) => {
    setActiveLanguage(language);
    const data = emptyData();
    const provider = { id: "prv_local", name: "Local Cluster", type: "mock", base_url: "", priority: 1, status: "active", healthy: true };
    data.providers = [provider];
    data.providerCatalog = [{ id: "local", name: "Mock Catalog", display_name: "Mock Provider", type: "mock", models_count: 0, source: "plugin" }];
    data.plugins = [{
      id: "tokenhub.provider.mock", name: "Mock Plugin", version: "1.0.0", source: "local_file",
      kinds: ["provider"], placements: ["gateway_chain"],
      capabilities: [{ kind: "provider_type", name: "mock" }],
    }];
    data.providerAdapters = [{ type: "mock", capabilities: ["chat"], plugin_id: "tokenhub.provider.mock" }];

    expect(providerTypeLabel(" mock ")).toBe(label);
    expect(providerTypeLabelFromData(data, provider.type)).toBe(label);
    expect(providerTypeOptionsFromData(data)).toMatchObject([{ value: "mock", label }]);
    data.providerCatalog = [];
    expect(providerTypeLabelFromData(data, provider.type)).toBe(label);
    expect(providerTypeOptionsFromData(data)).toMatchObject([{ value: "mock", label }]);
    expect(providerDisplayBaseURL(provider, [])).toBe(label);
    expect(providerDisplayBaseURL({ ...provider, base_url: "http://cluster.example.test/v1" }, [])).toBe("http://cluster.example.test/v1");
    expect(providerDisplayType(provider, [])).toBe("mock");
  });
});
