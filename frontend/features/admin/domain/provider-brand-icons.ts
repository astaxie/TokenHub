import type { ProviderCatalogEntry } from "../core/types";

type ProviderIconRule = {
  asset: string;
  exact: string[];
  prefixes?: string[];
};

const providerIconRules: ProviderIconRule[] = [
  { asset: "openai.svg", exact: ["openai", "openai-codex", "codex"] },
  { asset: "azure-color.svg", exact: ["azure", "azure-openai", "azure-cognitive-services"] },
  { asset: "vertexai-color.svg", exact: ["google-vertex", "google-vertex-anthropic", "vertex", "vertex-ai", "vertexai"] },
  { asset: "bedrock-color.svg", exact: ["amazon-bedrock", "bedrock"] },
  { asset: "anthropic.svg", exact: ["anthropic", "claude"] },
  { asset: "gemini-color.svg", exact: ["google", "google-gemini", "gemini"] },
  { asset: "xai.svg", exact: ["xai", "grok"] },
  { asset: "deepseek-color.svg", exact: ["deepseek"] },
  { asset: "qwen-color.svg", exact: ["qwen", "dashscope"] },
  { asset: "alibabacloud-color.svg", exact: ["alibaba", "alibaba-cn", "alibaba-intl", "alibabacloud"], prefixes: ["alibaba-"] },
  { asset: "kimi-color.svg", exact: ["kimi", "kimi-for-coding"], prefixes: ["kimi-"] },
  { asset: "moonshot.svg", exact: ["moonshot", "moonshot-ai"], prefixes: ["moonshot-"] },
  { asset: "zhipu-color.svg", exact: ["zai", "zhipuai", "zhipu", "glm"], prefixes: ["zai-", "zhipuai-"] },
  { asset: "minimax-color.svg", exact: ["minimax"], prefixes: ["minimax-"] },
  { asset: "doubao-color.svg", exact: ["doubao", "volcengine"] },
  { asset: "siliconcloud-color.svg", exact: ["siliconflow", "siliconflow-com"] },
  { asset: "modelscope-color.svg", exact: ["modelscope"] },
  { asset: "openrouter-color.svg", exact: ["openrouter"] },
  { asset: "groq.svg", exact: ["groq"] },
  { asset: "together-color.svg", exact: ["togetherai", "together"] },
  { asset: "fireworks-color.svg", exact: ["fireworks-ai", "firepass", "fireworks"] },
  { asset: "mistral-color.svg", exact: ["mistral"] },
  { asset: "cohere-color.svg", exact: ["cohere"] },
  { asset: "perplexity-color.svg", exact: ["perplexity", "perplexity-agent"] },
  { asset: "huggingface-color.svg", exact: ["huggingface"] },
  { asset: "nvidia-color.svg", exact: ["nvidia"] },
  { asset: "githubcopilot.svg", exact: ["github-copilot"] },
  { asset: "github.svg", exact: ["github", "github-models"] },
  { asset: "vercel.svg", exact: ["vercel", "vercel-ai-gateway"] },
  { asset: "cloudflare-color.svg", exact: ["cloudflare", "cloudflare-ai-gateway", "cloudflare-workers-ai"] },
  { asset: "ollama.svg", exact: ["ollama", "ollama-cloud"] },
  { asset: "lmstudio.svg", exact: ["lmstudio", "lm-studio"] },
  { asset: "vllm-color.svg", exact: ["vllm"] },
  { asset: "cursor.svg", exact: ["cursor"] },
  { asset: "dify.svg", exact: ["dify"] },
  { asset: "stepfun-color.svg", exact: ["stepfun", "stepfun-global", "stepfun-plan", "stepfun-ai"], prefixes: ["stepfun-"] },
  { asset: "baichuan-color.svg", exact: ["baichuan"] },
  { asset: "baidu-color.svg", exact: ["baidu"], prefixes: ["baidu-"] },
  { asset: "tencent-color.svg", exact: ["tencent"], prefixes: ["tencent-"] },
  { asset: "qiniu-color.svg", exact: ["qiniu", "qiniu-ai"] },
  { asset: "snowflake-color.svg", exact: ["snowflake", "snowflake-cortex"] },
  { asset: "aws-color.svg", exact: ["aws"] },
  { asset: "meta-color.svg", exact: ["meta"] },
  { asset: "llama.svg", exact: ["llama"] },
];

export function providerBrandIconSource(entry: Pick<ProviderCatalogEntry, "id" | "name" | "display_name" | "type">) {
  const values = [entry.id, entry.name, entry.display_name, entry.type].map(normalizeProviderKey).filter(Boolean);
  const rule = values.map((value) => providerIconRules.find((candidate) => candidate.exact.includes(value) || candidate.prefixes?.some((prefix) => value.startsWith(prefix)))).find(Boolean);
  return rule ? `/provider-icons/${rule.asset}` : "";
}

function normalizeProviderKey(value: string | undefined) {
  return (value || "").trim().toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
}
