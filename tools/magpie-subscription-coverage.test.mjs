import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { providerIDs } from "../backend/internal/subscriptionworker/registry.mjs";
import { plugins } from "./generate-builtin-platform-plugins.mjs";

const root = new URL("../", import.meta.url);
const read = (file) => readFile(new URL(file, root), "utf8");
const inventory = JSON.parse(await read("tools/magpie-subscription-coverage.json"));
const registryText = await read(inventory.sources.community.snapshot_file);
const upstreamRegistry = JSON.parse(registryText);
const entries = inventory.providers;
const workerEntries = entries.filter((entry) => entry.integration === "worker");
const nativeEntries = entries.filter((entry) => entry.integration === "native");
const ids = (values) => values.map((entry) => entry.upstream_id).sort();
const escape = (value) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

function classifications(records, exclusions) {
  const classified = new Map();
  for (const entry of [...records, ...exclusions.filter((entry) => entry.kind === "provider")]) {
    assert.ok(!classified.has(entry.upstream_id), `Duplicate classification: ${entry.upstream_id}`);
    classified.set(entry.upstream_id, entry);
  }
  const upstream = new Set(inventory.sources.magpie.account_ids);
  for (const plugin of upstreamRegistry.plugins) for (const id of plugin.providers ?? []) upstream.add(id);
  assert.deepEqual([...classified.keys()].sort(), [...upstream].sort(), "Each pinned upstream account/provider ID needs an integration or an explicit exclusion");
  return classified;
}

test("coverage records pin auditable upstream inventories and complete licenses", async () => {
  assert.equal(inventory.schema_version, 1);
  assert.equal(inventory.sources.magpie.commit, "d580537201938b9f510070a213e09b46479e53ad");
  assert.equal(inventory.sources.community.commit, "5c9b7a413756c2b4f139290047085075c83eb3e8");
  assert.equal(createHash("sha256").update(registryText).digest("hex"), inventory.sources.community.inventory_sha256);
  const provenance = JSON.parse(await read("backend/internal/subscriptionworker/vendor/PROVENANCE.json"));
  assert.equal(provenance.repository, inventory.sources.community.repository);
  assert.equal(provenance.commit, inventory.sources.community.commit);
  const declaration = inventory.sources.magpie.account_ids_declaration.match(/^var accountIDs = \[\]string\{(.+)\}$/)?.[1];
  assert.ok(declaration, "Retain the exact account registry declaration for source review");
  const nativeIDs = declaration.split(",").map((token) => {
    const value = token.trim();
    const id = value.startsWith('"') ? JSON.parse(value) : inventory.sources.magpie.resolved_constants[value];
    assert.ok(id, `Unresolved upstream account constant: ${value}`);
    return id;
  });
  assert.deepEqual(nativeIDs, inventory.sources.magpie.account_ids);
  for (const source of Object.values(inventory.sources)) {
    assert.match(source.inventory_sha256, /^[a-f0-9]{64}$/);
    assert.match(await read(source.license_file), /Permission is hereby granted, free of charge/);
  }
});

test("every pinned upstream provider is classified and exclusions cannot conceal account integrations", () => {
  const classified = classifications(entries, inventory.exclusions);
  const communityIDs = new Set(upstreamRegistry.plugins.flatMap((entry) => entry.providers ?? []));
  const nativeOnly = inventory.sources.magpie.account_ids.filter((id) => !communityIDs.has(id)).sort();
  assert.deepEqual(ids(nativeEntries), nativeOnly);
  for (const plugin of upstreamRegistry.plugins) {
    for (const id of plugin.providers ?? []) {
      assert.equal(classified.get(id).source, "community", id);
      assert.equal(classified.get(id).upstream_package, plugin.package, id);
    }
  }
  assert.deepEqual(inventory.exclusions.filter((entry) => entry.kind === "provider").map((entry) => entry.upstream_id).sort(),
    ["cloudflare-workers-ai", "opencode-zen-free"]);
  for (const entry of inventory.exclusions) {
    const plugin = upstreamRegistry.plugins.find((item) => item.package === entry.upstream_package);
    assert.ok(plugin, entry.upstream_id);
    assert.equal(plugin.kind ?? "provider", entry.kind, entry.upstream_id);
    assert.ok(entry.reason.length > 30, entry.upstream_id);
    assert.ok(!entries.some((item) => item.upstream_id === entry.upstream_id), entry.upstream_id);
    assert.ok(!providerIDs.includes(entry.upstream_id), entry.upstream_id);
  }
  const kimi = inventory.exclusions.find((entry) => entry.upstream_id === "kimi-desktop");
  assert.equal(kimi.kind, "agent");
  assert.equal(kimi.reason_code, "agent_connector");
  assert.ok(workerEntries.some((entry) => entry.upstream_id === "kimi-work"));
  assert.throws(() => classifications(entries.filter((entry) => entry.upstream_id !== "qwenwork-enterprise"), inventory.exclusions), /Each pinned upstream/);
});

