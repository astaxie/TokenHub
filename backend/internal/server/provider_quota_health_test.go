package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"
	"tokenhub/backend/internal/allowance"
)

type exhaustedProviderProbe struct{}

func (exhaustedProviderProbe) ProbeProvider(context.Context, Provider) (any, error) {
	return nil, newProviderHTTPError(http.StatusPaymentRequired, nil, []byte(`{"error":{"code":"insufficient_quota"}}`))
}

func TestCachedAllowanceOnlyMarksWholeProviderExhaustedWhenAllAccountsAreKnown(t *testing.T) {
	for _, tc := range []struct {
		name, second string
		age          time.Duration
		want         bool
	}{
		{"sole exhausted account", "none", 0, true},
		{"all exhausted", "exhausted", 0, true},
		{"working alternative", "available", 0, false},
		{"unknown alternative", "unknown", 0, false},
		{"stale exhaustion", "none", -11 * time.Minute, false},
		{"model scoped exhaustion", "model", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			t.Cleanup(func() { _ = store.Close() })
			p := store.AddProvider(Provider{ID: "cached-quota", Name: "Synthetic API provider", Type: ProviderAnthropic, Status: StatusActive, Healthy: true})
			s := New(store)
			t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
			for i, state := range []string{"exhausted", tc.second} {
				if state == "none" {
					continue
				}
				id := []string{"cached-a", "cached-b"}[i]
				r, err := store.AddProviderResource(ProviderResource{ID: id, ProviderID: p.ID, Name: id, ResourceType: ProviderResourceAPIKey, Status: StatusActive, Healthy: true})
				if err != nil {
					t.Fatal(err)
				}
				if state == "unknown" {
					continue
				}
				now := time.Now().UTC().Add(tc.age)
				used := 100.0
				if state == "available" {
					used = 20
				}
				window := allowance.Window{Name: "Week", UsedPercent: &used}
				if tc.second == "model" {
					window.Models = []string{"claude-*"}
				}
				a := allowance.Snapshot{Version: allowance.Version, FetchedAt: now, Windows: []allowance.Window{window}}
				raw, _ := json.Marshal(map[string]any{"allowance": a})
				if err := store.SaveProviderResourceQuota(r.ID, p.Type, string(raw), now); err != nil {
					t.Fatal(err)
				}
			}
			snapshots, err := s.providerMonitoringSnapshots(t.Context())
			if err != nil || len(snapshots) != 1 || (snapshots[0].State == "exhausted") != tc.want || !snapshots[0].Quota.Supported {
				t.Fatalf("wrong cached quota state: %+v %v", snapshots, err)
			}
		})
	}
}

func TestQuotaExhaustionDoesNotBlameResource(t *testing.T) {
	err := newProviderHTTPError(http.StatusPaymentRequired, nil, nil)
	if providerAttemptOutcome(err) != AttemptNeutral || !shouldFailoverRoutedError(err, true) {
		t.Fatal("quota exhaustion must fail over without adding a resource fault")
	}
}

func TestQuotaExhaustedProbeKeepsProviderHealthyAndReportsQuotaState(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	p := store.AddProvider(Provider{ID: "quota-health", Name: "Synthetic exhausted provider", Type: "quota_health_probe", Status: StatusActive, Healthy: true})
	s := New(store)
	t.Cleanup(func() { _ = s.Shutdown(t.Context()) })
	s.adapterRegistry.Register(p.Type, exhaustedProviderProbe{}, AdapterCapabilityProbe)
	s.checkProviderMonitoring(t.Context(), p)
	current, _ := store.GetProvider(p.ID)
	if !current.Healthy {
		t.Error("quota denial marked the provider unhealthy")
	}
	snapshots, err := s.providerMonitoringSnapshots(t.Context())
	if err != nil || len(snapshots) != 1 || snapshots[0].State != "exhausted" || snapshots[0].ActiveProbe.State != "exhausted" {
		t.Fatalf("quota denial reported as a fault: %+v %v", snapshots, err)
	}
}

