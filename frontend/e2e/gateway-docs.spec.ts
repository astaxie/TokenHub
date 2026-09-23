import { expect, test, type Locator, type Page } from "@playwright/test";
import e2eDefaults from "./config.cjs";

const backendPort = Number(process.env.TOKENHUB_E2E_BACKEND_PORT ?? e2eDefaults.backendPort);
const backendURL = `http://127.0.0.1:${backendPort}`;
const adminToken = "e2e_admin_token_0000000000000000";

test("gateway OpenAPI docs render as self-hosted Swagger UI", async ({ page }) => {
  const projectKey = await createGatewayDocsProjectKey();
  const externalRequests: string[] = [];
  page.on("request", (request) => {
    const url = new URL(request.url());
    if (url.origin !== backendURL) externalRequests.push(request.url());
  });

  await page.goto(`${backendURL}/docs`);

  await expect(page.getByRole("heading", { name: "TokenHub API Reference" })).toBeVisible();
  await expect(page.getByRole("main", { name: "TokenHub Swagger UI" })).toBeVisible();
  await expect(page.getByText("TokenHub Public Gateway API")).toBeVisible({ timeout: 10_000 });
  await expect(page.getByText("POST").first()).toBeVisible();
  await expect(page.getByText("/v1/chat/completions")).toBeVisible();
  await expect(page.getByRole("link", { name: "Open JSON" })).toHaveAttribute("href", "/openapi.json");

  await expect(page.getByRole("button", { name: "Authorize" })).toBeVisible();
  await expect(page.locator(".opblock-summary-path").first()).toContainText("/v1/");
  await expect(page.locator("pre, code, .microlight").first()).toBeVisible();
  await expectReadableCodeSamples(page);
  await expectMinimumContrast(page, [
    "h1",
    "a[href='/openapi.json']",
    ".opblock-summary-path",
  ]);

  const authorizeButton = page.getByRole("button", { name: "Authorize" });
  await focusByKeyboard(page, authorizeButton);
  await page.keyboard.press("Enter");
  const projectAuth = page.locator(".auth-container").filter({
    has: page.locator("code", { hasText: "TokenHubProjectKey" }),
  });
  const secretInput = projectAuth.locator("input[aria-label='auth-bearer-value']");
  await expect(secretInput).toBeVisible();
  await secretInput.fill(projectKey);
  await projectAuth.getByRole("button", { name: "Apply credentials" }).click();
  await projectAuth.getByRole("button", { name: "Close" }).click();
  await expect(secretInput).toBeHidden();

  const modelsOperation = operationBlock(page, "/v1/models");
  await modelsOperation.locator(".opblock-summary").click();
  await modelsOperation.getByRole("button", { name: "Try it out" }).click();
  const modelsRequestPromise = page.waitForRequest((request) => {
    const url = new URL(request.url());
    return request.method() === "GET" && url.origin === backendURL && url.pathname === "/v1/models";
  });
  const modelsResponsePromise = page.waitForResponse((response) => {
    const url = new URL(response.url());
    return url.origin === backendURL && url.pathname === "/v1/models";
  });
  await modelsOperation.getByRole("button", { name: "Execute" }).click();
  const modelsRequest = await modelsRequestPromise;
  const modelsResponse = await modelsResponsePromise;
  expect(new URL(modelsRequest.url()).origin).toBe(backendURL);
  expect(modelsRequest.headers().authorization).toMatch(/^Bearer sk_/);
  expect(modelsResponse.status()).toBe(200);
  await expect(modelsOperation.locator(".responses-wrapper")).toContainText("200");
  await expect(page.locator("body")).not.toContainText(projectKey);
  await expect(page.locator("body")).toContainText("<redacted>");

  await expectOperationCannotExecute(page, "/v1/image-assets/{asset_id}/content", "/v1/image-assets/imgasset_unsafe/content");

  const forbidden = externalRequests.filter((requestURL) =>
    /cdn|unpkg|cdnjs|validator\.swagger\.io/.test(requestURL),
  );
  expect(forbidden).toEqual([]);
});

