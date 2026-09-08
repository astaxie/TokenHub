import type { ApiContext } from "../core/types";
import { tx } from "../i18n/runtime";
import { adminFetch, readAdminError } from "../resources/payloads";
export async function pricingRequest(api: ApiContext, path: string, body: unknown, signal?: AbortSignal) {
  const response = await adminFetch(api, path, { method: "POST", body: JSON.stringify(body), signal });
  if (!response.ok) {
    const message = await readAdminError(response, tx("计费操作失败"));
    if (message.includes("changed after") || message.includes("pricing basis changed")) throw new Error(tx("价格依据已变化，请重新读取并分析。"));
    throw new Error(message);
  }
  return response.json();
}
