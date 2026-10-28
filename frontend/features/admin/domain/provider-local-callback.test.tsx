import { describe, expect, it } from "vitest";
import { supportsLocalSubscriptionCallback } from "./provider-local-callback";

describe("Local subscription callback eligibility", () => {
  it.each([
    ["http://localhost:8080", "http://localhost:3000/providers"],
    ["http://127.0.0.1:8080", "http://localhost:3000/providers"],
    ["http://[::1]:8080", "http://[::1]:3000/providers"],
  ])("allows an explicit local API %s and browser %s", (api, browser) => {
    expect(supportsLocalSubscriptionCallback("chatgpt-subscription", api, browser)).toBe(true);
  });

  it.each([
    ["http://localhost:8080", undefined],
    ["http://localhost:8080", "https://console.example.test/providers"],
    ["http://localhost:8080", "https://localhost:3000/providers"],
    ["http://localhost:8080", "http://localhost.example.test/providers"],
    ["http://localhost:8080", "file:///providers"],
    ["http://localhost:8080", "not-a-browser-url"],
    ["https://localhost:8080", "http://localhost:3000/providers"],
    ["http://api.example.test", "http://localhost:3000/providers"],
    ["http://192.168.1.2:8080", "http://localhost:3000/providers"],
    ["http://localhost.example.test", "http://localhost:3000/providers"],
    ["http://user:password@localhost:8080", "http://localhost:3000/providers"],
    ["http://localhost:8080", "http://user:password@localhost:3000/providers"],
    ["//localhost:8080", "http://localhost:3000/providers"],
    ["/api", "http://localhost:3000/providers"],
    ["", "http://localhost:3000/providers"],
    ["not-an-api-url", "http://localhost:3000/providers"],
  ])("rejects API %s and browser %s", (api, browser) => {
    expect(supportsLocalSubscriptionCallback("chatgpt-subscription", api, browser)).toBe(false);
  });

  it.each(["openai-codex", "workbuddy_subscription", "antigravity_subscription", "grok_subscription"])("does not alter %s authorization", providerType => {
    expect(supportsLocalSubscriptionCallback(providerType, "http://localhost:8080", "http://localhost:3000/providers")).toBe(false);
  });
});
