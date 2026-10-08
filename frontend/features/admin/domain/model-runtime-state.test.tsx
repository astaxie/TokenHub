import { describe, expect, it } from "vitest";
import { type AppData, type Model, type ModelRoute } from "../core/types";
import { emptyData } from "./catalog";
import { modelRuntimeState } from "./model-directory";

const model: Model = { id: "model", name: "model", family: "test", modality: "chat", status: "active" };
const route: ModelRoute = { id: "route", model_name: model.name, provider_id: "provider", provider_model: "upstream", status: "active", priority: 1, weight: 100 };

function fixture(): AppData {
  return {
    ...emptyData(),
    models: [model],
    routes: [route],
    providers: [{ id: "provider", name: "Test Provider", type: "mock", status: "active", healthy: true, priority: 1 }],
  };
}

describe("Model runtime availability", () => {
  it("uses overview availability on direct visits without monitoring snapshots", () => {
    const data = fixture();
    expect(data.providerMonitoring).toEqual([]);
    expect(modelRuntimeState(model, data)).toBe("healthy");
  });

  it.each(["missing", "disabled", "unhealthy"])("reports an unavailable route for a %s provider without monitoring snapshots", (state) => {
    const data = fixture();
    if (state === "missing") data.providers = [];
    if (state === "disabled") data.providers[0].status = "disabled";
    if (state === "unhealthy") data.providers[0].healthy = false;
    expect(modelRuntimeState(model, data)).toBe("unavailable");
  });

  it.each(["missing", "disabled", "unhealthy"])("reports an unavailable route for a %s bound resource", (state) => {
    const data = fixture();
    data.routes = [{ ...route, provider_resource_id: "resource" }];
    data.providerResources = state === "missing" ? [] : [{ id: "resource", provider_id: "provider", name: "Test Resource", resource_type: "account", status: state === "disabled" ? "disabled" : "active", healthy: state !== "unhealthy", priority: 1, weight: 100 }];
    expect(modelRuntimeState(model, data)).toBe("unavailable");
  });

  it("distinguishes partially available routes from disabled routes", () => {
    const data = fixture();
    data.routes.push({ ...route, id: "missing-provider", provider_id: "missing" });
    expect(modelRuntimeState(model, data)).toBe("degraded");
    data.routes[1].status = "disabled";
    expect(modelRuntimeState(model, data)).toBe("healthy");
    data.routes[0] = { ...route, status: "disabled" };
    expect(modelRuntimeState(model, data)).toBe("unmapped");
  });
});
