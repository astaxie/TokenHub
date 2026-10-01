import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import { join, relative } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const sourceRoots = ["frontend/app", "frontend/features", "frontend/lib"];

async function sourceFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = await Promise.all(
    entries.map((entry) => {
      const path = join(directory, entry.name);
      if (entry.isDirectory()) return entry.name === "node_modules" ? [] : sourceFiles(path);
      return /\.(css|tsx?)$/.test(entry.name) ? [path] : [];
    }),
  );
  return files.flat();
}

test("every custom property read without a fallback is defined", async () => {
  const files = (await Promise.all(sourceRoots.map((dir) => sourceFiles(join(root, dir))))).flat();
  const defined = new Set();
  const reads = [];

  for (const file of files) {
    const source = await readFile(file, "utf8");
    // Stylesheet declarations and inline style objects ("--name": value) in components.
    for (const match of source.matchAll(/(?:^|[\s{;"'])(--[\w-]+)["']?\s*:/gm)) defined.add(match[1]);
    for (const match of source.matchAll(/setProperty\(\s*["'](--[\w-]+)["']/g)) defined.add(match[1]);
    for (const match of source.matchAll(/var\(\s*(--[\w-]+)\s*\)/g)) {
      reads.push({ name: match[1], file: relative(root, file) });
    }
  }

  const undefinedReads = reads.filter((read) => !defined.has(read.name)).map((read) => `${read.name} in ${read.file}`);
  // Without a fallback, an undefined property makes the whole declaration invalid, so the
  // color or border silently disappears instead of failing loudly.
  assert.deepEqual([...new Set(undefinedReads)].sort(), []);
});
