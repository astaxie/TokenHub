import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { loadDotEnv } from "./env.mjs";

loadDotEnv(fileURLToPath(new URL("../.env", import.meta.url)));

export function gatewaySmoke(modelVariable) {
  const apiKey = process.env.TOKENHUB_API_KEY;
  const model = process.env[modelVariable];
  assert.ok(apiKey, "TOKENHUB_API_KEY is required.");
  assert.ok(model, `${modelVariable} is required.`);
  const base = (process.env.TOKENHUB_BASE_URL || "http://localhost:8080/v1").replace(/\/+$/, "").replace(/\/v1$/, "");
  return {
    model,
    async request(path, body, expectedStatus = 200, key = apiKey) {
      const multipart = body instanceof FormData;
      const response = await fetch(`${base}/v1${path}`, {
        method: "POST",
        headers: { authorization: `Bearer ${key}`, ...(multipart ? {} : { "content-type": "application/json" }) },
        body: multipart ? body : JSON.stringify(body),
        signal: AbortSignal.timeout(60_000),
      });
      // Avoid printing provider errors, transcripts, queries or documents on failure.
      assert.equal(response.status, expectedStatus, `${path}: unexpected HTTP status`);
      const result = await response.json();
      if (expectedStatus === 200) assert.ok(response.headers.get("x-request-id"), "Missing gateway request ID");
      return result;
    },
  };
}

export function silentWave() {
  const sampleRate = 16_000;
  const dataBytes = sampleRate * 2;
  const wav = Buffer.alloc(44 + dataBytes);
  wav.write("RIFF", 0);
  wav.writeUInt32LE(36 + dataBytes, 4);
  wav.write("WAVEfmt ", 8);
  wav.writeUInt32LE(16, 16);
  wav.writeUInt16LE(1, 20);
  wav.writeUInt16LE(1, 22);
  wav.writeUInt32LE(sampleRate, 24);
  wav.writeUInt32LE(sampleRate * 2, 28);
  wav.writeUInt16LE(2, 32);
  wav.writeUInt16LE(16, 34);
  wav.write("data", 36);
  wav.writeUInt32LE(dataBytes, 40);
  return wav;
}
