import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const {
  clearTransientAdminSession,
  readTransientAdminSession,
  saveTransientAdminSession,
} = await importTypeScript(new URL("./admin-session.ts", import.meta.url));

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem: (key) => values.get(key) ?? null,
    removeItem: (key) => values.delete(key),
    setItem: (key, value) => values.set(key, value),
    value: (key) => values.get(key) ?? null,
  };
}

const storageKey = "tokenhub.admin.session";
const validSession = {
  baseURL: "https://api.example.test",
  token: "admin-session-token",
  user: { id: "admin-1" },
  expiresAt: "2099-01-01T00:00:00Z",
};

test("does not restore a legacy bearer from localStorage", () => {
  const tab = memoryStorage();
  const persistent = memoryStorage({ [storageKey]: JSON.stringify(validSession) });

  assert.equal(readTransientAdminSession(storageKey, tab, persistent, 0), null);
  assert.equal(persistent.value(storageKey), null);
});

test("stores and restores an administrator bearer only for the current tab", () => {
  const tab = memoryStorage();
  const persistent = memoryStorage({ [storageKey]: "legacy-token" });

  saveTransientAdminSession(storageKey, validSession, tab, persistent);

  assert.equal(persistent.value(storageKey), null);
  assert.deepEqual(readTransientAdminSession(storageKey, tab, persistent, 0), validSession);
});

test("clears transient and legacy administrator sessions", () => {
  const encoded = JSON.stringify(validSession);
  const tab = memoryStorage({ [storageKey]: encoded });
  const persistent = memoryStorage({ [storageKey]: encoded });

  clearTransientAdminSession(storageKey, tab, persistent);

  assert.equal(tab.value(storageKey), null);
  assert.equal(persistent.value(storageKey), null);
});
