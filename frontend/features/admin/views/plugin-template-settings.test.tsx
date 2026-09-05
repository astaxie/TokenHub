import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { simRegistryFromPlugins } from "../domain/sim-registry";
import { PluginTemplateSettings } from "./plugin-template-settings";

function registry() {
  return simRegistryFromPlugins([{
    id: "example.sim",
    name: "Example Template",
    version: "1.0.0",
    capabilities: [
      { kind: "sim", name: "theme_tokens", value: JSON.stringify({ id: "light", title: "Example Light", mode: "light", tokens: { accent: "#2563eb", shadow: "0 1px 2px #00000022" } }) },
      { kind: "sim", name: "theme_tokens", value: JSON.stringify({ id: "dark", title: "Example Dark", mode: "dark", tokens: { accent: "#60a5fa" } }) },
      { kind: "sim", name: "shell_layout", value: JSON.stringify({ id: "shell", navigation: "sidebar", density: "compact" }) },
      { kind: "sim", name: "page_template", value: JSON.stringify({ id: "detail", target: "provider.detail", layout: "two_column", regions: ["main", "side"] }) },
      { kind: "sim", name: "dashboard_composition", value: JSON.stringify({ id: "dashboard", cards: [{ contribution_id: "cost", region: "main", size: "wide" }] }) },
    ],
  }]);
}

describe("PluginTemplateSettings", () => {
  it("shows only editable settings with human-readable labels", () => {
    render(<PluginTemplateSettings overrides={{}} pluginID="example.sim" registry={registry()} themeMode="light" />);

    expect(screen.getByRole("tab", { name: "Example Light" })).toHaveAttribute("aria-selected", "true");
    expect(screen.getByText("品牌与强调")).toBeInTheDocument();
    expect(screen.getByText("主题色")).toBeInTheDocument();
    expect(screen.getByText("用于按钮、选中项和重点信息。")).toBeInTheDocument();
    expect(screen.queryByText("导航栏")).not.toBeInTheDocument();
    expect(screen.queryByText(/contribution_id/)).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: "Example Dark" }));
    expect(screen.getByRole("textbox", { name: "主题色 当前值" })).toHaveValue("#60a5fa");
  });

  it("applies safe declared token overrides and restores defaults", () => {
    const onChange = vi.fn();
    const themeKey = "example.sim:theme_tokens:light";
    render(
      <PluginTemplateSettings
        activeThemeKey={themeKey}
        overrides={{}}
        pluginID="example.sim"
        registry={registry()}
        onThemeTokenOverridesChange={onChange}
      />,
    );

    fireEvent.change(screen.getByRole("textbox", { name: "主题色 当前值" }), { target: { value: "#16a34a" } });
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    expect(onChange).toHaveBeenCalledWith(themeKey, { accent: "#16a34a" });

    fireEvent.change(screen.getByRole("textbox", { name: "面板阴影 当前值" }), { target: { value: "url(https://example.test/a.css)" } });
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    expect(screen.getByRole("alert")).toHaveTextContent("样式值不安全");
    expect(onChange).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "恢复默认" }));
    expect(onChange).toHaveBeenLastCalledWith(themeKey, {});
  });
});

describe("PluginTemplateSettings theme token allowlist", () => {
  function registryWithTokens(tokens: Record<string, string>) {
    return simRegistryFromPlugins([{
      id: "example.sim",
      name: "Example Template",
      version: "1.0.0",
      capabilities: [
        { kind: "sim", name: "theme_tokens", value: JSON.stringify({ id: "light", title: "Example Light", mode: "light", tokens }) },
      ],
    }]);
  }

  function renderTokens(tokens: Record<string, string>) {
    return render(
      <PluginTemplateSettings
        overrides={{}}
        pluginID="example.sim"
        registry={registryWithTokens(tokens)}
      />,
    );
  }

  it("hides tokens the override normalizer would discard", () => {
    renderTokens({ "--accent": "#2563eb", "--not-a-real-token": "#000000" });

    expect(screen.getByRole("textbox", { name: "主题色 当前值" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "not-a-real-token 当前值" })).not.toBeInTheDocument();
    expect(screen.getByText("只能调整模板已经声明的安全主题 Token。")).toBeInTheDocument();
  });

  it("renders a duplicated token once, showing the default the shell applies", () => {
    renderTokens({ accent: "#2563eb", "--accent": "#16a34a" });

    expect(screen.getAllByRole("textbox", { name: "主题色 当前值" })).toHaveLength(1);
    expect(screen.getByRole("textbox", { name: "主题色 当前值" })).toHaveValue("#16a34a");
  });

  it("offers no apply action when the template declares no adjustable token", () => {
    renderTokens({ "--not-a-real-token": "#000000" });

    expect(screen.getByText("只能调整模板已经声明的安全主题 Token。")).toBeInTheDocument();
    expect(screen.getByText("调整保存在当前浏览器。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "保存设置" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "恢复默认" })).not.toBeInTheDocument();
  });
});
