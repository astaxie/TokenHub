#!/usr/bin/env node

import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { createOpenAICompatible } from "@ai-sdk/openai-compatible";
import { generateText } from "ai";

import { loadDotEnv, maskKey } from "./lib/env.mjs";

const __dirname = dirname(fileURLToPath(import.meta.url));
loadDotEnv(resolve(__dirname, ".env"));

const baseURL = normalizeBaseURL(process.env.TOKENHUB_BASE_URL || "http://localhost:8080/v1");
const apiKey = process.env.TOKENHUB_API_KEY || "";
const model = process.env.TOKENHUB_MODEL || "deepseek-chat";
const prompt = process.env.TOKENHUB_PROMPT || "请用三句话说明 TokenHub 网关链路已经打通。";

if (!apiKey || apiKey === "thk_your_tokenhub_key") {
  console.error("Missing TOKENHUB_API_KEY.");
  console.error("Create an internal API Key in TokenHub, then run:");
  console.error("  TOKENHUB_API_KEY=thk_xxx npm run test:deepseek");
  console.error("");
  console.error("Optional env vars:");
  console.error("  TOKENHUB_BASE_URL=http://localhost:8080/v1");
  console.error("  TOKENHUB_MODEL=DeepSeek-V4-Flash");
  process.exit(1);
}

const tokenhub = createOpenAICompatible({
  name: "tokenhub",
  baseURL,
  apiKey,
  includeUsage: true,
});

console.log("TokenHub AI SDK smoke test");
console.log(`Base URL: ${baseURL}`);
console.log(`Model:    ${model}`);
console.log(`API Key:  ${maskKey(apiKey)}`);
console.log("");

await checkModelVisibility({ baseURL, apiKey, model });

try {
  const startedAt = Date.now();
  const result = await generateText({
    model: tokenhub(model),
    system: "你是 TokenHub 网关链路测试助手。回答要简短，并明确说明请求已经经过 TokenHub。",
    prompt,
    maxRetries: 0,
  });

  console.log("Response:");
  console.log(result.text.trim());
  console.log("");
  console.log("Metadata:");
  console.log(JSON.stringify({
    finishReason: result.finishReason,
    usage: result.usage,
    elapsedMs: Date.now() - startedAt,
  }, null, 2));
} catch (error) {
  console.error("generateText failed.");
  console.error(formatAIError(error));
  process.exit(1);
}

async function checkModelVisibility({ baseURL, apiKey, model }) {
  const modelsURL = `${baseURL.replace(/\/$/, "")}/models`;
  const resp = await fetch(modelsURL, {
    headers: {
      authorization: `Bearer ${apiKey}`,
    },
  });

  if (!resp.ok) {
    const body = await resp.text();
    throw new Error(`GET /models failed: ${resp.status} ${body}`);
  }

  const payload = await resp.json();
  const modelIDs = Array.isArray(payload.data)
    ? payload.data.map((item) => item?.id).filter(Boolean)
    : [];

  if (modelIDs.length === 0) {
    console.warn("Warning: /models returned no visible models for this API key.");
    return;
  }

  if (!modelIDs.includes(model)) {
    console.warn(`Warning: model "${model}" was not listed by /models for this API key.`);
    console.warn(`Visible models: ${modelIDs.slice(0, 12).join(", ")}${modelIDs.length > 12 ? ", ..." : ""}`);
    console.warn("The generateText call will still run, but TokenHub may reject it if the key or route is not configured.");
    console.warn("");
    return;
  }

  console.log(`Model check: "${model}" is visible via /models.`);
  console.log("");
}

function normalizeBaseURL(value) {
  const trimmed = value.replace(/\/+$/, "");
  return trimmed.endsWith("/v1") ? trimmed : `${trimmed}/v1`;
}

function formatAIError(error) {
  const parts = [];
  if (error?.name) parts.push(`name: ${error.name}`);
  if (error?.message) parts.push(`message: ${error.message}`);
  if (error?.statusCode) parts.push(`statusCode: ${error.statusCode}`);
  if (error?.url) parts.push(`url: ${error.url}`);
  const responseBody = error?.responseBody || error?.lastError?.responseBody;
  if (responseBody) parts.push(`responseBody: ${responseBody}`);
  if (error?.lastError && error.lastError !== error) {
    parts.push("lastError:");
    parts.push(formatAIError(error.lastError));
  }
  return parts.join("\n") || String(error);
}
