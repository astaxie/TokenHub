import { describe, expect, it } from "vitest";
import { providerCatalogCategory } from "./provider-onboarding-categories";

describe("providerCatalogCategory", () => {
  it.each([
    "302ai", "aihubmix", "dmxapi", "fireworks-ai", "firepass", "openrouter",
    "siliconflow", "siliconflow-com", "togetherai", "amazon-bedrock", "azure",
    "google-vertex", "cloudflare-ai-gateway", "github-models", "vercel", "zenmux",
  ])("classifies the known hosted model directory %s as an aggregator", (id) => {
    expect(providerCatalogCategory({ id, type: "openai_compatible" })).toBe("aggregator");
  });

  it.each(["atomic-chat", "kronk", "lmstudio", "lm-studio", "ollama-local", "privatemode-ai", "vllm"])("classifies the known local deployment %s", (id) => {
    expect(providerCatalogCategory({ id, type: "openai_compatible" })).toBe("local");
  });

  it("normalizes catalog identities and gives known identities precedence over adapter types", () => {
    expect(providerCatalogCategory({ id: " Cloudflare_AI_Gateway ", type: "local" })).toBe("aggregator");
    expect(providerCatalogCategory({ id: "LM Studio", type: "openai_compatible" })).toBe("local");
    expect(providerCatalogCategory({ id: "ollama-cloud", type: "ollama" })).toBe("provider");
  });

  it("distinguishes the built-in Ollama Cloud catalog from local Ollama deployments", () => {
    expect(providerCatalogCategory({ id: "ollama", type: "openai_compatible", name: "Ollama Cloud", base_url: "https://ollama.com/v1" })).toBe("provider");
    expect(providerCatalogCategory({ id: "ollama", type: "local", base_url: "https://ollama.com/v1" })).toBe("provider");
    expect(providerCatalogCategory({ id: "ollama", type: "local", base_url: "http://127.0.0.1:11434" })).toBe("local");
    expect(providerCatalogCategory({ id: "private-inference", type: "local" })).toBe("local");
  });

  it("defaults to the provider directory without inferring from a protocol, custom name, or model family", () => {
    const entries = [
      { id: "openai", type: "openai_compatible" },
      { id: "private-gateway", type: "openai_compatible", display_name: "OpenRouter" },
      { id: "unknown-provider", type: "openai_compatible", categories: ["openai", "anthropic", "embedding"] },
    ];
    for (const entry of entries) expect(providerCatalogCategory(entry)).toBe("provider");
  });
});
