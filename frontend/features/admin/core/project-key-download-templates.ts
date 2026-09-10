import { type APIKey, type AppData } from "./types";
import { apiGatewayBaseURL } from "../domain/formatting";

export type ProjectKeyDownloadTemplate = {
  id: "codex_cli_config" | "environment";
  menuLabel: string;
  menuSubtitle: string;
  filenameSuffix: string;
  render: (item: APIKey, data: AppData, apiBaseURL: string) => string;
};

export const projectKeyDownloadTemplates = [
  {
    id: "codex_cli_config",
    menuLabel: "Codex CLI 配置",
    menuSubtitle: "config.toml · 连接 TokenHub Responses",
    filenameSuffix: "-codex-config.toml",
    render: (item, data, apiBaseURL) => codexConfigTemplate(item, data, apiBaseURL),
  },
  {
    id: "environment",
    menuLabel: "环境变量模板",
    menuSubtitle: ".env · 替换 Key 占位符",
    filenameSuffix: ".env",
    render: (item) => tokenHubEnvironmentTemplate(item),
  },
] as const satisfies readonly ProjectKeyDownloadTemplate[];

export const codexFingerprintModeOptions = [
  { value: "off", label: "关闭（透传）" },
  { value: "device", label: "仅收敛设备" },
  { value: "session", label: "收敛设备与会话（推荐）" },
  { value: "full", label: "完全收敛" },
] as const satisfies readonly { value: string; label: string }[];

export function codexFingerprintModeLabel(mode: string) {
  return codexFingerprintModeOptions.find((option) => option.value === mode)?.label ?? mode;
}

export function projectKeyDownloadFilename(name: string, template: ProjectKeyDownloadTemplate) {
  return `${templateFilename(name)}${template.filenameSuffix}`;
}

function codexConfigTemplate(item: APIKey, data: AppData, apiBaseURL: string) {
  const model = codexTemplateModel(item, data);
  const baseURL = apiGatewayBaseURL(apiBaseURL);
  return `# TokenHub Codex CLI configuration for ${item.name}
# Set the API key before starting Codex:
# export TOKENHUB_API_KEY="REPLACE_WITH_YOUR_TOKENHUB_API_KEY"

model = "${escapeTomlString(model)}"
model_provider = "tokenhub"
model_reasoning_effort = "medium"

[model_providers.tokenhub]
name = "TokenHub - ${escapeTomlString(item.name)}"
base_url = "${escapeTomlString(baseURL)}"
wire_api = "responses"
env_key = "TOKENHUB_API_KEY"
env_key_instructions = "Set TOKENHUB_API_KEY to REPLACE_WITH_YOUR_TOKENHUB_API_KEY"
`;
}

function tokenHubEnvironmentTemplate(item: APIKey) {
  return `# TokenHub API Key for ${item.name}
TOKENHUB_API_KEY=REPLACE_WITH_YOUR_TOKENHUB_API_KEY
`;
}

function codexTemplateModel(item: APIKey, data: AppData) {
  const activeModels = new Set(data.models.filter((model) => model.status === "active").map((model) => model.name));
  const routedModels = data.routes
    .filter((route) => route.status === "active" && activeModels.has(route.model_name))
    .map((route) => route.model_name);
  const allowedModels = (item.allowed_models ?? []).filter(Boolean);
  const candidates = allowedModels.length > 0 ? allowedModels.filter((model) => routedModels.includes(model)) : routedModels;
  return candidates[0] ?? allowedModels[0] ?? "REPLACE_WITH_MODEL_ID_FROM_V1_MODELS";
}

function escapeTomlString(value: string) {
  return value.replaceAll("\\", "\\\\").replaceAll("\"", "\\\"");
}

function templateFilename(value: string) {
  const normalized = value.trim().toLowerCase().replace(/[^a-z0-9._-]+/g, "-").replace(/^-+|-+$/g, "");
  return normalized || "tokenhub-key";
}
