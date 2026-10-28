import { describe, expect, it } from "vitest";
import { parseProviderAccountOAuthResult } from "./session";

describe("Provider OAuth identity parsing", () => {
  it("keeps the provider account and user subject distinct", () => {
    const result = parseProviderAccountOAuthResult("https://tokenhub.example.test/callback?access_token=synthetic-token&account_id=synthetic-workspace&user_id=synthetic-user", true);
    expect(result).toMatchObject({ access_token: "synthetic-token", account_id: "synthetic-workspace", user_id: "synthetic-user" });
  });

  it.each(["user_id", "sub"])("preserves legacy %s fallback while keeping the subject available", key => {
    const result = parseProviderAccountOAuthResult(`https://tokenhub.example.test/callback?access_token=synthetic-token&${key}=synthetic-subject`, true);
    expect(result).toMatchObject({ user_id: "synthetic-subject", account_id: "synthetic-subject" });
  });
});
