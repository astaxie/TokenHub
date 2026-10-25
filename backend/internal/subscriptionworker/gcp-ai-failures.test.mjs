import assert from "node:assert/strict";
import test from "node:test";
import { fixture } from "./worker-fixture.mjs";

for (const operation of ["refresh", "models"]) {
  test(`Google ${operation} distinguishes upstream outages from rejected account credentials`, async (t) => {
    let status = 503;
    const auth = { type: "oauth", project: "synthetic-project", location: "global", clientId: "synthetic-client", clientSecret: "synthetic-secret",
      access: "synthetic-access", refresh: "synthetic-refresh", expires: Date.now() + 3600000 };
    const worker = await fixture(t, { network: (request) => {
      if (operation === "models" && request.url.endsWith("/token")) {
        return { status: 200, body: JSON.stringify({ access_token: "synthetic-renewed", expires_in: 3600 }) };
      }
      return { status, body: JSON.stringify({ error: { message: "synthetic-secret" } }) };
    } });
    for (status of [503, 429, 403]) {
      const result = await worker.call(operation, { provider: "gcp-ai", resource_id: `status-${status}`, auth });
      assert.equal(result.event, "error");
      assert.equal(result.error.code, status === 403 ? "invalid_credentials" : "invalid_upstream");
      assert.equal(result.error.status, status);
      assert.ok(!JSON.stringify(result.error).includes("synthetic-secret"));
    }
  });
}
