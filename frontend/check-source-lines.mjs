#!/usr/bin/env node

import { readdir, readFile } from "node:fs/promises";
import { extname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const frontendRoot = fileURLToPath(new URL(".", import.meta.url));
const sourceRoots = ["app", "features", "lib"];
const sourceExtensions = new Set([".css", ".ts", ".tsx"]);
const maximumLines = 1500;
const legacyLineBaselines = new Map([
  ["app/styles/legacy/resources.css", 1510],
  ["features/admin/i18n/en.tsx", 1540],
  ["features/admin/i18n/ja.tsx", 1526],
  ["features/admin/views/provider-editor.tsx", 2093],
]);

const sourceFiles = [];
for (const sourceRoot of sourceRoots) {
  await collectSourceFiles(join(frontendRoot, sourceRoot), sourceFiles);
}

const violations = [];
for (const sourceFile of sourceFiles) {
  const contents = await readFile(sourceFile, "utf8");
  const lineCount = contents === "" ? 0 : contents.split(/\r?\n/).length;
  const relativePath = relative(frontendRoot, sourceFile);
  const allowedLines = legacyLineBaselines.get(relativePath) ?? maximumLines;
  if (lineCount > allowedLines) {
    violations.push({
      file: relativePath,
      lineCount,
      allowedLines,
    });
  }
}

if (violations.length > 0) {
  console.error(`Frontend source files exceeded their line limit:`);
  for (const violation of violations) {
    console.error(
      `- ${violation.file}: ${violation.lineCount} lines (limit ${violation.allowedLines})`,
    );
  }
  process.exitCode = 1;
} else {
  console.log(
    `Source line check passed (${sourceFiles.length} files, default limit ${maximumLines}, ${legacyLineBaselines.size} fixed baselines).`,
  );
}

async function collectSourceFiles(directory, output) {
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await collectSourceFiles(path, output);
      continue;
    }
    if (entry.isFile() && sourceExtensions.has(extname(entry.name))) {
      output.push(path);
    }
  }
}
