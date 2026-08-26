import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderAccountDetails, ProviderOAuthCallbackModal, ProviderOAuthNoticeModal } from "./provider-account-ui";

describe("Provider account OAuth UI", () => {
  afterEach(() => {
    setActiveLanguage("en");
  });

  it("renders OAuth modal copy from plugin metadata", () => {
    render(
      <ProviderOAuthNoticeModal
        busy={false}
        error=""
        oauthMetadata={{ notice_login_step: "Sign in to Kimi and approve TokenHub." }}
        onClose={vi.fn()}
        onConfirm={vi.fn()}
        onCopy={vi.fn()}
        open={true}
      />,
    );

    expect(screen.getByText("Sign in to Kimi and approve TokenHub.")).toBeInTheDocument();
    expect(screen.queryByText(/OpenAI\/Codex/)).not.toBeInTheDocument();
  });

  it("uses generic OAuth fallback copy without provider metadata", () => {
    setActiveLanguage("zh-CN");

    render(
      <ProviderOAuthCallbackModal
        busy={false}
        error=""
        onClose={vi.fn()}
        onConfirm={vi.fn()}
        onValueChange={vi.fn()}
        open={true}
        value=""
      />,
    );

    expect(screen.getByText("账号 OAuth 授权")).toBeInTheDocument();
    expect(screen.queryByText(/OpenAI\/Codex/)).not.toBeInTheDocument();
  });

  it("reads image capability details from the plugin profile option keys", () => {
    setActiveLanguage("zh-CN");

    render(
      <ProviderAccountDetails
        imageCapabilityProfile={{
          displayName: "Kimi ImageGen",
          publicModel: "kimi-image",
          upstreamModel: "moonshot-image",
          capabilityOption: "kimi_image_capability",
          capabilityCheckedAtOption: "kimi_image_capability_checked_at",
          capabilitySupportedValue: "available",
          capabilityUnsupportedValue: "blocked",
        }}
        resource={{
          id: "rsrc_kimi",
          provider_id: "prv_kimi",
          name: "Kimi Account",
          resource_type: "kimi_subscription_account",
          status: "active",
          healthy: true,
          priority: 1,
          weight: 100,
          options: {
            image_generation_capability: "blocked",
            kimi_image_capability: "available",
            kimi_image_capability_checked_at: "2099-01-01T00:00:00Z",
          },
        }}
      />,
    );

    expect(screen.getByText("生图能力")).toBeInTheDocument();
    expect(screen.getByText("支持")).toBeInTheDocument();
    expect(screen.queryByText("不支持")).not.toBeInTheDocument();
  });
});
