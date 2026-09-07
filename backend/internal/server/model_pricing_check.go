package server

import (
	"context"
	"gorm.io/gorm"
	"net/http"
)

type modelPricingCheck struct {
	Current     meteringRateCard `json:"current"`
	Proposed    meteringRateCard `json:"proposed"`
	Fingerprint string           `json:"fingerprint"`
	Changed     bool             `json:"changed"`
}

func checkedModelPricing(db *gorm.DB, card meteringRateCard, expected string) (Model, Model, modelPricingCheck, error) {
	var current Model
	if err := db.First(&current, "name = ?", card.Target).Error; err != nil {
		return current, current, modelPricingCheck{}, notFound(err, "model_not_found", "Model not found")
	}
	fingerprint := pricingFingerprint(current)
	if expected == "" || expected != fingerprint {
		return current, current, modelPricingCheck{}, NewHTTPError(409, "model_price_changed", "Model pricing changed after preview; reload current prices")
	}
	proposed, err := candidateModelPricing(current, card)
	if err != nil {
		return current, current, modelPricingCheck{}, err
	}
	return current, proposed, modelPricingCheck{Current: modelPricingCard(current), Proposed: modelPricingCard(proposed), Fingerprint: fingerprint, Changed: fingerprint != pricingFingerprint(proposed)}, nil
}
func (s *GormStore) CheckModelPricing(ctx context.Context, card meteringRateCard, expected string) (modelPricingCheck, error) {
	_, _, check, err := checkedModelPricing(s.db.WithContext(ctx), card, expected)
	return check, err
}
func (s *Server) handleCheckModelPricing(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	store, ok := s.store.(interface {
		CheckModelPricing(context.Context, meteringRateCard, string) (modelPricingCheck, error)
	})
	if !ok {
		writeError(w, r, NewHTTPError(503, "pricing_unavailable", "Pricing is unavailable"))
		return
	}
	var q struct {
		Card        meteringRateCard `json:"card"`
		Fingerprint string           `json:"fingerprint"`
	}
	if err := s.decodeJSON(w, r, &q); err != nil {
		writeError(w, r, err)
		return
	}
	result, err := store.CheckModelPricing(r.Context(), q.Card, q.Fingerprint)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}
