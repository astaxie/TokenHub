package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
	"tokenhub/backend/internal/quotasource"
)

func TestConfiguredAllowanceUsesCurrentAccountDestination(t *testing.T) {
	for _, preset := range []string{"newapi-key", "sub2api", "sub2api-account"} {
		for _, independent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/independent_token_%v", preset, independent), func(t *testing.T) {
				server, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
				var oldCalls, newCalls atomic.Int64
				old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					oldCalls.Add(1)
					_, _ = w.Write([]byte(`{"remaining":10,"data":{"balance":10,"quota":10,"total_available":5000000}}`))
				}))
				defer old.Close()
				current := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					newCalls.Add(1)
					if r.Header.Get("Authorization") != "Bearer synthetic-current-secret" {
						t.Error("current destination did not receive current credentials")
					}
					_, _ = w.Write([]byte(`{"remaining":10,"data":{"balance":10,"quota":10,"total_available":5000000}}`))
				}))
				defer current.Close()
				stale, err := store.UpdateProviderResource("a", ProviderResource{BaseURL: old.URL, APIKey: "synthetic-previous-secret"})
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.UpdateProviderResource("a", ProviderResource{BaseURL: current.URL, APIKey: "synthetic-current-secret"})
				if err != nil {
					t.Fatal(err)
				}
				cfg := providerAllowanceConfig{Source: quotasource.Config{Enabled: true, Preset: preset}}
				if independent {
					cfg.Token = "synthetic-current-secret"
				}
				if _, err := store.saveProviderAllowanceConfig(t.Context(), "a", cfg); err != nil {
					t.Fatal(err)
				}
				if _, _, err := server.refreshConfiguredAllowance(t.Context(), stale, true); err != nil {
					t.Fatal(err)
				}
				if oldCalls.Load() != 0 || newCalls.Load() != 1 {
					t.Fatalf("destination mismatch: previous=%d current=%d", oldCalls.Load(), newCalls.Load())
				}
			})
		}
	}
}

func TestQuotaFailureIgnoresUnrelatedObservationWrites(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
	resource, _ := store.GetProviderResource("a")
	fetched := time.Now().UTC().Add(-time.Minute)
	if err := store.SaveProviderResourceQuota("a", ProviderMock, `{"used_percent":20}`, fetched); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	if err := store.db.Model(&ProviderResourceObservation{}).Where("resource_id = ?", "a").Updates(map[string]any{"served_model": "synthetic-served-model", "updated_at": started.Add(time.Millisecond)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.saveProviderQuotaFailure(t.Context(), resource, "synthetic_quota_failure", started); err != nil {
		t.Fatal(err)
	}
	observation, _ := store.GetProviderResourceObservation("a")
	got := allowance.Read([]byte(observation.QuotaSnapshot), fetched)
	if got.Status != allowance.QueryError || !got.CheckedAt.After(started) || !got.FetchedAt.Equal(fetched) {
		t.Fatalf("failure not recorded with original quota age: %+v", got)
	}
	if observation.ServedModel != "synthetic-served-model" {
		t.Fatal("quota failure erased inference metadata")
	}
}

func TestLateQuotaFailureCannotOverwriteNewerQuotaCheck(t *testing.T) {
	for _, newerFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("newer_failure_%v", newerFailure), func(t *testing.T) {
			_, store, _, _ := accountRoutingFixture(t, accountrouting.Default)
			resource, _ := store.GetProviderResource("a")
			fetched := time.Now().UTC().Add(-time.Minute)
			if err := store.SaveProviderResourceQuota("a", ProviderMock, `{"used_percent":20}`, fetched); err != nil {
				t.Fatal(err)
			}
			started := time.Now().UTC().Add(-time.Second)
			if newerFailure {
				if err := store.saveProviderQuotaFailure(t.Context(), resource, "newer_failure", started.Add(time.Millisecond)); err != nil {
					t.Fatal(err)
				}
			} else if err := store.SaveProviderResourceQuota("a", ProviderMock, `{"used_percent":30}`, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			before, _ := store.GetProviderResourceObservation("a")
			if err := store.saveProviderQuotaFailure(t.Context(), resource, "older_failure", started); err != nil {
				t.Fatal(err)
			}
			after, _ := store.GetProviderResourceObservation("a")
			if after.QuotaSnapshot != before.QuotaSnapshot {
				t.Fatal("late failure overwrote a newer quota check")
			}
		})
	}
}

func TestAutomaticAllowanceExpiryResetHonorsScopedCaps(t *testing.T) {
	for _, excluded := range []bool{false, true} {
		for _, capKind := range []string{"global", "window", "none"} {
			t.Run(fmt.Sprintf("excluded_%v/%s", excluded, capKind), func(t *testing.T) {
				upstream := &quotaResetUpstream{availableCount: 1, creditID: "synthetic-expiring-card", creditExpiresAt: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)}
				server, store, _ := newQuotaResetTestServer(t, upstream, quotaResetTestCredentials())
				t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
				collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(`{"used":80}`))
				}))
				defer collector.Close()
				policy := allowance.Policy{Reset: allowance.ResetPolicy{BeforeExpiry: true}}
				switch capKind {
				case "global":
					policy.Cap = allowance.Cap{Percent: 70}
				case "window":
					policy.Windows = map[string]allowance.Cap{"scoped": {Percent: 70}}
				}
				window := quotasource.WindowRule{Name: "Scoped", Used: "used", PeriodSeconds: 604800, Models: []string{"synthetic-model"}}
				if excluded {
					window.Models = nil
					window.ExcludedModels = []string{"other-model"}
				}
				cfg, err := store.saveProviderAllowanceConfig(t.Context(), quotaResetTestResourceID, providerAllowanceConfig{Source: quotasource.Config{Enabled: true, URL: collector.URL, Windows: []quotasource.WindowRule{window}}, Policy: policy})
				if err != nil {
					t.Fatal(err)
				}
				resource, _ := store.GetProviderResource(quotaResetTestResourceID)
				result, _, err := server.refreshConfiguredAllowance(t.Context(), resource, true)
				if err != nil {
					t.Fatal(err)
				}
				decision := allowance.Evaluate(result.Allowance, policy, "synthetic-model", time.Now().UTC(), accountAllowanceMaxAge)
				if decision.Held != (capKind != "none") {
					t.Fatal("routing cap fixture does not match reset policy")
				}
				server.refreshAllowanceEntitlements(t.Context(), resource, cfg)
				want := 0
				if capKind == "none" {
					want = 1
				}
				if upstream.consumeCalls != want {
					t.Fatalf("reset consumption=%d want=%d", upstream.consumeCalls, want)
				}
			})
		}
	}
}
