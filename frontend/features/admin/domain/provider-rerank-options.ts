export function rerankFormValues(options: Record<string, string> = {}) {
  return { rerank_protocol: options.rerank_protocol ?? "", rerank_path: options.rerank_path ?? "" };
}
export function rerankOptions(values: Record<string, string>) {
  return Object.fromEntries(["rerank_protocol", "rerank_path"].filter((key) => values[key] !== undefined).map((key) => [key, values[key].trim()]));
}
