import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import e2eDefaults from "./config.cjs";

const frontendDirectory = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repositoryDirectory = path.resolve(frontendDirectory, "..");
const backendDirectory = path.join(repositoryDirectory, "backend");
const temporaryDirectory = await mkdtemp(path.join(tmpdir(), "tokenhub-browser-smoke-"));
const frontendPort = Number(process.env.TOKENHUB_E2E_FRONTEND_PORT ?? e2eDefaults.frontendPort);
const backendPort = Number(process.env.TOKENHUB_E2E_BACKEND_PORT ?? e2eDefaults.backendPort);
const upstreamPort = Number(process.env.TOKENHUB_E2E_UPSTREAM_PORT ?? e2eDefaults.upstreamPort);
const frontendURL = `http://127.0.0.1:${frontendPort}`;
const backendURL = `http://127.0.0.1:${backendPort}`;
const upstreamURL = `http://127.0.0.1:${upstreamPort}`;
const nextDistName = process.env.TOKENHUB_NEXT_DIST_DIR ?? e2eDefaults.nextDistDir;
const children = [];
let ready = false;
let shuttingDown = false;

function start(name, command, args, options) {
  const child = spawn(command, args, {
    ...options,
    stdio: "inherit",
  });
  child.name = name;
  children.push(child);
  child.once("exit", (code, signal) => {
    if (!shuttingDown && (ready || code !== 0)) {
      process.stderr.write(`[e2e stack] ${name} exited unexpectedly (${signal ?? code})\n`);
      void shutdown(1);
    }
  });
  child.once("error", (error) => {
    process.stderr.write(`[e2e stack] could not start ${name}: ${error.message}\n`);
    void shutdown(1);
  });
  return child;
}

function signal(child, signal) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  try {
    child.kill(signal);
  } catch (error) {
    if (error?.code !== "ESRCH") throw error;
  }
}

function waitForExit(child, timeoutMilliseconds) {
  if (child.exitCode !== null || child.signalCode !== null) return Promise.resolve();
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, timeoutMilliseconds);
    child.once("exit", () => {
      clearTimeout(timer);
      resolve();
    });
  });
}

async function shutdown(code) {
  if (shuttingDown) return;
  shuttingDown = true;
  for (const child of [...children].reverse()) signal(child, "SIGTERM");
  await Promise.all(children.map((child) => waitForExit(child, 3_000)));
  for (const child of [...children].reverse()) signal(child, "SIGKILL");
  await rm(temporaryDirectory, { recursive: true, force: true });
  if (nextDistName === ".next-e2e") {
    await rm(path.join(frontendDirectory, nextDistName), { recursive: true, force: true });
  }
  process.exit(code);
}

async function waitForURL(name, url, timeoutMilliseconds = 180_000) {
  const deadline = Date.now() + timeoutMilliseconds;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
      lastError = new Error(`${response.status} ${response.statusText}`);
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`${name} did not become ready at ${url}: ${lastError?.message ?? "timeout"}`);
}

process.on("SIGINT", () => void shutdown(0));
process.on("SIGTERM", () => void shutdown(0));

try {
  const sharedEnvironment = {
    ...process.env,
    TOKENHUB_E2E_UPSTREAM_PORT: String(upstreamPort),
    TOKENHUB_E2E_UPSTREAM_KEY: process.env.TOKENHUB_E2E_UPSTREAM_KEY ?? e2eDefaults.upstreamKey,
  };

  start("fake upstream", process.execPath, ["e2e/fake-upstream.mjs"], {
    cwd: frontendDirectory,
    env: sharedEnvironment,
  });
  await waitForURL("fake upstream", `${upstreamURL}/healthz`);

  start("Go backend", "go", ["run", "./cmd/tokenhub"], {
    cwd: backendDirectory,
    env: {
      ...sharedEnvironment,
      TOKENHUB_ENV: "test",
      TOKENHUB_HTTP_ADDR: `127.0.0.1:${backendPort}`,
      TOKENHUB_PUBLIC_BASE_URL: backendURL,
      TOKENHUB_CORS_ALLOWED_ORIGINS: frontendURL,
      TOKENHUB_ADMIN_TOKEN: "e2e_admin_token_0000000000000000",
      TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD: process.env.TOKENHUB_E2E_ADMIN_PASSWORD ?? e2eDefaults.adminPassword,
      TOKENHUB_SECRET_KEY: "e2e_secret_key_00000000000000000",
      TOKENHUB_DATABASE_URL: `sqlite://${path.join(temporaryDirectory, "tokenhub.db")}`,
      TOKENHUB_SQLITE_BACKUP_DIR: path.join(temporaryDirectory, "backups"),
      TOKENHUB_IMAGE_STORAGE_DIR: path.join(temporaryDirectory, "images"),
      TOKENHUB_MODEL_CATALOG_FILE: path.join(repositoryDirectory, "data/model-catalog.yaml"),
      TOKENHUB_PROVIDER_CATALOG_FILE: path.join(repositoryDirectory, "data/provider-catalog.json"),
      TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK: "true",
      TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS: "127.0.0.1/32",
      TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS: "1",
      TOKENHUB_SEED_DEMO: "false",
    },
  });
  await waitForURL("Go backend", `${backendURL}/healthz`);

  start("Next.js frontend", "npm", ["run", "dev", "--", "--hostname", "127.0.0.1", "--port", String(frontendPort)], {
    cwd: frontendDirectory,
    env: {
      ...sharedEnvironment,
      NEXT_TELEMETRY_DISABLED: "1",
      TOKENHUB_NEXT_DIST_DIR: nextDistName,
      TOKENHUB_API_BASE_URL: backendURL,
    },
  });
  await waitForURL("Next.js frontend", frontendURL);
  ready = true;
  process.stdout.write(`[e2e stack] ready at ${frontendURL}\n`);
} catch (error) {
  process.stderr.write(`[e2e stack] startup failed: ${error instanceof Error ? error.stack : error}\n`);
  await shutdown(1);
}

await new Promise(() => {});
