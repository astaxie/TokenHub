import { afterEach, describe, expect, it, vi } from "vitest";
import type { AdminResource, AdminUser } from "../core/types";
import { emptyData } from "../domain/catalog";
import { setActiveLanguage } from "../i18n/runtime";
import { resourceConfigFor } from "./settings-config";

const admin: AdminUser = { id: "usr_admin", username: "admin", name: "Admin", email: "admin@example.test", role: "admin", status: "active", team_id: "team_a" };
const template: AdminResource = { id: "quota_default", kind: "quota-policies", name: "Default user quota", status: "active", fields: { scope: "user", scope_id: "all_users", monthly_cost_usd: 50 } };
const config = resourceConfigFor("quota-policies")!;

afterEach(() => {
  vi.unstubAllGlobals();
  setActiveLanguage("zh-CN");
});

describe("default user quota configuration", () => {
  it.each(["admin", "system_admin"])("offers a default template to %s without loading users", (role) => {
    const field = config.fields.find((item) => item.key === "scope_id")!;
    const options = field.optionsFromData!(emptyData(), { ...admin, role }, { scope: "user" });
    expect(options).toContainEqual({ value: "all_users", label: "所有用户（每人独立额度）" });
  });

  it.each(["team_leader", "user", "security"])("does not offer a global template to %s", (role) => {
    const data = emptyData();
    data.users = [admin, { ...admin, id: "usr_other", team_id: "team_b" }];
    const field = config.fields.find((item) => item.key === "scope_id")!;
    const options = field.optionsFromData!(data, { ...admin, role }, { scope: "user" });
    expect(options.map((option) => option.value)).not.toContain("all_users");
    expect(options.map((option) => option.value)).not.toContain("usr_other");
  });

  it.each(["en", "ja"] as const)("localizes the template and explains separate usage in %s", (language) => {
    setActiveLanguage(language);
    const field = config.fields.find((item) => item.key === "scope_id")!;
    const option = field.optionsFromData!(emptyData(), admin, { scope: "user" })[0];
    expect(option.label).not.toBe("所有用户（每人独立额度）");
    const usage = config.columns.find((column) => column.key === "current_usage")!;
    expect(usage.render!(template, emptyData())).toBe(language === "en" ? "Tracked separately per user" : "ユーザーごとに個別集計");
    const fields = config.columns.find((column) => column.key === "fields")!;
    expect(fields.render!(template, emptyData())).toContain(option.label);
    expect(fields.render!(template, emptyData())).not.toContain("all_users");
  });

  it("preserves the explicit template target when creating and editing a policy", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify(template), { status: 200 })));
    vi.stubGlobal("fetch", fetchMock);
    const api = { baseURL: "http://quota.test", adminToken: "synthetic-session" };
    const values = config.toForm!(template);
    expect(values.scope_id).toBe("all_users");
    await config.create!(api, values);
    await config.update!(api, template, { ...values, monthly_cost_usd: "25" });
    for (const [index, cost] of [50, 25].entries()) {
      const request = fetchMock.mock.calls[index][1] as RequestInit;
      expect(request.method).toBe(index === 0 ? "POST" : "PATCH");
      expect(JSON.parse(String(request.body))).toMatchObject({ fields: { scope: "user", scope_id: "all_users", monthly_cost_usd: cost } });
    }
  });
});
