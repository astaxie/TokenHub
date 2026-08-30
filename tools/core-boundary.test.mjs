import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { describe, it } from "node:test";
import { fileURLToPath } from "node:url";

import {
  CORE_BOUNDARY_SURFACES,
  coreBoundaryViolations,
  renderCoreBoundaryViolations,
  scanCoreBoundary,
  scanImports,
} from "./core-boundary.mjs";

const REPOSITORY_ROOT = join(dirname(fileURLToPath(import.meta.url)), "..");

describe("minimal core boundary gate", () => {
  it("reports backend core imports of plugin implementation packages", () => {
    const violations = scanCoreBoundary(`
      package plugin

      import "tokenhub/backend/internal/plugins/acme"

      var _ = acme.Plugin{}
    `, "backend/internal/plugin/manifest.go");

    assert.deepEqual(violations.map((violation) => violation.rule), ["backend-implementation-import"]);
  });

  it("reports external process runtime imports in core contract files", () => {
    const violations = scanCoreBoundary(`
      package plugin

      import (
        "context"
        "os/exec"
      )

      var _ = exec.CommandContext
    `, "backend/internal/plugin/gateway_runner.go");

    assert.deepEqual(violations.map((violation) => violation.rule), ["external-runtime-import"]);
  });

  it("reports marketplace concepts in gateway hot path files", () => {
    const violations = scanCoreBoundary(`
      package server

      const marketplace = "download_url"
    `, "backend/internal/server/gateway_http.go");

    assert.deepEqual(violations.map((violation) => violation.rule), [
      "gateway-marketplace-term",
      "gateway-marketplace-term",
    ]);
  });

  it("reports plugin attempts to own public v1 endpoint routing", () => {
    const violations = scanCoreBoundary(`
      package plugin

      type PublicEndpoint struct {
        Path string
      }

      const completionsEndpoint = "/v1/chat/completions"
    `, "backend/internal/plugin/manifest.go");

    assert.deepEqual(violations.map((violation) => violation.rule), [
      "public-v1-endpoint-ownership",
      "public-v1-endpoint-ownership",
    ]);
  });

  it("does not confuse plugin API v1 compatibility with public v1 endpoints", () => {
    const violations = scanCoreBoundary(`
      package plugin

      const CurrentPluginAPI = "v1"
      const ProviderPolicyRouteProtocol = "route_protocol"
    `, "backend/internal/plugin/api_contract.go");

    assert.deepEqual(violations, []);
  });

  it("reports plugin attempts to own credential storage", () => {
    const violations = scanCoreBoundary(`
      package plugin

      import "gorm.io/gorm"

      type ProviderCredentialStore struct {
        DB *gorm.DB
        CredentialBlob string
      }
    `, "backend/internal/plugin/manifest.go");

    assert.deepEqual(violations.map((violation) => violation.rule), [
      "sensitive-storage-ownership",
      "sensitive-storage-ownership",
      "sensitive-storage-ownership",
    ]);
  });

  it("keeps stable public v1 endpoints declared from server routes", () => {
    const routes = readFileSync(join(REPOSITORY_ROOT, "backend/internal/server/routes.go"), "utf8");
    for (const endpoint of [
      '"/v1/chat/completions"',
      '"/v1/responses"',
      '"/v1/embeddings"',
      '"/v1/images/generations"',
      '"/v1/images/edits"',
    ]) {
      assert.equal(routes.includes(endpoint), true, `${endpoint} must stay in the core server route table`);
    }
  });

  it("reports frontend core and domain imports from React views", () => {
    const violations = scanCoreBoundary(`
      import { PluginsView } from "../views/plugins";
      export const value = PluginsView;
    `, "frontend/features/admin/domain/plugin-manager.ts");

    assert.deepEqual(violations.map((violation) => violation.rule), ["frontend-view-import"]);
  });

  it("reports frontend core and domain imports from plugin implementation packages", () => {
    const violations = scanCoreBoundary(`
      import sample from "@/plugins/sample-provider";
      export const value = sample;
    `, "frontend/features/admin/core/types.tsx");

    assert.deepEqual(violations.map((violation) => violation.rule), ["frontend-plugin-implementation-import"]);
  });

  it("parses Go and TypeScript imports", () => {
    assert.deepEqual(scanImports(`import alias "os/exec"`, "backend/internal/plugin/manifest.go").map((item) => item.specifier), ["os/exec"]);
    assert.deepEqual(scanImports(`export { value } from "../views/plugins";`, "frontend/features/admin/domain/plugin-manager.ts").map((item) => item.specifier), ["../views/plugins"]);
  });

  it("does not scan built-in provider implementation files as generic core", () => {
    const surfaces = CORE_BOUNDARY_SURFACES.map((surface) => surface.path);
    assert.equal(surfaces.includes("backend/internal/server/builtin_provider_plugins.go"), false);
    assert.equal(surfaces.includes("backend/internal/server/codex_compat_route.go"), false);
    assert.equal(surfaces.includes("backend/internal/server/provider_plugin_adapter.go"), false);
  });

  it("does not report violations in the current explicit core surface set", () => {
    const violations = coreBoundaryViolations(REPOSITORY_ROOT);
    assert.deepEqual(violations, [], renderCoreBoundaryViolations(violations));
  });
});
