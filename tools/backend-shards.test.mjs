import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { backendShardCommands, exactTestPattern, otherBackendPackages, parseTestInventory, shardInventory } from "./test-backend-shard.mjs";

test("backend shards cover the complete dynamic inventory exactly once", () => {
  const output = [...Array.from({ length: 101 }, (_, index) => `TestCase${index}`), "Example", "ExampleUsage", "FuzzRequest", "BenchmarkAllocation", "ok tokenhub/backend/internal/server 0.123s"].join("\n");
  const names = parseTestInventory(output);
  const shards = Array.from({ length: 4 }, (_, index) => shardInventory(names, index, 4));
  assert.equal(names.length, 104);
  assert.equal(new Set(shards.flat()).size, names.length);
  assert.deepEqual(shards.flat().sort(), names);
  assert.ok(Math.max(...shards.map((part) => part.length)) - Math.min(...shards.map((part) => part.length)) <= 1);
  for (let index = 0; index < 4; index++) {
    assert.deepEqual(shardInventory([...names].reverse(), index, 4), shards[index]);
    const pattern = new RegExp(exactTestPattern(shards[index]));
    for (const name of names) assert.equal(pattern.test(name), shards[index].includes(name));
    assert.equal(pattern.test(`${shards[index][0]}Extra`), false);
    assert.equal(pattern.test(`Prefix${shards[index][0]}`), false);
  }
});

test("backend patterns escape regex syntax and preserve full top-level names", () => {
  const names = ["TestA.B", "TestC+D", "TestE[1]", "TestF(2)", "TestG$", "TestH\\I"];
  const pattern = new RegExp(exactTestPattern(names));
  for (const name of names) assert.equal(pattern.test(name), true);
  assert.equal(pattern.test("TestAxB"), false);
});

test("backend sharding fails closed on empty or invalid inventories", () => {
  for (const output of ["", "ok package 0.1s", "TestOne\nTestOne", "TestOne/subtest", "TestOne\nunexpected output"]) {
    assert.throws(() => parseTestInventory(output));
  }
  for (const [names, index, count] of [[[], 0, 4], [["TestOne"], 4, 4], [["TestOne"], 0, 0], [["TestOne"], 1, 4], [["TestOne", "TestOne"], 0, 1], [["BadName"], 0, 1]]) {
    assert.throws(() => shardInventory(names, index, count));
  }
  assert.throws(() => exactTestPattern([]));
  for (const output of ["", "tokenhub/backend/internal/server", "other/package", "tokenhub/backend/internal/server\nother/package\nother/package"]) {
    assert.throws(() => otherBackendPackages(output));
    assert.throws(() => backendShardCommands(["TestA", "TestB"], output, 1, 2));
  }
});

test("non-server backend packages run once and every server shard keeps the existing timeout", () => {
  const packages = "tokenhub/backend/internal/server\ntokenhub/backend/cmd/tokenhub\ntokenhub/backend/internal/subscriptionworker\n";
  const names = ["TestA", "TestB", "ExampleC", "FuzzD"];
  const plans = Array.from({ length: 4 }, (_, index) => backendShardCommands(names, packages, index, 4));
  assert.equal(plans[0].commands.length, 2);
  assert.deepEqual(plans[0].commands[0], ["test", "-count=1", "-timeout", "15m", "tokenhub/backend/cmd/tokenhub", "tokenhub/backend/internal/subscriptionworker"]);
  for (const plan of plans.slice(1)) assert.equal(plan.commands.length, 1);
  for (const plan of plans) assert.deepEqual(plan.commands.at(-1).slice(0, 6), ["test", "-count=1", "-timeout", "15m", "./internal/server", "-run"]);
});

test("CI runs every backend shard without weakening the stable aggregate", async () => {
  const workflow = await readFile(new URL("../.github/workflows/ci.yml", import.meta.url), "utf8");
  const backend = workflow.split("  backend:\n")[1].split("\n  plugin-devkit:")[0];
  assert.match(backend, /name: Backend \(\$\{\{ matrix\.number \}\}\/4\)/);
  assert.match(backend, /fail-fast: false/);
  assert.deepEqual([...backend.matchAll(/- shard: (\d)/g)].map((match) => Number(match[1])), [0, 1, 2, 3]);
  assert.match(backend, /node \.\.\/tools\/test-backend-shard\.mjs \$\{\{ matrix\.shard \}\} 4/);
  assert.equal((backend.match(/if: matrix\.shard == 0/g) ?? []).length, 5);
  assert.match(backend, /timeout-minutes: 20/);
});
