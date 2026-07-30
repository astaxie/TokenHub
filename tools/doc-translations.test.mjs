import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { after, describe, it } from "node:test";

import {
  EMPTY_TREE,
  checkCoChange,
  checkExistence,
  counterpartsFor,
  isEnglishOnly,
  isNullSha,
  isTrackedEnglishDoc,
  parseNameStatus,
  requiresTranslation,
  resolveEndpoints,
} from "./doc-translations.mjs";

const scriptsDir = dirname(fileURLToPath(import.meta.url));
const checker = join(scriptsDir, "check-doc-translations.mjs");

describe("tracked universe", () => {
  it("tracks the two root documents and the English docs tree", () => {
    assert.equal(isTrackedEnglishDoc("README.md"), true);
    assert.equal(isTrackedEnglishDoc("CONTRIBUTING.md"), true);
    assert.equal(isTrackedEnglishDoc("docs/architecture.md"), true);
    assert.equal(isTrackedEnglishDoc("docs/development/workflows/fast-dev.md"), true);
  });

  it("does not track locale trees, which are targets rather than sources", () => {
    assert.equal(isTrackedEnglishDoc("docs/zh-CN/architecture.md"), false);
    assert.equal(isTrackedEnglishDoc("docs/ja/architecture.md"), false);
  });

  it("does not track documents outside the universe", () => {
    // The regression this guards: defining the universe as "every English .md"
    // swept in sdk/README.md, which has no counterpart and no sensible one.
    assert.equal(isTrackedEnglishDoc("sdk/README.md"), false);
    // The root translations are targets, not sources: treating README.zh-CN.md as a
    // source would demand a README.zh-CN.ja.md.
    assert.equal(isTrackedEnglishDoc("README.zh-CN.md"), false);
    assert.equal(isTrackedEnglishDoc("README.ja.md"), false);
    assert.equal(isTrackedEnglishDoc("backend/notes.md"), false);
    assert.equal(isTrackedEnglishDoc("docs/assets/logo.png"), false);
  });

  it("separates deliberately English-only documents from untracked ones", () => {
    assert.equal(isEnglishOnly("docs/postgresql-setup.md"), true);
    assert.equal(isEnglishOnly("docs/development/workflows/fast-dev.md"), true);
    assert.equal(isEnglishOnly("docs/architecture.md"), false);

    assert.equal(requiresTranslation("docs/postgresql-setup.md"), false);
    assert.equal(requiresTranslation("docs/architecture.md"), true);
    assert.equal(requiresTranslation("sdk/README.md"), false);
  });
});

describe("counterpart mapping", () => {
  it("maps root documents to a locale suffix", () => {
    const counterparts = counterpartsFor("README.md");
    assert.equal(counterparts.get("zh-CN"), "README.zh-CN.md");
    assert.equal(counterparts.get("ja"), "README.ja.md");
  });

  it("maps docs/ documents into the locale tree, preserving subdirectories", () => {
    assert.equal(counterpartsFor("docs/architecture.md").get("ja"), "docs/ja/architecture.md");
    assert.equal(
      counterpartsFor("docs/guides/setup.md").get("zh-CN"),
      "docs/zh-CN/guides/setup.md",
    );
  });
});

describe("existence rule", () => {
  it("passes when both counterparts exist", () => {
    const paths = new Set([
      "docs/architecture.md",
      "docs/zh-CN/architecture.md",
      "docs/ja/architecture.md",
    ]);
    assert.deepEqual(checkExistence(paths, paths), []);
  });

  it("reports each missing locale separately", () => {
    const paths = new Set(["docs/architecture.md", "docs/zh-CN/architecture.md"]);
    const failures = checkExistence(paths, paths);
    assert.equal(failures.length, 1);
    assert.equal(failures[0].locale, "ja");
    assert.equal(failures[0].counterpart, "docs/ja/architecture.md");
  });

  it("ignores English-only and untracked documents", () => {
    const paths = new Set(["docs/postgresql-setup.md", "sdk/README.md"]);
    assert.deepEqual(checkExistence(paths, paths), []);
  });
});

