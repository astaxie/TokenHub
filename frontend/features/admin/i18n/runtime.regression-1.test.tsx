import { afterEach, describe, expect, it } from "vitest";

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
    const { countWithUnit } = await import("./runtime");
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
});
