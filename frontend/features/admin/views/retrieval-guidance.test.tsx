import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { ProviderEmbeddingFields } from "./provider-embedding-fields";
import { ProviderRerankFields } from "./provider-rerank-fields";
import { RequestErrorSummary, requestErrorInfo } from "./request-error-summary";
import { retrievalSettings, preserveRetrievalCatalog, validEmbeddingSpaces } from "../domain/retrieval-settings";
import { providerPayload } from "../resources/payloads";
import { setActiveLanguage } from "../i18n/runtime";

function SettingsForm({ rerank = false }: { rerank?: boolean }) {
  const [values, setValues] = useState<Record<string, string>>({ type: "openai-compatible", catalog_id: "custom", base_url: "http://model-host:8000/v1" });
  const onUpdate = (key: string, value: string) => setValues(current => ({ ...current, [key]: value }));
  return <form>{rerank ? <ProviderRerankFields values={values} onUpdate={onUpdate} /> : <ProviderEmbeddingFields values={values} onUpdate={onUpdate} />}</form>;
}

describe("retrieval configuration guidance", () => {
  it("previews default and native embedding paths and flags full URLs", async () => {
    setActiveLanguage("zh-CN");
    render(<SettingsForm />);
    await userEvent.click(screen.getByText("文本 Embedding 配置", { exact: true }));
    expect(screen.getByText("http://model-host:8000/v1/embeddings", { exact: true, selector: ".retrieval-endpoint-preview code" })).toBeVisible();
    const path = screen.getByLabelText("Embedding 接口路径");
    expect(path).toHaveValue("");
    fireEvent.change(screen.getByLabelText("Embedding 协议"), { target: { value: "tei" } });
    expect(path).toHaveAttribute("placeholder", "/embed");
    expect(screen.getByText("http://model-host:8000/v1/embed", { exact: true, selector: ".retrieval-endpoint-preview code" })).toBeVisible();
    fireEvent.change(path, { target: { value: "https://upstream.example/embeddings" } });
    expect(path).toHaveAttribute("aria-invalid", "true");
    expect((path as HTMLInputElement).checkValidity()).toBe(false);
    expect(screen.queryByText("http://model-host:8000/v1/embed", { exact: true, selector: ".retrieval-endpoint-preview code" })).toBeNull();
    fireEvent.change(path, { target: { value: "/custom/embed" } });
    expect((path as HTMLInputElement).checkValidity()).toBe(true);
    expect(screen.getByText("http://model-host:8000/v1/custom/embed", { exact: true, selector: ".retrieval-endpoint-preview code" })).toBeVisible();
  });
  it("explains real custom auto rerank and updates native defaults without writing them", async () => {
    setActiveLanguage("zh-CN");
    render(<SettingsForm rerank />);
    await userEvent.click(screen.getByText("文本重排配置", { exact: true }));
    expect(screen.getByText("jina", { selector: "code" })).toBeVisible();
    expect(screen.getByText("http://model-host:8000/v1/rerank", { exact: true, selector: ".retrieval-endpoint-preview code" })).toBeVisible();
    fireEvent.change(screen.getByLabelText("重排协议"), { target: { value: "qwen" } });
    expect(screen.getByLabelText("重排接口路径")).toHaveAttribute("placeholder", "/reranks");
    expect(screen.getByLabelText("重排接口路径")).toHaveValue("");
    await userEvent.click(screen.getByText("查看配置示例", { exact: true }));
    expect(screen.getByText("上游若直接提供根路径接口，Base URL 不加 /v1；不要在两处重复填写 /v1。")).toBeVisible();
  });
  it("does not promise endpoints for native adapters or unknown catalogs", () => {
    const values = { type: "openai-compatible", catalog_id: "unrecognized", base_url: "https://model.example/v1" };
    expect(retrievalSettings(values, "rerank").endpoint).toBe("");
    expect(retrievalSettings({ ...values, rerank_protocol: "cohere" }, "rerank").endpoint).toBe("https://model.example/v1/rerank");
    expect(retrievalSettings({ ...values, type: "gemini" }, "embedding").managed).toBe(true);
    expect(retrievalSettings({ ...values, base_url: "https://user:secret@model.example/v1" }, "embedding").endpoint).toBe("");
  });
});

it("shows saved error messages and proven pre-upstream failures without interpreting HTML", () => {
  setActiveLanguage("zh-CN");
  render(<RequestErrorSummary code="provider_capability_not_supported" responseBody={JSON.stringify({ error: { message: "<b>inventory missing</b>", details: { stage: "route_selection", upstream_attempted: false } } })} />);
  expect(screen.getByRole("alert")).toHaveTextContent("尚未发送到上游");
  expect(screen.getByRole("alert")).toHaveTextContent("<b>inventory missing</b>");
  expect(screen.getByRole("alert").querySelector("b")).toBeNull();
  expect(requestErrorInfo('{"error":')).toBeNull();
  expect(requestErrorInfo('{"error":{"message":"upstream failed"}}')?.beforeUpstream).toBe(false);
  expect(requestErrorInfo('{"error":{"details":{"stage":"route_selection","upstream_attempted":"false"}}}')?.beforeUpstream).toBe(false);
});

it("preserves runtime catalogs without replacing the discovery template", () => {
  const provider = { type: "openai-compatible", options: { catalog_id: "cohere", secret_option: "do-not-copy" } };
  const payload = { type: "openai-compatible", catalog_id: "custom", options: { embedding_protocol: "", rerank_protocol: "" } };
  const saved = preserveRetrievalCatalog(payload, provider);
  expect(saved).toEqual({ ...payload, preserve_catalog: true });
  expect(saved.options).not.toHaveProperty("secret_option");
  expect(preserveRetrievalCatalog({ ...payload, type: "anthropic" }, provider)).not.toHaveProperty("preserve_catalog");
  expect(retrievalSettings({ type: provider.type, catalog_id: provider.options.catalog_id, base_url: "https://model.example/v2" }, "embedding").endpoint).toBe("https://model.example/v2/embed");
  expect(preserveRetrievalCatalog(payload, { ...provider, options: { catalog_id: "removed-vendor" } })).toMatchObject({ preserve_catalog: true, catalog_id: "custom" });
});

it("rejects invalid paths and space mappings even when the advanced tab is unmounted", () => {
  expect(() => providerPayload({ embedding_path: "https://model.example/embeddings" })).toThrow();
  expect(() => providerPayload({ embedding_path: "/../embeddings" })).toThrow();
  expect(() => providerPayload({ rerank_path: "//model.example/rerank" })).toThrow();
  expect(() => providerPayload({ embedding_spaces: "/embeddings" })).toThrow();
  expect(() => providerPayload({ embedding_spaces: '{"model":42}' })).toThrow();
  expect(validEmbeddingSpaces("{}")).toBe(true);
  expect(validEmbeddingSpaces("")).toBe(true);
  expect(() => providerPayload({ embedding_path: " /embeddings ", embedding_spaces: '{"model":"space"}' })).not.toThrow();
});
