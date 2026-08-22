package server

type ModelPricingPeriod struct {
	Name                string   `json:"name,omitempty" yaml:"name,omitempty"`
	Timezone            string   `json:"timezone,omitempty" yaml:"timezone,omitempty"`
	StartTime           string   `json:"start_time,omitempty" yaml:"start_time,omitempty"`
	EndTime             string   `json:"end_time,omitempty" yaml:"end_time,omitempty"`
	EffectiveFrom       string   `json:"effective_from,omitempty" yaml:"effective_from,omitempty"`
	EffectiveUntil      string   `json:"effective_until,omitempty" yaml:"effective_until,omitempty"`
	InputPriceUSDPer1M  *float64 `json:"input_price_usd_per_1m,omitempty" yaml:"input_price_usd_per_1m,omitempty"`
	OutputPriceUSDPer1M *float64 `json:"output_price_usd_per_1m,omitempty" yaml:"output_price_usd_per_1m,omitempty"`
}
