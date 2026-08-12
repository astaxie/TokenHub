import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  externalModelTemplateValues,
  filterReferenceModelTemplates,
  referenceModelTemplates,
} = await importTypeScript(new URL("./model-create-wizard.ts", import.meta.url));

const models = [
  {
    name: "qwen-max",
    category: "qwen",
    family: "qwen",
    modality: "chat",
    context_window: 32768,
    input_price_usd_per_1m: 1.2,
    output_price_usd_per_1m: 6,
    capabilities: ["chat", "tools"],
    supported_parameters: ["temperature"],
    metadata: { source: "tokenhub-standard-catalog" },
  },
  {
    name: "gpt-reference",
    category: "gpt",
    family: "gpt",
    modality: "chat",
    metadata: { source: "public-provider-conf" },
  },
  {
    name: "already-external",
    category: "gpt",
    family: "gpt",
    modality: "chat",
    metadata: { source: "tokenhub-standard-catalog", directory_role: "external" },
  },
  {
    name: "custom-record",
    category: "custom",
    family: "custom",
    modality: "chat",
    metadata: { source: "manual" },
  },
];

test("reference model templates expose only unpublished built-in catalog models", () => {
  assert.deepEqual(referenceModelTemplates(models).map((model) => model.name), ["gpt-reference", "qwen-max"]);
  assert.deepEqual(filterReferenceModelTemplates(models, "QWEN", "qwen").map((model) => model.name), ["qwen-max"]);
});

test("a reference template pre-fills the external model contract", () => {
  assert.deepEqual(externalModelTemplateValues(models[0]), {
    name: "qwen-max",
    category: "qwen",
    family: "qwen",
    modality: "chat",
    context_window: "32768",
    input_price_usd_per_1m: "1.2",
    cache_read_price_usd_per_1m: "",
    output_price_usd_per_1m: "6",
    embedding_price_usd_per_1m: "",
    capabilities: "chat, tools",
    supported_parameters: "temperature",
    input_modalities: "text",
    output_modalities: "",
    initial_provider_models: "",
    status: "active",
  });
});
