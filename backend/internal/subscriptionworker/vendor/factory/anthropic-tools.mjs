// Factory rejects composition keywords at the tool schema's root. Keep the
// original schema under one argument, and hide that wire-only envelope from
// the caller in both replies and the next turn's history.
const argument = "arguments"
const compositions = ["anyOf", "oneOf", "allOf"]
const schemaMaps = ["properties", "patternProperties", "$defs", "definitions", "dependentSchemas"]
const schemaLists = ["anyOf", "oneOf", "allOf", "prefixItems"]
const schemaValues = ["additionalProperties", "unevaluatedProperties", "propertyNames", "contains", "items", "additionalItems", "unevaluatedItems", "not", "if", "then", "else", "contentSchema"]
const isObject = (v) => v != null && typeof v === "object" && !Array.isArray(v)

export function parseToolJSON(text) {
  return JSON.parse(text, (_key, value, context) => {
    if (typeof value !== "number" || Number.isSafeInteger(value)) return value
    if (context?.source && typeof JSON.rawJSON === "function") return JSON.rawJSON(context.source)
    // Never round a tool's large integer into a different argument on an old
    // runtime that cannot retain JSON number tokens.
    if (!Number.isFinite(value) || Number.isInteger(value)) throw new Error("Factory: this runtime cannot preserve the tool's JSON number")
    return value
  })
}

function rebaseSchema(schema, rebase = true, legacy = false) {
  if (!isObject(schema)) return
  const id = schema.$id ?? (legacy ? schema.id : undefined)
  if (typeof id === "string" && id.split("#")[0]) rebase = false
  if (rebase) {
    // Draft 2019-09's recursive anchor must remain a resource root. Refuse
    // this unsupported relocation instead of changing its resolution scope.
    if (schema.$recursiveAnchor === true || Object.hasOwn(schema, "$recursiveRef")) throw new Error("Factory: a recursive tool schema needs $id before it can be wrapped")
    for (const key of ["$ref", "$dynamicRef"]) {
      const ref = schema[key]
      if (typeof ref !== "string" || !ref.startsWith("#")) continue
      const pointer = decodeURIComponent(ref.slice(1))
      if (pointer === "" || pointer.startsWith("/")) schema[key] = "#/properties/" + argument + ref.slice(1)
    }
  }
  // Visit schema positions only: const/default/examples may contain ordinary
  // data named $ref, properties or anyOf and must not be rewritten.
  for (const key of schemaMaps) {
    if (isObject(schema[key])) for (const child of Object.values(schema[key])) rebaseSchema(child, rebase, legacy)
  }
  for (const key of schemaLists) {
    if (Array.isArray(schema[key])) for (const child of schema[key]) rebaseSchema(child, rebase, legacy)
  }
  for (const key of schemaValues) {
    if (Array.isArray(schema[key])) for (const child of schema[key]) rebaseSchema(child, rebase, legacy)
    else rebaseSchema(schema[key], rebase, legacy)
  }
  if (isObject(schema.dependencies)) for (const child of Object.values(schema.dependencies)) rebaseSchema(child, rebase, legacy)
}

export function wrapAnthropicTools(body) {
  const names = new Set()
  const text = typeof body === "string" ? body
    : body instanceof ArrayBuffer ? Buffer.from(body).toString("utf8")
    : ArrayBuffer.isView(body) ? Buffer.from(body.buffer, body.byteOffset, body.byteLength).toString("utf8") : ""
  let request
  try { request = parseToolJSON(text) } catch (e) {
    if (!(e instanceof SyntaxError)) throw e
    return { body, names }
  }
  if (!Array.isArray(request?.tools) || !Array.isArray(request.messages)) return { body, names }
  for (const tool of request.tools) {
    const schema = tool?.input_schema
    if (typeof tool?.name !== "string" || !isObject(schema) || !compositions.some((key) => Object.hasOwn(schema, key))) continue
    rebaseSchema(schema, true, /draft-0[34]/.test(schema.$schema ?? ""))
    tool.input_schema = { type: "object", properties: { [argument]: schema }, required: [argument], additionalProperties: false }
    if (schema.$schema) tool.input_schema.$schema = schema.$schema
    if (Array.isArray(tool.input_examples)) tool.input_examples = tool.input_examples.map((input) => ({ [argument]: input }))
    names.add(tool.name)
  }
  if (!names.size) return { body, names }
  for (const message of request.messages) {
    if (message?.role !== "assistant" || !Array.isArray(message.content)) continue
    for (const block of message.content) {
      if (block?.type === "tool_use" && names.has(block.name) && Object.hasOwn(block, "input")) block.input = { [argument]: block.input }
    }
  }
  return { body: JSON.stringify(request), names }
}

