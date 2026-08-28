import { type PluginActionDescriptor, type ProviderCatalogEntry } from "../core/types";

type ProviderAuthModeOption = {
  value: string;
  authModes?: string[];
  apiKeyRequired?: boolean;
  defaultCatalogProviderType?: boolean;
};

const legacyProviderTypeFallback = "openai_compatible";
const providerModelsPreviewCapability = "models.preview";

export function defaultProviderTypeValue(providerTypeOptions: ProviderAuthModeOption[] = []) {
  return providerTypeOptions.find((option) => option.defaultCatalogProviderType)?.value ??
    providerTypeOptions.find((option) => option.value === legacyProviderTypeFallback)?.value ??
    providerTypeOptions[0]?.value ??
    legacyProviderTypeFallback;
}

export function providerTypeValue(values: Record<string, string>, providerTypeOptions: ProviderAuthModeOption[] = []) {
  return values.type || defaultProviderTypeValue(providerTypeOptions);
}

export function providerAuthMode(values: Record<string, string>, providerTypeOptions: ProviderAuthModeOption[] = []) {
  const providerType = providerTypeValue(values, providerTypeOptions);
  const modes = providerAuthModes(providerTypeOptions, providerType);
  if (modes.length === 0) return "";
  const configured = values.anthropic_auth_type?.trim();
  if (configured && modes.includes(configured)) return configured;
  return preferredProviderAuthMode(modes);
}

const providerConnectionTestFields = new Set(["base_url", "api_key", "type", "anthropic_auth_type", "custom_headers"]);

export function providerConnectionTestRunAfterUpdate(currentRun: number, key: string) {
  return providerConnectionTestFields.has(key) ? currentRun + 1 : currentRun;
}

export function providerCatalogModelsPreviewAction(entry: ProviderCatalogEntry | undefined, actions: PluginActionDescriptor[] = []) {
  if (!entry?.type) return undefined;
  return actions.find((action) => action.capability === providerModelsPreviewCapability && action.subject === entry.type);
}

export function providerCatalogSupportsModelPreview(entry: ProviderCatalogEntry | undefined, actions: PluginActionDescriptor[] = []) {
  return Boolean(providerCatalogModelsPreviewAction(entry, actions));
}

export function providerCatalogUsesDiscoveryPreview(catalogID: string, entry: ProviderCatalogEntry | undefined, actions: PluginActionDescriptor[] = []) {
  return catalogID === "custom" || providerCatalogSupportsModelPreview(entry, actions);
}

export function providerCatalogDiscoveryRouteID(catalogID: string, entry: ProviderCatalogEntry | undefined, actions: PluginActionDescriptor[] = []) {
  if (catalogID === "custom") return "custom";
  return providerCatalogSupportsModelPreview(entry, actions) ? catalogID : "";
}

export function providerCatalogAPIKeyRequired(catalogID: string, entry: ProviderCatalogEntry | undefined, actions: PluginActionDescriptor[] = [], providerTypeOptions: ProviderAuthModeOption[] = []) {
  if (catalogID === "custom") return true;
  const option = providerTypeOptions.find((item) => item.value === entry?.type);
  if (option?.apiKeyRequired === false) return false;
  if (option?.apiKeyRequired === true) return true;
  const action = providerCatalogModelsPreviewAction(entry, actions);
  if (!action) return true;
  return pluginActionRequiredFields(action).has("api_key");
}

export function customUpstreamDiscoveryPayload(
  values: Record<string, string>,
  providerID: string,
  modelCategory: string,
  headers: Record<string, unknown> = {},
  providerTypeOptions: ProviderAuthModeOption[] = [],
) {
  return {
    provider_id: providerID,
    name: values.name,
    type: providerTypeValue(values, providerTypeOptions),
    base_url: values.base_url,
    api_key: values.api_key,
    ...headers,
    anthropic_auth_type: providerAuthMode(values, providerTypeOptions),
    model_category: modelCategory,
  };
}

// Identifies the upstream a custom Provider's model list was loaded from. The
// Provider type and effective auth mode affect the discovery request,
// so changing either one must invalidate the cached list even when the Base URL
// and API key stay the same.
export function customUpstreamConnectionKey(values: Record<string, string>, providerTypeOptions: ProviderAuthModeOption[] = []) {
  return JSON.stringify([
    values.base_url,
    values.api_key,
    providerTypeValue(values, providerTypeOptions),
    providerAuthMode(values, providerTypeOptions),
    values.custom_headers,
  ]);
}

function providerAuthModes(providerTypeOptions: ProviderAuthModeOption[], providerType: string) {
  return providerTypeOptions.find((option) => option.value === providerType)?.authModes ?? [];
}

function pluginActionRequiredFields(action: PluginActionDescriptor) {
  const required = action.input_schema?.required;
  return new Set(Array.isArray(required) ? required.filter((item): item is string => typeof item === "string") : []);
}

function preferredProviderAuthMode(modes: string[]) {
  return modes.includes("x-api-key") ? "x-api-key" : modes[0] ?? "";
}

export function customUpstreamModelsVisible(
  mode: "create" | "edit",
  editTab: string,
  quickAPIConnect: boolean,
  quickAPITab: string,
  createStep: number,
) {
  if (mode === "edit") return editTab === "models";
  return quickAPIConnect ? quickAPITab === "models" : createStep === 3;
}

// Whether a custom Provider's listed models can be imported. Creating a
// Provider imports at least one model, and the models it imports have to be the
// ones the configured upstream reported — a list that is empty, or that predates
// an edit to the connection, is neither.
export function customUpstreamModelsAreCurrent(modelCount: number, loadedConnection: string, connection: string) {
  return modelCount > 0 && loadedConnection === connection;
}
