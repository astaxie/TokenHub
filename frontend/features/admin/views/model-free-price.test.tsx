import { render, screen } from "@testing-library/react";
import { setActiveLanguage } from "../i18n/runtime";
import { expect, it } from "vitest";
import { emptyData } from "../domain/catalog";
import { ModelDirectoryView } from "./model-directory";
import { modelTokenPriceMetric, formatRetrievalUSD } from "../domain/model-token-price";

it("distinguishes confirmed free retrieval from unknown and legacy prices", () => {
  const data = emptyData();
  data.models = [
    { id: "free", name: "confirmed-free", family: "bge", modality: "embedding", status: "active", input_price_usd_per_1m: 9, embedding_price_usd_per_1m: 0, metadata: { retrieval_pricing_confirmed: "true" } },
    { id: "unknown", name: "unknown-price", family: "bge", modality: "embedding", status: "active" },
  ];
  const noop = () => {};
  render(<ModelDirectoryView api={{ baseURL: "", adminToken: "" }} config={{ view: "models", title: "Models", eyebrow: "Models", description: "", fields: [], columns: [], list: d => d.models }} data={data} loading={false} readOnly onReload={noop} onCreateModel={noop} onOpenProviders={noop} onOpenRoutes={noop} onEditModel={noop} onDeleteModel={noop} />);
  expect(screen.getByRole("row", { name: /confirmed-free/ })).toHaveTextContent("$0.000000/Mt");
  expect(screen.getByRole("row", { name: /unknown-price/ })).toHaveTextContent("$-");
  expect(modelTokenPriceMetric({ ...data.models[0], modality: "rerank", input_price_usd_per_1m: 0 })).toBe("US$0.000000/Mt");
  expect(modelTokenPriceMetric({ ...data.models[0], modality: "chat", input_price_usd_per_1m: 0 })).toBe("$-");
  expect(modelTokenPriceMetric({ ...data.models[0], embedding_price_usd_per_1m: 0.5 })).toBe("US$0.500000/Mt");
});

it("formats retrieval USD according to the active language", () => {
 setActiveLanguage("zh-CN"); expect(formatRetrievalUSD(0.003)).toBe("US$0.003000");
 setActiveLanguage("en"); expect(formatRetrievalUSD(0.003)).toBe("$0.003000");
 setActiveLanguage("ja"); expect(formatRetrievalUSD(0.003)).toBe("$0.003000");
 expect(formatRetrievalUSD(Number.NaN)).toBe("—");
});
