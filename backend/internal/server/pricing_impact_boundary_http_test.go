package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPricingImpactHTTPValidatesScopeAndEmptyCoverage(t *testing.T) {
	app, _, token, _, _ := pricingImpactGateway(t)
	for _, tc := range []struct{ name, field, value string }{{"invalid timezone", "timezone", "Invalid/Zone"}, {"oversized range", "from", "2020-01-01"}} {
		t.Run(tc.name, func(t *testing.T) {
			query := pricingImpactRequest(t, app, token)
			query[tc.field] = tc.value
			response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", query, token)
			if response.Code != 400 {
				t.Fatalf("invalid scope accepted: %d %s", response.Code, response.Body)
			}
		})
	}
	query := pricingImpactRequest(t, app, token)
	unauthorized := httptest.NewRecorder()
	app.ServeHTTP(unauthorized, httptest.NewRequest("POST", "/api/admin/billing/model-pricing/impact", strings.NewReader("{}")))
	if unauthorized.Code != 401 {
		t.Fatalf("unauthenticated analysis: %d %s", unauthorized.Code, unauthorized.Body)
	}
	query["timezone"] = "Asia/Shanghai"
	query["from"] = "2020-01-01"
	query["to"] = "2020-01-02"
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", query, token)
	if response.Code != 200 {
		t.Fatalf("empty scope: %d %s", response.Code, response.Body)
	}
	var report pricingImpactReport
	if err := json.Unmarshal([]byte(response.Body), &report); err != nil {
		t.Fatal(err)
	}
	if !report.From.Equal(time.Date(2019, 12, 31, 16, 0, 0, 0, time.UTC)) || report.Requests != 0 || report.Overall != nil || report.RequestCoverage != nil || report.ChargeCoverage != nil {
		t.Fatalf("timezone or empty coverage misrepresented: %s", response.Body)
	}
}

func TestPricingImpactHTTPRejectsOversizedCohort(t *testing.T) {
	app, store, token, _, _ := pricingImpactGateway(t)
	records := make([]UsageRecord, statementRowLimit+1)
	for i := range records {
		id := fmt.Sprintf("oversized-analysis-%d", i)
		records[i] = UsageRecord{ID: id, RequestID: id, ModelName: "claude-tokenhub-test", CreatedAt: time.Now().UTC()}
	}
	if err := store.db.CreateInBatches(records, 100).Error; err != nil {
		t.Fatal(err)
	}
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", pricingImpactRequest(t, app, token), token)
	if response.Code != 422 || !strings.Contains(response.Body, "statement_too_large") {
		t.Fatalf("oversized cohort was not rejected: %d %s", response.Code, response.Body)
	}
}

func TestPricingImpactHTTPRejectsTamperedReceipt(t *testing.T) {
	app, _, token, _, _ := pricingImpactGateway(t)
	query := pricingImpactRequest(t, app, token)
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/impact", query, token)
	var report pricingImpactReport
	if response.Code != 200 {
		t.Fatalf("analysis: %d %s", response.Code, response.Body)
	}
	if err := json.Unmarshal([]byte(response.Body), &report); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(report.Receipt, ".")
	replacement := "A"
	if parts[1][0] == 'A' {
		replacement = "B"
	}
	receipt := parts[0] + "." + replacement + parts[1][1:]
	response = doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", map[string]any{"card": query["card"], "fingerprint": query["fingerprint"], "request_id": "tampered-analysis-receipt", "confirmed": true, "risk_acknowledged": true, "analysis_receipt": receipt}, token)
	if response.Code != 409 {
		t.Fatalf("tampered receipt accepted: %d %s", response.Code, response.Body)
	}
}
