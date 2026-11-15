import type { AllowanceConfig } from "./provider-allowance";

// Import only balance settings. Inference credentials, routes and entitlements
// remain under the target account's existing TokenHub configuration.
export function importMagpieBalance(
  raw: string,
  current: AllowanceConfig,
): AllowanceConfig {
  const value: unknown = JSON.parse(raw);
  if (!value || typeof value !== "object" || Array.isArray(value))
    throw new Error("invalid");
  const provider = value as Record<string, unknown>;
  if (typeof provider.balanceURL !== "string" || !provider.balanceURL.trim())
    throw new Error("invalid");
  for (const name of ["balancePath", "balanceMethod", "balanceToken"]) {
    if (provider[name] !== undefined && typeof provider[name] !== "string")
      throw new Error("invalid");
  }
  const headers: Record<string, string> = {};
  if (provider.headers !== undefined) {
    if (
      !provider.headers ||
      typeof provider.headers !== "object" ||
      Array.isArray(provider.headers)
    )
      throw new Error("invalid");
    for (const [name, value] of Object.entries(provider.headers)) {
      if (typeof value !== "string" || value === "[redacted]")
        throw new Error("invalid");
      headers[name] = value;
    }
  }
  const method =
    String(provider.balanceMethod ?? "GET")
      .trim()
      .toUpperCase() || "GET";
  if (method !== "GET" && method !== "POST") throw new Error("invalid");
  const token = String(provider.balanceToken ?? "");
  return {
    ...current,
    token,
    clear_token: !token,
    source: {
      enabled: true,
      auth: "magpie",
      url: provider.balanceURL,
      method,
      expression: String(provider.balancePath ?? ""),
      headers,
      refresh_seconds: current.source.refresh_seconds || 60,
    },
  };
}

export function magpieBalancePlaceholder(raw = ""): string {
  let path = "";
  try {
    path = new URL(raw).pathname.replace(/\/+$/, "");
  } catch {
    /* An unfinished URL has no default yet. */
  }
  if (path === "/api/user/self") return "$data.quota / 500000";
  if (path === "/api/usage/token") return "$data.total_available / 500000";
  if (path === "/api/v1/user/profile") return "$data.balance";
  if (path === "/v1/usage") return "$remaining";
  if (path.endsWith("/dashboard/billing/credit_grants"))
    return "$total_available";
  return "data.balance";
}
