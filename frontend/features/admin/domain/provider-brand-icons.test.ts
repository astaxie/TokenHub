import { describe, expect, it } from "vitest";
import { providerBrandIconSource } from "./provider-brand-icons";

const entry = (id: string, type = id, name = id) => ({ id, type, name, display_name: name });

describe("providerBrandIconSource", () => {
  it("uses the provider brand instead of the model category", () => {
    expect(providerBrandIconSource(entry("ollama", "local", "Ollama Cloud"))).toBe("/provider-icons/ollama.svg");
    expect(providerBrandIconSource(entry("azure-openai", "azure_openai", "Azure OpenAI"))).toBe("/provider-icons/azure-color.svg");
    expect(providerBrandIconSource(entry("google-vertex-anthropic", "openai_compatible", "Vertex (Anthropic)"))).toBe("/provider-icons/vertexai-color.svg");
    expect(providerBrandIconSource(entry("llama", "openai_compatible", "Llama"))).toBe("/provider-icons/llama.svg");
  });

  it("covers common catalog providers and keeps generic gateways neutral", () => {
    expect(providerBrandIconSource(entry("siliconflow", "openai_compatible", "SiliconFlow"))).toBe("/provider-icons/siliconcloud-color.svg");
    expect(providerBrandIconSource(entry("github-copilot", "openai_compatible", "GitHub Copilot"))).toBe("/provider-icons/githubcopilot.svg");
    expect(providerBrandIconSource(entry("openai-compatible", "openai_compatible", "OpenAI-Compatible"))).toBe("");
    expect(providerBrandIconSource(entry("private-gateway", "openai_compatible", "Private Gateway"))).toBe("");
  });
});
