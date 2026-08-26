import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ProviderResourceReasoningSettings } from "./provider-resource-reasoning-settings";

describe("ProviderResourceReasoningSettings", () => {
  it("shows plugin account resources without relying on OpenAI subscription literals", () => {
    render(
      <ProviderResourceReasoningSettings
        api={{ baseURL: "http://localhost:8080", adminToken: "admin-token" }}
        provider={{ id: "prv_kimi", name: "Kimi", type: "kimi_subscription", status: "active", healthy: true, priority: 1 }}
        providerType="kimi_subscription"
        resources={[
          { id: "rsrc_kimi", provider_id: "prv_kimi", name: "Kimi Account", resource_type: "kimi_subscription_account", status: "active", healthy: true, priority: 1, weight: 100 },
          { id: "rsrc_key", provider_id: "prv_kimi", name: "Kimi API Key", resource_type: "api_key", status: "active", healthy: true, priority: 2, weight: 100 },
          { id: "rsrc_codex", provider_id: "prv_kimi", name: "Codex Account", resource_type: "openai_subscription", status: "active", healthy: true, priority: 3, weight: 100 },
          { id: "rsrc_other", provider_id: "prv_other", name: "Other Account", resource_type: "kimi_subscription_account", status: "active", healthy: true, priority: 4, weight: 100 },
        ]}
        onSaved={() => {}}
      />,
    );

    expect(screen.getByText("Kimi Account")).toBeInTheDocument();
    expect(screen.getByText("Kimi API Key")).toBeInTheDocument();
    expect(screen.queryByText("Codex Account")).not.toBeInTheDocument();
    expect(screen.queryByText("Other Account")).not.toBeInTheDocument();
  });
});
