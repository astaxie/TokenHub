import { describe, expect, it } from "vitest";
import { emptyData } from "./catalog";
import { modelRouteDefaults, providerImageCapabilityProfileForModel } from "./entities";

describe("providerImageCapabilityProfileForModel", () => {
  it("requires plugin image capability metadata instead of inferring Codex defaults", () => {
    const data = emptyData();
    data.models = [{
      id: "mdl_codex_image",
      name: "codex-gpt-image-2",
      modality: "image",
      category: "codex",
      family: "codex",
      status: "active",
      capabilities: [],
      input_modalities: ["text"],
      output_modalities: ["image"],
      supported_parameters: [],
      metadata: { execution_type: "codex_subscription_image_generation" },
    }];
    data.providers = [{ id: "prv_codex", name: "Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 }];

    expect(providerImageCapabilityProfileForModel(data, "openai_codex", data.models[0])).toBeNull();
    expect(modelRouteDefaults(data.models[0], data)).toMatchObject({
      provider_id: "prv_codex",
      provider_model: "",
    });

    data.pluginActions = [{
      plugin_id: "tokenhub.provider.openai-codex",
      action_id: "openai_codex.image_capability.configure",
      kind: "mutate",
      capability: "image.capability.configure",
      subject: "openai_codex",
      metadata: {
        public_model: "codex-gpt-image-2",
        upstream_model: "gpt-image-2",
      },
    }];

    expect(providerImageCapabilityProfileForModel(data, "openai_codex", data.models[0])?.upstreamModel).toBe("gpt-image-2");
    expect(modelRouteDefaults(data.models[0], data)).toMatchObject({
      provider_id: "prv_codex",
      provider_model: "gpt-image-2",
    });

    data.pluginUI = [{
      plugin_id: "tokenhub.provider.openai-codex",
      id: "image",
      slot: "provider.model.panel",
      action: "openai_codex.image_capability.configure",
      provider_types: ["openai_codex"],
      schema: { layout: "image_capability" },
    }];

    expect(providerImageCapabilityProfileForModel(data, "openai_codex", data.models[0])?.upstreamModel).toBe("gpt-image-2");
    expect(modelRouteDefaults(data.models[0], data)).toMatchObject({
      provider_id: "prv_codex",
      provider_model: "gpt-image-2",
    });
  });
});
