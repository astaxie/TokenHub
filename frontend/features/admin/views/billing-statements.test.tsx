import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { BillingStatements, StatementLauncher } from "./billing-statements";

const api = { baseURL: "", adminToken: "test" };
const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, "showModal");
beforeEach(() => { setActiveLanguage("zh-CN"); window.localStorage.clear(); });
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); if (originalShowModal) Object.defineProperty(HTMLDialogElement.prototype, "showModal", originalShowModal); else Reflect.deleteProperty(HTMLDialogElement.prototype, "showModal"); });
describe("BillingStatements", () => {
  it("invalidates a preview when filters change", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ data: [{ id: "project", name: "Example" }] }))).mockImplementationOnce((_url, options) => {
      const query = JSON.parse(options.body);
      return Promise.resolve(new Response(JSON.stringify({ query, generated_at: "2020-02-02T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "request_admission", rows: [], totals: {}, unknown_count: 0, incomplete_count: 0, estimated_margin_usd: null })));
    });
    vi.stubGlobal("fetch", fetchMock);
    render(<BillingStatements api={api} />);
    await screen.findByRole("option", { name: "Example (project)" });
    expect(screen.queryByRole("button", { name: "导出当前预览 CSV" })).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("客户名称"), { target: { value: "Customer" } });
    const projects = screen.getByLabelText("客户项目（可多选）") as HTMLSelectElement;
    projects.options[0].selected = true; fireEvent.change(projects);
    fireEvent.submit(screen.getByRole("button", { name: "预览对账单" }).closest("form")!);
    await screen.findByRole("button", { name: "导出当前预览 CSV" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    fireEvent.change(screen.getByLabelText("客户名称"), { target: { value: "Another customer" } });
    expect(screen.queryByRole("button", { name: "导出当前预览 CSV" })).not.toBeInTheDocument();
  });
  it("reports project-loading failures", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("offline")));
    render(<BillingStatements api={api} />);
    await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("offline"));
  });
  it.each([
    { language: "zh-CN" as const, preview: "预览对账单", label: "音频秒数", currency: "US$" },
    { language: "en" as const, preview: "Preview statement", label: "Audio seconds", currency: "$" },
    { language: "ja" as const, preview: "明細書をプレビュー", label: "音声の秒数", currency: "$" },
  ])("renders native audio quantities and keeps token pricing in $language", async ({ language, preview, label, currency }) => {
    setActiveLanguage(language);
    const base = { source: "provider_estimate", at: "2020-01-01T00:00:00Z", timezone: "UTC", currency: "USD", status: "estimated", reason: "recorded_tenant_charge" };
    const rows = [
      { ...base, id: "paid", model: "audio-paid", amount: "0.25", units: {}, lines: [{ kind: "audio_second", units: 0, quantity: "1.25e1", rate: "0.02", amount: "0.25" }] },
      { ...base, id: "zero", model: "audio-zero", amount: "0", units: {}, lines: [{ kind: "audio_second", units: 0, quantity: "0", rate: "0.02", amount: "0" }] },
      { ...base, id: "token", model: "token-model", amount: "0.0001", units: { input: 100 }, lines: [{ kind: "input", units: 100, rate: "1", amount: "0.0001" }] },
    ];
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ data: [] }))).mockImplementationOnce((_url, options) => Promise.resolve(new Response(JSON.stringify({ query: JSON.parse(options.body), generated_at: "2020-02-02T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "upstream_attempt_start", rows, totals: {}, unknown_count: 0, incomplete_count: 0, estimated_margin_usd: null })))));
    render(<BillingStatements api={api} side="provider" />);
    fireEvent.submit(screen.getByRole("button", { name: preview }).closest("form")!);
    expect(await screen.findByText(`${label}: 12.5`, { exact: true })).toBeInTheDocument();
    expect(screen.getByText(`${label}: 0`, { exact: true })).toBeInTheDocument();
    expect(screen.getByText(`${label}: 12.5 × ${currency}0.02 = ${currency}0.25`, { exact: true })).toBeInTheDocument();
    expect(screen.getByText(`${label}: 0 × ${currency}0.02 = ${currency}0.00`, { exact: true })).toBeInTheDocument();
    expect(screen.getAllByText(/\/ 1M =/)).toHaveLength(1);
  });
  it("keeps native audio formulas beside converted paid and zero amounts", async () => {
    const base = { source: "provider_estimate", at: "2020-01-01T00:00:00Z", timezone: "UTC", currency: "USD", status: "estimated", units: {} };
    const rows = [
      { ...base, id: "paid", model: "audio-paid", amount: "0.25", usd: "0.25", display_amount: "1.8125", lines: [{ kind: "audio_second", units: 0, quantity: "12.5", rate: "0.02", amount: "0.25" }] },
      { ...base, id: "zero", model: "audio-zero", amount: "0", usd: "0", display_amount: "0", lines: [{ kind: "audio_second", units: 0, quantity: "0", rate: "0.02", amount: "0" }] },
    ];
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ data: [] }))).mockImplementationOnce((_url, options) => Promise.resolve(new Response(JSON.stringify({ query: JSON.parse(options.body), generated_at: "2020-02-02T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "upstream_attempt_start", rows, totals: { "provider_estimate:USD": "0.25" }, unknown_count: 0, incomplete_count: 0, estimated_margin_usd: null, conversion: { currency: "CNY", usd_exchange_rate: "7.25" }, display_totals: { provider_estimate: "1.8125" } }))));
    vi.stubGlobal("fetch", fetchMock);
    render(<BillingStatements api={api} side="provider" />);
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "CNY" } });
    fireEvent.change(screen.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value: "7.25" } });
    fireEvent.submit(screen.getByRole("button", { name: "预览对账单" }).closest("form")!);
    expect(await screen.findByText("换算金额：¥1.8125", { exact: true })).toBeInTheDocument();
    expect(screen.getByText("换算金额：¥0.00", { exact: true })).toBeInTheDocument();
    expect(screen.getByText("音频秒数: 12.5 × US$0.02 = US$0.25", { exact: true })).toBeInTheDocument();
    expect(screen.getByText("音频秒数: 0 × US$0.02 = US$0.00", { exact: true })).toBeInTheDocument();
    expect(screen.getByText("本地估算成本 · CNY: ¥1.8125", { exact: true })).toBeInTheDocument();
    expect(screen.queryByText(/\/ 1M =/)).not.toBeInTheDocument();
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toMatchObject({ display_currency: "CNY", display_exchange_rate: "7.25" });
  });
});

it("portal preview does not submit the parent provider form", async () => {
  Object.defineProperty(HTMLDialogElement.prototype, "showModal", { configurable: true, value: function(this: HTMLDialogElement) { this.setAttribute("open", ""); } });
  const parentSubmit = vi.fn((event: React.FormEvent) => event.preventDefault());
  vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({ data: [] }))).mockResolvedValueOnce(new Response(JSON.stringify({ query: { side: "provider", customer: "", timezone: "UTC" }, generated_at: "2020-02-02T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "upstream_attempt_start", rows: [], totals: {}, unknown_count: 0, incomplete_count: 0, estimated_margin_usd: null }))));
  render(<form onSubmit={parentSubmit}><StatementLauncher api={api} side="provider" /></form>);
  fireEvent.click(screen.getByRole("button", { name: "上游费用对账单" }));
  fireEvent.submit(screen.getByRole("button", { name: "预览对账单" }).closest("form")!);
  await screen.findByRole("button", { name: "导出当前预览 CSV" });
  expect(parentSubmit).not.toHaveBeenCalled();
});
