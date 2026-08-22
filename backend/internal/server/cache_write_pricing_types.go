package server

type CacheWritePriceConfiguration struct {
	CacheWritePriceConfigured   bool `json:"cache_write_price_configured,omitempty"`
	CacheWrite5mPriceConfigured bool `json:"cache_write_5m_price_configured,omitempty"`
	CacheWrite1hPriceConfigured bool `json:"cache_write_1h_price_configured,omitempty"`
}