describe("co-change rule", () => {
  it("passes when all three move together", () => {
    const changes = new Map([
      ["docs/architecture.md", "M"],
      ["docs/zh-CN/architecture.md", "M"],
      ["docs/ja/architecture.md", "M"],
    ]);
    assert.deepEqual(checkCoChange(changes), []);
  });

  it("fails when a translation is left behind", () => {
    const changes = new Map([
      ["docs/architecture.md", "M"],
      ["docs/zh-CN/architecture.md", "M"],
    ]);
    const failures = checkCoChange(changes);
    assert.equal(failures.length, 1);
    assert.equal(failures[0].locale, "ja");
    assert.match(failures[0].detail, /was not touched/);
  });

  it("accepts a newly added translation answering a modified source", () => {
    // A translation that did not exist before is legitimately an add while its
    // English source is only a modify.
    const changes = new Map([
      ["docs/architecture.md", "M"],
      ["docs/zh-CN/architecture.md", "A"],
      ["docs/ja/architecture.md", "A"],
    ]);
    assert.deepEqual(checkCoChange(changes), []);
  });

  it("requires a delete to be mirrored by a delete", () => {
    const changes = new Map([
      ["docs/architecture.md", "D"],
      ["docs/zh-CN/architecture.md", "D"],
      ["docs/ja/architecture.md", "M"],
    ]);
    const failures = checkCoChange(changes);
    assert.equal(failures.length, 1);
    assert.equal(failures[0].locale, "ja");
    assert.match(failures[0].detail, /deleted but .* was modified/);
  });

  it("treats a rename as delete-plus-add on both sides", () => {
    // --no-renames means git reports a rename this way; the old translation paths
    // must be cleaned up and the new ones created.
    const changes = new Map([
      ["docs/old.md", "D"],
      ["docs/zh-CN/old.md", "D"],
      ["docs/ja/old.md", "D"],
      ["docs/new.md", "A"],
      ["docs/zh-CN/new.md", "A"],
      ["docs/ja/new.md", "A"],
    ]);
    assert.deepEqual(checkCoChange(changes), []);
  });

  it("catches a rename that left the old translations behind", () => {
    const changes = new Map([
      ["docs/old.md", "D"],
      ["docs/new.md", "A"],
      ["docs/zh-CN/new.md", "A"],
      ["docs/ja/new.md", "A"],
    ]);
    const failures = checkCoChange(changes);
    assert.equal(failures.length, 2);
    assert.ok(failures.every((failure) => failure.source === "docs/old.md"));
  });

  it("accepts any non-delete status when all three move together", () => {
    // git reports T for a typechange, such as all three files becoming symlinks.
    // Accepting only A and M would reject a correctly mirrored change, and a gate
    // that fails correct PRs gets switched off.
    const changes = new Map([
      ["docs/architecture.md", "T"],
      ["docs/zh-CN/architecture.md", "T"],
      ["docs/ja/architecture.md", "T"],
    ]);
    assert.deepEqual(checkCoChange(changes), []);
  });

  it("still requires a delete to be mirrored when the source is typechanged", () => {
    const changes = new Map([
      ["docs/architecture.md", "T"],
      ["docs/zh-CN/architecture.md", "T"],
      ["docs/ja/architecture.md", "D"],
    ]);
    assert.equal(checkCoChange(changes).length, 1);
  });

  it("ignores English-only documents", () => {
    assert.deepEqual(checkCoChange(new Map([["docs/postgresql-setup.md", "M"]])), []);
  });
});

