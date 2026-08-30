import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
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
      const mode = anthropicAuthTypeBearer;
      const normalized = normalizeOpenAICompatibleBaseURL("dmxapi", "https://www.dmxapi.cn");
      const oauth = "/api/admin/provider-account-oauth/openai/generate-auth-url";
    `, "backend/internal/server/gateway_http.go");

    assert.deepEqual(violations.map((violation) => violation.rule), [
      "provider constant branch",
      "provider type literal",
      "provider type literal",
      "provider-specific auth identifier",
      "provider catalog URL normalizer",
      "provider-specific catalog URL literal",
      "provider-specific catalog URL literal",
      "provider-specific frontend OAuth endpoint",
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
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_provider_catalog.go"), false);
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_provider_plugins.go"), false);
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_provider_runtime.go"), false);
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_plugin_actions.go"), false);
    assert.equal(GENERIC_CORE_SURFACES.includes("backend/internal/server/builtin_admin_ui_plugins.go"), false);
  });

  it("does not reintroduce provider-specific checks in generic core surfaces", () => {
    const violations = providerCoreHardcodingViolations(REPOSITORY_ROOT);
    assert.deepEqual(violations, [], renderProviderCoreHardcodingViolations(violations));
  });

  it("does not scan every provider-specific adapter file by accident", () => {
    const frontendSources = discoverSourceFiles(REPOSITORY_ROOT, "frontend/features/admin");
    assert.ok(GENERIC_CORE_SURFACES.includes("backend/internal/server/provider_catalog.go"));
    assert.ok(frontendSources.includes("frontend/features/admin/views/provider-editor.tsx"));
    assert.ok(GENERIC_CORE_SURFACES.includes("frontend/features/admin/domain/catalog.tsx"));
    assert.equal(GENERIC_CORE_SURFACES.length < frontendSources.length, true);
  });

  it("keeps Codex live model catalog parsing outside generic provider account model cache", () => {
    const source = readFileSync(join(REPOSITORY_ROOT, "backend/internal/server/provider_account_models.go"), "utf8");
    const forbidden = [
      /\bcodexRemoteModelsResponse\b/,
      /\bcodexRemoteModel\b/,
      /\bCodexSubscriptionAdapter\)\s+Models\b/,
      /\bCodexSubscriptionAdapter\)\s+ModelsWithCredentials\b/,
      /\bCodexSubscriptionAdapter\)\s+ModelsWithETag\b/,
      /\bCodexSubscriptionAdapter\)\s+ResourceModels\b/,
      /\bmodelsWithCredentials\b/,
      /\bqueryOpenAICodexModels\b/,
      /\bcodexProviderCatalogFromModels\b/,
      /\bcodexProviderCatalogMetadata\b/,
      /\bcodexResourceSupportedModelsOption\b/,
      /\bcodexResourceModelsFetchedAtOption\b/,
      /\bcodexResourceModelsETagOption\b/,
      /\bcodexResourceModelCatalogOption\b/,
      /"codex_supported_models"/,
      /"codex_models_fetched_at"/,
      /"codex_models_etag"/,
      /"codex_model_catalog"/,
    ];
    const matches = forbidden.flatMap((pattern) => source.match(pattern) ?? []);
    assert.deepEqual(matches, []);
  });

  it("keeps provider editor Base URL defaults metadata-driven", () => {
    const source = readFileSync(join(REPOSITORY_ROOT, "frontend/features/admin/views/provider-editor.tsx"), "utf8");
    assert.equal(source.includes("https://api.openai.com/v1"), false);
  });
});
