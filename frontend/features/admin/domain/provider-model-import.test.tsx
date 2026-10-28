import { describe, expect, it } from "vitest";
import { type Model, type ProviderModel } from "../core/types";
import { emptyData } from "./catalog";
import { providerImportCandidates, providerImportMetadata, providerImportValues, validProviderImportDraft } from "./provider-model-import";

const upstream: ProviderModel = { id: "pm", provider_id: "p", upstream_model: "vendor/model", status: "active", call_supported: true, modality: "chat", family: "vendor", input_price_usd_per_1m: 2, output_price_usd_per_1m: 4 };
const reference = (name: string, metadata = { source: "tokenhub-standard-catalog" }): Model => ({ id: name, name, family: "vendor", modality: "chat", status: "active", metadata });
function fixture() {
  const data = emptyData();
  data.providers = [{ id: "p", name: "Provider", type: "mock", status: "active", healthy: true, priority: 1 }];
  data.providerModels = [upstream];
  return data;
}

describe("Provider model import policy", () => {
  it("allows reference promotion while protecting external, legacy and routed names", () => {
    const data = fixture();
    data.models = [reference(upstream.upstream_model)];
    expect(providerImportCandidates(data, "p")[0].collision).toBe(false);
    data.models[0].metadata = { source: "tokenhub-standard-catalog", directory_role: "external" };
    expect(providerImportCandidates(data, "p")[0].collision).toBe(true);
    data.models[0].metadata = {};
    expect(providerImportCandidates(data, "p")[0].collision).toBe(true);
    data.models = [];
    data.routes = [{ id: "route", model_name: upstream.upstream_model, provider_id: "p", provider_model: "other", priority: 1, weight: 100, status: "disabled" }];
    expect(providerImportCandidates(data, "p")[0].collision).toBe(true);
  });

  it("filters inactive, duplicate and unsupported inventory from inactive providers", () => {
    const data = fixture();
    data.providerModels.push(upstream, { ...upstream, id: "disabled", upstream_model: "disabled", status: "disabled" }, { ...upstream, id: "unsupported", upstream_model: "unsupported", call_supported: false }, { ...upstream, id: "retired", upstream_model: "retired", metadata: { lifecycle_status: "retired" } });
    expect(providerImportCandidates(data, "p").map((item) => item.model.id)).toEqual(["pm"]);
    data.providers[0].status = "disabled";
    expect(providerImportCandidates(data, "p")).toEqual([]);
  });

  it("only allows zero-cost confirmation for unverified zero subscription inventory", () => {
    const data = fixture();
    const free = { ...upstream, input_price_usd_per_1m: 0, output_price_usd_per_1m: 0, metadata: { pricing_status: "unverified", billing_mode: "subscription" } };
    data.providerModels = [free];
    expect(providerImportCandidates(data, "p")[0]).toMatchObject({ costRequired: true, canConfirmZeroCost: true });
    data.providerModels = [{ ...free, metadata: { pricing_status: "unverified" } }];
    expect(providerImportCandidates(data, "p")[0]).toMatchObject({ costRequired: true, canConfirmZeroCost: false });
    data.providerModels = [{ ...free, cache_read_price_usd_per_1m: 1 }];
    expect(providerImportCandidates(data, "p")[0].canConfirmZeroCost).toBe(false);
    data.providerModels = [{ ...free, metadata: { ...free.metadata, pricing_status: "configured" } }];
    expect(providerImportCandidates(data, "p")[0].costRequired).toBe(false);
  });

  it("requires explicit finite nonnegative public prices including free usage", () => {
    for (const input of ["", " ", "1e999", "-1"]) expect(validProviderImportDraft({ input, output: "0", searchUnit: "" })).toBe(false);
    expect(validProviderImportDraft({ input: "0", output: "0", searchUnit: "" })).toBe(true);
  });

  it("preserves functional metadata and exact mapping without inheriting upstream billing or routing policy", () => {
    const model = { ...upstream, display_name: "Friendly Model", context_window: 10000, capabilities: ["tools"], supported_parameters: ["temperature"], cache_read_price_usd_per_1m: 1, pricing_periods: [{ timezone: "UTC", start_time: "00:00", end_time: "08:00", input_price_usd_per_1m: 0.1 }], metadata: { endpoints: "chat/completions,responses", max_output_tokens: "4096", reasoning_effort_supported: "false", reasoning_effort_options: "low,high", source: "catalog", pricing_status: "unverified", billing_mode: "subscription", routing_policy: "private", search_unit_price_usd: "2" } };
    const values = providerImportValues(model, { input: "0", output: "3", searchUnit: "" });
    const metadata = providerImportMetadata(model);
    expect(values).toMatchObject({ name: "vendor/model", family: "vendor", context_window: "10000", input_price_usd_per_1m: "0", output_price_usd_per_1m: "3", capabilities: "tools", supported_parameters: "temperature", pricing_periods: "", cache_write_price_usd_per_1m: "", display_name: "Friendly Model", initial_provider_models: "p|vendor%2Fmodel" });
    expect(metadata).toMatchObject({ source: "provider-model-import", directory_role: "external", pricing_status: "configured", endpoints: "chat/completions,responses", max_output_tokens: "4096", reasoning_effort_supported: "false" });
    expect(metadata).not.toHaveProperty("billing_mode");
    expect(metadata).not.toHaveProperty("routing_policy");
    expect(metadata).not.toHaveProperty("catalog_price_reference");
    expect(values.cache_read_price_usd_per_1m).toBe("");
  });

  it("sets explicit embedding and audio prices without copying upstream native-unit costs", () => {
    const values = providerImportValues({ ...upstream, modality: "embedding", metadata: { search_unit_price_usd: "4" } }, { input: "0", output: "0", searchUnit: "" });
    expect(values.embedding_price_usd_per_1m).toBe("0");
    expect(values.retrieval_pricing_confirmed).toBe("true");
    expect(values.search_unit_price_usd).toBe("");
    const audio = { ...upstream, modality: "audio", metadata: { audio_price_usd_per_second: "5" } };
    const draft = { input: "0", output: "0", searchUnit: "" };
    expect(validProviderImportDraft(draft, "audio")).toBe(false);
    expect(providerImportMetadata(audio, draft)).not.toHaveProperty("audio_price_usd_per_second");
    expect(validProviderImportDraft({ ...draft, audioSecond: "0" }, "audio")).toBe(true);
    expect(providerImportMetadata(audio, { ...draft, audioSecond: "0" }).audio_price_usd_per_second).toBe("0");
  });
});
