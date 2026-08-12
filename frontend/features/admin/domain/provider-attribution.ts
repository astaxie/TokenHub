export const claudeCodeAttributionPreserve = "preserve";
export const claudeCodeAttributionStrip = "strip";

export function defaultProviderClaudeCodeAttributionPolicy(providerType?: string, catalogID?: string) {
  if (providerType?.trim() !== "anthropic") return claudeCodeAttributionStrip;
  const normalizedCatalogID = catalogID?.trim().toLowerCase() ?? "";
  if (!normalizedCatalogID || normalizedCatalogID === "custom" || normalizedCatalogID === "anthropic") {
    return claudeCodeAttributionPreserve;
  }
  return claudeCodeAttributionStrip;
}
