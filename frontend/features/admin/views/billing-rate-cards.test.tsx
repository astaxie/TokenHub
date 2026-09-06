import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { emptyData } from "../domain/catalog";
import { adminFetch } from "../resources/payloads";
import { BillingRateCards } from "./billing-rate-cards";
import { decimalAmount, emptyRates } from "./billing-pricing-fields";

vi.mock("../resources/payloads", () => ({ adminFetch: vi.fn(), readAdminError: async () => "Request failed" }));
const api = { baseURL: "", adminToken: "test-token" };
const base = { kind: "tenant", target: "test-model", source: "model", currency: "USD", rates: { ...emptyRates(), input: "1", output: "2" }, periods: [] };
const proposed = { ...base, rates: { ...base.rates, input: "2", output: "6" } };
const quote = { current: base, proposed, fingerprint: "current-hash", changed: true, snapshot: {}, charge: { amount: "0.008000000000", currency: "USD", lines: [{ kind: "input", units: 1000, amount: "0.002000000000" }, { kind: "output", units: 1000, amount: "0.006000000000" }] } };
function respond(value: unknown) { return new Response(JSON.stringify(value)); }
function requests(path: string) { return vi.mocked(adminFetch).mock.calls.filter((call) => call[1] === path); }
async function setup() {
  const data = emptyData(); data.models.push({ id: "model", name: "test-model", family: "test", modality: "chat", status: "active" });
  render(<BillingRateCards api={api} data={data} />);
  fireEvent.change(screen.getByRole("combobox", { name: "计价对象" }), { target: { value: "test-model" } });
  await waitFor(() => expect(screen.getByLabelText("普通输入", { exact: true })).toHaveValue("1"));
  fireEvent.change(screen.getByLabelText("普通输入", { exact: true }), { target: { value: "2" } });
  fireEvent.change(screen.getByLabelText("输出", { exact: true }), { target: { value: "6" } });
}
describe("Billing model pricing", () => {
  beforeEach(() => {
    vi.mocked(adminFetch).mockReset().mockImplementation(async (_api, path) => {
      if (String(path).includes("/models/")) return respond({ card: base, fingerprint: "current-hash" });
      if (String(path).includes("/preview")) return respond(quote);
      return respond({ data: [] });
    });
    HTMLDialogElement.prototype.showModal = function () { this.open = true; };
    HTMLDialogElement.prototype.close = function () { this.open = false; };
  });
  it("loads real model prices and only writes after explicit confirmation", async () => {
    await setup();
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    await screen.findByText("0.008");
    expect(JSON.parse(String(requests("/api/admin/billing/preview")[0][2]?.body)).card.rates.cache_read).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "应用到模型价格" }));
    const dialog = await screen.findByRole("dialog", { name: "确认调整模型价格" });
    expect(within(dialog).getByText("test-model")).toBeInTheDocument();
    expect(requests("/api/admin/billing/model-pricing/apply")).toHaveLength(0);
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(requests("/api/admin/billing/model-pricing/apply")).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "应用到模型价格" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "确认应用价格" }));
    await screen.findByText("模型价格已更新。新请求立即使用新价，在途请求保持原价。");
    const payload = JSON.parse(String(requests("/api/admin/billing/model-pricing/apply")[0][2]?.body));
    expect(payload).toMatchObject({ card: proposed, fingerprint: "current-hash", confirmed: true });
    expect(payload.request_id).toBeTruthy();
    expect(screen.getByRole("button", { name: "已应用" })).toBeDisabled();
  });
  it("invalidates a confirmed preview when a price or usage field changes", async () => {
    await setup(); fireEvent.click(screen.getByRole("button", { name: "试算费用" })); await screen.findByText("0.008");
    fireEvent.change(screen.getByLabelText("总输入 Token", { exact: true }), { target: { value: "300" } });
    expect(screen.queryByRole("button", { name: "应用到模型价格" })).not.toBeInTheDocument();
    expect(requests("/api/admin/billing/model-pricing/apply")).toHaveLength(0);
  });
  it("reuses the confirmation ID after a transient apply failure", async () => {
    await setup(); fireEvent.click(screen.getByRole("button", { name: "试算费用" })); await screen.findByText("0.008");
    fireEvent.click(screen.getByRole("button", { name: "应用到模型价格" }));
    vi.mocked(adminFetch).mockRejectedValueOnce(new Error("Temporary failure"));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "确认应用价格" }));
    await screen.findByText("Temporary failure");
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "确认应用价格" }));
    await screen.findByText("模型价格已更新。新请求立即使用新价，在途请求保持原价。");
    const applied = requests("/api/admin/billing/model-pricing/apply");
    expect(JSON.parse(String(applied[0][2]?.body)).request_id).toBe(JSON.parse(String(applied[1][2]?.body)).request_id);
  });
  it("rejects contradictory cache usage before preview", async () => {
    await setup(); fireEvent.change(screen.getByLabelText("缓存读取 Token", { exact: true }), { target: { value: "1001" } });
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("缓存读取与写入之和不能超过总输入 Token。");
    expect(requests("/api/admin/billing/preview")).toHaveLength(0);
  });
  it("normalizes cache write totals and rejects empty weekday sets", async () => {
    await setup();
    for (const [label, value] of [["其他缓存写入 Token", "10"], ["5 分钟缓存写入 Token", "20"], ["1 小时缓存写入 Token", "30"]]) fireEvent.change(screen.getByLabelText(label, { exact: true }), { target: { value } });
    fireEvent.click(screen.getByRole("button", { name: "试算费用" })); await screen.findByText("0.008");
    expect(JSON.parse(String(requests("/api/admin/billing/preview")[0][2]?.body)).usage.cache_write_input_tokens).toBe(60);
    fireEvent.click(screen.getByRole("button", { name: "添加时段", hidden: true }));
    for (const day of ["周一", "周二", "周三", "周四", "周五"]) fireEvent.click(screen.getByRole("checkbox", { name: day, hidden: true }));
    fireEvent.click(screen.getByRole("button", { name: "试算费用" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("每个时段至少选择一天");
  });
  it("shows actual price changes and preserves original request evidence", async () => {
    vi.mocked(adminFetch).mockResolvedValue(respond({ data: [{ id: "change", model_name: "test-model", actor_name: "admin", effective_at: "2026-09-06T00:00:00Z", before: base, after: { ...proposed, rates: { ...proposed.rates, input: "1234.500000000000" } } }] }));
    render(<BillingRateCards api={api} data={emptyData()} view="history" />);
    await screen.findByText("1,234.50");
    expect(requests("/api/admin/billing/rate-cards")).toHaveLength(0);
    vi.mocked(adminFetch).mockResolvedValue(respond({ data: [{ kind: "shadow_settlement", at: "2026-09-06T00:00:00Z", data: { tenant: { status: "estimated", charge: { amount: "1234.500000000000", currency: "USD" } } } }] }));
    fireEvent.change(screen.getByRole("textbox", { name: "请求 ID" }), { target: { value: "request" } });
    fireEvent.click(screen.getByRole("button", { name: "查询记录" }));
    await screen.findByText("1,234.50 USD");
    expect(screen.getByText(/"amount": "1234.500000000000"/)).toBeInTheDocument();
  });
});
it("formats exact decimals without floating point loss", () => {
  expect(decimalAmount("999999999999999999.123456789123")).toBe("999,999,999,999,999,999.123456789123");
  expect(decimalAmount("0.000000000001")).toBe("0.000000000001");
  expect(decimalAmount("0.000000000000")).toBe("0.00");
});
