// Translation co-change rules for TokenHub documentation.
//
// AGENTS.md requires user-facing documentation to stay synchronized across English,
// Simplified Chinese and Japanese. This module holds the pure decision logic; the
// git and filesystem access lives in check-doc-translations.mjs so the rules can be
// unit tested without a repository.
//
// Two independent rules are enforced. Neither subsumes the other:
//
//   existence  every tracked English document has both counterparts on disk. Catches
//              a new document added with no translations.
//   co-change  a tracked English document touched in the diff requires its
//              counterparts to be touched the same way. Catches an English edit whose
//              translations were left behind, a deletion leaving orphan translations,
//              and a rename that left the old translation paths in place.
//
// Neither rule can judge translation quality. That stays a reviewer's job; the gate
// only guarantees the files moved together.

export const LOCALES = ["zh-CN", "ja"];

// Documents that are deliberately English-only. These are inside the tracked universe
// and are skipped on purpose, which is different from a path simply not being tracked.
const ENGLISH_ONLY = ["docs/development/", "docs/postgresql-setup.md"];

// Root documents that translate to a sibling with a locale suffix rather than into a
// locale directory.
const ROOT_DOCUMENTS = ["README.md", "CONTRIBUTING.md"];

/**
 * The tracked universe is defined positively: the two root documents plus the English
 * tree under docs/. Defining it as "every English .md in the repository" would sweep in
 * files such as sdk/README.md that have no counterpart and no sensible one.
 */
export function isTrackedEnglishDoc(path) {
  if (ROOT_DOCUMENTS.includes(path)) {
    return true;
  }
  if (!path.startsWith("docs/") || !path.endsWith(".md")) {
    return false;
  }
  // The locale trees are translation targets, never sources.
  return LOCALES.every((locale) => !path.startsWith(`docs/${locale}/`));
}

export function isEnglishOnly(path) {
  return ENGLISH_ONLY.some((entry) =>
    entry.endsWith("/") ? path.startsWith(entry) : path === entry,
  );
}

/** Paths this gate has an opinion about: tracked, and not deliberately English-only. */
export function requiresTranslation(path) {
  return isTrackedEnglishDoc(path) && !isEnglishOnly(path);
}

/** Maps an English document to its counterpart path in each locale. */
export function counterpartsFor(path) {
  const counterparts = new Map();
  for (const locale of LOCALES) {
    if (ROOT_DOCUMENTS.includes(path)) {
      counterparts.set(locale, path.replace(/\.md$/, `.${locale}.md`));
    } else {
      counterparts.set(locale, path.replace(/^docs\//, `docs/${locale}/`));
    }
  }
  return counterparts;
}

/**
 * Rule 1. `existingPaths` is the set of repository-relative paths present on disk.
 */
export function checkExistence(englishDocs, existingPaths) {
  const failures = [];
  for (const doc of englishDocs) {
    if (!requiresTranslation(doc)) {
      continue;
    }
    for (const [locale, counterpart] of counterpartsFor(doc)) {
      if (!existingPaths.has(counterpart)) {
        failures.push({
          rule: "existence",
          source: doc,
          locale,
          counterpart,
          detail: `${doc} has no ${locale} translation at ${counterpart}`,
        });
      }
    }
  }
  return failures;
}

/**
 * A delete must be mirrored by a delete. An add or a modify may be answered by either,
 * because a translation that did not exist before is legitimately created as an add
 * while its English source is only modified.
 */
function statusSatisfies(englishStatus, counterpartStatus) {
  if (counterpartStatus === undefined) {
    return false;
  }
  if (englishStatus === "D") {
    return counterpartStatus === "D";
  }
  // Any non-delete status counts as touched. Listing A and M explicitly would reject a
  // fully mirrored change that git reports some other way — `T` when all three files
  // become symlinks, for example — and a gate that fails a correct PR gets disabled.
  return counterpartStatus !== "D";
}

function describeStatus(status) {
  switch (status) {
    case "A":
      return "added";
    case "M":
      return "modified";
    case "D":
      return "deleted";
    default:
      return status;
  }
}

/**
 * Rule 2. `changes` maps a repository-relative path to a single-letter git status.
 * Renames are expected to have been normalized to a delete plus an add by the caller
 * (`git diff --no-renames`): rename detection is a similarity heuristic, and a
 * translated file routinely scores differently from the English source it tracks, so
 * relying on it would make the gate's verdict depend on how close two languages look.
 */
export function checkCoChange(changes) {
  const failures = [];
  for (const [path, status] of changes) {
    if (!requiresTranslation(path)) {
      continue;
    }
    for (const [locale, counterpart] of counterpartsFor(path)) {
      const counterpartStatus = changes.get(counterpart);
      if (!statusSatisfies(status, counterpartStatus)) {
        failures.push({
          rule: "co-change",
          source: path,
          locale,
          counterpart,
          detail:
            counterpartStatus === undefined
              ? `${path} was ${describeStatus(status)} but ${counterpart} was not touched`
              : `${path} was ${describeStatus(status)} but ${counterpart} was ${describeStatus(counterpartStatus)}`,
        });
      }
    }
  }
  return failures;
}

/**
 * Resolves the diff endpoints from a GitHub Actions event payload. GITHUB_SHA is not
 * usable on a pull_request: it points at the synthetic merge commit rather than the
 * branch head, so the payload SHAs are read directly instead.
 *
 * Returns null when the event carries no usable endpoints, which the caller turns into
 * a hard failure under CI rather than a silent skip.
 */
export function resolveEndpoints(eventName, payload) {
  if (!payload) {
    return null;
  }
  switch (eventName) {
    case "pull_request":
    case "pull_request_target": {
      const base = payload.pull_request?.base?.sha;
      const head = payload.pull_request?.head?.sha;
      return base && head ? { base, head } : null;
    }
    case "push": {
      const { before, after } = payload;
      return before && after ? { base: before, head: after } : null;
    }
    case "merge_group": {
      const base = payload.merge_group?.base_sha;
      const head = payload.merge_group?.head_sha;
      return base && head ? { base, head } : null;
    }
    default:
      return null;
  }
}

/** git's empty tree object, used as the base when a ref is newly created. */
export const EMPTY_TREE = "4b825dc642cb6eb9a060e54bf8d69288fbee4904";

export function isNullSha(sha) {
  return typeof sha === "string" && /^0{40}$/.test(sha);
}

/** Parses the NUL-delimited output of `git diff --name-status -z --no-renames`. */
export function parseNameStatus(output) {
  const changes = new Map();
  const fields = output.split("\0").filter((field) => field !== "");
  for (let index = 0; index + 1 < fields.length; index += 2) {
    // Only the first letter matters; git appends a similarity score to some statuses.
    changes.set(fields[index + 1], fields[index].charAt(0));
  }
  return changes;
}
