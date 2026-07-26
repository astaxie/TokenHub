// Line-count ceiling for TokenHub source files.
//
// This is a growth alarm, not an architectural remedy. Freezing a 7,500-line file does
// not make it good; it buys time for a real package split by making sure the file does
// not quietly get worse first. A frozen entry can only ever be lowered.
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
 */
export const FROZEN = new Map([
  ["backend/internal/server/http.go", 7579],
  ["backend/internal/server/store.go", 5094],
  ["backend/internal/server/http_test.go", 4944],
  ["backend/internal/server/anthropic_messages.go", 1504],
  ["frontend/features/admin/views/provider-editor.tsx", 2029],
  ["frontend/app/styles/legacy/resources.css", 1510],
]);

export function countLines(contents) {
  return contents.split(/\r?\n/).length;
}

export function allowedFor(path, frozen = FROZEN) {
  return frozen.get(path) ?? DEFAULT_MAX_LINES;
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
 */
export function findTightenable(measured, frozen = FROZEN) {
  const tightenable = [];
  for (const [path, baseline] of frozen) {
    const lines = measured.get(path);
    if (lines === undefined) {
      tightenable.push({ path, lines: null, baseline, reason: "gone" });
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
