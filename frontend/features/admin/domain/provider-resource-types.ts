export const providerResourceAPIKeyType = "api_key";
export const providerResourceOpenAISubscriptionType = "openai_subscription";
export const providerResourceTypeCapabilityKind = "provider_resource_type";

type ProviderResourceTypeLike = {
  resource_type?: string;
};

export function isOpenAISubscriptionResourceType(resourceType: string | undefined) {
  return resourceType === providerResourceOpenAISubscriptionType;
}

export function isOpenAISubscriptionResource(resource: ProviderResourceTypeLike) {
  return isOpenAISubscriptionResourceType(resource.resource_type);
}

export function providerResourceTypeOptionOrder(value: string) {
  if (value === providerResourceOpenAISubscriptionType) return 0;
  if (value === providerResourceAPIKeyType) return 1;
  return 2;
}
