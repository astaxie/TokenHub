import type { ProviderCatalogEntry } from "../../../features/admin/core/types";
import iconManifest from "../../../public/provider-icons/subscription-icons.provenance.json";
import inventory from "../../../../tools/magpie-subscription-coverage.json";

const nativeCatalogIDs: Record<string, string> = {
  "chatgpt-api": "chatgpt-subscription", claude: "claude-subscription", codex: "openai-codex",
  copilot: "github-copilot-subscription", gemini: "gemini-cli",
};
const names: Record<string, string> = {
  antigravity: "Antigravity", "chatgpt-api": "ChatGPT API", claude: "Claude Code Subscription",
  codex: "OpenAI Codex", copilot: "GitHub Copilot Subscription", gemini: "Gemini CLI",
  autoclaw: "AutoClaw", "autoclaw-cn": "AutoClaw China", cline: "Cline", codearts: "CodeArts",
  "commandcode-plan": "Command Code", cursor: "Cursor", devin: "Devin", factory: "Factory",
  "gcp-ai": "GCP AI", grok: "Grok", joycode: "JoyCode", "kimi-work": "Kimi Work", kiro: "Kiro",
  "mimo-app": "MiMo", "minimax-code": "MiniMax Coding Plan", "minimax-code-global": "MiniMax Coding Plan Global",
  "nous-portal": "Nous Portal", opendesign: "OpenDesign", qoder: "Qoder", "qoder-cn": "Qoder China",
  qwenwork: "QwenWork", "qwenwork-enterprise": "QwenWork Enterprise", raccoon: "Raccoon",
  "trae-cn": "Trae China", "trae-global": "Trae Global", workbuddy: "WorkBuddy",
  "workbuddy-ai": "WorkBuddy AI", zcode: "ZCode", zed: "Zed",
};

export const subscriptionBrandIcons = inventory.providers.map(provider => {
  const icon = iconManifest.providers.find(item => item.upstream_id === provider.upstream_id);
  const name = names[provider.upstream_id];
  if (!icon || !name) throw new Error(`Missing subscription icon fixture: ${provider.upstream_id}`);
  const id = nativeCatalogIDs[provider.upstream_id] ?? provider.upstream_id;
  const entry: ProviderCatalogEntry = {
    id, name, display_name: name, type: provider.tokenhub.provider_type,
    base_url: `https://account.example.test/${id}`, models_count: 0, source: "ui-fixture",
  };
  return { entry, source: `/provider-icons/${icon.file}` };
});
