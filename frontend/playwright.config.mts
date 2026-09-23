import { defineConfig, devices } from "@playwright/test";
import e2eDefaults from "./e2e/config.cjs";

const frontendPort = Number(process.env.TOKENHUB_E2E_FRONTEND_PORT ?? e2eDefaults.frontendPort);
const baseURL = `http://127.0.0.1:${frontendPort}`;
const browserChannel = process.env.TOKENHUB_E2E_BROWSER_CHANNEL;

if (browserChannel !== undefined && browserChannel !== "chrome") {
  throw new Error(`Unsupported TOKENHUB_E2E_BROWSER_CHANNEL: ${browserChannel}`);
}

export default defineConfig({
  testDir: "./e2e",
  testMatch: "*.spec.ts",
  fullyParallel: false,
  workers: 1,
  forbidOnly: Boolean(process.env.CI),
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI
    ? [["line"], ["html", { open: "never", outputFolder: "playwright-report" }]]
    : "line",
  outputDir: "test-results",
  use: {
    baseURL,
    locale: "zh-CN",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "node e2e/start-stack.mjs",
    url: baseURL,
    reuseExistingServer: false,
    timeout: 240_000,
    stdout: "pipe",
    stderr: "pipe",
  },
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        ...(browserChannel ? { channel: browserChannel } : {}),
      },
    },
  ],
});
