package server

type ModelPricingPeriod struct {
	Name                      string   `json:"name,omitempty" yaml:"name,omitempty"`
	Timezone                  string   `json:"timezone,omitempty" yaml:"timezone,omitempty"`
	Weekdays                  []int    `json:"weekdays,omitempty" yaml:"weekdays,omitempty"`
	StartTime                 string   `json:"start_time,omitempty" yaml:"start_time,omitempty"`
	EndTime                   string   `json:"end_time,omitempty" yaml:"end_time,omitempty"`
	EffectiveFrom             string   `json:"effective_from,omitempty" yaml:"effective_from,omitempty"`
	EffectiveUntil            string   `json:"effective_until,omitempty" yaml:"effective_until,omitempty"`
	InputPriceUSDPer1M        *float64 `json:"input_price_usd_per_1m,omitempty" yaml:"input_price_usd_per_1m,omitempty"`
	OutputPriceUSDPer1M       *float64 `json:"output_price_usd_per_1m,omitempty" yaml:"output_price_usd_per_1m,omitempty"`
	CacheReadPriceUSDPer1M    *float64 `json:"cache_read_price_usd_per_1m,omitempty" yaml:"cache_read_price_usd_per_1m,omitempty"`
	CacheWritePriceUSDPer1M   *float64 `json:"cache_write_price_usd_per_1m,omitempty" yaml:"cache_write_price_usd_per_1m,omitempty"`
	CacheWrite5mPriceUSDPer1M *float64 `json:"cache_write_5m_price_usd_per_1m,omitempty" yaml:"cache_write_5m_price_usd_per_1m,omitempty"`
	CacheWrite1hPriceUSDPer1M *float64 `json:"cache_write_1h_price_usd_per_1m,omitempty" yaml:"cache_write_1h_price_usd_per_1m,omitempty"`
}
