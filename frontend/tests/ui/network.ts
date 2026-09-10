import type { BrowserContext, Request } from "@playwright/test";
import configuration from "./config.cjs";
const { apiOrigin, frontendOrigin } = configuration;

export type MockRequest = { method: string; path: string; query: URLSearchParams; body: unknown };
export type MockResponse = { status?: number; json: unknown };
type Handler = (request: MockRequest) => MockResponse | Promise<MockResponse>;

export class MockAPI {
  readonly calls: MockRequest[] = [];
  readonly violations: string[] = [];
  private handlers = new Map<string, Handler>();

  define(method: string, pathname: string, handler: Handler, validateQuery?: (query: URLSearchParams) => void) {
    const key = `${method} ${pathname}`;
    if (this.handlers.has(key)) throw new Error(`Duplicate UI fixture: ${key}`);
    this.handlers.set(key, input => {
      if (validateQuery) validateQuery(input.query);
      else if (input.query.size) throw new Error(`Unexpected query for ${key}`);
      return handler(input);
    });
  }
  respond(method: string, pathname: string, json: unknown) {
    this.define(method, pathname, () => ({ json: structuredClone(json) }));
  }
  assertClean() {
    if (this.violations.length) throw new Error(`UI network contract failed:\n${this.violations.join("\n")}`);
  }
  private read(request: Request): MockRequest {
    const url = new URL(request.url());
    return { method: request.method(), path: url.pathname, query: url.searchParams, body: request.postData() ? request.postDataJSON() : undefined };
  }
  async install(context: BrowserContext) {
    await context.routeWebSocket("**/*", socket => {
      this.violations.push(`Unexpected WebSocket: ${socket.url()}`);
      socket.close();
    });
    await context.route("**/*", async route => {
      const request = route.request();
      const url = new URL(request.url());
      // Frontend documents/assets/RSC are allowed; same-origin APIs are not.
      const frontendResource = ["document", "script", "stylesheet", "image", "font"].includes(request.resourceType()) || url.pathname.startsWith("/_next/static/") || request.headers().rsc === "1" || url.pathname === "/favicon.ico";
      if (url.origin === frontendOrigin && !url.pathname.startsWith("/api") && url.pathname !== "/_next/image" && request.method() === "GET" && frontendResource) {
        await route.continue();
        return;
      }
      if (url.origin !== apiOrigin) {
        this.violations.push(`Unexpected network request: ${request.method()} ${url.origin}${url.pathname}`);
        await route.abort("blockedbyclient");
        return;
      }
      const method = request.method() === "OPTIONS" ? request.headers()["access-control-request-method"] : request.method();
      const handler = this.handlers.get(`${method} ${url.pathname}`);
      if (!handler) {
        this.violations.push(`Missing UI fixture: ${method} ${url.pathname}`);
        await route.abort("blockedbyclient");
        return;
      }
      const cors = { "access-control-allow-origin": frontendOrigin, "access-control-allow-headers": "authorization,content-type", "access-control-allow-methods": method };
      if (request.method() === "OPTIONS") {
        await route.fulfill({ status: 204, headers: cors });
        return;
      }
      try {
        const input = this.read(request);
        this.calls.push(input);
        const response = await handler(input);
        await route.fulfill({ status: response.status ?? 200, json: response.json, headers: cors });
      } catch (error) {
        this.violations.push(`Invalid UI fixture request ${method} ${url.pathname}: ${error instanceof Error ? error.message : String(error)}`);
        await route.fulfill({ status: 500, json: { error: { message: "UI fixture contract failed" } }, headers: cors });
      }
    });
  }
}
