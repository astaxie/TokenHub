import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const navigationSource = await readFile(
  new URL("../frontend/features/admin/core/navigation.tsx", import.meta.url),
  "utf8",
);

test("platform admins have a visible playground navigation entry", () => {
  const start = navigationSource.indexOf("export const adminNavGroups");
  const end = navigationSource.indexOf("export const securityNavGroups", start);

  assert.notEqual(start, -1, "admin navigation groups are declared");
  assert.notEqual(end, -1, "admin navigation block has a stable boundary");

  const adminNavigation = navigationSource.slice(start, end);
  assert.match(
    adminNavigation,
    /\{ view: "playground", label: "模型演练场", icon: Send \}/,
    "admins can discover the playground they are authorized to use",
  );
});
