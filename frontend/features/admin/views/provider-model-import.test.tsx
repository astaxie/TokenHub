import { useState } from "react";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { type ProviderModel } from "../core/types";
import { emptyData } from "../domain/catalog";
import { modelConfig } from "../resources/provider-model-config";
import { ProviderModelImportModal } from "./provider-model-import-modal";
import { ModelDirectoryView } from "./model-directory";

const api = { baseURL: "http://gateway.test", adminToken: "synthetic-admin-token" };
function fixture(subscription = false) {
  const data = emptyData();
  data.providers = [{ id: "provider", name: "Test Provider", type: "mock", priority: 1, status: "active", healthy: true }];
  data.providerModels = ["alpha", "beta"].map<ProviderModel>((name) => ({ id: name, provider_id: "provider", upstream_model: name, modality: "chat", status: "active", call_supported: true, input_price_usd_per_1m: subscription ? 0 : 1, output_price_usd_per_1m: subscription ? 0 : 2, metadata: subscription ? { billing_mode: "subscription", pricing_status: "unverified", custom_note: "preserve" } : undefined }));
  return data;
}
function modal(data = fixture(), onReload = vi.fn(), onClose = vi.fn()) {
  return <ProviderModelImportModal api={api} data={data} onReload={onReload} onClose={onClose} onOpenProviders={vi.fn()} />;
}
async function selectAndPrice(user: ReturnType<typeof userEvent.setup>, input = "0", output = "3") {
  await user.click(screen.getByRole("checkbox", { name: "全选可引入模型" }));
  await user.type(screen.getByLabelText("统一输入价格"), input);
  await user.type(screen.getByLabelText("统一输出价格"), output);
  await user.click(screen.getByRole("button", { name: "应用到所选模型" }));
}

