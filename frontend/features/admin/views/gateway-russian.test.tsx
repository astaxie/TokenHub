import { afterEach, describe, expect, it } from "vitest";
import { setActiveLanguage } from "../i18n/runtime";
import { gatewayLanguageLabel } from "./gateway-docs-ui";
import { gatewayDocBundle, type GatewayDocStats } from "./gateway-view";
import { gatewayLLMUsageDocs } from "./gateway-llm-en";

describe("gateway Russian docs and labels", () => {
  afterEach(() => setActiveLanguage("en"));

  it("returns Russian language label for ru", () => {
    expect(gatewayLanguageLabel("ru")).toBe("Русский");
    expect(gatewayLanguageLabel("zh-CN")).toBe("中文");
    expect(gatewayLanguageLabel("ja")).toBe("日本語");
    expect(gatewayLanguageLabel("en")).toBe("English");
  });

  const mockStats: GatewayDocStats = {
    baseURL: "https://api.tokenhub.example.com",
    referenceURL: "https://api.tokenhub.example.com/docs",
    keyHint: "sk-th-12345...6789",
    authHeader: "Bearer sk-th-12345...6789",
    sampleModel: "gpt-4.1-mini",
    chatCurl: "curl -N https://api.tokenhub.example.com/chat/completions",
    activeRouteCountValue: 3,
    apiKeyCount: 5,
    projectCount: 2,
    userCount: 10,
    providerCount: 4,
    routeCount: 6,
    requestLogCount: 120,
    visibleModelCount: 8,
  };

  it("returns Russian doc bundle for admin role", () => {
    const bundle = gatewayDocBundle({
      language: "ru",
      baseURL: mockStats.baseURL,
      referenceURL: mockStats.referenceURL,
      keyHint: mockStats.keyHint,
      sampleModel: mockStats.sampleModel,
      activeRoutes: 3,
      data: {
        routes: [],
        keys: [],
        models: [],
        providers: [],
        users: [],
        projects: [],
        logs: [],
        summary: { active_route_count: 3, api_key_count: 5, user_count: 10 },
      } as any,
      callableModels: [{ name: "gpt-4.1-mini" }] as any,
      role: "admin",
    });

    expect(bundle.title).toBe("Руководство по шлюзу для трех ролей");
    expect(bundle.nav.title).toBe("Документация");
    expect(bundle.languageLabel).toBe("Язык документации");
    expect(bundle.quickCards.sampleModel).toBe("Пример модели");
    expect(bundle.groups[0].title).toBe("Начало работы");
    expect(bundle.groups[1].title).toBe("Руководства по ролям");
    expect(bundle.groups[2].title).toBe("Справочник API");
  });

  it("correctly pluralizes quick-card nouns in admin doc bundle for 1, 2, and 5", () => {
    const createAdminBundle = (routes: number, keys: number) =>
      gatewayDocBundle({
        language: "ru",
        baseURL: mockStats.baseURL,
        referenceURL: mockStats.referenceURL,
        keyHint: mockStats.keyHint,
        sampleModel: mockStats.sampleModel,
        activeRoutes: routes,
        data: {
          routes: new Array(routes).fill({ status: "active" }),
          keys: new Array(keys).fill({}),
          models: [],
          providers: [],
          users: [],
          projects: [],
          logs: [],
          summary: { active_route_count: routes, api_key_count: keys, user_count: 10 },
        } as any,
        callableModels: [{ name: "gpt-4.1-mini" }] as any,
        role: "admin",
      });

    const bundle1 = createAdminBundle(1, 1);
    expect(bundle1.quickCards.activeRoutes).toBe("1 активный маршрут");
    expect(bundle1.quickCards.apiKeys).toBe("1 API-ключ");

    const bundle2 = createAdminBundle(2, 2);
    expect(bundle2.quickCards.activeRoutes).toBe("2 активных маршрута");
    expect(bundle2.quickCards.apiKeys).toBe("2 API-ключа");

    const bundle5 = createAdminBundle(5, 5);
    expect(bundle5.quickCards.activeRoutes).toBe("5 активных маршрутов");
    expect(bundle5.quickCards.apiKeys).toBe("5 API-ключей");
  });

  it("returns Russian LLM usage doc bundle for user and team_leader roles", () => {
    const userBundle = gatewayLLMUsageDocs({
      language: "ru",
      role: "user",
      ...mockStats,
    });

    expect(userBundle.title).toBe("Вызов больших языковых моделей");
    expect(userBundle.nav.title).toBe("Документация API");
    expect(userBundle.quickCards.baseURL).toBe("Базовый URL");
    expect(userBundle.groups[0].title).toBe("Быстрый старт");
    expect(userBundle.groups[1].title).toBe("Справочник LLM API");
    expect(userBundle.groups[2].title).toBe("Ключи проекта");

    const leaderBundle = gatewayLLMUsageDocs({
      language: "ru",
      role: "team_leader",
      ...mockStats,
    });

    expect(leaderBundle.title).toBe("Вызов больших языковых моделей");
    expect(leaderBundle.groups[2].title).toBe("Внедрение в команде");
  });

  it("correctly pluralizes quick-card nouns in user/team bundle for 1, 2, and 5", () => {
    const createUserBundle = (models: number, keys: number) =>
      gatewayLLMUsageDocs({
        language: "ru",
        role: "user",
        ...mockStats,
        visibleModelCount: models,
        apiKeyCount: keys,
      });

    const bundle1 = createUserBundle(1, 1);
    expect(bundle1.quickCards.activeRoutes).toBe("1 доступная модель");
    expect(bundle1.quickCards.apiKeys).toBe("1 ключ проекта");

    const bundle2 = createUserBundle(2, 2);
    expect(bundle2.quickCards.activeRoutes).toBe("2 доступные модели");
    expect(bundle2.quickCards.apiKeys).toBe("2 ключа проекта");

    const bundle5 = createUserBundle(5, 5);
    expect(bundle5.quickCards.activeRoutes).toBe("5 доступных моделей");
    expect(bundle5.quickCards.apiKeys).toBe("5 ключей проекта");
  });
});
