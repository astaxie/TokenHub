#!/usr/bin/env node
//
// Strips NUL (U+0000) characters from the HTML files emitted by
// `docusaurus build` and fails if NUL characters leak into any other text
// asset of the build output.
//
// Why this is needed: Docusaurus 3 renders static pages with
// `renderToPipeableStream` from react-dom 18 (see
// @docusaurus/core/lib/client/renderToHtml.js). That renderer emits U+0000
// as a marker inside text it splits across stream chunks
// (https://github.com/react/react/issues/31134, closed as not planned).
// The marker bytes end up in the written HTML, where they corrupt heading
// `id` attributes (the table-of-contents links stay clean), so in-page
// anchors stop matching and tooltips render garbage.
//
// Removing U+0000 joins the split text nodes back into exactly the source
// text, which makes the emitted ids and anchors match again. NUL characters
// never legitimately occur in the site's text output, so the check is a
// cheap regression gate for any new leak path.

import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const buildDir = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'build');

const STRIP_EXTENSIONS = new Set(['.html']);
// Text formats that must never contain NUL; report a failure instead of
// stripping so a new renderer leak is caught instead of hidden.
const CHECK_EXTENSIONS = new Set([
  '.js',
  '.json',
  '.css',
  '.md',
  '.txt',
  '.xml',
  '.webmanifest',
  '.map',
]);
const BINARY_EXTENSIONS = new Set([
  '.png',
  '.jpg',
  '.jpeg',
  '.gif',
  '.ico',
  '.svg',
  '.woff',
  '.woff2',
  '.ttf',
  '.eot',
]);

function walk(dir, results) {
  for (const entry of readdirSync(dir)) {
    const fullPath = join(dir, entry);
    if (statSync(fullPath).isDirectory()) {
      walk(fullPath, results);
    } else {
      results.push(fullPath);
    }
  }
  return results;
}

function countNullCharacters(content) {
  return content.split('\u0000').length - 1;
}

function main() {
  const files = walk(buildDir, []);
  let strippedFiles = 0;
  let strippedCharacters = 0;

  for (const file of files) {
    const extension = file.slice(file.lastIndexOf('.'));
    if (BINARY_EXTENSIONS.has(extension)) {
      continue;
    }
    if (!STRIP_EXTENSIONS.has(extension) && !CHECK_EXTENSIONS.has(extension)) {
      continue;
    }
    const content = readFileSync(file, 'utf8');
    if (!content.includes('\u0000')) {
      continue;
    }
    if (STRIP_EXTENSIONS.has(extension)) {
      const count = countNullCharacters(content);
      writeFileSync(file, content.replaceAll('\u0000', ''));
      strippedFiles += 1;
      strippedCharacters += count;
    } else {
      console.error(
        `sanitize-build: NUL character in ${relative(buildDir, file)}; extend the sanitizer if this file type must be cleaned.`,
      );
      process.exitCode = 1;
    }
  }

  // Re-scan everything (HTML included): the build only passes when the
  // output is verifiably free of NUL characters.
  let remaining = 0;
  for (const file of files) {
    const extension = file.slice(file.lastIndexOf('.'));
    if (BINARY_EXTENSIONS.has(extension)) {
      continue;
    }
    if (!STRIP_EXTENSIONS.has(extension) && !CHECK_EXTENSIONS.has(extension)) {
      continue;
    }
    if (readFileSync(file, 'utf8').includes('\u0000')) {
      console.error(
        `sanitize-build: NUL character remains in ${relative(buildDir, file)}`,
      );
      remaining += 1;
    }
  }

  const relativeBuild = relative(process.cwd(), buildDir);
  if (process.exitCode || remaining > 0) {
    console.error(
      `sanitize-build: NUL characters remain in ${relativeBuild}; see https://github.com/react/react/issues/31134`,
    );
    process.exitCode = 1;
  } else if (strippedFiles > 0) {
    console.log(
      `sanitize-build: stripped ${strippedCharacters} NUL character(s) from ` +
        `${strippedFiles} HTML file(s) in ${relativeBuild} ` +
        '(react-dom renderToPipeableStream markers, ' +
        'see https://github.com/react/react/issues/31134)',
    );
  } else {
    console.log(`sanitize-build: no NUL characters found in ${relativeBuild}`);
  }
}

main();
