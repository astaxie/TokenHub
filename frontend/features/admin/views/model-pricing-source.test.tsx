import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { Model, ProviderModel } from "../core/types";
import { emptyData } from "../domain/catalog";
import { eligiblePricingSources } from "../domain/model-pricing-source";
import { modelToForm } from "../domain/formatting";
import { modelPriceSummary } from "../domain/entities";
import { modelPayload } from "../resources/payloads";
import { modelConfig } from "../resources/provider-model-config";
import { EditModal } from "./settings-table";
import { ModelDirectoryDetail } from "./model-directory-detail";
import { ModelDirectoryPrice } from "./model-directory-price";
import { PlaygroundPanel } from "./playground";
import { modelTokenPriceMetric } from "../domain/model-token-price";

const source: ProviderModel = { id: "source", provider_id: "provider", upstream_model: "private-upstream", status: "active", modality: "chat", input_price_usd_per_1m: 2, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 4, metadata: { pricing_status: "configured" }, pricing_periods: [{ name: "Night", timezone: "Asia/Shanghai", start_time: "23:00", end_time: "08:00", input_price_usd_per_1m: 1, cache_read_price_usd_per_1m: 0, output_price_usd_per_1m: 2, weekdays: [1, 2, 3] }] };
const model: Model = { id: "model", name: "public-chat", category: "other", family: "test", modality: "chat", status: "active", input_price_usd_per_1m: 99, output_price_usd_per_1m: 198, cache_read_price_usd_per_1m: 9, metadata: { directory_role: "external", custom_note: "preserve" } };

function fixture() {
  const data = emptyData();
  data.providers = [{ id: "provider", name: "Private Provider", type: "mock", priority: 1, status: "active", healthy: true }];
  data.providerModels = [source];
  return data;
}

