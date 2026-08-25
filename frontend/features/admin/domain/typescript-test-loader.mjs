import { mkdir, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { dirname, extname, join, relative, resolve } from "node:path";
import { tmpdir } from "node:os";
import { fileURLToPath, pathToFileURL } from "node:url";
import ts from "typescript";

export async function importTypeScript(moduleURL) {
  const fileName = fileURLToPath(moduleURL);
  const rootDir = dirname(fileName);
  const outputDir = await mkdtemp(join(tmpdir(), "tokenhub-ts-test-"));
  const emitted = await emitTypeScriptModule(fileName, rootDir, outputDir, new Map());
  return import(pathToFileURL(emitted).href);
}

async function emitTypeScriptModule(fileName, rootDir, outputDir, emitted) {
  const normalizedName = resolve(fileName);
  const cached = emitted.get(normalizedName);
  if (cached) return cached;
  const outputName = emittedModulePath(normalizedName, rootDir, outputDir);
  emitted.set(normalizedName, outputName);
  const source = await readFile(normalizedName, "utf8");
  const result = ts.transpileModule(source, {
    fileName: normalizedName,
    compilerOptions: {
      module: ts.ModuleKind.ESNext,
      target: ts.ScriptTarget.ES2022,
    },
    reportDiagnostics: true,
  });
  const errors = result.diagnostics?.filter((diagnostic) => diagnostic.category === ts.DiagnosticCategory.Error) ?? [];
  if (errors.length > 0) {
    throw new Error(ts.formatDiagnostics(errors, {
      getCanonicalFileName: (name) => name,
      getCurrentDirectory: () => "",
      getNewLine: () => "\n",
    }));
  }
  const outputText = await rewriteRelativeImports(result.outputText, normalizedName, rootDir, outputDir, emitted);
  await mkdir(dirname(outputName), { recursive: true });
  await writeFile(outputName, outputText);
  return outputName;
}

async function rewriteRelativeImports(outputText, fileName, rootDir, outputDir, emitted) {
  const importPattern = /\bfrom\s+["'](\.[^"']*)["']|import\s*\(\s*["'](\.[^"']*)["']\s*\)/g;
  const replacements = [];
  for (const match of outputText.matchAll(importPattern)) {
    const specifier = match[1] ?? match[2];
    const dependency = resolveTypeScriptImport(dirname(fileName), specifier);
    if (!dependency) continue;
    const dependencyOutput = await emitTypeScriptModule(dependency, rootDir, outputDir, emitted);
    let rewritten = relative(dirname(emittedModulePath(fileName, rootDir, outputDir)), dependencyOutput);
    if (!rewritten.startsWith(".")) rewritten = `./${rewritten}`;
    replacements.push({ start: match.index + match[0].indexOf(specifier), end: match.index + match[0].indexOf(specifier) + specifier.length, value: rewritten });
  }
  let next = outputText;
  for (const replacement of replacements.reverse()) {
    next = `${next.slice(0, replacement.start)}${replacement.value}${next.slice(replacement.end)}`;
  }
  return next;
}

function resolveTypeScriptImport(baseDir, specifier) {
  const resolved = resolve(baseDir, specifier);
  if (extname(resolved)) return resolved;
  return `${resolved}.ts`;
}

function emittedModulePath(fileName, rootDir, outputDir) {
  const relativeName = relative(rootDir, fileName).replace(/\.[^.]+$/, ".mjs");
  return join(outputDir, relativeName);
}
