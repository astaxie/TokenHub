import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  availableProviderModelSelectOptions,
  initialModelRoutes,
  providerModelSelectionValue,
} = await importTypeScript(new URL("./provider-model-selection.ts", import.meta.url));

test("available Provider model options only include active inventory on active Providers", () => {
  const data = {
    providers: [
      { id: "provider-a", name: "Provider A", priority: 2, status: "active" },
      { id: "provider-b", name: "Provider B", priority: 1, status: "active" },
      { id: "provider-disabled", name: "Disabled", priority: 3, status: "disabled" },
    ],
    providerModels: [
      { provider_id: "provider-a", upstream_model: "chat-a", display_name: "Chat A", status: "active" },
      { provider_id: "provider-b", upstream_model: "chat-b", status: "active" },
      { provider_id: "provider-b", upstream_model: "retired", status: "disabled" },
      { provider_id: "provider-disabled", upstream_model: "hidden", status: "active" },
    ],
  };

  assert.deepEqual(availableProviderModelSelectOptions(data), [
    {
      value: providerModelSelectionValue("provider-b", "chat-b"),
      label: "Provider B / chat-b",
    },
    {
      value: providerModelSelectionValue("provider-a", "chat-a"),
      label: "Provider A / chat-a / Chat A",
    },
  ]);
});

test("selected Provider models become unique active routes with safe encoded values", () => {
  const first = providerModelSelectionValue("provider,one", "vendor/model,one");
  const second = providerModelSelectionValue("provider:two", "vendor/model:two");

  assert.deepEqual(initialModelRoutes(`${first}, ${second}, ${first}, malformed`), [
    {
      provider_id: "provider,one",
      provider_model: "vendor/model,one",
      weight: 100,
      project_scope: "all",
      status: "active",
    },
    {
      provider_id: "provider:two",
      provider_model: "vendor/model:two",
      weight: 100,
      project_scope: "all",
      status: "active",
    },
  ]);
});
