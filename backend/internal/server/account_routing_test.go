package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"tokenhub/backend/internal/accountrouting"
	"tokenhub/backend/internal/allowance"
)

func accountRoutingFixture(t *testing.T, strategy accountrouting.Strategy) (*Server, *GormStore, CallContext, []RouteSelection) {
	t.Helper()
	store := NewMemoryStore()
	server := New(store)
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	model := store.AddModel(Model{Name: "account-routing-model", Modality: "chat", Status: StatusActive, Metadata: map[string]string{accountRoutingMetadataKey: string(strategy)}})
	provider := store.AddProvider(Provider{ID: "account-routing-provider", Name: "Provider", Type: ProviderMock, Healthy: true, Status: StatusActive})
	for _, id := range []string{"a", "b", "c"} {
		if _, err := store.AddProviderResource(ProviderResource{ID: id, ProviderID: provider.ID, Name: id, Healthy: true, Status: StatusActive, Priority: 1, Weight: 100}); err != nil {
			t.Fatal(err)
		}
	}
	store.AddRoute(ModelRoute{ID: "account-route", ModelName: model.Name, ProviderID: provider.ID, ProviderModel: "claude-sonnet", Priority: 1, Weight: 100, QualityScore: 50, CostScore: 50, Strategy: RouteStrategyPriorityWeighted, Status: StatusActive})
	routes, err := store.SelectRouteCandidates(model.Name)
	if err != nil {
		t.Fatal(err)
	}
	return server, store, CallContext{RequestID: "account-request", Project: Project{ID: "account-project"}, Model: model}, routes
}

