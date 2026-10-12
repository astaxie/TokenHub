export const embeddingOptionKeys = ["embedding_protocol", "embedding_path", "embedding_spaces"] as const;
export function embeddingFormValues(options: Record<string, string> = {}) {
  return Object.fromEntries(embeddingOptionKeys.map((key) => [key, options[key] ?? ""]));
}
export function embeddingOptions(values: Record<string, string>) {
  return Object.fromEntries(embeddingOptionKeys.filter((key) => values[key] !== undefined).map((key) => [key, values[key].trim()]));
}
