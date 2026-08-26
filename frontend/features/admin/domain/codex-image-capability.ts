import { type AdminUIContribution, type ModelRoute, type PluginActionDescriptor, type ProviderResource } from "../core/types";
import { codexImageModelName, codexImageUpstreamModel } from "./codex-provider-profile";
import { isProviderAccountResource } from "./provider-resource-types";

export type ImageCapabilityProfile = {
  displayName: string;
  publicModel: string;
  upstreamModel: string;
  resourceType?: string;
};

const defaultImageCapabilityProfile: ImageCapabilityProfile = {
  displayName: "订阅生图",
  publicModel: codexImageModelName,
  upstreamModel: codexImageUpstreamModel,
};

export function imageCapabilityProfileFromAction(action?: Pick<PluginActionDescriptor, "metadata" | "title"> | null): ImageCapabilityProfile | null {
  if (!action) return null;
  return {
    ...defaultImageCapabilityProfile,
    displayName: action.metadata?.display_name?.trim() || action.title?.trim() || defaultImageCapabilityProfile.displayName,
    publicModel: action.metadata?.public_model?.trim() || defaultImageCapabilityProfile.publicModel,
    upstreamModel: action.metadata?.upstream_model?.trim() || defaultImageCapabilityProfile.upstreamModel,
    resourceType: action.metadata?.provider_resource_type?.trim() || action.metadata?.resource_type?.trim() || undefined,
  };
}

export function providerImageCapabilityContribution(contributions: AdminUIContribution[], providerType: string, action?: PluginActionDescriptor) {
  if (!action) return undefined;
  return contributions.find((contribution) =>
    contribution.slot === "provider.model.panel" &&
    adminUIContributionLayout(contribution) === "image_capability" &&
    contribution.action === action.action_id &&
    contribution.plugin_id === action.plugin_id &&
    (!contribution.provider_types?.length || contribution.provider_types.includes(providerType)),
  );
}

export function providerImageCapabilityProfile(contributions: AdminUIContribution[], actions: PluginActionDescriptor[], providerType: string) {
  const action = actions.find((item) => item.capability === "image.capability.configure" && (!item.subject || item.subject === providerType));
  return providerImageCapabilityContribution(contributions, providerType, action) ? imageCapabilityProfileFromAction(action) : null;
}

function adminUIContributionLayout(contribution: AdminUIContribution) {
  const layout = contribution.schema?.layout;
  return typeof layout === "string" ? layout.trim() : "";
}

export function codexImageRouteEnabled(routes: ModelRoute[], providerID: string, profile?: ImageCapabilityProfile | null) {
  const imageProfile = profile ?? defaultImageCapabilityProfile;
  return routes.some((route) =>
    route.model_name === imageProfile?.publicModel &&
    route.provider_id === providerID &&
    route.provider_model === imageProfile?.upstreamModel &&
    route.status === "active",
  );
}

export function codexImageResources(resources: ProviderResource[], providerID: string, profile?: ImageCapabilityProfile | null) {
  return resources.filter((resource) =>
    resource.provider_id === providerID &&
    (profile?.resourceType ? resource.resource_type === profile.resourceType : isProviderAccountResource(resource)),
  );
}

export function defaultCodexImageResourceID(resources: ProviderResource[], providerID: string, selectedAccountID: string, profile?: ImageCapabilityProfile | null) {
  const candidates = codexImageResources(resources, providerID, profile);
  const available = candidates.filter((resource) => resource.status === "active" && resource.healthy !== false);
  if (selectedAccountID !== "all" && available.some((resource) => resource.id === selectedAccountID)) return selectedAccountID;
  return available.find((resource) => resource.options?.image_generation_capability === "supported")?.id ?? available[0]?.id ?? "";
}

export function codexImageModelState(resources: ProviderResource[], providerID: string, routeEnabled: boolean, profile?: ImageCapabilityProfile | null) {
  const candidates = codexImageResources(resources, providerID, profile);
  const available = candidates.filter((resource) => resource.status === "active" && resource.healthy !== false);
  const supported = available.filter((resource) => resource.options?.image_generation_capability === "supported").length;
  const unsupported = available.filter((resource) => resource.options?.image_generation_capability === "unsupported").length;
  if (routeEnabled && supported > 0) return "enabled";
  if (routeEnabled) return "enabled_without_account";
  if (available.length > 0 && unsupported === available.length) return "unsupported";
  if (supported > 0) return "tested_disabled";
  return "untested";
}
