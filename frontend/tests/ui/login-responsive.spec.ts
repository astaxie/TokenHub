import type { Locator, Page } from "@playwright/test";
import { test, expect, capture } from "./harness";

test.use({ sessionUser: null });

const identityProviders = [
  { id: "idp_ui_github", name: "UI GitHub", display_name: "UI GitHub", provider_type: "oauth2", icon_key: "github" },
  { id: "idp_ui_google", name: "UI Google", display_name: "UI Google", provider_type: "oidc", icon_key: "google" },
  { id: "idp_ui_custom", name: "UI Enterprise Identity Provider", display_name: "UI Enterprise Identity Provider", provider_type: "oidc", icon_key: "sso" },
];

test.beforeEach(async ({ api }) => {
  api.respond("GET", "/api/admin/auth/identity-providers", { data: [] });
});

async function openLogin(page: Page) {
  await page.goto("/overview");
  await expect(page.locator(".login-card")).toBeVisible();
  await page.evaluate(() => document.fonts.ready);
}

async function bounds(locator: Locator) {
  await expect(locator).toBeVisible();
  const box = await locator.boundingBox();
  expect(box, "Visible content must have measurable bounds").not.toBeNull();
  return box!;
}

async function expectHorizontalFit(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth), "The document must not scroll horizontally").toBeLessThanOrEqual(page.viewportSize()!.width);
  const elements = page.locator([
    ".login-brand-lockup", ".login-utility-controls", ".login-card", ".login-card-head",
    ".login-card input", ".login-submit", ".login-sso-button", ".login-hero-copy",
    ".login-feature-card", ".login-dashboard-image",
  ].join(", "));
  for (let index = 0; index < await elements.count(); index++) {
    const element = elements.nth(index);
    const box = await bounds(element);
    expect(box.x, `Element ${index} must not be clipped at the left edge`).toBeGreaterThanOrEqual(-1);
    expect(box.x + box.width, `Element ${index} must not be clipped at the right edge`).toBeLessThanOrEqual(page.viewportSize()!.width + 1);
  }
  for (const text of await page.locator(".login-card-head h1, .login-hero-copy h1, .login-feature-card strong, .login-feature-card small").all()) {
    expect(await text.evaluate(element => element.scrollWidth - element.clientWidth), "Login text must wrap within its container").toBeLessThanOrEqual(1);
  }
}

async function expectImageLoaded(image: Locator) {
  await expect(image).toBeVisible();
  await expect.poll(() => image.evaluate(element => {
    const img = element as HTMLImageElement;
    return img.complete && img.naturalWidth > 0 && img.naturalHeight > 0;
  }), { message: "The login image must load successfully" }).toBe(true);
}

const viewports = [
  { name: "wide-desktop", width: 1920, height: 1080 },
  { name: "desktop", width: 1440, height: 1000 },
  { name: "laptop", width: 1366, height: 768 },
  { name: "small-laptop", width: 1280, height: 720 },
  { name: "compact-desktop", width: 1024, height: 768 },
  { name: "tablet", width: 768, height: 1024 },
  { name: "mobile", width: 390, height: 844 },
  { name: "small-mobile", width: 320, height: 568 },
  { name: "mobile-landscape", width: 844, height: 390 },
];

for (const viewport of viewports) {
  test.describe(`login-responsive ${viewport.name}`, () => {
    test.use({ viewport: { width: viewport.width, height: viewport.height } });

    test("keeps the form reachable and content separated without horizontal clipping", async ({ page }, info) => {
      await openLogin(page);
      await expectImageLoaded(page.locator(".login-brand-mark img"));
      await expectImageLoaded(page.locator(".login-dashboard-image"));
      await expectHorizontalFit(page);

      const header = await bounds(page.locator(".login-utility-actions"));
      const card = await bounds(page.locator(".login-card"));
      const hero = await bounds(page.locator(".login-hero-panel"));
      const account = page.getByPlaceholder("请输入账号或邮箱");
      expect(header.y).toBeGreaterThanOrEqual(0);
      expect(header.y + header.height).toBeLessThanOrEqual(card.y);
      await expect(account).toBeInViewport({ ratio: 1 });
      if (viewport.width <= 1180) {
        expect(card.y + card.height, "Compact layouts must show the form before the marketing content").toBeLessThanOrEqual(hero.y);
      } else {
        expect(hero.x + hero.width, "Desktop columns must not overlap").toBeLessThanOrEqual(card.x);
      }
      if (viewport.height >= 700) {
        await expect(page.getByRole("button", { name: "登录控制台", exact: true })).toBeInViewport({ ratio: 1 });
      }
      await capture(page, info, page.locator(".login-shell"), `login-${viewport.name}`, "登录页：分辨率适配", "viewport");

      await account.fill("ui-review-user");
      await page.getByPlaceholder("请输入密码").fill("synthetic-ui-password");
      const submit = page.getByRole("button", { name: "登录控制台", exact: true });
      await submit.scrollIntoViewIfNeeded();
      await expect(submit).toBeInViewport({ ratio: 1 });
      await expect(submit).toBeEnabled();
      if (viewport.height < 700) {
        await capture(page, info, page.locator(".login-card"), `login-${viewport.name}-actions`, "登录页：短屏滚动后操作区", "viewport");
      }
      if (viewport.name === "mobile") {
        await page.locator(".login-feature-grid").scrollIntoViewIfNeeded();
        await expect(page.locator(".login-feature-icon svg")).toHaveCount(4);
        await capture(page, info, page.locator(".login-hero-panel"), "login-mobile-features", "登录页：移动端功能图标与介绍", "viewport");
      }
    });
  });
}

