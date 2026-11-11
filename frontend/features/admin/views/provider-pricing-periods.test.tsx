import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ProviderModel } from "../core/types";
import { pricingPeriodDrafts, serializePricingPeriodDrafts } from "../domain/pricing-period-draft";
import { ProviderModelInventory } from "./provider-model-inventory";

const model: ProviderModel = {
  id: "schedule-test", provider_id: "synthetic-provider", upstream_model: "synthetic-chat", modality: "chat", status: "active",
  input_price_usd_per_1m: 2, cache_read_price_usd_per_1m: 0.2, output_price_usd_per_1m: 8,
  metadata: { custom_note: "preserve", pricing_status: "configured" },
  pricing_periods: [{ name: "Off-peak", timezone: "Asia/Shanghai", start_time: "23:00", end_time: "08:00", weekdays: [1, 5], effective_from: "2026-11-01T00:00:00+08:00", effective_until: "2026-12-01T00:00:00+08:00", input_price_usd_per_1m: 1, cache_read_price_usd_per_1m: 0.1, output_price_usd_per_1m: 4, cache_write_5m_price_usd_per_1m: 0, cache_write_1h_price_usd_per_1m: 3 }],
};
afterEach(() => vi.unstubAllGlobals());

async function setup(response = () => new Response("{}", { status: 200 }), storedModel = model) {
  const fetchMock = vi.fn<typeof fetch>(async () => response());
  vi.stubGlobal("fetch", fetchMock);
  const user = userEvent.setup();
  const onSaved = vi.fn();
  render(<ProviderModelInventory api={{ baseURL: "http://localhost:8080", adminToken: "synthetic" }} models={[storedModel]} onSaved={onSaved} />);
  await user.click(screen.getByRole("button", { name: /分时价格/ }));
  return { user, fetchMock, onSaved, editor: within(screen.getByRole("region", { name: "分时成本设置" })) };
}

