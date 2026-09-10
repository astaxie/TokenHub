package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	billingstore "tokenhub/backend/internal/billing/persistence"
	"tokenhub/backend/internal/metering"
)

func statementFixture(t *testing.T, s *GormStore, id, project string, at time.Time, amounts ...string) {
	t.Helper()
	price := meteringPriceSnapshot{Currency: "USD", Source: "legacy_float_configuration", Rates: metering.Rates{Input: "10"}, At: at}
	a := meteringRequestSnapshot{RequestID: id, ProjectID: project, TeamID: "original-team", APIKeyID: "key", ModelName: "retail", Price: price, LegacyPrice: &price}
	if err := saveMeteringEntry(s.db, id+":admission", "admission", id, a, at); err != nil {
		t.Fatal(err)
	}
	attempts := []meteringAttemptCharge{}
	for n, amount := range amounts {
		aid := id + ":" + string(rune('a'+n))
		p := price
		p.Rates = metering.Rates{Input: amount}
		prepared := meteringAttemptSnapshot{ID: aid, RequestID: id, ProviderID: "supplier", ResourceID: "account", UpstreamModel: "wholesale", At: at.Add(time.Duration(n) * time.Hour), Price: &p}
		if err := saveMeteringEntry(s.db, aid, "attempt_prepared", id, prepared, prepared.At); err != nil {
			t.Fatal(err)
		}
		charge, err := metering.Price(p.Rates, metering.Units{Input: 1000000}, "USD", "")
		if err != nil {
			t.Fatal(err)
		}
		attempts = append(attempts, meteringAttemptCharge{ID: aid, Status: 500, Charge: meteringShadowCharge{Status: "estimated", Charge: &charge, Price: &p, Units: metering.Units{Input: 1000000}}})
	}
	result := map[string]any{"tenant": meteringShadowCharge{Status: "estimated", LegacyUSD: "10", Units: metering.Units{Input: 1000000}}, "attempts": attempts}
	if err := saveMeteringEntry(s.db, id+":shadow", "shadow_settlement", id, result, at.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
}
func statementTestQuery(side string) statementQuery {
	return statementQuery{Side: side, From: "2020-01-01", To: "2020-02-01", Timezone: "UTC", Customer: "Example customer", ProjectIDs: []string{"project-a"}}
}

func TestBillingStatementSeparatesRetriesAndTenantCharges(t *testing.T) {
	s := NewMemoryStore()
	at := time.Date(2020, 1, 31, 23, 30, 0, 0, time.UTC)
	statementFixture(t, s, "one", "project-a", at, "2", "3")
	statementFixture(t, s, "other", "project-b", at, "100")
	q := statementTestQuery("margin")
	out, err := s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 3 || out.EstimatedMarginUSD == nil || *out.EstimatedMarginUSD != "5.000000000000" {
		t.Fatalf("invalid margin: %+v", out)
	}
	// Current price and project changes must not rewrite historical statements.
	if err := s.db.Create(&Project{ID: "project-a", TeamID: "new-team"}).Error; err != nil {
		t.Fatal(err)
	}
	q.Side = "tenant"
	out, err = s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].TeamID != "original-team" || *out.Rows[0].Amount != "10" {
		t.Fatalf("invalid tenant statement: %+v", out)
	}
	payload, _ := json.Marshal(out)
	for _, secret := range []string{"supplier", "account", "wholesale", "provider_estimate", "estimated_margin_usd\":\""} {
		if strings.Contains(string(payload), secret) {
			t.Fatalf("tenant payload leaks %q", secret)
		}
	}
	q.Side = "provider"
	q.ProjectIDs = nil
	out, err = s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 2 {
		t.Fatalf("procurement must use attempt start, got %d rows", len(out.Rows))
	}
}

