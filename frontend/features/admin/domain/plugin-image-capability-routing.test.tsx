import { describe, expect, it } from "vitest";
import { type AppData, type Model, type ModelRoute, type Provider, type ProviderResource } from "../core/types";
import { emptyData } from "./catalog";
import { modelRouteDefaults, providerModelSelectOptions, providerSelectOptions } from "./entities";
import { modelAvailabilitySummary } from "./formatting";

function pluginImageData(): AppData {
  const data = emptyData();
  data.pluginUI = [{
    plugin_id: "tokenhub.provider.kimi",
    id: "image-capability",
    slot: "provider.model.panel",
    action: "kimi.image_capability.configure",
    provider_types: ["kimi_subscription"],
    schema: { layout: "image_capability" },
  }];
  data.pluginActions = [{
    plugin_id: "tokenhub.provider.kimi",
    action_id: "kimi.image_capability.configure",
    kind: "mutate",
    capability: "image.capability.configure",
    subject: "kimi_subscription",
    metadata: {
      display_name: "Kimi Subscription ImageGen",
      provider_resource_type: "kimi_subscription_account",
      public_model: "kimi-image",
      upstream_model: "moonshot-image",
    },
  }];
  data.providers = [
    provider("prv_openai", "OpenAI API", "openai_compatible"),
    provider("prv_kimi", "Kimi Pool", "kimi_subscription"),
  ];
  data.models = [imageModel("kimi-image")];
  data.routes = [route("route_kimi_image", "prv_kimi")];
  return data;
}

describe("plugin image capability routing", () => {
  it("drives route defaults and provider selectors from plugin metadata", () => {
    const data = pluginImageData();

    expect(providerSelectOptions(data, null, { model_name: "kimi-image" }).map((option) => option.value)).toEqual(["prv_kimi"]);
    expect(providerModelSelectOptions(data, null, { model_name: "kimi-image", provider_id: "prv_kimi" })).toEqual([
      { value: "moonshot-image", label: "moonshot-image" },
    ]);
    expect(modelRouteDefaults(data.models[0], data)).toMatchObject({
      model_name: "kimi-image",
      provider_id: "prv_kimi",
      provider_model: "moonshot-image",
    });
  });

  it("checks route health against the plugin resource type", () => {
    const data = pluginImageData();
    data.providerResources = [
      resource("rsrc_wrong", "prv_kimi", "openai_subscription", "supported"),
    ];

    expect(modelAvailabilitySummary(data.models[0], data)).toMatchObject({
      tone: "warning",
      healthyRoutes: 0,
    });

    data.providerResources.push(resource("rsrc_kimi", "prv_kimi", "kimi_subscription_account", "supported"));
    expect(modelAvailabilitySummary(data.models[0], data)).toMatchObject({
      tone: "ready",
      healthyRoutes: 1,
    });
  });
});

function provider(id: string, name: string, type: string): Provider {
  return { id, name, type, status: "active", healthy: true, priority: 10 };
}

function imageModel(name: string): Model {
  return { id: `mdl_${name}`, name, family: "image", modality: "image", status: "active" };
}

function route(id: string, providerID: string): ModelRoute {
  return {
    id,
    model_name: "kimi-image",
    provider_id: providerID,
    provider_model: "moonshot-image",
    priority: 1,
    weight: 100,
    status: "active",
  };
}

function resource(id: string, providerID: string, resourceType: string, imageCapability: string): ProviderResource {
  return {
    id,
    provider_id: providerID,
    name: id,
    resource_type: resourceType,
    status: "active",
    healthy: true,
    priority: 1,
    weight: 100,
    options: { image_generation_capability: imageCapability },
  };
}
