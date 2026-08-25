import { type ProviderCatalogEntry } from "../core/types";

export const codexProviderType = "openai_codex";
export const codexSubscriptionBaseURL = "https://chatgpt.com/backend-api/codex";
export const openAIAccountOAuthRedirectURI = "http://localhost:1455/auth/callback";
export const codexImageModelName = "codex-gpt-image-2";
export const codexImageUpstreamModel = "gpt-image-2";
export const fallbackCodexReasoningEfforts = ["low", "medium", "high", "xhigh", "max"];

export const codexProviderCatalogSummary: ProviderCatalogEntry = {
  id: "openai-codex",
  name: "OpenAI Codex",
  display_name: "OpenAI Codex",
  type: codexProviderType,
  base_url: codexSubscriptionBaseURL,
  categories: ["codex"],
  category_counts: { codex: 0 },
  models_count: 0,
  source: "openai-codex-live",
};

export const accountProviderCatalogOptions = [codexProviderCatalogSummary];

export const codexLunaProbeDefaults = {
  model: "gpt-5.6-luna",
  reasoning_effort: "medium",
  speed: "standard",
  prompt: "请用一句话确认 Codex 连接正常。",
};
