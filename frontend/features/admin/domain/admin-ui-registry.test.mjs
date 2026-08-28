import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  adminUIActionKey,
  adminUIContributionsForSlot,
  adminUIFieldValue,
  adminUIFields,
  adminUIPluginPageKey,
  adminUIPluginPages,
  redactAdminUIResult,
} = await importTypeScript(new URL("./admin-ui-registry.ts", import.meta.url));

test("admin UI registry builds plugin pages from nav section contributions", () => {
  const contributions = [
    {
      plugin_id: "tokenhub.admin.runtime",
      id: "runtime",
      slot: "nav.section",
      title: "Runtime",
      schema: { description: "Inspect runtime state." },
    },
    {
      plugin_id: "tokenhub.admin.runtime",
      id: "settings",
      slot: "settings.panel",
      title: "Settings",
    },
  ];

  assert.deepEqual(adminUIContributionsForSlot(contributions, "nav.section"), [contributions[0]]);
  assert.equal(adminUIPluginPageKey(contributions[0]), "tokenhub.admin.runtime:runtime");
  assert.deepEqual(adminUIPluginPages(contributions), [{
    key: "tokenhub.admin.runtime:runtime",
    pluginID: "tokenhub.admin.runtime",
    id: "runtime",
    title: "Runtime",
    description: "Inspect runtime state.",
    contribution: contributions[0],
  }]);
});

test("admin UI registry parses fields and resolves source paths", () => {
  const contribution = {
    plugin_id: "tokenhub.admin.runtime",
    id: "runtime",
    slot: "nav.section",
    schema: {
      fields: [
        { name: "requests", type: "metric", label: "Requests", source: "summary.request_count", format: "compact" },
        { name: "first_plugin", type: "text", label: "First plugin", source: "plugins.0.id" },
        { name: "raw", type: "code_viewer", label: "Raw", source: "summary" },
        { name: "ignored", type: "table", label: "Ignored" },
      ],
    },
  };
  const fields = adminUIFields(contribution);

  assert.equal(fields.length, 3);
  assert.equal(adminUIFieldValue({ summary: { request_count: 1200 }, plugins: [{ id: "tokenhub.one" }] }, fields[0]), "1.20K");
  assert.equal(adminUIFieldValue({ summary: {}, plugins: [{ id: "tokenhub.one" }] }, fields[1]), "tokenhub.one");
  assert.match(adminUIFieldValue({ summary: { request_count: 1200 }, plugins: [] }, fields[2]), /request_count/u);
});

test("admin UI registry action keys and redaction are shared across surfaces", () => {
  assert.equal(adminUIActionKey("tokenhub.plugin", "inspect"), "tokenhub.plugin:inspect");
  assert.deepEqual(redactAdminUIResult({
    data: {
      access_token: "secret",
      nested: { api_key: "hidden", safe: "visible" },
    },
  }), {
    data: {
      access_token: "[redacted]",
      nested: { api_key: "[redacted]", safe: "visible" },
    },
  });
});
