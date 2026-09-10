import { describe, expect, it } from "vitest";

import { buildCustomProviderCatalogEntry } from "./catalog";

describe("buildCustomProviderCatalogEntry", () => {
  it("does not synthesize a legacy OpenAI-compatible provider type", () => {
    const entry = buildCustomProviderCatalogEntry("openai", []);

    expect(entry.type).toBe("");
    expect(entry.id).toBe("custom");
    expect(entry.display_name).toBe("自定义渠道商");
    expect(entry.categories).toEqual(["openai"]);
  });
});