describe("endpoint resolution", () => {
  it("reads pull_request payloads from the base and head SHAs", () => {
    // Deliberately not GITHUB_SHA: on a pull_request that is the synthetic merge
    // commit, not the branch head.
    const endpoints = resolveEndpoints("pull_request", {
      pull_request: { base: { sha: "aaa" }, head: { sha: "bbb" } },
    });
    assert.deepEqual(endpoints, { base: "aaa", head: "bbb" });
  });

  it("reads push payloads from before and after", () => {
    assert.deepEqual(resolveEndpoints("push", { before: "aaa", after: "bbb" }), {
      base: "aaa",
      head: "bbb",
    });
  });

  it("reads merge_group payloads", () => {
    const endpoints = resolveEndpoints("merge_group", {
      merge_group: { base_sha: "aaa", head_sha: "bbb" },
    });
    assert.deepEqual(endpoints, { base: "aaa", head: "bbb" });
  });

  it("returns null for unusable payloads rather than guessing", () => {
    assert.equal(resolveEndpoints("pull_request", {}), null);
    assert.equal(resolveEndpoints("push", { before: "aaa" }), null);
    assert.equal(resolveEndpoints("schedule", { before: "aaa", after: "bbb" }), null);
    assert.equal(resolveEndpoints("push", null), null);
  });

  it("recognizes the all-zero SHA of a newly created ref", () => {
    assert.equal(isNullSha("0".repeat(40)), true);
    assert.equal(isNullSha("aaa"), false);
    assert.equal(isNullSha(undefined), false);
  });
});

describe("name-status parsing", () => {
  it("parses NUL-delimited pairs", () => {
    const changes = parseNameStatus("M\0docs/a.md\0A\0docs/b.md\0D\0docs/c.md\0");
    assert.deepEqual(
      changes,
      new Map([
        ["docs/a.md", "M"],
        ["docs/b.md", "A"],
        ["docs/c.md", "D"],
      ]),
    );
  });

  it("keeps only the status letter when git appends a score", () => {
    assert.equal(parseNameStatus("M100\0docs/a.md\0").get("docs/a.md"), "M");
  });

  it("returns an empty map for empty output", () => {
    assert.equal(parseNameStatus("").size, 0);
  });
});

