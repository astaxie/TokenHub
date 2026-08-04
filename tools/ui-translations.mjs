import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

const sourceExtension = /\.[cm]?[jt]sx?$/;
const literalTxCall = /\btx\(\s*("(?:\\.|[^"\\])*")/g;
const literalProperty = /(?:^|[,{]\s*)("(?:\\.|[^"\\])*")\s*:/gm;

export function sourceFiles(root) {
  return readdirSync(root, { withFileTypes: true }).flatMap((entry) => {
    const path = join(root, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === "node_modules" || entry.name === ".next") return [];
      return sourceFiles(path);
    }
    return sourceExtension.test(entry.name) ? [path] : [];
  });
}

export function literalTxKeys(source) {
  return Array.from(source.matchAll(literalTxCall), (match) => JSON.parse(match[1]));
}

export function literalPropertyKeys(source) {
  return Array.from(source.matchAll(literalProperty), (match) => JSON.parse(match[1]));
}

export function newLiteralTxKeys(before, after) {
  const existing = new Set(literalTxKeys(before));
  return Array.from(new Set(literalTxKeys(after))).filter((key) => !existing.has(key)).sort();
}

function localeObjectSource(source, locale) {
  const match = new RegExp(`\\b${locale}\\s*:\\s*\\{`).exec(source);
  if (!match) return "";
  const start = source.indexOf("{", match.index);
  let depth = 0;
  let quote = "";
  let escaped = false;
  let lineComment = false;
  let blockComment = false;
  for (let index = start; index < source.length; index += 1) {
    const character = source[index];
    const next = source[index + 1];
    if (lineComment) {
      if (character === "\n") lineComment = false;
      continue;
    }
    if (blockComment) {
      if (character === "*" && next === "/") {
        blockComment = false;
        index += 1;
      }
      continue;
    }
    if (quote) {
      if (escaped) escaped = false;
      else if (character === "\\") escaped = true;
      else if (character === quote) quote = "";
      continue;
    }
    if (character === "/" && next === "/") {
      lineComment = true;
      index += 1;
      continue;
    }
    if (character === "/" && next === "*") {
      blockComment = true;
      index += 1;
      continue;
    }
    if (character === '"' || character === "'" || character === "`") {
      quote = character;
      continue;
    }
    if (character === "{") depth += 1;
    if (character === "}") {
      depth -= 1;
      if (depth === 0) return source.slice(start, index + 1);
    }
  }
  throw new Error(`Unclosed ${locale} translation object`);
}

export function translationKeys(i18nRoot, locale) {
  const keys = new Set(literalPropertyKeys(readFileSync(join(i18nRoot, `${locale}.tsx`), "utf8")));
  for (const path of sourceFiles(i18nRoot)) {
    if (/\/(?:en|ja|runtime|translations)\.tsx$/.test(path)) continue;
    for (const key of literalPropertyKeys(localeObjectSource(readFileSync(path, "utf8"), locale))) keys.add(key);
  }
  return keys;
}

export function translationUsage(frontendRoot) {
  const usage = new Map();
  for (const path of sourceFiles(frontendRoot)) {
    const source = readFileSync(path, "utf8");
    for (const key of literalTxKeys(source)) {
      const paths = usage.get(key) ?? new Set();
      paths.add(path);
      usage.set(key, paths);
    }
  }
  return usage;
}

export function missingTranslations(frontendRoot, i18nRoot, locale) {
  const catalog = translationKeys(i18nRoot, locale);
  return Array.from(translationUsage(frontendRoot))
    .filter(([key]) => !catalog.has(key))
    .map(([key, paths]) => ({ key, paths: Array.from(paths).sort() }))
    .sort((left, right) => left.key.localeCompare(right.key));
}

export function missingKeys(keys, i18nRoot, locale) {
  const catalog = translationKeys(i18nRoot, locale);
  return Array.from(new Set(keys)).filter((key) => !catalog.has(key)).sort();
}
