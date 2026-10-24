// Preset facts adapted from yetone/magpie, MIT, Copyright (c) 2026 yetone.
// See tools/licenses/magpie-LICENSE for the complete license.
export const magpieCommit = "d7ad3f2a3eea4fadc6f7131bf8b0957024892ba1";
export const magpiePresetSource = `https://github.com/yetone/magpie/blob/${magpieCommit}/internal/provider/presets.go`;

const qianfan = ["qianfan-code-latest", "deepseek-v4.1-flash", "deepseek-v4-pro", "deepseek-v4-pro-0813", "deepseek-v4-flash", "deepseek-v4-flash-0731", "deepseek-v3.2", "glm-5.3", "glm-5.3-flash", "glm-5.2", "glm-5.1", "glm-5"];
const ark = ["ark-code-latest", "doubao-seed-evolving", "doubao-seed-2.1-pro", "doubao-seed-2.1-lite", "doubao-seed-2.1-turbo", "doubao-seed-2.0-mini", "minimax-m3", "glm-5.3", "glm-5.3-flash", "deepseek-v4.1-flash", "deepseek-v4-flash", "deepseek-v4-pro", "kimi-k2.7-code", "kimi-k2.8-preview", "kimi-k3"];

// These are seed identities and protocol endpoints, not a promise of account
// entitlement, context limits, reasoning support, or zero-cost inference.
export const magpieProviderPresets = [
  {
    id: "baidu-qianfan-personal", name: "Baidu Qianfan Token Plan Personal",
    api: "https://qianfan.baidubce.com/v2/tokenplan/personal",
    doc: "https://cloud.baidu.com/doc/qianfan/s/Dmrabu8b6",
    models: qianfan.filter(id => !["deepseek-v4-flash", "deepseek-v3.2", "glm-5"].includes(id)),
    discovery: "seed", billing: "subscription", responses: true,
  },
  {
    id: "baidu-qianfan-team", name: "Baidu Qianfan Token Plan Enterprise",
    api: "https://qianfan.baidubce.com/v2/tokenplan/team",
    doc: "https://cloud.baidu.com/doc/qianfan/s/Dmrabu8b6",
    models: qianfan, discovery: "seed", billing: "subscription", responses: true,
  },
  {
    id: "huawei-maas-token-plan", name: "Huawei Cloud MaaS Token Plan",
    api: "https://api.modelarts-maas.com/plan/v2",
    doc: "https://support.huaweicloud.com/Token-plan-maas/tokenplan-maas-0001.html",
    models: ["glm-5.3", "glm-5.1", "kimi-k2.6", "deepseek-v4.1-flash", "deepseek-v4-flash"],
    discovery: "seed", billing: "subscription",
  },
  {
    id: "volcengine-coding-plan", name: "Volcengine Ark Coding Plan",
    api: "https://ark.cn-beijing.volces.com/api/coding/v3",
    doc: "https://www.volcengine.com/docs/82379/1925114",
    models: ark, discovery: "seed", billing: "subscription", responses: true,
  },
  {
    id: "volcengine-agent-plan", name: "Volcengine Ark Agent Plan",
    api: "https://ark.cn-beijing.volces.com/api/plan/v3",
    doc: "https://www.volcengine.com/docs/82379/1925114",
    models: ark, discovery: "seed", billing: "subscription", responses: true,
  },
  {
    id: "qwen-token-plan", name: "Qwen AI Token Plan",
    api: "https://token-plan.maas.qianwenaiapi.com/compatible-mode/v1",
    doc: "https://platform.qianwenai.com/docs/token-plan/overview",
    models: ["auto", "qwen3.8-max", "qwen3.8-flash", "qwen3.7-max", "qwen3.7-plus", "qwen3.6-flash", "deepseek-v4.1-flash", "deepseek-v4-pro", "deepseek-v4-pro-0813", "deepseek-v4-flash-0731", "glm-5.3", "glm-5.2"],
    discovery: "seed", billing: "subscription",
  },
  {
    id: "kimi-code-global", name: "Kimi Code (Global)",
    api: "https://api.kimi.ai/coding/v1",
    doc: "https://www.kimi.com/code/docs/en/third-party-tools/other-coding-agents.html",
    models: [], discovery: "account", billing: "subscription",
  },
  {
    id: "tencent-token-plan-enterprise-intl", name: "Tencent Cloud Token Plan Enterprise Global",
    api: "https://tokenhub-intl.tencentmaas.com/plan/v3",
    doc: "https://cloud.tencent.com/document/product/1823/130659",
    models: ["auto", "hy4-preview", "glm-5.3", "glm-5.3-flash", "glm-5.2", "kimi-k3", "kimi-k2.7-code", "kimi-k2.7-code-highspeed", "minimax-m3", "deepseek-v4.1-flash", "deepseek-v4-pro", "deepseek-v4-pro-0813", "deepseek-v4-flash-0731", "deepseek-v4-pro-202606", "deepseek-v4-flash-202605", "mimo-v2.6-pro", "mimo-v2.6-flash"],
    discovery: "seed", billing: "subscription", responses: true,
  },
  {
    id: "clinepass", name: "ClinePass",
    api: "https://api.cline.bot/api/v1",
    doc: "https://docs.cline.bot/getting-started/clinepass",
    models: ["cline-pass/glm-5.3", "cline-pass/glm-5.3-flash", "cline-pass/kimi-k3", "cline-pass/deepseek-v4-pro", "cline-pass/deepseek-v4.1-flash", "cline-pass/mimo-v2.5", "cline-pass/mimo-v2.5-pro", "cline-pass/minimax-m3", "cline-pass/muse-spark-1.3-contributor", "cline-pass/qwen3.8-max", "cline-pass/qwen3.7-max", "cline-pass/qwen3.7-plus"],
    discovery: "seed", billing: "subscription",
  },
  {
    id: "ollama-cloud", name: "Ollama Cloud (Account Models)",
    api: "https://ollama.com/v1", doc: "https://docs.ollama.com/cloud",
    models: [], discovery: "account", billing: "subscription",
  },
  {
    id: "commandcode", name: "Command Code API",
    api: "https://api.commandcode.ai/provider/v1", doc: "https://commandcode.ai/docs/provider",
    models: [], discovery: "account", billing: "account", responses: true,
  },
  {
    id: "nous", name: "Nous Portal API",
    api: "https://inference-api.nousresearch.com/v1", doc: "https://portal.nousresearch.com",
    models: [], discovery: "account", billing: "account",
  },
];

export function magpieCatalogEntry(preset) {
  const endpoints = preset.responses ? "chat/completions,responses" : "chat/completions";
  const provenance = {
    upstream_source: preset.doc, inventory_source: magpiePresetSource, upstream_commit: magpieCommit,
    catalog_reviewed_at: "2026-10-24", pricing_status: "unverified", billing_mode: preset.billing,
    pricing_note: "Account plan terms and current prices must be configured before publishing; omitted prices do not mean free inference.",
  };
  return {
    id: preset.id, type: "openai_compatible", api: preset.api, name: preset.name, display_name: preset.name, doc: preset.doc,
    metadata: { ...provenance, model_discovery: preset.discovery },
    models: preset.models.map(id => ({
      id, name: id, display_name: id, type: "chat", endpoints, billing_mode: preset.billing,
      modalities: { input: ["text"], output: ["text"] },
      metadata: { ...provenance, limits_status: "unverified", entitlement_status: "seed: verify against the connected account" },
    })),
  };
}
