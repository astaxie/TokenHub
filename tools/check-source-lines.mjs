#!/usr/bin/env node
//
// Fails when a source file exceeds the line ceiling in tools/source-lines.mjs.
//
//   node tools/check-source-lines.mjs
//   node tools/check-source-lines.mjs --update   # lower baselines that shrank
//
// --update only ever lowers an existing baseline. It cannot add one and cannot raise
// one, so it is a ratchet handle rather than a bypass.

import { execFileSync } from "node:child_process";
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { extname, join, relative, sep } from "node:path";

import {
  DEFAULT_MAX_LINES,
  FROZEN,
  SKIP_DIRECTORIES,
  SOURCE_ROOTS,
  countLines,
  findTightenable,
  findViolations,
  renderFrozen,
} from "./source-lines.mjs";

function repositoryRoot() {
  return execFileSync("git", ["rev-parse", "--show-toplevel"], { encoding: "utf8" }).trim();
}

function collect(root, directory, extensions, measured) {
  let entries;
  try {
    entries = readdirSync(directory, { withFileTypes: true });
  } catch (error) {
    if (error.code === "ENOENT") return;
    throw error;
  }
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      if (SKIP_DIRECTORIES.has(entry.name)) continue;
      collect(root, path, extensions, measured);
    } else if (entry.isFile() && extensions.includes(extname(entry.name))) {
      const key = relative(root, path).split(sep).join("/");
      measured.set(key, countLines(readFileSync(path, "utf8")));
    }
  }
}

function measureAll(root) {
  const measured = new Map();
  for (const { root: sourceRoot, extensions } of SOURCE_ROOTS) {
    collect(root, join(root, sourceRoot), extensions, measured);
  }
  return measured;
}

function update(root, measured) {
  const tightenable = findTightenable(measured, FROZEN);
  if (tightenable.length === 0) {
    console.log("No baseline can be tightened; every frozen file is still at its recorded size.");
    return 0;
  }

  const next = new Map(FROZEN);
  for (const entry of tightenable) {
    if (entry.reason === "gone") {
      next.delete(entry.path);
      console.log(`- ${entry.path}: removed (file no longer present)`);
    } else if (entry.reason === "graduated") {
      next.delete(entry.path);
      console.log(
        `- ${entry.path}: removed (${entry.lines} lines now fits the default limit` +
          ` ${DEFAULT_MAX_LINES})`,
      );
    } else {
      next.set(entry.path, entry.lines);
      console.log(`- ${entry.path}: ${entry.baseline} -> ${entry.lines}`);
    }
  }

  const modulePath = join(root, "tools/source-lines.mjs");
  const source = readFileSync(modulePath, "utf8");
  // A function replacer, not a string: String.replace expands $&, $1 and friends in a
  // string replacement, so a path containing those characters would rewrite the table
  // into garbage.
  const replaced = source.replace(
    /export const FROZEN = new Map\(\[[\s\S]*?\n\]\);/,
    () => renderFrozen(next),
  );
  if (replaced === source) {
    console.error("Could not locate the FROZEN table in tools/source-lines.mjs.");
    return 1;
  }
  writeFileSync(modulePath, replaced);
  console.log(`\nTightened ${tightenable.length} baseline(s).`);
  return 0;
}

function main() {
  const args = process.argv.slice(2);
  const root = repositoryRoot();
  const measured = measureAll(root);

  if (args.includes("--update")) {
    return update(root, measured);
  }

  const violations = findViolations(measured, FROZEN);
  if (violations.length > 0) {
    console.error("Source files exceeded their line limit:\n");
    for (const violation of violations) {
      const kind = violation.frozen ? "frozen baseline" : "limit";
      console.error(`- ${violation.path}: ${violation.lines} lines (${kind} ${violation.allowed})`);
    }
    console.error(
      [
        "",
        `New files must stay under ${DEFAULT_MAX_LINES} lines. A file already listed in`,
        "FROZEN may not grow past the size it had when the gate landed — split it, or",
        "add the new code somewhere else.",
      ].join("\n"),
    );
    return 1;
  }

  const tightenable = findTightenable(measured, FROZEN);
  if (tightenable.length > 0) {
    console.log(
      `Source line check passed, but ${tightenable.length} baseline(s) can be tightened.` +
        " Run: node tools/check-source-lines.mjs --update",
    );
    return 0;
  }

  console.log(
    `Source line check passed (${measured.size} files, default limit ${DEFAULT_MAX_LINES},` +
      ` ${FROZEN.size} frozen baselines).`,
  );
  return 0;
}

try {
  process.exitCode = main();
} catch (error) {
  console.error(`Source line check could not run: ${error.message}`);
  process.exitCode = 1;
}
