package server

import (
	"strconv"
	"tokenhub/backend/internal/metering"
)

const retrievalSearchUnitPriceKey = "search_unit_price_usd"

func nativeRetrievalCost(model Model, usage Usage) (float64, bool) {
	e := usage.RetrievalEvidence
	if !validNativeRetrievalEvidence(e) {
		return 0, false
	}
	charge, err := metering.PriceNative(e.Unit, *e.Quantity, model.Metadata[retrievalSearchUnitPriceKey], "USD", "")
	if err != nil {
		return 0, false
	}
	amount, err := strconv.ParseFloat(charge.USD, 64)
	return amount, err == nil
}
