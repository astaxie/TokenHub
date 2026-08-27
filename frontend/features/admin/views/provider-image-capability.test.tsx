import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProviderImageCapability, unwrapProviderImageCapabilityResult } from "./provider-image-capability";

const provider = { id: "prv_codex", name: "Codex", type: "openai_codex", status: "active", healthy: true, priority: 1 };
const resource = {
  id: "rsrc_codex",
  provider_id: "prv_codex",
  name: "Codex Account",
  resource_type: "openai_subscription",
  status: "active",
  healthy: true,
  priority: 1,
  weight: 100,
};
const action = {
  plugin_id: "tokenhub.provider.openai-codex",
  action_id: "openai_codex.image_capability.configure",
  kind: "mutate",
  capability: "image.capability.configure",
  subject: "openai_codex",
  metadata: {
    display_name: "Codex Subscription ImageGen",
    public_model: "codex-gpt-image-2",
    upstream_model: "gpt-image-2",
  },
};

describe("ProviderImageCapability", () => {
  it("configures image capability through the plugin action endpoint", async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn().mockResolvedValue(undefined);
    const setNotice = vi.fn();
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { enabled: true, tested: true, capability: "supported", resource_id: "rsrc_codex" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderImageCapability
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[action]}
        provider={provider}
        routes={[]}
        resources={[resource]}
        selectedAccountID="rsrc_codex"
        onChanged={onChanged}
        setNotice={setNotice}
      />,
    );

    expect(screen.getByText("Codex Subscription ImageGen")).toBeInTheDocument();
    expect(screen.getByText("codex-gpt-image-2 ← gpt-image-2")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "开始测试并启用" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.image_capability.configure");
    expect(JSON.parse(String(init.body))).toEqual({
      provider_id: "prv_codex",
      resource_id: "rsrc_codex",
      enabled: true,
    });
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(setNotice).toHaveBeenCalledWith("订阅生图测试通过并已启用。");
  });

  it("stays hidden when the plugin action is not registered", () => {
    render(
      <ProviderImageCapability
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[]}
        provider={provider}
        routes={[]}
        resources={[resource]}
        selectedAccountID="rsrc_codex"
        onChanged={vi.fn()}
        setNotice={vi.fn()}
      />,
    );

    expect(screen.queryByText("Codex 订阅生图")).not.toBeInTheDocument();
  });

  it("uses plugin metadata to select non-Codex account resources", async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn().mockResolvedValue(undefined);
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      data: { enabled: true, tested: true, capability: "supported", resource_id: "rsrc_kimi" },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderImageCapability
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[
          {
            ...action,
            plugin_id: "tokenhub.provider.kimi",
            action_id: "kimi.wrong_image_capability.configure",
            subject: "kimi_subscription",
            metadata: {
              display_name: "Wrong Subscription ImageGen",
              provider_resource_type: "kimi_other_account",
              public_model: "wrong-image",
              upstream_model: "wrong-upstream-image",
            },
          },
          {
            ...action,
            plugin_id: "tokenhub.provider.kimi",
            action_id: "kimi.image_capability.configure",
            subject: "kimi_subscription",
            metadata: {
              display_name: "Kimi Subscription ImageGen",
              provider_resource_type: "kimi_subscription_account",
              public_model: "kimi-image",
              upstream_model: "moonshot-image",
            },
          },
        ]}
        provider={{ ...provider, id: "prv_kimi", name: "Kimi", type: "kimi_subscription" }}
        routes={[]}
        resources={[
          { ...resource, id: "rsrc_codex", provider_id: "prv_kimi", resource_type: "openai_subscription" },
          { ...resource, id: "rsrc_kimi", provider_id: "prv_kimi", name: "Kimi Account", resource_type: "kimi_subscription_account" },
        ]}
        selectedAccountID="all"
        onChanged={onChanged}
        setNotice={vi.fn()}
      />,
    );

    expect(screen.getByText("Kimi Subscription ImageGen")).toBeInTheDocument();
    expect(screen.getByText("kimi-image ← moonshot-image")).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "开始测试并启用" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(JSON.parse(String((fetchMock.mock.calls[0] as [string, RequestInit])[1].body)).resource_id).toBe("rsrc_kimi");
  });

  it("maps image capability action errors from plugin metadata", async () => {
    const user = userEvent.setup();
    const onChanged = vi.fn().mockResolvedValue(undefined);
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "kimi_image_forbidden", message: "raw provider message" },
    }), {
      status: 403,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderImageCapability
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[{
          ...action,
          plugin_id: "tokenhub.provider.kimi",
          action_id: "kimi.image_capability.configure",
          subject: "kimi_subscription",
          metadata: {
            display_name: "Kimi Subscription ImageGen",
            provider_resource_type: "kimi_subscription_account",
            public_model: "kimi-image",
            upstream_model: "moonshot-image",
            "error_message.kimi_image_forbidden": "Kimi account cannot create images. Choose another account and retry.",
          },
        }]}
        provider={{ ...provider, id: "prv_kimi", name: "Kimi", type: "kimi_subscription" }}
        routes={[]}
        resources={[{
          ...resource,
          id: "rsrc_kimi",
          provider_id: "prv_kimi",
          name: "Kimi Account",
          resource_type: "kimi_subscription_account",
        }]}
        selectedAccountID="rsrc_kimi"
        onChanged={onChanged}
        setNotice={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("checkbox"));
    await user.click(screen.getByRole("button", { name: "开始测试并启用" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Kimi account cannot create images. Choose another account and retry.");
    expect(onChanged).toHaveBeenCalledTimes(1);
  });

  it("unwraps plugin action result envelopes", () => {
    expect(unwrapProviderImageCapabilityResult({
      data: { enabled: true, tested: true, resource_id: "rsrc_codex" },
    })).toEqual({ enabled: true, tested: true, resource_id: "rsrc_codex" });
  });
});
