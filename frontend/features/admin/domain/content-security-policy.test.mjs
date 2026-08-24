import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const { adminContentSecurityPolicy } = await importTypeScript(new URL("./content-security-policy.ts", import.meta.url));

test("uses a nonce instead of unsafe inline scripts in production", () => {
  const policy = adminContentSecurityPolicy("request-nonce", false);
  assert.match(policy, /script-src 'self' 'nonce-request-nonce' 'strict-dynamic'/);
  assert.doesNotMatch(policy, /script-src[^;]*'unsafe-inline'/);
  assert.doesNotMatch(policy, /script-src[^;]*'unsafe-eval'/);
  assert.match(policy, /frame-ancestors 'none'/);
  assert.match(policy, /object-src 'none'/);
});

test("allows eval only for the Next.js development runtime", () => {
  const policy = adminContentSecurityPolicy("development-nonce", true);
  assert.match(policy, /script-src[^;]*'unsafe-eval'/);
  assert.doesNotMatch(policy, /script-src[^;]*'unsafe-inline'/);
});