describe("checker end to end", () => {
  const repos = [];

  after(() => {
    for (const repo of repos) {
      rmSync(repo, { recursive: true, force: true });
    }
  });

  function run(cwd, args = [], env = {}) {
    try {
      const stdout = execFileSync(process.execPath, [checker, ...args], {
        cwd,
        encoding: "utf8",
        // A bare environment keeps a real CI run from leaking GITHUB_* variables into
        // these cases and silently changing which branch is exercised.
        env: { PATH: process.env.PATH, HOME: process.env.HOME, ...env },
      });
      return { code: 0, output: stdout };
    } catch (error) {
      return { code: error.status, output: `${error.stdout ?? ""}${error.stderr ?? ""}` };
    }
  }

  function write(repo, path, contents) {
    mkdirSync(join(repo, dirname(path)), { recursive: true });
    writeFileSync(join(repo, path), contents);
  }

  function newRepo() {
    const repo = mkdtempSync(join(tmpdir(), "tokenhub-i18n-"));
    repos.push(repo);
    execFileSync("git", ["init", "-q", "-b", "main"], { cwd: repo });
    execFileSync("git", ["config", "user.email", "test@example.com"], { cwd: repo });
    execFileSync("git", ["config", "user.name", "Test"], { cwd: repo });
    return repo;
  }

  function commit(repo, message) {
    execFileSync("git", ["add", "-A"], { cwd: repo });
    execFileSync("git", ["commit", "-q", "-m", message], { cwd: repo });
    return execFileSync("git", ["rev-parse", "HEAD"], { cwd: repo, encoding: "utf8" }).trim();
  }

  function seed(repo) {
    for (const path of [
      "docs/architecture.md",
      "docs/zh-CN/architecture.md",
      "docs/ja/architecture.md",
    ]) {
      write(repo, path, "v1\n");
    }
    return commit(repo, "seed");
  }

  it("passes when all three documents change together", () => {
    const repo = newRepo();
    const base = seed(repo);
    for (const path of [
      "docs/architecture.md",
      "docs/zh-CN/architecture.md",
      "docs/ja/architecture.md",
    ]) {
      write(repo, path, "v2\n");
    }
    const head = commit(repo, "update all three");

    const result = run(repo, ["--base", base, "--head", head]);
    assert.equal(result.code, 0, result.output);
  });

  it("fails when only the English document changes", () => {
    const repo = newRepo();
    const base = seed(repo);
    write(repo, "docs/architecture.md", "v2\n");
    const head = commit(repo, "update English only");

    const result = run(repo, ["--base", base, "--head", head]);
    assert.equal(result.code, 1);
    assert.match(result.output, /docs\/zh-CN\/architecture\.md was not touched/);
    assert.match(result.output, /docs\/ja\/architecture\.md was not touched/);
  });

  it("fails on a new English document with no translations", () => {
    const repo = newRepo();
    seed(repo);
    write(repo, "docs/newthing.md", "v1\n");
    commit(repo, "add untranslated doc");

    const result = run(repo, ["--existence-only"]);
    assert.equal(result.code, 1);
    assert.match(result.output, /\[existence\]/);
    assert.match(result.output, /docs\/newthing\.md has no zh-CN translation/);
  });

  it("ignores documents that are deliberately English-only", () => {
    const repo = newRepo();
    const base = seed(repo);
    write(repo, "docs/postgresql-setup.md", "english only\n");
    const head = commit(repo, "add English-only doc");

    const result = run(repo, ["--base", base, "--head", head]);
    assert.equal(result.code, 0, result.output);
  });

  it("treats an all-zero base as a new ref and diffs against the empty tree", () => {
    const repo = newRepo();
    const head = seed(repo);

    const result = run(repo, ["--base", "0".repeat(40), "--head", head]);
    // Everything counts as added, and the seed adds all three together.
    assert.equal(result.code, 0, result.output);
    assert.match(result.output, new RegExp(EMPTY_TREE.slice(0, 7)));
  });

  it("fails closed under CI when the base commit is missing", () => {
    const repo = newRepo();
    const head = seed(repo);

    const result = run(repo, ["--base", "0".repeat(39) + "1", "--head", head], {
      CI: "true",
    });
    assert.equal(result.code, 1);
    assert.match(result.output, /not present in this checkout/);
    assert.match(result.output, /fetch-depth: 0/);
  });

  it("fails closed under CI when the event payload yields no endpoints", () => {
    const repo = newRepo();
    seed(repo);

    const result = run(repo, [], { CI: "true", GITHUB_EVENT_NAME: "schedule" });
    assert.equal(result.code, 1);
    assert.match(result.output, /Failing closed/);
  });

  it("fails closed when CI is set to 1 rather than true", () => {
    // Comparing against the literal "true" made this branch unreachable on every
    // runner that sets CI=1, which is most of them.
    const repo = newRepo();
    seed(repo);

    const result = run(repo, [], { CI: "1", GITHUB_EVENT_NAME: "schedule" });
    assert.equal(result.code, 1);
    assert.match(result.output, /Failing closed/);
  });

  it("reports a clean diagnostic when git is unavailable", () => {
    const repo = newRepo();
    seed(repo);

    const result = run(repo, [], { PATH: "/nonexistent" });
    assert.equal(result.code, 1);
    assert.match(result.output, /could not run/);
    assert.equal(/at .*node:internal/.test(result.output), false, "no raw stack trace");
  });

  it("degrades to the existence rule locally when there is no base", () => {
    const repo = newRepo();
    seed(repo);

    const result = run(repo, []);
    assert.equal(result.code, 0, result.output);
    assert.match(result.output, /co-change rule was skipped/);
  });
});
