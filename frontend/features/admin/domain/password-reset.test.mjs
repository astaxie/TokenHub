import assert from "node:assert/strict";
import test from "node:test";

import { importTypeScript } from "./typescript-test-loader.mjs";

const { consumePasswordResetTokenFromURL } = await importTypeScript(new URL("./password-reset.ts", import.meta.url));

test("consumes a password reset token from the fragment and scrubs the URL immediately", () => {
  const replacements = [];
  const token = consumePasswordResetTokenFromURL(
    "https://console.example.test/?language=zh#reset_token=fragment-secret&oauth_code=pending",
    (url) => replacements.push(url),
  );
  assert.equal(token, "fragment-secret");
  assert.deepEqual(replacements, ["/?language=zh#oauth_code=pending"]);
});

test("rejects query reset tokens while scrubbing them from the URL", () => {
  const replacements = [];
  const token = consumePasswordResetTokenFromURL(
    "https://console.example.test/?reset_token=legacy-secret&language=en",
    (url) => replacements.push(url),
  );
  assert.equal(token, "");
  assert.deepEqual(replacements, ["/?language=en"]);
});

test("leaves unrelated URLs unchanged", () => {
  let replaced = false;
  assert.equal(consumePasswordResetTokenFromURL("https://console.example.test/overview#section", () => { replaced = true; }), "");
  assert.equal(replaced, false);
});

test("prefers the fragment token and removes duplicate query credentials", () => {
  const replacements = [];
  const token = consumePasswordResetTokenFromURL(
    "https://console.example.test/?reset_token=query-secret#reset_token=fragment-secret&section=security",
    (url) => replacements.push(url),
  );
  assert.equal(token, "fragment-secret");
  assert.deepEqual(replacements, ["/#section=security"]);
});

test("is idempotent after the reset token has been consumed", () => {
  const replacements = [];
  const firstURL = "https://console.example.test/#reset_token=fragment-secret";
  const token = consumePasswordResetTokenFromURL(firstURL, (url) => replacements.push(url));
  assert.equal(token, "fragment-secret");
  assert.deepEqual(replacements, ["/"]);

  let replacedAgain = false;
  assert.equal(consumePasswordResetTokenFromURL("https://console.example.test/", () => { replacedAgain = true; }), "");
  assert.equal(replacedAgain, false);
});
