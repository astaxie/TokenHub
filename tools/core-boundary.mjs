import { readFileSync, statSync } from "node:fs";
import { join } from "node:path";

export const CORE_BOUNDARY_SURFACES = [
  {
    path: "backend/internal/plugin/manifest.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/descriptor.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/registry.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/gateway_chain.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/gateway_runner.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/action_broker.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/background_job.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/api_contract.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/plugin/provider_command.go",
    rules: ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"],
  },
  {
    path: "backend/internal/server/gateway_http.go",
    rules: ["backend-implementation-import", "gateway-marketplace-term"],
  },
  {
    path: "backend/internal/server/gateway_routed_execution.go",
    rules: ["backend-implementation-import", "gateway-marketplace-term"],
  },
  {
    path: "backend/internal/server/gateway_responses_execution.go",
    rules: ["backend-implementation-import", "gateway-marketplace-term"],
  },
  {
    path: "backend/internal/server/gateway_stream_transform_hooks.go",
    rules: ["backend-implementation-import", "gateway-marketplace-term"],
  },
  {
    path: "frontend/features/admin/core/types.tsx",
    rules: ["frontend-view-import", "frontend-plugin-implementation-import"],
  },
  {
    path: "frontend/features/admin/domain/plugin-actions.ts",
    rules: ["frontend-view-import", "frontend-plugin-implementation-import"],
  },
  {
    path: "frontend/features/admin/domain/plugin-manager.ts",
    rules: ["frontend-view-import", "frontend-plugin-implementation-import"],
  },
  {
    path: "frontend/features/admin/domain/plugin-marketplace.ts",
    rules: ["frontend-view-import", "frontend-plugin-implementation-import"],
  },
  {
    path: "frontend/features/admin/domain/sim-selection.ts",
    rules: ["frontend-view-import", "frontend-plugin-implementation-import"],
  },
];

export const CORE_BOUNDARY_RULES = {
  "backend-implementation-import": {
    message: "core backend files must not import plugin implementation packages",
    scan: scanBackendImplementationImports,
  },
  "external-runtime-import": {
    message: "core contract files must not import external process runtime packages",
    scan: scanExternalRuntimeImports,
  },
  "gateway-marketplace-term": {
    message: "gateway hot path files must not depend on marketplace implementation concepts",
    scan: scanGatewayMarketplaceTerms,
  },
  "public-v1-endpoint-ownership": {
    message: "plugin contracts must not own public /v1 endpoint routing",
    scan: scanPublicV1EndpointOwnership,
  },
  "sensitive-storage-ownership": {
    message: "plugin contracts must not own credential or secret persistence",
    scan: scanSensitiveStorageOwnership,
  },
  "frontend-view-import": {
    message: "frontend core/domain files must not import React views",
    scan: scanFrontendViewImports,
  },
  "frontend-plugin-implementation-import": {
    message: "frontend core/domain files must not import plugin implementation packages",
    scan: scanFrontendPluginImplementationImports,
  },
};

export function coreBoundaryViolations(root, surfaces = CORE_BOUNDARY_SURFACES) {
  const violations = [];
  for (const surface of surfaces) {
    const absolutePath = join(root, surface.path);
    if (!isFile(absolutePath)) continue;
    const source = readFileSync(absolutePath, "utf8");
    violations.push(...scanCoreBoundary(source, surface.path, surface.rules));
  }
  return sortViolations(violations);
}

export function scanCoreBoundary(source, path = "source", rules = ruleNamesForPath(path)) {
  const code = stripSlashComments(source);
  const violations = [];
  for (const ruleName of rules) {
    const rule = CORE_BOUNDARY_RULES[ruleName];
    if (!rule) throw new Error(`Unknown core boundary rule: ${ruleName}`);
    for (const violation of rule.scan(code, path)) {
      violations.push({ ...violation, path, rule: ruleName, message: rule.message });
    }
  }
  return sortViolations(violations);
}

export function renderCoreBoundaryViolations(violations) {
  if (violations.length === 0) return "";
  return violations
    .map((violation) => `- ${violation.path}:${violation.line}:${violation.column} ${violation.rule}: ${violation.match}`)
    .join("\n");
}

export function scanImports(source, path = "source") {
  if (path.endsWith(".go")) return scanGoImports(source);
  if (/\.[cm]?[jt]sx?$/.test(path)) return scanJavaScriptImports(source);
  return [];
}

function ruleNamesForPath(path) {
  if (path.startsWith("backend/internal/plugin/")) {
    return ["backend-implementation-import", "external-runtime-import", "public-v1-endpoint-ownership", "sensitive-storage-ownership"];
  }
  if (path.startsWith("backend/internal/server/gateway")) {
    return ["backend-implementation-import", "gateway-marketplace-term"];
  }
  if (path.startsWith("frontend/features/admin/core/") || path.startsWith("frontend/features/admin/domain/")) {
    return ["frontend-view-import", "frontend-plugin-implementation-import"];
  }
  return [];
}

function scanBackendImplementationImports(source, path) {
  return scanImports(source, path).flatMap((item) => {
    if (/^tokenhub\/backend\/(?:internal\/(?:builtin|plugins|providerplugins|builtinplugins)|plugins)\//.test(item.specifier)) {
      return [violationAt(source, item.index, item.specifier)];
    }
    return [];
  });
}

function scanExternalRuntimeImports(source, path) {
  return scanImports(source, path).flatMap((item) => {
    if (item.specifier === "os/exec") return [violationAt(source, item.index, item.specifier)];
    return [];
  });
}

