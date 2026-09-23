import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const { modelEndpointProtocols, modelMetadataFacts } = await importTypeScript(new URL("./model-endpoints.ts", import.meta.url));

const catalog = readFileSync(new URL("../../../../data/model-catalog.yaml", import.meta.url), "utf8");
const glmStart = catalog.indexOf('  - name: "zai-org/glm-5.2"');
const glmEnd = catalog.indexOf("\n  - name:", glmStart + 1);
const glm = catalog.slice(glmStart, glmEnd);
const yamlList = (field) => (glm.match(new RegExp(`^    ${field}: \\[(.*)\\]$`, "m"))?.[1] ?? "")
  .split(",")
  .map((value) => value.trim().replaceAll('"', ""))
  .filter(Boolean);
const glmMetadata = { endpoints: glm.match(/^ {6}endpoints: "(.*)"$/m)?.[1] ?? "" };
const glmCapabilities = yamlList("capabilities");
const glmSupportedParameters = yamlList("supported_parameters");

test("model endpoint protocols come from the real GLM-5.2 catalog entry", () => {
  assert.notEqual(glmStart, -1, "GLM-5.2 must remain in the tracked model catalog");
  assert.deepEqual(modelEndpointProtocols(glmMetadata), ["chat/completions", "anthropic"]);
});

test("model metadata facts keep the real GLM-5.2 protocol, parameter, and capability rows separate", () => {
  assert.deepEqual(modelMetadataFacts(glmMetadata, glmCapabilities, glmSupportedParameters), [
    { kind: "protocols", values: ["chat/completions", "anthropic"] },
    { kind: "parameters", values: ["temperature", "tools", "response_format", "reasoning"] },
    { kind: "capabilities", values: ["chat", "reasoning", "tools", "structured_outputs", "serverless"] },
  ]);
});
