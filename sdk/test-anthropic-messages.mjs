#!/usr/bin/env node

import { existsSync, readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
loadDotEnv(resolve(__dirname, ".env"));

const baseURL = normalizeHostURL(
  process.env.TOKENHUB_ANTHROPIC_BASE_URL
    || process.env.TOKENHUB_BASE_URL
    || "http://localhost:8080",
);
const apiKey = process.env.TOKENHUB_API_KEY || "";
const model = process.env.TOKENHUB_ANTHROPIC_MODEL || process.env.TOKENHUB_MODEL || "";

if (!apiKey || apiKey === "thk_your_tokenhub_key" || !model) {
  console.error("Missing TOKENHUB_API_KEY or TOKENHUB_ANTHROPIC_MODEL.");
  console.error("Run with:");
  console.error("  TOKENHUB_API_KEY=thk_xxx TOKENHUB_ANTHROPIC_MODEL=claude-model npm run test:anthropic-messages");
  process.exit(1);
}

console.log("TokenHub Anthropic Messages smoke test");
console.log(`Base URL: ${baseURL}`);
console.log(`Model:    ${model}`);
console.log(`API Key:  ${maskKey(apiKey)}`);
console.log("");

await checkModelVisibility();
await checkTokenCount();
await checkStreamingToolRequest();

async function checkModelVisibility() {
  const response = await fetch(`${baseURL}/v1/models`, {
    headers: { authorization: `Bearer ${apiKey}` },
  });
  const payload = await responseJSON(response, "GET /v1/models");
  const models = Array.isArray(payload.data) ? payload.data : [];
  if (!models.some((item) => item?.id === model)) {
    throw new Error(`Model ${model} is not visible to the TokenHub key`);
  }
  console.log("Model discovery: passed");
}

async function checkTokenCount() {
  const response = await fetch(`${baseURL}/v1/messages/count_tokens`, {
    method: "POST",
    headers: anthropicHeaders(),
    body: JSON.stringify({
      model,
      messages: [{ role: "user", content: "Understand this repository." }],
      tools: [repositoryTool()],
    }),
  });
  const payload = await responseJSON(response, "POST /v1/messages/count_tokens");
  if (!Number.isFinite(payload.input_tokens) || payload.input_tokens <= 0) {
    throw new Error(`Invalid token count response: ${JSON.stringify(payload)}`);
  }
  console.log(`Token counting: passed (${payload.input_tokens} estimated input tokens)`);
}

async function checkStreamingToolRequest() {
  const response = await fetch(`${baseURL}/v1/messages`, {
    method: "POST",
    headers: anthropicHeaders(),
    body: JSON.stringify({
      model,
      max_tokens: 1024,
      stream: true,
      system: "Use the provided repository tool when it helps answer the request.",
      messages: [{
        role: "user",
        content: "Call inspect_repository for the backend and then summarize what should be inspected next.",
      }],
      tools: [repositoryTool()],
      tool_choice: { type: "auto" },
    }),
  });
  if (!response.ok) {
    throw new Error(`POST /v1/messages failed: ${response.status} ${await response.text()}`);
  }
  const stream = await response.text();
  for (const event of ["event: message_start", "event: content_block_start", "event: message_delta", "event: message_stop"]) {
    if (!stream.includes(event)) {
      throw new Error(`Messages stream is missing ${event}:\n${stream}`);
    }
  }
  console.log("Streaming Messages: passed");
}

function repositoryTool() {
  return {
    name: "inspect_repository",
    description: "Inspect one repository area.",
    input_schema: {
      type: "object",
      properties: {
        area: { type: "string" },
      },
      required: ["area"],
    },
  };
}

function anthropicHeaders() {
  return {
    authorization: `Bearer ${apiKey}`,
    "anthropic-version": "2023-06-01",
    "content-type": "application/json",
  };
}

async function responseJSON(response, label) {
  const body = await response.text();
  if (!response.ok) {
    throw new Error(`${label} failed: ${response.status} ${body}`);
  }
  try {
    return JSON.parse(body);
  } catch {
    throw new Error(`${label} returned invalid JSON: ${body}`);
  }
}

function loadDotEnv(path) {
  if (!existsSync(path)) return;
  const lines = readFileSync(path, "utf8").split(/\r?\n/);
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq < 0) continue;
    const key = trimmed.slice(0, eq).trim();
    const raw = trimmed.slice(eq + 1).trim();
    if (!key || process.env[key] !== undefined) continue;
    process.env[key] = stripEnvQuotes(raw);
  }
}

function stripEnvQuotes(value) {
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    return value.slice(1, -1);
  }
  return value;
}

function normalizeHostURL(value) {
  return value.replace(/\/+$/, "").replace(/\/v1$/, "");
}

function maskKey(value) {
  if (value.length <= 10) return "***";
  return `${value.slice(0, 6)}...${value.slice(-4)}`;
}
