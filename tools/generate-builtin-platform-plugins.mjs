import { mkdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const toolsDir = path.dirname(fileURLToPath(import.meta.url));
const repositoryRoot = path.resolve(toolsDir, "..");
const outputRoot = path.join(repositoryRoot, "data", "builtin-plugins", "platform");
export const plugins = [
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
  nativeSubscription("github-copilot-subscription", "GitHub Copilot Subscription", "github-copilot-subscription", "Connect a GitHub Copilot subscription with device authorization."),
  nativeSubscription("chatgpt-subscription", "ChatGPT API", "chatgpt-subscription", "Connect the direct ChatGPT API with dynamic client authorization."),
  nativeSubscription("gemini-cli", "Gemini CLI", "gemini_cli_subscription", "Connect a Google account to Gemini CLI Code Assist."),
  nativeSubscription("antigravity", "Antigravity", "antigravity_subscription", "Connect a Google account to the Antigravity subscription API."),
  nativeSubscription("claude-subscription", "Claude Code Subscription", "claude_subscription", "Use the genuine Claude Code CLI with an imported subscription account.", "claude"),
  ...[
    ["cursor", "Cursor"], ["kiro", "Kiro"], ["qoder", "Qoder"], ["qoder-cn", "Qoder China"],
    ["workbuddy", "WorkBuddy"], ["workbuddy-ai", "WorkBuddy AI"], ["mimo-app", "MiMo"], ["zcode", "ZCode"],
    ["factory", "Factory"], ["grok", "Grok"], ["devin", "Devin"], ["commandcode-plan", "Command Code"], ["zed", "Zed"],
    ["kimi-work", "Kimi Work"], ["minimax-code", "MiniMax Coding Plan"], ["minimax-code-global", "MiniMax Coding Plan Global"],
    ["cline", "Cline"], ["trae-cn", "Trae China"], ["trae-global", "Trae Global"], ["opendesign", "OpenDesign"],
    ["nous-portal", "Nous Portal"], ["raccoon", "Raccoon"], ["autoclaw", "AutoClaw"], ["autoclaw-cn", "AutoClaw China"],
    ["codearts", "CodeArts"], ["joycode", "JoyCode"],
  ].map(([id, name]) => workerSubscription(id, name)),
];

export async function generateBuiltinPlatformPlugins(destination = outputRoot) {
  const [license, notices] = await Promise.all([
    readFile(path.join(repositoryRoot, "LICENSE"), "utf8"),
    readFile(path.join(repositoryRoot, "backend", "internal", "server", "SUBSCRIPTION_THIRD_PARTY_NOTICES.md"), "utf8"),
  ]);
  for (const plugin of plugins) {
    const directory = path.join(destination, plugin.id);
    await mkdir(directory, { recursive: true });
    await writeFile(path.join(directory, "plugin.yaml"), manifest(plugin), "utf8");
    await writeFile(path.join(directory, "README.md"), readme(plugin), "utf8");
    await writeFile(path.join(directory, "LICENSE"), license, "utf8");
    if (plugin.subscription) await writeFile(path.join(directory, "THIRD_PARTY_NOTICES"), notices, "utf8");
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await generateBuiltinPlatformPlugins();
  process.stdout.write(`Generated ${plugins.length} built-in platform plugin packages in ${path.relative(repositoryRoot, outputRoot)}\n`);
}

function provider(id, name, hostAdapter, summary) {
  return { id, name, hostAdapter, summary, category: "provider_integration", kinds: ["provider"], placements: [], settingsScopes: [] };
}

function nativeSubscription(id, name, hostAdapter, summary, subscription = "native") {
  return { ...provider(`tokenhub.provider.${id}`, name, hostAdapter, summary), subscription };
}

function workerSubscription(id, name) {
  return {
    ...nativeSubscription(`${id}-subscription`, name, `${id.replaceAll("-", "_")}_subscription`, `Connect a ${name} subscription account.`, "worker"),
    workerID: id,
    description: `Connect an explicitly authorized ${name} subscription account through TokenHub's isolated bundled runtime.`,
  };
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
description: ${JSON.stringify(plugin.description ?? plugin.summary)}
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
  if (plugin.subscription === "claude") return `# Claude Code Subscription

Runs the genuine Claude Code CLI with an imported account token or \`claudeAiOauth\` session JSON. The CLI must be installed on the backend host. Account directories are private, built-in tools and inherited settings are disabled, and caller tools are exposed through a strict local MCP bridge.

Tool continuations are bound to the authenticated project, API key, account, model, and conversation. Each account supports one active conversation; unanswered tool calls expire after five minutes. CLI model aliases are listed locally; account entitlement is checked when the CLI invokes a model. Session rotations are encrypted back into the account resource.
`;
  if (plugin.subscription === "native") return `# ${plugin.name}

${plugin.summary}

- Plugin ID: \`${plugin.id}\`
- Configuration: Provider management, with a separate account resource
- Lifecycle: installed, enabled, configured, and used independently
- Credentials: encrypted account credentials; native host-managed OAuth
`;
  if (plugin.subscription === "worker") return `# ${plugin.name}

Connect an explicitly authorized ${plugin.name} account to TokenHub.

- Plugin ID: \`${plugin.id}\`
- Configuration: Provider management, with a separate account resource
- Lifecycle: installed, enabled, configured, and used independently
- Authentication: available browser/device flows or explicit session JSON import
- Credentials: encrypted per-account session; refresh rotations persist before acknowledgement
- Runtime: Node.js 22.23.1 or later; isolated account process with host-controlled network access
- Models: account model discovery; public access through OpenAI-compatible chat completions

The bundled upstream adapter's license and provenance are retained in
\`backend/internal/subscriptionworker/vendor/\`.
`;
  return `# ${plugin.name}

Built-in ${plugin.category.replaceAll("_", " ")} plugin for TokenHub.

- Plugin ID: \`${plugin.id}\`
- Configuration: ${plugin.category === "provider_integration" ? "Provider management" : "TokenHub admin console"}
- Lifecycle: installed, enabled, configured, and used independently
`;
}
