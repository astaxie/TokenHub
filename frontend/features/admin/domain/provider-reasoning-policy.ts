type ProviderReasoningPolicyOption = {
  value: string;
  routeProtocols?: string[];
};

export function providerTypeOptionsSupportAnthropicReasoning(providerTypeOptions: ProviderReasoningPolicyOption[], providerType?: string) {
  const normalizedType = providerType?.trim().toLowerCase() ?? "";
  const option = providerTypeOptions.find((item) => item.value.trim().toLowerCase() === normalizedType);
  return option?.routeProtocols?.some((protocol) => protocol.trim().toLowerCase() === "chat/completions") ?? false;
}
