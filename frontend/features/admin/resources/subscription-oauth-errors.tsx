import { tx } from "../i18n/runtime";

export function subscriptionOAuthErrorMessage(code?: string, details?: unknown): string | undefined {
  if (code === "subscription_oauth_authorization_denied") return tx("本次授权未获同意。请重新打开授权，并在登录页同意所需权限。");
  if (code === "subscription_oauth_client_rejected") return tx("上游拒绝了本次注册的授权客户端。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址（保留其中的 client_id 参数）。");
  if (code === "subscription_oauth_exchange_failed") return tx("未能完成账号授权换取凭据。请重新打开授权后再试；若仍失败，请检查账号访问资格和服务器连接。");
  if (code !== "subscription_oauth_code_rejected") return undefined;
  const reason = details && typeof details === "object" && "oauth_reason" in details ? details.oauth_reason : undefined;
  switch (reason) {
    case "code_expired": return tx("授权码已过期。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
    case "code_already_used": return tx("授权码已被使用。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
    case "pkce_verifier_mismatch": return tx("授权码与本次授权会话的校验信息不匹配。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
    case "redirect_uri_mismatch": return tx("回调地址与本次授权登记的地址不匹配。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
    default: return tx("授权码已失效、已被使用或与本次授权不匹配。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
  }
}
