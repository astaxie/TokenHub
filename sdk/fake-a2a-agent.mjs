#!/usr/bin/env node

import { createServer } from "node:http";

const host = process.env.FAKE_A2A_HOST || "127.0.0.1";
const port = Number(process.env.FAKE_A2A_PORT || 19091);
const baseURL = `http://${host}:${port}`;

const server = createServer(async (request, response) => {
  if (request.method === "GET" && request.url === "/.well-known/agent-card.json") {
    return json(response, {
      name: "Fixture Agent",
      description: "A2A process E2E fixture",
      version: "1",
      supportedInterfaces: [{ url: `${baseURL}/a2a`, protocolBinding: "JSONRPC", protocolVersion: "1.0" }],
      capabilities: { streaming: true },
      defaultInputModes: ["text/plain"],
      defaultOutputModes: ["text/plain"],
      skills: [{ id: "echo", name: "Echo", description: "Returns fixture text", tags: [] }],
    });
  }
  if (request.method !== "POST" || request.url !== "/a2a") {
    response.writeHead(404).end();
    return;
  }
  const body = await readBody(request);
  const rpc = JSON.parse(body);
  if (request.headers["a2a-version"] !== "1.0") return rpcResponse(response, rpc.id, null, { code: -32009, message: "version" });
  if (request.headers.authorization !== "Bearer fixture-static") return rpcResponse(response, rpc.id, null, { code: -32007, message: "upstream auth" });

  switch (rpc.method) {
    case "SendMessage":
      return rpcResponse(response, rpc.id, { task: task("fixture-upstream-task", "fixture reply") });
    case "GetTask":
      return rpcResponse(response, rpc.id, task(rpc.params.id, "fixture reply"));
    case "CancelTask":
      return rpcResponse(response, rpc.id, { ...task(rpc.params.id, ""), status: { state: "TASK_STATE_CANCELED" } });
    case "SendStreamingMessage":
    case "SubscribeToTask":
      response.writeHead(200, { "content-type": "text/event-stream", "cache-control": "no-cache" });
      sse(response, rpc.id, { task: task("fixture-stream-task", "") });
      sse(response, rpc.id, { statusUpdate: {
        taskId: "fixture-stream-task",
        contextId: "fixture-context",
        status: { state: "TASK_STATE_COMPLETED", message: {
          messageId: "fixture-stream-message", role: "ROLE_AGENT", taskId: "fixture-stream-task", parts: [{ text: "fixture streamed reply" }],
        } },
      } });
      sse(response, rpc.id, { artifactUpdate: {
        taskId: "fixture-stream-task",
        contextId: "fixture-context",
        append: true,
        lastChunk: true,
        artifact: { artifactId: "fixture-stream-artifact", parts: [{ text: "fixture streamed artifact" }] },
      } });
      response.end();
      return;
    default:
      return rpcResponse(response, rpc.id, null, { code: -32601, message: "method not found" });
  }
});

server.listen(port, host, () => console.log(`fake A2A Agent listening on ${baseURL}`));

function task(id, text) {
  return {
    id,
    contextId: "fixture-context",
    status: { state: "TASK_STATE_COMPLETED" },
    artifacts: text ? [{ artifactId: "fixture-artifact", parts: [{ text }] }] : [],
  };
}

function rpcResponse(response, id, result, error) {
  json(response, { jsonrpc: "2.0", id, ...(error ? { error } : { result }) });
}

function sse(response, id, result) {
  response.write(`data: ${JSON.stringify({ jsonrpc: "2.0", id, result })}\n\n`);
}

function json(response, value) {
  response.writeHead(200, { "content-type": "application/json" });
  response.end(JSON.stringify(value));
}

async function readBody(request) {
  const chunks = [];
  for await (const chunk of request) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}
