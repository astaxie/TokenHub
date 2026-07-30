import assert from "node:assert/strict";
import { describe, it } from "node:test";

import {
  DEFAULT_MAX_LINES,
  allowedFor,
  countLines,
  findTightenable,
  findViolations,
  renderFrozen,
} from "./source-lines.mjs";

describe("line counting", () => {
  it("counts a trailing newline as an extra segment", () => {
    // This is wc -l plus one. The definition is load-bearing: revision 1 of the plan
    // recorded wc -l baselines, which would have failed the clean repository because
    // the checker measures one higher.
    assert.equal(countLines("a\nb\n"), 3);
    assert.equal(countLines("a\nb"), 2);
    assert.equal(countLines(""), 1);
  });

  it("treats CRLF the same as LF", () => {
    assert.equal(countLines("a\r\nb\r\n"), countLines("a\nb\n"));
  });
});

describe("limits", () => {
  it("uses the default limit for an unlisted file", () => {
    assert.equal(allowedFor("backend/new.go", new Map()), DEFAULT_MAX_LINES);
  });

  it("uses the frozen baseline for a listed file", () => {
    assert.equal(allowedFor("backend/big.go", new Map([["backend/big.go", 4000]])), 4000);
  });
});

describe("violations", () => {
  it("passes a new file at the limit and fails it one line over", () => {
    assert.deepEqual(findViolations(new Map([["a.go", DEFAULT_MAX_LINES]]), new Map()), []);

    const violations = findViolations(new Map([["a.go", DEFAULT_MAX_LINES + 1]]), new Map());
    assert.equal(violations.length, 1);
    assert.equal(violations[0].allowed, DEFAULT_MAX_LINES);
    assert.equal(violations[0].frozen, false);
  });

  it("lets a frozen file sit at its baseline but not grow", () => {
    const frozen = new Map([["big.go", 4000]]);
    assert.deepEqual(findViolations(new Map([["big.go", 4000]]), frozen), []);

    const violations = findViolations(new Map([["big.go", 4001]]), frozen);
    assert.equal(violations.length, 1);
    assert.equal(violations[0].frozen, true);
    assert.equal(violations[0].allowed, 4000);
  });

  it("allows a frozen file to shrink", () => {
    assert.deepEqual(findViolations(new Map([["big.go", 10]]), new Map([["big.go", 4000]])), []);
  });
});

describe("ratchet", () => {
  it("reports a baseline that can be lowered", () => {
    const tightenable = findTightenable(new Map([["big.go", 3900]]), new Map([["big.go", 4000]]));
    assert.deepEqual(tightenable, [{ path: "big.go", lines: 3900, baseline: 4000, reason: "shrank" }]);
  });

  it("reports a baseline whose file is gone", () => {
    const tightenable = findTightenable(new Map(), new Map([["big.go", 4000]]));
    assert.equal(tightenable[0].reason, "gone");
  });

  it("never offers to raise a baseline", () => {
    // A file that grew is a violation, not something --update may bless.
    assert.deepEqual(findTightenable(new Map([["big.go", 4200]]), new Map([["big.go", 4000]])), []);
  });

  it("never offers to add a baseline for an unlisted oversized file", () => {
    assert.deepEqual(findTightenable(new Map([["new.go", 9000]]), new Map()), []);
  });
});

describe("serialization", () => {
  it("survives a path containing replacement-pattern characters", () => {
    // String.replace expands $& and $1 in a string replacement, so --update writes the
    // rendered table through a function replacer. This locks in the input that would
    // otherwise corrupt the file.
    const original = 'export const FROZEN = new Map([\n  ["a.go", 1],\n]);\ntrailer\n';
    const rendered = renderFrozen(new Map([["weird/$&$1.go", 7]]));
    const rewritten = original.replace(
      /export const FROZEN = new Map\(\[[\s\S]*?\n\]\);/,
      () => rendered,
    );
    assert.ok(rewritten.includes("weird/$&$1.go"), "the path survives verbatim");
    assert.ok(rewritten.endsWith("trailer\n"), "content after the table is preserved");
  });

  it("renders a sorted, re-parsable table", () => {
    const rendered = renderFrozen(
      new Map([
        ["z.go", 2],
        ["a.go", 1],
      ]),
    );
    assert.match(rendered, /^export const FROZEN = new Map\(\[/);
    assert.ok(rendered.indexOf('"a.go"') < rendered.indexOf('"z.go"'), "entries are sorted");
    assert.match(rendered, /\n\]\);$/);
  });
});
