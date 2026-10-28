import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { once } from "node:events";

const directory = dirname(fileURLToPath(import.meta.url));

export async function fixture(t, { network = () => ({ status: 404, body: "{}" }), rejectSave = false } = {}) {
  const privateHome = await mkdtemp(join(tmpdir(), "tokenhub-worker-test-"));
  const child = spawn(process.execPath, [join(directory, "worker.mjs")], {
    cwd: privateHome, env: { HOME: privateHome, USERPROFILE: privateHome, XDG_CONFIG_HOME: privateHome }, stdio: ["pipe", "pipe", "pipe"],
  });
  const calls = new Map();
  const requests = [];
  const cancellations = [];
  const saves = [];
  let serial = 0;
  let buffer = "";
  let stderr = "";
  child.stderr.on("data", (data) => { stderr += data; });
  const send = (value) => child.stdin.write(JSON.stringify(value) + "\n");
  child.stdout.on("data", (data) => {
    buffer += data;
    for (;;) {
      const end = buffer.indexOf("\n");
      if (end < 0) break;
      const event = JSON.parse(buffer.slice(0, end));
      buffer = buffer.slice(end + 1);
      if (event.event === "network.request") {
        requests.push(event);
        void Promise.resolve(network(event)).then((response) => {
          if (!response) return;
          const headers = response.headers ?? { "content-type": ["application/json"] };
          send({ operation: "network.response", request_id: event.request_id, status: response.status, headers });
          for (const chunk of response.chunks ?? [response.body ?? ""]) send({ operation: "network.chunk", request_id: event.request_id, data: Buffer.from(chunk).toString("base64") });
          send({ operation: "network.end", request_id: event.request_id });
        }).catch((error) => {
          calls.get(event.id)?.reject(error);
          send({ operation: "network.error", request_id: event.request_id });
        });
      } else if (event.event === "auth.update") {
        saves.push(event.auth);
        const rejected = typeof rejectSave === "function" ? rejectSave(event.auth, saves.length) : rejectSave;
        send({ operation: "auth.ack", update_id: event.update_id, ...(rejected ? { error: "save_failed" } : {}) });
      } else if (event.event === "network.cancel") {
        cancellations.push(event.request_id);
      } else {
        const pending = calls.get(event.id);
        if (!pending) continue;
        pending.events.push(event);
        if (["result", "end", "error"].includes(event.event)) {
          calls.delete(event.id);
          clearTimeout(pending.timer);
          pending.resolve({ ...event, events: pending.events });
        }
      }
    }
  });
  child.on("exit", () => {
    for (const call of calls.values()) call.reject(new Error("Worker exited unexpectedly: " + stderr));
  });
  t.after(async () => {
    child.stdin.end();
    if (child.exitCode === null) await once(child, "exit");
    await rm(privateHome, { recursive: true, force: true });
    assert.equal(stderr, "", "worker must not log credentials or vendor diagnostics");
  });
  function call(operation, fields = {}) {
    return new Promise((resolve, reject) => {
      const id = fields.id ?? String(++serial);
      const timer = setTimeout(() => reject(new Error("Worker command timed out: " + operation)), 10000);
      calls.set(id, { resolve, reject, events: [], timer });
      send({ id, operation, resource_id: "resource-test", provider: "grok", ...fields });
    });
  }
  return { call, requests, saves, cancellations, send };
}
