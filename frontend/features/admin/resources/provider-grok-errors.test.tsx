import { afterEach, describe, expect, it } from "vitest";
import { setActiveLanguage, type AppLanguage } from "../i18n/runtime";
import { readAdminError } from "./payloads";
import { readPluginActionError } from "./provider-model-config";

afterEach(() => setActiveLanguage("en"));

describe("Grok subscription inference permission guidance", () => {
  const locales: [AppLanguage, string][] = [
    ["zh-CN", "Grok 授权缺少模型调用所需的 api:access 权限。请编辑此账号，重新授权并同意所需权限，然后保存账号。"],
    ["en", "The Grok authorization is missing the api:access permission required for model requests. Edit this account, authorize it again with the required permission, then save the account."],
    ["ja", "Grok の認可に、モデルの呼び出しに必要な api:access 権限がありません。このアカウントを編集し、必要な権限に同意して再認可した後、アカウントを保存してください。"],
    ["ru", "В авторизации Grok отсутствует разрешение api:access, необходимое для запросов к моделям. Отредактируйте этот аккаунт, повторно авторизуйте его с необходимым разрешением, затем сохраните аккаунт."],
  ];

  it.each(locales)("localizes provider and account probe errors in %s", async (language, expected) => {
    setActiveLanguage(language);
    const response = () => new Response(JSON.stringify({ error: { code: "grok_api_scope_missing", message: "Synthetic upstream diagnostic" } }), { status: 409 });
    expect(await readAdminError(response(), "fallback")).toBe(expected);
    expect(await readPluginActionError(response(), undefined, "fallback")).toBe(expected);
  });

  it("does not change unrelated authentication errors", async () => {
    const message = "The gateway's credential for this provider was rejected";
    const response = new Response(JSON.stringify({ error: { code: "provider_auth_error", message } }), { status: 502 });
    expect(await readAdminError(response, "fallback")).toBe(message);
  });
});
