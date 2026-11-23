import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "../features/admin/domain/typescript-test-loader.mjs";

// The module reads the build-time value once; a sub-path build sets it in next.config.
process.env.NEXT_PUBLIC_TOKENHUB_BASE_PATH = "/gateway/tokenhub/";

const { withBasePath } = await importTypeScript(new URL("./base-path.ts", import.meta.url));

test("prefixes root-relative paths exactly once", () => {
  assert.equal(withBasePath("/brand/tokenhub-logo.png"), "/gateway/tokenhub/brand/tokenhub-logo.png");
  assert.equal(withBasePath("/gateway/tokenhub/brand/tokenhub-logo.png"), "/gateway/tokenhub/brand/tokenhub-logo.png");
});

test("leaves absolute, protocol-relative and empty values untouched", () => {
  assert.equal(withBasePath("https://example.test/brand/x.png"), "https://example.test/brand/x.png");
  assert.equal(withBasePath("//cdn.example.test/brand/x.png"), "//cdn.example.test/brand/x.png");
  assert.equal(withBasePath(""), "");
});
