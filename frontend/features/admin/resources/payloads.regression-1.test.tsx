import { describe, expect, it } from "vitest";

import { readAdminError } from "./payloads";

describe("Provider model catalog error localization", () => {
  it.each([
    ["provider_models_authentication_failed", "上游拒绝了 Provider 凭据，请检查 API Key 或认证配置。"],
    ["provider_models_rate_limited", "上游模型目录请求过于频繁，请稍后重试。"],
    ["provider_models_upstream_error", "上游模型目录加载失败，请检查 Provider 连接配置后重试。"],
  ])("localizes %s without exposing the upstream status", async (code, expected) => {
    const response = new Response(JSON.stringify({ error: { code, message: "401 Unauthorized" } }), {
      status: 502,
      headers: { "content-type": "application/json" },
    });

    const message = await readAdminError(response, "Provider 模型加载失败");

    expect(message).toBe(expected);
    expect(message).not.toContain("401 Unauthorized");
  });
});