describe("Provider time-based pricing", () => {
  it("refreshes pristine rows while retaining edited schedules and their input nodes", async () => {
    const api = { baseURL: "http://localhost:8080", adminToken: "synthetic" };
    const other = { ...model, id: "other-model", upstream_model: "other-chat" };
    const view = render(<ProviderModelInventory api={api} models={[model, other]} />);
    const user = userEvent.setup();
    await user.click(screen.getAllByRole("button", { name: /分时价格/ })[0]);
    const name = screen.getByLabelText("时段名称");
    await user.clear(name);
    await user.type(name, "Unsaved schedule");
    const refreshed = { ...other, input_price_usd_per_1m: 9, pricing_periods: [{ ...other.pricing_periods![0], name: "Server update" }] };
    view.rerender(<ProviderModelInventory api={api} models={[{ ...model }, refreshed]} />);
    expect(screen.getByLabelText("时段名称")).toBe(name);
    expect(name).toHaveValue("Unsaved schedule");
    const otherRow = screen.getByRole("row", { name: /other-chat/ });
    expect(within(otherRow).getAllByRole("spinbutton")[0]).toHaveValue(9);
    await user.click(within(otherRow).getByRole("button", { name: /分时价格/ }));
    expect(screen.getByLabelText("时段名称")).toHaveValue("Server update");
  });

  it("preserves edits made during a save and marks only the submitted version clean", async () => {
    const api = { baseURL: "http://localhost:8080", adminToken: "synthetic" };
    let stored = model;
    let finishSave = () => {};
    const fetchMock = vi.fn<typeof fetch>(async (_input, init) => {
      const body = JSON.parse(String(init?.body));
      expect(body).not.toHaveProperty("dirty");
      if (fetchMock.mock.calls.length === 1) await new Promise<void>(resolve => { finishSave = resolve; });
      stored = { ...stored, ...body };
      return new Response("{}", { status: 200 });
    });
    vi.stubGlobal("fetch", fetchMock);
    const onSaved = vi.fn(() => { view.rerender(<ProviderModelInventory api={api} models={[stored]} onSaved={onSaved} />); });
    const view = render(<ProviderModelInventory api={api} models={[stored]} onSaved={onSaved} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /分时价格/ }));
    const name = screen.getByLabelText("时段名称");
    await user.clear(name);
    await user.type(name, "Submitted schedule");
    const input = within(screen.getByRole("table")).getAllByRole("spinbutton")[0];
    await user.clear(input);
    await user.type(input, "3");
    await user.click(screen.getByRole("button", { name: "保存成本与时段" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    await user.clear(input);
    await user.type(input, "5");
    await act(async () => { finishSave(); });
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(stored.input_price_usd_per_1m).toBe(3);
    expect(input).toHaveValue(5);
    expect(screen.getByLabelText("时段名称")).toBe(name);
    expect(name).toHaveValue("Submitted schedule");
    await user.click(screen.getByRole("button", { name: "保存成本与时段" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(2));
    expect(stored.input_price_usd_per_1m).toBe(5);
    expect(stored.pricing_periods![0].name).toBe("Submitted schedule");
    view.rerender(<ProviderModelInventory api={api} models={[{ ...stored, input_price_usd_per_1m: 7 }]} onSaved={onSaved} />);
    expect(input).toHaveValue(7);
  });

  it("round-trips supported padded schedule fields without changing price overrides", () => {
    const period = model.pricing_periods![0];
    const padded = { ...period, timezone: " Asia/Shanghai ", start_time: " 23:00 ", end_time: "\t08:00\n", effective_from: ` ${period.effective_from} `, effective_until: `\t${period.effective_until}\n` };
    const drafts = pricingPeriodDrafts([padded]);
    expect(drafts[0]).toMatchObject({ timezone: period.timezone, start_time: period.start_time, end_time: period.end_time, effective_from: period.effective_from, effective_until: period.effective_until });
    expect(serializePricingPeriodDrafts(drafts)).toEqual([period]);
    expect(serializePricingPeriodDrafts([{ ...drafts[0], effective_from: ` ${period.effective_from} `, effective_until: " \t " }])).toEqual([{ ...period, effective_until: undefined }]);
  });

  it("shows saved padded clocks and saves a cleared effective-date bound", async () => {
    const period = model.pricing_periods![0];
    const padded = { ...period, timezone: " Asia/Shanghai ", start_time: " 23:00 ", end_time: " 08:00 ", effective_from: ` ${period.effective_from} `, effective_until: ` ${period.effective_until} ` };
    const { user, editor, fetchMock, onSaved } = await setup(undefined, { ...model, pricing_periods: [padded] });
    expect(editor.getByLabelText("开始时间")).toHaveValue("23:00");
    expect(editor.getByLabelText("结束时间")).toHaveValue("08:00");
    expect(editor.getByLabelText("时区（IANA）")).toHaveValue("Asia/Shanghai");
    await user.click(editor.getByText("星期、生效日期与缓存写入"));
    const effectiveFrom = editor.getByLabelText("生效开始（RFC 3339，可选）");
    await user.clear(effectiveFrom);
    await user.type(effectiveFrom, " ");
    await user.click(editor.getByRole("button", { name: "保存成本与时段" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    const payload = JSON.parse(String(fetchMock.mock.calls[0][1]?.body));
    expect(payload.pricing_periods).toEqual([{ ...period, effective_from: undefined }]);
    expect(payload.pricing_periods[0]).not.toHaveProperty("effective_from");
  });

  it.each(["existing", "new"])("saves %s Local schedules without substituting the browser timezone", async source => {
    const period = { ...model.pricing_periods![0], timezone: "Local" };
    const { user, editor, fetchMock, onSaved } = await setup(undefined, { ...model, pricing_periods: source === "existing" ? [period] : [] });
    if (source === "new") {
      await user.click(editor.getByRole("button", { name: "添加时段" }));
      await user.clear(editor.getByLabelText("时区（IANA）"));
      await user.type(editor.getByLabelText("时区（IANA）"), " Local ");
      await user.click(editor.getByText("星期、生效日期与缓存写入"));
      await user.type(editor.getByLabelText("生效开始（RFC 3339，可选）"), period.effective_from!);
      await user.type(editor.getByLabelText("生效结束（RFC 3339，可选）"), period.effective_until!);
    }
    await user.click(source === "existing" ? screen.getByRole("button", { name: "保存成本" }) : editor.getByRole("button", { name: "保存成本与时段" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    expect(fetchMock).toHaveBeenCalledOnce();
    const payload = JSON.parse(String(fetchMock.mock.calls[0][1]?.body));
    expect(payload.pricing_periods).toEqual([source === "existing" ? period : {
      timezone: "Local", start_time: "00:00", end_time: "08:00", effective_from: period.effective_from, effective_until: period.effective_until,
    }]);
  });

  it("persists free overrides and omitted inheritance while preserving advanced fields", async () => {
    const { user, editor, fetchMock, onSaved } = await setup();
    await user.clear(editor.getByLabelText("输入（缓存命中）USD/1M"));
    await user.type(editor.getByLabelText("输入（缓存命中）USD/1M"), "0");
    await user.clear(editor.getByLabelText("输出 USD/1M"));
    await user.click(editor.getByRole("button", { name: "保存成本与时段" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalledOnce());
    const payload = JSON.parse(String(fetchMock.mock.calls[0][1]?.body));
    expect(payload).toMatchObject({ input_price_usd_per_1m: 2, output_price_usd_per_1m: 8, metadata: model.metadata });
    expect(payload.pricing_periods).toEqual([{ ...model.pricing_periods![0], cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: undefined }]);
  });

  it("removes all periods without silently restoring the saved schedule", async () => {
    const { user, editor, fetchMock } = await setup();
    await user.click(editor.getByRole("button", { name: "删除时段" }));
    expect(editor.getByText("尚未配置时段，全天使用基础价格。")).toBeVisible();
    await user.click(editor.getByRole("button", { name: "保存成本与时段" }));
    await waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body)).pricing_periods).toEqual([]);
  });

  it("rejects invalid time zones before making a request", async () => {
    const { user, editor, fetchMock } = await setup();
    await user.clear(editor.getByLabelText("时区（IANA）"));
    await user.type(editor.getByLabelText("时区（IANA）"), "Mars/Olympus");
    await user.click(editor.getByRole("button", { name: "保存成本与时段" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("分时价格无效");
    expect(fetchMock).not.toHaveBeenCalled();
    expect(editor.getByLabelText("时区（IANA）")).toHaveValue("Mars/Olympus");
  });

  it("preserves drafts when the server rejects overlapping periods", async () => {
    const { user, editor, fetchMock } = await setup(() => new Response(JSON.stringify({ error: { message: "Synthetic overlapping period" } }), { status: 400 }));
    await user.click(editor.getByRole("button", { name: "添加时段" }));
    await user.click(editor.getByRole("button", { name: "保存成本与时段" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Synthetic overlapping period");
    expect(editor.getAllByLabelText("时区（IANA）")).toHaveLength(2);
    expect(JSON.parse(String(fetchMock.mock.calls[0][1]?.body)).pricing_periods).toHaveLength(2);
  });

  it("rejects non-finite prices without converting them to inherited rates", () => {
    const drafts = pricingPeriodDrafts(model.pricing_periods);
    drafts[0].input_price_usd_per_1m = "1e999";
    expect(() => serializePricingPeriodDrafts(drafts)).toThrow("pricing_period_invalid_rate");
    expect(() => serializePricingPeriodDrafts(Array.from({ length: 65 }, () => drafts[0]))).toThrow("pricing_period_limit");
  });

  it("rejects native invalid values and never serializes editor state", () => {
    const drafts = pricingPeriodDrafts(model.pricing_periods);
    drafts[0].input_price_usd_per_1m = "";
    drafts[0].invalidFieldKeys = ["input_price_usd_per_1m"];
    expect(() => serializePricingPeriodDrafts(drafts)).toThrow("pricing_period_invalid_input");
    drafts[0].invalidFieldKeys = [];
    expect(serializePricingPeriodDrafts(drafts)[0]).not.toHaveProperty("invalidFieldKeys");
    expect(serializePricingPeriodDrafts(drafts)[0]).not.toHaveProperty("draftID");
    expect(serializePricingPeriodDrafts(drafts)[0]).not.toHaveProperty("input_price_usd_per_1m");
    drafts[0].input_price_usd_per_1m = "0.123456789";
    expect(serializePricingPeriodDrafts(drafts)[0].input_price_usd_per_1m).toBe(0.123456789);
  });

  it("rejects partial clocks until both bounds are deliberately cleared", () => {
    const drafts = pricingPeriodDrafts(model.pricing_periods);
    drafts[0].start_time = "";
    drafts[0].end_time = "";
    drafts[0].invalidFieldKeys = ["start_time", "end_time"];
    expect(() => serializePricingPeriodDrafts(drafts)).toThrow("pricing_period_invalid_input");
    drafts[0].invalidFieldKeys = ["end_time"];
    expect(() => serializePricingPeriodDrafts(drafts)).toThrow("pricing_period_invalid_input");
    drafts[0].invalidFieldKeys = [];
    const period = serializePricingPeriodDrafts(drafts)[0];
    expect(period).not.toHaveProperty("start_time");
    expect(period).not.toHaveProperty("end_time");
    expect(period).not.toHaveProperty("invalidFieldKeys");
    expect(period).not.toHaveProperty("draftID");
    expect(period.input_price_usd_per_1m).toBe(1);
  });
});
