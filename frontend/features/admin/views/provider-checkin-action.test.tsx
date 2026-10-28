import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { ProviderPluginPanels } from "./provider-plugin-panels";

describe("Subscription account check-in", () => {
  afterEach(() => { vi.unstubAllGlobals(); setActiveLanguage("zh-CN"); });

  function view(resources = true) {
    return render(<ProviderPluginPanels api={{ baseURL: "http://localhost:8080", adminToken: "synthetic" }}
      provider={{ id: "provider", name: "Synthetic Qoder", type: "qoder_subscription", status: "active", healthy: true, priority: 1 }}
      resources={resources ? [{ id: "account", provider_id: "provider", name: "Synthetic account", resource_type: "qoder_account", status: "active", healthy: true, priority: 1, weight: 1 }] : []}
      contributions={[{ plugin_id: "tokenhub.provider.qoder-subscription", id: "subscription-checkin", title: "Daily check-in", slot: "provider.resource.panel", provider_types: ["qoder_subscription"], resource_types: ["qoder_account"], action: "qoder_subscription.checkin.run" }]}
      actions={[{ plugin_id: "tokenhub.provider.qoder-subscription", action_id: "qoder_subscription.checkin.run", capability: "checkin.run", kind: "mutate", input_schema: { type: "object", required: ["resource_id"], properties: { resource_id: { type: "string" } } } }]} />);
  }

  it("submits only the selected account and renders the reward without implementation details", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ data: { outcome: "claimed", credit: 1250, streak: 3 } }), { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);
    view();
    expect(screen.getByText("每日签到")).toBeInTheDocument();
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "签到" }));
    await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("签到成功"));
    expect(screen.getByRole("status")).toHaveTextContent("1,250");
    expect(screen.getByRole("status")).toHaveTextContent("连续签到 3 天");
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toContain("qoder_subscription.checkin.run");
    expect(JSON.parse(String(init.body))).toEqual({ resource_id: "account" });
    expect(screen.queryByText(/tokenhub.provider/)).not.toBeInTheDocument();
  });

  it("disables check-in without a matching account", () => {
    view(false);
    expect(screen.getByRole("button", { name: "签到" })).toBeDisabled();
  });

  it("shows an action failure and allows retrying", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { message: "Synthetic check-in failure" } }), { status: 503 })));
    view();
    await userEvent.click(screen.getByRole("button", { name: "签到" }));
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Synthetic check-in failure"));
    expect(screen.getByRole("button", { name: "签到" })).toBeEnabled();
  });
});
