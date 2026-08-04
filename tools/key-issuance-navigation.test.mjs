import assert from "node:assert/strict";
import { describe, it } from "node:test";

import { resourceCreateTarget } from "../frontend/features/admin/domain/resource-create-target.ts";

describe("resource create navigation", () => {
  it("keeps Key issuance in the API Key wizard", () => {
    assert.equal(resourceCreateTarget("api-keys"), "api-key-wizard");
  });

  it("keeps resource-specific create targets explicit", () => {
    assert.equal(resourceCreateTarget("providers"), "provider-modal");
    assert.equal(resourceCreateTarget("projects"), "project-workspace");
    assert.equal(resourceCreateTarget("notification-channels"), "notification-channel-modal");
    assert.equal(resourceCreateTarget("users"), "resource-modal");
  });
});
