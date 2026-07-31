#!/usr/bin/env node
//
// Fails when English documentation moves without its Simplified Chinese and Japanese
// counterparts. See tools/doc-translations.mjs for the rules themselves.
//
//   node tools/check-doc-translations.mjs
//   node tools/check-doc-translations.mjs --base <sha> --head <sha>
//   node tools/check-doc-translations.mjs --existence-only
//
// Without endpoints the co-change rule cannot run. Under CI that is a hard failure:
// the usual cause is a shallow checkout, and passing there would turn a configuration
// mistake into a silent bypass of the gate. Locally it degrades to the existence rule
// with a notice, because a developer running the script on a dirty tree has no base to
// compare against and should still get the useful half.

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import {
  EMPTY_TREE,
  checkCoChange,
  checkExistence,
  isNullSha,
  parseNameStatus,
  resolveEndpoints,
} from "./doc-translations.mjs";

function parseArgs(argv) {
  const options = { base: null, head: null, existenceOnly: false };
  for (let index = 0; index < argv.length; index += 1) {
    const arg = argv[index];
    if (arg === "--base") {
      options.base = argv[index + 1];
      index += 1;
    } else if (arg === "--head") {
      options.head = argv[index + 1];
      index += 1;
    } else if (arg === "--existence-only") {
      options.existenceOnly = true;
    } else if (arg === "--help" || arg === "-h") {
      console.log(
        [
          "Usage: node tools/check-doc-translations.mjs [--base <sha> --head <sha>] [--existence-only]",
          "",
          "  --base/--head     diff endpoints; defaults to the GitHub Actions event payload",
          "  --existence-only  skip the co-change rule (no diff endpoints needed)",
        ].join("\n"),
      );
      process.exit(0);
    } else {
      console.error(`Unknown argument: ${arg}`);
      process.exit(2);
    }
  }
  return options;
}

/**
 * Many CI systems set CI=1 rather than CI=true. Comparing against "true" alone made
 * the fail-closed branch unreachable on those runners, which is precisely the
 * environment it exists to protect.
 */
function isTruthyEnv(value) {
  if (value === undefined || value === "") {
    return false;
  }
  return !["false", "0", "no", "off"].includes(value.toLowerCase());
}

/** Raised for a git invocation that failed, so main() can report it cleanly. */
class GitError extends Error {}

function git(args, cwd) {
  try {
    return execFileSync("git", args, { cwd, encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  } catch (error) {
    const detail = error.stderr?.toString().trim() || error.message;
    throw new GitError(`git ${args.join(" ")} failed: ${detail}`);
  }
}

function repositoryRoot() {
  return git(["rev-parse", "--show-toplevel"]).trim();
}

/** Every path git tracks, which is the correct notion of "on disk" for this gate. */
function trackedPaths(root) {
  const output = git(["ls-files", "-z"], root);
  return new Set(output.split("\0").filter((path) => path !== ""));
}

function commitExists(root, sha) {
  try {
    git(["cat-file", "-e", `${sha}^{commit}`], root);
    return true;
  } catch {
    return false;
  }
}

function readEventPayload() {
  const path = process.env.GITHUB_EVENT_PATH;
  if (!path) {
    return null;
  }
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch (error) {
    console.error(`Could not read GITHUB_EVENT_PATH (${path}): ${error.message}`);
    return null;
  }
}

function report(failures) {
  console.error("Documentation translation check failed:\n");
  for (const failure of failures) {
    console.error(`- [${failure.rule}] ${failure.detail}`);
  }
  console.error(
    [
      "",
      "Every English document under docs/ (plus README.md and CONTRIBUTING.md) must be",
      `accompanied by its zh-CN and ja counterparts, and must be added, modified or`,
      "deleted together with them.",
      "",
      "Deliberately English-only documents are listed in ENGLISH_ONLY in",
      "tools/doc-translations.mjs.",
    ].join("\n"),
  );
}

function main() {
  const options = parseArgs(process.argv.slice(2));
  const root = repositoryRoot();
  const tracked = trackedPaths(root);

  // The existence rule runs first and unconditionally, so that even when co-change
  // enforcement cannot run the operator still learns about missing translations
  // instead of only being told the check was inconclusive.
  const failures = checkExistence(tracked, tracked);

  if (options.existenceOnly) {
    if (failures.length > 0) {
      report(failures);
      return 1;
    }
    console.log(
      `Translation existence check passed (${tracked.size} tracked paths, co-change skipped).`,
    );
    return 0;
  }

  let endpoints = null;
  if (options.base && options.head) {
    endpoints = { base: options.base, head: options.head };
  } else {
    endpoints = resolveEndpoints(process.env.GITHUB_EVENT_NAME, readEventPayload());
  }

  const underCI = isTruthyEnv(process.env.CI) || isTruthyEnv(process.env.GITHUB_ACTIONS);

  if (!endpoints) {
    if (failures.length > 0) {
      report(failures);
    }
    if (underCI) {
      console.error(
        "\nCould not determine diff endpoints from the event payload, so the co-change" +
          "\nrule did not run. Failing closed.",
      );
      return 1;
    }
    if (failures.length > 0) {
      return 1;
    }
    console.log(
      "Translation existence check passed. No diff endpoints available, so the" +
        " co-change rule was skipped (pass --base/--head to run it).",
    );
    return 0;
  }

  const base = isNullSha(endpoints.base) ? EMPTY_TREE : endpoints.base;
  const head = endpoints.head;

  for (const [label, sha] of [
    ["base", base],
    ["head", head],
  ]) {
    // The empty tree is a tree, not a commit, so it never needs this check.
    if (sha === EMPTY_TREE) {
      continue;
    }
    if (!commitExists(root, sha)) {
      if (failures.length > 0) {
        report(failures);
      }
      console.error(
        `\nThe ${label} commit ${sha} is not present in this checkout, so the co-change` +
          "\nrule did not run. Failing closed — a shallow checkout is the usual cause;" +
          "\nuse actions/checkout with fetch-depth: 0.",
      );
      return 1;
    }
  }

  const diff = git(
    ["diff", "--name-status", "-z", "--no-renames", base, head, "--", "*.md"],
    root,
  );
  failures.push(...checkCoChange(parseNameStatus(diff)));

  if (failures.length > 0) {
    report(failures);
    return 1;
  }
  console.log(
    `Translation check passed (${tracked.size} tracked paths, diff ${base.slice(0, 7)}..${head.slice(0, 7)}).`,
  );
  return 0;
}

try {
  process.exitCode = main();
} catch (error) {
  // Fail closed, but with a diagnostic rather than a stack trace. git being absent or
  // the checkout not being a repository are configuration problems, and a Node
  // traceback buries that under noise.
  console.error(
    error instanceof GitError
      ? `Documentation translation check could not run: ${error.message}`
      : `Documentation translation check crashed: ${error.stack ?? error}`,
  );
  process.exitCode = 1;
}
