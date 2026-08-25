import { describe, expect, it } from "vitest";
import { providerResourcePayload } from "./payloads";

describe("providerResourcePayload Super Grok subscription", () => {
  it("sends OAuth credentials for xai_subscription resources", () => {
    const payload = providerResourcePayload({
      provider_id: "prv_xai_grok",
      name: "Super Grok Account",
      resource_type: "xai_subscription",
      auth_type: "oauth",
      access_token: "xai-access-token",
      refresh_token: "xai-refresh-token",
      account_email: "owner@example.com",
      account_id: "xai-sub-1",
      base_url: "https://cli-chat-proxy.grok.com/v1",
      status: "active",
      healthy: "true",
    }) as Record<string, unknown>;

    expect(payload.resource_type).toBe("xai_subscription");
    expect(payload.api_key).toBe("xai-access-token");
    expect(payload.credentials).toEqual(expect.objectContaining({
      auth_type: "oauth",
      access_token: "xai-access-token",
      refresh_token: "xai-refresh-token",
      email: "owner@example.com",
      account_id: "xai-sub-1",
    }));
    expect((payload.options as Record<string, string>).credential_source).toBe("xai_subscription");
  });
});
