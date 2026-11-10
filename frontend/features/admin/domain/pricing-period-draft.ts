import type { ModelPricingPeriod } from "../core/types";
import { parseModelPricingPeriods } from "./model-pricing-periods";

export const pricingPeriodRateKeys = [
  "input_price_usd_per_1m", "cache_read_price_usd_per_1m", "output_price_usd_per_1m",
  "cache_write_price_usd_per_1m", "cache_write_5m_price_usd_per_1m", "cache_write_1h_price_usd_per_1m",
] as const;
export type PricingPeriodRateKey = typeof pricingPeriodRateKeys[number];
export type PricingPeriodDraft = Omit<ModelPricingPeriod, PricingPeriodRateKey> & Record<PricingPeriodRateKey, string>;

export function pricingPeriodDrafts(periods: ModelPricingPeriod[] = []): PricingPeriodDraft[] {
  return periods.map(period => ({
    ...period,
    ...normalizedScheduleFields(period),
    input_price_usd_per_1m: priceText(period.input_price_usd_per_1m),
    cache_read_price_usd_per_1m: priceText(period.cache_read_price_usd_per_1m),
    output_price_usd_per_1m: priceText(period.output_price_usd_per_1m),
    cache_write_price_usd_per_1m: priceText(period.cache_write_price_usd_per_1m),
    cache_write_5m_price_usd_per_1m: priceText(period.cache_write_5m_price_usd_per_1m),
    cache_write_1h_price_usd_per_1m: priceText(period.cache_write_1h_price_usd_per_1m),
  }));
}

function priceText(value?: number) { return value == null ? "" : String(value); }

function normalizedScheduleFields(period: Pick<ModelPricingPeriod, "timezone" | "start_time" | "end_time" | "effective_from" | "effective_until">) {
  return {
    timezone: period.timezone?.trim() || undefined,
    start_time: period.start_time?.trim() || undefined,
    end_time: period.end_time?.trim() || undefined,
    effective_from: period.effective_from?.trim() || undefined,
    effective_until: period.effective_until?.trim() || undefined,
  };
}

export function newPricingPeriodDraft(): PricingPeriodDraft {
  return pricingPeriodDrafts([{ timezone: "UTC", start_time: "00:00", end_time: "08:00" }])[0];
}

export function serializePricingPeriodDrafts(drafts: PricingPeriodDraft[]): ModelPricingPeriod[] {
  if (drafts.length > 64) throw new Error("pricing_period_limit");
  const periods = drafts.map(draft => {
    const period: ModelPricingPeriod = {
      ...draft,
      ...normalizedScheduleFields(draft),
      input_price_usd_per_1m: undefined, cache_read_price_usd_per_1m: undefined, output_price_usd_per_1m: undefined,
      cache_write_price_usd_per_1m: undefined, cache_write_5m_price_usd_per_1m: undefined, cache_write_1h_price_usd_per_1m: undefined,
    };
    for (const key of pricingPeriodRateKeys) {
      const value = draft[key].trim();
      if (!value) delete period[key];
      else {
        const rate = Number(value);
        if (!Number.isFinite(rate) || rate < 0) throw new Error("pricing_period_invalid_rate");
        period[key] = rate;
      }
    }
    return period;
  });
  return parseModelPricingPeriods(JSON.stringify(periods)) as ModelPricingPeriod[];
}
