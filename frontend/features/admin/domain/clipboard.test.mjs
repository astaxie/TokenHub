import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const { copyText } = await importTypeScript(new URL("./clipboard.ts", import.meta.url));

function fakeDocument({ commandResult = true, commandError } = {}) {
  const calls = [];
  const activeElement = { focus: () => calls.push("restore-focus") };
  const textarea = {
    style: {},
    value: "",
    focus: () => calls.push("focus"),
    remove: () => calls.push("remove"),
    select: () => calls.push("select"),
    setAttribute: (name, value) => calls.push(["attribute", name, value]),
    setSelectionRange: (start, end) => calls.push(["selection", start, end]),
  };
  const document = {
    activeElement,
    body: { appendChild: (element) => calls.push(["append", element]) },
    createElement: (tagName) => {
      calls.push(["create", tagName]);
      return textarea;
    },
    execCommand: (command) => {
      calls.push(["command", command]);
      if (commandError) throw commandError;
      return commandResult;
    },
  };
  return { calls, document, textarea };
}

test("uses the Clipboard API when it is available", async () => {
  const writes = [];
  const legacy = fakeDocument();

  assert.equal(await copyText("th-key", {
    clipboard: { writeText: async (value) => writes.push(value) },
    document: legacy.document,
  }), true);
  assert.deepEqual(writes, ["th-key"]);
  assert.deepEqual(legacy.calls, []);
});

test("falls back to document.execCommand when Clipboard API is unavailable over HTTP", async () => {
  const legacy = fakeDocument();

  assert.equal(await copyText("th-http-key", { document: legacy.document }), true);
  assert.equal(legacy.textarea.value, "th-http-key");
  assert.ok(legacy.calls.some((call) => Array.isArray(call) && call[0] === "command" && call[1] === "copy"));
  assert.ok(legacy.calls.includes("remove"));
  assert.ok(legacy.calls.includes("restore-focus"));
});

test("uses the fallback when Clipboard API access is rejected", async () => {
  const legacy = fakeDocument();

  assert.equal(await copyText("th-fallback-key", {
    clipboard: { writeText: async () => { throw new Error("denied"); } },
    document: legacy.document,
  }), true);
  assert.ok(legacy.calls.some((call) => Array.isArray(call) && call[0] === "command"));
});

test("reports failure and removes the temporary textarea when both methods fail", async () => {
  const legacy = fakeDocument({ commandError: new Error("blocked") });

  assert.equal(await copyText("th-failed-key", { document: legacy.document }), false);
  assert.ok(legacy.calls.includes("remove"));
});
