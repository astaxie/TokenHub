import { describe, expect, it } from "vitest";

import { emptyData } from "../domain/catalog";
import { defaultFormValues, readAdminError } from "./payloads";
import { providerConfig } from "./provider-model-config";

describe("Provider model catalog error localization", () => {
  it.each([
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
