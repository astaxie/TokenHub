import { type AppRole } from "../core/types";
import { formatNumber } from "../domain/formatting";
import { type AppLanguage } from "../i18n/runtime";
import { gatewayJapaneseLLMUsageDocs } from "./gateway-llm-ja";
import { gatewayChineseLLMUsageDocs } from "./gateway-llm-zh";
import { type GatewayDocBundle, type GatewayDocStats } from "./gateway-view";

export function gatewayLLMUsageDocs({
  language,
  role,
  ...stats
}: GatewayDocStats & { language: AppLanguage; role: AppRole }): GatewayDocBundle {
  if (language === "zh-CN") return gatewayChineseLLMUsageDocs(stats, role);
  if (language === "ja") return gatewayJapaneseLLMUsageDocs(stats, role);
  return gatewayEnglishLLMUsageDocs(stats, role);
}

export function gatewayListModelsCurl(stats: GatewayDocStats) {
  return `curl --request GET \\
  --url "${stats.baseURL}/models" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json"`;
}

export function gatewayRetrieveModelCurl(stats: GatewayDocStats) {
  return `curl --request GET \\
  --url "${stats.baseURL}/models/${encodeURIComponent(stats.sampleModel)}" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json"`;
}

export function gatewayStreamingCurl(stats: GatewayDocStats) {
  return `curl -N --request POST \\
  --url "${stats.baseURL}/chat/completions" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "${stats.sampleModel}",
    "messages": [
      {"role": "user", "content": "Stream a short release note for this model gateway."}
    ],
    "stream": true,
    "stream_options": {"include_usage": true}
  }'`;
}

export function gatewayAnthropicMessagesCurl(stats: GatewayDocStats) {
  return `curl -N --request POST \\
  --url "${stats.baseURL}/messages" \\
  --header "${stats.authHeader}" \\
  --header "anthropic-version: 2023-06-01" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "${stats.sampleModel}",
    "max_tokens": 2048,
    "messages": [
      {"role": "user", "content": "Understand this repository and summarize its architecture."}
    ],
    "stream": true
  }'`;
}

export function gatewayAnthropicCountTokensCurl(stats: GatewayDocStats) {
  return `curl --request POST \\
  --url "${stats.baseURL}/messages/count_tokens" \\
  --header "${stats.authHeader}" \\
  --header "anthropic-version: 2023-06-01" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "${stats.sampleModel}",
    "messages": [
      {"role": "user", "content": "Understand this repository."}
    ]
  }'`;
}

export function gatewayClaudeCodeExample(stats: GatewayDocStats) {
  const anthropicBaseURL = stats.baseURL.replace(/\/v1\/?$/, "");
  return `export ANTHROPIC_BASE_URL="${anthropicBaseURL}"
export ANTHROPIC_AUTH_TOKEN="YOUR_TOKENHUB_API_KEY"
export ANTHROPIC_MODEL="${stats.sampleModel}"

claude`;
}

export function gatewayResponsesCurl(stats: GatewayDocStats) {
  return `curl --request POST \\
  --url "${stats.baseURL}/responses" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "${stats.sampleModel}",
    "input": "Summarize the weekly project status in three bullets."
  }'`;
}

export function gatewayEmbeddingsCurl(stats: GatewayDocStats) {
  return `curl --request POST \\
  --url "${stats.baseURL}/embeddings" \\
  --header "${stats.authHeader}" \\
  --header "Content-Type: application/json" \\
  --data '{
    "model": "text-embedding-3-small",
    "input": "TokenHub enterprise knowledge base"
  }'`;
}

export function gatewayOpenAISDKExample(stats: GatewayDocStats) {
  return `import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: "${stats.baseURL}",
});

const completion = await client.chat.completions.create({
  model: "${stats.sampleModel}",
  messages: [
    { role: "user", content: "Write a concise onboarding checklist." },
  ],
});

console.log(completion.choices[0]?.message?.content);`;
}

