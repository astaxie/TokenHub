import assert from "node:assert/strict";
import { dirname, join } from "node:path";
import { describe, it } from "node:test";
import { fileURLToPath } from "node:url";

import {
  GENERIC_CORE_SURFACES,
  discoverSourceFiles,
  providerCoreHardcodingViolations,
  renderProviderCoreHardcodingViolations,
  scanProviderCoreHardcoding,
} from "./provider-core-hardcoding.mjs";

const REPOSITORY_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("provider-specific core hardcoding gate", () => {
  it("reports provider constants and provider type literals in generic core surfaces", () => {
    const violations = scanProviderCoreHardcoding(`
      if provider.Type == ProviderOpenAICodex {
        return "openai_codex";
      }
      if (item.key !== "codex") {
        return "legacy";
      }
    `, "backend/internal/server/gateway_http.go");

    assert.deepEqual(violations.map((violation) => violation.rule), [
      "provider constant branch",
      "provider type literal",
      "provider type literal",
    ]);
  });

  it("does not report provider names in comments or longer compatibility keys", () => {
    const violations = scanProviderCoreHardcoding(`
      // if provider.Type == ProviderOpenAICodex { return "codex" }
      const legacyProviderAuthModeField = "anthropic_auth_type";
      const errorCode = "codex_upstream_error";
    `, "frontend/features/admin/views/provider-editor.tsx");

    assert.deepEqual(violations, []);
  });

  it("keeps built-in provider declarations outside the generic surface list", () => {
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_provider_plugins.go"), false);
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_plugin_actions.go"), false);
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_admin_ui_plugins.go"), false);
  });

  it("does not reintroduce provider-specific checks in generic core surfaces", () => {
    const violations = providerCoreHardcodingViolations(REPOSITORY_ROOT);
    assert.deepEqual(violations, [], renderProviderCoreHardcodingViolations(violations));
  });

  it("does not scan every provider-specific adapter file by accident", () => {
    const frontendSources = discoverSourceFiles(REPOSITORY_ROOT, "frontend/features/admin");
    assert.ok(frontendSources.includes("frontend/features/admin/views/provider-editor.tsx"));
    assert.ok(GENERIC_CORE_SURFACES.includes("frontend/features/admin/domain/catalog.tsx"));
    assert.equal(GENERIC_CORE_SURFACES.length < frontendSources.length, true);
  });
});
