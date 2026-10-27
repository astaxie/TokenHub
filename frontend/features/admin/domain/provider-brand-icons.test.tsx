import { describe, expect, it } from "vitest";
import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import coverage from "../../../../tools/magpie-subscription-coverage.json";
import iconManifest from "../../../public/provider-icons/subscription-icons.provenance.json";
import { providerBrandIconSource } from "./provider-brand-icons";

const entry = (id: string, type = id, name = id) => ({ id, type, name, display_name: name });

describe("providerBrandIconSource", () => {
  const nativeCatalogIDs: Record<string, string> = { "chatgpt-api": "chatgpt-subscription", claude: "claude-subscription", codex: "openai-codex", copilot: "github-copilot-subscription", gemini: "gemini-cli" };

  it.each(coverage.providers)("uses the product icon for the $upstream_id catalog and saved account", (provider) => {
    const asset = iconManifest.providers.find(item => item.upstream_id === provider.upstream_id);
    expect(asset, `Missing brand asset for ${provider.upstream_id}`).toBeDefined();
    const catalogID = nativeCatalogIDs[provider.upstream_id] ?? provider.upstream_id;
    const providerType = provider.tokenhub.provider_type;
    const expected = `/provider-icons/${asset!.file}`;
    expect(providerBrandIconSource(entry(catalogID, providerType, "Custom account name"))).toBe(expected);
    expect(providerBrandIconSource(entry(providerType, providerType, "Renamed account"))).toBe(expected);
  });

  it("ships every declared subscription icon locally with its recorded artwork", () => {
    expect(new Set(iconManifest.providers.map(item => item.upstream_id))).toEqual(new Set(coverage.providers.map(item => item.upstream_id)));
    for (const { file, sha256 } of iconManifest.assets) {
      const bytes = readFileSync(resolve("public/provider-icons", file));
      expect(createHash("sha256").update(bytes).digest("hex"), file).toBe(sha256);
    }
    for (const provider of iconManifest.providers) expect(iconManifest.assets.some(asset => asset.file === provider.file)).toBe(true);
  });

  it("uses the provider brand instead of the model category", () => {
    expect(providerBrandIconSource(entry("ollama", "local", "Ollama Cloud"))).toBe("/provider-icons/ollama.svg");
    expect(providerBrandIconSource(entry("azure-openai", "azure_openai", "Azure OpenAI"))).toBe("/provider-icons/azure-color.svg");
    expect(providerBrandIconSource(entry("google-vertex-anthropic", "openai_compatible", "Vertex (Anthropic)"))).toBe("/provider-icons/vertexai-color.svg");
    expect(providerBrandIconSource(entry("llama", "openai_compatible", "Llama"))).toBe("/provider-icons/llama.svg");
  });

  it("prefers the catalog brand over a shared protocol adapter or custom label", () => {
    expect(providerBrandIconSource(entry("stepfun", "openai", "OpenAI"))).toBe("/provider-icons/stepfun-color.svg");
    expect(providerBrandIconSource(entry("azure-openai", "openai", "Azure OpenAI"))).toBe("/provider-icons/azure-color.svg");
  });

  it("covers common catalog providers and keeps generic gateways neutral", () => {
    expect(providerBrandIconSource(entry("siliconflow", "openai_compatible", "SiliconFlow"))).toBe("/provider-icons/siliconcloud-color.svg");
    expect(providerBrandIconSource(entry("github-copilot", "openai_compatible", "GitHub Copilot"))).toBe("/provider-icons/githubcopilot.svg");
    expect(providerBrandIconSource(entry("stepfun", "openai_compatible", "Stepfun"))).toBe("/provider-icons/stepfun-color.svg");
    expect(providerBrandIconSource(entry("kimi", "openai_compatible", "Kimi"))).toBe("/provider-icons/kimi-color.svg");
    expect(providerBrandIconSource(entry("moonshot", "openai_compatible", "Moonshot"))).toBe("/provider-icons/moonshot.svg");
    expect(providerBrandIconSource(entry("openai-compatible", "openai_compatible", "OpenAI-Compatible"))).toBe("");
    expect(providerBrandIconSource(entry("private-gateway", "openai_compatible", "Private Gateway"))).toBe("");
    expect(providerBrandIconSource(entry("bailing", "openai_compatible", "Bailing"))).toBe("");
  });
});
