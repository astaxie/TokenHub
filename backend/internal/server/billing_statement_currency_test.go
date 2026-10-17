package server

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	billingstore "tokenhub/backend/internal/billing/persistence"
)

func TestBillingStatementReportingCurrency(t *testing.T) {
	store := NewMemoryStore()
	statementFixture(t, store, "converted", "project-a", time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC), "2", "3")
	for _, test := range []struct {
		currency, rate, tenant, provider, margin string
	}{
		{"USD", "", "10.000000000000", "5.000000000000", "5.000000000000"},
		{"CNY", "7.25", "72.500000000000", "36.250000000000", "36.250000000000"},
		{"JPY", "150.5", "1505.000000000000", "752.500000000000", "752.500000000000"},
		{"RUB", "90.125", "901.250000000000", "450.625000000000", "450.625000000000"},
	} {
		t.Run(test.currency, func(t *testing.T) {
			query := statementTestQuery("margin")
			query.DisplayCurrency, query.DisplayExchangeRate = test.currency, test.rate
			result, err := store.BillingStatement(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			if result.Conversion == nil || result.Conversion.Currency != test.currency || result.Conversion.USDExchangeRate != result.Query.DisplayExchangeRate {
				t.Fatalf("missing conversion snapshot: %+v", result)
			}
			if result.DisplayTotals["tenant"] != test.tenant || result.DisplayTotals["provider_estimate"] != test.provider || **result.DisplayEstimatedMargin != test.margin {
				t.Fatalf("unexpected converted totals: %+v", result)
			}
			if len(result.Rows) != 3 || result.DisplayTotals["provider_billed"] != "" || result.Totals["tenant:USD"] != "10.000000000000" || *result.EstimatedMarginUSD != "5.000000000000" {
				t.Fatalf("reporting conversion changed source accounting: %+v", result)
			}
			for _, row := range result.Rows {
				if row.DisplayAmount == nil || *row.DisplayAmount == nil || row.Currency != "USD" {
					t.Fatalf("unexpected row: %+v", row)
				}
			}
		})
	}
}

