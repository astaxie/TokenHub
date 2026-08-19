import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const { modelDirectorySubtitle, modelDisplayName, modelMetadataPayload, modelMetadataWithDisplayName } = await importTypeScript(new URL("./model-display-name.ts", import.meta.url));
const { defaultDisplayName } = await importTypeScript(new URL("./form-defaults.ts", import.meta.url));
const catalog = await readFile(new URL("../../../../data/model-catalog.yaml", import.meta.url), "utf8");
const createModalSource = await readFile(new URL("../views/model-create-modal.tsx", import.meta.url), "utf8");
const directorySource = await readFile(new URL("../views/model-directory.tsx", import.meta.url), "utf8");
const glm52 = catalog.slice(catalog.indexOf('  - name: "zai-org/glm-5.2"'), catalog.indexOf('  - name:', catalog.indexOf('  - name: "zai-org/glm-5.2"') + 1));
const catalogTitle = glm52.match(/^ {4}title: "([^"]+)"$/m)?.[1];
const catalogEndpoints = glm52.match(/^ {6}endpoints: "([^"]+)"$/m)?.[1];

test("model display name uses and preserves tracked GLM-5.2 metadata", () => {
  assert.equal(catalogTitle, "GLM 5.2");
  assert.equal(catalogEndpoints, "chat/completions,anthropic");

  const metadata = modelMetadataWithDisplayName({ endpoints: catalogEndpoints }, catalogTitle);
  assert.deepEqual(metadata, { endpoints: "chat/completions,anthropic", title: "GLM 5.2" });
  assert.equal(modelDisplayName(metadata, "zai-org/glm-5.2"), "GLM 5.2");
  assert.equal(modelDirectorySubtitle("zai-org/glm-5.2", "GLM 5.2"), "zai-org/glm-5.2");
});

test("clearing a display name retains unrelated tracked metadata", () => {
  const metadata = modelMetadataWithDisplayName({ endpoints: catalogEndpoints, title: catalogTitle }, " ");
  assert.deepEqual(metadata, { endpoints: "chat/completions,anthropic" });
  assert.equal(modelDisplayName(metadata, "zai-org/glm-5.2"), "zai-org/glm-5.2");
});

test("clearing the only display name sends an explicit empty metadata map", () => {
  assert.deepEqual(modelMetadataPayload({ title: catalogTitle }, " "), { metadata: {} });
  assert.deepEqual(modelMetadataPayload(undefined, " "), {});
});

test("model creation and directory views expose the display name", () => {
  assert.match(createModalSource, /renderField\("display_name"\)/);
  assert.match(directorySource, /modelDisplayName\(model\.metadata, model\.name\)/);
});

test("only role configuration defaults a display name", () => {
  assert.equal(defaultDisplayName("models"), "");
  assert.deepEqual(modelMetadataWithDisplayName(undefined, defaultDisplayName("models")), {});
  assert.equal(defaultDisplayName("role-configs"), "普通用户");
});
