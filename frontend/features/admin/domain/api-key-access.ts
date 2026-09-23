export const apiKeyPlaceholder = "YOUR_TOKENHUB_API_KEY";
export const modelPlaceholder = "YOUR_MODEL";
export const accessProtocols = [
  { id: "chat", label: "OpenAI · Chat Completions" },
  { id: "responses", label: "OpenAI · Responses" },
  { id: "anthropic", label: "Anthropic · Messages" },
  { id: "gemini", label: "Gemini · generateContent" },
] as const;
export type AccessProtocol = typeof accessProtocols[number]["id"];

function shellQuote(value: string) {
  return `'${value.replaceAll("'", "'\\''")}'`;
}

export function apiKeyAccessConfig(baseURL: string, protocol: AccessProtocol, secret: string, model: string) {
  const root = baseURL.trim().replace(/\/+$/, "").replace(/\/(v1|v1beta)$/, "");
  const modelID = model.trim() || modelPlaceholder;
  const key = secret || apiKeyPlaceholder;
  const base = protocol === "anthropic" || protocol === "gemini" ? root : `${root}/v1`;
  const path = protocol === "chat" ? "/v1/chat/completions" : protocol === "responses" ? "/v1/responses"
    : protocol === "anthropic" ? "/v1/messages" : `/v1beta/models/${encodeURIComponent(modelID)}:generateContent`;
  const authHeader = protocol === "anthropic" ? "x-api-key" : protocol === "gemini" ? "x-goog-api-key" : "Authorization";
  const auth = authHeader === "Authorization" ? `Bearer ${key}` : key;
  const body = protocol === "gemini" ? { contents: [{ role: "user", parts: [{ text: "Hello" }] }] }
    : protocol === "responses" ? { model: modelID, input: "Hello" }
      : { model: modelID, ...(protocol === "anthropic" ? { max_tokens: 256 } : {}), messages: [{ role: "user", content: "Hello" }] };
  const headers = [`${authHeader}: ${auth}`, "Content-Type: application/json"];
  if (protocol === "anthropic") headers.push("anthropic-version: 2023-06-01");
  const example = [`curl ${shellQuote(`${root}${path}`)}`, ...headers.map(header => `  -H ${shellQuote(header)}`), `  -d ${shellQuote(JSON.stringify(body))}`].join(" \\\n");
  return { base, endpoint: `${root}${path}`, authHeader, auth, example };
}
