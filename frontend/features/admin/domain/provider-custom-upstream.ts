type ProviderAuthModeOption = {
  value: string;
  authModes?: string[];
};

export function providerAuthMode(values: Record<string, string>, providerTypeOptions: ProviderAuthModeOption[] = []) {
  const providerType = values.type || "";
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
    type: values.type || "openai_compatible",
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
    values.type || "openai_compatible",
    providerAuthMode(values, providerTypeOptions),
    values.custom_headers,
  ]);
}

function providerAuthModes(providerTypeOptions: ProviderAuthModeOption[], providerType: string) {
  const modes = providerTypeOptions.find((option) => option.value === providerType)?.authModes ?? [];
  if (modes.length > 0) return modes;
  return providerType === "anthropic" ? ["bearer", "x-api-key"] : [];
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
