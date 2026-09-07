package server

import (
	"encoding/json"
	"testing"
)

func TestModelPricingCheckHTTPDoesNotRequireSampleUsage(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	token := billingAdminToken(t, store)
	app := New(store).Handler()
	loaded := doJSON(t, app, "GET", "/api/admin/billing/models/user-quota-model/pricing", nil, token)
	var draft struct {
		Card        meteringRateCard `json:"card"`
		Fingerprint string           `json:"fingerprint"`
	}
	if err := json.Unmarshal([]byte(loaded.Body), &draft); err != nil {
		t.Fatal(err)
	}
	draft.Card.Rates.Input = "3"
	checked := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/check", map[string]any{"card": draft.Card, "fingerprint": draft.Fingerprint}, token)
	if checked.Code != 200 {
		t.Fatalf("direct price validation: %d %s", checked.Code, checked.Body)
	}
	var body struct {
		Changed  bool             `json:"changed"`
		Proposed meteringRateCard `json:"proposed"`
	}
	if err := json.Unmarshal([]byte(checked.Body), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Changed || body.Proposed.Rates.Input != "3" {
		t.Fatalf("invalid candidate: %+v", body)
	}
	read := doJSON(t, app, "GET", "/api/admin/billing/models/user-quota-model/pricing", nil, token)
	if err := json.Unmarshal([]byte(read.Body), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Card.Rates.Input != "2" {
		t.Fatal("validation changed actual pricing")
	}
}

func TestModelPricingHTTPRequiresRiskAcknowledgment(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	token := billingAdminToken(t, store)
	app := New(store).Handler()
	loaded := doJSON(t, app, "GET", "/api/admin/billing/models/user-quota-model/pricing", nil, token)
	var draft modelPricingDraft
	if err := json.Unmarshal([]byte(loaded.Body), &draft); err != nil {
		t.Fatal(err)
	}
	draft.Card.Rates.Input = "3"
	body := map[string]any{"card": draft.Card, "fingerprint": draft.Fingerprint, "request_id": "risk-confirmation-test", "confirmed": true}
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", body, token)
	if response.Code != 400 {
		t.Fatalf("unacknowledged risk accepted: %d %s", response.Code, response.Body)
	}
	body["risk_acknowledged"] = true
	response = doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", body, token)
	if response.Code != 200 {
		t.Fatalf("acknowledged change rejected: %d %s", response.Code, response.Body)
	}
}

func TestModelPricingHTTPPersistsRiskAcknowledgment(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	token := billingAdminToken(t, store)
	app := New(store).Handler()
	loaded := doJSON(t, app, "GET", "/api/admin/billing/models/user-quota-model/pricing", nil, token)
	var draft modelPricingDraft
	if err := json.Unmarshal([]byte(loaded.Body), &draft); err != nil {
		t.Fatal(err)
	}
	draft.Card.Rates.Input = "3"
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", map[string]any{"card": draft.Card, "fingerprint": draft.Fingerprint, "request_id": "persist-risk-confirmation", "confirmed": true, "risk_acknowledged": true}, token)
	if response.Code != 200 {
		t.Fatalf("apply: %d %s", response.Code, response.Body)
	}
	history := doJSON(t, app, "GET", "/api/admin/billing/price-changes", nil, token)
	var body struct {
		Data []modelPriceChange `json:"data"`
	}
	if err := json.Unmarshal([]byte(history.Body), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) < 1 || !body.Data[0].RiskAcknowledged || len(body.Data[0].AcknowledgedRisks) != 1 || body.Data[0].AcknowledgedRisks[0] != "analysis_not_performed" {
		t.Fatalf("risk confirmation not preserved: %+v", body)
	}
}
