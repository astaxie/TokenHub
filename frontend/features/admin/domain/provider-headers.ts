export type ProviderHeaderEntry = {
  name: string;
  value: string;
  sensitive: boolean;
  retained?: boolean;
};

export const providerHeaderMask = "••••••••";
const reservedProviderHeaders = new Set([
  "accept-encoding", "anthropic-beta", "anthropic-version", "api-key", "authorization", "connection", "content-length", "content-type", "cookie", "cookie2", "expect", "forwarded", "host", "keep-alive", "openai-organization", "openai-project", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade", "via", "x-api-key", "x-forwarded-for", "x-forwarded-host", "x-forwarded-port", "x-forwarded-proto", "x-goog-api-key", "x-real-ip", "x-tokenhub-upstream-account",
]);

export function providerHeadersFormValue(headers?: Record<string, string>, sensitiveHeaders: string[] = []) {
  const sensitive = new Set(sensitiveHeaders.map((name) => name.toLowerCase()));
  return JSON.stringify(Object.entries(headers ?? {}).map(([name, value]) => ({
    name,
    value,
    sensitive: sensitive.has(name.toLowerCase()),
    retained: sensitive.has(name.toLowerCase()) && value === providerHeaderMask,
  } satisfies ProviderHeaderEntry)));
}

export function parseProviderHeaderEntries(value?: string): ProviderHeaderEntry[] {
  if (!value?.trim()) return [];
  try {
    const parsed = JSON.parse(value) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.map((entry) => {
      const item = entry as Partial<ProviderHeaderEntry>;
      return { name: String(item.name ?? ""), value: String(item.value ?? ""), sensitive: item.sensitive === true, retained: item.retained === true };
    });
  } catch {
    return [];
  }
}

export function serializeProviderHeaderEntries(entries: ProviderHeaderEntry[]) {
  return JSON.stringify(entries);
}

function validProviderHeaderValue(value: string) {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code === 0x09 || (code >= 0x20 && code !== 0x7f)) continue;
    return false;
  }
  return true;
}

export function providerHeadersPayload(value?: string) {
  const entries = parseProviderHeaderEntries(value);
  return {
    headers: Object.fromEntries(entries.map((entry) => [entry.name.trim(), entry.value])),
    sensitive_headers: entries.filter((entry) => entry.sensitive).map((entry) => entry.name.trim()),
  };
}

export function providerHeaderEntryErrors(entries: ProviderHeaderEntry[]) {
  const errors: string[] = [];
  const seen = new Set<string>();
  const encoder = new TextEncoder();
  let totalBytes = 0;
  if (entries.length > 32) errors.push("自定义请求头最多可配置 32 项。");
  for (const entry of entries) {
    const name = entry.name.trim();
    const comparable = name.toLowerCase();
    if (!name) errors.push("请求头名称不能为空。");
    else if (seen.has(comparable)) errors.push("请求头名称不能重复（大小写不敏感）。");
    else if (encoder.encode(name).length > 128 || !/^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(name)) errors.push("请求头名称格式不合法。");
    else if (reservedProviderHeaders.has(comparable)) errors.push("该请求头由 TokenHub 管理，不能覆盖。");
    else seen.add(comparable);
    if ((!entry.value || entry.value === providerHeaderMask) && (!entry.sensitive || !entry.retained)) errors.push("请求头值不能为空。");
    if (!validProviderHeaderValue(entry.value)) errors.push("请求头值不能包含非法控制字符。");
    if (encoder.encode(entry.value).length > 4096) errors.push("单个请求头值不能超过 4 KiB。");
    totalBytes += encoder.encode(name).length + encoder.encode(entry.value).length;
  }
  if (totalBytes > 16 * 1024) errors.push("自定义请求头总大小不能超过 16 KiB。");
  return errors;
}

export function providerHeaderFormError(value?: string) {
  return providerHeaderEntryErrors(parseProviderHeaderEntries(value))[0] ?? "";
}

export function effectiveProviderHeaderEntries(inherited: ProviderHeaderEntry[], overrides: ProviderHeaderEntry[]) {
  const merged = new Map<string, ProviderHeaderEntry>();
  for (const entry of inherited) merged.set(entry.name.toLowerCase(), entry);
  for (const entry of overrides) merged.set(entry.name.toLowerCase(), entry);
  return Array.from(merged.values());
}
