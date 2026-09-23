import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const shellStyles = await readFile(new URL("../frontend/app/styles/redesign/shell.css", import.meta.url), "utf8");

function declarationsFor(selector) {
  const escapedSelector = selector.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return shellStyles.match(new RegExp(`${escapedSelector}\\s*\\{([^}]*)\\}`))?.[1] ?? "";
}

test("topbar stacking context keeps global search above page content", () => {
  const topbar = declarationsFor(".topbar");
  const searchPanel = declarationsFor(".top-search-panel");

  assert.match(topbar, /position:\s*relative\s*;/);
  assert.match(topbar, /z-index:\s*\d+\s*;/);
  assert.match(searchPanel, /position:\s*absolute\s*;/);
  assert.match(searchPanel, /z-index:\s*\d+\s*;/);
});
