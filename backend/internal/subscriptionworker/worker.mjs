import { AsyncLocalStorage } from "node:async_hooks";
import { once } from "node:events";
import { createInterface } from "node:readline";
import { createHost } from "./host.mjs";
import { installIsolation } from "./isolation.mjs";
import { createNetworkRelay } from "./network.mjs";

// stdout is exclusively the private transport. Upstream diagnostics may contain
// sensitive data, so they are deliberately not forwarded to application logs.
for (const method of ["log", "info", "warn", "error", "debug"]) console[method] = () => {};
const scope = new AsyncLocalStorage();
let writing = Promise.resolve();
function emit(value) {
  const task = writing.then(async () => {
    if (!process.stdout.write(JSON.stringify(value) + "\n")) await once(process.stdout, "drain");
  });
  writing = task.catch(() => {});
  return task;
}
const relayCallback = installIsolation(scope);
const network = createNetworkRelay(scope, emit);
globalThis.fetch = network.fetch;
const host = createHost({ scope, emit, relayCallback });
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity });
lines.on("line", (line) => {
  if (Buffer.byteLength(line) > 32 * 1024 * 1024) { process.exitCode = 1; lines.close(); return; }
  let message;
  try { message = JSON.parse(line); } catch { process.exitCode = 1; lines.close(); return; }
  if (!message || typeof message !== "object" || Array.isArray(message)) return;
  if (String(message.operation).startsWith("network.")) network.receive(message);
  else void host.dispatch(message);
});
lines.on("close", () => { void writing.finally(() => process.exit(process.exitCode || 0)); });
