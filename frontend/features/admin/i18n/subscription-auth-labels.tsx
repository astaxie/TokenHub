import { tx } from "./runtime";

export function subscriptionAuthLabel(providerType: string, label: string) {
  if (!["antigravity_subscription", "gemini_cli_subscription"].includes(providerType)) return label;
  switch (label) {
    case "Google account": return tx("Google 账号（默认）");
    case "Google account with Cloud project (advanced)": return tx("Google 账号（指定 Cloud 项目）");
    case "Google OAuth client":
    case "Custom Google OAuth client (advanced)": return tx("自定义 Google OAuth（高级）");
    case "Google OAuth client ID": return tx("Google OAuth 客户端 ID");
    case "Google OAuth client secret": return tx("Google OAuth 客户端密钥");
    case "Google Cloud project ID (optional; required for some accounts)": return tx("Google Cloud 项目 ID（可选，部分账号需要）");
    default: return label;
  }
}