describe("Model pricing source", () => {
  it("lists only active confirmed chat models and accepts confirmed zero costs", () => {
    const data = fixture();
    data.providers.push({ ...data.providers[0], id: "inactive-provider", status: "disabled" });
    data.providerModels.push(
      { ...source, id: "free", input_price_usd_per_1m: 0, output_price_usd_per_1m: 0 },
      { ...source, id: "unconfirmed", metadata: { pricing_status: "unverified" } },
      { ...source, id: "disabled", status: "disabled" },
      { ...source, id: "inactive-owner", provider_id: "inactive-provider" },
      { ...source, id: "embedding", modality: "embedding" },
    );
    expect(eligiblePricingSources(data).map(item => item.id)).toEqual(["source", "free"]);
  });

  it("retains custom prices and metadata when linking and switching back", async () => {
    const data = fixture();
    const onSave = vi.fn();
    render(<EditModal state={{ config: modelConfig(), item: model }} data={data} api={{ baseURL: "", adminToken: "" }} loading={false} onClose={vi.fn()} onSave={onSave} />);
    const user = userEvent.setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "对外定价方式" }), "provider");
    expect(screen.queryByLabelText(/^对外输入价/)).not.toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText(/^定价来源模型/), source.id);
    const preview = screen.getByLabelText("来源价格预览");
    expect(preview).toHaveTextContent("23:00 – 08:00");
    expect(preview).toHaveTextContent("Asia/Shanghai");
    expect(preview).toHaveTextContent("Night");
    await user.click(screen.getByRole("button", { name: "保存" }));
    const linked = modelPayload(onSave.mock.calls[0][0], model.metadata);
    expect(linked).toMatchObject({ input_price_usd_per_1m: 99, output_price_usd_per_1m: 198, metadata: { pricing_mode: "provider", pricing_provider_model_id: "source", custom_note: "preserve" } });
    await user.selectOptions(screen.getByRole("combobox", { name: "对外定价方式" }), "custom");
    expect(screen.getByLabelText(/^对外输入价/)).toHaveValue(99);
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(modelPayload(onSave.mock.calls[1][0], model.metadata)).toMatchObject({ input_price_usd_per_1m: 99, metadata: { pricing_mode: "custom" } });
  });

  it("blocks an unavailable saved source but allows returning to custom pricing", async () => {
    const linked = { ...model, metadata: { ...model.metadata, pricing_mode: "provider", pricing_provider_model_id: "missing" } };
    const onSave = vi.fn();
    render(<EditModal state={{ config: modelConfig(), item: linked }} data={fixture()} api={{ baseURL: "", adminToken: "" }} loading={false} onClose={vi.fn()} onSave={onSave} />);
    const user = userEvent.setup();
    expect(screen.getByRole("alert")).toHaveTextContent("原定价来源已不可用");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(onSave).not.toHaveBeenCalled();
    await user.selectOptions(screen.getByRole("combobox", { name: "对外定价方式" }), "custom");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(onSave).toHaveBeenCalledOnce();
    expect(modelToForm(linked)).toMatchObject({ pricing_mode: "provider", pricing_provider_model_id: "missing", input_price_usd_per_1m: "99" });
  });

  it("creates a linked model through the wizard while retaining template prices", async () => {
    const data = fixture();
    data.models = [{ ...model, metadata: { source: "tokenhub-standard-catalog" } }];
    const onSave = vi.fn();
    render(<EditModal state={{ config: modelConfig() }} data={data} api={{ baseURL: "", adminToken: "" }} loading={false} onClose={vi.fn()} onSave={onSave} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /public-chat/ }));
    await user.click(screen.getByRole("button", { name: "下一步：选择 Provider 模型" }));
    await user.click(screen.getByRole("checkbox", { name: "Private Provider / private-upstream" }));
    await user.selectOptions(screen.getByRole("combobox", { name: "对外定价方式" }), "provider");
    await user.selectOptions(screen.getByLabelText(/^定价来源模型/), source.id);
    expect(screen.getByText("所选来源决定对外价格；各线路的真实成本仍按实际供应商核算。")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "创建对外模型" }));
    expect(onSave).toHaveBeenCalledOnce();
    expect(modelPayload(onSave.mock.calls[0][0])).toMatchObject({ input_price_usd_per_1m: 99, metadata: { pricing_mode: "provider", pricing_provider_model_id: source.id }, routes: [expect.objectContaining({ provider_id: source.provider_id, provider_model: source.upstream_model })] });
  });

  it("restores custom fields when changing a linked draft to a non-chat modality", async () => {
    const linked = { ...model, metadata: { pricing_mode: "provider", pricing_provider_model_id: source.id } };
    const onSave = vi.fn();
    render(<EditModal state={{ config: modelConfig(), item: linked }} data={fixture()} api={{ baseURL: "", adminToken: "" }} loading={false} onClose={vi.fn()} onSave={onSave} />);
    const user = userEvent.setup();
    await user.selectOptions(screen.getByRole("combobox", { name: "能力" }), "embedding");
    expect(screen.queryByRole("combobox", { name: "对外定价方式" })).not.toBeInTheDocument();
    expect(screen.getByLabelText(/^对外输入价/)).toHaveValue(99);
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(modelPayload(onSave.mock.calls[0][0], linked.metadata)).toMatchObject({ modality: "embedding", metadata: { pricing_mode: "custom" } });
  });

  it("never presents stored custom rates as linked effective prices", () => {
    const linked = { ...model, metadata: { pricing_mode: "provider", pricing_provider_model_id: "source" } };
    const { container } = render(<ModelDirectoryPrice model={linked} />);
    expect(container).toHaveTextContent("跟随上游定价");
    expect(container).not.toHaveTextContent("99");
    expect(modelPriceSummary(linked)).toBe("跟随上游定价");
    expect(modelTokenPriceMetric(linked)).toBe("跟随上游定价");
  });

  it("hides source identities and procurement rates in read-only details", () => {
    const linked = { ...model, metadata: { pricing_mode: "provider", pricing_provider_model_id: "source" } };
    render(<ModelDirectoryDetail model={linked} data={fixture()} readOnly onClose={vi.fn()} />);
    const detail = screen.getByRole("dialog", { name: "模型详情" });
    expect(detail).toHaveTextContent("跟随上游定价");
    expect(detail).not.toHaveTextContent("Private Provider");
    expect(detail).not.toHaveTextContent("private-upstream");
    expect(within(detail).queryByLabelText("来源价格预览")).not.toBeInTheDocument();
    expect(detail).not.toHaveTextContent("198");
  });

  it("reports an unavailable scoped price without displaying a stale numeric amount", () => {
    const linked = { ...model, pricing_available: false, metadata: { pricing_mode: "provider" } };
    const { container } = render(<ModelDirectoryPrice model={linked} />);
    expect(container).toHaveTextContent("来源价格暂不可用");
    expect(container).not.toHaveTextContent("99");
    expect(modelPriceSummary(linked)).toBe("来源价格暂不可用");
  });

  it.each([true, false])("shows linked pricing mode in the administrator playground when availability is %s", available => {
    const data = fixture();
    data.models = [{ ...model, pricing_available: available, metadata: { directory_role: "external", pricing_mode: "provider", pricing_provider_model_id: source.id } }];
    data.routes = [{ id: "route", model_name: model.name, provider_id: source.provider_id, provider_model: source.upstream_model, status: "active", priority: 1, weight: 100 }];
    const { container } = render(<PlaygroundPanel data={data} api={{ baseURL: "", adminToken: "" }} canViewRoutes />);
    const priceBar = container.querySelector(".playground-model-bar");
    expect(priceBar).toHaveTextContent(available ? "跟随上游定价" : "来源价格暂不可用");
    expect(priceBar).not.toHaveTextContent("99");
    expect(priceBar).not.toHaveTextContent("198");
  });
});
