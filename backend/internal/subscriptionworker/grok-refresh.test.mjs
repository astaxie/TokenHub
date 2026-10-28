import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";

const auth = { type: "oauth", access: "synthetic-old-access", refresh: "synthetic-stable-refresh", expires: 1 };
const json = (body) => ({ status: 200, body: JSON.stringify(body) });

for (const includeRefreshToken of [false, true]) {
  test(`Grok renews the latest access token when the token endpoint ${includeRefreshToken ? "retains" : "omits"} the refresh token`, async (t) => {
    let renewals = 0;
    const worker = await fixture(t, { network: (request) => {
      assert.equal(request.url, "https://auth.x.ai/oauth2/token");
      const form = new URLSearchParams(Buffer.from(request.body, "base64").toString());
      assert.equal(form.get("refresh_token"), auth.refresh);
      return json({ access_token: `synthetic-access-${++renewals}`, expires_in: 3600,
        ...(includeRefreshToken ? { refresh_token: auth.refresh } : {}) });
    } });
    for (let revision = 1; revision <= 3; revision++) {
      const result = await worker.call("refresh", revision === 1 ? { auth } : {});
      assert.equal(result.event, "result");
      assert.equal(result.result.auth.access, `synthetic-access-${revision}`);
      assert.equal(result.result.auth.refresh, auth.refresh);
      assert.equal(worker.saves.at(-1).access, result.result.auth.access);
    }
    assert.equal(renewals, 3, "Every new access-token revision must remain renewable");
  });
}

test("Grok recovers a later inference 401 after a refresh without token rotation", async (t) => {
  let renewals = 0;
  let attempts = 0;
  const worker = await fixture(t, { network: (request) => {
    if (request.url === "https://auth.x.ai/oauth2/token") {
      return json({ access_token: `synthetic-access-${++renewals}`, expires_in: 3600 });
    }
    assert.equal(request.url, "https://cli-chat-proxy.grok.com/v1/responses");
    attempts++;
    const access = request.headers.authorization[0];
    assert.equal(access, `Bearer ${worker.saves.at(-1).access}`, "Inference must use durably saved credentials");
    if (attempts > 1 && access === "Bearer synthetic-access-1") return { status: 401, body: "{}" };
    return json({ id: "synthetic-response", status: "completed", output: [] });
  } });
  const fields = { model: "grok-4.7", protocol: "responses", body: { input: "Hello" } };
  for (const input of [{ ...fields, auth }, fields]) {
    const result = await worker.call("fetch", input);
    assert.equal(result.event, "end");
    assert.equal(result.events.find(event => event.event === "response").status, 200);
  }
  assert.equal(attempts, 3, "The second inference retries once with the newly refreshed access token");
  assert.equal(renewals, 2);
  assert.equal(worker.saves.length, 2);
});
