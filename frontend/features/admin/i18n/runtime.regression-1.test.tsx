import { afterEach, describe, expect, it, vi } from "vitest";

import { type AppLanguage, countRatioWithUnit, setActiveLanguage } from "./runtime";

describe("localized count ratios", () => {
  afterEach(() => setActiveLanguage("en"));

  it.each<[AppLanguage, string]>([
    ["zh-CN", "2/3 个可引入模型"],
    ["en", "2/3 importable models"],
    ["ja", "2/3 件の取り込み可能モデル"],
    ["ru", "2/3 доступные для импорта модели"],
  ])("preserves the current and total counts in %s", (language, expected) => {
    setActiveLanguage(language);

    expect(countRatioWithUnit(2, 3, "个可引入模型", "importable model", "件の取り込み可能モデル", "importable models")).toBe(expected);
  });

  it("handles Russian countWithUnit plural rules", async () => {
    const {
      countWithLabel,
      countWithUnit,
      modelCatalogTemplatesHintText,
      providerImportHintText,
    } = await import("./runtime");
    setActiveLanguage("ru");
    expect(countWithUnit(1, "个模型", "model", "モデル")).toBe("1 модель");
    expect(countWithUnit(2, "个模型", "model", "モデル")).toBe("2 модели");
    expect(countWithUnit(5, "个模型", "model", "モデル")).toBe("5 моделей");
    expect(countWithUnit(2, "人", "member", "人")).toBe("2 участника");
    expect(countWithUnit(1, "条路由", "route", "ルート")).toBe("1 маршрут");
    expect(countWithUnit(2, "条路由", "route", "ルート")).toBe("2 маршрута");
    expect(countWithUnit(5, "条路由", "route", "ルート")).toBe("5 маршрутов");
    expect(countWithUnit(1, "个团队", "team", "チーム")).toBe("1 команда");
    expect(countWithUnit(2, "个团队", "team", "チーム")).toBe("2 команды");
    expect(countWithUnit(5, "个团队", "team", "チーム")).toBe("5 команд");

    // Route direct count phrases through Russian plural selection (1, 2, 5)
    expect(countWithLabel(1, "个待引入")).toBe("1 модель к импорту");
    expect(countWithLabel(2, "个待引入")).toBe("2 модели к импорту");
    expect(countWithLabel(5, "个待引入")).toBe("5 моделей к импорту");

    expect(countWithLabel(1, "个已引入模型")).toBe("1 импортированная модель");
    expect(countWithLabel(2, "个已引入模型")).toBe("2 импортированные модели");
    expect(countWithLabel(5, "个已引入模型")).toBe("5 импортированных моделей");

    expect(countWithLabel(1, "个可选上游模型")).toBe("1 доступная модель провайдера");
    expect(countWithLabel(2, "个可选上游模型")).toBe("2 доступные модели провайдера");
    expect(countWithLabel(5, "个可选上游模型")).toBe("5 доступных моделей провайдера");

    expect(countWithLabel(1, "条启用线路")).toBe("1 активная линия");
    expect(countWithLabel(2, "条启用线路")).toBe("2 активные линии");
    expect(countWithLabel(5, "条启用线路")).toBe("5 активных линий");

    expect(countWithLabel(1, "个已配置路由")).toBe("1 настроенный маршрут");
    expect(countWithLabel(2, "个已配置路由")).toBe("2 настроенных маршрута");
    expect(countWithLabel(5, "个已配置路由")).toBe("5 настроенных маршрутов");

    expect(providerImportHintText(1)).toBe(
      "После сохранения будет импортировано 1 модель провайдера; перейдите в каталог моделей для создания внешней модели, установки единой цены и выбора начальных маршрутов."
    );
    expect(providerImportHintText(2)).toBe(
      "После сохранения будет импортировано 2 модели провайдера; перейдите в каталог моделей для создания внешней модели, установки единой цены и выбора начальных маршрутов."
    );
    expect(providerImportHintText(5)).toBe(
      "После сохранения будет импортировано 5 моделей провайдера; перейдите в каталог моделей для создания внешней модели, установки единой цены и выбора начальных маршрутов."
    );

    expect(modelCatalogTemplatesHintText(1)).toBe(
      "1 стандартная модель, можно сразу применить возможности, контекст и рекомендованную цену."
    );
    expect(modelCatalogTemplatesHintText(2)).toBe(
      "2 стандартные модели, можно сразу применить возможности, контекст и рекомендованную цену."
    );
    expect(modelCatalogTemplatesHintText(5)).toBe(
      "5 стандартных моделей, можно сразу применить возможности, контекст и рекомендованную цену."
    );
  });

  it("handles routeAttemptCountText in Russian conditionally", async () => {
    const { routeAttemptCountText } = await import("./runtime");
    setActiveLanguage("ru");
    expect(routeAttemptCountText(0)).toBe("0 попыток");
    expect(routeAttemptCountText(1)).toBe("1 попытка");
    expect(routeAttemptCountText(2)).toBe("2 попытки, с fallback");
    expect(routeAttemptCountText(5)).toBe("5 попыток, с fallback");
    expect(routeAttemptCountText(21)).toBe("21 попытка, с fallback");
    expect(routeAttemptCountText(22)).toBe("22 попытки, с fallback");
    expect(routeAttemptCountText(25)).toBe("25 попыток, с fallback");
  });

  it("formats counts with active locale grouping rules", async () => {
    const {
      formatLocaleNumber,
      selectedModelsText,
      selectedOptionsText,
      importUsersDoneMessage,
      importUsersSkippedMessage,
    } = await import("./runtime");

    setActiveLanguage("ru");
    const count = 1000;
    const formatted = formatLocaleNumber(count);
    expect(formatted).toBe("1\u00A0000");
    expect(selectedModelsText(count)).toBe(`Выбрано моделей: ${formatted}`);
    expect(selectedOptionsText(count)).toBe(`Выбрано вариантов: ${formatted}`);
    expect(importUsersDoneMessage(1000, 2000, 3000)).toBe(
      `Импорт пользователей завершен: создано ${formatLocaleNumber(1000)}, обновлено ${formatLocaleNumber(2000)}, пропущено ${formatLocaleNumber(3000)}`
    );
    expect(importUsersSkippedMessage(count, "ошибка")).toBe(
      `Не импортировано строк: ${formatted}. Ошибки: ошибка`
    );
  });

  it("formats countdown quantities with active locale grouping rules", async () => {
    const { formatResetExpiryCountdown } = await import("./runtime");

    setActiveLanguage("ru");
    expect(formatResetExpiryCountdown(1000, 2, 30)).toBe("через 1\u00A0000 дн. 2 ч.");
    expect(formatResetExpiryCountdown(0, 1000, 15)).toBe("через 1\u00A0000 ч. 15 мин.");
    expect(formatResetExpiryCountdown(0, 0, 1000)).toBe("через 1\u00A0000 мин.");

    setActiveLanguage("en");
    expect(formatResetExpiryCountdown(1000, 1, 0)).toBe("1,000 days 1 hour left");

    setActiveLanguage("ja");
    expect(formatResetExpiryCountdown(1000, 2, 0)).toBe("1,000日2時間後");

    setActiveLanguage("zh-CN");
    expect(formatResetExpiryCountdown(1000, 2, 0)).toBe("1,000天2小时后");

    const { issuedKeyCloseCountdownLabel } = await import("../shared/ui");
    setActiveLanguage("ru");
    expect(issuedKeyCloseCountdownLabel(1000)).toBe("Закрыть через 1\u00A0000с");
  });

  it("formats pagination values with active locale grouping rules", async () => {
    const { render } = await import("@testing-library/react");
    const { PaginationControls } = await import("../shared/pagination");

    setActiveLanguage("ru");
    const pagination = {
      page: 1,
      pageSize: 20,
      pageCount: 500,
      startIndex: 0,
      endIndex: 20,
      setPage: () => {},
      setPageSize: () => {},
    };
    const { container } = render(<PaginationControls pagination={pagination} totalItems={10000} />);
    expect(container.querySelector(".pagination-summary")?.textContent).toBe("1–20 из 10\u00A0000");
    expect(container.querySelector(".page-buttons span")?.textContent).toBe("1 / 500");
  });

  it("positions LanguageSelect dropdown properly near viewport bottom for 4 options", async () => {
    const { fireEvent, render } = await import("@testing-library/react");
    const { LanguageSelect } = await import("./language-switcher");

    // Simulate trigger near viewport bottom
    const triggerBottom = 580;
    const triggerTop = 550;
    const innerHeight = 600;

    Object.defineProperty(window, "innerHeight", { writable: true, configurable: true, value: innerHeight });
    Object.defineProperty(window, "innerWidth", { writable: true, configurable: true, value: 800 });

    const { getByRole } = render(<LanguageSelect language="en" onChange={() => {}} />);
    const trigger = getByRole("button");

    // Mock getBoundingClientRect
    vi.spyOn(trigger, "getBoundingClientRect").mockReturnValue({
      top: triggerTop,
      bottom: triggerBottom,
      left: 600,
      right: 760,
      width: 160,
      height: 30,
      x: 600,
      y: triggerTop,
      toJSON: () => {},
    });

    fireEvent.click(trigger);

    const listbox = document.querySelector('[role="listbox"]') as HTMLElement;
    expect(listbox).not.toBeNull();

    // With 4 options: height is 4 * 38 + 3 * 2 + 10 = 168.
    // Distance to bottom: 600 - 580 = 20px (< 168 + 12 = 180), so opens above!
    // Top position above: triggerTop (550) - 168 - 6 = 376.
    expect(listbox.style.top).toBe("376px");
  });
});
