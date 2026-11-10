export type ModelPricingPeriod = {
  name?: string;
  timezone?: string;
  weekdays?: number[];
  start_time?: string;
  end_time?: string;
  effective_from?: string;
  effective_until?: string;
  input_price_usd_per_1m?: number;
  output_price_usd_per_1m?: number;
  cache_read_price_usd_per_1m?: number;
  cache_write_price_usd_per_1m?: number;
  cache_write_5m_price_usd_per_1m?: number;
  cache_write_1h_price_usd_per_1m?: number;
};
