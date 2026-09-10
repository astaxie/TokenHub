type ProviderReasoningPolicyOption = {
  value: string;
  routeProtocols?: string[];
  reasoningConfigurable?: boolean;
};

export function providerTypeSupportsReasoningConfig(providerTypeOptions: ProviderReasoningPolicyOption[], providerType?: string) {
  const normalizedType = providerType?.trim().toLowerCase() ?? "";
  const option = providerTypeOptions.find((item) => item.value.trim().toLowerCase() === normalizedType);
  if (typeof option?.reasoningConfigurable === "boolean") return option.reasoningConfigurable;
  return option?.routeProtocols?.some((protocol) => protocol.trim().toLowerCase() === "chat/completions") ?? false;
}
