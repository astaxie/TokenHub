// The models an account is offered before anyone has signed in — the seed
// magpie and OpenCode read for the provider. Regenerate with
// `bun refresh-models.mjs` (beside this seed): it
// reads amr-link's catalog at the signed-in account's key, which is the same
// list a signed-in account is given again on every listing.
//
// name: what the model is called; context/output: its token limits; input:
// the non-text modalities it takes; cost and capability: the vendor's own
// words, which the plugin turns into magpie's rate and thinking flags;
// efforts: the reasoning levels amr-link was seen to take and think at,
// measured, not read (#137; max too, plugins#141: the vendor names low, high
// and max when asked for one it doesn't take), so a refresh keeps them.

export const MODELS = {
  "claude-haiku-5.5": { name: "Claude Haiku 5.5", context: 1000000, output: 132000, input: ["image", "file"], cost: "low", capability: "standard", efforts: ["low", "high", "max"] },
  "deepseek-v4-flash": { name: "DeepSeek V4 Flash", context: 1048000, output: 232000, cost: "medium", capability: "standard", efforts: ["low", "high", "max"] },
  "deepseek-v4-flash-vision-exp": { name: "DeepSeek V4 Flash Vision (exp)", context: 1048000, output: 232000, input: ["image"], cost: "medium", capability: "standard", efforts: ["low", "high", "max"] },
  "deepseek-v4-pro": { name: "DeepSeek V4 Pro", context: 1048000, output: 232000, cost: "high", capability: "advanced", efforts: ["low", "high", "max"] },
  "deepseek-v4.1-flash": { name: "DeepSeek V4.1 Flash", context: 1048000, output: 232000, input: ["image"], cost: "low", capability: "advanced", efforts: ["low", "high", "max"] },
  "glm-5.3-flash": { name: "GLM 5.3 Flash", context: 1048000, output: 232000, input: ["image", "video"], cost: "low", capability: "advanced", efforts: ["low", "high", "max"] },
  "glm-5.3-flashx": { name: "GLM 5.3 FlashX", context: 1000000, output: 232000, input: ["image", "file", "video"], cost: "medium", capability: "advanced", efforts: ["low", "high", "max"] },
  "gpt-6-luna": { name: "GPT-6 Luna", context: 1050000, output: 304000, input: ["image", "file"], cost: "low", capability: "standard", efforts: [] },
  "kimi-k2.7-code": { name: "Kimi K2.7 Code", context: 262000, output: 262000, input: ["image"], cost: "medium", capability: "advanced", efforts: ["low", "high", "max"] },
  "mimo-v2.6-flash": { name: "MiMo V2.6 Flash", context: 1048000, output: 232000, input: ["image", "audio", "video"], cost: "low", capability: "standard", efforts: ["low", "high", "max"] },
  "mimo-v2.6-pro": { name: "MiMo V2.6 Pro", context: 1048000, output: 232000, input: ["image", "audio", "video"], cost: "medium", capability: "advanced", efforts: ["low", "high", "max"] },
}
