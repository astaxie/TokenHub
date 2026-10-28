package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProviderMonitoringQuotaSummaryPreservesUnknownAndKnownPercentages(t *testing.T) {
	const balanceOnly = `{"plan_type":"balance-only","metrics":[{"name":"Credits","value":23.5,"unit":"USD"}],"windows":[]}`
	const unknownWindows = `{"plan_type":"unknown","windows":[{"label":"Unmeasured","reset_at":1999999999}],"rate_limit":{"primary_window":{}}}`
	const knownHigh = `{"plan_type":"high-allowance","remaining_percent":85}`
	const knownLow = `{"plan_type":"low-allowance","remaining_percent":55}`
	const exhausted = `{"plan_type":"exhausted","remaining_percent":0,"limit_reached":true}`
	for _, test := range []struct {
		name         string
		quotas       []string
		known        bool
		remaining    float64
		limitReached bool
		plan         string
	}{
		{name: "Balance without an allowance", quotas: []string{balanceOnly}, plan: "balance-only"},
		{name: "Windows without measured usage", quotas: []string{unknownWindows}, plan: "unknown"},
		{name: "Explicit exhausted percentage", quotas: []string{exhausted}, known: true, remaining: 0, limitReached: true, plan: "exhausted"},
		{name: "Exhaustion without measured usage", quotas: []string{`{"rate_limit":{"allowed":false}}`}, known: true, remaining: 0, limitReached: true},
		{name: "Zero used window is a full allowance", quotas: []string{`{"windows":[{"used_percent":0}]}`}, known: true, remaining: 100},
		{name: "Zero used account is a full allowance", quotas: []string{`{"used_percent":0}`}, known: true, remaining: 100},
		{name: "Unknown accounts before known accounts", quotas: []string{balanceOnly, unknownWindows, knownHigh, knownLow}, known: true, remaining: 55, plan: "low-allowance"},
		{name: "Unknown accounts after known accounts", quotas: []string{knownLow, knownHigh, unknownWindows, balanceOnly}, known: true, remaining: 55, plan: "low-allowance"},
		{name: "Unknown account before exhausted account", quotas: []string{balanceOnly, exhausted}, known: true, remaining: 0, limitReached: true, plan: "exhausted"},
		{name: "Unknown account after exhausted account", quotas: []string{exhausted, balanceOnly}, known: true, remaining: 0, limitReached: true, plan: "exhausted"},
		{name: "Missing cache preserves successful account counts", quotas: []string{balanceOnly, knownLow, ""}, known: true, remaining: 55, plan: "low-allowance"},
		{
			name:   "Nested secondary window sets the minimum allowance",
			quotas: []string{`{"primary_window":{"used_percent":5},"secondary_window":{"used_percent":15},"rate_limit":{"primary_window":{"used_percent":30},"secondary_window":{"used_percent":80}},"windows":[{"label":"Unmeasured"},{"used_percent":60}]}`},
			known:  true, remaining: 20,
		},
		{
			name:   "Top-level secondary window sets the minimum allowance",
			quotas: []string{`{"primary_window":{"used_percent":5},"secondary_window":{"used_percent":90},"rate_limit":{"primary_window":{"used_percent":30},"secondary_window":{"used_percent":80}}}`},
			known:  true, remaining: 10,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			quota := monitoringQuotaSummaryFromCachedAccounts(t, test.quotas)
			encoded, err := json.Marshal(quota)
			if err != nil {
				t.Fatal(err)
			}
			raw, present := quota["remaining_percent"]
			if present != test.known {
				t.Fatalf("remaining_percent presence = %v, want %v; summary = %s", present, test.known, encoded)
			}
			if test.known {
				var remaining float64
				if string(raw) == "null" || json.Unmarshal(raw, &remaining) != nil || remaining != test.remaining {
					t.Fatalf("remaining_percent = %s, want explicit JSON number %g", raw, test.remaining)
				}
			}
			var summary struct {
				SuccessfulAccounts int    `json:"successful_accounts"`
				FailedAccounts     int    `json:"failed_accounts"`
				LimitReached       bool   `json:"limit_reached"`
				PlanType           string `json:"plan_type"`
				Accounts           []struct {
					Quota     json.RawMessage `json:"quota"`
					ErrorCode string          `json:"error_code"`
				} `json:"accounts"`
			}
			if err := json.Unmarshal(encoded, &summary); err != nil {
				t.Fatal(err)
			}
			successful := 0
			for _, cached := range test.quotas {
				if cached != "" {
					successful++
				}
			}
			if summary.SuccessfulAccounts != successful || summary.FailedAccounts != len(test.quotas)-successful || len(summary.Accounts) != len(test.quotas) {
				t.Fatalf("cached account counts changed: %+v", summary)
			}
			if summary.LimitReached != test.limitReached || summary.PlanType != test.plan {
				t.Fatalf("quota status or selected account plan changed: %+v", summary)
			}
		})
	}
}

func monitoringQuotaSummaryFromCachedAccounts(t *testing.T, quotas []string) map[string]json.RawMessage {
	t.Helper()
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	provider := store.AddProvider(Provider{ID: "prv_quota_summary", Name: "Quota Summary", Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true})
	for index, quota := range quotas {
		resource, err := store.AddProviderResource(ProviderResource{
			ID: fmt.Sprintf("rsrc_quota_summary_%d", index), ProviderID: provider.ID,
			Name: fmt.Sprintf("Account %02d", index), Priority: index,
			ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
			Credentials: &ProviderResourceCredentials{AccessToken: "synthetic-access", AccountID: fmt.Sprintf("synthetic-account-%d", index)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if quota != "" {
			if err := store.SaveProviderResourceQuota(resource.ID, provider.Type, quota, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
		}
	}
	server := New(store)
	upstreamCalls := 0
	mustCodexSubscriptionAdapterForTest(t, server).Client = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		upstreamCalls++
		return nil, fmt.Errorf("monitoring must only read cached quota snapshots")
	})}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/providers/monitoring", nil)
	request.Header.Set("Authorization", "Bearer dev_admin_token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("monitoring request failed: %d %s", response.Code, response.Body.String())
	}
	if upstreamCalls != 0 {
		t.Fatalf("monitoring fetched quota upstream %d times", upstreamCalls)
	}
	var payload struct {
		Data []struct {
			Quota map[string]json.RawMessage `json:"quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data) != 1 {
		t.Fatalf("monitoring returned %d providers, want one", len(payload.Data))
	}
	return payload.Data[0].Quota
}
