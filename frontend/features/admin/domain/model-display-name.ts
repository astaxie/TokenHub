export function modelDisplayName(metadata: Record<string, string> | undefined, fallback: string) {
  return metadata?.title?.trim() || fallback;
}

export function modelMetadataWithDisplayName(existing: Record<string, string> | undefined, displayName: string) {
  const metadata = { ...(existing ?? {}) };
  const title = displayName.trim();
  if (title) metadata.title = title;
  else delete metadata.title;
  return metadata;
}

export function modelMetadataPayload(existing: Record<string, string> | undefined, displayName: string) {
  const metadata = modelMetadataWithDisplayName(existing, displayName);
  return existing !== undefined || Object.keys(metadata).length > 0 ? { metadata } : {};
}

export function modelDirectorySubtitle(modelID: string, displayName: string, aliasLabel = "") {
  return [displayName !== modelID ? modelID : "", aliasLabel].filter(Boolean).join(" · ");
}
