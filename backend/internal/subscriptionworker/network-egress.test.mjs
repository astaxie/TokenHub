import assert from "node:assert/strict";
import { AsyncLocalStorage } from "node:async_hooks";
import test from "node:test";
import { createNetworkRelay } from "./network.mjs";
import { failure, publicError } from "./validation.mjs";

function fixture() {
  const scope = new AsyncLocalStorage();
  const context = { id: "command", provider: "grok", controller: new AbortController() };
  let requests = 0;
  const relay = createNetworkRelay(scope, async (event) => {
    if (event.event !== "network.request") return;
    if (++requests === 1) {
      relay.receive({ operation: "network.error", request_id: event.request_id, error: "provider_proxy_connect_failed", status: 502 });
      return;
    }
    relay.receive({ operation: "network.response", request_id: event.request_id, status: 200, body: Buffer.from("recovered").toString("base64") });
  });
  return { context, run: (fn) => scope.run(context, fn), fetch: () => relay.fetch("https://cli-chat-proxy.grok.com/v1/responses") };
}

test("a successful retry clears the command's previous guarded egress failure", async () => {
  const worker = fixture();
  await worker.run(async () => {
    await assert.rejects(worker.fetch(), { code: "provider_proxy_connect_failed" });
    assert.equal(publicError(new Error("adapter wrapper"), worker.context).code, "provider_proxy_connect_failed");
    assert.equal(await (await worker.fetch()).text(), "recovered");
    assert.equal(publicError(new Error("later unrelated failure"), worker.context).code, "subscription_operation_failed");
  });
});

test("guarded egress failures do not replace explicit errors or failed credential saves", async () => {
  const worker = fixture();
  await worker.run(async () => {
    await assert.rejects(worker.fetch(), { code: "provider_proxy_connect_failed" });
    assert.equal(publicError(failure("invalid_request", "Invalid request"), worker.context).code, "invalid_request");
    assert.equal(publicError(Object.assign(new Error("bad input"), { status: 400 }), worker.context).status, 400);
    assert.equal(publicError(Object.assign(new Error("expired"), { signIn: "expired" }), worker.context).sign_in, "expired");
    worker.context.account = { saveError: failure("credential_save_failed", "Save failed", 503) };
    assert.equal(publicError(new Error("adapter wrapper"), worker.context).code, "credential_save_failed");
  });
});
