import { describe, expect, it } from "vitest";
import { providerUpdatePayload, modelPayload } from "./payloads";
import { availableProviderModelSelectOptions } from "../domain/provider-model-selection";
describe("Rerank configuration and publication", () => {
 it("keeps embedding settings when editing rerank settings", () => {
  const payload=providerUpdatePayload({ type:"openai_compatible", rerank_protocol:"cohere", rerank_path:"/rerank", _existing_options:JSON.stringify({embedding_protocol:"tei"}) });
  expect(payload.options).toMatchObject({embedding_protocol:"tei",rerank_protocol:"cohere",rerank_path:"/rerank"});
 });
 it("retains explicit free search unit prices independently of token prices", () => {
  const payload=modelPayload({name:"public-rerank",modality:"rerank",search_unit_price_usd:"0",retrieval_pricing_confirmed:"true"});
  expect(payload.metadata).toMatchObject({search_unit_price_usd:"0",retrieval_pricing_confirmed:"true"});
 });
 it("excludes uncallable models from new route selection", () => {
  const options=availableProviderModelSelectOptions({providers:[{id:"p",name:"P",status:"active",priority:1}],providerModels:[{provider_id:"p",upstream_model:"unsupported",status:"active",call_supported:false},{provider_id:"p",upstream_model:"supported",status:"active",call_supported:true}]});
  expect(options).toHaveLength(1);expect(options[0].label).toContain("supported");expect(options[0].label).not.toContain("unsupported");
 });
});
