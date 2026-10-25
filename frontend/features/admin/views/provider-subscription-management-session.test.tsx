import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearSavedSession, readSavedSession, saveSession } from "../core/session";
import { authExpiredEventName, type AdminUIContribution, type PluginActionDescriptor, type PluginDescriptor, type Provider, type ProviderCatalogEntry, type ProviderResource } from "../core/types";
import { ProviderUpsertModal } from "./provider-editor";
import { ProviderPluginPanels } from "./provider-plugin-panels";

const api = { baseURL: "http://localhost:8080", adminToken: "synthetic-admin" };
const provider: Provider = { id: "provider", name: "Synthetic subscription", type: "qoder_subscription", status: "active", healthy: true, priority: 1 };
const resource: ProviderResource = { id: "account", provider_id: provider.id, name: "Synthetic account", resource_type: "qoder_account", status: "active", healthy: true, priority: 1, weight: 100 };
const plugin: PluginDescriptor = { id: "test.subscription", name: provider.name, version: "1", source: "built_in", kinds: ["provider"], placements: [], capabilities: [{ kind: "provider_resource_type", name: resource.resource_type, subject: provider.type }] };
const catalog: ProviderCatalogEntry = { id: "qoder-subscription", name: provider.name, display_name: provider.name, type: provider.type, source: "fixture", models_count: 0, models: [] };
const message = "Subscription account requires reauthorization";

function setup(capability: "quota.read" | "checkin.run") {
  const session = { baseURL: api.baseURL, token: api.adminToken, expiresAt: new Date(Date.now() + 60_000).toISOString(), user: { id: "admin", username: "synthetic", name: "Synthetic administrator", email: "admin@example.test", role: "admin", status: "active" } };
  saveSession(session);
  const expired = vi.fn();
  window.addEventListener(authExpiredEventName, expired);
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input));
    if (url.pathname.includes("provider-catalog")) return new Response(JSON.stringify({ data: catalog }));
    if (url.pathname.endsWith(capability)) return new Response(JSON.stringify({ error: { code: "provider_resource_reauthorization_required", message } }), { status: 409 });
    throw new Error(`Undeclared request: ${url}`);
  }));
  const actions: PluginActionDescriptor[] = [{ plugin_id: plugin.id, action_id: capability, capability, subject: provider.type, kind: capability === "quota.read" ? "read" : "mutate", metadata: { provider_resource_type: resource.resource_type } }];
  const contributions: AdminUIContribution[] = [{ plugin_id: plugin.id, id: capability, slot: "provider.resource.panel", title: "Synthetic account action", provider_types: [provider.type], resource_types: [resource.resource_type], action: capability }];
  return { actions, contributions, assertSessionRetained() {
    expect(readSavedSession()).toEqual(session);
    expect(expired).not.toHaveBeenCalled();
  }, dispose() { window.removeEventListener(authExpiredEventName, expired); } };
}

afterEach(() => { clearSavedSession(); });

describe("Subscription management authentication boundaries", () => {
  it("shows an expired quota account without signing the administrator out", async () => {
    const test = setup("quota.read");
    try {
      render(<ProviderUpsertModal api={api} catalog={[catalog]} provider={provider} plugins={[plugin]} pluginActions={test.actions} pluginUI={test.contributions} resources={[resource]} loading={false} mode="edit" onClose={vi.fn()} onSaved={vi.fn()} setError={vi.fn()} setLoading={vi.fn()} setNotice={vi.fn()} />);
      await userEvent.click(screen.getByRole("tab", { name: "高级" }));
      expect(await screen.findByText("账号会话已失效，请重新进行账号授权。")).toBeVisible();
      test.assertSessionRetained();
    } finally { test.dispose(); }
  });

  it("shows an expired check-in account without signing the administrator out", async () => {
    const test = setup("checkin.run");
    try {
      render(<ProviderPluginPanels api={api} provider={provider} resources={[resource]} contributions={test.contributions} actions={test.actions} />);
      await userEvent.click(screen.getByRole("button", { name: "签到" }));
      expect(await screen.findByRole("alert")).toHaveTextContent("账号会话已失效，请重新进行账号授权。");
      expect(screen.getByRole("button", { name: "签到" })).toBeEnabled();
      test.assertSessionRetained();
    } finally { test.dispose(); }
  });
});