func TestBillingStatementReportingCurrencyValidatesHTTPInput(t *testing.T) {
	store, app := newMethodRoutingBillingServer(t, "currency-validation-password")
	token := createAdminOperationMethodRoutingSession(t, store, "currency-admin", "admin")
	for _, test := range []struct{ currency, rate string }{
		{"EUR", "1"}, {"", "7"}, {"CNY", ""}, {"CNY", "0"}, {"CNY", "-1"},
		{"CNY", "NaN"}, {"CNY", "1e2"}, {"CNY", "01"}, {"CNY", "1.0000000000001"},
		{"CNY", "1000000000000000000"}, {"USD", "2"},
	} {
		t.Run(test.currency+"/"+test.rate, func(t *testing.T) {
			query := statementTestQuery("tenant")
			query.DisplayCurrency, query.DisplayExchangeRate = test.currency, test.rate
			body, err := json.Marshal(query)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/admin/billing/statements", strings.NewReader(string(body)))
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_statement_conversion") {
				t.Fatalf("invalid reporting conversion accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestBillingStatementReportingCurrencyDoesNotChangeLegacyJSON(t *testing.T) {
	store := NewMemoryStore()
	statementFixture(t, store, "legacy-json", "project-a", time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC), "2")
	result, err := store.BillingStatement(context.Background(), statementTestQuery("tenant"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"conversion", "display_currency", "display_exchange_rate", "display_amount", "display_totals", "display_estimated_margin"} {
		if strings.Contains(string(body), `"`+field+`"`) {
			t.Fatalf("legacy response contains new field %s: %s", field, body)
		}
	}
}

func TestBillingStatementReportingCurrencyPreservesUnknownAndDirectAmounts(t *testing.T) {
	store := NewMemoryStore()
	at := time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC)
	for _, record := range []billingstore.RecordRow{
		{ID: "cny", Currency: "CNY", NetAmount: "5", UsageStartAt: at, UsageEndAt: at.Add(time.Hour), CreatedAt: at},
		{ID: "jpy", Currency: "JPY", NetAmount: "100", UsageStartAt: at, UsageEndAt: at.Add(time.Hour), CreatedAt: at},
		{ID: "usd-refund", Currency: "USD", NetAmount: "-2.5", UsageStartAt: at, UsageEndAt: at.Add(time.Hour), CreatedAt: at},
	} {
		record.ExternalID = record.ID
		if err := store.db.Create(&record).Error; err != nil {
			t.Fatal(err)
		}
	}
	query := statementTestQuery("provider")
	query.ProjectIDs = nil
	query.DisplayCurrency, query.DisplayExchangeRate = "CNY", "7"
	result, err := store.BillingStatement(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if result.UnknownCount != 2 || result.DisplayTotals["provider_billed"] != "-12.500000000000" || result.DisplayEstimatedMargin == nil || *result.DisplayEstimatedMargin != nil {
		t.Fatalf("unexpected conversion completeness: %+v", result)
	}
	for _, row := range result.Rows {
		if row.DisplayAmount == nil {
			t.Fatal("requested display amount was omitted")
		}
		switch row.ID {
		case "cny":
			if row.USD != nil || *row.DisplayAmount == nil || **row.DisplayAmount != "5.000000000000" {
				t.Fatalf("same-currency original amount was lost: %+v", row)
			}
		case "jpy":
			if *row.DisplayAmount != nil {
				t.Fatalf("missing USD evidence was invented: %+v", row)
			}
		case "usd-refund":
			if *row.DisplayAmount == nil || **row.DisplayAmount != "-17.500000000000" {
				t.Fatalf("refund conversion failed: %+v", row)
			}
		}
	}
	body, err := json.Marshal(result)
	if err != nil || !strings.Contains(string(body), `"display_amount":null`) || !strings.Contains(string(body), `"display_estimated_margin":null`) {
		t.Fatalf("unknown conversion must serialize as null: %s, %v", body, err)
	}
}

func TestBillingStatementCurrencyRoundingAndAggregatePrecision(t *testing.T) {
	for _, test := range []struct{ input, want string }{
		{"0.0000000000005", "0.000000000000"}, {"0.0000000000015", "0.000000000002"},
		{"-0.0000000000005", "0.000000000000"}, {"-0.0000000000015", "-0.000000000002"},
		{"-0.0000000000016", "-0.000000000002"}, {"-0.0000000000014", "-0.000000000001"},
		{"999999999999999999.9999999999995", "1000000000000000000.000000000000"},
	} {
		t.Run(test.input, func(t *testing.T) {
			value, ok := new(big.Rat).SetString(test.input)
			if !ok || roundStatementCurrency(value) != test.want {
				t.Fatalf("rounding %s: got %s, want %s", test.input, roundStatementCurrency(value), test.want)
			}
		})
	}
	result := statementResult{
		Query: statementQuery{Side: "margin", DisplayCurrency: "CNY", DisplayExchangeRate: "0.5"},
		Rows: []statementRow{
			{Source: "tenant", Currency: "USD", Amount: statementString("0.000000000001"), USD: statementString("0.000000000001"), Status: "estimated"},
			{Source: "tenant", Currency: "USD", Amount: statementString("0.000000000001"), USD: statementString("0.000000000001"), Status: "estimated"},
			{Source: "provider_estimate", Currency: "USD", Amount: statementString("10"), USD: statementString("10"), Status: "estimated"},
		},
		Totals: map[string]string{},
	}
	if err := summarizeStatement(&result); err != nil {
		t.Fatal(err)
	}
	if err := convertStatementCurrency(&result); err != nil {
		t.Fatal(err)
	}
	if **result.Rows[0].DisplayAmount != "0.000000000000" || result.DisplayTotals["tenant"] != "0.000000000001" || **result.DisplayEstimatedMargin != "-4.999999999999" {
		t.Fatalf("rounding occurred before aggregation or lost negative margin: %+v", result)
	}
}

func TestBillingStatementReportingCurrencyAuthorizationAndTenantIsolation(t *testing.T) {
	store, app := newMethodRoutingBillingServer(t, "currency-permissions-password")
	statementFixture(t, store, "tenant-isolation", "project-a", time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC), "2")
	query := statementTestQuery("tenant")
	query.DisplayCurrency, query.DisplayExchangeRate = "CNY", "7"
	body, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"admin", "user", "team_leader", "security_admin"} {
		t.Run(role, func(t *testing.T) {
			token := createAdminOperationMethodRoutingSession(t, store, "currency-isolation-"+role, role)
			request := httptest.NewRequest(http.MethodPost, "/api/admin/billing/statements", strings.NewReader(string(body)))
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if role != "admin" {
				if response.Code != http.StatusForbidden {
					t.Fatalf("role %s: status %d", role, response.Code)
				}
				return
			}
			if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("administrator request failed: %d %s", response.Code, response.Body.String())
			}
			for _, secret := range []string{"supplier", "account", "wholesale", "provider_estimate"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("tenant response contains provider information %q", secret)
				}
			}
		})
	}
}

func TestBillingStatementReportingCurrencyUsesUSDBeforeNativeFallback(t *testing.T) {
	result := statementResult{
		Query: statementQuery{Side: "margin", DisplayCurrency: "CNY", DisplayExchangeRate: "7.25"},
		Rows: []statementRow{
			{Source: "tenant", Currency: "USD", Amount: statementString("999999999999999999.999999999999"), USD: statementString("999999999999999999.999999999999"), Status: "estimated"},
			{Source: "provider_estimate", Currency: "CNY", Amount: statementString("14"), USD: statementString("2"), Status: "estimated"},
		},
		Totals: map[string]string{},
	}
	if err := summarizeStatement(&result); err != nil {
		t.Fatal(err)
	}
	if err := convertStatementCurrency(&result); err != nil {
		t.Fatal(err)
	}
	if **result.Rows[0].DisplayAmount != "7249999999999999999.999999999993" || **result.Rows[1].DisplayAmount != "14.500000000000" || **result.DisplayEstimatedMargin != "7249999999999999985.499999999993" {
		t.Fatalf("reporting conversion lost exact USD basis: %+v", result)
	}
	result.Rows[1].USD = nil
	result.UnknownCount = 0
	result.EstimatedMarginUSD = nil
	if err := summarizeStatement(&result); err != nil {
		t.Fatal(err)
	}
	if err := convertStatementCurrency(&result); err != nil {
		t.Fatal(err)
	}
	if **result.Rows[1].DisplayAmount != "14.000000000000" || result.UnknownCount != 1 || *result.DisplayEstimatedMargin != nil {
		t.Fatalf("native fallback repaired unknown USD evidence or margin: %+v", result)
	}
}
