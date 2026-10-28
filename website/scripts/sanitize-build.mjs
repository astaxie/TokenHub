#!/usr/bin/env node
//
// Strips NUL (U+0000) characters from the HTML files emitted by
// `docusaurus build`, fails if NUL characters leak into any other text
// asset of the build output, and validates that every anchor fragment in
// the generated search indexes resolves to an id that exists in the built
// page.
//
// Why this is needed: Docusaurus 3 renders static pages with
// `renderToPipeableStream` from react-dom 18 (see
// @docusaurus/core/lib/client/renderToHtml.js). That renderer emits U+0000
// as a marker inside text it splits across stream chunks
// (https://github.com/react/react/issues/31134, closed as not planned).
// The marker bytes end up in the written HTML, where they corrupt heading
// `id` attributes and hash-link hrefs (the table-of-contents links stay
// clean), so in-page anchors stop matching and tooltips render garbage.
//
// Removing U+0000 joins the split text nodes back into exactly the source
// text, which makes the emitted ids and anchors match again. NUL characters
// never legitimately occur in the site's text output, so the check is a
// cheap regression gate for any new leak path.
//
// The search plugin builds its index from the emitted HTML; its patched
// parser (see patches/@easyops-cn+docusaurus-search-local+0.55.3.patch)
// strips the markers itself before parsing, so indexed anchors are built
// from clean text. The fragment validation below asserts that contract:
// every `#fragment` stored in a search index must match an id attribute of
// the destination page, and no index text may carry U+FFFD — the signature
// of a NUL that survived into the index parser.

import { readdirSync, readFileSync, statSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const buildDir = resolve(dirname(fileURLToPath(import.meta.url)), '..', 'build');

// Keep in sync with the baseUrl fallback in docusaurus.config.ts. Search
// documents store site-absolute URLs (for example
// "/TokenHub/zh-Hans/billing/agent-token-cost-api/"); stripping this prefix
// maps them to files under build/.
const baseUrl = process.env.DOCS_BASE_URL ?? '/TokenHub/';

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

function isSearchIndexFile(file) {
  return /^search-index(-[\da-f]+)?\.json$/.test(basename(file));
}

// Collects `id="..."` attribute values from a built page. Docusaurus emits
// double-quoted attributes; single quotes are accepted defensively.
function pageIds(html) {
  const ids = new Set();
  for (const match of html.matchAll(/\sid\s*=\s*"([^"]*)"/g)) {
    ids.add(match[1]);
  }
  for (const match of html.matchAll(/\sid\s*=\s*'([^']*)'/g)) {
    ids.add(match[1]);
  }
  return ids;
}

function decodeFragment(fragment) {
  try {
    return decodeURIComponent(fragment);
  } catch {
    return null;
  }
}

// Validates every anchor fragment stored in the search indexes against the
// ids of the built page it points at, and rejects U+FFFD anywhere in the
// indexed URLs and anchors (the cheerio conversion of a leaked NUL).
function validateSearchIndexes(files) {
  const indexFiles = files.filter(isSearchIndexFile);
  const pageIdCache = new Map();
  let checkedFragments = 0;
  let failures = 0;

  const idsForPage = (file) => {
    let ids = pageIdCache.get(file);
    if (!ids) {
      ids = pageIds(readFileSync(file, 'utf8'));
      pageIdCache.set(file, ids);
    }
    return ids;
  };

  for (const indexFile of indexFiles) {
    let data;
    try {
      data = JSON.parse(readFileSync(indexFile, 'utf8'));
    } catch (error) {
      console.error(
        `sanitize-build: cannot parse ${relative(buildDir, indexFile)}: ${error.message}`,
      );
      failures += 1;
      continue;
    }
    for (const entry of data) {
      for (const doc of entry.documents ?? []) {
        const { u, h } = doc;
        if (`${u ?? ''}${h ?? ''}`.includes('\uFFFD')) {
          console.error(
            `sanitize-build: U+FFFD in search document ${u}${h ?? ''} ` +
              `(${relative(buildDir, indexFile)}); the index parsed unsanitized HTML`,
          );
          failures += 1;
        }
        if (!h || !h.startsWith('#')) {
          continue;
        }
        if (!u || !u.startsWith(baseUrl)) {
          console.error(
            `sanitize-build: search document URL ${u} does not start with ` +
              `baseUrl ${baseUrl}; update DOCS_BASE_URL for this build`,
          );
          failures += 1;
          continue;
        }
        const pageFile = join(
          buildDir,
          u.slice(baseUrl.length).replace(/\/?$/, '/'),
          'index.html',
        );
        let ids;
        try {
          ids = idsForPage(pageFile);
        } catch {
          console.error(
            `sanitize-build: search document ${u} has no built page at ` +
              `${relative(buildDir, pageFile)}`,
          );
          failures += 1;
          continue;
        }
        const fragment = h.slice(1);
        checkedFragments += 1;
        const decoded = decodeFragment(fragment);
        if (!ids.has(fragment) && !(decoded !== null && ids.has(decoded))) {
          console.error(
            `sanitize-build: search fragment ${h} of ${u} ` +
              `(${relative(buildDir, indexFile)}) matches no element id on the page`,
          );
          failures += 1;
        }
      }
    }
  }

  if (failures === 0) {
    console.log(
      `sanitize-build: validated ${checkedFragments} search-index fragment(s) ` +
        `across ${indexFiles.length} index file(s)`,
    );
  }
  return { checkedFragments, indexFiles: indexFiles.length, failures };
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

  const validation = validateSearchIndexes(files);

  const relativeBuild = relative(process.cwd(), buildDir);
  if (process.exitCode || remaining > 0 || validation.failures > 0) {
    if (validation.failures > 0) {
      console.error(
        `sanitize-build: ${validation.failures} search-index validation failure(s) in ${relativeBuild}`,
      );
    }
    console.error(
      `sanitize-build: output is not clean; see https://github.com/react/react/issues/31134`,
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
