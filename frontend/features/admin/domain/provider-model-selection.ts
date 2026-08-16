type ProviderModelSelectionData = {
  providers: Array<{ id: string; name: string; priority: number; status: string }>;
  providerModels: Array<{ provider_id: string; upstream_model: string; display_name?: string; status: string }>;
};

export type InitialModelRoute = {
  provider_id: string;
  provider_model: string;
  weight: number;
  project_scope: "all";
  status: "active";
};

type ProviderCatalogModelFilter = {
  catalogID: string;
  usesCodexCatalog: boolean;
  quickAPIFlow: boolean;
  selectedCategory: string;
  discoveredCategory: string;
  matchesStandardModel: boolean;
};

export function providerCatalogModelIsSelectable(filter: ProviderCatalogModelFilter) {
  if (filter.quickAPIFlow || filter.catalogID === "kronk") return true;
  if (filter.selectedCategory !== "all" && filter.discoveredCategory !== filter.selectedCategory) return false;
  if (filter.usesCodexCatalog || filter.catalogID === "custom") return true;
  return filter.matchesStandardModel;
}

export function providerModelSelectionValue(providerID: string, upstreamModel: string) {
  return `${encodeURIComponent(providerID)}|${encodeURIComponent(upstreamModel)}`;
}

function parseProviderModelSelection(value: string) {
  const parts = value.split("|");
  if (parts.length !== 2 || !parts[0] || !parts[1]) return null;
  try {
    const providerID = decodeURIComponent(parts[0]).trim();
    const upstreamModel = decodeURIComponent(parts[1]).trim();
    return providerID && upstreamModel ? { providerID, upstreamModel } : null;
  } catch {
    return null;
  }
}

export function initialModelRoutes(value?: string): InitialModelRoute[] {
  const routes: InitialModelRoute[] = [];
  const seen = new Set<string>();
  for (const selected of (value ?? "").split(",").map((item) => item.trim()).filter(Boolean)) {
    const model = parseProviderModelSelection(selected);
    if (!model) continue;
    const key = providerModelSelectionValue(model.providerID, model.upstreamModel);
    if (seen.has(key)) continue;
    seen.add(key);
    routes.push({
      provider_id: model.providerID,
      provider_model: model.upstreamModel,
      weight: 100,
      project_scope: "all",
      status: "active",
    });
  }
  return routes;
}

export function availableProviderModelSelectOptions(data: ProviderModelSelectionData) {
  const activeProviders = new Map(
    data.providers
      .filter((provider) => provider.status === "active")
      .map((provider) => [provider.id, provider]),
  );
  const seen = new Set<string>();
  return data.providerModels
    .flatMap((model) => {
      const provider = activeProviders.get(model.provider_id);
      const value = providerModelSelectionValue(model.provider_id, model.upstream_model);
      if (!provider || model.status !== "active" || seen.has(value)) return [];
      seen.add(value);
      const displayName = model.display_name && model.display_name !== model.upstream_model ? ` / ${model.display_name}` : "";
      return [{
        value,
        label: `${provider.name || provider.id} / ${model.upstream_model}${displayName}`,
        providerPriority: provider.priority,
      }];
    })
    .sort((left, right) => left.providerPriority - right.providerPriority || left.label.localeCompare(right.label))
    .map(({ value, label }) => ({ value, label }));
}
