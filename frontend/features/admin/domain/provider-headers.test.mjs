import assert from "node:assert/strict";
import { describe, test } from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  providerHeaderEntryErrors,
  providerHeaderFormError,
} = await importTypeScript(new URL("./provider-headers.ts", import.meta.url));

describe("provider header validation", () => {
  test("keeps core reserved headers blocked independently of provider plugins", () => {
    const entries = [{ name: "Authorization", value: "Bearer user-key", sensitive: false }];

    assert.deepEqual(providerHeaderEntryErrors(entries), ["该请求头由 TokenHub 管理，不能覆盖。"]);
    assert.equal(providerHeaderFormError(JSON.stringify(entries)), "该请求头由 TokenHub 管理，不能覆盖。");
  });

  test("allows provider-owned headers unless a provider plugin manages them", () => {
    const entries = [{ name: "API-Key", value: "user-key", sensitive: true }];

    assert.deepEqual(providerHeaderEntryErrors(entries), []);
    assert.deepEqual(providerHeaderEntryErrors(entries, ["api-key"]), ["该请求头由 Provider 插件管理，不能覆盖。"]);
    assert.equal(providerHeaderFormError(JSON.stringify(entries), ["api-key"]), "该请求头由 Provider 插件管理，不能覆盖。");
  });

  test("normalizes managed headers and duplicate names case-insensitively", () => {
    assert.deepEqual(
      providerHeaderEntryErrors([{ name: "X-Native-Version", value: "2026-01-01", sensitive: false }], [" x-native-version "]),
      ["该请求头由 Provider 插件管理，不能覆盖。"],
    );

    assert.deepEqual(
      providerHeaderEntryErrors([
        { name: "X-Trace-ID", value: "one", sensitive: false },
        { name: "x-trace-id", value: "two", sensitive: false },
      ]),
      ["请求头名称不能重复（大小写不敏感）。"],
    );
  });
});
