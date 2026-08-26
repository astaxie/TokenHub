import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ProviderAccountTokenRenewal } from "./provider-account-token-renewal";

const resource = {
  id: "rsrc_codex",
  provider_id: "prv_codex",
  name: "Codex Account",
  resource_type: "openai_subscription",
  status: "active",
  healthy: true,
  priority: 1,
  weight: 100,
  credential_summary: { has_refresh_token: "true" },
};

describe("ProviderAccountTokenRenewal", () => {
  it("runs credential refresh through the plugin action endpoint", async () => {
    const user = userEvent.setup();
    const onRenewed = vi.fn().mockResolvedValue(undefined);
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { credential_summary: {} } }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }));
    vi.stubGlobal("fetch", fetchMock);

    render(
      <ProviderAccountTokenRenewal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[{
          plugin_id: "tokenhub.provider.openai-codex",
          action_id: "openai_codex.credentials.refresh",
          kind: "mutate",
          capability: "credentials.refresh",
          subject: "openai_codex",
        }]}
        providerType="openai_codex"
        resource={resource}
        onRenewed={onRenewed}
      />,
    );

    await user.click(screen.getByRole("button", { name: "续租 Token" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("http://localhost:8080/api/admin/plugins/tokenhub.provider.openai-codex/actions/openai_codex.credentials.refresh");
    expect(JSON.parse(String(init.body))).toEqual({
      provider_id: "prv_codex",
      resource_id: "rsrc_codex",
      force: true,
    });
    await waitFor(() => expect(onRenewed).toHaveBeenCalledTimes(1));
  });

  it("stays hidden when the matching plugin action is not registered", () => {
    render(
      <ProviderAccountTokenRenewal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[]}
        providerType="openai_codex"
        resource={resource}
        onRenewed={vi.fn()}
      />,
    );

    expect(screen.queryByRole("button", { name: "续租 Token" })).not.toBeInTheDocument();
  });

  it("shows a provider-neutral reauthorization notice", () => {
    render(
      <ProviderAccountTokenRenewal
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        pluginActions={[{
          plugin_id: "tokenhub.provider.kimi",
          action_id: "kimi.credentials.refresh",
          kind: "mutate",
          capability: "credentials.refresh",
          subject: "kimi_subscription",
        }]}
        providerType="kimi_subscription"
        resource={{ ...resource, credential_summary: { has_refresh_token: "true", oauth_reauthorization_required: "true" } }}
        onRenewed={vi.fn()}
      />,
    );

    expect(screen.getByText("账号会话已失效，请重新进行账号授权。")).toBeInTheDocument();
    expect(screen.queryByText(/OpenAI\/Codex/)).not.toBeInTheDocument();
  });
});
