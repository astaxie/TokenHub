export const statementCurrencies = ["USD", "CNY", "JPY", "RUB"] as const;
export type StatementCurrency = typeof statementCurrencies[number];
export type StatementCurrencyPreferences = { currency: StatementCurrency; rates: Partial<Record<StatementCurrency, string>> };
export const statementCurrencyStorageKey = "tokenhub.billing-statement-currency.v1";

export function normalizeStatementExchangeRateInput(value: string, locale: string): string {
  const separator = new Intl.NumberFormat(locale).formatToParts(1.1).find(part => part.type === "decimal")?.value;
  return separator && separator !== "." ? value.replaceAll(separator, ".") : value;
}

export function validStatementExchangeRate(value: string): boolean {
  return /^(0|[1-9][0-9]{0,17})(\.[0-9]{1,12})?$/.test(value) && /[1-9]/.test(value);
}

export function readStatementCurrencyPreferences(saved: string | null): StatementCurrencyPreferences {
  const defaults: StatementCurrencyPreferences = { currency: "USD", rates: {} };
  try {
    const value = JSON.parse(saved ?? "null");
    if (!value || typeof value !== "object") return defaults;
    const currency = statementCurrencies.includes(value.currency) ? value.currency as StatementCurrency : "USD";
    const rates: StatementCurrencyPreferences["rates"] = {};
    for (const code of statementCurrencies) {
      if (code !== "USD" && typeof value.rates?.[code] === "string" && validStatementExchangeRate(value.rates[code])) rates[code] = value.rates[code];
    }
    return { currency, rates };
  } catch { return defaults; }
}
