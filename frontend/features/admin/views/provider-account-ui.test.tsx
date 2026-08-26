import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderOAuthCallbackModal, ProviderOAuthNoticeModal } from "./provider-account-ui";

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
});
