import type { ProviderCatalogModel } from "../core/types";

export function validManualModelID(id: string) {
  return id.trim() !== "" && !/[,\n\r]/.test(id);
}

export function selectedProviderModelIDs(selected: Record<string, boolean>, manual: ProviderCatalogModel[], imported: ReadonlySet<string>, discoveryCurrent: boolean) {
  const discovered = discoveryCurrent
    ? Object.entries(selected).filter(([id, checked]) => checked && !imported.has(id)).map(([id]) => id)
    : [];
  const manualIDs = manual.map((model) => model.id).filter((id) => !imported.has(id));
  return Array.from(new Set([...discovered, ...manualIDs]));
}

export function mergedProviderCatalogModels(discovered: ProviderCatalogModel[], manual: ProviderCatalogModel[], includeDiscovered: boolean) {
  const merged = new Map<string, ProviderCatalogModel>();
  for (const model of includeDiscovered ? discovered : []) merged.set(model.id, model);
  // Manual entries win: an operator who typed a display name expects it to persist.
  for (const model of manual) merged.set(model.id, model);
  return Array.from(merged.values());
}

export function manualProviderModelsFields({
  mode,
  manual,
  discovered,
  discoveryCurrent,
}: {
  mode: "create" | "edit";
  manual: ProviderCatalogModel[];
  discovered?: ProviderCatalogModel[];
  discoveryCurrent: boolean;
}) {
  if (manual.length === 0) return {};
  return {
    catalog_id: "custom",
    // providerPayload parses this form value into the request's model array, so
    // this helper runs after that parse and must also return an array.
    custom_models: mergedProviderCatalogModels(discovered ?? [], manual, discoveryCurrent),
    // The request category filters imports; persisted options keep their category.
    model_category: "",
    ...(mode === "edit" ? { preserve_catalog: true } : {}),
  };
}
