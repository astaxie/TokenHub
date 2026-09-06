package server

import (
	"net/http"
	"time"
)

type billingPricingStore interface {
	BillingModelPricing(string) (modelPricingDraft, error)
	PreviewBillingModel(meteringRateCard, Usage, time.Time) (map[string]any, error)
	ApplyBillingModel(meteringRateCard, string, string, AdminUser) (modelPriceChange, error)
	BillingPriceChanges() ([]modelPriceChange, error)
}

func (s *Server) registerBillingWorkflowRoutes() {
	s.registerSingleMethodRoute("GET", "/api/admin/billing/models/{model}/pricing", s.handleBillingModelPricing, s.adminMethodNotAllowed("billing", "GET"))
	s.registerSingleMethodRoute("POST", "/api/admin/billing/model-pricing/apply", s.handleBillingModelApply, s.adminMethodNotAllowed("billing", "POST"))
	s.registerSingleMethodRoute("GET", "/api/admin/billing/price-changes", s.handleBillingPriceChanges, s.adminMethodNotAllowed("billing", "GET"))
	s.registerMethodRoutes("/api/admin/billing/statements", func(methods string) http.HandlerFunc { return s.adminMethodNotAllowed("billing", methods) }, methodRoute{Method: "GET", Handler: s.handleBillingStatements}, methodRoute{Method: "POST", Handler: s.handleBillingStatement})
}
func (s *Server) billingPricingStore(w http.ResponseWriter, r *http.Request) (billingPricingStore, bool) {
	store, ok := s.store.(billingPricingStore)
	if !ok {
		writeError(w, r, NewHTTPError(503, "metering_unavailable", "Billing persistence is unavailable"))
	}
	return store, ok
}
func (s *Server) handleBillingModelPricing(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	store, ok := s.billingPricingStore(w, r)
	if !ok {
		return
	}
	result, err := store.BillingModelPricing(r.PathValue("model"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, result)
}
func (s *Server) handleBillingModelApply(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "billing", r.Method)
	if !ok {
		return
	}
	store, ok := s.billingPricingStore(w, r)
	if !ok {
		return
	}
	var request struct {
		Card        meteringRateCard `json:"card"`
		Fingerprint string           `json:"fingerprint"`
		RequestID   string           `json:"request_id"`
		Confirmed   bool             `json:"confirmed"`
	}
	if err := s.decodeJSON(w, r, &request); err != nil {
		writeError(w, r, err)
		return
	}
	if !request.Confirmed {
		writeError(w, r, NewHTTPError(400, "pricing_confirmation_required", "Confirm the model price change before applying it"))
		return
	}
	result, err := store.ApplyBillingModel(request.Card, request.Fingerprint, request.RequestID, user)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !result.Replayed {
		s.recordAdminAudit(r, user, "apply_price", "model", result.ModelName, result.Before, result.After)
	}
	result.RequestHash = ""
	writeJSON(w, 200, map[string]any{"data": result, "effective": "new_requests"})
}
func (s *Server) handleBillingPriceChanges(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "billing", r.Method); !ok {
		return
	}
	store, ok := s.billingPricingStore(w, r)
	if !ok {
		return
	}
	result, err := store.BillingPriceChanges()
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"data": result, "limit": 100})
}