function unwrap(input, name) {
  if (!isObject(input) || !Object.hasOwn(input, argument) || Object.keys(input).length !== 1) throw new Error(`Factory: tool ${name} returned invalid wrapped arguments`)
  return input[argument]
}

function responseHeaders(res) {
  const headers = new Headers(res.headers)
  headers.delete("content-length")
  headers.delete("content-encoding")
  return { status: res.status, statusText: res.statusText, headers }
}

function toolStream(body, names) {
  const reader = body.getReader()
  const decoder = new TextDecoder("utf-8", { fatal: true })
  const encoder = new TextEncoder()
  const blocks = new Map()
  let pending = "", ended = false
  const incomplete = () => {
    if (blocks.size) throw new Error("Factory: incomplete wrapped tool arguments")
  }
  const frame = (raw, ending) => {
    const lines = raw.split(/\r\n|\n|\r/)
    const data = lines.filter((line) => line.startsWith("data:")).map((line) => line.slice(5).replace(/^ /, "")).join("\n")
    if (!data) return [raw + ending]
    if (data === "[DONE]") { incomplete(); return [raw + ending] }
    const event = parseToolJSON(data)
    const replace = (value) => {
      let first = true
      const next = []
      for (const line of lines) {
        if (!line.startsWith("data:")) next.push(line)
        else if (first) { next.push("data: " + JSON.stringify(value)); first = false }
      }
      return next.join(raw.includes("\r\n") ? "\r\n" : "\n") + ending
    }
    if (event.type === "content_block_start" && event.content_block?.type === "tool_use" && names.has(event.content_block.name)) {
      if (blocks.has(event.index)) throw new Error("Factory: duplicate wrapped tool block")
      blocks.set(event.index, { name: event.content_block.name, json: "", initial: event.content_block.input })
      event.content_block.input = {}
      return [replace(event)]
    }
    const block = blocks.get(event.index)
    if (block && event.type === "content_block_delta" && event.delta?.type === "input_json_delta") {
      if (typeof event.delta.partial_json !== "string") throw new Error("Factory: invalid tool argument delta")
      block.json += event.delta.partial_json
      return []
    }
    if (block && event.type === "content_block_stop") {
      const input = unwrap(block.json ? parseToolJSON(block.json) : block.initial, block.name)
      blocks.delete(event.index)
      const delta = { type: "content_block_delta", index: event.index, delta: { type: "input_json_delta", partial_json: JSON.stringify(input) } }
      return ["event: content_block_delta\ndata: " + JSON.stringify(delta) + "\n\n", raw + ending]
    }
    if (event.type === "error") blocks.clear() // preserve the upstream error
    if (event.type === "message_stop") incomplete()
    return [raw + ending]
  }
  return new ReadableStream({
    async pull(controller) {
      try {
        while (true) {
          const boundary = /\r\n\r\n|\r\n\n|\n\r\n|\n\n|\r\r/.exec(pending)
          if (boundary || (ended && pending)) {
            const raw = boundary ? pending.slice(0, boundary.index) : pending
            const ending = boundary?.[0] ?? ""
            pending = boundary ? pending.slice(boundary.index + ending.length) : ""
            const out = frame(raw, ending)
            for (const value of out) controller.enqueue(encoder.encode(value))
            if (out.length) return
            continue
          }
          if (ended) { incomplete(); controller.close(); return }
          const chunk = await reader.read()
          pending += decoder.decode(chunk.value, { stream: !chunk.done })
          ended = chunk.done
        }
      } catch (e) {
        controller.error(e)
        await reader.cancel(e).catch(() => {})
      }
    },
    cancel(reason) { return reader.cancel(reason) },
  })
}

export async function unwrapAnthropicTools(res, names) {
  if (!names.size || !res.ok || !res.body) return res
  if ((res.headers.get("content-type") ?? "").includes("text/event-stream")) return new Response(toolStream(res.body, names), responseHeaders(res))
  if (!(res.headers.get("content-type") ?? "").includes("json")) return res
  const text = await res.text()
  const message = parseToolJSON(text)
  let changed = false
  for (const block of Array.isArray(message?.content) ? message.content : []) {
    if (block?.type !== "tool_use" || !names.has(block.name)) continue
    block.input = unwrap(block.input, block.name)
    changed = true
  }
  return new Response(changed ? JSON.stringify(message) : text, responseHeaders(res))
}
