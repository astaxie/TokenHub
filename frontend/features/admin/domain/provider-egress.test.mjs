import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const { providerEgressTestPayload } = await importTypeScript(new URL("./provider-egress.ts", import.meta.url));

test("proxy test uses unsaved Provider settings without unrelated form fields", () => {
  assert.deepEqual(providerEgressTestPayload("prv_one", {
    name: "Gateway Base Settings",
    status: "active",
    provider_egress_mode: "configured_proxy",
    provider_proxy_host: "proxy.internal",
    provider_proxy_port: "8080",
    provider_proxy_password: "unsaved-password",
    public_base_url: "https://tokenhub.example",
  }), {
    provider_id: "prv_one",
    fields: {
      provider_egress_mode: "configured_proxy",
      provider_proxy_host: "proxy.internal",
      provider_proxy_port: "8080",
      provider_proxy_password: "unsaved-password",
    },
  });
});
