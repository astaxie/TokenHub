import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, sep } from "node:path";

export const GENERIC_CORE_SURFACES = [
  "backend/internal/server/admin_plugin_actions.go",
  "backend/internal/server/admin_provider_resources_routing.go",
  "backend/internal/server/admin_providers_http.go",
  "backend/internal/server/gateway_compact_http.go",
  "backend/internal/server/gateway_embeddings_http.go",
  "backend/internal/server/gateway_http.go",
  "backend/internal/server/gateway_responses_execution.go",
  "backend/internal/server/gateway_routed_execution.go",
  "backend/internal/server/model_catalog_data.go",
  "backend/internal/server/provider_headers.go",
  "backend/internal/server/provider_adapter_migration.go",
  "backend/internal/server/provider_catalog.go",
  "backend/internal/server/provider_monitoring.go",
  "backend/internal/server/provider_model_categories.go",
  "backend/internal/server/provider_plugin_runtime.go",
  "backend/internal/server/provider_route_bridge.go",
  "frontend/features/admin/domain/provider-account-catalog.ts",
  "frontend/features/admin/domain/catalog.tsx",
  "frontend/features/admin/domain/model-categories.ts",
  "frontend/features/admin/domain/provider-resource-types.ts",
  "frontend/features/admin/domain/provider-headers.ts",
  "frontend/features/admin/resources/payloads.tsx",
  "frontend/features/admin/resources/project-key-config.tsx",
  "frontend/features/admin/resources/provider-model-config.tsx",
  "frontend/features/admin/views/provider-editor.tsx",
  "frontend/features/admin/views/provider-account-quota-reset.tsx",
  "frontend/features/admin/views/provider-plugin-form-sections.tsx",
  "frontend/features/admin/views/provider-plugin-panels.tsx",
];

export const PROVIDER_CORE_PATTERNS = [
  {
    name: "provider constant branch",
    pattern: /\bProvider(?:OpenAICodex|Anthropic|Gemini)\b/g,
  },
  {
    name: "provider type literal",
    pattern: /(["'`])(?:openai_codex|openai-codex|codex|anthropic|gemini)\1/g,
  },
  {
    name: "provider-specific auth identifier",
    pattern: /\banthropicAuthType[A-Za-z0-9_]*\b/g,
  },
  {
    name: "provider-specific quota contract",
    pattern: /\b(?:OpenAIAccountQuota|OpenAIAccountQuotaWindow|queryOpenAIAccountQuota)\b/g,
  },
  {
    name: "provider catalog URL normalizer",
    pattern: /\bnormalizeOpenAICompatibleBaseURL\b/g,
  },
  {
    name: "provider-specific catalog URL literal",
    pattern: /(["'`])(?:dmxapi|302ai|https:\/\/www\.dmxapi\.cn|https:\/\/api\.dmxapi\.cn|api\.highwayapi\.ai\/openai)\1/g,
  },
  {
    name: "provider-specific frontend OAuth endpoint",
    pattern: /(["'`])\/api\/admin\/provider-account-oauth\/openai\/(?:generate-auth-url|exchange-code)\1/g,
  },
  {
    name: "provider-specific frontend quota reset compatibility",
    pattern: /(["'`])(?:tokenhub\.codex-quota-reset\.|openai_quota_reset_[^"'`]*)\1/g,
  },
  {
    name: "provider model base URL placeholder",
    pattern: /(["'`])https:\/\/api\.openai\.com\/v1\1/g,
  },
];

const JS_TS_EXTENSIONS = new Set([".js", ".mjs", ".ts", ".tsx"]);

export function providerCoreHardcodingViolations(root, surfaces = GENERIC_CORE_SURFACES) {
  const violations = [];
  for (const surface of surfaces) {
    const path = join(root, surface);
    if (!exists(path)) continue;
    const source = readFileSync(path, "utf8");
    violations.push(...scanProviderCoreHardcoding(source, surface));
  }
  return violations.sort((left, right) => left.path.localeCompare(right.path) || left.line - right.line || left.column - right.column);
}

export function scanProviderCoreHardcoding(source, path = "source") {
  const code = stripSourceComments(source, path);
  const violations = [];
  for (const rule of PROVIDER_CORE_PATTERNS) {
    for (const match of code.matchAll(rule.pattern)) {
      const position = lineColumnAt(code, match.index ?? 0);
      violations.push({
        path,
        line: position.line,
        column: position.column,
        rule: rule.name,
        match: match[0],
      });
    }
  }
  return violations;
}

export function renderProviderCoreHardcodingViolations(violations) {
  if (violations.length === 0) return "";
  return violations
    .map((violation) => `- ${violation.path}:${violation.line}:${violation.column} ${violation.rule}: ${violation.match}`)
    .join("\n");
}

export function discoverSourceFiles(root, directory, extensions = JS_TS_EXTENSIONS) {
  const files = [];
  collect(root, join(root, directory), extensions, files);
  return files.sort();
}

function collect(root, directory, extensions, files) {
  let entries;
  try {
    entries = readdirSync(directory, { withFileTypes: true });
  } catch (error) {
    if (error.code === "ENOENT") return;
    throw error;
  }
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules" || entry.name === ".next" || entry.name === ".git") continue;
      collect(root, path, extensions, files);
    } else if (entry.isFile() && extensions.has(extensionOf(entry.name))) {
      files.push(relative(root, path).split(sep).join("/"));
    }
  }
}

function stripSourceComments(source, path) {
  if (path.endsWith(".go")) return stripSlashComments(source);
  if ([...JS_TS_EXTENSIONS].some((extension) => path.endsWith(extension))) return stripSlashComments(source);
  return source;
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

function exists(path) {
  try {
    return statSync(path).isFile();
  } catch (error) {
    if (error.code === "ENOENT") return false;
    throw error;
  }
}

function extensionOf(name) {
  const index = name.lastIndexOf(".");
  return index === -1 ? "" : name.slice(index);
}
