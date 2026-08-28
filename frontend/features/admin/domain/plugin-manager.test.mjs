import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  pluginManagerDisplayState,
  pluginManagerDistributionReady,
  pluginManagerLifecycleState,
  pluginManagerNextStatus,
} = await importTypeScript(new URL("./plugin-manager.ts", import.meta.url));

test("Plugin Manager lifecycle defaults old plugin payloads to enabled state", () => {
  const state = pluginManagerDisplayState({
    plugin: {
      id: "tokenhub.provider.kimi",
      name: "Kimi Provider",
      version: "1.0.0",
      source: "marketplace",
      kinds: ["provider"],
      placements: ["gateway_chain"],
      capabilities: [],
    },
  });

  assert.equal(state.status, "enabled");
  assert.equal(state.labelKey, "已启用");
  assert.equal(state.tone, "ok");
  assert.equal(state.restartRequired, false);
  assert.equal(state.loadable, true);
  assert.equal(state.installed, true);
  assert.equal(state.actions.disable.available, true);
  assert.equal(state.actions.enable.available, false);
  assert.equal(state.actions.uninstall.available, true);
  assert.equal(state.nextStatus, "disabled");
});

test("Plugin Manager lifecycle prefers nested lifecycle fields over legacy top-level fields", () => {
  const state = pluginManagerLifecycleState({
    status: "enabled",
    restart_required: false,
    lifecycle: {
      status: "pending_restart",
      reason: "installed update",
      restart_required: true,
      health: "healthy",
      audit_event: "pending_restart",
      loadable: false,
    },
  });

  assert.equal(state.status, "pending_restart");
  assert.equal(state.rawStatus, "pending_restart");
  assert.equal(state.labelKey, "待重启");
  assert.equal(state.restartRequired, true);
  assert.equal(state.restartTextKey, "重启后生效");
  assert.equal(state.reason, "installed update");
  assert.equal(state.auditEvent, "pending_restart");
  assert.equal(state.loadable, false);
});

test("Plugin Manager lifecycle treats unknown statuses neutrally", () => {
  const state = pluginManagerLifecycleState({
    status: "queued_for_magic",
    health: "healthy",
  });

  assert.equal(state.status, "unknown");
  assert.equal(state.rawStatus, "queued_for_magic");
  assert.equal(state.labelKey, "未知");
  assert.equal(state.tone, "neutral");
  assert.equal(state.pillStatus, "unknown");
  assert.equal(state.unknownStatus, true);
  assert.equal(state.loadable, false);
});

test("Plugin Manager lifecycle surfaces validation failure as an unavailable operation state", () => {
  const state = pluginManagerDisplayState({
    plugin: {
      id: "tokenhub.local-failed",
      source: "local_file",
      status: "failed_validation",
      health: "unhealthy",
      last_error_code: "plugin_api_unsupported",
    },
  });

  assert.equal(state.status, "failed_validation");
  assert.equal(state.labelKey, "校验失败");
  assert.equal(state.tone, "error");
  assert.equal(state.lastErrorCode, "plugin_api_unsupported");
  assert.equal(state.loadable, false);
  assert.equal(state.actions.operation.available, false);
  assert.equal(state.actions.disable.available, false);
});

test("Plugin Manager lifecycle derives rollback availability from optional payload fields", () => {
  const state = pluginManagerDisplayState({
    plugin: {
      id: "tokenhub.local-rollback",
      source: "local_file",
      status: "enabled",
      lifecycle: {
        rollback_available: true,
        rollback_version: "1.0.0",
      },
    },
  });

  assert.equal(state.status, "rollback_available");
  assert.equal(state.labelKey, "可回滚");
  assert.equal(state.tone, "warn");
  assert.equal(state.rollbackAvailable, true);
  assert.equal(state.rollbackVersion, "1.0.0");
  assert.equal(state.actions.rollback.available, true);
  assert.equal(state.actions.operation.available, true);
});

test("Plugin Manager lifecycle requires a rollback version before enabling rollback action", () => {
  const state = pluginManagerDisplayState({
    plugin: {
      id: "tokenhub.local-rollback",
      source: "local_file",
      status: "rollback_available",
      rollback_available: true,
    },
  });

  assert.equal(state.status, "rollback_available");
  assert.equal(state.rollbackAvailable, false);
  assert.equal(state.actions.rollback.available, false);
  assert.equal(state.actions.rollback.disabledReason, "not_applicable");
});

