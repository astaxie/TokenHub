#!/usr/bin/env node
import assert from "node:assert/strict";
import { gatewaySmoke } from "./lib/gateway-smoke.mjs";

const gateway = gatewaySmoke("TOKENHUB_RERANK_MODEL");
const texts = ["Certificate renewal instructions", "A recipe for vegetable soup"];
const query = "How do I renew a certificate?";
function verify(result, documents, topN, returnDocuments) {
  assert.equal(result.model, gateway.model);
  assert.equal(result.results.length, topN);
  let previous = Infinity;
  const seen = new Set();
  for (const item of result.results) {
    assert.ok(Number.isInteger(item.index) && item.index >= 0 && item.index < documents.length);
    assert.ok(!seen.has(item.index), "Duplicate result index");
    seen.add(item.index);
    assert.ok(Number.isFinite(item.relevance_score) && item.relevance_score <= previous);
    previous = item.relevance_score;
    const original = documents[item.index];
    assert.equal(item.id, typeof original === "string" ? undefined : original.id);
    if (returnDocuments) assert.equal(item.document?.text, typeof original === "string" ? original : original.text);
    else assert.equal(item.document, undefined);
  }
}
for (const documents of [texts, texts.map((text, index) => ({ id: `doc-${index}`, text })), [texts[0], { id: "doc-1", text: texts[1] }]]) {
  for (const returnDocuments of [false, true]) {
    const result = await gateway.request("/rerank", { model: gateway.model, query, documents, top_n: 1, return_documents: returnDocuments });
    verify(result, documents, 1, returnDocuments);
  }
}
await gateway.request("/rerank", { model: gateway.model, query, documents: texts }, 401, "invalid-smoke-key");
await gateway.request("/rerank", { model: gateway.model, query, documents: texts, top_n: 3 }, 400);
await gateway.request("/rerank", { model: gateway.model, query, documents: [{ id: "", text: "Document" }] }, 400);
console.log("Rerank smoke passed: strings, document IDs, mixed inputs, ordering, top_n, optional documents and validation.");
