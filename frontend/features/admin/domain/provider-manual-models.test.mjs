import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  manualProviderModelsFields,
  mergedProviderCatalogModels,
  selectedProviderModelIDs,
  validManualModelID,
} = await importTypeScript(new URL("./provider-manual-models.ts", import.meta.url));

const discovered = [
  { id: "gpt-4o", name: "GPT-4o", display_name: "GPT-4o" },
  { id: "manual-model", name: "Discovered manual", display_name: "Discovered manual" },
];

test("manual model IDs reject empty values, commas and line breaks", () => {
  assert.equal(validManualModelID("gpt-4o-mini"), true);
  assert.equal(validManualModelID("  gpt-4o-mini  "), true);
  assert.equal(validManualModelID(""), false);
  assert.equal(validManualModelID("   "), false);
  assert.equal(validManualModelID("model,alias"), false);
  assert.equal(validManualModelID("model\nnext"), false);
  assert.equal(validManualModelID("model\rnext"), false);
});

test("selected IDs include manual models and skip imported or stale discoveries", () => {
  const manual = [{ id: "manual-model", name: "Manual", display_name: "Manual" }];
  assert.deepEqual(
    selectedProviderModelIDs({ "gpt-4o": true, "stale-model": true, "manual-model": false }, manual, new Set(["stale-model"]), true),
    ["gpt-4o", "manual-model"],
  );
  assert.deepEqual(
    selectedProviderModelIDs({ "gpt-4o": true }, manual, new Set(["gpt-4o"]), false),
    ["manual-model"],
  );
});

test("merging keeps discovered models and lets a manual entry override a duplicate", () => {
  const manual = [
    { id: "manual-model", name: "Manual", display_name: "Manual" },
    { id: "private-model", name: "Private", display_name: "Private" },
  ];
  const merged = mergedProviderCatalogModels(discovered, manual, true);
  assert.deepEqual(merged.map((model) => model.display_name), ["GPT-4o", "Manual", "Private"]);

  const stale = mergedProviderCatalogModels(discovered, manual, false);
  assert.deepEqual(stale.map((model) => model.id), ["manual-model", "private-model"]);
});

test("manual fields are omitted without manual models and emitted otherwise", () => {
  assert.deepEqual(manualProviderModelsFields({ mode: "create", manual: [], discoveryCurrent: true }), {});
  assert.deepEqual(
    manualProviderModelsFields({ mode: "create", manual: [{ id: "private-model", name: "Private", display_name: "Private" }], discoveryCurrent: false }),
    {
      catalog_id: "custom",
      custom_models: [{ id: "private-model", name: "Private", display_name: "Private" }],
      model_category: "",
    },
  );
  assert.deepEqual(
    manualProviderModelsFields({
      mode: "edit",
      manual: [{ id: "private-model", name: "Private", display_name: "Private" }],
      discovered,
      discoveryCurrent: true,
    }),
    {
      catalog_id: "custom",
      custom_models: [
        { id: "gpt-4o", name: "GPT-4o", display_name: "GPT-4o" },
        { id: "manual-model", name: "Discovered manual", display_name: "Discovered manual" },
        { id: "private-model", name: "Private", display_name: "Private" },
      ],
      model_category: "",
      preserve_catalog: true,
    },
  );
});