func TestBillingStatementDistinguishesFreeUnknownAndLegacy(t *testing.T) {
	s := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	statementFixture(t, s, "free", "project-a", at, "0")
	q := statementTestQuery("margin")
	out, err := s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if out.UnknownCount != 0 || out.EstimatedMarginUSD == nil || *out.EstimatedMarginUSD != "10.000000000000" {
		t.Fatalf("free is not unknown: %+v", out)
	}
	if err := s.db.Create(&UsageRecord{ID: "legacy", ProjectID: "project-a", CostUSD: 20, ProviderCostUSD: 0, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	out, err = s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if out.UnknownCount != 1 || out.EstimatedMarginUSD != nil {
		t.Fatalf("legacy zero is not proven free: %+v", out)
	}
	for _, r := range out.Rows {
		if r.ID == "legacy:provider" && r.Amount != nil {
			t.Fatal("retail substituted for missing cost")
		}
	}
}

func TestBillingStatementIncludesUnfinishedAttempt(t *testing.T) {
	s := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	statementFixture(t, s, "unfinished", "project-a", at, "2")
	if err := s.db.Delete(&meteringEntry{}, "id = ?", "unfinished:shadow").Error; err != nil {
		t.Fatal(err)
	}
	q := statementTestQuery("margin")
	out, err := s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if out.UnknownCount != 2 || out.EstimatedMarginUSD != nil {
		t.Fatalf("unfinished evidence: %+v", out)
	}
}

func TestBillingStatementExternalCurrenciesAndOverlap(t *testing.T) {
	s := NewMemoryStore()
	from := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := s.db.Create(&billingstore.ConnectorRow{ID: "connector", Config: map[string]string{"provider_id": "supplier"}, CredentialCiphertext: "secret"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&billingstore.RecordRow{ID: "external", ExternalID: "supplier-line", UsageQuantity: 123, UsageUnit: "tokens", ConnectorID: "connector", Currency: "CNY", NetAmount: "-2.5", UsageStartAt: from.Add(-time.Hour), UsageEndAt: from.Add(time.Hour), SourceTimezone: "Asia/Shanghai", RawSnapshotID: "secret", CreatedAt: from}).Error; err != nil {
		t.Fatal(err)
	}
	q := statementTestQuery("provider")
	q.ProjectIDs = nil
	q.ProviderID = "supplier"
	out, err := s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].ExternalID != "supplier-line" || out.Rows[0].UsageQuantity != 123 || out.Rows[0].Status != "period_overlap" || out.Rows[0].USD != nil || out.Totals["provider_billed:CNY"] != "-2.500000000000" {
		t.Fatalf("external data: %+v", out)
	}
	payload, _ := json.Marshal(out)
	if strings.Contains(string(payload), "secret") {
		t.Fatal("secret exported")
	}
}

func TestBillingStatementValidatesRangeAndScope(t *testing.T) {
	q := statementTestQuery("tenant")
	q.Timezone = "America/New_York"
	q.From = "2020-03-08"
	q.To = "2020-03-09"
	from, to, err := q.window()
	if err != nil || to.Sub(from) != 23*time.Hour {
		t.Fatalf("DST window: %v %v", to.Sub(from), err)
	}
	for _, mutate := range []func(*statementQuery){func(q *statementQuery) { q.ProjectIDs = nil }, func(q *statementQuery) { q.Customer = "" }, func(q *statementQuery) { q.Timezone = "invalid" }, func(q *statementQuery) { q.To = "2021-01-01" }, func(q *statementQuery) { q.ProviderID = "supplier" }} {
		q := statementTestQuery("tenant")
		mutate(&q)
		if _, _, err := q.window(); err == nil {
			t.Fatalf("accepted invalid query %+v", q)
		}
	}
}

func TestBillingStatementRequiresPlatformAdministrator(t *testing.T) {
	store, app := newMethodRoutingBillingServer(t, "statement-routing-password")
	for _, role := range []string{"user", "team_leader", "security_admin"} {
		token := createAdminOperationMethodRoutingSession(t, store, "statements-"+role, role)
		req := httptest.NewRequest(http.MethodPost, "/api/admin/billing/statements", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		app.ServeHTTP(response, req)
		if response.Code != 403 {
			t.Fatalf("role %s: %d", role, response.Code)
		}
	}
}

func TestBillingStatementFiltersBeforeApplyingLimit(t *testing.T) {
	s := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	statementFixture(t, s, "selected", "project-a", at, "2")
	entries := make([]meteringEntry, statementRowLimit+1)
	for i := range entries {
		entries[i] = meteringEntry{ID: fmt.Sprintf("noise-%d", i), Kind: "admission", Scope: fmt.Sprintf("noise-%d", i), Payload: `{"project_id":"unrelated","model":"other"}`, CreatedAt: at}
	}
	if err := s.db.CreateInBatches(entries, 100).Error; err != nil {
		t.Fatal(err)
	}
	out, err := s.BillingStatement(context.Background(), statementTestQuery("tenant"))
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("expected selected customer only, got %d", len(out.Rows))
	}
}

func TestBillingStatementLegacyProviderModelUsesRequestEvidence(t *testing.T) {
	s := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	if err := s.db.Create(&UsageRecord{ID: "legacy", RequestID: "request", ProjectID: "project-a", ProviderID: "supplier", ModelName: "retail", CostUSD: 10, ProviderCostUSD: 2, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&RequestLog{ID: "log", RequestID: "request", ProviderModel: "wholesale", CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Create(&UsageRecord{ID: "unknown", RequestID: "unknown-request", ProjectID: "project-a", ProviderID: "supplier", ModelName: "retail", CostUSD: 10, ProviderCostUSD: 3, CreatedAt: at}).Error; err != nil {
		t.Fatal(err)
	}
	q := statementTestQuery("provider")
	q.Model = "wholesale"
	out, err := s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 || out.Rows[0].Model != "wholesale" || *out.Rows[0].Amount != "2.000000000000" {
		t.Fatalf("legacy model: %+v", out)
	}
}

func TestBillingStatementMissingFXBlocksMargin(t *testing.T) {
	s := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	statementFixture(t, s, "fx", "project-a", at, "2")
	var entry meteringEntry
	if err := s.db.First(&entry, "id = ?", "fx:shadow").Error; err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Tenant   meteringShadowCharge    `json:"tenant"`
		Attempts []meteringAttemptCharge `json:"attempts"`
	}
	if err := json.Unmarshal([]byte(entry.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	payload.Attempts[0].Charge.Charge.Currency = "CNY"
	payload.Attempts[0].Charge.Charge.USD = ""
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&entry).Update("payload", string(encoded)).Error; err != nil {
		t.Fatal(err)
	}
	out, err := s.BillingStatement(context.Background(), statementTestQuery("margin"))
	if err != nil {
		t.Fatal(err)
	}
	if out.UnknownCount != 1 || out.EstimatedMarginUSD != nil {
		t.Fatalf("missing FX must suppress margin: %+v", out)
	}
}

func TestBillingStatementPriceEvidenceToleratesOnlyDecimalQuantum(t *testing.T) {
	if !equalStatementMoney("0.300000000000", "0.30000000000000004") {
		t.Fatal("floating tail is not a price change")
	}
	if equalStatementMoney("0.300000000000", "0.300001") {
		t.Fatal("different prices must remain incomplete")
	}
	if equalStatementMoney("0.300000000000", "NaN") {
		t.Fatal("invalid amount accepted")
	}
}

func TestBillingStatementLegacyModelFilterPrecedesLimit(t *testing.T) {
	s := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	usages := make([]UsageRecord, statementRowLimit+1)
	logs := make([]RequestLog, len(usages))
	for i := range usages {
		id := fmt.Sprintf("old-%d", i)
		usages[i] = UsageRecord{ID: id, RequestID: id, ProjectID: "project-a", ProviderID: "supplier", CostUSD: 10, ProviderCostUSD: 2, CreatedAt: at}
		logs[i] = RequestLog{ID: id, RequestID: id, ProviderModel: "other", CreatedAt: at}
	}
	logs[0].ProviderModel = "selected"
	if err := s.db.CreateInBatches(usages, 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.CreateInBatches(logs, 100).Error; err != nil {
		t.Fatal(err)
	}
	q := statementTestQuery("provider")
	q.ProviderID = "supplier"
	q.Model = "selected"
	out, err := s.BillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Rows) != 1 {
		t.Fatalf("expected one selected model, got %d", len(out.Rows))
	}
}
