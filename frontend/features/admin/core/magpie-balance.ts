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
