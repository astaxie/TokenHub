export const claudeCodeAttributionPreserve = "preserve";
export const claudeCodeAttributionStrip = "strip";

type ProviderAttributionOption = {
  value: string;
  claudeCodeAttributionDefault?: string;
};

export function defaultProviderClaudeCodeAttributionPolicy(providerType?: string, catalogID?: string, providerTypeOptions: ProviderAttributionOption[] = []) {
  const pluginDefault = normalizeClaudeCodeAttributionPolicy(providerTypeOptions.find((option) => option.value === providerType)?.claudeCodeAttributionDefault);
  if (pluginDefault) return pluginDefault;
  if (providerType?.trim() !== "anthropic") return claudeCodeAttributionStrip;
  const normalizedCatalogID = catalogID?.trim().toLowerCase() ?? "";
  if (!normalizedCatalogID || normalizedCatalogID === "custom" || normalizedCatalogID === "anthropic") {
    return claudeCodeAttributionPreserve;
  }
  return claudeCodeAttributionStrip;
}

function normalizeClaudeCodeAttributionPolicy(value?: string) {
  const policy = value?.trim().toLowerCase() ?? "";
  return policy === claudeCodeAttributionPreserve || policy === claudeCodeAttributionStrip ? policy : "";
}
