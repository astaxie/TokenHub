export const modelPricingPeriodsJSONError = "model_pricing_periods_must_be_json_array";
export const modelPricingPeriodsObjectArrayError = "model_pricing_periods_must_be_object_array";
export const modelPricingPeriodsInvalidPeriodError = "model_pricing_periods_invalid_period";

export function parseModelPricingPeriods(value?: string) {
  const raw = value?.trim();
  if (!raw) return [];
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error(modelPricingPeriodsJSONError);
  }
  if (!Array.isArray(parsed) || parsed.some((item) => item == null || typeof item !== "object" || Array.isArray(item))) {
    throw new Error(modelPricingPeriodsObjectArrayError);
  }
  if (parsed.some((item) => !validPricingPeriod(item as Record<string, unknown>))) {
    throw new Error(modelPricingPeriodsInvalidPeriodError);
  }
  return parsed;
}

function validPricingPeriod(period: Record<string, unknown>) {
  return validTimezone(period.timezone) &&
    validClockPair(period.start_time, period.end_time) &&
    validRFC3339(period.effective_from) &&
    validRFC3339(period.effective_until) &&
    validPriceOverrides(period);
}

const pricingPeriodPriceFields = [
  "input_price_usd_per_1m",
  "output_price_usd_per_1m",
];

function validTimezone(value: unknown) {
  if (value == null || String(value).trim() === "") return true;
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: String(value).trim() }).format(new Date(0));
    return true;
  } catch {
    return false;
  }
}

function validClockPair(start: unknown, end: unknown) {
  const startText = start == null ? "" : String(start).trim();
  const endText = end == null ? "" : String(end).trim();
  if (!startText && !endText) return true;
  return /^([01]\d|2[0-3]):[0-5]\d$/.test(startText) && /^([01]\d|2[0-3]):[0-5]\d$/.test(endText);
}

function validRFC3339(value: unknown) {
  if (value == null || String(value).trim() === "") return true;
  const text = String(value).trim();
  return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(text) && !Number.isNaN(Date.parse(text));
}

function validPriceOverrides(period: Record<string, unknown>) {
  return pricingPeriodPriceFields.every((field) => {
    const value = period[field];
    return value == null || (typeof value === "number" && Number.isFinite(value) && value >= 0);
  });
}
