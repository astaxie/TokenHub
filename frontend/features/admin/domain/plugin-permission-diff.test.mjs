import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  pluginPermissionDiffDisplay,
} = await importTypeScript(new URL("./plugin-permission-diff.ts", import.meta.url));

test("plugin permission diff display defaults old backend payloads safely", () => {
  const state = pluginPermissionDiffDisplay({});

  assert.equal(state.available, false);
  assert.equal(state.operation, "unknown");
  assert.equal(state.verdict, "unknown");
  assert.equal(state.tone, "neutral");
  assert.equal(state.requiresReview, false);
  assert.equal(state.requiresApproval, false);
  assert.deepEqual(state.summary, {
    added: 0,
    removed: 0,
    unchanged: 0,
    changed_sensitivity: 0,
  });
  assert.deepEqual(state.sections.map((section) => [section.id, section.count]), [
    ["added", 0],
    ["removed", 0],
    ["changed_sensitivity", 0],
    ["unchanged", 0],
  ]);
  assert.equal(state.trust.verdict, "unverified");
  assert.equal(state.trust.tone, "neutral");
  assert.equal(state.compatibility.verdict, "unknown");
  assert.equal(state.compatibility.tone, "neutral");
});

test("plugin permission diff display normalizes approval-required secret additions", () => {
  const state = pluginPermissionDiffDisplay({
    operation: "install",
    plugin_id: "tokenhub.provider.codex",
    candidate_version: "2.0.0",
    permission_diff: {
      available: true,
      verdict: "approval_required",
      reason_code: "secret_permission_added",
      highest_sensitivity: "secret",
      summary: {
        added: 2,
        removed: 0,
        unchanged: 1,
        changed_sensitivity: 0,
      },
      added: [
        { kind: "data", name: "provider_credentials", access: "read", sensitivity: "secret" },
        { kind: "network", name: "https://api.example.com/v1", access: "connect", sensitivity: "sensitive" },
      ],
      unchanged: [
        { kind: "data", name: "audit", access: "write", sensitivity: "internal" },
      ],
    },
    trust: {
      verdict: "trusted",
      checksum_present: true,
      signature_present: true,
      signature_algorithm: "ed25519",
      signature_key_id: "official-2026",
    },
    compatibility: {
      plugin_api: "1",
      manifest_schema_version: 1,
      core_version: "2.0.0",
      verdict: "compatible",
    },
  });

  assert.equal(state.available, true);
  assert.equal(state.operation, "install");
  assert.equal(state.pluginID, "tokenhub.provider.codex");
  assert.equal(state.currentVersion, "");
  assert.equal(state.candidateVersion, "2.0.0");
  assert.equal(state.verdict, "approval_required");
  assert.equal(state.verdictLabelKey, "需要批准");
  assert.equal(state.tone, "error");
  assert.equal(state.reasonLabelKey, "新增密钥权限");
  assert.equal(state.highestSensitivity, "secret");
  assert.equal(state.highestSensitivityTone, "error");
  assert.equal(state.requiresReview, true);
  assert.equal(state.requiresApproval, true);
  assert.equal(state.sections[0].id, "added");
  assert.equal(state.sections[0].count, 2);
  assert.deepEqual(state.sections[0].changes.map((change) => [change.kind, change.name, change.sensitivity, change.sensitivityTone]), [
    ["data", "provider_credentials", "secret", "error"],
    ["network", "https://api.example.com/v1", "sensitive", "warn"],
  ]);
  assert.equal(state.trust.labelKey, "可信");
  assert.equal(state.trust.tone, "ok");
  assert.equal(state.trust.signatureKeyID, "official-2026");
  assert.equal(state.compatibility.labelKey, "兼容");
  assert.equal(state.compatibility.tone, "ok");
});

