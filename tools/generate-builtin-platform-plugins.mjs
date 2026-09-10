import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const toolsDir = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(toolsDir, "..");
const outputRoot = path.join(repositoryRoot, "data", "builtin-plugins", "platform");
const license = await readFile(path.join(repositoryRoot, "LICENSE"), "utf8");

const plugins = [
  provider("tokenhub.provider.mock", "Mock Provider", "mock", "Test gateway behavior without a remote model service."),
  provider("tokenhub.provider.openai-compatible", "OpenAI-Compatible", "openai_compatible", "Connect any service that implements the OpenAI-compatible API."),
  provider("tokenhub.provider.openai-codex", "OpenAI Codex Subscription", "openai_codex", "Connect an OpenAI Codex subscription account."),
  provider("tokenhub.provider.azure-openai", "Azure OpenAI", "azure_openai", "Connect TokenHub to an Azure OpenAI deployment."),
  provider("tokenhub.provider.qwen", "Qwen", "qwen", "Connect TokenHub to Qwen model services."),
  provider("tokenhub.provider.local", "Local Model Service", "local", "Connect TokenHub to a model service on the local network."),
  ui("tokenhub.admin.plugin-ecosystem", "TokenHub Plugin Ecosystem Dashboard", "Expose plugin operations and status in the admin console."),
  ui("tokenhub.admin.core-provider", "TokenHub Core Provider Settings", "Add host-managed Provider settings to the admin console."),
  template("tokenhub.sim.default", "TokenHub Default Interface Template", "Provide the default TokenHub admin console theme and layout."),
  template("tokenhub.sim.antd", "Ant Design Style Interface Template", "Provide an Ant Design style admin console theme and layout."),
  template("tokenhub.sim.knowledge-sidebar", "Knowledge Sidebar Interface Template", "Provide a knowledge-management style sidebar theme and layout."),
];

for (const plugin of plugins) {
  const directory = path.join(outputRoot, plugin.id);
  await mkdir(directory, { recursive: true });
  await writeFile(path.join(directory, "plugin.yaml"), manifest(plugin), "utf8");
  await writeFile(path.join(directory, "README.md"), readme(plugin), "utf8");
  await writeFile(path.join(directory, "LICENSE"), license, "utf8");
}

process.stdout.write(`Generated ${plugins.length} built-in platform plugin packages in ${path.relative(repositoryRoot, outputRoot)}\n`);

function provider(id, name, hostAdapter, summary) {
  return { id, name, hostAdapter, summary, category: "provider_integration", kinds: ["provider"], placements: [], settingsScopes: [] };
}

function ui(id, name, summary) {
  return { id, name, hostAdapter: "", summary, category: "ui_template", kinds: ["admin_ui"], placements: ["presentation"], settingsScopes: [] };
}

function template(id, name, summary) {
  return { id, name, hostAdapter: "", summary, category: "ui_template", kinds: ["sim"], placements: ["presentation"], settingsScopes: ["browser"] };
}

function manifest(plugin) {
  const hostAdapter = plugin.hostAdapter ? `host_adapter: ${JSON.stringify(plugin.hostAdapter)}\n` : "";
  const settings = plugin.settingsScopes.length > 0
    ? `settings:\n  scopes: ${JSON.stringify(plugin.settingsScopes)}\n`
    : "";
  return `schema_version: 2
id: ${JSON.stringify(plugin.id)}
name: ${JSON.stringify(plugin.name)}
version: 1.0.0
summary: ${JSON.stringify(plugin.summary)}
description: ${JSON.stringify(plugin.summary)}
category: ${plugin.category}
tokenhub:
  plugin_api: v2
  min_core: 0.7.0
${hostAdapter}${settings}distribution:
  license: Apache-2.0
kinds: ${JSON.stringify(plugin.kinds)}
placement: ${JSON.stringify(plugin.placements)}
permissions:
  network:
    allow: []
  data:
    read: []
    write: []
`;
}

function readme(plugin) {
  return `# ${plugin.name}

Built-in ${plugin.category.replaceAll("_", " ")} plugin for TokenHub.

- Plugin ID: \`${plugin.id}\`
- Configuration: ${plugin.category === "provider_integration" ? "Provider management" : "TokenHub admin console"}
- Lifecycle: installed, enabled, configured, and used independently
`;
}