function scanGatewayMarketplaceTerms(source) {
  const violations = [];
  for (const match of source.matchAll(/\b(?:Marketplace|marketplace|Advisory|advisory|ReleaseNotes|release_notes|download_url|signature_url)\b/g)) {
    violations.push(violationAt(source, match.index ?? 0, match[0]));
  }
  return violations;
}

function scanPublicV1EndpointOwnership(source) {
  const violations = [];
  const patterns = [
    /\b(?:HandleFunc|Handle|ServeHTTP|http\.Handler|http\.ServeMux)\b/g,
    /\b(?:PublicEndpoint|PublicRoute|GatewayEndpoint|EndpointPath|RoutePath)\b/g,
    /(["'`])\/v1\/[^"'`]*\1/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      violations.push(violationAt(source, match.index ?? 0, match[0]));
    }
  }
  return violations;
}

function scanSensitiveStorageOwnership(source, path) {
  const violations = [];
  for (const item of scanImports(source, path)) {
    if (/^(?:database\/sql|gorm\.io\/gorm|github\.com\/jmoiron\/sqlx)$/.test(item.specifier)) {
      violations.push(violationAt(source, item.index, item.specifier));
    }
  }
  const patterns = [
    /\b(?:CredentialBlob|SecretHash|EncryptedSecret|EncryptionKey|ProviderCredentialStore|CredentialStore)\b/g,
    /\b(?:EncryptProviderCredential|DecryptProviderCredential|StoreProviderCredential|LoadProviderCredential)s?\b/g,
    /(["'`])(?:credential_blob|secret_hash|encrypted_secret|provider_credential_store)\1/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      violations.push(violationAt(source, match.index ?? 0, match[0]));
    }
  }
  return violations;
}

function scanFrontendViewImports(source, path) {
  return scanImports(source, path).flatMap((item) => {
    if (/(^|\/|\\.\\.)views(\/|$)/.test(item.specifier) || item.specifier.startsWith("../views")) {
      return [violationAt(source, item.index, item.specifier)];
    }
    return [];
  });
}

function scanFrontendPluginImplementationImports(source, path) {
  return scanImports(source, path).flatMap((item) => {
    if (/^@?\/?(?:plugins|plugin-implementations|features\/admin\/plugins)(\/|$)/.test(item.specifier)) {
      return [violationAt(source, item.index, item.specifier)];
    }
    return [];
  });
}

function scanGoImports(source) {
  const imports = [];
  const single = /^\s*import\s+((?:[.\w]+\s+)?)"([^"]+)"/gm;
  for (const match of source.matchAll(single)) {
    imports.push({ specifier: match[2], index: match.index ?? 0 });
  }
  const blocks = source.matchAll(/^\s*import\s*\(([\s\S]*?)^\s*\)/gm);
  for (const block of blocks) {
    const blockStart = block.index ?? 0;
    for (const line of block[1].matchAll(/((?:[.\w]+\s+)?)"([^"]+)"/g)) {
      imports.push({ specifier: line[2], index: blockStart + (line.index ?? 0) });
    }
  }
  return imports;
}

function scanJavaScriptImports(source) {
  const imports = [];
  const patterns = [
    /\bimport\s+(?:[^"']+?\s+from\s+)?["']([^"']+)["']/g,
    /\bexport\s+[^"']+?\s+from\s+["']([^"']+)["']/g,
    /\bimport\s*\(\s*["']([^"']+)["']\s*\)/g,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      imports.push({ specifier: match[1], index: match.index ?? 0 });
    }
  }
  return imports;
}

function violationAt(source, index, match) {
  const position = lineColumnAt(source, index);
  return { line: position.line, column: position.column, match };
}

function sortViolations(violations) {
  return violations.sort((left, right) => {
    return left.path.localeCompare(right.path) || left.line - right.line || left.column - right.column || left.rule.localeCompare(right.rule);
  });
}

function stripSlashComments(source) {
  let out = "";
  let state = "code";
  for (let index = 0; index < source.length; index += 1) {
    const char = source[index];
    const next = source[index + 1];
    switch (state) {
      case "code":
        if (char === "/" && next === "/") {
          state = "line";
          out += "  ";
          index += 1;
        } else if (char === "/" && next === "*") {
          state = "block";
          out += "  ";
          index += 1;
        } else {
          if (char === '"' || char === "'" || char === "`") state = char;
          out += char;
        }
        break;
      case "line":
        if (char === "\n") {
          state = "code";
          out += char;
        } else {
          out += " ";
        }
        break;
      case "block":
        if (char === "*" && next === "/") {
          state = "code";
          out += "  ";
          index += 1;
        } else {
          out += char === "\n" ? char : " ";
        }
        break;
      case '"':
      case "'":
        out += char;
        if (char === "\\") {
          if (next !== undefined) {
            out += next;
            index += 1;
          }
        } else if (char === state) {
          state = "code";
        }
        break;
      case "`":
        out += char;
        if (char === "`") state = "code";
        break;
    }
  }
  return out;
}

function lineColumnAt(source, index) {
  const before = source.slice(0, index);
  const lines = before.split(/\r?\n/);
  return { line: lines.length, column: lines[lines.length - 1].length + 1 };
}

function isFile(path) {
  try {
    return statSync(path).isFile();
  } catch (error) {
    if (error.code === "ENOENT") return false;
    throw error;
  }
}
