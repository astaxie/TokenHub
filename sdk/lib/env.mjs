// Shared helpers for the SDK smoke tests.
//
// The three smoke test scripts each carried a byte-identical copy of this .env loader.
// Node's built-in `--env-file` is not a drop-in replacement here: it aborts when the
// file is missing, and `--env-file-if-exists` needs a newer runtime than the `>=20`
// this package declares.

import { existsSync, readFileSync } from "node:fs";

/**
 * Loads `path` into process.env without overwriting anything already set, and returns
 * silently when the file does not exist so the scripts stay runnable from env vars
 * alone. `path` is resolved by the caller, keeping the lookup independent of cwd.
 */
export function loadDotEnv(path) {
  if (!existsSync(path)) return;
  const lines = readFileSync(path, "utf8").split(/\r?\n/);
  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith("#")) continue;
    const eq = trimmed.indexOf("=");
    if (eq < 0) continue;
    const key = trimmed.slice(0, eq).trim();
    const raw = trimmed.slice(eq + 1).trim();
    if (!key || process.env[key] !== undefined) continue;
    process.env[key] = stripEnvQuotes(raw);
  }
}

export function stripEnvQuotes(value) {
  if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
    return value.slice(1, -1);
  }
  return value;
}

/** Renders a credential for logging: enough to identify it, not enough to reuse it. */
export function maskKey(value) {
  if (value.length <= 10) return "***";
  return `${value.slice(0, 6)}...${value.slice(-4)}`;
}
