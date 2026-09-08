import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderResourceProbePanel } from "./provider-resource-probe-panel";

describe("ProviderResourceProbePanel", () => {
  afterEach(() => {
    setActiveLanguage("en");
    vi.unstubAllGlobals();
  });

  it("runs a non-Codex provider resource probe from plugin metadata", async () => {
    const user = userEvent.setup();
    const fetchMock = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => new Response(JSON.stringify({
      data: {
        model: "kimi-fast",
        output_text: "pong",
        latency_ms: 25,
        usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
      },
    }), { status: 200, headers: { "content-type": "application/json" } }));
    vi.stubGlobal("fetch", fetchMock);
    setActiveLanguage("zh-CN");

    render(
      <ProviderResourceProbePanel
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        accountCatalogErrors={{}}
        accountCatalogLoading={false}
        accountResources={[resource()]}
        pluginActions={[
          {
            plugin_id: "tokenhub.provider.kimi",
            action_id: "kimi.wrong.probe.run",
            kind: "test",
            capability: "probe.run",
            subject: "kimi_subscription",
            metadata: {
              default_payload_json: `{"model":"wrong-model"}`,
              probe_fields: "model,prompt",
              provider_resource_type: "kimi_other_account",
            },
          },
          {
            plugin_id: "tokenhub.provider.kimi",
            action_id: "kimi.probe.run",
            kind: "test",
            capability: "probe.run",
            subject: "kimi_subscription",
            metadata: {
              default_payload_json: `{"model":"kimi-fast"}`,
              probe_fields: "model,prompt",
              provider_resource_type: "kimi_subscription_account",
            },
          },
        ]}
        providerType="kimi_subscription"
        selectedAccountCatalog={{
          id: "kimi-subscription",
          name: "Kimi Subscription",
          display_name: "Kimi Subscription",
          type: "kimi_subscription",
          models_count: 1,
          source: "test",
          models: [{ id: "kimi-fast", name: "kimi-fast" }],
        }}
        selectedAccountID="rsrc_kimi"
        selectedAccountResources={[resource()]}
      />,
    );

    await user.type(screen.getByLabelText("真实提示词"), "ping");
    await user.click(screen.getByRole("button", { name: "发送真实测试" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [input, init] = fetchMock.mock.calls[0] as [RequestInfo | URL, RequestInit | undefined];
    expect(String(input)).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.kimi/actions/kimi.probe.run");
    expect(JSON.parse(String(init?.body))).toMatchObject({
      provider_id: "prv_kimi",
      resource_id: "rsrc_kimi",
      model: "kimi-fast",
      prompt: "ping",
    });
    expect(await screen.findByText("pong")).toBeInTheDocument();
  });

  it("localizes probe result labels and formats metrics for the selected language", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({
      data: {
        model: "kimi-fast",
        speed: "fast",
        upstream_service_tier: "priority",
        latency_ms: 1234,
        usage: { prompt_tokens: 1234, completion_tokens: 56, total_tokens: 1290 },
      },
    }), { status: 200, headers: { "content-type": "application/json" } })));
    setActiveLanguage("en");

    render(
      <ProviderResourceProbePanel
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        accountCatalogErrors={{}}
        accountCatalogLoading={false}
        accountResources={[resource()]}
        pluginActions={[{
          plugin_id: "tokenhub.provider.kimi",
          action_id: "kimi.probe.run",
          kind: "test",
          capability: "probe.run",
          subject: "kimi_subscription",
          metadata: {
            default_payload_json: `{"model":"kimi-fast"}`,
            probe_fields: "model,prompt",
            provider_resource_type: "kimi_subscription_account",
          },
        }]}
        providerType="kimi_subscription"
        selectedAccountCatalog={{
          id: "kimi-subscription",
          name: "Kimi Subscription",
          display_name: "Kimi Subscription",
          type: "kimi_subscription",
          models_count: 1,
          source: "test",
          models: [{ id: "kimi-fast", name: "kimi-fast" }],
        }}
        selectedAccountID="rsrc_kimi"
        selectedAccountResources={[resource()]}
      />,
    );

    await user.type(screen.getByLabelText("Live prompt"), "ping");
    await user.click(screen.getByRole("button", { name: "Send Live Test" }));

    expect(await screen.findByText("Request Speed")).toBeInTheDocument();
    expect(screen.getByText("Upstream Service Tier")).toBeInTheDocument();
    expect(screen.getByText("Duration")).toBeInTheDocument();
    expect(screen.getByText("1,234 ms")).toBeInTheDocument();
    expect(screen.getByText("1,234")).toBeInTheDocument();
    expect(screen.getByText("1,290")).toBeInTheDocument();
  });
});

function resource() {
  return {
    id: "rsrc_kimi",
    provider_id: "prv_kimi",
    name: "Kimi Account",
    resource_type: "kimi_subscription_account",
    status: "active",
    healthy: true,
    priority: 1,
    weight: 100,
  };
}
