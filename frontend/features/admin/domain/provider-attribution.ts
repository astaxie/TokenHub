export const systemPromptTransformPreserve = "preserve";
export const systemPromptTransformStrip = "strip";

type ProviderSystemPromptTransformOption = {
  value: string;
  systemPromptTransformDefault?: string;
  claudeCodeAttributionDefault?: string;
};

export function defaultProviderSystemPromptTransformPolicy(providerType?: string, _catalogID?: string, providerTypeOptions: ProviderSystemPromptTransformOption[] = []) {
  const option = providerTypeOptions.find((item) => item.value === providerType);
  const pluginDefault = normalizeSystemPromptTransformPolicy(option?.systemPromptTransformDefault ?? option?.claudeCodeAttributionDefault);
  return pluginDefault || systemPromptTransformStrip;
}

function normalizeSystemPromptTransformPolicy(value?: string) {
  const policy = value?.trim().toLowerCase() ?? "";
  return policy === systemPromptTransformPreserve || policy === systemPromptTransformStrip ? policy : "";
}