describe("Provider model import dialog", () => {
  it("closes after every selected model is imported and the directory refresh finishes", async () => {
    let finishReload: () => void = () => undefined;
    const reload = vi.fn(() => new Promise<void>((resolve) => { finishReload = resolve; }));
    const close = vi.fn();
    const fetch = vi.fn().mockResolvedValue(new Response("{}"));
    vi.stubGlobal("fetch", fetch);
    render(modal(fixture(), reload, close));
    const user = userEvent.setup();
    await selectAndPrice(user);
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(close).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toHaveAttribute("aria-busy", "true");
    await act(async () => finishReload());
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("keeps successful imports visible if the directory refresh fails", async () => {
    const reload = vi.fn().mockRejectedValue(new Error("Synthetic refresh failure"));
    const close = vi.fn();
    const fetch = vi.fn().mockResolvedValue(new Response("{}"));
    vi.stubGlobal("fetch", fetch);
    render(modal(fixture(), reload, close));
    const user = userEvent.setup();
    await selectAndPrice(user);
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    await screen.findByText("目录刷新失败，已成功引入的模型不会重复提交。请关闭后刷新目录。");
    expect(close).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toHaveAttribute("aria-busy", "false");
    expect(screen.getAllByText("已引入，重试时自动跳过")).toHaveLength(2);
    expect(screen.getByRole("button", { name: "引入所选模型" })).toBeDisabled();
  });

  it("explains missing prices and reveals the first invalid selected model before submitting", async () => {
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    render(modal());
    const user = userEvent.setup();
    const submit = screen.getByRole("button", { name: "引入所选模型" });
    expect(submit).toBeDisabled();
    await user.click(screen.getByRole("checkbox", { name: "全选可引入模型" }));
    expect(submit).toBeEnabled();
    expect(submit).toHaveAccessibleDescription("还有 2 个所选模型的对外价格未填完整或无效。");
    await user.type(screen.getByLabelText("搜索 Provider 模型"), "beta");
    await user.click(submit);
    expect(screen.getByRole("alert")).toHaveTextContent("尚未引入：还有 2 个所选模型的对外价格未填完整或无效。");
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.getByLabelText("搜索 Provider 模型")).toHaveValue("");
    expect(within(screen.getByRole("region", { name: "alpha" })).getByLabelText("输入价格（USD / 1M tokens）")).toHaveFocus();
  });

  it("requires bulk prices to be applied and ignores unused bulk staging fields on submit", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response("{}"));
    vi.stubGlobal("fetch", fetch);
    render(modal());
    const user = userEvent.setup();
    await user.click(screen.getByRole("checkbox", { name: "alpha" }));
    await user.type(screen.getByLabelText("统一输入价格"), "1");
    await user.type(screen.getByLabelText("统一输出价格"), "2");
    expect(screen.getByText("填写统一价格后，请点击“应用到所选模型”；也可逐项填写。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    expect(fetch).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "应用到所选模型" }));
    await user.clear(screen.getByLabelText("统一输入价格"));
    await user.type(screen.getByLabelText("统一输入价格"), "-1");
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    await screen.findByText("已引入，重试时自动跳过");
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({ input_price_usd_per_1m: 1, output_price_usd_per_1m: 2 });
  });

  it("selects filtered callable models while preserving drafts and explaining conflicts", async () => {
    const data = fixture();
    data.models = [{ id: "old", name: "beta", family: "beta", modality: "chat", status: "active" }];
    data.providerModels.push({ id: "unknown", provider_id: "provider", upstream_model: "unknown", modality: "chat", status: "active", metadata: { pricing_status: "unverified" } });
    render(modal(data));
    const user = userEvent.setup();
    expect(screen.getByRole("dialog")).toHaveFocus();
    expect(screen.getByRole("checkbox", { name: "beta" })).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "unknown" })).toBeDisabled();
    expect(screen.getByText("同名对外模型已存在，不会覆盖现有价格和路由。")).toBeVisible();
    await user.type(screen.getByLabelText("搜索 Provider 模型"), "alpha");
    await selectAndPrice(user);
    expect(screen.getByLabelText("对外模型 ID")).toHaveValue("alpha");
    expect(screen.getByLabelText("对外模型 ID")).toHaveAttribute("readonly");
    await user.clear(screen.getByLabelText("搜索 Provider 模型"));
    expect(screen.getByLabelText("输入价格（USD / 1M tokens）")).toHaveValue(0);
    expect(screen.getByRole("button", { name: "引入所选模型" })).toBeEnabled();
    await user.type(screen.getByLabelText("搜索 Provider 模型"), "nothing");
    expect(screen.getByText("没有匹配的 Provider 模型")).toBeVisible();
  });

  it("keeps successful creates and cost confirmations across partial failures and reload errors", async () => {
    const calls: Array<{ path: string; method: string; body: Record<string, unknown> }> = [];
    let failBeta = true;
    vi.stubGlobal("fetch", vi.fn(async (url: string, init: RequestInit) => {
      const body = JSON.parse(String(init.body));
      calls.push({ path: new URL(url).pathname, method: init.method!, body });
      if (init.method === "POST" && body.name === "beta" && failBeta) { failBeta = false; return new Response(JSON.stringify({ error: { message: "Synthetic route failure" } }), { status: 400 }); }
      return new Response("{}", { status: 200 });
    }));
    const reload = vi.fn().mockRejectedValueOnce(new Error("Synthetic refresh failure")).mockResolvedValue(undefined);
    const close = vi.fn();
    render(modal(fixture(true), reload, close));
    const user = userEvent.setup();
    await selectAndPrice(user);
    expect(screen.getByRole("button", { name: "引入所选模型" })).toBeEnabled();
    const confirmation = screen.getByRole("checkbox", { name: "确认所选订阅模型的按量成本为 0（订阅费用另行核算）" });
    expect(confirmation).not.toBeChecked();
    expect(confirmation.closest("footer")).not.toBeNull();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    expect(confirmation).toHaveFocus();
    expect(confirmation).not.toBeChecked();
    expect(confirmation).toHaveAttribute("aria-invalid", "true");
    expect(screen.getByRole("alert")).toHaveTextContent("尚未引入：请勾选上方的订阅成本确认，再次点击“引入所选模型”。");
    expect(confirmation).toHaveAccessibleDescription("尚未引入：请勾选上方的订阅成本确认，再次点击“引入所选模型”。");
    expect(calls).toHaveLength(0);
    await user.click(confirmation);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(confirmation).not.toHaveAttribute("aria-invalid", "true");
    expect(calls).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    await screen.findByText("目录刷新失败，已成功引入的模型不会重复提交。请关闭后刷新目录。");
    expect(close).not.toHaveBeenCalled();
    expect(screen.getByRole("status").closest("footer")).not.toBeNull();
    expect(screen.getByText("目录刷新失败，已成功引入的模型不会重复提交。请关闭后刷新目录。").closest("footer")).not.toBeNull();
    expect(within(screen.getByRole("region", { name: "alpha" })).getByText("已引入，重试时自动跳过")).toBeVisible();
    expect(within(screen.getByRole("region", { name: "beta" })).getByRole("alert")).toHaveTextContent("Synthetic route failure");
    expect(screen.queryByRole("checkbox", { name: /确认所选订阅模型/ })).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    await waitFor(() => expect(screen.getAllByText("已引入，重试时自动跳过")).toHaveLength(2));
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
    expect(calls.filter((call) => call.method === "PATCH")).toEqual([
      { path: "/api/admin/provider-models/alpha", method: "PATCH", body: { metadata: { billing_mode: "subscription", pricing_status: "configured", custom_note: "preserve" } } },
      { path: "/api/admin/provider-models/beta", method: "PATCH", body: { metadata: { billing_mode: "subscription", pricing_status: "configured", custom_note: "preserve" } } },
    ]);
    expect(calls.filter((call) => call.method === "POST").map((call) => call.body.name)).toEqual(["alpha", "beta", "beta"]);
    expect(calls[1].body).toMatchObject({ name: "alpha", create_only: true, input_price_usd_per_1m: 0, output_price_usd_per_1m: 3, routes: [{ provider_id: "provider", provider_model: "alpha", weight: 100, status: "active", project_scope: "all" }] });
    expect(screen.getByRole("button", { name: "引入所选模型" })).toBeDisabled();
  });

  it("locks edits and dismissal while an import is pending", async () => {
    let resolve: (value: Response) => void = () => undefined;
    const request = new Promise<Response>((done) => { resolve = done; });
    const fetch = vi.fn().mockReturnValueOnce(request).mockResolvedValue(new Response("{}"));
    vi.stubGlobal("fetch", fetch);
    const close = vi.fn();
    render(modal(fixture(), vi.fn(), close));
    const user = userEvent.setup();
    await selectAndPrice(user);
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    expect(screen.getByRole("dialog")).toHaveAttribute("aria-busy", "true");
    expect(screen.getByLabelText("选择 Provider")).toBeDisabled();
    expect(screen.getByRole("checkbox", { name: "alpha" })).toBeDisabled();
    for (const button of screen.getAllByRole("button", { name: "关闭" })) expect(button).toBeDisabled();
    await user.keyboard("{Escape}");
    fireEvent.submit(screen.getByRole("dialog"));
    expect(close).not.toHaveBeenCalled();
    expect(fetch).toHaveBeenCalledTimes(1);
    await act(async () => resolve(new Response("{}")));
    await waitFor(() => expect(screen.getByRole("dialog")).toHaveAttribute("aria-busy", "false"));
  });

  it("requires a separate public duration rate for audio models", async () => {
    const data = fixture();
    data.providerModels = [{ ...data.providerModels[0], modality: "audio", metadata: { audio_price_usd_per_second: "4" } }];
    render(modal(data));
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    const user = userEvent.setup();
    await selectAndPrice(user);
    expect(screen.getByLabelText(/音频价格（USD \/ 秒）/)).toHaveValue(null);
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    expect(screen.getByLabelText(/音频价格（USD \/ 秒）/)).toHaveFocus();
    expect(fetch).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText(/音频价格（USD \/ 秒）/), "0");
    expect(screen.getByRole("button", { name: "引入所选模型" })).toBeEnabled();
  });

  it("focuses an invalid optional rerank price without publishing any models", async () => {
    const data = fixture();
    data.providerModels = [{ ...data.providerModels[0], modality: "rerank" }];
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    render(modal(data));
    const user = userEvent.setup();
    await selectAndPrice(user);
    await user.type(screen.getByLabelText("搜索单元价格 USD/次"), "-1");
    await user.click(screen.getByRole("button", { name: "引入所选模型" }));
    expect(screen.getByLabelText("搜索单元价格 USD/次")).toHaveFocus();
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each(["collision", "unconfirmed cost"])("allows a selection blocked by %s after refresh to be removed", async (reason) => {
    const data = fixture();
    const fetch = vi.fn();
    vi.stubGlobal("fetch", fetch);
    const view = render(modal(data));
    const user = userEvent.setup();
    await selectAndPrice(user);
    const updated = structuredClone(data);
    if (reason === "collision") updated.models = [{ id: "external-alpha", name: "alpha", family: "alpha", modality: "chat", status: "active" }];
    else updated.providerModels[0].metadata = { pricing_status: "unverified", catalog_price_reference: "true" };
    view.rerender(modal(updated));
    await user.type(screen.getByLabelText("搜索 Provider 模型"), "beta");
    const submit = screen.getByRole("button", { name: "引入所选模型" });
    expect(submit).toHaveAccessibleDescription("部分所选模型已不可引入，请取消选择后重试。");
    await user.click(submit);
    expect(fetch).not.toHaveBeenCalled();
    const blocked = screen.getByRole("checkbox", { name: "alpha" });
    expect(blocked).toHaveFocus();
    expect(blocked).toBeEnabled();
    await user.click(blocked);
    expect(blocked).not.toBeChecked();
    expect(blocked).toBeDisabled();
    expect(submit).not.toHaveAttribute("aria-describedby");
  });

  it("restores focus on close and provides an empty provider path", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return <><button onClick={() => setOpen(true)}>Open import</button>{open ? <ProviderModelImportModal api={api} data={emptyData()} onReload={vi.fn()} onClose={() => setOpen(false)} onOpenProviders={vi.fn()} /> : null}</>;
    }
    render(<Harness />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "Open import" }));
    expect(screen.getByText("暂无可用的 Provider")).toBeVisible();
    expect(screen.getByRole("button", { name: "前往 Provider 渠道" })).toBeVisible();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("button", { name: "Open import" })).toHaveFocus();
  });

  it("keeps import unavailable in a read-only directory", () => {
    render(<ModelDirectoryView api={api} config={modelConfig()} data={fixture()} loading={false} readOnly onReload={vi.fn()} onCreateModel={vi.fn()} onOpenProviders={vi.fn()} onOpenRoutes={vi.fn()} onEditModel={vi.fn()} onDeleteModel={vi.fn()} />);
    expect(screen.queryByRole("button", { name: "从 Provider 引入" })).not.toBeInTheDocument();
  });
});
