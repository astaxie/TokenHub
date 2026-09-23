import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const { filterAPIKeys } = await importTypeScript(new URL("./api-key-filter.ts", import.meta.url));

const keys = [
  { id: "key_1", project_id: "project_a", name: "shenshen_sun@example.com", status: "active" },
  { id: "key_2", project_id: "project_a", name: "alpha-service", status: "active" },
  { id: "key_3", project_id: "project_b", name: "project-nightly", status: "disabled" },
];

test("matches a name substring anywhere in the key name", () => {
  assert.deepEqual(filterAPIKeys(keys, "sun@example").map((key) => key.id), ["key_1"]);
  assert.deepEqual(filterAPIKeys(keys, "alpha").map((key) => key.id), ["key_2"]);
});

test("matching is case-insensitive and ignores surrounding whitespace", () => {
  assert.deepEqual(filterAPIKeys(keys, "  SHENSHEN ").map((key) => key.id), ["key_1"]);
});

test("does not match JSON field names such as project_id", () => {
  // 只有名称真的包含 project 的 key_3 应该命中，不能因为每行都有 project_id 字段而全量命中。
  assert.deepEqual(filterAPIKeys(keys, "project").map((key) => key.id), ["key_3"]);
});

test("empty and whitespace-only queries return the original list", () => {
  assert.equal(filterAPIKeys(keys, ""), keys);
  assert.equal(filterAPIKeys(keys, "   "), keys);
});