func saveAccountQuota(t *testing.T, store *GormStore, id, raw string) {
	t.Helper()
	if err := store.SaveProviderResourceQuota(id, ProviderMock, raw, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestAccountRoutingReadsCacheAndKeepsAuthorizedRouteSlots(t *testing.T) {
	server, store, call, routes := accountRoutingFixture(t, accountrouting.Smart)
	saveAccountQuota(t, store, "a", `{"windows":[{"used":50,"span":604800,"reset_after_seconds":604800}]}`)
	saveAccountQuota(t, store, "b", `{"windows":[{"used":70,"span":604800,"reset_after_seconds":86400},{"used":100,"models":["opus"]}]}`)
	saveAccountQuota(t, store, "c", `{"windows":[{"used":100}]}`)
	// An excluded account with a better quota can never return to the pool.
	planned := server.planAccountOrder(context.Background(), call, routes[:2])
	if len(planned) != 2 || planned[0].Resource.ID != "b" {
		t.Fatalf("cached quota not used: %+v", planned)
	}
	for _, route := range planned {
		if route.Resource.ID == "c" {
			t.Fatal("excluded account reintroduced")
		}
	}
	// Another route's slots and priority tier must remain exactly where the
	// model planner put them, even if its account would otherwise rank first.
	other := routes[2]
	other.Route.ID = "other-route"
	other.Route.Priority = 2
	mixed := []RouteSelection{routes[0], other, routes[1]}
	ordered := server.planAccountOrder(context.Background(), call, mixed)
	if ordered[1].Route.ID != "other-route" || ordered[1].Resource.ID != other.Resource.ID {
		t.Fatal("account ordering changed the model route")
	}
	if got := server.planAccountOrder(context.Background(), call, []RouteSelection{routes[0]}); got[0].Resource.ID != "a" {
		t.Fatal("single eligible account was replaced")
	}
	if rows := store.ListResources(accountRoutingStateKind); len(rows) != 0 {
		t.Fatal("planning wrote drain state")
	}
}

func TestAccountDrainPersistsAndIgnoresLateAttempts(t *testing.T) {
	server, store, call, routes := accountRoutingFixture(t, accountrouting.Drain)
	plan := server.planAccountOrder(context.Background(), call, routes)
	if plan[0].Resource.ID != "a" {
		t.Fatal("initial drain order is unstable")
	}
	recordAccountRoutingOutcome(context.Background(), store, plan[0], false)
	// A temporary failure and a successful fallback must not permanently move
	// the current account. Only an explicit quota-exhaustion outcome does.
	recordAccountRoutingOutcome(context.Background(), store, plan[1], false)
	if got := server.planAccountOrder(context.Background(), call, routes); got[0].Resource.ID != "a" {
		t.Fatal("temporary failover advanced drain")
	}
	recordAccountRoutingOutcome(context.Background(), store, plan[0], true)
	restarted := New(store)
	t.Cleanup(func() { _ = restarted.Shutdown(context.Background()) })
	next := restarted.planAccountOrder(context.Background(), call, routes)
	if next[0].Resource.ID != "b" {
		t.Fatalf("drain state not shared: %+v", next)
	}
	recordAccountRoutingOutcome(context.Background(), store, next[0], false)
	recordAccountRoutingOutcome(context.Background(), store, plan[0], false)
	recordAccountRoutingOutcome(context.Background(), store, plan[0], true)
	if got := server.planAccountOrder(context.Background(), call, routes); got[0].Resource.ID != "b" {
		t.Fatal("late attempt rewound newer state")
	}
	// Removing the selected account changes only this pool; no saved ID can
	// add an account that the caller is no longer permitted to use.
	for _, route := range server.planAccountOrder(context.Background(), call, []RouteSelection{routes[0]}) {
		if route.Resource.ID != "a" {
			t.Fatal("drain bypassed eligibility")
		}
	}
}

func TestAccountDrainGatewayAdvancesOnlyOnQuotaExhaustion(t *testing.T) {
	for _, disposition := range []ProviderErrorDisposition{ProviderErrorQuotaExhausted, ProviderErrorTransientSame} {
		t.Run(string(disposition), func(t *testing.T) {
			server, store, call, routes := accountRoutingFixture(t, accountrouting.Drain)
			planned := server.planAccountOrder(context.Background(), call, routes)
			_, selected, _, attempts, err := executeRoutedWithStore(context.Background(), store, RoutedCall{Call: call, Routes: planned}, false, func(_ context.Context, route RouteSelection, _ bool, _ int) (bool, Usage, error) {
				if route.Resource.ID == "a" {
					return false, Usage{}, &ProviderInvocationError{Err: NewHTTPError(http.StatusBadGateway, "provider_unavailable", "Synthetic upstream failure"), Disposition: disposition}
				}
				return true, Usage{TotalTokens: 1}, nil
			})
			if err != nil || selected.Resource == nil || selected.Resource.ID != "b" || len(attempts) != 2 {
				t.Fatalf("gateway did not fail over: selected=%+v attempts=%d error=%v", selected.Resource, len(attempts), err)
			}
			want := "a"
			if disposition == ProviderErrorQuotaExhausted {
				want = "b"
			}
			if got := server.planAccountOrder(context.Background(), call, routes)[0].Resource.ID; got != want {
				t.Fatalf("next request selected %s, want %s", got, want)
			}
		})
	}
}

func TestQuotaFailureKeepsLastReadingAndRejectsLateFailures(t *testing.T) {
	_, store, _, _ := accountRoutingFixture(t, accountrouting.Smart)
	saveAccountQuota(t, store, "a", `{"windows":[{"used":25,"span":604800,"reset_after_seconds":86400}]}`)
	before, ok := store.GetProviderResourceObservation("a")
	if !ok {
		t.Fatal("no cache")
	}
	expected, ok := store.GetProviderResource("a")
	if !ok {
		t.Fatal("no account")
	}
	if err := store.saveProviderQuotaFailure(context.Background(), expected, "provider_quota_unavailable", time.Now()); err != nil {
		t.Fatal(err)
	}
	after, _ := store.GetProviderResourceObservation("a")
	projection := allowance.Read([]byte(after.QuotaSnapshot), *after.QuotaFetchedAt)
	if projection.Status != allowance.QueryError || len(projection.Windows) != 1 || !before.QuotaFetchedAt.Equal(*after.QuotaFetchedAt) {
		t.Fatalf("failure destroyed quota age: %+v", projection)
	}
	// A late failed request cannot hide a more recent successful refresh.
	started := time.Now().Add(-time.Hour)
	saveAccountQuota(t, store, "a", `{"used_percent":10}`)
	if err := store.saveProviderQuotaFailure(context.Background(), expected, "late_failure", started); err != nil {
		t.Fatal(err)
	}
	latest, _ := store.GetProviderResourceObservation("a")
	if got := allowance.Read([]byte(latest.QuotaSnapshot), *latest.QuotaFetchedAt); got.Status != allowance.Available {
		t.Fatal("late failure replaced fresh quota")
	}
}

func TestAccountStrategyIsIndependentAtomicAndPreservedOnModelEdit(t *testing.T) {
	server, store, call, routes := accountRoutingFixture(t, accountrouting.Default)
	strategy := string(accountrouting.Expiry)
	policy := ModelRoutePolicy{Strategy: RouteStrategyQuality, AccountStrategy: &strategy, Routes: []ModelRoutePolicyRoute{{RouteID: routes[0].Route.ID, Weight: 100, QualityScore: 90, CostScore: 60}}}
	response := doJSON(t, server.Handler(), http.MethodPatch, "/api/admin/model-routing-policies/"+call.Model.Name, policy, "dev_admin_token")
	if response.Code != http.StatusOK {
		t.Fatalf("save policy: %d %s", response.Code, response.Body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["account_strategy"] != strategy || payload["strategy"] != RouteStrategyQuality {
		t.Fatalf("strategies mixed: %+v", payload)
	}
	updated, err := store.UpdateModel(call.Model.Name, Model{Metadata: map[string]string{accountRoutingMetadataKey: "drain", "fixture": "retained"}})
	if err != nil || modelAccountStrategy(updated) != accountrouting.Expiry {
		t.Fatalf("ordinary model edit replaced account strategy: %+v %v", updated, err)
	}
	invalid := "not-a-strategy"
	policy.AccountStrategy = &invalid
	policy.Strategy = RouteStrategyCost
	if _, err := store.UpdateModelRoutePolicy(call.Model.Name, policy); err == nil {
		t.Fatal("invalid strategy was accepted")
	}
	for _, route := range store.ListRoutes() {
		if route.ModelName == call.Model.Name && route.Strategy != RouteStrategyQuality {
			t.Fatal("invalid update partially changed model strategy")
		}
	}
	policy.AccountStrategy = nil
	policy.Strategy = RouteStrategyCost
	if _, err := store.UpdateModelRoutePolicy(call.Model.Name, policy); err != nil {
		t.Fatal(err)
	}
	for _, model := range store.ListModels() {
		if model.Name == call.Model.Name && modelAccountStrategy(model) != accountrouting.Expiry {
			t.Fatal("omitted field cleared account strategy")
		}
	}
}

func TestAccountLeastUsedCountsPersistedAttemptsAndIgnoresCapacitySkips(t *testing.T) {
	server, store, call, routes := accountRoutingFixture(t, accountrouting.LeastUsed)
	for _, id := range []string{"a", "b", "c"} {
		saveAccountQuota(t, store, id, `{"used_percent":20}`)
	}
	for i, row := range []RouteAttemptLog{{ProviderResourceID: "a", TotalTokens: 100, Invoked: true}, {ProviderResourceID: "b", TotalTokens: 10, Invoked: true}, {ProviderResourceID: "c", TotalTokens: 1000, Invoked: false}} {
		row.ID = fmt.Sprintf("account-attempt-%d", i)
		row.CreatedAt = time.Now()
		if err := store.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	got := server.planAccountOrder(context.Background(), call, routes)
	var ids []string
	for _, r := range got {
		ids = append(ids, r.Resource.ID)
	}
	if strings.Join(ids, ",") != "c,b,a" {
		t.Fatalf("least used order=%v", ids)
	}
}
