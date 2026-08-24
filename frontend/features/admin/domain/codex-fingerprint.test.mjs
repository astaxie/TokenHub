import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  applyCodexFingerprintOption,
  defaultCodexFingerprintMode,
  normalizeCodexFingerprintMode,
} = await importTypeScript(new URL("./codex-fingerprint.ts", import.meta.url));

test("Codex fingerprint mode defaults to session", () => {
  assert.equal(defaultCodexFingerprintMode, "session");
  assert.equal(normalizeCodexFingerprintMode(), "session");
  assert.equal(normalizeCodexFingerprintMode("unknown"), "session");
});

test("non-default Codex fingerprint modes are persisted", () => {
  for (const mode of ["off", "device", "full"]) {
    assert.deepEqual(
      applyCodexFingerprintOption({}, { resource_type: "openai_subscription", codex_fingerprint_mode: mode }),
      { codex_fingerprint_mode: mode },
    );
  }
});

test("default session mode is represented by an absent option", () => {
  assert.deepEqual(
    applyCodexFingerprintOption(
      { codex_fingerprint_mode: "off", preserved: "value" },
      { resource_type: "openai_subscription", codex_fingerprint_mode: "session" },
    ),
    { preserved: "value" },
  );
});

test("non-Codex resources cannot retain the fingerprint option", () => {
  assert.deepEqual(
    applyCodexFingerprintOption(
      { codex_fingerprint_mode: "full", preserved: "value" },
      { resource_type: "api_key", codex_fingerprint_mode: "full" },
    ),
    { preserved: "value" },
  );
});
