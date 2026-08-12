import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  effectiveProviderHeaderEntries,
  parseProviderHeaderEntries,
  providerHeaderEntryErrors,
  providerHeaderFormError,
  providerHeaderMask,
  providerHeadersFormValue,
  providerHeadersPayload,
} from "../frontend/features/admin/domain/provider-headers.ts";

describe("Provider custom request headers", () => {
  it("round trips public and sensitive values through the form payload", () => {
    const form = providerHeadersFormValue(
      { "User-Agent": "TokenHub/1.0", "X-Tenant": providerHeaderMask },
      ["x-tenant"],
    );
    assert.deepEqual(parseProviderHeaderEntries(form), [
      { name: "User-Agent", value: "TokenHub/1.0", sensitive: false, retained: false },
      { name: "X-Tenant", value: providerHeaderMask, sensitive: true, retained: true },
    ]);
    assert.deepEqual(providerHeadersPayload(form), {
      headers: { "User-Agent": "TokenHub/1.0", "X-Tenant": providerHeaderMask },
      sensitive_headers: ["X-Tenant"],
    });
  });

  it("merges Resource overrides without case-sensitive duplicates", () => {
    const effective = effectiveProviderHeaderEntries(
      [{ name: "X-Tenant", value: "provider", sensitive: false }, { name: "User-Agent", value: "TokenHub", sensitive: false }],
      [{ name: "x-tenant", value: "resource", sensitive: true }],
    );
    assert.deepEqual(effective, [
      { name: "x-tenant", value: "resource", sensitive: true },
      { name: "User-Agent", value: "TokenHub", sensitive: false },
    ]);
  });

  it("reports duplicate, empty, and newline errors before save", () => {
    const errors = providerHeaderEntryErrors([
      { name: "X-Test", value: "one", sensitive: false },
      { name: "x-test", value: "", sensitive: false },
      { name: "X-Newline", value: "bad\nvalue", sensitive: false },
    ]);
    assert.equal(errors.length, 3);
  });

  it("allows only previously stored sensitive values to be retained empty", () => {
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "X-Secret", value: "", sensitive: true }])), "请求头值不能为空。");
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "X-Secret", value: "", sensitive: true, retained: true }])), "");
  });

  it("rejects reserved, malformed, and oversized values before save", () => {
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "Authorization", value: "mine", sensitive: true }])), "该请求头由 TokenHub 管理，不能覆盖。");
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "Bad Header", value: "value", sensitive: false }])), "请求头名称格式不合法。");
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "X-Large", value: "x".repeat(4097), sensitive: false }])), "单个请求头值不能超过 4 KiB。");
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "X-Nul", value: "a\0b", sensitive: false }])), "请求头值不能包含非法控制字符。");
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "X-Del", value: "a\x7fb", sensitive: false }])), "请求头值不能包含非法控制字符。");
    assert.equal(providerHeaderFormError(JSON.stringify([{ name: "X-Tab", value: "a\tb", sensitive: false }])), "");
  });
});
