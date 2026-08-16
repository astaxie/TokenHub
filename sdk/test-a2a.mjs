#!/usr/bin/env node

const baseURL = String(process.env.TOKENHUB_BASE_URL || "http://127.0.0.1:8080").replace(/\/$/, "");
const apiKey = process.env.TOKENHUB_API_KEY || "thk_demo_local";
const adminToken = process.env.TOKENHUB_ADMIN_TOKEN || "dev_admin_token";
const slug = process.env.TOKENHUB_A2A_AGENT_SLUG || "e2e-agent";
const cardURL = process.env.TOKENHUB_A2A_CARD_URL || "";

try {
  if (cardURL) await registerFixtureAgent();
  const card = await requestJSON(`/a2a/${slug}/.well-known/agent-card.json`, { headers: { authorization: `Bearer ${apiKey}` } });
  assert(card.supportedInterfaces?.some((item) => item.protocolVersion === "1.0" && item.protocolBinding === "JSONRPC"), "public card is not A2A 1.0 JSONRPC");

  const message = {
    messageId: `message-${Date.now()}`,
    role: "ROLE_USER",
    parts: [{ text: "A2A process E2E" }],
  };
  const wrongVersion = await a2a("SendMessage", { message }, "0.3");
  assert(wrongVersion.error?.data?.[0]?.reason === "VERSION_NOT_SUPPORTED", "A2A 0.3 was not rejected");

  const sent = await a2a("SendMessage", { message });
  const task = sent.result?.task;
  assert(String(task?.id || "").startsWith("atask_"), "gateway task ID is missing");
  assert(!JSON.stringify(sent).includes("fixture-upstream-task"), "upstream task ID leaked");

  const fetched = await a2a("GetTask", { id: task.id });
  assert(fetched.result?.id === task.id, "GetTask did not preserve the gateway task ID");

  const listed = await a2a("ListTasks", { pageSize: 10 });
  assert(listed.result?.tasks?.some((item) => item.id === task.id), "ListTasks did not return the gateway task");

	const subscribedResponse = await fetch(`${baseURL}/a2a/${slug}`, {
		method: "POST",
		headers: a2aHeaders("1.0"),
		body: JSON.stringify({ jsonrpc: "2.0", id: "subscribe-e2e", method: "SubscribeToTask", params: { id: task.id } }),
	});
	const subscribedBody = await subscribedResponse.text();
	assert(subscribedResponse.headers.get("content-type")?.includes("text/event-stream"), "SubscribeToTask stream content type is missing");
	assert(subscribedBody.includes("fixture streamed reply"), "SubscribeToTask content is missing");
	assert(subscribedBody.includes("fixture streamed artifact"), "SubscribeToTask artifact is missing");
	assert(!subscribedBody.includes("fixture-stream-task"), "SubscribeToTask leaked the upstream task ID");

	const canceled = await a2a("CancelTask", { id: task.id });
	assert(canceled.result?.status?.state === "TASK_STATE_CANCELED", "CancelTask did not return the canceled state");
	assert(!JSON.stringify(canceled).includes("fixture-upstream-task"), "CancelTask leaked the upstream task ID");

  const streamResponse = await fetch(`${baseURL}/a2a/${slug}`, {
    method: "POST",
    headers: a2aHeaders("1.0"),
    body: JSON.stringify({ jsonrpc: "2.0", id: "stream-e2e", method: "SendStreamingMessage", params: { message } }),
  });
  const streamBody = await streamResponse.text();
  assert(streamResponse.headers.get("content-type")?.includes("text/event-stream"), "A2A stream content type is missing");
  assert(streamBody.includes("fixture streamed reply"), "A2A stream content is missing");
  assert(streamBody.includes("fixture streamed artifact"), "A2A stream artifact is missing");
  assert(!streamBody.includes("fixture-stream-task"), "stream leaked the upstream task ID");

  const bridge = await requestJSON("/v1/responses", {
    method: "POST",
    headers: { authorization: `Bearer ${apiKey}`, "content-type": "application/json" },
    body: JSON.stringify({ model: `agent/${slug}`, input: "Responses process E2E" }),
  });
  assert(String(bridge.output_text || "").includes("fixture reply"), "Responses bridge output is missing");

  const bridgeStreamResponse = await fetch(`${baseURL}/v1/responses`, {
    method: "POST",
    headers: { authorization: `Bearer ${apiKey}`, "content-type": "application/json" },
    body: JSON.stringify({ model: `agent/${slug}`, input: "Responses streaming process E2E", stream: true }),
  });
  const bridgeStreamBody = await bridgeStreamResponse.text();
  assert(bridgeStreamResponse.headers.get("content-type")?.includes("text/event-stream"), "Responses bridge stream content type is missing");
  assert(bridgeStreamBody.includes("response.output_text.delta") && bridgeStreamBody.includes("fixture streamed reply"), "Responses bridge stream output is missing");
  assert(bridgeStreamBody.includes("response.completed") && bridgeStreamBody.includes("[DONE]"), "Responses bridge stream did not complete");

  console.log("A2A 1.0 process E2E passed");
  console.log(JSON.stringify({ agent: slug, task_id: task.id, methods: ["SendMessage", "GetTask", "ListTasks", "CancelTask", "SendStreamingMessage", "SubscribeToTask"], responses_bridge: ["non_streaming", "streaming"] }, null, 2));
} catch (error) {
  console.error("A2A 1.0 process E2E failed");
  console.error(error instanceof Error ? error.stack : String(error));
  process.exit(1);
}

async function registerFixtureAgent() {
  const registered = await requestJSON("/api/admin/agents", {
    method: "POST",
    headers: { authorization: `Bearer ${adminToken}`, "content-type": "application/json" },
    body: JSON.stringify({ slug, card_url: cardURL, headers: { Authorization: "Bearer fixture-static" } }),
  });
  const bindings = await requestJSON(`/api/admin/agent-access-bindings?agent_id=${encodeURIComponent(registered.id)}`, {
    headers: { authorization: `Bearer ${adminToken}` },
  });
  if (!(bindings.data || []).some((item) => item.scope_type === "global" && item.scope_id === "*" && item.effect === "allow")) {
    await requestJSON("/api/admin/agent-access-bindings", {
      method: "POST",
      headers: { authorization: `Bearer ${adminToken}`, "content-type": "application/json" },
      body: JSON.stringify({ agent_id: registered.id, scope_type: "global", scope_id: "*", effect: "allow", status: "active" }),
    });
  }
}

async function a2a(method, params, version = "1.0") {
  return requestJSON(`/a2a/${slug}`, {
    method: "POST",
    headers: a2aHeaders(version),
    body: JSON.stringify({ jsonrpc: "2.0", id: `${method}-${Date.now()}`, method, params }),
  });
}

function a2aHeaders(version) {
  return { authorization: `Bearer ${apiKey}`, "content-type": "application/json", "A2A-Version": version };
}

async function requestJSON(path, options = {}) {
  const response = await fetch(`${baseURL}${path}`, options);
  const text = await response.text();
  let payload;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    throw new Error(`${options.method || "GET"} ${path} returned invalid JSON: ${response.status} ${text}`);
  }
  if (!response.ok) throw new Error(`${options.method || "GET"} ${path} failed: ${response.status} ${text}`);
  return payload;
}

function assert(condition, message) {
  if (!condition) throw new Error(message);
}
