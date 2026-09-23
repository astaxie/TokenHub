package server

const cacheReadConfiguredKey = "cache_read_price_configured"

func pricingWeekdayMatches(days []int, day int) bool {
	if len(days) == 0 {
		return true
	}
	for _, configured := range days {
		if configured == day {
			return true
		}
	}
	return false
}

func withConfiguredCacheReadPrice(metadata map[string]string, configured bool) map[string]string {
	result := make(map[string]string, len(metadata)+1)
	for key, value := range metadata {
		result[key] = value
	}
	if configured {
		result[cacheReadConfiguredKey] = "true"
	} else {
		delete(result, cacheReadConfiguredKey)
	}
	return result
}