test("Plugin Manager lifecycle prevents mutable operations for built-in and mandatory plugins", () => {
  const builtIn = pluginManagerDisplayState({
    plugin: { id: "tokenhub.provider.openai", source: "built_in", status: "enabled" },
  });
  const mandatory = pluginManagerDisplayState({
    plugin: { id: "tokenhub.local-required", source: "local_file", status: "mandatory", mandatory: true },
  });

  assert.equal(builtIn.actions.disable.available, false);
  assert.equal(builtIn.actions.disable.disabledReason, "built_in");
  assert.equal(builtIn.actions.uninstall.available, false);
  assert.equal(builtIn.nextStatus, undefined);
  assert.equal(mandatory.status, "mandatory");
  assert.equal(mandatory.actions.disable.available, false);
  assert.equal(mandatory.actions.disable.disabledReason, "mandatory");
  assert.equal(mandatory.actions.uninstall.available, false);
});

test("Plugin Manager lifecycle derives marketplace install and update actions without required marketplace fields", () => {
  const availablePlugin = {
    id: "tokenhub.marketplace.kimi",
    source: "marketplace",
    version: "1.1.0",
    distribution: {
      download_url: "https://plugins.example/kimi.zip",
      checksum_sha256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    },
  };

  const oldMarketplace = pluginManagerDisplayState({
    plugin: availablePlugin,
    marketplace: {},
  });
  const installedMarketplace = pluginManagerDisplayState({
    plugin: availablePlugin,
    marketplace: {
      installed: true,
      installed_version: "1.0.0",
      update_available: true,
    },
  });

  assert.equal(oldMarketplace.installed, false);
  assert.equal(oldMarketplace.actions.install.available, true);
  assert.equal(oldMarketplace.actions.update.available, false);
  assert.equal(oldMarketplace.actions.uninstall.available, false);
  assert.equal(installedMarketplace.installed, true);
  assert.equal(installedMarketplace.installedVersion, "1.0.0");
  assert.equal(installedMarketplace.updateAvailable, true);
  assert.equal(installedMarketplace.actions.install.available, false);
  assert.equal(installedMarketplace.actions.update.available, true);
});

test("Plugin Manager lifecycle blocks distribution-backed actions when package metadata is incomplete", () => {
  const plugin = {
    id: "tokenhub.marketplace.kimi",
    source: "marketplace",
    distribution: {
      download_url: "https://plugins.example/kimi.zip",
    },
  };
  const state = pluginManagerDisplayState({
    plugin,
    marketplace: {
      installed: false,
      update_available: true,
    },
  });

  assert.equal(pluginManagerDistributionReady(plugin), false);
  assert.equal(state.distributionReady, false);
  assert.equal(state.actions.install.available, false);
  assert.equal(state.actions.install.disabledReason, "missing_distribution");
  assert.equal(state.actions.update.available, false);
  assert.equal(state.actions.update.disabledReason, "missing_distribution");
});

test("Plugin Manager lifecycle derives next status only for direct toggleable states", () => {
  assert.equal(pluginManagerNextStatus("disabled"), "enabled");
  assert.equal(pluginManagerNextStatus("enabled"), "disabled");
  assert.equal(pluginManagerNextStatus("rollback_available"), "disabled");
  assert.equal(pluginManagerNextStatus("pending_restart"), undefined);
  assert.equal(pluginManagerNextStatus("failed_validation"), undefined);
  assert.equal(pluginManagerNextStatus("mandatory"), undefined);
  assert.equal(pluginManagerNextStatus("unknown"), undefined);
});

test("Plugin Manager display state disables actions when the plugin payload is missing", () => {
  const state = pluginManagerDisplayState({});

  assert.equal(state.installed, false);
  assert.equal(state.actions.install.available, false);
  assert.equal(state.actions.operation.available, false);
  assert.equal(state.actions.operation.disabledReason, "not_installed");
  assert.equal(state.nextStatus, undefined);
});