test("worker coverage matches every actual Node and Go registration by identity", async () => {
  assert.deepEqual(ids(workerEntries), [...providerIDs].sort());
  const go = await read("backend/internal/server/provider_subscription_worker.go");
  const declaration = go.match(/func subscriptionWorkerDefinitions\(\)[\s\S]+?\n}/)?.[0];
  assert.ok(declaration);
  const definitions = [...declaration.matchAll(/\{"([^"]+)", "([^"]+)"\}/g)].map(([, id, name]) => ({ id, name }));
  assert.deepEqual(definitions.map((entry) => entry.id).sort(), ids(workerEntries));
  for (const entry of workerEntries) {
    const id = entry.upstream_id;
    assert.equal(entry.source, "community", id);
    assert.equal(entry.tokenhub.provider_type, id.replaceAll("-", "_") + "_subscription", id);
    assert.equal(entry.tokenhub.resource_type, id.replaceAll("-", "_") + "_account", id);
    assert.equal(entry.tokenhub.plugin_id, `tokenhub.provider.${id}-subscription`, id);
    const plugin = plugins.find((item) => item.workerID === id);
    assert.ok(plugin, `Missing package generator entry: ${id}`);
    assert.equal(plugin.name, definitions.find((item) => item.id === id).name);
    assert.equal(plugin.hostAdapter, entry.tokenhub.provider_type, id);
  }
});

test("native coverage maps upstream IDs to real TokenHub registration constants", async () => {
  for (const entry of nativeEntries) {
    const mapping = entry.tokenhub;
    assert.equal(entry.source, "magpie");
    const constants = await read(mapping.constant_file);
    const value = constants.match(new RegExp(`\\b${escape(mapping.provider_constant)}\\s*=\\s*"([^"]+)"`))?.[1];
    assert.equal(value, mapping.provider_type, entry.upstream_id);
    if (mapping.resource_constant) {
      const resource = constants.match(new RegExp(`\\b${escape(mapping.resource_constant)}\\s*=\\s*"([^"]+)"`))?.[1];
      assert.equal(resource, mapping.resource_type, entry.upstream_id);
    }
    const registration = await read(mapping.registration_file);
    if (!mapping.resource_constant) assert.ok(registration.includes(JSON.stringify(mapping.resource_type)), entry.upstream_id);
    assert.ok(registration.includes(JSON.stringify(mapping.plugin_id)), entry.upstream_id);
    assert.match(registration, new RegExp(`\\b${escape(mapping.provider_constant)}\\b`));
    assert.ok(plugins.some((plugin) => plugin.id === mapping.plugin_id && plugin.hostAdapter === mapping.provider_type), entry.upstream_id);
  }
});

test("every integrated provider has a checked-in package with its exact host adapter", async () => {
  const integrated = plugins.filter((plugin) => plugin.subscription || plugin.id === "tokenhub.provider.openai-codex");
  assert.deepEqual(entries.map((entry) => entry.tokenhub.plugin_id).sort(), integrated.map((plugin) => plugin.id).sort());
  for (const entry of entries) {
    const mapping = entry.tokenhub;
    const manifest = await read(`data/builtin-plugins/platform/${mapping.plugin_id}/plugin.yaml`);
    assert.match(manifest, new RegExp(`^id: ${escape(JSON.stringify(mapping.plugin_id))}$`, "m"));
    assert.match(manifest, new RegExp(`^host_adapter: ${escape(JSON.stringify(mapping.provider_type))}$`, "m"));
    assert.match(manifest, /^kinds: \["provider"\]$/m);
  }
});

test("coverage keeps unsupported optional capabilities explicit", () => {
  const gcpCredits = inventory.limitations.find((entry) => entry.providers.includes("gcp-ai") && entry.capability === "promotional_credit_balance");
  assert.equal(gcpCredits.status, "not_exposed");
  for (const limit of inventory.limitations) {
    assert.ok(limit.reason.length > 30);
    for (const id of limit.providers) assert.ok(entries.some((entry) => entry.upstream_id === id), id);
  }
});
