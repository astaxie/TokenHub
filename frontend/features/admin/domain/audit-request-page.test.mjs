import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const { auditRequestPagePath } = await importTypeScript(new URL("./audit-request-page.ts", import.meta.url));

test("audit request page path carries server-side filters and pagination", () => {
  assert.equal(
    auditRequestPagePath({
      page: 3,
      pageSize: 50,
      status: "error",
      query: "Alpha Finance / 429",
    }),
    "/api/admin/audit/requests?page=3&page_size=50&status=error&q=Alpha+Finance+%2F+429",
  );
});

test("audit request page path normalizes empty search terms", () => {
  assert.equal(
    auditRequestPagePath({
      page: 1,
      pageSize: 20,
      status: "all",
      query: "   ",
    }),
    "/api/admin/audit/requests?page=1&page_size=20&status=all&q=",
  );
});
