import { defineConfig } from "@playwright/test";
import configuration from "./tests/ui/config.cjs";
const { distDirectory, frontendOrigin, outputDirectory } = configuration;

if (process.env.TOKENHUB_UI_RUN !== "1") throw new Error("Run npm run test:ui or npm run capture:ui to prepare the isolated frontend.");

export default defineConfig({
  testDir: "./tests/ui",
  testMatch: "**/*.spec.ts",
  fullyParallel: true,
  workers: 2,
  retries: 0,
  forbidOnly: Boolean(process.env.CI),
  timeout: 30_000,
  expect: { timeout: 10_000 },
  outputDir: outputDirectory,
  reporter: process.env.TOKENHUB_UI_CAPTURE === "1"
    ? [["line"], ["./tests/ui/gallery-reporter.mjs"]]
    : "line",
  use: {
    baseURL: frontendOrigin,
    browserName: "chromium",
    headless: true,
    locale: "zh-CN",
    timezoneId: "Asia/Shanghai",
    colorScheme: "light",
    viewport: { width: 1440, height: 1000 },
    serviceWorkers: "block",
    screenshot: "only-on-failure",
    trace: "retain-on-failure",
  },
  webServer: {
    command: "node tests/ui/start-frontend.mjs",
    url: frontendOrigin,
    env: { TOKENHUB_NEXT_DIST_DIR: distDirectory },
    reuseExistingServer: false,
    timeout: 60_000,
    stdout: "pipe",
    stderr: "pipe",
  },
});
