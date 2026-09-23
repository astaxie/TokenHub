import { spawn } from "node:child_process";
import { readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import e2eDefaults from "./config.cjs";

const frontendDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const nextEnvPath = path.join(frontendDirectory, "next-env.d.ts");
const playwrightCLI = path.join(frontendDirectory, "node_modules/@playwright/test/cli.js");
const originalNextEnv = await readFile(nextEnvPath, "utf8");
const nextDistName = process.env.TOKENHUB_NEXT_DIST_DIR ?? e2eDefaults.nextDistDir;
if (!/^\.[a-zA-Z0-9._/-]+$/.test(nextDistName) || nextDistName.includes("..")) {
  throw new Error(`Unsafe TOKENHUB_NEXT_DIST_DIR: ${nextDistName}`);
}
if (nextDistName === ".next-e2e") {
  await rm(path.join(frontendDirectory, nextDistName), { recursive: true, force: true });
}
const preparedNextEnv = originalNextEnv.replace(
  /import "\.\/[^"]+\/(?:dev\/)?types\/routes\.d\.ts";/,
  `import "./${nextDistName}/dev/types/routes.d.ts";`,
);
await writeFile(nextEnvPath, preparedNextEnv);

const child = spawn(process.execPath, [playwrightCLI, "test", ...process.argv.slice(2)], {
  cwd: frontendDirectory,
  env: { ...process.env, TOKENHUB_NEXT_DIST_DIR: nextDistName },
  stdio: "inherit",
});

let result;
try {
  result = await new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => resolve({ code, signal }));
  });
} finally {
  if (nextDistName === ".next-e2e") {
    await rm(path.join(frontendDirectory, nextDistName), { recursive: true, force: true });
  }
  await writeFile(nextEnvPath, originalNextEnv);
}

if (result.signal) {
  process.kill(process.pid, result.signal);
} else {
  process.exitCode = result.code ?? 1;
}
