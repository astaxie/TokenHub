package server

import (
	"strconv"
	"tokenhub/backend/internal/metering"
)

const retrievalSearchUnitPriceKey = "search_unit_price_usd"

func retrievalPricingEvidence(modality string, usage Usage) *RetrievalUsageEvidence {
	// Older models and snapshots omit modality. Preserve their retrieval
	// semantics, but never let unrelated evidence override a known modality.
	if modality == "" || modality == "embedding" || modality == "rerank" {
		return usage.RetrievalEvidence
	}
	return nil
}

func nativeRetrievalCost(model Model, usage Usage) (float64, bool) {
	e := retrievalPricingEvidence(model.Modality, usage)
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
