import assert from "node:assert/strict";
import test from "node:test";
import { importTypeScript } from "./typescript-test-loader.mjs";

const { apiKeyCanManage } = await importTypeScript(new URL("./api-key-management-authz.ts", import.meta.url));

const baseData = {
  projects: [{ id: "prj_authz", name: "Authz Project", status: "active" }],
  resources: {
    "project-members": [
      {
        id: "member_viewer",
        status: "active",
        fields: { project_id: "prj_authz", user_id: "usr_viewer", role: "viewer" },
      },
      {
        id: "member_maintainer",
        status: "active",
        fields: { project_id: "prj_authz", user_id: "usr_maintainer", role: "maintainer" },
      },
    ],
  },
};

const adminIssuedKey = {
  id: "key_admin_issued",
  project_id: "prj_authz",
  owner_user_id: "usr_viewer",
  metadata: { created_by: "usr_admin" },
};

test("API key management actions stay hidden from assigned non-creators", () => {
  assert.equal(apiKeyCanManage(baseData, adminIssuedKey, { id: "usr_viewer", role: "user" }), false);
});

test("API key management actions remain visible to creators and privileged project users", () => {
  assert.equal(apiKeyCanManage(baseData, { ...adminIssuedKey, metadata: { created_by: "usr_creator" } }, { id: "usr_creator", role: "user" }), true);
  assert.equal(apiKeyCanManage(baseData, adminIssuedKey, { id: "usr_maintainer", role: "user" }), true);
  assert.equal(apiKeyCanManage(baseData, adminIssuedKey, { id: "usr_admin", role: "admin" }), true);
});

test("legacy assigned API keys fail closed for ordinary users", () => {
  assert.equal(apiKeyCanManage(baseData, { ...adminIssuedKey, metadata: undefined }, { id: "usr_viewer", role: "user" }), false);
});

test("team-based API key management ignores disabled teams", () => {
  const teamKey = { ...adminIssuedKey, project_id: "prj_team" };
  const teamData = {
    projects: [{
      id: "prj_team",
      name: "Team Project",
      status: "active",
      teams: [
        { project_id: "prj_team", team_id: "team_active", role: "maintainer" },
        { project_id: "prj_team", team_id: "team_disabled", role: "maintainer" },
      ],
    }],
    resources: {
      teams: [
        { id: "team_active", status: "active" },
        { id: "team_disabled", status: "disabled" },
      ],
    },
  };
  assert.equal(apiKeyCanManage(teamData, teamKey, { id: "usr_active_team", role: "user", team_ids: ["team_active"] }), true);
  assert.equal(apiKeyCanManage(teamData, teamKey, { id: "usr_disabled_team", role: "user", team_ids: ["team_disabled"] }), false);
  assert.equal(apiKeyCanManage({ ...teamData, resources: {} }, teamKey, { id: "usr_unloaded_team", role: "user", team_ids: ["team_active"] }), false);
});
