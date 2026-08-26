import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  codexImageModelState,
  codexImageResources,
  codexImageRouteEnabled,
  defaultCodexImageResourceID,
  imageCapabilityProfileFromAction,
} = await importTypeScript(new URL("./codex-image-capability.ts", import.meta.url));

const route = { model_name: "codex-gpt-image-2", provider_id: "provider-1", provider_model: "gpt-image-2", status: "active" };
const resource = (id, capability, status = "active", resourceType = "openai_subscription") => ({
  id,
  provider_id: "provider-1",
  resource_type: resourceType,
  status,
  healthy: status === "active",
  options: capability ? { image_generation_capability: capability } : {},
});

test("Codex image route detection requires the exact active mapping", () => {
  assert.equal(codexImageRouteEnabled([route], "provider-1"), true);
  assert.equal(codexImageRouteEnabled([{ ...route, status: "disabled" }], "provider-1"), false);
  assert.equal(codexImageRouteEnabled([{ ...route, provider_model: "gpt-image-1" }], "provider-1"), false);
});

test("Codex image testing prefers the selected available account, then a supported account", () => {
  const resources = [resource("unsupported", "unsupported"), resource("supported", "supported"), resource("disabled", "supported", "disabled")];
  assert.equal(defaultCodexImageResourceID(resources, "provider-1", "unsupported"), "unsupported");
  assert.equal(defaultCodexImageResourceID(resources, "provider-1", "all"), "supported");
  assert.equal(defaultCodexImageResourceID(resources, "provider-1", "disabled"), "supported");
});

test("Codex image model state distinguishes disabled, unsupported, and unusable routes", () => {
  assert.equal(codexImageModelState([resource("one", "supported")], "provider-1", true), "enabled");
  assert.equal(codexImageModelState([resource("one", "supported")], "provider-1", false), "tested_disabled");
  assert.equal(codexImageModelState([resource("one", "unsupported")], "provider-1", false), "unsupported");
  assert.equal(codexImageModelState([resource("one", "unsupported")], "provider-1", true), "enabled_without_account");
  assert.equal(codexImageModelState([resource("one")], "provider-1", false), "untested");
});

test("image capability profile follows plugin metadata for resources and routes", () => {
  const profile = imageCapabilityProfileFromAction({
    metadata: {
      display_name: "Kimi Subscription ImageGen",
      provider_resource_type: "kimi_subscription_account",
      public_model: "kimi-image",
      upstream_model: "moonshot-image",
    },
  });
  assert.deepEqual(profile, {
    displayName: "Kimi Subscription ImageGen",
    publicModel: "kimi-image",
    upstreamModel: "moonshot-image",
    resourceType: "kimi_subscription_account",
  });
  assert.equal(codexImageRouteEnabled([{ ...route, model_name: "kimi-image", provider_model: "moonshot-image" }], "provider-1", profile), true);
  assert.deepEqual(
    codexImageResources([
      resource("codex", "supported"),
      resource("kimi", "supported", "active", "kimi_subscription_account"),
    ], "provider-1", profile).map((item) => item.id),
    ["kimi"],
  );
});
