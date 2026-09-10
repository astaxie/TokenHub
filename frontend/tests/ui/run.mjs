import { spawn } from "node:child_process";
import { open, readFile, unlink, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import configuration from "./config.cjs";
const { apiOrigin, distDirectory, outputDirectory } = configuration;

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const args = process.argv.slice(2);
const capture = args.includes("--capture");
const forwarded = args.filter(arg => arg !== "--capture");
if (forwarded.some(arg => arg === "-c" || arg.startsWith("-c=") || arg === "--config" || arg.startsWith("--config="))) {
  throw new Error("The UI configuration is fixed; use a test file filter or --grep to select scenarios.");
}
const lockPath = path.join(frontend, ".ui-test.lock");
const lock = await open(lockPath, "wx").catch(error => {
  if (error.code === "EEXIST") throw new Error("A UI run already owns .ui-test.lock. Wait for it to finish; remove a stale lock only after checking no UI runner is active.");
  throw error;
});
const nextEnvPath = path.join(frontend, "next-env.d.ts");
const routeImport = /^import "\.\/[^"\n]+\/(?:dev\/)?types\/routes\.d\.ts";$/m;
let originalImport;
const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith("TOKENHUB_") && key !== "NEXT_PUBLIC_API_BASE_URL"));
Object.assign(environment, {
  TOKENHUB_UI_RUN: "1", TOKENHUB_UI_CAPTURE: capture ? "1" : "0",
  TOKENHUB_API_BASE_URL: apiOrigin, TOKENHUB_NEXT_DIST_DIR: distDirectory, NEXT_TELEMETRY_DISABLED: "1",
});
let child;
let interrupted = false;
function stop() { interrupted = true; child?.kill("SIGTERM"); }
process.on("SIGINT", stop);
process.on("SIGTERM", stop);
async function run(script, scriptArgs) {
  child = spawn(process.execPath, [path.join(frontend, script), ...scriptArgs], { cwd: frontend, env: environment, stdio: "inherit" });
  return new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => resolve(signal ? 1 : code ?? 1));
  });
}
try {
  await lock.writeFile(String(process.pid));
  originalImport = (await readFile(nextEnvPath, "utf8")).match(routeImport)?.[0];
  const inspectOnly = forwarded.includes("--list") || forwarded.includes("--help");
  if (capture && !inspectOnly) {
    for (const file of ["index.html", "manifest.json"]) {
      await unlink(path.join(frontend, outputDirectory, file)).catch(error => { if (error.code !== "ENOENT") throw error; });
    }
  }
  const built = inspectOnly ? 0 : await run("node_modules/next/dist/bin/next", ["build"]);
  process.exitCode = built === 0 && !interrupted
    ? await run("node_modules/@playwright/test/cli.js", ["test", "--config=playwright.ui.config.mts", ...forwarded])
    : built || 1;
} finally {
  // Restore only the generated import, preserving unrelated concurrent edits.
  try {
    if (originalImport) {
      const current = await readFile(nextEnvPath, "utf8");
      if (current.match(routeImport)?.[0]?.includes(`./${distDirectory}/`)) {
        await writeFile(nextEnvPath, current.replace(routeImport, originalImport));
      }
    }
  } finally {
    await lock.close();
    await unlink(lockPath);
    process.off("SIGINT", stop);
    process.off("SIGTERM", stop);
  }
}
