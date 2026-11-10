package server

import (
	"errors"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
)

type pricingMembershipFixture struct {
	store   *GormStore
	source  ProviderModel
	model   Model
	route   ModelRoute
	project Project
	key     APIKey
}

func newPricingMembershipFixture(t *testing.T) pricingMembershipFixture {
	t.Helper()
	store, source := pricingSourceFixture(t)
	model := store.AddModel(linkedPricingModel(source))
	route := store.AddRoute(ModelRoute{ModelName: model.Name, ProviderID: source.ProviderID, ProviderModel: source.UpstreamModel, Status: StatusActive})
	project := store.CreateProject(Project{Name: "Pricing membership", Status: StatusActive})
	key, _, err := store.CreateAPIKey(project.ID, APIKey{Name: "Membership key", Status: StatusActive}, "pricing-membership-key")
	if err != nil {
		t.Fatal(err)
	}
	return pricingMembershipFixture{store: store, source: source, model: model, route: route, project: project, key: key}
}

func TestModelPricingSourceMembershipDoesNotReadPricingSources(t *testing.T) {
	for _, state := range []string{"available", "missing_source", "disabled_provider"} {
		t.Run(state, func(t *testing.T) {
			fixture := newPricingMembershipFixture(t)
			switch state {
			case "missing_source":
				if err := fixture.store.DeleteProviderModel(fixture.source.ID); err != nil {
					t.Fatal(err)
				}
			case "disabled_provider":
				if err := fixture.store.db.Model(&Provider{}).Where("id = ?", fixture.source.ProviderID).Update("status", StatusDisabled).Error; err != nil {
					t.Fatal(err)
				}
			}

			var sourceQueries atomic.Int64
			callback := "test:membership-without-pricing"
			if err := fixture.store.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "provider_models" || tx.Statement.Table == "providers" {
					sourceQueries.Add(1)
					_ = tx.AddError(errors.New("pricing reads are unavailable during membership checks"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = fixture.store.db.Callback().Query().Remove(callback) })

			models := accessibleModelMembership(fixture.store, fixture.key)
			if len(models) != 1 || models[0].Name != fixture.model.Name {
				t.Fatalf("pricing availability changed membership: %+v", models)
			}
			if models[0].InputPriceUSDPer1M != fixture.model.InputPriceUSDPer1M || models[0].Metadata[modelPricingProviderKey] != fixture.source.ID || models[0].pricingUnavailable {
				t.Fatalf("membership projected pricing instead of retaining the stored model: %+v", models[0])
			}
			for _, name := range []string{fixture.model.Name, fixture.model.ID} {
				if !keyCanAccessModel(fixture.store, fixture.key, name) {
					t.Fatalf("authorized model %q lost membership", name)
				}
			}
			if keyCanAccessModel(fixture.store, fixture.key, "unpublished-model") {
				t.Fatal("unpublished model gained membership")
			}
			if queries := sourceQueries.Load(); queries != 0 {
				t.Fatalf("membership performed %d pricing source queries", queries)
			}
		})
	}
}

func TestModelPricingSourceMembershipPreservesAuthorization(t *testing.T) {
	for _, scope := range []string{"project_models", "key_models", "route_project", "routing_policy", "inactive_route", "inactive_model", "unknown_key"} {
		t.Run(scope, func(t *testing.T) {
			fixture := newPricingMembershipFixture(t)
			if err := fixture.store.DeleteProviderModel(fixture.source.ID); err != nil {
				t.Fatal(err)
			}
			if !keyCanAccessModel(fixture.store, fixture.key, fixture.model.Name) {
				t.Fatal("broken pricing reference removed otherwise authorized membership")
			}
			var err error
			switch scope {
			case "project_models":
				_, err = fixture.store.UpdateProject(fixture.project.ID, Project{ModelAccessMode: ModelAccessModeRestricted, AllowedModels: []string{}})
			case "key_models":
				_, err = fixture.store.UpdateAPIKey(fixture.key.ID, APIKey{ModelAccessMode: ModelAccessModeRestricted, Allowed: []string{}})
			case "route_project":
				_, err = fixture.store.UpdateRoute(fixture.route.ID, ModelRoute{ProjectScope: RouteProjectScopeExclude, ProjectIDs: []string{fixture.project.ID}})
			case "routing_policy":
				fixture.store.CreateResource(routingPolicyResourceKind, AdminResource{
					Name: "Require internal routes", Status: StatusActive,
					Fields: map[string]any{"scope": RoutingPolicyScopeProject, "scope_id": fixture.project.ID, "required_tags": []string{"internal"}},
				})
			case "inactive_route":
				_, err = fixture.store.UpdateRoute(fixture.route.ID, ModelRoute{Status: StatusDisabled})
			case "inactive_model":
				err = fixture.store.db.Model(&Model{}).Where("id = ?", fixture.model.ID).Update("status", StatusDisabled).Error
			case "unknown_key":
				fixture.key.ID = "missing-membership-key"
			}
			if err != nil {
				t.Fatal(err)
			}
			if models := accessibleModelMembership(fixture.store, fixture.key); len(models) != 0 {
				t.Fatalf("membership bypassed %s authorization: %+v", scope, models)
			}
			if keyCanAccessModel(fixture.store, fixture.key, fixture.model.Name) {
				t.Fatalf("model authorization bypassed %s", scope)
			}
		})
	}
}

func TestModelPricingSourceProjectionReleasesMembershipMutex(t *testing.T) {
	fixture := newPricingMembershipFixture(t)
	var sourceQueries, lockedQueries atomic.Int64
	callback := "test:pricing-projection-unlocked"
	if err := fixture.store.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table != "provider_models" && tx.Statement.Table != "providers" {
			return
		}
		sourceQueries.Add(1)
		if fixture.store.mu.TryLock() {
			fixture.store.mu.Unlock()
		} else {
			lockedQueries.Add(1)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.store.db.Callback().Query().Remove(callback) })

	models := fixture.store.AccessibleModels(fixture.key)
	if len(models) != 1 || models[0].InputPriceUSDPer1M != fixture.source.InputPriceUSDPer1M || models[0].pricingUnavailable {
		t.Fatalf("public projection did not resolve the linked price: %+v", models)
	}
	if sourceQueries.Load() == 0 {
		t.Fatal("projection did not read the current pricing source")
	}
	if queries := lockedQueries.Load(); queries != 0 {
		t.Fatalf("pricing projection held the membership mutex during %d source queries", queries)
	}
}

type pricingMembershipFallbackStore struct {
	Store
	models []Model
	calls  int
}

func (s *pricingMembershipFallbackStore) AccessibleModels(APIKey) []Model {
	s.calls++
	return s.models
}

func TestModelPricingSourceMembershipSupportsOtherStores(t *testing.T) {
	store := &pricingMembershipFallbackStore{models: []Model{{Name: "fallback-model"}}}
	if models := accessibleModelMembership(store, APIKey{}); len(models) != 1 || models[0].Name != "fallback-model" {
		t.Fatalf("fallback store lost membership: %+v", models)
	}
	if !keyCanAccessModel(store, APIKey{}, "fallback-model") || store.calls != 2 {
		t.Fatalf("membership did not preserve the Store fallback: calls=%d", store.calls)
	}
}
