#!/usr/bin/env node
import assert from "node:assert/strict";
import { gatewaySmoke, silentWave } from "./lib/gateway-smoke.mjs";

const gateway = gatewaySmoke("TOKENHUB_TRANSCRIPTION_MODEL");
function upload(data = silentWave(), name = "smoke.wav", type = "audio/wav") {
  const form = new FormData();
  form.set("model", gateway.model);
  form.set("response_format", "json");
  form.set("file", new Blob([data], { type }), name);
  return form;
}
const result = await gateway.request("/audio/transcriptions", upload());
assert.equal(typeof result.text, "string", "Transcription must return text");
await gateway.request("/audio/transcriptions", upload(), 401, "invalid-smoke-key");
const missingFile = new FormData();
missingFile.set("model", gateway.model);
await gateway.request("/audio/transcriptions", missingFile, 400);
await gateway.request("/audio/transcriptions", upload("not audio", "smoke.txt", "text/plain"), 415);
console.log("Transcription smoke passed: multipart JSON response, authentication, required file and media validation.");
