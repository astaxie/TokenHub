#!/usr/bin/env node

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { missingKeys, newLiteralTxKeys } from "./ui-translations.mjs";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const i18nRoot = join(root, "frontend/features/admin/i18n");

function git(args) {
  return execFileSync("git", args, {
    cwd: root,
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  });
}

function argumentsFromCLI(argv) {
  const options = { base: "", head: "" };
  for (let index = 0; index < argv.length; index += 1) {
    if (argv[index] === "--base") options.base = argv[++index] ?? "";
    else if (argv[index] === "--head") options.head = argv[++index] ?? "";
    else throw new Error(`Unknown argument: ${argv[index]}`);
  }
  if (Boolean(options.base) !== Boolean(options.head)) throw new Error("--base and --head must be provided together");
  return options;
}

function eventEndpoints() {
  const eventPath = process.env.GITHUB_EVENT_PATH;
  if (!eventPath || !existsSync(eventPath)) return null;
  const event = JSON.parse(readFileSync(eventPath, "utf8"));
  const base = event.pull_request?.base?.sha || event.merge_group?.base_sha || event.before;
  const head = event.pull_request?.head?.sha || event.merge_group?.head_sha || event.after;
  return base && head && !/^0+$/.test(base) ? { base, head } : null;
}

function changedPaths(base, head) {
  const range = base && head ? [base, head] : ["HEAD"];
  const tracked = git(["diff", "--name-only", "--diff-filter=ACMRT", ...range, "--", "frontend"])
    .split("\n").filter(Boolean);
  if (base && head) return tracked;
  const untracked = git(["ls-files", "--others", "--exclude-standard", "frontend"])
    .split("\n").filter(Boolean);
  return Array.from(new Set([...tracked, ...untracked]));
}

function sourceAt(path, revision) {
  if (!revision) return readFileSync(join(root, path), "utf8");
  try {
    return git(["show", `${revision}:${path}`]);
  } catch {
    return "";
  }
}

function main() {
  const cli = argumentsFromCLI(process.argv.slice(2));
  const endpoints = cli.base ? cli : eventEndpoints();
  if (process.env.CI && !endpoints) throw new Error("UI translation check could not resolve CI base/head commits");
  const base = endpoints?.base || "";
  const head = endpoints?.head || "";
  const paths = changedPaths(base, head).filter((path) => /\.[cm]?[jt]sx?$/.test(path));
  const additions = new Map();
  for (const path of paths) {
    const before = sourceAt(path, base || "HEAD");
    const after = sourceAt(path, head);
    for (const key of newLiteralTxKeys(before, after)) {
      const sources = additions.get(key) ?? [];
      sources.push(path);
      additions.set(key, sources);
    }
  }
  const failures = [];
  for (const locale of ["en", "ja"]) {
    for (const key of missingKeys(additions.keys(), i18nRoot, locale)) {
      failures.push(`${locale}: ${JSON.stringify(key)} used by ${additions.get(key).join(", ")}`);
    }
  }
  if (failures.length > 0) {
    console.error(`UI translation check failed:\n\n${failures.join("\n")}\n\nAdd both English and Japanese catalog entries for every new literal tx key.`);
    process.exitCode = 1;
    return;
  }
  console.log(`UI translation check passed (${additions.size} new literal key${additions.size === 1 ? "" : "s"}).`);
}

try {
  main();
} catch (error) {
  console.error(`UI translation check crashed: ${error instanceof Error ? error.message : error}`);
  process.exitCode = 2;
}
