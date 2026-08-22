import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { LoginView } from "./auth";

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
    expect(sso).toHaveAttribute("href", expect.stringContaining("/api/admin/auth/oauth/start"));
    expect(sso).toHaveAttribute("href", expect.stringContaining("id=idp_google"));
  });
});
