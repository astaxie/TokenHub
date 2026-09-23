import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  providerPluginOptionFieldKey,
  providerPluginOptionValues,
  providerPluginOptionValuesForPlugin,
} = await importTypeScript(new URL("./provider-plugin-options.ts", import.meta.url));

describe("provider plugin option fields", () => {
  test("round trips plugin IDs and option names containing colons", () => {
    const fieldKey = providerPluginOptionFieldKey("vendor:plugin", "routing:tenant");
    const values = { [fieldKey]: "tenant-a" };

    assert.equal(fieldKey, "plugin_option:vendor%3Aplugin:routing%3Atenant");
    assert.deepEqual(providerPluginOptionValues(values), { "routing:tenant": "tenant-a" });
    assert.deepEqual(providerPluginOptionValuesForPlugin(values, "vendor:plugin"), { "routing:tenant": "tenant-a" });
    assert.deepEqual(providerPluginOptionValuesForPlugin(values, "vendor"), {});
  });

  test("ignores malformed encoded option fields", () => {
    assert.deepEqual(providerPluginOptionValues({
      "plugin_option:%ZZ:tenant": "invalid-plugin",
      "plugin_option:vendor:%ZZ": "invalid-option",
    }), {});
  });
});
