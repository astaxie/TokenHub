import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  defaultProviderImageCapabilityResourceID,
  imageCapabilityProfileFromAction,
  providerImageCapabilityResources,
  providerImageCapabilityState,
  providerImageCapabilityContribution,
  providerImageCapabilityProfile,
  providerImageRouteEnabled,
} = await importTypeScript(new URL("./provider-image-capability.ts", import.meta.url));

const route = { model_name: "codex-gpt-image-2", provider_id: "provider-1", provider_model: "gpt-image-2", status: "active" };
const resource = (id, capability, status = "active", resourceType = "openai_subscription") => ({
  id,
  provider_id: "provider-1",
  resource_type: resourceType,
  status,
  healthy: status === "active",
  options: capability ? { image_generation_capability: capability } : {},
});

test("provider image route detection requires the exact active mapping", () => {
  assert.equal(providerImageRouteEnabled([route], "provider-1"), true);
  assert.equal(providerImageRouteEnabled([{ ...route, status: "disabled" }], "provider-1"), false);
  assert.equal(providerImageRouteEnabled([{ ...route, provider_model: "gpt-image-1" }], "provider-1"), false);
});

test("provider image testing prefers the selected available account, then a supported account", () => {
  const resources = [resource("unsupported", "unsupported"), resource("supported", "supported"), resource("disabled", "supported", "disabled")];
  assert.equal(defaultProviderImageCapabilityResourceID(resources, "provider-1", "unsupported"), "unsupported");
  assert.equal(defaultProviderImageCapabilityResourceID(resources, "provider-1", "all"), "supported");
  assert.equal(defaultProviderImageCapabilityResourceID(resources, "provider-1", "disabled"), "supported");
});

test("provider image model state distinguishes disabled, unsupported, and unusable routes", () => {
  assert.equal(providerImageCapabilityState([resource("one", "supported")], "provider-1", true), "enabled");
  assert.equal(providerImageCapabilityState([resource("one", "supported")], "provider-1", false), "tested_disabled");
  assert.equal(providerImageCapabilityState([resource("one", "unsupported")], "provider-1", false), "unsupported");
  assert.equal(providerImageCapabilityState([resource("one", "unsupported")], "provider-1", true), "enabled_without_account");
  assert.equal(providerImageCapabilityState([resource("one")], "provider-1", false), "untested");
});

test("image capability profile follows plugin metadata for resources and routes", () => {
  const profile = imageCapabilityProfileFromAction({
    metadata: {
      display_name: "Kimi Subscription ImageGen",
      provider_resource_type: "kimi_subscription_account",
      public_model: "kimi-image",
      upstream_model: "moonshot-image",
      capability_option: "kimi_image_capability",
      capability_supported_value: "available",
      capability_unsupported_value: "blocked",
    },
  });
  assert.deepEqual(profile, {
    displayName: "Kimi Subscription ImageGen",
    publicModel: "kimi-image",
    upstreamModel: "moonshot-image",
    resourceType: "kimi_subscription_account",
    capabilityOption: "kimi_image_capability",
    capabilitySupportedValue: "available",
    capabilityUnsupportedValue: "blocked",
  });
  assert.equal(providerImageRouteEnabled([{ ...route, model_name: "kimi-image", provider_model: "moonshot-image" }], "provider-1", profile), true);
  assert.deepEqual(
    providerImageCapabilityResources([
      resource("codex", "supported"),
      { ...resource("kimi", undefined, "active", "kimi_subscription_account"), options: { kimi_image_capability: "available" } },
    ], "provider-1", profile).map((item) => item.id),
    ["kimi"],
  );
  assert.equal(defaultProviderImageCapabilityResourceID([
    { ...resource("unknown", undefined, "active", "kimi_subscription_account"), options: { kimi_image_capability: "blocked" } },
    { ...resource("kimi", undefined, "active", "kimi_subscription_account"), options: { kimi_image_capability: "available" } },
  ], "provider-1", "all", profile), "kimi");
  assert.equal(providerImageCapabilityState([
    { ...resource("kimi", undefined, "active", "kimi_subscription_account"), options: { kimi_image_capability: "available" } },
  ], "provider-1", true, profile), "enabled");
});

test("image capability panel requires a matching Admin UI contribution", () => {
  const action = { plugin_id: "tokenhub.provider.kimi", action_id: "kimi.image_capability.configure" };
  assert.equal(providerImageCapabilityContribution([
    { plugin_id: "tokenhub.provider.kimi", id: "image", slot: "provider.model.panel", action: "kimi.image_capability.configure", provider_types: ["kimi_subscription"], schema: { layout: "image_capability" } },
    { plugin_id: "tokenhub.provider.kimi", id: "quota", slot: "provider.resource.panel", action: "kimi.image_capability.configure", provider_types: ["kimi_subscription"] },
  ], "kimi_subscription", action)?.id, "image");
  assert.equal(providerImageCapabilityContribution([
    { plugin_id: "tokenhub.provider.kimi", id: "image", slot: "provider.model.panel", action: "kimi.image_capability.configure", provider_types: ["kimi_subscription"], schema: { layout: "quota" } },
    { plugin_id: "tokenhub.provider.other", id: "image", slot: "provider.model.panel", action: "kimi.image_capability.configure", provider_types: ["kimi_subscription"], schema: { layout: "image_capability" } },
  ], "kimi_subscription", action), undefined);
  assert.equal(providerImageCapabilityProfile([
    { plugin_id: "tokenhub.provider.kimi", id: "image", slot: "provider.model.panel", action: "kimi.image_capability.configure", provider_types: ["kimi_subscription"], schema: { layout: "image_capability" } },
  ], [{ ...action, capability: "image.capability.configure", subject: "kimi_subscription", metadata: { public_model: "kimi-image", upstream_model: "moonshot-image" } }], "kimi_subscription")?.publicModel, "kimi-image");
});
