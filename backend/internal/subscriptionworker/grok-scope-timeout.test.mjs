import assert from "node:assert/strict";
import test from "node:test";
import { createGrokProvider } from "./grok.mjs";

const missingScope = JSON.stringify({ code: "permission-denied", error: "OAuth2 token missing required scope: api:access" });

async function stalledGrokRequest(t, initial) {
  let controller;
  let cancelled = false;
  const body = new ReadableStream({
    start(value) { controller = value; value.enqueue(new TextEncoder().encode(initial)); },
    cancel() { cancelled = true; },
  });
  t.after(() => { if (!cancelled) controller.close(); });
  t.mock.method(globalThis, "fetch", async () => new Response(body, { status: 403 }));
  const provider = await createGrokProvider({ client: {} });
  const loader = await provider.auth.loader(async () => ({ access: "synthetic-token", expires: Date.now() + 3600000 }));
  const pending = loader.fetch("https://cli-chat-proxy.grok.com/v1/responses", {
    method: "POST", body: JSON.stringify({ model: "grok-4.7", input: "Hello", stream: true }),
  });
  return { pending, cancelled: () => cancelled };
}

async function within(pending, milliseconds) {
  let timer;
  try {
    return await Promise.race([
      pending.then((response) => ({ response }), (error) => ({ error })),
      new Promise((resolve) => { timer = setTimeout(() => resolve({ timedOut: true }), milliseconds); }),
    ]);
  } finally { clearTimeout(timer); }
}

test("Grok detects a complete scope rejection without waiting for EOF", async (t) => {
  const { pending, cancelled } = await stalledGrokRequest(t, missingScope);
  const result = await within(pending, 500);
  assert.equal(result.timedOut, undefined, "Complete scope diagnostic must not wait for EOF");
  assert.equal(result.error?.code, "grok_api_scope_missing");
  assert.equal(cancelled(), true, "Rejected response must cancel its upstream body");
});

test("Grok bounds incomplete scope inspection and preserves the original error response", async (t) => {
  const partial = missingScope.slice(0, -1);
  const { pending, cancelled } = await stalledGrokRequest(t, partial);
  const result = await within(pending, 2000);
  assert.equal(result.timedOut, undefined, "Incomplete diagnostic must have a bounded inspection time");
  assert.equal(result.error, undefined);
  assert.equal(result.response.status, 403);
  const reader = result.response.body.getReader();
  assert.equal(new TextDecoder().decode((await reader.read()).value), partial);
  await reader.cancel();
  reader.releaseLock();
  assert.equal(cancelled(), true, "Inspection must not leave a tee branch blocking upstream cancellation");
});
