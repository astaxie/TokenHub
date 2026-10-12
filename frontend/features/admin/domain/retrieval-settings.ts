export type RetrievalKind = "embedding" | "rerank";
const compatibleTypes = new Set(["openai", "openai-compatible", "local", "qwen", "deepseek"]);

export function retrievalSettings(values: Record<string, string>, kind: RetrievalKind) {
  const catalog = values.catalog_id?.trim() ?? "";
  let protocol = values[`${kind}_protocol`]?.trim() ?? "";
  const managed = !compatibleTypes.has(values.type || "openai-compatible");
  if (!protocol && !managed) {
    if (kind === "embedding") protocol = ({ cohere: "cohere", voyage: "voyage", voyageai: "voyage", jina: "jina" } as Record<string, string>)[catalog] ?? "openai";
    else {
      protocol = ({ cohere: "cohere", voyage: "voyage", voyageai: "voyage", jina: "jina", siliconflow: "jina", "siliconflow-cn": "jina" } as Record<string, string>)[catalog] ?? "";
      if (!protocol && ["openai-compatible", "local"].includes(values.type || "openai-compatible") && ["", "custom", "local", "openai-compatible"].includes(catalog)) protocol = "jina";
    }
  }
  const defaultPath = kind === "embedding"
    ? protocol === "dashscope" ? "/services/embeddings/text-embedding/text-embedding" : ["cohere", "tei"].includes(protocol) ? "/embed" : "/embeddings"
    : protocol === "dashscope" ? "/services/rerank/text-rerank/text-rerank" : protocol === "qwen" ? "/reranks" : "/rerank";
  const path = values[`${kind}_path`]?.trim() || defaultPath;
  const invalidPath = !path.startsWith("/") || path.startsWith("//") || /[?#]|\.\./.test(path);
  let endpoint = "";
  const base = values.base_url?.trim().replace(/\/+$/, "") ?? "";
  try {
    const url = new URL(base);
    if (!managed && protocol && !invalidPath && ["http:", "https:"].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash) endpoint = base + path;
  } catch { /* An incomplete Base URL has no preview. */ }
  return { protocol, managed, defaultPath, endpoint, invalidPath };
}

export function validEmbeddingSpaces(value = "") {
  if (!value.trim()) return true;
  try {
    const parsed: unknown = JSON.parse(value);
    return !!parsed && typeof parsed === "object" && !Array.isArray(parsed) && Object.entries(parsed).every(([key, space]) => key.trim() && typeof space === "string" && space.trim());
  } catch { return false; }
}

// Discover models using the selected template, while the server preserves the
// saved runtime catalog on same-type edits, including obsolete catalog IDs.
export function preserveRetrievalCatalog(payload: Record<string, unknown>, provider: { type: string; options?: Record<string, string> }) {
  if (!compatibleTypes.has(provider.type) || (payload.type !== undefined && payload.type !== provider.type)) return payload;
  return { ...payload, preserve_catalog: true };
}
