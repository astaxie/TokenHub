package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/metering"
)

func billingWorkflowFixture(t *testing.T) (*GormStore, Project, APIKey) {
	t.Helper()
	store, project, key, _ := setupUserQuotaTest(t, map[string]any{})
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := store.UpdateModel("user-quota-model", Model{InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 6, CacheReadPriceUSDPer1M: 0.5}); err != nil {
		t.Fatal(err)
	}
	return store, project, key
}
func billingAdminToken(t *testing.T, store *GormStore) string {
	t.Helper()
	user, err := store.CreateAdminUser(AdminUser{ID: "billing_admin", Username: "billing_admin", Email: "billing_admin@example.test", Role: "admin", Status: StatusActive}, "BillingTestPass123!")
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := store.CreateAdminSession(user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return token.Token
}
func TestBillingModelApplyConfirmationIdempotencyAndInFlightPrice(t *testing.T) {
	store, project, key := billingWorkflowFixture(t)
	token := billingAdminToken(t, store)
	app := New(store).Handler()
	draft, err := store.BillingModelPricing("user-quota-model")
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	draft.Card.Rates.Input = "3"
	quote, err := store.PreviewBillingModel(draft.Card, Usage{PromptTokens: 1000000}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"card": quote["proposed"], "fingerprint": quote["fingerprint"], "request_id": "confirmation-once", "confirmed": false}
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", body, token)
	if response.Code != 400 {
		t.Fatalf("unconfirmed apply: %d %s", response.Code, response.Body)
	}
	before, _ := store.BillingModelPricing("user-quota-model")
	if before.Card.Rates.Input != "2" {
		t.Fatal("unconfirmed request changed price")
	}
	body["confirmed"] = true
	body["risk_acknowledged"] = true
	for range 2 {
		response = doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", body, token)
		if response.Code != 200 {
			t.Fatalf("apply: %d %s", response.Code, response.Body)
		}
	}
	changes, err := store.BillingPriceChanges()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, change := range changes {
		if change.RequestID == "confirmation-once" {
			count++
			if change.Before.Rates.Input != "2" || change.After.Rates.Input != "3" || change.ActorID != "billing_admin" {
				t.Fatalf("change=%+v", change)
			}
		}
	}
	if count != 1 {
		t.Fatalf("change count=%d", count)
	}
	var audits int64
	if err := store.db.Model(&AuditEvent{}).Where("action = ?", "apply_price").Count(&audits).Error; err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("duplicate audit events: %d", audits)
	}
	next, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	oldUsage := priceUsageAt(call.Model, Usage{PromptTokens: 1000000}, call.StartedAt)
	newUsage := priceUsageAt(next.Model, Usage{PromptTokens: 1000000}, next.StartedAt)
	if oldUsage.CostUSD != 2 || newUsage.CostUSD != 3 {
		t.Fatalf("old=%v new=%v", oldUsage.CostUSD, newUsage.CostUSD)
	}
	if quote["charge"].(metering.Charge).Amount != billingAmount(newUsage.CostUSD) {
		t.Fatal("preview and live pricing disagree")
	}
}
func TestBillingPricingFingerprintIncludesInheritedMetadata(t *testing.T) {
	for _, input := range []float64{0, 2} {
		t.Run(fmt.Sprint(input), func(t *testing.T) {
			store, _, _ := billingWorkflowFixture(t)
			_, err := store.UpdateModel("user-quota-model", Model{InputPriceUSDPer1M: input, CacheReadPriceUSDPer1M: 1, OutputPriceUSDPer1M: 6, Metadata: map[string]string{cacheReadEstimateRatioKey: "0.1"}})
			if err != nil {
				t.Fatal(err)
			}
			draft, _ := store.BillingModelPricing("user-quota-model")
			draft.Card.Rates.Input = "4"
			draft.Card.Rates.CacheRead = ""
			if _, err := store.PreviewBillingModel(draft.Card, Usage{PromptTokens: 1000, CachedInputTokens: 1000}, time.Now()); err != nil {
				t.Fatal(err)
			}
			var patch modelPatchRequest
			if err := json.Unmarshal([]byte(`{"metadata":{"cache_read_estimate_ratio":"0.5"}}`), &patch); err != nil {
				t.Fatal(err)
			}
			if _, err := store.UpdateModel("user-quota-model", patch.Model); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ApplyBillingModel(draft.Card, draft.Fingerprint, "stale-ratio", AdminUser{ID: "admin"}); err == nil || AsHTTPError(err).Code != "model_price_changed" {
				t.Fatalf("stale metadata accepted: %v", err)
			}
		})
	}
}
func TestBillingModelPreviewPreservesDefaultsPeriodsAndExplicitFree(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	_, err := store.UpdateModel("user-quota-model", Model{InputPriceUSDPer1M: 2, OutputPriceUSDPer1M: 6})
	if err != nil {
		t.Fatal(err)
	}
	draft, _ := store.BillingModelPricing("user-quota-model")
	draft.Card.Periods = []meteringPeriod{{ModelPricingPeriod: ModelPricingPeriod{Name: "all day", Timezone: "UTC"}, Rates: metering.Rates{Input: "4"}}}
	usage := Usage{PromptTokens: 1000000, CachedInputTokens: 500000, CostUSD: 999}
	quote, err := store.PreviewBillingModel(draft.Card, usage, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if quote["charge"].(metering.Charge).Amount != "2.200000000000" {
		t.Fatalf("preview defaults: %+v", quote["charge"])
	}
	draft.Card.Rates.CacheRead = "0"
	quote, err = store.PreviewBillingModel(draft.Card, usage, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if quote["charge"].(metering.Charge).Amount != "2.000000000000" {
		t.Fatalf("free cache: %+v", quote["charge"])
	}
}
func TestBillingStatementsIncludeRetryCostsAndPostedFailedCharges(t *testing.T) {
	store, project, key := billingWorkflowFixture(t)
	pm := ProviderModel{ID: "pm_cost", ProviderID: "p_cost", UpstreamModel: "upstream", InputPriceUSDPer1M: 3, OutputPriceUSDPer1M: 6}
	if err := store.db.Create(&pm).Error; err != nil {
		t.Fatal(err)
	}
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an existing admission lacking the newly added model name.
	var admission meteringEntry
	if err := store.db.First(&admission, "id = ?", call.RequestID+":admission").Error; err != nil {
		t.Fatal(err)
	}
	var old map[string]any
	if err := json.Unmarshal([]byte(admission.Payload), &old); err != nil {
		t.Fatal(err)
	}
	delete(old, "model")
	payload, _ := json.Marshal(old)
	if err := store.db.Model(&admission).Update("payload", string(payload)).Error; err != nil {
		t.Fatal(err)
	}
	var winner RouteSelection
	for i, tokens := range []int64{100000, 1000000} {
		route, err := store.PrepareMeteringAttempt(call.RequestID, i+1, RouteSelection{Provider: Provider{ID: "p_cost", Name: "=SUM(1)"}, ProviderModel: "upstream"}, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		attemptUsage := priceUsage(call.Model, Usage{PromptTokens: tokens, UpstreamRequestID: fmt.Sprintf("upstream-%d", i)})
		call.RouteAttempts = append(call.RouteAttempts, RouteAttempt{Selection: route, Invoked: true, Status: 500, Usage: attemptUsage, StartedAt: time.Now().UTC(), EndedAt: time.Now().UTC()})
		winner = route
	}
	store.FinishCall(call, winner, Usage{PromptTokens: 1000000}, 500, "stream_failed", "", "")
	from, to := call.StartedAt.Add(-time.Second), time.Now().Add(time.Hour)
	q := billingStatementQuery{Kind: "provider", From: from, To: to, GroupBy: "provider", Limit: 100}
	report, err := store.PlatformBillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if report.Records != 2 || report.Pending != 0 || report.KnownAmountUSD != "3.300000000000" {
		t.Fatalf("provider report: %+v", report)
	}
	q.Kind = "tenant"
	q.GroupBy = "model"
	q.Model = "user-quota-model"
	tenant, err := store.PlatformBillingStatement(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if tenant.Records != 1 || tenant.Pending != 1 || tenant.Complete || !tenant.Items[0].EvidenceIncomplete || tenant.KnownAmountUSD != "2.000000000000" {
		t.Fatalf("posted failure hidden: %+v", tenant)
	}
	reconciled, err := store.ListProviderReconciliationUsages(from, to, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled) != 2 || !reconciled[0].ProviderCostKnown {
		t.Fatalf("retry costs missing from reconciliation: %+v", reconciled)
	}
	token := billingAdminToken(t, store)
	app := New(store).Handler()
	query := url.Values{"kind": {"provider"}, "from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}, "format": {"csv"}}
	response := doJSON(t, app, "GET", "/api/admin/billing/statements?"+query.Encode(), nil, token)
	if response.Code != 200 {
		t.Fatalf("CSV: %d %s", response.Code, response.Body)
	}
	rows, err := csv.NewReader(strings.NewReader(response.Body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[1][5] != "'=SUM(1)" {
		t.Fatalf("CSV scope/escaping: %+v", rows)
	}
	query.Set("kind", "tenant")
	response = doJSON(t, app, "GET", "/api/admin/billing/statements?"+query.Encode(), nil, token)
	rows, err = csv.NewReader(strings.NewReader(response.Body)).ReadAll()
	if err != nil || response.Code != 200 || len(rows) != 2 || rows[1][13] != "charged" || rows[1][17] != "2" || rows[1][19] != "true" {
		t.Fatalf("CSV lost posted charge or incomplete evidence: %v %+v", err, rows)
	}
}
func TestBillingStatementsUnknownCostAndLegacyNamespace(t *testing.T) {
	store, project, key := billingWorkflowFixture(t)
	call, err := store.StartCall(context.Background(), project, key, "user-quota-model", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareMeteringAttempt(call.RequestID, 1, RouteSelection{Provider: Provider{ID: "p"}, ProviderModel: "upstream"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	usage := UsageRecord{ID: "legacy", RequestID: "legacy-request", ModelName: "public-alias", ProviderID: "p", CostUSD: 99, ProviderCostUSD: 4, CreatedAt: now}
	if err := store.db.Create(&usage).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Create(&RequestLog{ID: "log_legacy", RequestID: usage.RequestID, ProviderModel: "upstream", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	report, err := store.PlatformBillingStatement(context.Background(), billingStatementQuery{Kind: "provider", From: call.StartedAt.Add(-time.Second), To: now.Add(time.Hour), GroupBy: "model", Model: "upstream", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if report.Records != 2 || report.Pending != 2 || report.Legacy != 1 || report.Complete || report.KnownAmountUSD != "4.000000000000" {
		t.Fatalf("unknown/legacy amounts: %+v", report)
	}
}
func TestBillingStatementsKeysetPaginationDoesNotLoseRows(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	at := time.Now().UTC().Add(-time.Minute)
	entries := []meteringEntry{}
	for i := 0; i < 251; i++ {
		id := fmt.Sprintf("batch-%04d", 250-i)
		admission, _ := encodeMetering(meteringRequestSnapshot{RequestID: id, ModelName: "batch"})
		settlement, _ := encodeMetering(billingSettlement{Tenant: meteringShadowCharge{Status: "estimated", LegacyUSD: "0.01"}})
		entries = append(entries, meteringEntry{ID: id + ":admission", Kind: "admission", Scope: id, Payload: admission, CreatedAt: at.Add(time.Duration(i) * time.Millisecond)}, meteringEntry{ID: id + ":shadow", Kind: "shadow_settlement", Scope: id, Payload: settlement, CreatedAt: at})
	}
	if err := store.db.CreateInBatches(entries, 100).Error; err != nil {
		t.Fatal(err)
	}
	result, err := store.PlatformBillingStatement(context.Background(), billingStatementQuery{Kind: "tenant", From: at.Add(-time.Second), To: at.Add(time.Hour), GroupBy: "model", Offset: 250, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result.Records != 251 || len(result.Items) != 1 || result.KnownAmountUSD != "2.510000000000" {
		t.Fatalf("pagination: %+v", result)
	}
}
func TestBillingWorkflowEndpointsRejectOrdinaryUsers(t *testing.T) {
	store, _, _ := billingWorkflowFixture(t)
	_, session, err := store.CreateAdminSession("usr_user_quota", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	app := New(store).Handler()
	for _, path := range []string{"/api/admin/billing/models/user-quota-model/pricing", "/api/admin/billing/price-changes", "/api/admin/billing/statements"} {
		response := doJSON(t, app, "GET", path, nil, session.Token)
		if response.Code != 403 {
			t.Fatalf("%s: %d", path, response.Code)
		}
	}
	response := doJSON(t, app, "POST", "/api/admin/billing/model-pricing/apply", map[string]any{"confirmed": true}, session.Token)
	if response.Code != 403 {
		t.Fatalf("apply authorization: %d", response.Code)
	}
}
