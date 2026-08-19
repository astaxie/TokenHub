import { createServer } from "node:http";
import e2eDefaults from "./config.cjs";

const host = process.env.TOKENHUB_E2E_UPSTREAM_HOST ?? "127.0.0.1";
const port = Number(process.env.TOKENHUB_E2E_UPSTREAM_PORT ?? e2eDefaults.upstreamPort);
const expectedKey = process.env.TOKENHUB_E2E_UPSTREAM_KEY ?? e2eDefaults.upstreamKey;

const server = createServer((request, response) => {
  if (request.method === "GET" && request.url === "/healthz") {
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({ ok: true }));
    return;
  }

  if (request.method === "GET" && request.url === "/v1/models") {
    if (request.headers.authorization !== `Bearer ${expectedKey}`) {
      response.writeHead(401, { "content-type": "application/json" });
      response.end(JSON.stringify({ error: { message: "invalid test credential" } }));
      return;
    }
    response.writeHead(200, { "content-type": "application/json" });
    response.end(JSON.stringify({
      object: "list",
      data: [{ id: "e2e-chat-model", object: "model", owned_by: "tokenhub-e2e" }],
    }));
    return;
  }

  response.writeHead(404, { "content-type": "application/json" });
  response.end(JSON.stringify({ error: { message: "not found" } }));
});

server.listen(port, host, () => {
  process.stdout.write(`[e2e upstream] listening on http://${host}:${port}\n`);
});

function shutdown() {
  server.close((error) => {
    if (error) {
      process.stderr.write(`[e2e upstream] shutdown failed: ${error.message}\n`);
      process.exitCode = 1;
    }
  });
}

process.on("SIGINT", shutdown);
process.on("SIGTERM", shutdown);
