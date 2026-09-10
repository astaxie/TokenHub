import { tx } from "../i18n/runtime";

export function providerBlockedAddressMessage(code?: string, details?: unknown): string | undefined {
  if (code !== "provider_models_address_blocked" && code !== "provider_address_blocked") return undefined;
  const guidance = tx("Provider 域名解析到被安全策略禁止的地址。如果后端主机使用 Fake-IP 代理，请前往「系统设置 → 基础设置 → 编辑配置」，开启「允许 Provider 使用 Synthetic DNS / Fake-IP」，在「Synthetic DNS / Fake-IP 网段」填写代理实际使用的地址池，保存后重试；未使用 Fake-IP 时请检查 Provider 地址和 DNS。");
  const raw = details && typeof details === "object" && "blocked_ips" in details ? details.blocked_ips : undefined;
  const addresses = Array.isArray(raw)
    ? [...new Set(raw.filter((value): value is string => typeof value === "string" && value.length <= 45 && /^[0-9a-f:.]+$/i.test(value)))].slice(0, 16)
    : [];
  return addresses.length
    ? tx("被拒绝的 IP：{addresses}。{guidance}").replace("{addresses}", addresses.join(", ")).replace("{guidance}", guidance)
    : guidance;
}
