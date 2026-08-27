import { type AdminUIContribution, type ModelRoute, type PluginActionDescriptor, type ProviderResource } from "../core/types";
import { isProviderAccountResource } from "./provider-resource-types";

export type ImageCapabilityProfile = {
  displayName: string;
  publicModel: string;
  upstreamModel: string;
  resourceType?: string;
  capabilityOption: string;
  capabilityCheckedAtOption: string;
  capabilitySupportedValue: string;
  capabilityUnsupportedValue: string;
  routeBackfillOption: string;
  routeBackfillValue: string;
};

const defaultImageCapabilityDisplayName = "订阅生图";

export function imageCapabilityProfileFromAction(action?: Pick<PluginActionDescriptor, "metadata" | "title"> | null): ImageCapabilityProfile | null {
  if (!action) return null;
  const publicModel = action.metadata?.public_model?.trim() ?? "";
  const upstreamModel = action.metadata?.upstream_model?.trim() ?? "";
  if (!publicModel || !upstreamModel) return null;
  return {
    displayName: action.metadata?.display_name?.trim() || action.title?.trim() || defaultImageCapabilityDisplayName,
    publicModel,
    upstreamModel,
    resourceType: action.metadata?.provider_resource_type?.trim() || action.metadata?.resource_type?.trim() || undefined,
    capabilityOption: action.metadata?.capability_option?.trim() || action.metadata?.provider_capability_option?.trim() || "image_capability",
    capabilityCheckedAtOption: action.metadata?.capability_checked_at_option?.trim() || action.metadata?.provider_capability_checked_at_option?.trim() || "image_capability_checked_at",
    capabilitySupportedValue: action.metadata?.capability_supported_value?.trim() || action.metadata?.supported_value?.trim() || "supported",
    capabilityUnsupportedValue: action.metadata?.capability_unsupported_value?.trim() || action.metadata?.unsupported_value?.trim() || "unsupported",
    routeBackfillOption: action.metadata?.route_backfill_option?.trim() || action.metadata?.provider_route_backfill_option?.trim() || "image_capability_route_backfill_v1",
    routeBackfillValue: action.metadata?.route_backfill_value?.trim() || action.metadata?.provider_route_backfill_value?.trim() || "completed",
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
  return providerImageCapabilityProfiles(contributions, actions, providerType)[0] ?? null;
}

export function providerImageCapabilityProfiles(contributions: AdminUIContribution[], actions: PluginActionDescriptor[], providerType: string) {
  return actions
    .filter((item) => item.capability === "image.capability.configure" && (!item.subject || item.subject === providerType))
    .filter((action) => providerImageCapabilityContribution(contributions, providerType, action))
    .map((action) => imageCapabilityProfileFromAction(action))
    .filter((profile): profile is ImageCapabilityProfile => Boolean(profile));
}

export function imageCapabilityProfileForModel(profiles: ImageCapabilityProfile[], modelName: string) {
  const normalizedModelName = modelName.trim();
  return profiles.find((profile) => profile.publicModel === normalizedModelName) ?? null;
}

function adminUIContributionLayout(contribution: AdminUIContribution) {
  const layout = contribution.schema?.layout;
  return typeof layout === "string" ? layout.trim() : "";
}

export function providerImageRouteEnabled(routes: ModelRoute[], providerID: string, profile?: ImageCapabilityProfile | null) {
  if (!profile) return false;
  return routes.some((route) =>
    route.model_name === profile.publicModel &&
    route.provider_id === providerID &&
    route.provider_model === profile.upstreamModel &&
    route.status === "active",
  );
}

export function providerImageCapabilityResources(resources: ProviderResource[], providerID: string, profile?: ImageCapabilityProfile | null) {
  if (!profile) return [];
  return resources.filter((resource) =>
    resource.provider_id === providerID &&
    (profile?.resourceType ? resource.resource_type === profile.resourceType : isProviderAccountResource(resource)),
  );
}

export function defaultProviderImageCapabilityResourceID(resources: ProviderResource[], providerID: string, selectedAccountID: string, profile?: ImageCapabilityProfile | null) {
  if (!profile) return "";
  const candidates = providerImageCapabilityResources(resources, providerID, profile);
  const available = candidates.filter((resource) => resource.status === "active" && resource.healthy !== false);
  if (selectedAccountID !== "all" && available.some((resource) => resource.id === selectedAccountID)) return selectedAccountID;
  return available.find((resource) => resource.options?.[profile.capabilityOption] === profile.capabilitySupportedValue)?.id ?? available[0]?.id ?? "";
}

export function providerImageCapabilityState(resources: ProviderResource[], providerID: string, routeEnabled: boolean, profile?: ImageCapabilityProfile | null) {
  if (!profile) return "untested";
  const candidates = providerImageCapabilityResources(resources, providerID, profile);
  const available = candidates.filter((resource) => resource.status === "active" && resource.healthy !== false);
  const supported = available.filter((resource) => resource.options?.[profile.capabilityOption] === profile.capabilitySupportedValue).length;
  const unsupported = available.filter((resource) => resource.options?.[profile.capabilityOption] === profile.capabilityUnsupportedValue).length;
  if (routeEnabled && supported > 0) return "enabled";
  if (routeEnabled) return "enabled_without_account";
  if (available.length > 0 && unsupported === available.length) return "unsupported";
  if (supported > 0) return "tested_disabled";
  return "untested";
}