async function createGatewayDocsProjectKey() {
  const response = await fetch(`${backendURL}/api/admin/projects/prj_default/keys`, {
    method: "POST",
    headers: {
      authorization: `Bearer ${adminToken}`,
      "content-type": "application/json",
    },
    body: JSON.stringify({ name: "Gateway Docs E2E Key", group: "browser-smoke" }),
  });
  if (response.status !== 201) {
    throw new Error(`Expected project key creation to return 201, got ${response.status}: ${await response.text()}`);
  }
  const payload = await response.json() as { api_key?: unknown };
  if (typeof payload.api_key !== "string" || !payload.api_key.startsWith("sk_")) {
    throw new Error("Project key creation response did not include a generated API key");
  }
  return payload.api_key;
}

function operationBlock(page: Page, path: string) {
  return page.locator(".opblock").filter({
    has: page.locator(".opblock-summary-path", { hasText: new RegExp(`^${escapeRegExp(path)}$`) }),
  }).first();
}

function escapeRegExp(value: string) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

async function expectOperationCannotExecute(page: Page, path: string, requestPath: string) {
  const operation = operationBlock(page, path);
  await operation.locator(".opblock-summary").click();
  const tryItButton = operation.getByRole("button", { name: "Try it out" });
  if (await tryItButton.count() === 0) return;

  await tryItButton.click();
  const requestSeen = { value: false };
  const markMatchingRequest = (requestURL: string) => {
    const url = new URL(requestURL);
    if (url.origin === backendURL && url.pathname === requestPath) requestSeen.value = true;
  };
  page.on("request", (request) => markMatchingRequest(request.url()));
  await operation.locator("input").filter({ hasNot: page.locator("[type='checkbox']") }).first().fill("imgasset_unsafe");
  await operation.getByRole("button", { name: "Execute" }).click();
  await page.waitForTimeout(500);
  expect(requestSeen.value).toBe(false);
}

async function focusByKeyboard(page: Page, locator: Locator) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    await page.keyboard.press("Tab");
    if (await locator.evaluate((element) => element === document.activeElement).catch(() => false)) return;
  }
  throw new Error("Expected keyboard navigation to reach the target control");
}

async function expectReadableCodeSamples(page: Page) {
  const readable = await page.locator("pre, code, .microlight").first().evaluate((element) => {
    const style = window.getComputedStyle(element);
    const fontSize = Number.parseFloat(style.fontSize);
    const lineHeight = style.lineHeight === "normal" ? fontSize * 1.2 : Number.parseFloat(style.lineHeight);
    return fontSize >= 12 && lineHeight >= fontSize;
  });
  expect(readable).toBe(true);
}

async function expectMinimumContrast(page: Page, selectors: string[]) {
  const failures = await page.evaluate((targets) => {
    const parseRGB = (value: string) => {
      const match = value.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
      if (!match) return null;
      return [Number(match[1]), Number(match[2]), Number(match[3])];
    };
    const luminance = ([red, green, blue]: number[]) => {
      const channel = [red, green, blue].map((value) => {
        const normalized = value / 255;
        return normalized <= 0.03928 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4;
      });
      return (0.2126 * channel[0]) + (0.7152 * channel[1]) + (0.0722 * channel[2]);
    };
    const backgroundFor = (element: Element) => {
      let current: Element | null = element;
      while (current) {
        const color = window.getComputedStyle(current).backgroundColor;
        if (!color.endsWith(", 0)") && color !== "transparent" && color !== "rgba(0, 0, 0, 0)") return color;
        current = current.parentElement;
      }
      return window.getComputedStyle(document.body).backgroundColor || "rgb(255, 255, 255)";
    };
    return targets.flatMap((selector) => {
      const element = document.querySelector(selector);
      if (!element) return [`${selector}: missing`];
      const foreground = parseRGB(window.getComputedStyle(element).color);
      const background = parseRGB(backgroundFor(element));
      if (!foreground || !background) return [`${selector}: color parse failed`];
      const lighter = Math.max(luminance(foreground), luminance(background));
      const darker = Math.min(luminance(foreground), luminance(background));
      const ratio = (lighter + 0.05) / (darker + 0.05);
      return ratio >= 4.5 ? [] : [`${selector}: ${ratio.toFixed(2)}`];
    });
  }, selectors);
  expect(failures).toEqual([]);
}
