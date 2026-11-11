import type { ApiContext, ProviderCatalogEntry } from "../core/types";
import { adminFetch, readLoadError } from "./payloads";

// fetchProviderCatalog reloads the provider catalog on its own and rejects when that
// read fails. The catalog sync button must not announce success while the cards still
// render a stale catalog, and the page loader cannot provide that guarantee: it reports
// failures through page-level state and resolves normally, so awaiting it never rejects.
// Keeping this separate from the page loader also avoids re-reading every view's data
// for a catalog-only change.
export async function fetchProviderCatalog(api: ApiContext): Promise<ProviderCatalogEntry[]> {
  const resp = await adminFetch(api, "/api/admin/provider-catalog");
  if (!resp.ok) throw new Error(await readLoadError(resp, "provider-catalog"));
  const payload = await resp.json() as { data?: ProviderCatalogEntry[] };
  return payload.data ?? [];
}
