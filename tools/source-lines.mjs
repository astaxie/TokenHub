// Line-count ceiling for TokenHub source files.
//
// This is a growth alarm, not an architectural remedy. Freezing a 7,500-line file does
// not make it good; it buys time for a real package split by making sure the file does
// not quietly get worse first. `--update` can only ever lower a frozen entry; raising
// one is a deliberate hand edit, so it shows up in a diff someone has to agree to.
//
// Counting is `contents.split(/\r?\n/).length`, which is `wc -l` plus one for a file
// ending in a newline. One definition is used everywhere so a baseline recorded by the
// checker always matches what the checker later measures.

export const DEFAULT_MAX_LINES = 1500;

export const SOURCE_ROOTS = [
  { root: "backend", extensions: [".go"] },
  { root: "frontend/app", extensions: [".css", ".ts", ".tsx"] },
  { root: "frontend/features", extensions: [".css", ".ts", ".tsx"] },
  { root: "frontend/lib", extensions: [".css", ".ts", ".tsx"] },
];

export const SKIP_DIRECTORIES = new Set([
  "node_modules",
  ".next",
  ".git",
  "data",
  "vendor",
]);

/**
 * Files already over the ceiling when the gate landed. Test files are deliberately
 * included rather than exempted, matching the stance .golangci.yml already takes on
 * errcheck in tests: a 4,900-line test file is as hard to review as a 4,900-line
 * source file.
 *
 * Raising an entry is deliberately not something `--update` can do. It only lowers,
 * so every increase lands as a visible diff a reviewer has to agree to — which is the
 * whole point of a ratchet. Every entry below records the size the file has on `main`,
 * which this branch has had to re-measure twice: once for the image generation feature
 * and again for the project workspace, native release and version update work that
 * merged while this gate was in review. `frontend/features/admin/i18n/ja.tsx` is frozen
 * for the first time here — it crossed 1,500 lines on `main`, where the frontend-only
 * checker this gate absorbs had already recorded it.
 *
 * Every baseline here is above DEFAULT_MAX_LINES, and that is an invariant rather than
 * a coincidence. A frozen entry may only ever relax the default; once a file is split
 * back down to the default limit it graduates out of the table entirely, because the
 * default already covers it. `--update` removes graduated entries, and `allowedFor`
 * floors any baseline at the default so a hand-edited low number cannot turn this
 * table into a stricter-than-default limit on one file.
 */
export const FROZEN = new Map([
  ["frontend/app/styles/legacy/resources.css", 1508],
  ["frontend/features/admin/views/provider-editor.tsx", 2006],
]);

export function countLines(contents) {
  return contents.split(/\r?\n/).length;
}

export function allowedFor(path, frozen = FROZEN) {
  const baseline = frozen.get(path);
  if (baseline === undefined) return DEFAULT_MAX_LINES;
  // A frozen entry exists to relax the default, never to tighten it. A baseline that
  // somehow fell below the default is stale bookkeeping, not a stricter rule for that
  // one file, so it must not fail a change the default would have allowed.
  return Math.max(baseline, DEFAULT_MAX_LINES);
}

/**
 * `measured` maps a repository-relative path to its line count.
 */
export function findViolations(measured, frozen = FROZEN) {
  const violations = [];
  for (const [path, lines] of measured) {
    const allowed = allowedFor(path, frozen);
    if (lines > allowed) {
      violations.push({ path, lines, allowed, frozen: frozen.has(path) });
    }
  }
  return violations.sort((left, right) => left.path.localeCompare(right.path));
}

/**
 * Baselines that shrank and can be tightened. `--update` exists only to record this:
 * it never adds an entry and never raises one, because a checker that can bless any
 * number on request is just a slower way of deleting the gate.
 *
 * Three reasons, all of which only ever tighten: "gone" for a deleted file, "graduated"
 * for a file that now fits under DEFAULT_MAX_LINES and so no longer needs an exemption,
 * and "shrank" for one that is smaller than its baseline but still over the default.
 */
export function findTightenable(measured, frozen = FROZEN) {
  const tightenable = [];
  for (const [path, baseline] of frozen) {
    const lines = measured.get(path);
    if (lines === undefined) {
      tightenable.push({ path, lines: null, baseline, reason: "gone" });
    } else if (lines <= DEFAULT_MAX_LINES) {
      tightenable.push({ path, lines, baseline, reason: "graduated" });
    } else if (lines < baseline) {
      tightenable.push({ path, lines, baseline, reason: "shrank" });
    }
  }
  return tightenable.sort((left, right) => left.path.localeCompare(right.path));
}

/** Serializes a frozen map back into the source form of this module. */
export function renderFrozen(entries) {
  const lines = [...entries]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([path, count]) => `  [${JSON.stringify(path)}, ${count}],`);
  return `export const FROZEN = new Map([\n${lines.join("\n")}\n]);`;
}
