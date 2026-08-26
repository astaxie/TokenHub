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
