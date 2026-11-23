// Next does not rewrite raw public-file references for basePath, so views that
// hard-code "/brand/..." or catalog-provided "/provider-icons/..." must prefix it.
const basePath = process.env.NEXT_PUBLIC_TOKENHUB_BASE_PATH?.trim().replace(/\/+$/, "") ?? "";

export function withBasePath(path: string) {
  if (!basePath || !path.startsWith("/") || path.startsWith("//")) return path;
  if (path === basePath || path.startsWith(`${basePath}/`)) return path;
  return `${basePath}${path}`;
}
