import assert from "node:assert/strict";
import { access, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { providerIDs } from "../backend/internal/subscriptionworker/registry.mjs";
import { generateBuiltinPlatformPlugins, plugins } from "./generate-builtin-platform-plugins.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const packageRoot = path.join(root, "data", "builtin-plugins", "platform");
const expected = [
  "tokenhub.provider.mock", "tokenhub.provider.openai-compatible", "tokenhub.provider.openai-codex",
  "tokenhub.provider.azure-openai", "tokenhub.provider.qwen", "tokenhub.provider.local",
  "tokenhub.admin.plugin-ecosystem", "tokenhub.admin.core-provider",
  "tokenhub.sim.default", "tokenhub.sim.antd", "tokenhub.sim.knowledge-sidebar",
  "tokenhub.provider.github-copilot-subscription", "tokenhub.provider.chatgpt-subscription",
  "tokenhub.provider.claude-subscription", "tokenhub.provider.gemini-cli", "tokenhub.provider.antigravity",
  ...providerIDs.map((id) => `tokenhub.provider.${id}-subscription`),
];

test("every non-catalog built-in plugin has an inspectable package", async () => {
  assert.equal(new Set(plugins.map(({ id }) => id)).size, expected.length);
  assert.deepEqual(plugins.map(({ id }) => id).sort(), [...expected].sort());
  assert.deepEqual((await readdir(packageRoot)).sort(), [...expected].sort());
  for (const id of expected) {
    const directory = path.join(packageRoot, id);
    await Promise.all(["plugin.yaml", "README.md", "LICENSE"].map((name) => access(path.join(directory, name))));
    const manifest = await readFile(path.join(directory, "plugin.yaml"), "utf8");
    assert.match(manifest, new RegExp(`^id: ${JSON.stringify(id).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`, "m"));
    assert.match(manifest, /^schema_version: 2$/m);
    assert.match(manifest, /^  plugin_api: v2$/m);
  }
});

test("community package names and host adapters match the Go and Node registries", async () => {
  const source = await readFile(path.join(root, "backend/internal/server/provider_subscription_worker.go"), "utf8");
  const definitions = source.split("func subscriptionWorkerDefinitions()")[1].split("func (d subscriptionWorkerDefinition)")[0];
  const hosts = [...definitions.matchAll(/\{"([^"]+)", "([^"]+)"\}/g)].map(([, id, name]) => ({ id, name }));
  assert.equal(hosts.length, providerIDs.length);
  assert.deepEqual(hosts.map(({ id }) => id).sort(), [...providerIDs].sort());
  for (const { id, name } of hosts) {
    const plugin = plugins.find((item) => item.workerID === id);
    assert.ok(plugin, `Missing generated package for ${id}`);
    assert.equal(plugin.id, `tokenhub.provider.${id}-subscription`);
    assert.equal(plugin.name, name);
    assert.equal(plugin.hostAdapter, `${id.replaceAll("-", "_")}_subscription`);
  }
});

test("native subscription packages match registered names and provider type constants", async () => {
  const directory = path.join(root, "backend/internal/server");
  const definitions = await readFile(path.join(directory, "builtin_subscription_providers.go"), "utf8");
  const sources = (await Promise.all(["copilot", "chatgpt", "google", "claude"].map((name) => readFile(path.join(directory, `provider_subscription_${name}.go`), "utf8")))).join("\n");
  const native = [...definitions.matchAll(/\{(\w+), "(tokenhub\.provider\.[^"]+)", "([^"]+)"/g)];
  for (const [, constant, id, name] of native) {
    const providerType = sources.match(new RegExp(`\\b${constant}\\s*=\\s*"([^"]+)"`))?.[1];
    assert.ok(providerType, `Missing host provider type ${constant}`);
    const plugin = plugins.find((item) => item.id === id);
    assert.ok(plugin, `Missing generated package for ${id}`);
    assert.equal(plugin.name, name);
    assert.equal(plugin.hostAdapter, providerType);
  }
  const claude = plugins.find(({ id }) => id === "tokenhub.provider.claude-subscription");
  assert.equal(claude.hostAdapter, sources.match(/\bproviderClaudeSubscription\s*=\s*"([^"]+)"/)?.[1]);
  const claudeRegistration = await readFile(path.join(directory, "provider_subscription_claude_catalog.go"), "utf8");
  assert.ok(claudeRegistration.includes(`register(${JSON.stringify(claude.id)}, ${JSON.stringify(claude.name)}, registration)`));
});

test("all new subscription packages preserve complete third-party notices", async () => {
  const notices = await readFile(path.join(root, "backend/internal/server/SUBSCRIPTION_THIRD_PARTY_NOTICES.md"), "utf8");
  assert.match(notices, /Copyright \(c\) 2026 yetone/);
  assert.match(notices, /Copyright \(c\) 2026 magpie-community contributors/);
  assert.equal(plugins.filter((plugin) => plugin.subscription).length, 5 + providerIDs.length);
  for (const plugin of plugins) {
    const noticeFile = path.join(packageRoot, plugin.id, "THIRD_PARTY_NOTICES");
    if (plugin.subscription) assert.equal(await readFile(noticeFile, "utf8"), notices, plugin.id);
    else await assert.rejects(access(noticeFile), { code: "ENOENT" });
  }
});

test("regeneration reproduces every checked-in package without deleting other files", async (t) => {
  const temporary = await mkdtemp(path.join(os.tmpdir(), "tokenhub-platform-plugins-"));
  t.after(() => rm(temporary, { recursive: true, force: true }));
  const sentinel = path.join(temporary, "unmanaged-file");
  await writeFile(sentinel, "Preserve independent packages and files.\n");
  await generateBuiltinPlatformPlugins(temporary);
  assert.equal(await readFile(sentinel, "utf8"), "Preserve independent packages and files.\n");
  for (const plugin of plugins) {
    const files = ["plugin.yaml", "README.md", "LICENSE", ...(plugin.subscription ? ["THIRD_PARTY_NOTICES"] : [])];
    for (const name of files) {
      assert.equal(await readFile(path.join(temporary, plugin.id, name), "utf8"), await readFile(path.join(packageRoot, plugin.id, name), "utf8"), `${plugin.id}/${name}`);
    }
  }
});
