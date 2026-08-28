import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  pluginActionDeclared,
  pluginActionInputDefaults,
  pluginActionInputFields,
  pluginActionInputSchemaSupported,
  pluginActionKey,
  pluginActionPayload,
  pluginBackgroundJobKey,
  pluginBackgroundJobPayload,
  redactPluginActionResult,
} = await importTypeScript(new URL("./plugin-actions.ts", import.meta.url));

test("plugin action and background job keys stay stable", () => {
  assert.equal(pluginActionKey("tokenhub.plugin", "inspect"), "tokenhub.plugin:inspect");
  assert.equal(pluginActionKey("tokenhub.plugin"), "tokenhub.plugin:");
  assert.equal(pluginBackgroundJobKey("tokenhub.plugin", "sync"), "tokenhub.plugin:sync");
  assert.equal(pluginBackgroundJobKey("tokenhub.plugin"), "tokenhub.plugin:");
});

test("plugin action declaration requires both identifiers", () => {
  assert.equal(pluginActionDeclared({ plugin_id: "tokenhub.plugin", action_id: "inspect" }), true);
  assert.equal(pluginActionDeclared({ plugin_id: "tokenhub.plugin", action_id: " " }), false);
  assert.equal(pluginActionDeclared(undefined), false);
});

test("plugin action input helpers extract supported fields and stable defaults", () => {
  const action = {
    plugin_id: "tokenhub.plugin",
    action_id: "inspect",
    input_schema: {
      type: "object",
      required: ["name", 12, "enabled"],
      properties: {
        name: { type: "string" },
        enabled: { type: "boolean" },
        retries: { type: "integer" },
        ratio: { type: "number" },
        unsupported: { type: "object" },
      },
    },
  };

  assert.deepEqual(pluginActionInputFields(action), [
    { name: "name", type: "string", required: true },
    { name: "enabled", type: "boolean", required: true },
    { name: "retries", type: "integer", required: false },
    { name: "ratio", type: "number", required: false },
  ]);
  assert.deepEqual(pluginActionInputDefaults(action), {
    name: "",
    enabled: false,
    retries: "",
    ratio: "",
  });
  assert.equal(pluginActionInputSchemaSupported(action.input_schema), false);
});

test("plugin action input schema support accepts empty object schemas and rejects non-object fields", () => {
  assert.equal(pluginActionInputSchemaSupported({ type: "object" }), true);
  assert.equal(pluginActionInputSchemaSupported({ type: "object", properties: {} }), true);
  assert.equal(pluginActionInputSchemaSupported({ type: "object", properties: { name: { type: "string" } } }), true);
  assert.equal(pluginActionInputSchemaSupported({ type: "object", properties: { nested: { type: "object" } } }), false);
  assert.equal(pluginActionInputSchemaSupported({ type: "array" }), false);
});

test("plugin action and background job payload coercion matches field types", () => {
  const host = {
    input_schema: {
      type: "object",
      properties: {
        name: { type: "string" },
        enabled: { type: "boolean" },
        retries: { type: "integer" },
        ratio: { type: "number" },
      },
    },
  };
  const values = {
    name: "  TokenHub  ",
    enabled: "",
    retries: "7",
    ratio: "3.5",
  };

  assert.deepEqual(pluginActionPayload(host, values), {
    name: "  TokenHub  ",
    enabled: false,
    retries: 7,
    ratio: 3.5,
  });
  assert.deepEqual(pluginBackgroundJobPayload(host, values), {
    name: "  TokenHub  ",
    enabled: false,
    retries: 7,
    ratio: 3.5,
  });
  assert.deepEqual(pluginActionPayload(host, {
    name: "",
    enabled: true,
    retries: "",
    ratio: "",
  }), {
    enabled: true,
  });
});

test("plugin action result redaction removes nested secret material", () => {
  assert.deepEqual(redactPluginActionResult({
    access_token: "secret",
    refresh_token: "refresh",
    nested: {
      api_key: "hidden",
      safe: "visible",
      child: [{ credential_blob: "blob", safe: true }],
    },
    items: [
      { id_token: "token", keep: 1 },
      "plain",
    ],
    output_api_key: "abc",
    credentials: { value: "secret" },
  }), {
    access_token: "[redacted]",
    refresh_token: "[redacted]",
    nested: {
      api_key: "[redacted]",
      safe: "visible",
      child: [{ credential_blob: "[redacted]", safe: true }],
    },
    items: [
      { id_token: "[redacted]", keep: 1 },
      "plain",
    ],
    output_api_key: "[redacted]",
    credentials: "[redacted]",
  });
});