func TestQuotaExhaustedGatewaySignalPreservesFailedRequestCount(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	store.RecordProviderObservation(ProviderObservation{ProviderID: "quota", Source: "gateway_request", ErrorCode: "provider_payment_required", ObservedAt: now})
	got, err := store.ProviderMonitoringObservations(t.Context(), now, []string{"quota"})
	if err != nil || got["quota"].Gateway.State != "exhausted" || got["quota"].Gateway.SuccessRate != 0 || got["quota"].Gateway.Samples != 1 {
		t.Fatalf("quota was hidden as success or reported as a fault: %+v %v", got, err)
	}
}

func TestUsageCapHoldDoesNotBecomeAServiceFault(t *testing.T) {
	store := NewMemoryStore()
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	store.RecordProviderObservation(ProviderObservation{ProviderID: "cap", Source: "gateway_request", ErrorCode: "provider_usage_cap_reached", ObservedAt: now})
	summary, err := store.ProviderMonitoringObservations(t.Context(), now, []string{"cap"})
	if err != nil || summary["cap"].Gateway.State != "exhausted" || summary["cap"].Gateway.SuccessRate != 0 {
		t.Fatalf("usage cap hold became a service fault: %+v %v", summary, err)
	}
}

func TestQuotaStateDoesNotHideNewerAuthenticationOrNetworkFailures(t *testing.T) {
	now := time.Now().UTC()
	zero := 0.0
	earlier := now.Add(-time.Minute)
	for _, code := range []string{"provider_auth_error", "provider_connection_failed"} {
		configuration := ProviderMonitoringSignal{State: "healthy"}
		probe := ProviderMonitoringSignal{State: "exhausted", Source: "active_probe", Samples: 1, ErrorCode: "provider_payment_required", ObservedAt: &earlier}
		gateway := ProviderMonitoringSignal{State: "down", Source: "gateway_request", Samples: 1, ErrorCode: code, ObservedAt: &now}
		state, _ := aggregateProviderMonitoringState(configuration, ProviderMonitoringSignal{}, probe, gateway)
		if state != "down" {
			t.Fatalf("newer %s failure hidden by older quota denial: %s", code, state)
		}
		quota := ProviderAccountQuotaSnapshot{"allowance": allowance.Snapshot{Version: allowance.Version, FetchedAt: earlier, Windows: []allowance.Window{{Balance: &zero}}}}
		snapshot := ProviderMonitoringSnapshot{Provider: Provider{Status: StatusActive}, State: "down", ActiveResourceCount: 1, Gateway: gateway, Quota: ProviderQuotaSummary{Accounts: []ProviderQuotaAccountSummary{{Quota: quota}}}}
		applyProviderQuotaAvailability(&snapshot, now)
		if snapshot.State != "down" {
			t.Fatalf("cached quota hid newer %s failure", code)
		}
	}
}

func TestStructuredQuotaErrorsKeepAuthenticationAndServerFailuresDistinct(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   ProviderErrorDisposition
	}{
		{"OpenAI quota", 429, `{"error":{"code":"insufficient_quota"}}`, ProviderErrorQuotaExhausted},
		{"subscription quota", 403, `{"error":{"type":"usage_limit_reached"}}`, ProviderErrorQuotaExhausted},
		{"balance", 403, `{"code":"insufficient_balance"}`, ProviderErrorQuotaExhausted},
		{"ordinary rate limit", 429, `{"error":{"type":"rate_limit_error"}}`, ProviderErrorTransientSame},
		{"real auth error", 403, `{"error":{"code":"invalid_api_key"}}`, ProviderErrorAuthBroken},
		{"quota mentioned in a message", 403, `{"error":{"message":"insufficient_quota"}}`, ProviderErrorAuthBroken},
		{"server error", 503, `{"error":{"code":"insufficient_quota"}}`, ProviderErrorTransientSame},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newProviderHTTPError(tc.status, nil, []byte(tc.body))
			if providerErrorDisposition(err) != tc.want {
				t.Fatalf("wrong classification: %v (%s)", err, providerErrorDisposition(err))
			}
		})
	}
}