export function gatewayPythonSDKExample(stats: GatewayDocStats) {
  return `from openai import OpenAI

client = OpenAI(
    api_key="YOUR_TOKENHUB_API_KEY",
    base_url="${stats.baseURL}",
)

completion = client.chat.completions.create(
    model="${stats.sampleModel}",
    messages=[
        {"role": "user", "content": "Write a concise onboarding checklist."},
    ],
)

print(completion.choices[0].message.content)`;
}

export function gatewayEnglishLLMUsageDocs(stats: GatewayDocStats, role: AppRole): GatewayDocBundle {
  const teamLeader = role === "team_leader";
  return {
    defaultDocID: "quickstart",
    nav: {
      title: "API Documentation",
      subtitle: "OpenAI-compatible LLM usage",
      searchPlaceholder: "Search endpoints, parameters, or errors",
      noResults: "No matching API document",
    },
    eyebrow: "LLM API Docs",
    title: "Call Large Language Models",
    description: teamLeader
      ? "Use project-scoped keys to call approved models, then manage access, quota, and cost attribution at the project level."
      : "Use a project API key to call OpenAI-compatible model endpoints. Start with model discovery, then call chat, responses, or embeddings.",
    languageLabel: "Documentation language",
    quickInfoLabel: "API basics",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "Sample model",
      currentConfig: "Current API scope",
      activeRoutes: `${formatNumber(stats.visibleModelCount)} callable model${stats.visibleModelCount === 1 ? "" : "s"}`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} project key${stats.apiKeyCount === 1 ? "" : "s"}`,
    },
    groups: [
      {
        title: "Start Here",
        items: [
          {
            id: "quickstart",
            group: "Start Here",
            badge: "GUIDE",
            title: "Quick Start",
            description: "Make your first OpenAI-compatible LLM request through TokenHub.",
            details: [
              { label: "Base URL", value: stats.baseURL },
              { label: "Auth header", value: "Authorization: Bearer <API Key>" },
              { label: "Sample model", value: stats.sampleModel },
              { label: "Current scope", value: teamLeader ? "Team projects" : "Assigned projects" },
            ],
            notesTitle: "Call sequence",
            notes: [
              "Create or copy a project API key from Key Management. Console login tokens are not accepted by /v1 model endpoints.",
              "Call GET /v1/models first. The response is the model list available to that API key.",
              "Use one of those model IDs in POST /v1/chat/completions, /v1/responses, or /v1/embeddings.",
              "For failed calls, copy request_id from the response and search Request Logs.",
            ],
            examplesTitle: "First requests",
            examples: [
              { title: "List available models", code: gatewayListModelsCurl(stats) },
              { title: "Create a chat completion", code: stats.chatCurl },
            ],
          },
          {
            id: "authentication",
            group: "Start Here",
            badge: "AUTH",
            title: "Authentication",
            description: "Every model API request uses a project API key in the Authorization header.",
            table: {
              title: "Required headers",
              columns: ["Header", "Required", "Value"],
              rows: [
                ["Authorization", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "POST requests", "application/json"],
              ],
            },
            notesTitle: "Permission checks",
            notes: [
              "The key must be active and attached to an active project.",
              "The requested model must be visible to the project and backed by at least one enabled route.",
              teamLeader
                ? "Team leaders should issue keys from the intended project so usage rolls up to the right team and cost center."
                : "If you belong to multiple projects, choose the project that should own usage and cost before creating a key.",
            ],
          },
        ],
      },
      {
        title: "LLM API Reference",
        items: [
          {
            id: "list-models",
            group: "LLM API Reference",
            method: "GET",
            path: "/v1/models",
            title: "List Models",
            description: "Return the LLM models currently available to the API key. This endpoint is OpenAI-compatible.",
            params: {
              title: "Request headers",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["Authorization", "header", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "Yes", "application/json"],
              ],
            },
            table: {
              title: "Model fields",
              columns: ["Field", "Description"],
              rows: [
                ["id", "Model identifier used in later API calls."],
                ["object", "Object type, usually model."],
                ["created", "Model creation Unix timestamp."],
                ["input_token_price_per_m", "JieKou-compatible integer input price per million tokens."],
                ["output_token_price_per_m", "JieKou-compatible integer output price per million tokens."],
                ["title", "Model title."],
                ["description", "Model description."],
                ["context_size", "Maximum context size."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayListModelsCurl(stats) }],
          },
          {
            id: "retrieve-model",
            group: "LLM API Reference",
            method: "GET",
            path: "/v1/models/{model}",
            title: "Retrieve Model",
            description: "Return one model visible to the API key. The response fields match the JieKou-compatible model object.",
            params: {
              title: "Path and headers",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "path", "Yes", `Model ID from /v1/models, for example ${stats.sampleModel}.`],
                ["Authorization", "header", "Yes", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "Yes", "application/json"],
              ],
            },
            table: {
              title: "Response fields",
              columns: ["Field", "Description"],
              rows: [
                ["id", "Model identifier used in API calls."],
                ["created", "Model creation Unix timestamp."],
                ["object", "Object type, always model."],
                ["input_token_price_per_m", "JieKou-compatible integer input price per million tokens."],
                ["output_token_price_per_m", "JieKou-compatible integer output price per million tokens."],
                ["title", "Model title."],
                ["description", "Model description."],
                ["context_size", "Maximum context size."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayRetrieveModelCurl(stats) }],
          },
          {
            id: "chat-completions",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/chat/completions",
            title: "Create Chat Completion",
            description: "Generate a model response from a message list. Use this for normal chat, tool calling, structured output, and streaming.",
            params: {
              title: "Request body",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "string", "Yes", `A model ID from /v1/models, for example ${stats.sampleModel}.`],
                ["messages", "array", "Yes", "Conversation messages with system, user, or assistant roles."],
                ["max_tokens", "integer", "No", "Maximum generated tokens."],
                ["temperature", "number", "No", "Sampling temperature."],
                ["reasoning_effort", "string", "No", "Reasoning effort; omitted when the selected route cannot represent it."],
                ["stream", "boolean", "No", "When true, returns Server-Sent Events ending with data: [DONE]."],
                ["tools", "array", "No", "Function tools supported by compatible upstream models."],
                ["response_format", "object", "No", "JSON object or JSON schema output when supported."],
              ],
            },
            table: {
              title: "Response fields",
              columns: ["Field", "Description"],
              rows: [
                ["id", "Unique completion ID."],
                ["choices[].message", "Assistant response content."],
                ["choices[].finish_reason", "Why generation stopped, such as stop or length."],
                ["usage", "prompt, completion, and total token counts."],
              ],
            },
            examplesTitle: "Examples",
            examples: [
              { title: "Non-streaming", code: stats.chatCurl },
              { title: "Streaming", code: gatewayStreamingCurl(stats) },
            ],
          },
          {
            id: "responses-api",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/responses",
            title: "Create Response",
            description: "Use the Responses-style API for simple text input and future multimodal extensions.",
            params: {
              title: "Request body",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "string", "Yes", "A callable model ID."],
                ["input", "string | array", "Yes", "Input text or structured input content."],
                ["reasoning.effort", "string", "No", "Nested reasoning effort for OpenAI-compatible, Anthropic, and Gemini routes."],
                ["stream", "boolean", "No", "Not implemented; true returns 501."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayResponsesCurl(stats) }],
          },
          {
            id: "anthropic-messages",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/messages",
            title: "Create Anthropic Message",
            description: "Call TokenHub from Claude Code or an Anthropic-compatible client with text, images, client tools, tool results, and streaming.",
            params: {
              title: "Request body",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "string", "Yes", "A callable TokenHub model ID."],
                ["max_tokens", "integer", "Yes", "Maximum generated tokens."],
                ["messages", "array", "Yes", "Anthropic user and assistant messages with structured content blocks."],
                ["system", "string | array", "No", "Top-level Anthropic system prompt."],
                ["tools", "array", "No", "Client tool definitions using input_schema."],
                ["tool_choice", "object", "No", "Controls automatic, required, named, or disabled tool use."],
                ["stream", "boolean", "No", "Returns Anthropic named Server-Sent Events when true."],
              ],
            },
            notesTitle: "Routing behavior",
            notes: [
              "Native Anthropic routes preserve Anthropic content blocks and beta headers.",
              "OpenAI-compatible routes translate client tools, tool results, images, and streaming events.",
              "Unsupported Anthropic server tools return an explicit 400 response on OpenAI-compatible routes.",
            ],
            examplesTitle: "Examples",
            examples: [
              { title: "Messages API", code: gatewayAnthropicMessagesCurl(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "anthropic-count-tokens",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/messages/count_tokens",
            title: "Count Anthropic Message Tokens",
            description: "Return a deterministic input-token estimate after authenticating the key and verifying model access. This request is not billed as inference.",
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayAnthropicCountTokensCurl(stats) }],
          },
          {
            id: "embeddings-api",
            group: "LLM API Reference",
            method: "POST",
            path: "/v1/embeddings",
            title: "Create Embeddings",
            description: "Create vector embeddings for search, retrieval, classification, and clustering workflows.",
            params: {
              title: "Request body",
              columns: ["Field", "Type", "Required", "Description"],
              rows: [
                ["model", "string", "Yes", "An embedding model ID visible to the key."],
                ["input", "string | array", "Yes", "Text to embed."],
                ["encoding_format", "string", "No", "float or base64 when supported by the upstream model."],
              ],
            },
            examplesTitle: "Example",
            examples: [{ title: "cURL", code: gatewayEmbeddingsCurl(stats) }],
          },
        ],
      },
      {
        title: teamLeader ? "Team Rollout" : "Project Keys",
        items: [
          {
            id: "project-keys",
            group: teamLeader ? "Team Rollout" : "Project Keys",
            badge: "KEY",
            title: teamLeader ? "Issue Keys for Projects" : "Use Project Keys",
            description: teamLeader
              ? "A team member may belong to multiple projects. Issue each key under the project that should own usage, quota, and cost."
              : "You may belong to multiple projects. Create the key under the project that should own usage, quota, and cost.",
            table: {
              title: teamLeader ? "Team key rollout checklist" : "Project key rules",
              columns: ["Item", "Rule"],
              rows: teamLeader ? [
                ["Project", "Create or select the project before issuing a key."],
                ["Members", "Add the application owner to the project member panel."],
                ["Models", "Verify GET /v1/models with the new key before handing it to the app."],
                ["Reports", "Review team usage by member, project, model, and cost center."],
              ] : [
                ["Project", "Each API key belongs to exactly one project."],
                ["Models", "The model list is filtered by project and key permissions."],
                ["Secret", "A new key is shown once. Store it in your app secret manager."],
                ["Usage", "Requests are attributed to the key's project and your account."],
              ],
            },
          },
          {
            id: "sdk-examples",
            group: teamLeader ? "Team Rollout" : "Project Keys",
            badge: "SDK",
            title: "SDKs and Claude Code",
            description: "Use the TokenHub Base URL with OpenAI-compatible SDKs or the TokenHub host URL with Claude Code.",
            examplesTitle: "Client examples",
            examples: [
              { title: "Node.js", code: gatewayOpenAISDKExample(stats) },
              { title: "Python", code: gatewayPythonSDKExample(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "errors",
            group: teamLeader ? "Team Rollout" : "Project Keys",
            badge: "REF",
            title: "Errors and Troubleshooting",
            description: "Use status codes to locate API key, project membership, model routing, and quota problems.",
            table: {
              title: "Common errors",
              columns: ["Status", "Likely cause", "Action"],
              rows: [
                ["401", "Missing, malformed, disabled, or expired API key.", "Check the Authorization header and key status."],
                ["403", "Project, key, or model permission does not allow the request.", teamLeader ? "Check project membership, key model scope, and team project ownership." : "Ask your team leader to check project membership and model access."],
                ["404/503", "No enabled healthy route can serve the model.", "Ask an administrator to enable routing or check provider health."],
                ["429", "Project quota, concurrency, or provider resource limit was reached.", teamLeader ? "Review project quota and concurrency limits." : "Wait for quota reset or request a quota increase."],
                ["500", "Upstream provider or routing error.", `Search request_id in Request Logs. Current visible log sample: ${formatNumber(stats.requestLogCount)}.`],
              ],
            },
          },
        ],
      },
    ],
  };
}
