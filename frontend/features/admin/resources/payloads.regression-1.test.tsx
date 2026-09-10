import { describe, expect, it } from "vitest";

import { emptyData } from "../domain/catalog";
import { notificationChannelConfig } from "./settings-config";
import { defaultFormValues, readAdminError } from "./payloads";
import { providerConfig } from "./provider-model-config";

describe("Provider model catalog error localization", () => {
  it.each([
    ["provider_models_address_blocked", "sensitive transport details must stay hidden", "Provider 域名解析到被安全策略禁止的地址。如果后端主机使用 Fake-IP 代理，请前往「系统设置 → 基础设置 → 编辑配置」，开启「允许 Provider 使用 Synthetic DNS / Fake-IP」，在「Synthetic DNS / Fake-IP 网段」填写代理实际使用的地址池，保存后重试；未使用 Fake-IP 时请检查 Provider 地址和 DNS。"],
    ["provider_models_dns_failed", "sensitive transport details must stay hidden", "Provider 域名解析失败，请检查后端所在主机的 DNS 配置。"],
    ["provider_models_timeout", "sensitive transport details must stay hidden", "连接上游模型目录超时。如果上游需要代理，请前往「系统设置 → 基础设置 → 编辑配置」，将「Provider 出口模式」设为「使用统一代理」，填写后端主机可访问的代理协议、Host 和端口，保存后重试；仅开启浏览器代理不代表后端已使用代理。"],
    ["provider_models_tls_failed", "sensitive transport details must stay hidden", "上游 TLS 证书验证失败，请检查证书有效期、域名和信任链。"],

    ["provider_models_authentication_failed", "401 Unauthorized", "上游拒绝了 Provider 凭据，请检查 API Key 或认证配置。"],
    ["provider_models_rate_limited", "429 Too Many Requests", "上游模型目录请求过于频繁，请稍后重试。"],
    ["provider_models_upstream_error", "500 Internal Server Error", "上游模型目录加载失败，请检查 Provider 连接配置后重试。"],
    ["provider_models_request_failed", "Failed to request upstream models", "无法连接上游模型目录，请检查 Provider 地址和网络配置后重试。"],
    ["provider_models_invalid_response", "Upstream models response is invalid", "上游模型目录返回了无法识别的数据，请检查 Provider 兼容性。"],
    ["provider_models_empty", "Upstream did not return any models", "上游模型目录未返回任何模型，请检查 Provider 配置或稍后重试。"],
  ])("localizes %s without exposing the backend message", async (code, backendMessage, expected) => {
    const response = new Response(JSON.stringify({ error: { code, message: backendMessage } }), {
      status: 502,
      headers: { "content-type": "application/json" },
    });

    const message = await readAdminError(response, "Provider 模型加载失败");

    expect(message).toBe(expected);
    expect(message).not.toBe(backendMessage);
  });
});

describe("Provider form defaults", () => {
  it("does not synthesize a legacy OpenAI-compatible provider type", () => {
    expect(defaultFormValues(providerConfig(), emptyData()).type).toBe("");
  });

  it("prefers plugin provider types over the legacy OpenAI-compatible fallback", () => {
    const data = emptyData();
    data.providerAdapters = [{
      type: "kimi_subscription",
      capabilities: ["responses"],
      plugin_id: "tokenhub.provider.kimi",
      provider_policy: { route_protocols: ["responses"], supports_custom_headers: true },
    }];

    expect(defaultFormValues(providerConfig(), data).type).toBe("kimi_subscription");
  });
});

describe("Generic form defaults", () => {
  it("does not synthesize a legacy OpenAI-compatible type for notification channels", () => {
    expect(defaultFormValues(notificationChannelConfig(), emptyData()).type).toBe("");
  });
});
