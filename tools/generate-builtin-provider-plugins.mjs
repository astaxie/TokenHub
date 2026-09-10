import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const toolsDir = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(toolsDir, "..");
const catalogPath = path.join(repositoryRoot, "data", "provider-catalog.json");
const outputRoot = path.join(repositoryRoot, "data", "builtin-plugins", "providers");
const license = await readFile(path.join(repositoryRoot, "LICENSE"), "utf8");
const catalog = JSON.parse(await readFile(catalogPath, "utf8"));

const nativePlugins = new Map([
  ["anthropic", { id: "tokenhub.provider.anthropic", adapter: "anthropic" }],
  ["deepseek", { id: "tokenhub.provider.deepseek", adapter: "deepseek" }],
  ["google", { id: "tokenhub.provider.gemini", adapter: "gemini" }],
  ["kronk", { id: "tokenhub.provider.kronk", adapter: "kronk" }],
  ["openai", { id: "tokenhub.provider.openai", adapter: "openai" }],
  ["siliconflow", { id: "tokenhub.provider-catalog.siliconflow", adapter: "openai_compatible" }],
]);

const englishNames = new Map([
  ["tencent-token-plan-enterprise-pro", "Tencent Cloud Token Plan Enterprise Pro"],
  ["tencent-token-plan-enterprise-auto", "Tencent Cloud Token Plan Enterprise Lite"],
  ["tencent-token-plan-general-personal", "Tencent Cloud General Token Plan Personal"],
  ["tencent-token-plan-hy-personal", "Tencent Cloud HY Token Plan Personal"],
]);

const entries = Object.entries(catalog.providers ?? {}).sort(([left], [right]) => left.localeCompare(right));
if (entries.length === 0) throw new Error("provider catalog is empty");

for (const [catalogID, value] of entries) {
  const native = nativePlugins.get(catalogID);
  const pluginID = native?.id ?? `tokenhub.provider-catalog.${catalogID.toLowerCase()}`;
  const adapter = native?.adapter ?? String(value.type || "openai_compatible").trim();
  const name = englishNames.get(catalogID) ?? String(value.name || value.display_name || catalogID).trim();
  const baseURL = String(value.base_url || value.api || "").trim();
  const defaultBaseURL = concreteHTTPURL(baseURL);
  const docURL = String(value.doc_url || value.doc || "").trim();
  const categories = Array.isArray(value.categories) ? value.categories.filter((item) => typeof item === "string" && item.trim()) : [];
  const summary = `Connect TokenHub to ${name}.`;
  const description = `${summary} Connection behavior is provided by the host-managed ${adapter} adapter.`;
  const directory = path.join(outputRoot, catalogID);
  await mkdir(directory, { recursive: true });
  await writeFile(path.join(directory, "plugin.yaml"), manifest({
    adapter,
    baseURL,
    defaultBaseURL,
    catalogID,
    categories,
    description,
    docURL,
    modelsCount: Array.isArray(value.models) ? value.models.length : 0,
    name,
    pluginID,
    summary,
  }), "utf8");
  await writeFile(path.join(directory, "catalog.json"), `${JSON.stringify({ id: catalogID, ...value }, null, 2)}\n`, "utf8");
  await writeFile(path.join(directory, "README.md"), readme({ adapter, catalogID, name, pluginID }), "utf8");
  await writeFile(path.join(directory, "LICENSE"), license, "utf8");
}

process.stdout.write(`Generated ${entries.length} built-in Provider plugin packages in ${path.relative(repositoryRoot, outputRoot)}\n`);

function manifest({ adapter, baseURL, catalogID, categories, defaultBaseURL, description, docURL, modelsCount, name, pluginID, summary }) {
  const defaultBaseURLDeclaration = defaultBaseURL ? `    default_base_url: ${quote(defaultBaseURL)}\n` : "";
  return `schema_version: 2
id: ${quote(pluginID)}
name: ${quote(name)}
version: 1.0.0
summary: ${quote(summary)}
description: ${quote(description)}
category: provider_integration
tokenhub:
  plugin_api: v2
  min_core: 0.7.0
host_adapter: ${quote(adapter)}
distribution:
  license: Apache-2.0
kinds:
  - provider
placement: []
capabilities:
  provider_types:
    - ${quote(adapter)}
  provider:
${defaultBaseURLDeclaration}    catalog:
      id: ${quote(catalogID)}
      name: ${quote(name)}
      display_name: ${quote(name)}
      type: ${quote(adapter)}
      base_url: ${quote(baseURL)}
      doc_url: ${quote(docURL)}
      categories: ${JSON.stringify(categories)}
      models_count: ${modelsCount}
permissions:
  network:
    allow: []
  data:
    read: []
    write: []
`;
}

function concreteHTTPURL(value) {
  if (!value || /[{}$<>]/.test(value)) return "";
  try {
    const url = new URL(value);
    return url.protocol === "http:" || url.protocol === "https:" ? value : "";
  } catch {
    return "";
  }
}

function readme({ adapter, catalogID, name, pluginID }) {
  return `# ${name}

Built-in Provider Integration for \`${catalogID}\`.

- Plugin ID: \`${pluginID}\`
- Host Adapter: \`${adapter}\`
- Configuration: TokenHub Provider management
- Lifecycle: installed, enabled, configured, and used independently

This package contains declarative provider metadata. Executable protocol handling remains inside the trusted TokenHub Host Adapter.
`;
}

function quote(value) {
  return JSON.stringify(String(value));
}