test("plugin permission diff display derives fallback summaries from change arrays", () => {
  const state = pluginPermissionDiffDisplay({
    operation: "update",
    plugin_id: "tokenhub.extension.cache",
    current_version: "1.0.0",
    candidate_version: "1.1.0",
    permission_diff: {
      available: true,
      verdict: "review_required",
      reason_code: "permission_added",
      added: [
        { kind: "data", name: "usage", access: "read", sensitivity: "internal" },
      ],
      removed: [
        { kind: "data", name: "audit", access: "write", sensitivity: "internal" },
      ],
      changed_sensitivity: [
        {
          kind: "network",
          name: "api.example.com",
          access: "connect",
          sensitivity: "sensitive",
          previous_sensitivity: "internal",
          candidate_sensitivity: "sensitive",
        },
      ],
    },
  });

  assert.equal(state.operation, "update");
  assert.deepEqual(state.summary, {
    added: 1,
    removed: 1,
    unchanged: 0,
    changed_sensitivity: 1,
  });
  assert.equal(state.highestSensitivity, "sensitive");
  assert.equal(state.sections[2].id, "changed_sensitivity");
  assert.equal(state.sections[2].changes[0].previousSensitivity, "internal");
  assert.equal(state.sections[2].changes[0].candidateSensitivity, "sensitive");
  assert.equal(state.sections[2].changes[0].previousSensitivityLabelKey, "内部");
  assert.equal(state.sections[2].changes[0].candidateSensitivityLabelKey, "敏感");
});

test("plugin permission diff display keeps unknown future values neutral and visible", () => {
  const state = pluginPermissionDiffDisplay({
    operation: "replace",
    permission_diff: {
      available: true,
      verdict: "delegated_approval",
      reason_code: "policy_escalated",
      highest_sensitivity: "restricted",
      summary: {
        added: -1,
        removed: 4.5,
        unchanged: 2,
        changed_sensitivity: 1,
      },
      added: [
        { kind: "", name: "", access: "", sensitivity: "restricted" },
      ],
    },
    trust: {
      verdict: "quarantined",
      reason_code: "advisory_hold",
    },
    compatibility: {
      verdict: "future_review",
      manifest_schema_version: -1,
      reason_code: "future_schema",
    },
  });

  assert.equal(state.operation, "unknown");
  assert.equal(state.verdict, "unknown");
  assert.equal(state.rawVerdict, "delegated_approval");
  assert.equal(state.tone, "neutral");
  assert.equal(state.reasonCode, "policy_escalated");
  assert.equal(state.reasonLabelKey, "未知原因");
  assert.equal(state.highestSensitivity, "unknown");
  assert.equal(state.summary.added, 1);
  assert.equal(state.summary.removed, 0);
  assert.equal(state.summary.unchanged, 2);
  assert.equal(state.summary.changed_sensitivity, 1);
  assert.equal(state.sections[0].changes[0].kind, "unknown");
  assert.equal(state.sections[0].changes[0].name, "unknown");
  assert.equal(state.sections[0].changes[0].access, "unknown");
  assert.equal(state.trust.verdict, "quarantined");
  assert.equal(state.trust.labelKey, "未验证");
  assert.equal(state.trust.tone, "neutral");
  assert.equal(state.compatibility.verdict, "future_review");
  assert.equal(state.compatibility.labelKey, "未知");
  assert.equal(state.compatibility.manifestSchemaVersion, undefined);
});

test("plugin permission diff display does not require raw install request fields", () => {
  const input = {
    plugin_id: "tokenhub.provider.kimi",
    candidate_version: "1.0.0",
    permission_diff: {
      available: true,
      verdict: "allow",
      reason_code: "unchanged",
      summary: {
        added: 0,
        removed: 0,
        unchanged: 0,
        changed_sensitivity: 0,
      },
    },
    download_url: "https://plugins.example/kimi.zip",
    checksum_sha256: "a".repeat(64),
    signature_url: "https://plugins.example/kimi.zip.sig",
    public_key: "secret-public-key-material",
  };

  const state = pluginPermissionDiffDisplay(input);

  assert.equal(state.verdict, "allow");
  assert.equal(state.tone, "ok");
  assert.equal(JSON.stringify(state).includes("plugins.example"), false);
  assert.equal(JSON.stringify(state).includes("secret-public-key-material"), false);
  assert.equal(JSON.stringify(state).includes("a".repeat(64)), false);
});
