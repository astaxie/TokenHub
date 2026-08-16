import { type ModelRoute, type ProviderResource } from "../core/types";

export const codexImageModelName = "codex-gpt-image-2";
export const codexImageUpstreamModel = "gpt-image-2";

export function codexImageRouteEnabled(routes: ModelRoute[], providerID: string) {
  return routes.some((route) =>
    route.model_name === codexImageModelName &&
    route.provider_id === providerID &&
    route.provider_model === codexImageUpstreamModel &&
    route.status === "active",
  );
}

export function codexImageResources(resources: ProviderResource[], providerID: string) {
  return resources.filter((resource) =>
    resource.provider_id === providerID && resource.resource_type === "openai_subscription",
  );
}

export function defaultCodexImageResourceID(resources: ProviderResource[], providerID: string, selectedAccountID: string) {
  const candidates = codexImageResources(resources, providerID);
  const available = candidates.filter((resource) => resource.status === "active" && resource.healthy !== false);
  if (selectedAccountID !== "all" && available.some((resource) => resource.id === selectedAccountID)) return selectedAccountID;
  return available.find((resource) => resource.options?.image_generation_capability === "supported")?.id ?? available[0]?.id ?? "";
}

export function codexImageModelState(resources: ProviderResource[], providerID: string, routeEnabled: boolean) {
  const candidates = codexImageResources(resources, providerID);
  const available = candidates.filter((resource) => resource.status === "active" && resource.healthy !== false);
  const supported = available.filter((resource) => resource.options?.image_generation_capability === "supported").length;
  const unsupported = available.filter((resource) => resource.options?.image_generation_capability === "unsupported").length;
  if (routeEnabled && supported > 0) return "enabled";
  if (routeEnabled) return "enabled_without_account";
  if (available.length > 0 && unsupported === available.length) return "unsupported";
  if (supported > 0) return "tested_disabled";
  return "untested";
}
