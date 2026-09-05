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
      { kind: "sim", name: "shell_layout", value: JSON.stringify({ id: "shell", navigation: "sidebar", density: "compact" }) },
      { kind: "sim", name: "page_template", value: JSON.stringify({ id: "detail", target: "provider.detail", layout: "two_column", regions: ["main", "side"] }) },
      { kind: "sim", name: "dashboard_composition", value: JSON.stringify({ id: "dashboard", cards: [{ contribution_id: "cost", region: "main", size: "wide" }] }) },
    ],
  }]);
}

describe("PluginTemplateSettings", () => {
  it("shows inspectable template blocks and updates the selected declaration", () => {
    render(<PluginTemplateSettings contributions={[]} overrides={{}} pluginID="example.sim" registry={registry()} />);

    expect(screen.getByRole("button", { name: /导航栏/ })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /全局搜索/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /cost/ }));
    expect(screen.getByText("仪表盘卡片")).toBeInTheDocument();
    expect(screen.getByText(/contribution_id/)).toBeInTheDocument();
  });

  it("applies safe declared token overrides and restores defaults", () => {
    const onChange = vi.fn();
    const themeKey = "example.sim:theme_tokens:light";
    render(
      <PluginTemplateSettings
        contributions={[]}
        overrides={{}}
        pluginID="example.sim"
        registry={registry()}
        onThemeTokenOverridesChange={onChange}
      />,
    );

    fireEvent.change(screen.getByRole("textbox", { name: "accent 当前值" }), { target: { value: "#16a34a" } });
    fireEvent.click(screen.getByRole("button", { name: "应用调整" }));
    expect(onChange).toHaveBeenCalledWith(themeKey, { accent: "#16a34a" });

    fireEvent.change(screen.getByRole("textbox", { name: "shadow 当前值" }), { target: { value: "url(https://example.test/a.css)" } });
    fireEvent.click(screen.getByRole("button", { name: "应用调整" }));
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
        contributions={[]}
        overrides={{}}
        pluginID="example.sim"
        registry={registryWithTokens(tokens)}
      />,
    );
  }

  it("hides tokens the override normalizer would discard", () => {
    renderTokens({ "--accent": "#2563eb", "--not-a-real-token": "#000000" });

    expect(screen.getByRole("textbox", { name: "accent 当前值" })).toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "not-a-real-token 当前值" })).not.toBeInTheDocument();
    expect(screen.getByText("只能调整模板已经声明的安全主题 Token。")).toBeInTheDocument();
  });

  it("renders a duplicated token once, showing the default the shell applies", () => {
    renderTokens({ accent: "#2563eb", "--accent": "#16a34a" });

    expect(screen.getAllByRole("textbox", { name: "accent 当前值" })).toHaveLength(1);
    expect(screen.getByRole("textbox", { name: "accent 当前值" })).toHaveValue("#16a34a");
  });

  it("offers no apply action when the template declares no adjustable token", () => {
    renderTokens({ "--not-a-real-token": "#000000" });

    expect(screen.getByText("只能调整模板已经声明的安全主题 Token。")).toBeInTheDocument();
    expect(screen.getByText("调整保存在当前浏览器。")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "应用调整" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "恢复默认" })).not.toBeInTheDocument();
  });
});
