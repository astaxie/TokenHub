import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import ts from "typescript";

export async function importTypeScript(moduleURL) {
  const fileName = fileURLToPath(moduleURL);
  const source = await readFile(moduleURL, "utf8");
  const result = ts.transpileModule(source, {
    fileName,
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
  return import(`data:text/javascript;base64,${Buffer.from(result.outputText).toString("base64")}`);
}
