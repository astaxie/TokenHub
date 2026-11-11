package server

import "encoding/json"

// Only read-only projections omit unknown prices. Model's ordinary JSON contract
// stays unchanged, including create requests that embed Model alongside routes.
type modelPricingView struct{ Model }

func modelPricingViews(models []Model) []modelPricingView {
	if models == nil {
		return nil
	}
	views := make([]modelPricingView, len(models))
	for index, model := range models {
		views[index] = modelPricingView{Model: model}
	}
	return views
}

func (view modelPricingView) MarshalJSON() ([]byte, error) {
	if !view.pricingUnavailable {
		return json.Marshal(view.Model)
	}
	return unknownPricingJSON(view.Model, []string{
		"input_price_usd_per_1m", "output_price_usd_per_1m", "embedding_price_usd_per_1m",
		"cache_read_price_usd_per_1m", "cache_write_price_usd_per_1m", "cache_write_5m_price_usd_per_1m", "cache_write_1h_price_usd_per_1m",
		"cache_write_price_configured", "cache_write_5m_price_configured", "cache_write_1h_price_configured", "pricing_periods",
	})
}

func (item modelListItem) MarshalJSON() ([]byte, error) {
	type plainModelListItem modelListItem
	if !item.pricingUnavailable {
		return json.Marshal(plainModelListItem(item))
	}
	return unknownPricingJSON(plainModelListItem(item), []string{"input_token_price_per_m", "output_token_price_per_m"})
}

func unknownPricingJSON(value any, fields []string) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, err
	}
	for _, field := range fields {
		delete(object, field)
	}
	object["pricing_available"] = json.RawMessage("false")
	return json.Marshal(object)
}
