export function providerAnthropicAuthType(values: Record<string, string>) {
  if (values.type !== "anthropic") return "";
  return values.anthropic_auth_type || "x-api-key";
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
) {
  return {
    provider_id: providerID,
    name: values.name,
    type: values.type || "openai_compatible",
    base_url: values.base_url,
    api_key: values.api_key,
    ...headers,
    anthropic_auth_type: providerAnthropicAuthType(values),
    model_category: modelCategory,
  };
}

// Identifies the upstream a custom Provider's model list was loaded from. The
// Provider type and effective Anthropic auth mode affect the discovery request,
// so changing either one must invalidate the cached list even when the Base URL
// and API key stay the same.
export function customUpstreamConnectionKey(values: Record<string, string>) {
  return JSON.stringify([
    values.base_url,
    values.api_key,
    values.type || "openai_compatible",
    providerAnthropicAuthType(values),
    values.custom_headers,
  ]);
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
