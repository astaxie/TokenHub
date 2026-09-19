#!/usr/bin/env node

import assert from "node:assert/strict";
import { TypeSafeClient } from "@typesafe-ai/sdk";
import { loadDotEnv } from "./lib/env.mjs";
import { fileURLToPath } from "node:url";

loadDotEnv(fileURLToPath(new URL(".env", import.meta.url)));
const apiKey = process.env.TOKENHUB_API_KEY;
if (!apiKey) throw new Error("TOKENHUB_API_KEY is required.");
const baseURL = (process.env.TOKENHUB_BASE_URL || "http://localhost:8080").replace(/\/+$/, "").replace(/\/v1$/, "");
const client = new TypeSafeClient({ apiKey, baseURL, retry: { maxRetries: 0 }, timeout: 30_000 });
const { data: result, response, requestId } = await client.systemOne({
  model: process.env.TOKENHUB_MODEL || "jev-1.13.0",
  state: "Please refund this order.",
  questions: {
    intent: { type: "choice", instructions: "Classify the customer intent.", criteria: { refund: "Return funds.", other: "Another request." } },
    urgent: { type: "noul", instructions: "Is immediate attention required?" },
    severity: { type: "score", instructions: "Assess urgency.", criteria: ["low", "high"] },
  },
}).withResponse();
assert.ok(requestId, "The upstream request ID must be available through withResponse().");
assert.equal(requestId, response.headers.get("x-typesafe-request-id"));
assert.ok(response.headers.get("x-request-id"), "The TokenHub request ID must remain available.");
assert.notEqual(requestId, response.headers.get("x-request-id"));
assert.equal(typeof result.model, "string");
assert.deepEqual(Object.keys(result.answers).sort(), ["intent", "severity", "urgent"]);
assert.equal(result.answers.intent.type, "choice");
assert.ok(["refund", "other"].includes(result.answers.intent.choice));
assert.equal(result.answers.urgent.type, "noul");
assert.ok(result.answers.urgent.noul >= 0 && result.answers.urgent.noul <= 1);
assert.equal(result.answers.severity.type, "score");
assert.ok(result.answers.severity.score >= 0 && result.answers.severity.score <= 1);
assert.deepEqual(result.answers.severity.legend, { 0: "low", 1: "high" });
assert.ok(Number.isSafeInteger(result.usage.input_tokens) && result.usage.input_tokens >= 0);
assert.ok(Number.isSafeInteger(result.usage.output_tokens) && result.usage.output_tokens >= 0);
const optionalResult = await client.systemOne({
  model: process.env.TOKENHUB_MODEL || "jev-1.13.0",
  state: null,
  questions: {
    intent: { type: "choice", criteria: { refund: "Return funds.", other: "Another request." } },
    urgent: { type: "noul", criteria: null },
    severity: { type: "score", criteria: ["low", "high"] },
  },
});
assert.deepEqual(Object.keys(optionalResult.answers).sort(), ["intent", "severity", "urgent"]);
assert.equal(optionalResult.answers.intent.type, "choice");
assert.equal(optionalResult.answers.urgent.type, "noul");
assert.equal(optionalResult.answers.severity.type, "score");
assert.deepEqual(optionalResult.answers.severity.legend, { 0: "low", 1: "high" });
console.log("System One SDK smoke passed: choice, noul, score, native usage, and optional/null inputs.");
