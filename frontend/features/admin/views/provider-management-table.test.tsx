import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Provider, ProviderMonitoringSnapshot, ProviderQuotaSummary } from "../core/types";
import { emptyData } from "../domain/catalog";
import { providerConfig } from "../resources/provider-model-config";
import { ProviderAccountQuota, ProviderChannelTable } from "./crud-projects";

const provider: Provider = { id: "prv_test", name: "Example Provider", type: "openai_compatible", base_url: "https://provider.example/v1", status: "active", healthy: true, priority: 1 };

function setup(snapshot?: ProviderMonitoringSnapshot, readOnly = false, configuredProvider = provider) {
  const data = emptyData();
  data.providers = [configuredProvider];
  data.providerModels = [{ id: "upstream_1", provider_id: provider.id, upstream_model: "example-chat", status: "active" }];
  if (snapshot) data.providerMonitoring = [snapshot];
  const config = providerConfig();
  if (readOnly) { config.update = undefined; config.remove = undefined; config.actions = []; }
  const onEdit = vi.fn();
  const onDelete = vi.fn();
  const table = (currentProvider: Provider) => <ProviderChannelTable config={config} currentUser={null} data={{ ...data, providers: [currentProvider] }} loading={false} providers={[currentProvider]} summaryProviders={[currentProvider]} query="Example" onAction={vi.fn()} onEdit={onEdit} onDelete={onDelete} />;
  const view = render(table(configuredProvider));
  return { onEdit, onDelete, rerenderProvider: (nextProvider: Provider) => view.rerender(table(nextProvider)) };
}

