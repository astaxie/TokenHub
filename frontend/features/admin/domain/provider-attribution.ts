export const claudeCodeAttributionPreserve = "preserve";
export const claudeCodeAttributionStrip = "strip";

type ProviderAttributionOption = {
  value: string;
  claudeCodeAttributionDefault?: string;
};

export function defaultProviderClaudeCodeAttributionPolicy(providerType?: string, _catalogID?: string, providerTypeOptions: ProviderAttributionOption[] = []) {
  const pluginDefault = normalizeClaudeCodeAttributionPolicy(providerTypeOptions.find((option) => option.value === providerType)?.claudeCodeAttributionDefault);
  return pluginDefault || claudeCodeAttributionStrip;
}

function normalizeClaudeCodeAttributionPolicy(value?: string) {
  const policy = value?.trim().toLowerCase() ?? "";
  return policy === claudeCodeAttributionPreserve || policy === claudeCodeAttributionStrip ? policy : "";
}
