import { fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { statementCurrencyStorageKey } from "../domain/billing-currency";
import { setActiveLanguage } from "../i18n/runtime";
import { BillingStatements } from "./billing-statements";

const api = { baseURL: "", adminToken: "test" };
beforeEach(() => { setActiveLanguage("zh-CN"); window.localStorage.clear(); });
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });
function mockStatements() {
  const fetch = vi.fn().mockImplementation((_url, options) => {
    if (!options?.body) return Promise.resolve(new Response(JSON.stringify({ data: [] })));
    const query = JSON.parse(options.body);
    return Promise.resolve(new Response(JSON.stringify({ query, generated_at: "2020-02-02T00:00:00Z", from: "2020-01-01T00:00:00Z", to: "2020-02-01T00:00:00Z", time_basis: "upstream_attempt_start", rows: [{ id: "known", source: "provider_billed", at: "2020-01-02T00:00:00Z", timezone: "UTC", model: "sample", currency: "USD", amount: "2", usd: "2", display_amount: "14.5", units: {}, status: "provider_billed" }, { id: "unknown", source: "provider_estimate", at: "2020-01-02T00:00:00Z", timezone: "UTC", model: "sample", currency: "USD", amount: null, usd: null, display_amount: null, units: {}, status: "pending" }], totals: { "provider_billed:USD": "2" }, unknown_count: 1, incomplete_count: 1, estimated_margin_usd: null, conversion: { currency: "CNY", usd_exchange_rate: "7.25" }, display_totals: { provider_billed: "14.5" } })));
  });
  vi.stubGlobal("fetch", fetch);
  return fetch;
}
function submit() { fireEvent.submit(screen.getByRole("button", { name: "预览对账单" }).closest("form")!); }

describe("Statement currency preferences", () => {
  it("requires a positive exact manual rate before requesting conversion", async () => {
    const fetch = mockStatements();
    render(<BillingStatements api={api} side="provider" />);
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "CNY" } });
    for (const value of ["", "0", "-1", "1e2"]) {
      fireEvent.change(screen.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value } });
      submit();
      expect(screen.getByRole("alert")).toHaveTextContent("请输入大于 0 的手动汇率");
    }
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it("shows original and converted amounts, preserves unknowns, and invalidates edited previews", async () => {
    const fetch = mockStatements();
    render(<BillingStatements api={api} side="provider" />);
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "CNY" } });
    fireEvent.change(screen.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value: "7.25" } });
    submit();
    await screen.findByRole("button", { name: "导出当前预览 CSV" });
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toMatchObject({ display_currency: "CNY", display_exchange_rate: "7.25" });
    expect(screen.getByText("US$2.00", { exact: true })).toBeInTheDocument();
    expect(screen.getByText("换算金额：¥14.50")).toBeInTheDocument();
    expect(screen.getByText("换算金额：未知")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value: "7.5" } });
    expect(screen.queryByRole("button", { name: "导出当前预览 CSV" })).not.toBeInTheDocument();
    submit();
    await screen.findByRole("button", { name: "导出当前预览 CSV" });
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "JPY" } });
    expect(screen.queryByRole("button", { name: "导出当前预览 CSV" })).not.toBeInTheDocument();
    expect(screen.getByLabelText("手动汇率：1 USD 兑目标币种")).toHaveValue("");
  });

  it("restores browser currency and per-currency rates across mounts", () => {
    mockStatements();
    const first = render(<BillingStatements api={api} side="provider" />);
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "CNY" } });
    fireEvent.change(screen.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value: "7.25" } });
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "JPY" } });
    fireEvent.change(screen.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value: "150" } });
    first.unmount();
    render(<BillingStatements api={api} side="provider" />);
    expect(screen.getByLabelText("对账单币种")).toHaveValue("JPY");
    expect(screen.getByLabelText("手动汇率：1 USD 兑目标币种")).toHaveValue("150");
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "CNY" } });
    expect(screen.getByLabelText("手动汇率：1 USD 兑目标币种")).toHaveValue("7.25");
    expect(JSON.parse(window.localStorage.getItem(statementCurrencyStorageKey)!)).toEqual({ currency: "CNY", rates: { CNY: "7.25", JPY: "150" } });
  });

  it("preserves another form's saved rate when selecting and editing a different currency", () => {
    mockStatements();
    window.localStorage.setItem(statementCurrencyStorageKey, JSON.stringify({ currency: "CNY", rates: { CNY: "7.1", JPY: "150" } }));
    const first = render(<BillingStatements api={api} side="provider" />);
    const second = render(<BillingStatements api={api} side="provider" />);
    const firstForm = within(first.container), secondForm = within(second.container);
    fireEvent.change(firstForm.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value: "7.3" } });
    expect(JSON.parse(window.localStorage.getItem(statementCurrencyStorageKey)!).rates.CNY).toBe("7.3");
    fireEvent.change(secondForm.getByLabelText("对账单币种"), { target: { value: "JPY" } });
    expect(JSON.parse(window.localStorage.getItem(statementCurrencyStorageKey)!).rates.CNY).toBe("7.3");
    fireEvent.change(secondForm.getByLabelText("手动汇率：1 USD 兑目标币种"), { target: { value: "151" } });
    expect(JSON.parse(window.localStorage.getItem(statementCurrencyStorageKey)!)).toEqual({ currency: "JPY", rates: { CNY: "7.3", JPY: "151" } });
    first.unmount(); second.unmount();
    render(<BillingStatements api={api} side="provider" />);
    expect(screen.getByLabelText("手动汇率：1 USD 兑目标币种")).toHaveValue("151");
    fireEvent.change(screen.getByLabelText("对账单币种"), { target: { value: "CNY" } });
    expect(screen.getByLabelText("手动汇率：1 USD 兑目标币种")).toHaveValue("7.3");
  });

  it("submits and persists the locale decimal separator without losing precision", async () => {
    setActiveLanguage("ru");
    const fetch = mockStatements();
    render(<BillingStatements api={api} side="provider" />);
    fireEvent.change(screen.getByLabelText("Валюта выписки"), { target: { value: "CNY" } });
    fireEvent.change(screen.getByLabelText("Ручной курс: единиц целевой валюты за 1 USD"), { target: { value: "7,250000000001" } });
    fireEvent.submit(screen.getByRole("button", { name: "Предпросмотр акта сверки" }).closest("form")!);
    await screen.findByRole("button", { name: "Экспорт текущего предпросмотра в CSV" });
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toMatchObject({ display_currency: "CNY", display_exchange_rate: "7.250000000001" });
    expect(JSON.parse(window.localStorage.getItem(statementCurrencyStorageKey)!)).toEqual({ currency: "CNY", rates: { CNY: "7.250000000001" } });
  });
});
