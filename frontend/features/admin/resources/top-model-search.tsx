import { useEffect, useState } from "react";
import { canAccessView } from "../core/navigation";
import { type AdminUser, type ApiContext, type AppData, type Model } from "../core/types";
import { adminFetch } from "../resources/payloads";

export function useTopModelSearch(api: ApiContext | undefined, user: AdminUser, data: AppData, query: string) {
  const [result, setResult] = useState<{ models: Model[]; loading: boolean; error: boolean }>({ models: [], loading: false, error: false });
  const enabled = Boolean(api && canAccessView(user, "models") && data.models.length === 0 && query.trim());
  useEffect(() => {
    const controller = new AbortController();
    setResult({ models: [], loading: enabled, error: false });
    if (!enabled || !api) return;
    const timer = setTimeout(() => {
      void adminFetch(api, `/api/admin/model-search?q=${encodeURIComponent(query.trim())}`, { signal: controller.signal })
        .then(async (response) => {
          if (!response.ok) throw new Error("model search failed");
          return await response.json() as { data: Model[] };
        })
        .then((payload) => { if (!controller.signal.aborted) setResult({ models: payload.data, loading: false, error: false }); })
        .catch(() => { if (!controller.signal.aborted) setResult({ models: [], loading: false, error: true }); });
    }, 150);
    return () => { clearTimeout(timer); controller.abort(); };
  }, [api, enabled, query, user.id, user.role]);
  return result;
}
