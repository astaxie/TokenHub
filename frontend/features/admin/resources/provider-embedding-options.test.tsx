import { describe, expect, it } from "vitest";
import { embeddingFormValues, embeddingOptions } from "../domain/provider-embedding-options";
import { providerUpdatePayload } from "./payloads";
describe("Embedding provider configuration", () => {
 it("preserves unrelated options and explicitly clears overrides", () => {
  const fields = embeddingFormValues({ embedding_protocol: "tei", embedding_path: "/embed" });
  const result = providerUpdatePayload({ ...fields, type: "openai_compatible", embedding_path: "", _existing_options: JSON.stringify({ unrelated: "keep" }) });
  expect(result.options).toMatchObject({ unrelated: "keep", embedding_protocol: "tei", embedding_path: "" });
 });
 it("does not overwrite overrides when a different form omits embedding fields", () => {
  expect(embeddingOptions({ name: "provider" })).toEqual({});
 });
});
