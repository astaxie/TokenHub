import { afterEach, describe, expect, it } from "vitest";
import { setActiveLanguage, type AppLanguage } from "../i18n/runtime";
import { readAdminError } from "./payloads";
import { readPluginActionError } from "./provider-model-config";

afterEach(() => setActiveLanguage("en"));

describe("Google subscription regional eligibility guidance", () => {
  const locales: [AppLanguage, string][] = [
    ["zh-CN", "Google 订阅服务不支持当前请求所在地区。请检查账号的地区资格以及 TokenHub 服务器的网络出口地区。"],
    ["en", "Google subscription access is unavailable in the location associated with this request. Check the account's regional eligibility and the TokenHub server's outbound location."],
    ["ja", "このリクエストに関連する地域では Google サブスクリプションサービスを利用できません。アカウントの地域別の利用資格と TokenHub サーバーの接続元地域を確認してください。"],
    ["ru", "Доступ к подписке Google недоступен в регионе, связанном с этим запросом. Проверьте региональную доступность для аккаунта и регион исходящего подключения сервера TokenHub."],
  ];

  it.each(locales)("localizes provider and account probe errors in %s", async (language, expected) => {
    setActiveLanguage(language);
    const response = () => new Response(JSON.stringify({ error: { code: "google_subscription_location_unsupported", message: "Synthetic upstream diagnostic" } }), { status: 502 });
    expect(await readAdminError(response(), "fallback")).toBe(expected);
    expect(await readPluginActionError(response(), undefined, "fallback")).toBe(expected);
  });

  it("preserves unrelated authentication errors", async () => {
    const message = "The gateway's credential for this provider was rejected";
    const response = new Response(JSON.stringify({ error: { code: "provider_auth_error", message } }), { status: 502 });
    expect(await readAdminError(response, "fallback")).toBe(message);
  });
});
