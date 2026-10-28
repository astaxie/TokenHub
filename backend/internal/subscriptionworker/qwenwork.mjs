import { AsyncLocalStorage } from "node:async_hooks";
import { QwenWorkEnterpriseAuthPlugin } from "./vendor/qoder/index.mjs";

// Enterprise deployments can omit the catalog service. The pinned adapter
// substitutes public-cloud seeds internally, so retain that provenance here.
export async function createQwenWorkEnterpriseProvider(context) {
  const hooks = await QwenWorkEnterpriseAuthPlugin(context);
  const discoveries = new AsyncLocalStorage();
  const models = hooks.provider.models;
  hooks.provider.models = (...args) => discoveries.run({ fallback: false }, async () => {
    const result = await models(...args);
    if (result && discoveries.getStore().fallback) {
      Object.defineProperty(result, Symbol.for("magpie.fellBack"), { value: true });
    }
    return result;
  });
  hooks["network.response"] = (request, response) => {
    const discovery = discoveries.getStore();
    if (discovery && response.status === 503 && new URL(request.url).pathname === "/algo/api/v2/model/list") {
      discovery.fallback = true;
    }
    return response;
  };
  return hooks;
}
