const legacyAnthropicReasoningProviderTypes = new Set([
  "openai",
  "openai_compatible",
  "azure_openai",
  "deepseek",
  "qwen",
  "local",
]);

type ProviderReasoningPolicyOption = {
  value: string;
  routeProtocols?: string[];
};

export function providerSupportsAnthropicReasoning(providerType?: string) {
  return legacyAnthropicReasoningProviderTypes.has(providerType?.trim().toLowerCase() ?? "");
}

export function providerTypeOptionsSupportAnthropicReasoning(providerTypeOptions: ProviderReasoningPolicyOption[], providerType?: string) {
  const normalizedType = providerType?.trim().toLowerCase() ?? "";
  const option = providerTypeOptions.find((item) => item.value.trim().toLowerCase() === normalizedType);
  if (option?.routeProtocols?.length) {
    return option.routeProtocols.some((protocol) => protocol.trim().toLowerCase() === "chat/completions");
  }
  return providerSupportsAnthropicReasoning(providerType);
}
