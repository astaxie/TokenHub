import { failure } from "./validation.mjs";

// These are application dependencies, never configurable module names or URLs.
const REGISTRY = {
  cursor: ["cursor", "CursorAuthPlugin"], kiro: ["kiro", "KiroAuthPlugin"],
  qoder: ["qoder", "QoderAuthPlugin"], "qoder-cn": ["qoder", "QoderCNAuthPlugin"],
  qwenwork: ["qoder", "QwenWorkAuthPlugin"], "qwenwork-enterprise": ["qoder", "QwenWorkEnterpriseAuthPlugin"],
  "gcp-ai": ["gcp-ai", "createGCPProvider"],
  workbuddy: ["workbuddy", "WorkBuddyAuthPlugin"], "workbuddy-ai": ["workbuddy", "WorkBuddyAIAuthPlugin"],
  "mimo-app": ["mimo", "MimoAuthPlugin"], zcode: ["zcode", "ZCodeAuthPlugin"],
  factory: ["factory", "FactoryAuthPlugin"], grok: ["grok", "GrokAuthPlugin"],
  devin: ["devin", "DevinAuthPlugin"], "commandcode-plan": ["commandcode", "CommandCodePlugin"], zed: ["zed", "ZedAuthPlugin"],
  "kimi-work": ["kimi-work", "default"], "minimax-code": ["minimax", "MiniMaxCodeAuthPlugin"],
  "minimax-code-global": ["minimax", "MiniMaxCodeGlobalAuthPlugin"], cline: ["cline", "ClinePlugin"],
  "trae-cn": ["trae", "TraeCNAuthPlugin"], "trae-global": ["trae", "TraeGlobalAuthPlugin"],
  opendesign: ["opendesign", "OpenDesignAuthPlugin"], "nous-portal": ["nous", "NousPortalAuthPlugin"],
  raccoon: ["raccoon", "RaccoonAuthPlugin"], autoclaw: ["autoclaw", "AutoclawAuthPlugin"],
  "autoclaw-cn": ["autoclaw", "AutoclawCnAuthPlugin"], codearts: ["codearts", "CodeArtsAuthPlugin"],
  joycode: ["joycode", "JoyCodeAuthPlugin"],
};

export const providerIDs = Object.freeze(Object.keys(REGISTRY));

export async function loadProvider(id, context, getAuth) {
  if (!Object.hasOwn(REGISTRY, id)) throw failure("provider_not_supported", "Subscription provider is not supported");
  if (id === "cursor") return (await import("./cursor.mjs")).createCursorProvider(context, getAuth);
  if (id === "qwenwork-enterprise") return (await import("./qwenwork.mjs")).createQwenWorkEnterpriseProvider(context);
  if (id === "gcp-ai") return (await import("./gcp-ai.mjs")).createGCPProvider(context);
  if (id === "grok") return (await import("./grok.mjs")).createGrokProvider(context);
  if (id === "cline") return (await import("./cline.mjs")).createClineProvider(context);
  if (id === "workbuddy" || id === "workbuddy-ai") return (await import("./workbuddy.mjs")).createWorkBuddyProvider(id, context);
  const [directory, name] = REGISTRY[id];
  const module = await import(`./vendor/${directory}/index.mjs`);
  if (id === "kimi-work") {
    const hooks = module._internal.createPlugin({ readSession: () => {
      const auth = getAuth();
      if (!auth?.access || !auth.webToken || !auth.uid) throw failure("invalid_credentials", "Import Kimi Work access, webToken and uid fields");
      return { workToken: auth.access, webToken: auth.webToken, uid: auth.uid, deviceId: auth.deviceId };
    } });
    hooks.auth.methods = [];
    return hooks;
  }
  return module[name](context);
}

const LOCAL_METHOD = /(?:CLI's sign-in|CLI's or|cursor-agent's sign-in|desktop|app's sign-in|current sign-in|CPA Grok|本机|桌面端)/i;

export function safeMethods(hooks) {
  return (hooks.auth?.methods ?? []).map((method, index) => ({ ...method, method_id: String(index) }))
    .filter((method) => !LOCAL_METHOD.test(method.label ?? "") && !(method.prompts ?? []).some((prompt) => /^(path|home|file)$/i.test(prompt.key)));
}

export function describeMethod(method) {
  return { method_id: method.method_id, type: method.type, label: method.label ?? method.type,
    ...(method.keyless ? { keyless: true } : {}),
    prompts: (method.prompts ?? []).map(({ type, key, message, placeholder, options }) => ({ type, key, message, placeholder, options })) };
}

export function describeOptions(hooks) {
  return (hooks.auth?.options ?? []).filter((option) => typeof option.key === "string" && /^[a-z][a-z0-9_]*$/.test(option.key))
    .map(({ key, label, description, confirm }) => ({ key, label, description, ...(confirm ? { confirm } : {}) }));
}

export function validateOptions(hooks, options = {}) {
  const keys = new Set(describeOptions(hooks).map((option) => option.key));
  if (!options || typeof options !== "object" || Array.isArray(options) ||
      Object.entries(options).some(([key, value]) => !keys.has(key) || typeof value !== "boolean")) {
    throw failure("invalid_request", "Subscription options must use declared boolean settings");
  }
  return { ...options };
}

export function normalizeModels(id, config, models = config.models ?? {}) {
  return Object.fromEntries(Object.entries(models).filter(([, model]) => model && typeof model === "object").map(([key, model]) => {
    const api = model.api ?? {};
    return [key, { ...model, id: key, providerID: id, name: model.name ?? key,
      api: { id: api.id ?? model.id ?? key, url: api.url ?? model.provider?.api ?? config.api ?? "",
        npm: api.npm ?? model.provider?.npm ?? config.npm ?? "@ai-sdk/openai-compatible" },
      capabilities: model.capabilities ?? { reasoning: model.reasoning ?? false, toolcall: model.tool_call ?? true,
        attachment: model.attachment ?? false, input: Object.fromEntries((model.modalities?.input ?? ["text"]).map((value) => [value, true])), output: { text: true } },
      limit: model.limit ?? { context: 0, output: 0 }, variants: model.variants ?? {} }];
  }));
}

export function modelProtocol(model) {
  if (model.api.npm === "@ai-sdk/anthropic") return "anthropic";
  if (model.api.npm === "@ai-sdk/google") return "gemini";
  if (model.api.npm === "@ai-sdk/openai") return "responses";
  return "chat";
}
