import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const { auditRequestPagePath, auditRequestTimeRangeParameters } = await importTypeScript(new URL("./audit-request-page.ts", import.meta.url));

test("audit request page path carries server-side filters and pagination", () => {
  assert.equal(
    auditRequestPagePath({
      page: 3,
      pageSize: 50,
      status: "error",
      query: "Alpha Finance / 429",
	  model: "gpt-4.1",
	  since: "2026-08-16T00:00:00.000Z",
	  until: "2026-08-16T01:00:00.000Z",
    }),
    "/api/admin/audit/requests?page=3&page_size=50&status=error&q=Alpha+Finance+%2F+429&model=gpt-4.1&since=2026-08-16T00%3A00%3A00.000Z&until=2026-08-16T01%3A00%3A00.000Z",
  );
});

test("audit request page path normalizes empty search terms", () => {
  assert.equal(
    auditRequestPagePath({
      page: 1,
      pageSize: 20,
      status: "all",
      query: "   ",
	  model: "",
	  since: "",
	  until: "",
    }),
    "/api/admin/audit/requests?page=1&page_size=20&status=all&q=",
  );
});

test("audit request time ranges use an inclusive UTC window", () => {
  const now = new Date("2026-08-16T01:00:00.000Z");
  assert.deepEqual(auditRequestTimeRangeParameters("15m", now), {
    since: "2026-08-16T00:45:00.000Z",
    until: "2026-08-16T01:00:00.000Z",
  });
  assert.deepEqual(auditRequestTimeRangeParameters("1h", now), {
    since: "2026-08-16T00:00:00.000Z",
    until: "2026-08-16T01:00:00.000Z",
  });
  assert.deepEqual(auditRequestTimeRangeParameters("24h", now), {
    since: "2026-08-15T01:00:00.000Z",
    until: "2026-08-16T01:00:00.000Z",
  });
  assert.deepEqual(auditRequestTimeRangeParameters("all", now), { since: "", until: "" });
});
