import { ID, INFERENCE, WELCOME, LEAD, AuthError, createAuth, metadata } from "./oauth.mjs"

const NPM = "@ai-sdk/openai-compatible"
const PATHS = new Set(["/v1/chat/completions", "/v1/completions", "/v1/embeddings", "/v1/models"])

function target(input) {
  const u = new URL(input)
  if (![new URL(INFERENCE).origin, new URL(WELCOME).origin].includes(u.origin) ||
      u.username || u.password || u.hash || !PATHS.has(u.pathname)) throw new AuthError("refusing_inference_destination")
  return u.pathname.slice(3) + u.search
}

function abortable(work, signal) {
  signal.throwIfAborted()
  return new Promise((resolve, reject) => {
    const stop = () => reject(signal.reason)
    signal.addEventListener("abort", stop, { once: true })
    Promise.resolve().then(() => { signal.throwIfAborted(); return work() }).then(resolve, reject).finally(() => signal.removeEventListener("abort", stop))
  })
}

function model(provider, id, baseURL) {
  const known = provider.models?.[id]
  return {
    id, providerID: provider.id, name: known?.name ?? id,
    api: { id, url: baseURL, npm: NPM }, status: "active", headers: known?.headers ?? {}, options: known?.options ?? {},
    cost: known?.cost ?? { input: 0, output: 0, cache: { read: 0, write: 0 } },
    limit: known?.limit ?? { context: 0, output: 0 },
    capabilities: known?.capabilities ?? {
      temperature: true, reasoning: false, attachment: false, toolcall: true,
      input: { text: true, image: false, audio: false, video: false, pdf: false },
      output: { text: true, image: false, audio: false, video: false, pdf: false }, interleaved: false,
    },
    release_date: known?.release_date ?? "", variants: known?.variants ?? {},
  }
}

export async function NousPortalAuthPlugin({ client }) {
  const auth = createAuth(client)
  async function signedFetch(getAuth, input, init) {
    const req = new Request(input, init)
    const path = target(req.url) // refuse foreign destinations before even refreshing
    req.signal.throwIfAborted()
    // These endpoints take JSON, not uploads. Keep the exact request bytes for
    // at most one 401 replay; the upstream RESPONSE is never buffered or parsed.
    const body = req.body ? await req.arrayBuffer() : undefined
    let a = await abortable(() => auth.fresh(getAuth), req.signal)
    const send = (current) => {
      req.signal.throwIfAborted()
      const headers = new Headers(req.headers)
      for (const name of [...headers.keys()]) {
        if (["authorization", "x-api-key", "x-nous-refresh-token", "cookie", "proxy-authorization", "content-length", "host", "connection", "transfer-encoding", "upgrade", "te", "trailer", "proxy-connection", "keep-alive"].includes(name) || name.startsWith("x-magpie-")) headers.delete(name)
      }
      headers.set("Authorization", "Bearer " + current.access)
      return fetch(metadata(current).baseURL + path, {
        method: req.method, headers, body, signal: req.signal, redirect: "error",
      })
    }
    let renewed = a.signIn === "renewed"
    let res = await send(a)
    if (res.status === 401) {
      await res.body?.cancel()
      a = await abortable(() => auth.fresh(getAuth, a.access), req.signal)
      renewed ||= a.signIn === "renewed"
      res = await send(a)
    }
    const headers = new Headers(res.headers)
    // fetch decodes the body; the upstream wire encoding and size no longer apply.
    headers.delete("content-encoding")
    headers.delete("content-length")
    headers.set("X-Magpie-Sign-In", renewed && res.ok ? "renewed" : "kept")
    return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
  }
  return {
    config: async (config) => {
      config.provider ??= {}
      const p = config.provider[ID] ??= {}
      p.name ??= "Nous Portal (OAuth)"
      p.npm ??= NPM
      p.api ??= INFERENCE
      // Bootstrap only, from Hermes's subscription-proxy documentation. A
      // successful live catalog REPLACES it, even when the account has no models.
      p.models = { "Hermes-4-70B": { name: "Hermes-4-70B", limit: { context: 0, output: 0 } }, ...(p.models ?? {}) }
    },
    provider: {
      id: ID,
      async models(provider, { auth: saved }) {
        if (saved?.type !== "oauth") return provider.models
        // Magpie calls auth.refresh before this hook. Only an auth SNAPSHOT
        // is supplied here, not getAuth: never redeem or persist through that
        // snapshot, which cannot notice a concurrent logout or new login.
        const a = auth.current(saved)
        const baseURL = metadata(a).baseURL
        let res
        try {
          res = await fetch(baseURL + "/models", {
            headers: { Accept: "application/json", Authorization: "Bearer " + a.access },
            signal: AbortSignal.timeout(15000), redirect: "error",
          })
        } catch { throw new AuthError("models_unavailable") }
        if (!res.ok) throw new AuthError("models_unavailable", false, res.status)
        let body
        try { body = await res.json() } catch { throw new AuthError("invalid_models_response") }
        if (!Array.isArray(body?.data)) throw new AuthError("invalid_models_response")
        const out = []
        for (const m of body.data) {
          if (typeof m?.id !== "string" || !m.id.trim()) throw new AuthError("invalid_models_response")
          out.push([m.id, model(provider, m.id, baseURL)])
        }
        return Object.fromEntries(out)
      },
    },
    auth: {
      provider: ID, icon: "nousresearch", refreshLead: LEAD,
      refresh: auth.refresh,
      async loader(getAuth) {
        if ((await auth.read(getAuth))?.type !== "oauth") return {}
        return {
          baseURL: INFERENCE, apiKey: "nous-portal-oauth",
          fetch: (input, init) => signedFetch(getAuth, input, init),
        }
      },
      methods: [{ type: "oauth", label: "Nous Portal (device code)", authorize: auth.authorize }],
    },
  }
}
