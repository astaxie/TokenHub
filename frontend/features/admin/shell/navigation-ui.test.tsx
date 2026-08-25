import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { Sidebar } from "./navigation-ui";

describe("Sidebar", () => {
  it("renders plugin nav section contributions for admins", async () => {
    const user = userEvent.setup();
    const onSelectPluginPage = vi.fn();
    const data = emptyData();
    data.pluginUI = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      id: "ecosystem-page",
      slot: "nav.section",
      title: "Plugin Ecosystem",
      schema: { description: "Inspect plugins." },
    }];

    render(
      <Sidebar
        activePluginPageKey=""
        activeView="overview"
        collapsed={false}
        data={data}
        onLogout={vi.fn()}
        onSelect={vi.fn()}
        onSelectPluginPage={onSelectPluginPage}
        onToggleCollapse={vi.fn()}
        onToggleGroup={vi.fn()}
        openGroups={{}}
        user={{ id: "usr_admin", username: "admin", name: "Admin", email: "admin@example.test", role: "admin", status: "active" }}
      />,
    );

    expect(screen.getByText("插件扩展")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Plugin Ecosystem" }));
    expect(onSelectPluginPage).toHaveBeenCalledWith("tokenhub.admin.plugin-ecosystem:ecosystem-page");
  });

  it("hides plugin nav section contributions from non-admin users", () => {
    const data = emptyData();
    data.pluginUI = [{
      plugin_id: "tokenhub.admin.plugin-ecosystem",
      id: "ecosystem-page",
      slot: "nav.section",
      title: "Plugin Ecosystem",
    }];

    render(
      <Sidebar
        activePluginPageKey=""
        activeView="overview"
        collapsed={false}
        data={data}
        onLogout={vi.fn()}
        onSelect={vi.fn()}
        onSelectPluginPage={vi.fn()}
        onToggleCollapse={vi.fn()}
        onToggleGroup={vi.fn()}
        openGroups={{}}
        user={{ id: "usr_user", username: "user", name: "User", email: "user@example.test", role: "user", status: "active" }}
      />,
    );

    expect(screen.queryByText("插件扩展")).not.toBeInTheDocument();
    expect(screen.queryByText("Plugin Ecosystem")).not.toBeInTheDocument();
  });
});
