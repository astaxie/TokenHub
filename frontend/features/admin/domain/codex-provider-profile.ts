export const codexProviderType = "openai_codex";
export const openAIAccountOAuthRedirectURI = "http://localhost:1455/auth/callback";
export const codexImageModelName = "codex-gpt-image-2";
export const codexImageUpstreamModel = "gpt-image-2";
export const fallbackCodexReasoningEfforts = ["low", "medium", "high", "xhigh", "max"];

export const codexLunaProbeDefaults = {
  model: "gpt-5.6-luna",
  reasoning_effort: "medium",
  speed: "standard",
  prompt: "请用一句话确认 Codex 连接正常。",
};
