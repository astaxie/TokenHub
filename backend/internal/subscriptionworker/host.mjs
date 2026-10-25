import { randomUUID } from "node:crypto";
import { isDeepStrictEqual } from "node:util";
import { describeMethod, describeOptions, loadProvider, modelProtocol, normalizeModels, providerIDs, safeMethods, validateOptions } from "./registry.mjs";
import { enterpriseHost, failure, headerObject, MAX_BODY, publicError, toHeaders, validateAuth, validateTarget } from "./validation.mjs";

export function createHost({ scope, emit, relayCallback }) {
  const accounts = new Map();
  const acknowledgements = new Map();
  const sessions = new Map();
  const active = new Map();

  function acknowledge(message) {
    const pending = acknowledgements.get(message.update_id);
    if (!pending) return;
    acknowledgements.delete(message.update_id);
    if (message.error) pending.reject(failure("credential_save_failed", "Subscription credential changes could not be saved", 503));
    else pending.resolve();
  }

  async function save(account, auth) {
    const context = scope.getStore();
    const next = validateAuth(account.provider, auth, context.enterpriseHost);
    const updateID = randomUUID();
    const promise = new Promise((resolve, reject) => acknowledgements.set(updateID, { resolve, reject }));
    const timer = setTimeout(() => {
      const pending = acknowledgements.get(updateID);
      acknowledgements.delete(updateID);
      pending?.reject(failure("credential_save_failed", "Subscription credential changes could not be saved", 503));
    }, 30000);
    timer.unref();
    try {
      await emit({ id: context.id, event: "auth.update", update_id: updateID, resource_id: account.resourceID, auth: next });
      await promise;
      account.auth = next;
      account.saveError = null;
      account.pendingSave = null;
      if (account.provider === "qwenwork-enterprise") account.enterpriseHost = context.enterpriseHost;
      account.loader = null;
    } catch (error) {
      account.saveError = error;
      const { metadata: previousMetadata, ...previousIdentity } = account.auth ?? {};
      const { metadata: nextMetadata, ...nextIdentity } = next;
      account.pendingSave = { previous: account.auth, next, metadataOnly: previousIdentity.type === "api" &&
        typeof previousIdentity.key === "string" && previousIdentity.key.trim() !== "" && isDeepStrictEqual(previousIdentity, nextIdentity) };
      throw error;
    } finally { clearTimeout(timer); }
  }

  async function accountFor(command) {
    const resourceID = command.resource_id || "catalog";
    const key = command.provider + ":" + resourceID;
    if (!providerIDs.includes(command.provider)) throw failure("provider_not_supported", "Subscription provider is not supported");
    let account = accounts.get(key);
    if (!account) {
      account = { provider: command.provider, resourceID, auth: null, loader: null, hooks: null, models: null };
      const client = { auth: { set: async ({ path, body }) => {
        if (path?.id !== account.provider) throw failure("invalid_credentials", "A subscription adapter cannot update another provider");
        await save(account, body);
        return { data: true };
      } } };
      account.hooks = await loadProvider(command.provider, { client, magpie: { option: (provider, key) => provider === account.provider && scope.getStore()?.options?.[key] === true } }, () => account.auth);
      const config = {};
      await account.hooks.config?.(config);
      account.config = config.provider?.[command.provider];
      if (!account.config) throw new Error("Subscription adapter did not declare its provider");
      account.models = normalizeModels(command.provider, account.config);
      accounts.set(key, account);
    }
    const context = scope.getStore();
    context.account = account;
    context.options = validateOptions(account.hooks, command.options);
    let validated;
    if (command.auth !== undefined && command.auth !== null) {
      const incoming = typeof command.auth === "string" ? JSON.parse(command.auth) : command.auth;
      validated = validateAuth(command.provider, incoming);
    }
    if (account.saveError) {
      const pending = account.pendingSave;
      // Retrying an unchanged API key's metadata is safe. Token rotations stay
      // retained and blocked: another process may already have spent the token.
      // Never replace newer administrator credentials with a pending snapshot.
      if (!pending?.metadataOnly || !isDeepStrictEqual(validated ?? account.auth, pending.previous)) throw account.saveError;
      await save(account, pending.next);
      validated = account.auth;
    }
    if (validated) {
      if (command.provider === "qwenwork-enterprise") account.enterpriseHost = enterpriseHost(validated.enterpriseHost);
      if (JSON.stringify(validated) !== JSON.stringify(account.auth)) {
        account.auth = validated;
        account.loader = null;
      }
    }
    context.enterpriseHost = account.enterpriseHost;
    return account;
  }

  function runtimeAuth(account) {
    if (account.provider === "kimi-work" && account.auth) return { ...account.auth, source: "kimi-work-desktop" };
    return account.auth && structuredClone(account.auth);
  }

  async function fresh(account, force = false) {
    if (account.refreshing) return account.refreshing;
    const auth = account.auth;
    const refresh = account.hooks.auth?.refresh;
    if (!auth || typeof refresh !== "function") return;
    const lead = account.hooks.auth.refreshLead ?? 300000;
    if (!force && !(Number(auth.expires) > 0 && Number(auth.expires) <= Date.now() + lead)) return;
    account.refreshing = (async () => {
      const updated = await refresh(runtimeAuth(account));
      if (updated) await save(account, { ...auth, ...updated, type: updated.type === "success" ? "oauth" : updated.type || auth.type });
    })();
    try { await account.refreshing; }
    finally { account.refreshing = null; }
  }

  async function models(account) {
    await fresh(account);
    const provider = { id: account.provider, ...account.config, models: structuredClone(account.models) };
    const result = account.hooks.provider?.models ? await account.hooks.provider.models(provider, { auth: runtimeAuth(account) }) : provider.models;
    const fallback = !account.hooks.provider?.models || !result || result === provider.models || result?.[Symbol.for("magpie.fellBack")] === true;
    account.models = normalizeModels(account.provider, account.config, result);
    return { provider: account.provider, models: Object.values(account.models).map((model) => ({ ...model, protocol: modelProtocol(model) })), fallback };
  }

  async function fetchModel(account, command) {
    await fresh(account);
    if (!account.auth) throw failure("invalid_credentials", "Connect a subscription account before making requests");
    const modelID = command.model;
    if (typeof modelID !== "string" || !modelID || modelID.length > 512) throw failure("invalid_request", "A subscription model is required");
    let model = account.models[modelID];
    if (!model) { await models(account); model = account.models[modelID]; }
    if (!model) throw failure("model_not_found", "The connected subscription does not list this model", 404);
    const protocol = modelProtocol(model);
    if (command.protocol && command.protocol !== protocol) throw failure("protocol_mismatch", "This subscription model uses a different request protocol");
    const getAuth = async () => runtimeAuth(account);
    account.loader ??= await account.hooks.auth.loader(getAuth, { id: account.provider, ...account.config, models: account.models });
    const loader = account.loader;
    if (!loader || (typeof loader.fetch !== "function" && !loader.apiKey)) throw failure("invalid_credentials", "The subscription account credentials are incomplete");
    // An account's regional or tenant endpoint takes precedence over its seed.
    const base = (loader.baseURL || model.api.url || account.config.api).replace(/\/+$/, "");
    const path = protocol === "chat" ? "/chat/completions" : protocol === "anthropic" ? "/messages" : protocol === "responses" ? "/responses" :
      `/models/${encodeURIComponent(model.api.id)}:${command.stream === false ? "generateContent" : "streamGenerateContent?alt=sse"}`;
    const url = base + path;
    validateTarget(account.provider, url, scope.getStore().enterpriseHost);
    let body = command.body;
    if (typeof body === "string") {
      try { body = JSON.parse(body); } catch { throw failure("invalid_request", "Subscription requests must contain JSON"); }
    }
    if (!body || typeof body !== "object" || Array.isArray(body)) throw failure("invalid_request", "Subscription requests must contain a JSON object");
    body = { ...body, ...(protocol !== "gemini" ? { model: model.api.id } : {}) };
    const text = JSON.stringify(body);
    if (Buffer.byteLength(text) > MAX_BODY) throw failure("body_too_large", "Subscription request exceeds the size limit");
    const headers = toHeaders(command.headers);
    for (const name of [...headers.keys()]) if (/^(authorization|cookie|host|content-length|x-api-key|x-goog-api-key|proxy-authorization)$/i.test(name)) headers.delete(name);
    // Mirror the native SDK's credential header even when a custom adapter
    // wraps fetch. Some adapters rely on that header; others replace it.
    if (loader.apiKey) {
      const keyHeader = protocol === "anthropic" ? "x-api-key" : protocol === "gemini" ? "x-goog-api-key" : "authorization";
      headers.set(keyHeader, keyHeader === "authorization" ? `Bearer ${loader.apiKey}` : loader.apiKey);
    }
    const extras = { headers: {} };
    await account.hooks["chat.headers"]?.({ sessionID: command.session_id || "", model,
      provider: { info: { id: account.provider } } }, extras);
    for (const [name, value] of Object.entries({ ...account.loader.headers, ...model.headers, ...extras.headers })) headers.set(name, value);
    headers.set("content-type", "application/json");
    if (protocol === "anthropic") headers.set("anthropic-version", "2023-06-01");
    const response = await (loader.fetch || globalThis.fetch)(url, { method: "POST", headers, body: text, signal: scope.getStore().controller.signal });
    if (account.saveError) { await response.body?.cancel(); throw account.saveError; }
    await emit({ id: command.id, event: "response", status: response.status, headers: headerObject(response.headers) });
    if (response.body) {
      const reader = response.body.getReader();
      try {
        while (true) {
          const { value, done } = await reader.read();
          if (done) break;
          for (let start = 0; start < value.length; start += 65536) await emit({ id: command.id, event: "chunk", data: Buffer.from(value.subarray(start, start + 65536)).toString("base64") });
        }
      } finally { reader.releaseLock(); }
    }
    await emit({ id: command.id, event: "end" });
  }

  function sessionFor(command) {
    const session = sessions.get(command.session_id);
    if (!session || session.provider !== command.provider || session.resourceID !== (command.resource_id || "catalog")) throw failure("invalid_session", "Authorization session was not found");
    return session;
  }

  function endSession(session) {
    session.controller.abort();
    for (const server of session.servers) { server.closeAllConnections?.(); server.close(); }
    clearTimeout(session.timer);
  }

  function retainFinishedSession(session) {
    endSession(session);
    session.timer = setTimeout(() => sessions.delete(session.id), 5 * 60 * 1000);
    session.timer.unref();
  }

  async function startAuth(account, command) {
    if (sessions.size > 16) throw failure("invalid_session", "Too many pending authorization sessions");
    const method = safeMethods(account.hooks).find((item) => item.method_id === String(command.method_id ?? command.method ?? "0"));
    if (!method) throw failure("method_not_supported", "This authorization method is not available");
    const inputs = command.inputs ?? {};
    if (!inputs || typeof inputs !== "object" || Array.isArray(inputs) || Object.entries(inputs).some(([key, value]) =>
      !(method.prompts ?? []).some((prompt) => prompt.key === key) || typeof value !== "string" || value.length > 65536)) {
      throw failure("invalid_request", "Authorization inputs do not match the selected method");
    }
    for (const prompt of method.prompts ?? []) {
      const why = prompt.validate?.(inputs[prompt.key]);
      if (why) throw failure("invalid_request", "An authorization input is invalid");
      if (prompt.type === "select" && inputs[prompt.key] !== undefined && !(prompt.options ?? []).some((option) => option.value === inputs[prompt.key])) {
        throw failure("invalid_request", "An authorization selection is invalid");
      }
    }
    if (method.type === "api" && !method.authorize) {
      if (typeof command.key !== "string" || !command.key.trim()) throw failure("invalid_credentials", "An API key is required");
      const auth = validateAuth(account.provider, { type: "api", key: command.key, ...(Object.keys(inputs).length ? { metadata: inputs } : {}) });
      await save(account, auth);
      return { state: "complete", auth };
    }
    const session = { id: randomUUID(), provider: account.provider, resourceID: account.resourceID, state: "pending", servers: new Set(),
      controller: new AbortController(), auth: null, error: null, authorization: null, started: false };
    sessions.set(session.id, session);
    const context = { ...scope.getStore(), session, controller: session.controller,
      ...(account.provider === "qwenwork-enterprise" ? { enterpriseHost: enterpriseHost(inputs.domain) } : {}) };
    session.context = context;
    session.timer = setTimeout(() => { session.state = "expired"; endSession(session); sessions.delete(session.id); }, 15 * 60 * 1000);
    session.timer.unref();
    try {
      session.authorization = await scope.run(context, () => method.authorize(method.prompts?.length ? inputs : undefined));
      if (method.type === "api") {
        const result = session.authorization;
        if (result?.type !== "success") throw new Error("Authorization failed");
        await finishAuth(account, session, result, command.key);
      } else if (session.authorization.method === "auto") {
        session.started = true;
        void scope.run(context, async () => {
          try { await finishAuth(account, session, await session.authorization.callback()); }
          catch (error) { session.state = "failed"; session.error = publicError(error); retainFinishedSession(session); }
        });
      }
      return { session_id: session.id, state: session.state, url: session.authorization.url || "", instructions: session.authorization.instructions || "",
        method: session.authorization.method || "auto", opened: session.authorization.opened === true,
        ...(ownedAuthorizationListener(session, session.authorization.url) ? { local_listener: true } : {}) };
    } catch (error) { endSession(session); sessions.delete(session.id); throw error; }
  }

  function ownedAuthorizationListener(session, value) {
    let url;
    try { url = new URL(value); } catch { return false; }
    return url.protocol === "http:" && !url.username && !url.password && !url.hash && url.port !== "" &&
      ["localhost", "127.0.0.1", "::1"].includes(url.hostname.replace(/^\[|\]$/g, "")) &&
      [...session.servers].some((server) => server.address()?.port === Number(url.port));
  }

  async function finishAuth(account, session, result, key) {
    if (session.controller.signal.aborted) return;
    if (result?.type !== "success") throw new Error("Subscription authorization failed");
    const { type: ignoredType, provider: ignoredProvider, ...rest } = result;
    const auth = validateAuth(account.provider, { ...rest, type: "key" in rest || key ? "api" : "oauth", ...(key && !rest.key ? { key } : {}) }, session.context.enterpriseHost);
    await scope.run(session.context, () => save(account, auth));
    session.auth = auth;
    session.state = "complete";
    retainFinishedSession(session);
  }

  async function operation(command) {
    if (command.operation === "providers") return { providers: providerIDs };
    const account = await accountFor(command);
    if (command.operation === "describe") return { provider: account.provider, name: account.config.name,
      methods: safeMethods(account.hooks).map(describeMethod), options: describeOptions(account.hooks), refresh_lead_ms: account.hooks.auth?.refreshLead ?? null,
      models: Object.values(account.models).map((model) => ({ ...model, protocol: modelProtocol(model) })),
      capabilities: { models: !!account.hooks.provider?.models, usage: !!account.hooks.auth?.usage, refresh: !!account.hooks.auth?.refresh, checkin: !!account.hooks.auth?.checkin } };
    if (command.operation === "import") { await save(account, validateAuth(command.provider, command.auth)); return { state: "complete", auth: account.auth }; }
    if (command.operation === "models") return models(account);
    if (command.operation === "refresh") { await fresh(account, true); return { auth: account.auth }; }
    if (command.operation === "usage") { await fresh(account); return account.hooks.auth?.usage ? account.hooks.auth.usage(async () => runtimeAuth(account), { id: account.provider, models: account.models }) : { windows: [] }; }
    if (command.operation === "checkin") {
      if (!account.hooks.auth?.checkin) throw failure("operation_not_supported", "This subscription provider does not support check-in");
      await fresh(account);
      return account.hooks.auth.checkin(async () => runtimeAuth(account));
    }
    if (command.operation === "fetch") { await fetchModel(account, command); return undefined; }
    if (command.operation === "auth.start") return startAuth(account, command);
    if (command.operation === "auth.status") {
      const session = sessionFor(command);
      return { session_id: session.id, state: session.state, ...(session.auth ? { auth: session.auth } : {}), ...(session.error ? { error: session.error } : {}) };
    }
    if (command.operation === "auth.cancel") { const session = sessionFor(command); endSession(session); sessions.delete(session.id); return { state: "cancelled" }; }
    if (command.operation === "auth.callback") {
      const session = sessionFor(command);
      if (session.state !== "pending") throw failure("invalid_session", "This authorization session is no longer pending");
      if (command.url) return scope.run(session.context, () => relayCallback(session, command.url));
      if (session.authorization.method !== "code" || session.started) throw failure("invalid_session", "This authorization session is not waiting for a code");
      session.started = true;
      await scope.run(session.context, async () => finishAuth(account, session, await session.authorization.callback(command.code || "")));
      return { state: session.state, auth: session.auth };
    }
    throw failure("operation_not_supported", "Subscription operation is not supported");
  }

  async function dispatch(command) {
    if (command.operation === "auth.ack") { acknowledge(command); return; }
    if (command.operation === "cancel") { active.get(command.target_id || command.id)?.abort(); return; }
    if (typeof command.id !== "string" || !command.id || command.id.length > 256) return;
    const controller = new AbortController();
    active.set(command.id, controller);
    try {
      const result = await scope.run({ id: command.id, operation: command.operation, resourceID: command.resource_id || "catalog", provider: command.provider, controller }, () => operation(command));
      if (command.operation !== "fetch") await emit({ id: command.id, event: "result", result });
    } catch (error) { await emit({ id: command.id, event: "error", error: publicError(error) }); }
    finally { active.delete(command.id); }
  }

  return { dispatch };
}
