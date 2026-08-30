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

export function providerSystemPromptTransformPolicy(values: Record<string, string>) {
  return values.system_prompt_transform_policy || values.claude_code_attribution_policy || "";
}

function normalizeSystemPromptTransformPolicy(value?: string) {
  const policy = value?.trim().toLowerCase() ?? "";
  return policy === systemPromptTransformPreserve || policy === systemPromptTransformStrip ? policy : "";
}
