import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { adminFetch } from "../resources/payloads";
import { BillingRateCards } from "./billing-rate-cards";
import { completedRates, decimalAmount, emptyRates } from "./billing-pricing-fields";

vi.mock("../resources/payloads", () => ({ adminFetch: vi.fn(), readAdminError: async () => "Request failed" }));
const api = { baseURL: "", adminToken: "test-token" };
function setup() {
  const data = emptyData();
  data.models.push({ id: "model", name: "test-model", family: "test", modality: "chat", status: "active" });
  render(<BillingRateCards api={api} data={data} />);
  fireEvent.change(screen.getByRole("combobox", { name: "计价对象" }), { target: { value: "test-model" } });
  fireEvent.change(screen.getByRole("textbox", { name: "普通输入" }), { target: { value: "2" } });
  fireEvent.change(screen.getByRole("textbox", { name: "输出" }), { target: { value: "6" } });
}
const preview = { snapshot: {}, charge: { amount: "0.008000000000", currency: "USD", lines: [{ kind: "input", units: 1000, amount: "0.002000000000" }, { kind: "output", units: 1000, amount: "0.006000000000" }] } };
describe("BillingRateCards", () => {
  beforeEach(() => vi.mocked(adminFetch).mockReset());
  it("calculates with two base rates and publishes the same resolved cache rates", async () => {
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify(preview)));
    setup();
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    await screen.findByText("0.008");
    const body = JSON.parse(String(vi.mocked(adminFetch).mock.calls[0][2]?.body));
    expect(body.card.rates).toEqual({ input: "2", output: "6", cache_read: "2", cache_write: "2", cache_write_5m: "2", cache_write_1h: "2" });
    expect(body.usage.cache_write_input_tokens).toBe(0);
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify({ data: { id: "rate_test" } })));
    fireEvent.click(screen.getByRole("button", { name: "发布用于后续核对" }));
    await screen.findByText("已发布。后续请求将按此价目核对，实际收费保持不变。");
    expect(JSON.parse(String(vi.mocked(adminFetch).mock.calls[1][2]?.body)).rates).toEqual(body.card.rates);
    expect(screen.getByRole("button", { name: "已发布" })).toBeDisabled();
    fireEvent.change(screen.getByRole("textbox", { name: "普通输入" }), { target: { value: "3" } });
    expect(screen.queryByRole("button", { name: "已发布" })).not.toBeInTheDocument();
    expect(screen.queryByText("0.008")).not.toBeInTheDocument();
  });
  it("preserves unknown provider rates when the corresponding usage is zero", async () => {
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify(preview)));
    const data = emptyData();
    data.providerModels.push({ id: "upstream", provider_id: "provider", upstream_model: "test-model", status: "active" });
    render(<BillingRateCards api={api} data={data} />);
    fireEvent.change(screen.getByRole("combobox", { name: "价目用途" }), { target: { value: "provider" } });
    fireEvent.change(screen.getByRole("combobox", { name: "计价对象" }), { target: { value: "provider:test-model" } });
    fireEvent.change(screen.getByRole("textbox", { name: "普通输入" }), { target: { value: "2" } });
    fireEvent.change(screen.getByRole("textbox", { name: "输出 Token" }), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    await waitFor(() => expect(adminFetch).toHaveBeenCalledOnce());
    const body = JSON.parse(String(vi.mocked(adminFetch).mock.calls[0][2]?.body));
    expect(body.card.rates.output).toBe("");
    for (const key of ["cache_read", "cache_write", "cache_write_5m", "cache_write_1h"]) expect(body.card.rates[key]).toBe("");
    vi.mocked(adminFetch).mockClear();
    fireEvent.change(screen.getByLabelText("缓存读取 Token", { exact: true }), { target: { value: "1" } });
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("试算用量涉及未知单价");
    expect(adminFetch).not.toHaveBeenCalled();
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify(preview)));
    fireEvent.change(screen.getByLabelText("缓存读取", { exact: true }), { target: { value: "0" } });
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    await waitFor(() => expect(adminFetch).toHaveBeenCalledOnce());
    expect(JSON.parse(String(vi.mocked(adminFetch).mock.calls[0][2]?.body)).card.rates.cache_read).toBe("0");
  });
  it("rejects contradictory cache usage before sending a request", async () => {
    setup();
    fireEvent.change(screen.getByLabelText("缓存读取 Token", { exact: true }), { target: { value: "1001" } });
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("缓存读取与写入之和不能超过总输入 Token。");
    expect(adminFetch).not.toHaveBeenCalled();
  });
  it("normalizes all three cache-write categories into the provider usage contract", async () => {
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify(preview)));
    setup();
    for (const [label, value] of [["其他缓存写入 Token", "10"], ["5 分钟缓存写入 Token", "20"], ["1 小时缓存写入 Token", "30"]]) fireEvent.change(screen.getByLabelText(label, { exact: true }), { target: { value } });
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    await waitFor(() => expect(adminFetch).toHaveBeenCalledOnce());
    const body = JSON.parse(String(vi.mocked(adminFetch).mock.calls[0][2]?.body));
    expect(body.usage).toMatchObject({ cache_write_input_tokens: 60, cache_write_5m_input_tokens: 20, cache_write_1h_input_tokens: 30 });
    expect(body.usage).not.toHaveProperty("cache_write_other_tokens");
  });
  it("rejects windows with no selected weekday", async () => {
    setup();
    fireEvent.click(screen.getByRole("button", { name: "添加时段", hidden: true }));
    for (const day of ["周一", "周二", "周三", "周四", "周五"]) fireEvent.click(screen.getByRole("checkbox", { name: day, hidden: true }));
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("每个时段至少选择一天");
    expect(adminFetch).not.toHaveBeenCalled();
  });
  it("formats historical rates and charges without changing raw evidence", async () => {
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify({ data: [{ id: "rate", kind: "tenant", target: "test-model", revision: 1, currency: "USD", source: "test", rates: { ...emptyRates(), input: "1234.500000000000" } }] })));
    render(<BillingRateCards api={api} data={emptyData()} view="history" />);
    await screen.findByText("1,234.50");
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify({ data: [{ kind: "shadow_settlement", at: "2026-09-06T00:00:00Z", data: { tenant: { status: "estimated", charge: { amount: "1234.500000000000", currency: "USD" } } } }] })));
    fireEvent.change(screen.getByRole("textbox", { name: "请求 ID" }), { target: { value: "request" } });
    fireEvent.click(screen.getByRole("button", { name: "查询记录" }));
    await screen.findByText("1,234.50 USD");
    expect(screen.getByText(/"amount": "1234.500000000000"/)).toBeInTheDocument();
  });
  it("automatically loads published cards and gives a readable missing-record message", async () => {
    vi.mocked(adminFetch).mockResolvedValue(new Response(JSON.stringify({ data: [] })));
    render(<BillingRateCards api={api} data={emptyData()} view="history" />);
    await screen.findByText("还没有发布价目");
    vi.mocked(adminFetch).mockResolvedValue(new Response("{}", { status: 404 }));
    fireEvent.change(screen.getByRole("textbox", { name: "请求 ID" }), { target: { value: " req_missing " } });
    fireEvent.click(screen.getByRole("button", { name: "查询记录" }));
    await screen.findByText("没有找到计费记录，请检查请求 ID，并确认请求发生在功能启用之后。");
    expect(vi.mocked(adminFetch).mock.calls[1][1]).toBe("/api/admin/billing/evidence/req_missing");
  });
});
describe("Billing price presentation", () => {
  it("preserves explicit zero and resolves cache-write inheritance", () => {
    expect(completedRates({ ...emptyRates(), input: "2", output: "6", cache_read: "0", cache_write: "1", cache_write_1h: "0" })).toEqual({ input: "2", output: "6", cache_read: "0", cache_write: "1", cache_write_5m: "1", cache_write_1h: "0" });
  });
  it("formats exact decimal amounts without losing integer or fractional precision", () => {
    expect(decimalAmount("999999999999999999.123456789123")).toBe("999,999,999,999,999,999.123456789123");
    expect(decimalAmount("0.000000000001")).toBe("0.000000000001");
    expect(decimalAmount("0.000000000000")).toBe("0.00");
  });
});
