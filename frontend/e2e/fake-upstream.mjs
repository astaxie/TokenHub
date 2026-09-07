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

  if (request.method === "GET" && ["/v1/models", "/open/v1/models"].includes(request.url)) {
    const expectedAuthorization = request.url === "/open/v1/models" ? undefined : `Bearer ${expectedKey}`;
    if (request.headers.authorization !== expectedAuthorization) {
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

  if (request.method === "POST" && request.url === "/v1/chat/completions") {
    if (request.headers.authorization !== `Bearer ${expectedKey}`) { response.writeHead(401); response.end(); return; }
    response.writeHead(200, { "content-type": "application/json", "x-request-id": "e2e-supplier-invocation" });
    response.end(JSON.stringify({ id: "e2e-response", choices: [{ message: { role: "assistant", content: "Fixture reply" }, finish_reason: "stop" }], usage: { prompt_tokens: 1000000, completion_tokens: 10000, total_tokens: 1010000, prompt_tokens_details: { cached_tokens: 500000 }, cache_write_input_tokens: 100000, cache_write_5m_input_tokens: 60000, cache_write_1h_input_tokens: 30000 } }));
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