test.describe("login-responsive icon controls", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("renders brand and feature icons and toggles password, theme and language", async ({ page }, info) => {
    await openLogin(page);
    await expectImageLoaded(page.locator(".login-brand-mark img"));
    for (const icon of [".login-input-field > svg", ".password-leading-icon", ".password-toggle svg", ".login-theme-toggle svg", ".language-select-trigger > svg:first-child"]) {
      await expect(page.locator(icon)).toBeVisible();
      const box = await bounds(page.locator(icon));
      expect(box.width).toBeGreaterThan(0);
      expect(box.height).toBeGreaterThan(0);
    }
    const features = page.locator(".login-feature-icon svg");
    await expect(features).toHaveCount(4);
    for (const feature of await features.all()) await expect(feature).toBeVisible();

    const password = page.getByPlaceholder("请输入密码");
    await password.fill("synthetic-ui-password");
    await expect(password).toHaveAttribute("type", "password");
    await page.getByRole("button", { name: "显示密码", exact: true }).click();
    await expect(password).toHaveAttribute("type", "text");
    await expect(password).toHaveValue("synthetic-ui-password");
    await page.getByRole("button", { name: "隐藏密码", exact: true }).click();
    await expect(password).toHaveAttribute("type", "password");

    await page.getByRole("button", { name: "切换主题", exact: true }).click();
    await expect(page.locator(".login-shell")).toHaveAttribute("data-theme", "dark");
    await expect(page.locator(".login-dashboard-image")).toHaveAttribute("src", "/brand/login-dashboard-dark.png");
    await expectImageLoaded(page.locator(".login-dashboard-image"));
    await expectHorizontalFit(page);
    await capture(page, info, page.locator(".login-shell"), "login-mobile-dark", "登录页：移动端深色主题与密码图标", "viewport");

    await page.getByRole("button", { name: "界面语言", exact: true }).click();
    const menu = page.getByRole("listbox", { name: "界面语言" });
    await expect(menu).toBeInViewport({ ratio: 1 });
    await page.getByRole("option", { name: "English", exact: true }).click();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.getByRole("heading", { name: "Welcome back", exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Show password", exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Toggle theme", exact: true }).click();
    await expect(page.locator(".login-shell")).toHaveAttribute("data-theme", "light");
    await expect(page.locator(".login-dashboard-image")).toHaveAttribute("src", "/brand/login-dashboard-en.png");
    await expectImageLoaded(page.locator(".login-dashboard-image"));
    await expectHorizontalFit(page);
  });

  test("keeps SSO icons and login failure actions usable with long provider labels", async ({ page, api }, info) => {
    api.replaceResponse("GET", "/api/admin/auth/identity-providers", { data: identityProviders });
    let release = () => {};
    const pending = new Promise<void>(resolve => { release = resolve; });
    api.define("POST", "/api/admin/auth/login", async request => {
      expect(request.body).toEqual({ identity: "ui-review-user", password: "synthetic-ui-password" });
      await pending;
      return { status: 401, json: { error: { message: "Synthetic invalid credentials" } } };
    });
    await openLogin(page);
    await expect(page.locator(".login-sso-button")).toHaveCount(identityProviders.length);
    for (const provider of identityProviders) {
      const link = page.getByRole("link", { name: `使用 ${provider.display_name} 登录`, exact: true });
      await link.scrollIntoViewIfNeeded();
      await expect(link).toBeInViewport({ ratio: 1 });
      await expect(link.locator(".login-sso-icon svg")).toBeVisible();
      const target = new URL((await link.getAttribute("href"))!);
      expect(target.pathname).toBe("/api/admin/auth/oauth/start");
      expect(target.searchParams.get("id")).toBe(provider.id);
    }
    await expectHorizontalFit(page);
    await capture(page, info, page.locator(".login-card"), "login-mobile-sso", "登录页：SSO 图标与长名称", "viewport");

    await page.getByPlaceholder("请输入账号或邮箱").fill("ui-review-user");
    await page.getByPlaceholder("请输入密码").fill("synthetic-ui-password");
    try {
      await page.getByRole("button", { name: "登录控制台", exact: true }).click();
      await expect(page.getByRole("button", { name: "登录中", exact: true })).toBeDisabled();
      for (const link of await page.locator(".login-sso-button").all()) {
        await expect(link).toHaveAttribute("aria-disabled", "true");
      }
    } finally {
      release();
    }
    await expect(page.locator(".login-error")).toHaveText("login 401");
    await expect(page.getByRole("button", { name: "登录控制台", exact: true })).toBeEnabled();
    await expect(page.locator(".app-shell")).toHaveCount(0);
    await expectHorizontalFit(page);
    await page.locator(".login-error").scrollIntoViewIfNeeded();
    await capture(page, info, page.locator(".login-card"), "login-mobile-error", "登录页：失败提示与重试操作", "viewport");
  });
});

for (const language of ["en", "ja", "ru"] as const) {
  test.describe(`login-responsive localized ${language}`, () => {
    test.use({ sessionLanguage: language, viewport: { width: 320, height: 568 } });
    test("fits translated text on a narrow mobile screen", async ({ page }, info) => {
      await openLogin(page);
      await expect(page.locator("html")).toHaveAttribute("lang", language);
      await expectHorizontalFit(page);
      await expect(page.locator(".login-card input").first()).toBeInViewport({ ratio: 1 });
      await page.locator(".login-submit").scrollIntoViewIfNeeded();
      await expect(page.locator(".login-submit")).toBeInViewport({ ratio: 1 });
      await capture(page, info, page.locator(".login-card"), `login-mobile-${language}`, "登录页：移动端多语言表单", "viewport");
    });
  });
}
