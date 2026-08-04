import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  clampPlaygroundMaxTokens,
  hasPlaygroundUsage,
  playgroundMaxTokenLimit,
  selectPlaygroundCandidateBranch,
} = await importTypeScript(new URL("./playground-logic.ts", import.meta.url));

test("playground max tokens honor model limits", () => {
  assert.equal(playgroundMaxTokenLimit(2048, 32768), 2048);
  assert.equal(playgroundMaxTokenLimit(undefined, 8192), 8192);
  assert.equal(clampPlaygroundMaxTokens(4096, 2048), 2048);
  assert.equal(clampPlaygroundMaxTokens(undefined, 1024), 1024);
});

test("unknown usage is not presented as zero usage", () => {
  assert.equal(hasPlaygroundUsage(undefined), false);
  assert.equal(hasPlaygroundUsage({ prompt_tokens: 0, completion_tokens: 0 }), false);
  assert.equal(hasPlaygroundUsage({ prompt_tokens: 12, completion_tokens: 3 }), true);
});

test("switching an earlier candidate removes descendants", () => {
  const turns = [
    { id: "one", selectedCandidateID: "a", value: 1 },
    { id: "two", selectedCandidateID: "c", value: 2 },
  ];
  assert.deepEqual(selectPlaygroundCandidateBranch(turns, "one", "b"), [
    { id: "one", selectedCandidateID: "b", value: 1 },
  ]);
  assert.equal(selectPlaygroundCandidateBranch(turns, "one", "a"), turns);
});
