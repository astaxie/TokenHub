import { test as base, expect, type Locator, type Page, type TestInfo } from "@playwright/test";
import { languageStorageKey, sessionStorageKey } from "../../features/admin/core/types";
import configuration from "./config.cjs";
import { fixedTime, shellResponses, user } from "./fixtures/shell";
import { MockAPI } from "./network";
const { apiOrigin } = configuration;

export const test = base.extend<{ api: MockAPI }>({
  api: [async ({ context, page }, runScenario) => {
    const api = new MockAPI();
    for (const [key, json] of shellResponses()) {
      const [method, pathname] = key.split(" ");
      api.respond(method, pathname, json);
    }
    api.define("GET", "/api/admin/billing/statements", input => ({ json: { kind: input.query.get("kind"), from: input.query.get("from"), to: input.query.get("to"), known_amount_usd: "0", records: 0, pending: 0, legacy: 0, complete: true, offset: 0, limit: 100, groups: [], items: [] } }), query => {
      expect(Object.fromEntries(query)).toEqual({ kind: "provider", group_by: "provider", provider_id: "", project_id: "", model: "", from: "2026-08-31T16:00:00.000Z", to: "2026-09-30T16:00:00.000Z", offset: "0", limit: "100" });
    });
    await api.install(context);
    await page.clock.setFixedTime(new Date(fixedTime));
    await page.emulateMedia({ reducedMotion: "reduce" });
    await context.addInitScript(({ sessionKey, languageKey, session }) => {
      window.sessionStorage.setItem(sessionKey, JSON.stringify(session));
      window.localStorage.setItem(languageKey, "zh-CN");
    }, { sessionKey: sessionStorageKey, languageKey: languageStorageKey, session: { baseURL: apiOrigin, token: "ui-fixture-session", user, expiresAt: "2099-01-01T00:00:00Z" } });
    const pageErrors: string[] = [];
    context.on("page", opened => opened.on("pageerror", error => pageErrors.push(error.message)));
    page.on("pageerror", error => pageErrors.push(error.message));
    try {
      await runScenario(api);
    } finally {
      // Keep interception installed until every page and its requests are closed.
      await context.close();
      api.assertClean();
      expect(pageErrors, "Unexpected application errors").toEqual([]);
    }
  }, { auto: true }],
});
export { expect };

export function section(page: Page, title: string): Locator {
  return page.locator("section.section").filter({ has: page.getByRole("heading", { name: title, exact: true }) });
}
export async function openBilling(page: Page) {
  await page.goto("/billing");
  await expect(page.locator(".app-shell")).toBeVisible();
  await page.locator("summary").filter({ hasText: "客户对账单与毛利" }).click();
  await expect(section(page, "费用对账单").getByRole("option", { name: "UI Review Project (prj_ui)", exact: true })).toBeAttached();
}
export async function capture(page: Page, testInfo: TestInfo, subject: Locator, id: string, title: string, mode: "section" | "viewport" = "section") {
  testInfo.annotations.push({ type: "ui-capture", description: JSON.stringify({ id, title }) });
  if (process.env.TOKENHUB_UI_CAPTURE !== "1") return;
  await page.evaluate(() => document.fonts.ready);
  const image = testInfo.outputPath(`${id}.png`);
  if (mode === "viewport") {
    await page.screenshot({ path: image, fullPage: false, animations: "disabled", caret: "hide" });
  } else {
    const bounds = await subject.boundingBox();
    if (!bounds || bounds.height > page.viewportSize()!.height) {
      throw new Error("Capture long scrollable sections as named viewport segments instead of a clipped element image.");
    }
    await subject.scrollIntoViewIfNeeded();
    await subject.screenshot({ path: image, animations: "disabled", caret: "hide" });
  }
  await testInfo.attach(id, { path: image, contentType: "image/png" });
}
