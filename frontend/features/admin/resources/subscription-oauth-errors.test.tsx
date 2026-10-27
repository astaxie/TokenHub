import { afterEach, describe, expect, it } from "vitest";
import { setActiveLanguage, type AppLanguage } from "../i18n/runtime";
import { readAdminError } from "./payloads";
import { subscriptionOAuthErrorMessage } from "./subscription-oauth-errors";

afterEach(() => setActiveLanguage("en"));

describe("Subscription OAuth exchange error guidance", () => {
  it.each([
    ["code_expired", "授权码已过期。"],
    ["code_already_used", "授权码已被使用。"],
    ["pkce_verifier_mismatch", "授权码与本次授权会话的校验信息不匹配。"],
    ["redirect_uri_mismatch", "回调地址与本次授权登记的地址不匹配。"],
  ])("uses the safe reason enum %s without displaying upstream diagnostics", async (oauthReason, expected) => {
    setActiveLanguage("zh-CN");
    const response = new Response(JSON.stringify({ error: { code: "subscription_oauth_code_rejected", message: "private-upstream-body", details: { oauth_reason: oauthReason, diagnostic: "private-diagnostic" } } }), { status: 409 });
    const message = await readAdminError(response, "fallback");
    expect(message).toContain(expected);
    expect(message).toContain("完整回调地址");
    expect(message).not.toMatch(/private-|账号会话已失效/);
  });

  it.each([undefined, null, "code_expired", { oauth_reason: "private-unknown-reason" }, { oauth_reason: ["code_expired"] }])("keeps unknown or malformed details out of the error message", details => {
    setActiveLanguage("zh-CN");
    expect(subscriptionOAuthErrorMessage("subscription_oauth_code_rejected", details)).toBe("授权码已失效、已被使用或与本次授权不匹配。请重新打开授权并完成登录；仅在页面要求手动回填时，粘贴本次登录生成的完整回调地址。");
  });

  const locales: AppLanguage[] = ["zh-CN", "en", "ja", "ru"];
  it.each(locales)("localizes fixed errors in %s", language => {
    setActiveLanguage(language);
    for (const code of ["subscription_oauth_code_rejected", "subscription_oauth_client_rejected", "subscription_oauth_exchange_failed", "subscription_oauth_authorization_denied"]) {
      const message = subscriptionOAuthErrorMessage(code);
      expect(message).toBeTruthy();
      if (language !== "zh-CN") expect(message).not.toContain("请重新打开授权");
    }
    for (const oauthReason of ["code_expired", "code_already_used", "pkce_verifier_mismatch", "redirect_uri_mismatch"]) {
      const message = subscriptionOAuthErrorMessage("subscription_oauth_code_rejected", { oauth_reason: oauthReason });
      expect(message).toBeTruthy();
      if (language !== "zh-CN") expect(message).not.toContain("请重新打开授权");
    }
  });

  it("keeps registered client guidance separate from saved-session expiry", () => {
    setActiveLanguage("en");
    expect(subscriptionOAuthErrorMessage("subscription_oauth_client_rejected")).toContain("including its client_id parameter");
    expect(subscriptionOAuthErrorMessage("subscription_oauth_client_rejected")).not.toContain("secret");
    expect(subscriptionOAuthErrorMessage("provider_resource_reauthorization_required")).toBeUndefined();
  });

  it.each<[AppLanguage, string]>([
    ["zh-CN", "仅在页面要求手动回填时"],
    ["en", "Only if the page asks for a manual callback"],
    ["ja", "手動入力を求められた場合にのみ"],
    ["ru", "Только если страница запрашивает ручной ввод"],
  ])("makes manual callback instructions conditional in %s", (language, condition) => {
    setActiveLanguage(language);
    expect(subscriptionOAuthErrorMessage("subscription_oauth_client_rejected")).toContain(condition);
    for (const oauthReason of [undefined, "code_expired", "code_already_used", "pkce_verifier_mismatch", "redirect_uri_mismatch"]) {
      expect(subscriptionOAuthErrorMessage("subscription_oauth_code_rejected", { oauth_reason: oauthReason })).toContain(condition);
    }
  });
});
