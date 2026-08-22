import { type AppData, type Model, type ModelRoute } from "../core/types";
import { modelCategory } from "./catalog";
import { findProvider, modelIsInDirectory, modelRoutesFor } from "./entities";
import { modelDisplayName } from "./model-display-name";

export type ModelPublicationState = "published" | "draft" | "disabled";
export type ModelRuntimeState = "healthy" | "degraded" | "unavailable" | "unmapped";

export function modelPublicationState(model: Model, data: AppData): ModelPublicationState {
  if (model.status !== "active") return "disabled";
  return modelRoutesFor(model, data).some((route) => route.status === "active") ? "published" : "draft";
}

export function modelRuntimeState(model: Model, data: AppData): ModelRuntimeState {
  const activeRoutes = modelRoutesFor(model, data).filter((route) => route.status === "active");
  if (activeRoutes.length === 0) return "unmapped";
  const healthy = activeRoutes.filter((route) => {
    const provider = findProvider(data, route.provider_id);
    if (!provider || provider.status !== "active" || !provider.healthy) return false;
    if (!route.provider_resource_id) return true;
    const resource = data.providerResources.find((item) => item.id === route.provider_resource_id);
    return Boolean(resource && resource.status === "active" && resource.healthy);
  }).length;
  if (healthy === 0) return "unavailable";
  return healthy === activeRoutes.length ? "healthy" : "degraded";
}

export function externalModels(data: AppData, readOnly = false) {
  if (readOnly) return data.models;
  return data.models.filter((model) => modelIsInDirectory(model, data));
}

export function filterExternalModels(
  models: Model[],
  data: AppData,
  publication: "all" | ModelPublicationState,
  query: string,
  providerID: string,
) {
  const normalized = query.trim().toLowerCase();
  return models
    .filter((model) => publication === "all" || modelPublicationState(model, data) === publication)
    .filter((model) => {
      const routes = modelRoutesFor(model, data);
      return !providerID || routes.some((route) => route.provider_id === providerID);
    })
    .filter((model) => !normalized || modelDirectorySearchText(model, modelRoutesFor(model, data), data).includes(normalized))
    .sort((left, right) => {
      const stateRank = (model: Model) => ({ unavailable: 0, degraded: 1, healthy: 2, unmapped: 3 }[modelRuntimeState(model, data)]);
      return stateRank(left) - stateRank(right) || left.name.localeCompare(right.name);
    });
}

export function modelDirectorySearchText(model: Model, routes: ModelRoute[], data: AppData) {
  return [
    model.name,
    modelDisplayName(model.metadata, ""),
    model.id,
    model.family,
    model.modality,
    modelCategory(model),
    ...(model.capabilities ?? []),
    ...routes.flatMap((route) => [route.provider_model, route.provider_id, findProvider(data, route.provider_id)?.name]),
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

export function isCustomModelAlias(model: Model, routes: ModelRoute[]) {
  if (routes.length === 0) return false;
  return routes.some((route) => normalizeModelName(route.provider_model) !== normalizeModelName(model.name));
}

function normalizeModelName(value: string) {
  const slash = value.lastIndexOf("/");
  const normalized = slash >= 0 ? value.slice(slash + 1) : value;
  return normalized.trim().toLowerCase().replaceAll("_", "-");
}
