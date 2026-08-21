import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const workflow = await readFile(new URL("../.github/workflows/ci.yml", import.meta.url), "utf8");
const aggregate = workflow.match(/\n  ci:\n[\s\S]*$/)?.[0] ?? "";

test("stable CI aggregate requires every compatibility job", () => {
  const needsBlock = aggregate.match(/\n    needs:\n((?:      - [^\n]+\n)+)/)?.[1] ?? "";
  const needs = [...needsBlock.matchAll(/- ([^\n]+)/g)].map((match) => match[1]).sort();
  assert.deepEqual(needs, [
    "backend",
    "backend-db-n1",
    "backend-postgres",
    "deployment",
    "frontend",
    "repo-gates",
  ]);

  const expectedJobs = Number(aggregate.match(/EXPECTED_JOBS: (\d+)/)?.[1]);
  assert.equal(expectedJobs, needs.length);
});
