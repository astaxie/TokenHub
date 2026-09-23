import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { applyIdentityProviderTemplate, identityProviderEndpointDefaults, identityProviderTemplateOptionsFromData, identityProviderTemplatesFromData, LoginView } from "./auth";

const baseProps = {
  loading: false,
  error: "",
  baseURL: "http://localhost:8080",
  identityProviders: [],
  oauthReturnURL: "http://localhost:3000/overview",
  language: "zh-CN" as const,
  theme: "light" as const,
  onLanguageChange: vi.fn(),
  onThemeToggle: vi.fn(),
};

describe("LoginView", () => {
  it("submits the entered identity and password", async () => {
    const user = userEvent.setup();
    const onLogin = vi.fn();
    render(<LoginView {...baseProps} onLogin={onLogin} />);

    await user.type(screen.getByLabelText("账号 / 邮箱"), "admin@example.com");
    await user.type(screen.getByLabelText("密码"), "correct-horse");
    await user.click(screen.getByRole("button", { name: "登录控制台" }));

    expect(onLogin).toHaveBeenCalledWith("admin@example.com", "correct-horse");
    expect(screen.queryByText("保持登录状态")).not.toBeInTheDocument();
  });

  it("hides SSO actions when no identity providers are configured", () => {
    render(<LoginView {...baseProps} onLogin={vi.fn()} />);

    expect(screen.queryByText("或")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "SSO 企业单点登录" })).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: /登录/ })).not.toBeInTheDocument();
  });

  it("shows server errors and configured SSO entry points", () => {
    render(
      <LoginView
        {...baseProps}
        error="login 401"
        identityProviders={[{
          id: "idp_google",
          name: "company-google",
          display_name: "Company Google",
          provider_type: "oidc",
          icon_key: "google",
        }]}
        onLogin={vi.fn()}
      />,
    );

    expect(screen.getByText("login 401")).toHaveAttribute("class", "login-error");
    const sso = screen.getByRole("link", { name: "使用 Company Google 登录" });
    expect(sso).toHaveTextContent("Company Google");
    expect(sso).toHaveAttribute("href", expect.stringContaining("/api/admin/auth/oauth/start"));
    expect(sso).toHaveAttribute("href", expect.stringContaining("id=idp_google"));
  });
});

describe("identity provider template plugins", () => {
  it("adds identity provider templates from plugin capability metadata", () => {
    const data = {
      plugins: [{
        id: "tokenhub.identity.acme",
        name: "Acme Identity",
        version: "1.0.0",
        source: "marketplace",
        kinds: ["admin_ui"],
        placements: ["presentation"],
        capabilities: [{
          kind: "identity_provider_template",
          name: "entry",
          subject: "acme_sso",
          value: JSON.stringify({
            label: "Acme SSO",
            provider_type: "oidc",
            icon_key: "sso",
            login_label: "Acme",
            issuer_placeholder: "https://id.acme.example",
            scopes: "openid profile email",
            username_claim: "preferred_username",
            endpoints: {
              authorize_url: "{issuer}/authorize",
              token_url: "{issuer}/token",
              userinfo_url: "{issuer}/userinfo",
            },
          }),
        }],
      }],
    };

    const templates = identityProviderTemplatesFromData(data);
    const acme = templates.find((template) => template.key === "acme_sso");

    expect(identityProviderTemplateOptionsFromData(data)).toContainEqual({ value: "acme_sso", label: "Acme SSO" });
    expect(acme?.loginLabel).toBe("Acme");
    expect(identityProviderEndpointDefaults(acme!, "https://id.acme.example/tenant/")).toEqual({
      authorize_url: "https://id.acme.example/tenant/authorize",
      token_url: "https://id.acme.example/tenant/token",
      userinfo_url: "https://id.acme.example/tenant/userinfo",
    });
    expect(applyIdentityProviderTemplate({}, "acme_sso", true, templates)).toEqual(expect.objectContaining({
      provider_template: "acme_sso",
      provider_type: "oidc",
      login_label: "Acme",
      scopes: "openid profile email",
    }));
  });
});
