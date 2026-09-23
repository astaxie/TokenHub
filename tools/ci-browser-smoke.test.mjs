import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const workflow = await readFile(new URL("../.github/workflows/ci.yml", import.meta.url), "utf8");
const playwrightConfig = await readFile(
  new URL("../frontend/playwright.config.mts", import.meta.url),
  "utf8",
);

test("browser smoke uses the runner-provided Chrome without installing system dependencies", () => {
  assert.doesNotMatch(workflow, /playwright install[^\n]*--with-deps/);
  assert.match(workflow, /name: Check Chrome\s+run: google-chrome --version/);
  assert.match(
    workflow,
    /name: Run browser smoke tests\s+env:\s+TOKENHUB_E2E_BROWSER_CHANNEL: chrome\s+run: npm run test:e2e/,
  );
});

test("Playwright validates and applies the CI browser channel", () => {
  assert.match(playwrightConfig, /process\.env\.TOKENHUB_E2E_BROWSER_CHANNEL/);
  assert.match(playwrightConfig, /browserChannel !== "chrome"/);
  assert.match(playwrightConfig, /channel: browserChannel/);
});
