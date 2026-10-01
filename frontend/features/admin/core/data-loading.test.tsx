import { describe, expect, it } from "vitest";
import { loadPlanForView, type LoadPlan } from "./data-loading";
import { roleViewAccess } from "./navigation";
import { type AdminUser, type AppRole } from "./types";

const pluginFlags = ["plugins", "pluginMarketplace", "pluginChain", "pluginUI", "pluginActions", "pluginBackgroundJobs"] as const;

function userWithRole(role: AppRole): AdminUser {
  return { id: `usr_${role}`, username: role, email: `${role}@tokenhub.local`, role, status: "active" } as AdminUser;
}

function requestedPluginFlags(plan: LoadPlan) {
  return pluginFlags.filter((flag) => plan[flag]);
}

describe("loadPlanForView", () => {
  it.each(["user", "team_leader", "security"] as const)("never requests plugin endpoints for the %s role", (role) => {
    const user = userWithRole(role);
    for (const view of roleViewAccess[role]) {
      expect({ view, flags: requestedPluginFlags(loadPlanForView(user, view)) }).toEqual({ view, flags: [] });
    }
  });

  it("keeps loading plugin data for admins", () => {
    const admin = userWithRole("admin");

    expect(requestedPluginFlags(loadPlanForView(admin, "overview"))).toEqual([...pluginFlags]);
    expect(requestedPluginFlags(loadPlanForView(admin, "usage"))).toEqual(["pluginUI", "pluginActions", "pluginBackgroundJobs"]);
  });
});