describe("Provider management view", () => {
  it("shows awaiting observation when the background quota snapshot is not ready", () => {
    const quota: ProviderQuotaSummary = {
      supported: true,
      limit_reached: false,
      successful_accounts: 0,
      failed_accounts: 1,
      accounts: [{ resource_id: "rsrc_pending", resource_name: "Pending account", error_code: "quota_not_cached" }],
    };
    render(<ProviderAccountQuota quota={quota} refreshing={{}} resources={[]} onRefresh={vi.fn()} />);
    expect(screen.getByText("待观测")).toBeVisible();
    expect(screen.queryByText("查询失败")).not.toBeInTheDocument();
  });

  it("shows inventory counts separately from health and does not claim unobserved health", () => {
    setup();
    const row = screen.getByRole("row", { name: /Example Provider/ });
    expect(within(row).getByText("1 个模型")).toBeVisible();
    expect(within(row).getByText("待观测")).toBeVisible();
    expect(within(row).getByText("启用", { exact: true })).toBeVisible();
    expect(screen.queryByRole("columnheader", { name: "真实监控 · L3" })).not.toBeInTheDocument();
    expect(screen.queryByRole("columnheader", { name: "账号配额" })).not.toBeInTheDocument();
  });

  it("uses brand icons and recovers from an image failure when the provider brand changes", () => {
    const { rerenderProvider } = setup();
    const icon = () => within(screen.getByRole("row", { name: /Example Provider/ })).getByTitle("Example Provider");
    expect(icon()).toHaveClass("fallback");
    expect(icon().querySelector("img")).not.toBeInTheDocument();

    rerenderProvider({ ...provider, options: { catalog_id: "stepfun" } });
    const image = icon().querySelector("img");
    expect(image).toHaveAttribute("src", "/provider-icons/stepfun-color.svg");
    fireEvent.error(image!);
    expect(icon()).toHaveClass("fallback");
    expect(icon().querySelector("img")).not.toBeInTheDocument();

    rerenderProvider({ ...provider, options: { catalog_id: "kimi" } });
    expect(icon().querySelector("img")).toHaveAttribute("src", "/provider-icons/kimi-color.svg");
    expect(icon()).not.toHaveClass("fallback");
  });

  it("uses the monitoring snapshot independently of the configured enabled state", () => {
    setup({
      provider, adapter: { type: provider.type, capabilities: ["chat"] },
      route_count: 0, active_route_count: 0, resource_count: 0, active_resource_count: 0, healthy_resource_count: 0,
      state: "down", status_label: "Functional Down", status_detail: "active_probe:connection_failed",
      configuration: { state: "healthy", source: "configuration", samples: 0 },
      resources: { state: "unknown", source: "resources", samples: 0 },
      active_probe: { state: "down", source: "active_probe", samples: 1, success_rate: 0 },
      gateway: { state: "unknown", source: "gateway_request", samples: 0 },
      quota: { supported: false, limit_reached: false, successful_accounts: 0, failed_accounts: 0 },
      quality_score: 0, trend: [],
    });
    const row = screen.getByRole("row", { name: /Example Provider/ });
    expect(within(row).getByText("故障", { exact: true })).toBeVisible();
    expect(within(row).getByText("启用", { exact: true })).toBeVisible();
  });

  it.each([{ ...provider, status: "disabled" }, { ...provider, healthy: false }])("keeps unobserved health neutral without a monitoring snapshot (%j)", async (configuredProvider) => {
    setup(undefined, false, configuredProvider);
    expect(within(screen.getByRole("row", { name: /Example Provider/ })).getByText("待观测", { exact: true })).toBeVisible();
    await userEvent.setup().click(screen.getByRole("button", { name: "可用性监控" }));
    expect(within(screen.getByRole("row", { name: /Example Provider/ })).getByText("待观测", { exact: true })).toBeVisible();
  });

  it.each(["down", "degraded"] as const)("keeps health awaiting observation when only configuration is %s", async (configurationState) => {
    const user = userEvent.setup();
    setup({
      provider, adapter: { type: provider.type, capabilities: ["chat"] },
      route_count: 0, active_route_count: 0, resource_count: 0, active_resource_count: 0, healthy_resource_count: 0,
      state: configurationState, status_label: configurationState === "down" ? "Functional Down" : "Degraded", status_detail: "configuration:provider_unhealthy",
      configuration: { state: configurationState, source: "configuration", samples: 0 },
      resources: { state: "unknown", source: "configuration", samples: 0 },
      active_probe: { state: "unknown", source: "active_probe", samples: 0 },
      gateway: { state: "unknown", source: "gateway_request", samples: 0 },
      quota: { supported: false, limit_reached: false, successful_accounts: 0, failed_accounts: 0 },
      quality_score: 0, trend: [],
    });
    let row = screen.getByRole("row", { name: /Example Provider/ });
    expect(within(row).getByText("待观测", { exact: true })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "可用性监控" }));
    row = screen.getByRole("row", { name: /Example Provider/ });
    expect(within(row).getByText("待观测", { exact: true })).toBeVisible();
  });

  it("keeps the same filtered providers when opening monitoring and returning", async () => {
    const user = userEvent.setup();
    setup();
    await user.click(screen.getByRole("button", { name: "可用性监控" }));
    expect(screen.getByRole("columnheader", { name: "账号配额" })).toBeVisible();
    expect(screen.getByRole("row", { name: /Example Provider/ })).toBeVisible();
    await user.click(screen.getByRole("button", { name: /^供应商列表$/ }));
    expect(screen.getByRole("row", { name: /Example Provider/ })).toBeVisible();
    expect(screen.queryByRole("columnheader", { name: "账号配额" })).not.toBeInTheDocument();
  });

  it("preserves edit and destructive action handlers without adding read-only actions", async () => {
    const user = userEvent.setup();
    const { onEdit, onDelete } = setup();
    await user.click(screen.getByRole("button", { name: /^编辑$/ }));
    expect(onEdit).toHaveBeenCalledWith(provider);
    await user.click(screen.getByRole("button", { name: /^删除$/ }));
    expect(onDelete).toHaveBeenCalledWith(provider);
  });

  it("does not expose editing or deletion when the configuration is read-only", () => {
    setup(undefined, true);
    expect(screen.queryByRole("button", { name: /^编辑$/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^删除$/ })).not.toBeInTheDocument();
    expect(screen.queryByText("更多", { exact: true })).not.toBeInTheDocument();
  });
});
