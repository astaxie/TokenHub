package server

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestSelectRouteCandidatesBatchesLookupQueries(t *testing.T) {
	store := NewMemoryStore()
	seed := func(modelName string, count int) {
		t.Helper()
		store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
		providers := make([]Provider, 0, count)
		resources := make([]ProviderResource, 0, count)
		routes := make([]ModelRoute, 0, count)
		for index := 0; index < count; index++ {
			providerID := fmt.Sprintf("prv_%s_%03d", modelName, index)
			resourceID := fmt.Sprintf("rsrc_%s_%03d", modelName, index)
			providers = append(providers, Provider{
				ID: providerID, Name: providerID, Type: ProviderMock,
				Status: StatusActive, Healthy: true,
			})
			resources = append(resources, ProviderResource{
				ID: resourceID, ProviderID: providerID, Name: resourceID,
				Status: StatusActive, Healthy: true,
			})
			route := ModelRoute{
				ID: fmt.Sprintf("route_%s_%03d", modelName, index), ModelName: modelName,
				ProviderID: providerID, ProviderModel: modelName,
				Priority: index + 1, Weight: 100, Status: StatusActive,
			}
			if index%2 == 0 {
				route.ProviderResourceID = resourceID
			}
			routes = append(routes, route)
		}
		if err := store.db.CreateInBatches(&providers, 100).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.CreateInBatches(&resources, 100).Error; err != nil {
			t.Fatal(err)
		}
		if err := store.db.CreateInBatches(&routes, 100).Error; err != nil {
			t.Fatal(err)
		}
	}

	seed("batch-small", 2)
	seed("batch-large", 64)
	var small, large []RouteSelection
	smallQueries := countStoreQueries(t, store, func() {
		var err error
		small, err = store.SelectRouteCandidates("batch-small")
		if err != nil {
			t.Fatal(err)
		}
	})
	largeQueries := countStoreQueries(t, store, func() {
		var err error
		large, err = store.SelectRouteCandidates("batch-large")
		if err != nil {
			t.Fatal(err)
		}
	})
	if len(small) != 2 || len(large) != 64 {
		t.Fatalf("candidate counts = %d, %d; want 2, 64", len(small), len(large))
	}
	if smallQueries != 4 || largeQueries != smallQueries {
		t.Fatalf("lookup queries = small:%d large:%d, want four for both", smallQueries, largeQueries)
	}
}

func TestSelectRouteCandidatesChunksBatchLookups(t *testing.T) {
	store := NewMemoryStore()
	modelName := "chunked-route-lookups"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	count := routeCandidateLookupBatchSize + 1
	providers := make([]Provider, 0, count)
	resources := make([]ProviderResource, 0, count)
	routes := make([]ModelRoute, 0, count)
	for index := 0; index < count; index++ {
		providerID := fmt.Sprintf("prv_chunk_%03d", index)
		resourceID := fmt.Sprintf("rsrc_chunk_%03d", index)
		providers = append(providers, Provider{
			ID: providerID, Name: providerID, Type: ProviderMock,
			Status: StatusActive, Healthy: true,
		})
		resources = append(resources, ProviderResource{
			ID: resourceID, ProviderID: providerID, Name: resourceID,
			Status: StatusActive, Healthy: true,
		})
		routes = append(routes, ModelRoute{
			ID: fmt.Sprintf("route_chunk_%03d", index), ModelName: modelName,
			ProviderID: providerID, ProviderResourceID: resourceID, ProviderModel: modelName,
			Priority: index + 1, Weight: 100, Status: StatusActive,
		})
	}
	if err := store.db.CreateInBatches(&providers, 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.CreateInBatches(&resources, 100).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.CreateInBatches(&routes, 100).Error; err != nil {
		t.Fatal(err)
	}

	var candidates []RouteSelection
	queries := countStoreQueries(t, store, func() {
		var err error
		candidates, err = store.SelectRouteCandidates(modelName)
		if err != nil {
			t.Fatal(err)
		}
	})
	if len(candidates) != count {
		t.Fatalf("candidate count = %d, want %d", len(candidates), count)
	}
	if queries != 5 {
		t.Fatalf("lookup queries = %d, want route query plus two provider and two resource batches", queries)
	}
}

func TestSelectRouteCandidatesValidatesResourceOwnershipAndOrdersTies(t *testing.T) {
	store := NewMemoryStore()
	modelName := "resource-order-model"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_resource_order", Name: "Resource order", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	foreignProvider := store.AddProvider(Provider{
		ID: "prv_resource_foreign", Name: "Foreign resource", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	createdAt := time.Date(2026, time.August, 16, 0, 0, 0, 0, time.UTC)
	resources := []ProviderResource{
		{ID: "rsrc_z", ProviderID: provider.ID, Name: "Z", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100, CreatedAt: createdAt},
		{ID: "rsrc_a", ProviderID: provider.ID, Name: "A", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100, CreatedAt: createdAt},
		{ID: "rsrc_foreign", ProviderID: foreignProvider.ID, Name: "Foreign", Status: StatusActive, Healthy: true},
	}
	if err := store.db.Create(&resources).Error; err != nil {
		t.Fatal(err)
	}
	routes := []ModelRoute{
		{ID: "route_foreign", ModelName: modelName, ProviderID: provider.ID, ProviderResourceID: "rsrc_foreign", ProviderModel: modelName, Priority: 1, Weight: 100, Status: StatusActive},
		{ID: "route_resources", ModelName: modelName, ProviderID: provider.ID, ProviderModel: modelName, Priority: 2, Weight: 100, Status: StatusActive},
	}
	if err := store.db.Create(&routes).Error; err != nil {
		t.Fatal(err)
	}

	candidates, err := store.SelectRouteCandidates(modelName)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d, want two owned resources", len(candidates))
	}
	for index, resourceID := range []string{"rsrc_a", "rsrc_z"} {
		if candidates[index].Route.ID != "route_resources" || routeResourceID(candidates[index]) != resourceID {
			t.Fatalf("candidate %d = route %q resource %q, want route_resources/%s",
				index, candidates[index].Route.ID, routeResourceID(candidates[index]), resourceID)
		}
	}
}

func TestSelectRouteCandidatesPreservesGroupsAndProviderFallback(t *testing.T) {
	store := NewMemoryStore()
	modelName := "resource-group-model"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_resource_group", Name: "Resource group", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	resources := []ProviderResource{
		{ID: "rsrc_blue", ProviderID: provider.ID, Name: "Blue", Group: "blue", Status: StatusActive, Healthy: true},
		{ID: "rsrc_green", ProviderID: provider.ID, Name: "Green", Group: "green", Status: StatusActive, Healthy: true},
	}
	if err := store.db.Create(&resources).Error; err != nil {
		t.Fatal(err)
	}
	routes := []ModelRoute{
		{ID: "route_blue", ModelName: modelName, ProviderID: provider.ID, ResourceGroup: " blue ", ProviderModel: modelName, Priority: 1, Weight: 100, Status: StatusActive},
		{ID: "route_missing_group", ModelName: modelName, ProviderID: provider.ID, ResourceGroup: "missing", ProviderModel: modelName, Priority: 2, Weight: 100, Status: StatusActive},
	}
	if err := store.db.Create(&routes).Error; err != nil {
		t.Fatal(err)
	}

	candidates, err := store.SelectRouteCandidates(modelName)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidate count = %d, want grouped resource plus provider fallback", len(candidates))
	}
	if candidates[0].Route.ID != "route_blue" || routeResourceID(candidates[0]) != "rsrc_blue" {
		t.Fatalf("grouped candidate = %+v", candidates[0])
	}
	if candidates[1].Route.ID != "route_missing_group" || candidates[1].Resource != nil {
		t.Fatalf("fallback candidate = %+v", candidates[1])
	}
}

func TestSelectRouteCandidatesFallsBackAfterBatchLookupErrors(t *testing.T) {
	for _, target := range []string{"providers", "provider_resources"} {
		t.Run(target, func(t *testing.T) {
			store := NewMemoryStore()
			modelName := "lookup-error-" + target
			store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
			badProvider := store.AddProvider(Provider{
				ID: "prv_lookup_error_bad_" + target, Name: "Lookup error bad", Type: ProviderMock,
				Status: StatusActive, Healthy: true,
			})
			goodProvider := store.AddProvider(Provider{
				ID: "prv_lookup_error_good_" + target, Name: "Lookup error good", Type: ProviderMock,
				Status: StatusActive, Healthy: true,
			})
			badResource, err := store.AddProviderResource(ProviderResource{
				ID: "rsrc_lookup_error_bad_" + target, ProviderID: badProvider.ID,
				Name: "Lookup error bad", Status: StatusActive, Healthy: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			goodResource, err := store.AddProviderResource(ProviderResource{
				ID: "rsrc_lookup_error_good_" + target, ProviderID: goodProvider.ID,
				Name: "Lookup error good", Status: StatusActive, Healthy: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			for index, route := range []ModelRoute{
				{ID: "route_lookup_error_bad_" + target, ProviderID: badProvider.ID, ProviderResourceID: badResource.ID},
				{ID: "route_lookup_error_good_" + target, ProviderID: goodProvider.ID, ProviderResourceID: goodResource.ID},
			} {
				route.ModelName = modelName
				route.ProviderModel = modelName
				route.Priority = index + 1
				route.Weight = 100
				route.Status = StatusActive
				store.AddRoute(route)
			}

			wantErr := errors.New(target + " lookup failed")
			attempts := 0
			callbackName := "test:route-candidate-query-error:" + target
			if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Table == target {
					attempts++
					if attempts <= 2 {
						_ = tx.AddError(wantErr)
					}
				}
			}); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := store.db.Callback().Query().Remove(callbackName); err != nil {
					t.Errorf("remove query callback: %v", err)
				}
			}()

			candidates, err := store.SelectRouteCandidates(modelName)
			if err != nil {
				t.Fatalf("SelectRouteCandidates returned batch lookup error: %v", err)
			}
			if attempts != 3 {
				t.Fatalf("%s lookup attempts = %d, want batch plus two individual attempts", target, attempts)
			}
			if len(candidates) != 1 || candidates[0].Route.ID != "route_lookup_error_good_"+target {
				t.Fatalf("fallback candidates = %+v, want the unaffected route", candidates)
			}
		})
	}
}

func TestSelectRouteCandidatesIgnoresOptionalRuntimeStatsErrors(t *testing.T) {
	store := NewMemoryStore()
	modelName := "runtime-stats-error-model"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_runtime_stats_error", Name: "Runtime stats error", Type: ProviderMock,
		Status: StatusActive, Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID: "route_runtime_stats_error", ModelName: modelName, ProviderID: provider.ID,
		ProviderModel: modelName, Priority: 1, Weight: 100,
		Status: StatusActive, Strategy: RouteStrategyAdaptive,
	})

	callbackName := "test:route-runtime-stats-error"
	resultCallbackName := callbackName + ":result"
	var statsQueryErr error
	if err := store.db.Callback().Row().Before("gorm:row").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "route_attempt_logs" {
			tx.Statement.Table = "missing_route_attempt_logs"
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.db.Callback().Row().After("gorm:row").Register(resultCallbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "route_attempt_logs" {
			statsQueryErr = tx.Error
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.db.Callback().Row().Remove(callbackName); err != nil {
			t.Errorf("remove runtime stats error callback: %v", err)
		}
		if err := store.db.Callback().Row().Remove(resultCallbackName); err != nil {
			t.Errorf("remove runtime stats result callback: %v", err)
		}
	}()

	candidates, err := store.SelectRouteCandidates(modelName)
	if err != nil {
		t.Fatalf("optional runtime stats failure rejected candidates: %v", err)
	}
	if statsQueryErr == nil {
		t.Fatal("runtime stats query did not reach the intended database-side failure")
	}
	if len(candidates) != 1 || candidates[0].Provider.ID != provider.ID || candidates[0].Runtime != (RouteRuntimeStats{}) {
		t.Fatalf("candidates after optional runtime stats failure = %+v", candidates)
	}
}

func TestSelectCodexRouteCandidatesDistinguishesResourceAvailability(t *testing.T) {
	now := time.Now().UTC()
	for _, binding := range []string{"explicit", "implicit"} {
		for _, test := range []struct {
			name          string
			resource      *ProviderResource
			wantCode      string
			wantCandidate bool
		}{
			{name: "missing", wantCode: "provider_resource_missing"},
			{name: "disabled", resource: &ProviderResource{Status: StatusDisabled, Healthy: false}, wantCode: "provider_resource_disabled"},
			{name: "unhealthy", resource: &ProviderResource{Status: StatusActive, Healthy: false}, wantCode: "provider_resource_unhealthy"},
			{name: "cooling down", resource: &ProviderResource{Status: StatusActive, Healthy: false, CooldownUntil: ptrTime(now.Add(time.Minute))}, wantCode: "provider_resource_cooling_down"},
			{name: "expired cooldown", resource: &ProviderResource{Status: StatusActive, Healthy: false, CooldownUntil: ptrTime(now.Add(-time.Minute))}, wantCandidate: true},
			{name: "healthy", resource: &ProviderResource{Status: StatusActive, Healthy: true}, wantCandidate: true},
		} {
			t.Run(binding+"/"+test.name, func(t *testing.T) {
				store := NewMemoryStore()
				modelName := "codex-resource-availability"
				store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
				provider := store.AddProvider(Provider{
					ID: "prv_codex_resource_availability", Name: "Codex resource availability",
					Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
				})
				resourceID := "rsrc_codex_resource_availability"
				if test.resource != nil {
					resource := *test.resource
					resource.ID = resourceID
					resource.ProviderID = provider.ID
					resource.Name = "Codex account"
					resource.ResourceType = ProviderResourceOpenAISubscription
					if err := store.db.Create(&resource).Error; err != nil {
						t.Fatal(err)
					}
				}
				route := ModelRoute{
					ID: "route_codex_resource_availability", ModelName: modelName,
					ProviderID: provider.ID, ProviderModel: modelName,
					Status: StatusActive, Priority: 1, Weight: 100,
				}
				if binding == "explicit" {
					route.ProviderResourceID = resourceID
				}
				store.AddRoute(route)

				candidates, err := store.SelectRouteCandidates(modelName)
				if test.wantCandidate {
					if err != nil || len(candidates) != 1 || routeResourceID(candidates[0]) != resourceID {
						t.Fatalf("eligible resource candidates=%+v err=%v", candidates, err)
					}
					return
				}
				httpErr := AsHTTPError(err)
				if httpErr == nil || httpErr.Code != test.wantCode {
					t.Fatalf("availability error=%+v want code=%q candidates=%+v", httpErr, test.wantCode, candidates)
				}
				if len(candidates) != 0 {
					t.Fatalf("unavailable resource returned candidates: %+v", candidates)
				}
			})
		}
	}
}

func TestSelectPluginAccountRouteCandidatesRequireExistingAccountResource(t *testing.T) {
	now := time.Now().UTC()
	for _, binding := range []string{"explicit", "implicit"} {
		for _, test := range []struct {
			name          string
			resource      *ProviderResource
			wantCode      string
			wantCandidate bool
		}{
			{name: "disabled", resource: &ProviderResource{Status: StatusDisabled, Healthy: false}, wantCode: "provider_resource_disabled"},
			{name: "unhealthy", resource: &ProviderResource{Status: StatusActive, Healthy: false}, wantCode: "provider_resource_unhealthy"},
			{name: "cooling down", resource: &ProviderResource{Status: StatusActive, Healthy: false, CooldownUntil: ptrTime(now.Add(time.Minute))}, wantCode: "provider_resource_cooling_down"},
			{name: "healthy", resource: &ProviderResource{Status: StatusActive, Healthy: true}, wantCandidate: true},
		} {
			t.Run(binding+"/"+test.name, func(t *testing.T) {
				store := NewMemoryStore()
				modelName := "plugin-account-resource-availability"
				store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
				provider := store.AddProvider(Provider{
					ID: "prv_plugin_resource_availability", Name: "Plugin resource availability",
					Type: "kimi_subscription", Status: StatusActive, Healthy: true,
				})
				resourceID := "rsrc_plugin_resource_availability"
				if test.resource != nil {
					resource := *test.resource
					resource.ID = resourceID
					resource.ProviderID = provider.ID
					resource.Name = "Kimi account"
					resource.ResourceType = "kimi_subscription_account"
					if err := store.db.Create(&resource).Error; err != nil {
						t.Fatal(err)
					}
				}
				route := ModelRoute{
					ID: "route_plugin_resource_availability", ModelName: modelName,
					ProviderID: provider.ID, ProviderModel: modelName,
					Status: StatusActive, Priority: 1, Weight: 100,
				}
				if binding == "explicit" {
					route.ProviderResourceID = resourceID
				}
				store.AddRoute(route)

				candidates, err := store.SelectRouteCandidates(modelName)
				if test.wantCandidate {
					if err != nil || len(candidates) != 1 || routeResourceID(candidates[0]) != resourceID {
						t.Fatalf("eligible plugin resource candidates=%+v err=%v", candidates, err)
					}
					return
				}
				httpErr := AsHTTPError(err)
				if httpErr == nil || httpErr.Code != test.wantCode {
					t.Fatalf("availability error=%+v want code=%q candidates=%+v", httpErr, test.wantCode, candidates)
				}
				if len(candidates) != 0 {
					t.Fatalf("unavailable plugin resource returned candidates: %+v", candidates)
				}
			})
		}
	}
}

func TestPluginAccountRouteCandidatesUseProviderFallbackWhenNoResourcesExist(t *testing.T) {
	store := NewMemoryStore()
	modelName := "plugin-account-provider-fallback"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_plugin_account_provider_fallback", Name: "Plugin account provider fallback",
		Type: "kimi_subscription", Status: StatusActive, Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID: "route_plugin_account_provider_fallback", ModelName: modelName,
		ProviderID: provider.ID, ProviderModel: modelName,
		Status: StatusActive, Priority: 1, Weight: 100,
	})

	candidates, err := store.SelectRouteCandidates(modelName)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("plugin provider-level candidates=%+v err=%v", candidates, err)
	}
	if candidates[0].Resource != nil {
		t.Fatalf("plugin route without resources should use provider-level fallback: %+v", candidates[0])
	}
}

func TestPluginAccountExplicitMissingResourceFails(t *testing.T) {
	store := NewMemoryStore()
	modelName := "plugin-account-explicit-missing-resource"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_plugin_explicit_missing_resource", Name: "Plugin explicit missing resource",
		Type: "kimi_subscription", Status: StatusActive, Healthy: true,
	})
	store.AddRoute(ModelRoute{
		ID: "route_plugin_explicit_missing_resource", ModelName: modelName,
		ProviderID: provider.ID, ProviderResourceID: "rsrc_missing_plugin_account",
		ProviderModel: modelName, Status: StatusActive, Priority: 1, Weight: 100,
	})

	candidates, err := store.SelectRouteCandidates(modelName)
	httpErr := AsHTTPError(err)
	if httpErr == nil || httpErr.Code != "provider_resource_missing" {
		t.Fatalf("availability error=%+v want missing resource candidates=%+v", httpErr, candidates)
	}
}

func TestRouteCandidatesRequireResourceFromProviderPolicy(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name          string
		resource      *ProviderResource
		wantCode      string
		wantCandidate bool
	}{
		{name: "missing", wantCode: "provider_resource_missing"},
		{name: "disabled", resource: &ProviderResource{Status: StatusDisabled, Healthy: false}, wantCode: "provider_resource_disabled"},
		{name: "unhealthy", resource: &ProviderResource{Status: StatusActive, Healthy: false}, wantCode: "provider_resource_unhealthy"},
		{name: "cooling down", resource: &ProviderResource{Status: StatusActive, Healthy: false, CooldownUntil: ptrTime(now.Add(time.Minute))}, wantCode: "provider_resource_cooling_down"},
		{name: "healthy", resource: &ProviderResource{Status: StatusActive, Healthy: true}, wantCandidate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewMemoryStore()
			modelName := "provider-policy-requires-resource"
			store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
			provider := store.AddProvider(Provider{
				ID: "prv_policy_requires_resource", Name: "Policy Requires Resource",
				Type: "subscription_plugin", Status: StatusActive, Healthy: true,
				Options: map[string]string{providerRouteRequiresResourceOption: "true"},
			})
			resourceID := "rsrc_policy_requires_resource"
			if test.resource != nil {
				resource := *test.resource
				resource.ID = resourceID
				resource.ProviderID = provider.ID
				resource.Name = "Plugin account"
				resource.ResourceType = ProviderResourceAPIKey
				if err := store.db.Create(&resource).Error; err != nil {
					t.Fatal(err)
				}
			}
			store.AddRoute(ModelRoute{
				ID: "route_policy_requires_resource", ModelName: modelName,
				ProviderID: provider.ID, ProviderModel: modelName,
				Status: StatusActive, Priority: 1, Weight: 100,
			})

			candidates, err := store.SelectRouteCandidates(modelName)
			if test.wantCandidate {
				if err != nil || len(candidates) != 1 || routeResourceID(candidates[0]) != resourceID {
					t.Fatalf("eligible policy resource candidates=%+v err=%v", candidates, err)
				}
				return
			}
			httpErr := AsHTTPError(err)
			if httpErr == nil || httpErr.Code != test.wantCode {
				t.Fatalf("availability error=%+v want code=%q candidates=%+v", httpErr, test.wantCode, candidates)
			}
			if len(candidates) != 0 {
				t.Fatalf("unavailable policy resource returned candidates: %+v", candidates)
			}
		})
	}
}

func TestProviderCredentialScopeRejectsProviderLevelAPIKey(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_resource_credentials", Name: "Resource Credentials",
		Type: "subscription_plugin", APIKey: "create-secret", Status: StatusActive, Healthy: true,
		Options: map[string]string{providerCredentialsScopeOption: providerCredentialsScopeResource},
	})
	if stored, ok := store.GetProvider(provider.ID); !ok || stored.APIKey != "" {
		t.Fatalf("resource credentials provider stored api key on create: ok=%v provider=%+v", ok, stored)
	}

	_, err := store.UpdateProvider(provider.ID, Provider{
		APIKey:  "update-secret",
		Options: map[string]string{providerCredentialsScopeOption: providerCredentialsScopeResource},
		Healthy: true,
	})
	if httpErr := AsHTTPError(err); httpErr == nil || httpErr.Code != "provider_adapter_credential_conflict" {
		t.Fatalf("resource credentials update error = %+v", httpErr)
	}
}

func TestPluginAccountGroupedResourceMissingSurvivesBatchLookupFallback(t *testing.T) {
	store := NewMemoryStore()
	modelName := "plugin-account-grouped-resource-missing"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_plugin_grouped_resource_missing", Name: "Plugin grouped resource missing",
		Type: "kimi_subscription", Status: StatusActive, Healthy: true,
	})
	if err := store.db.Create(&ProviderResource{
		ID: "rsrc_plugin_grouped_resource_missing", ProviderID: provider.ID, Name: "Plugin account",
		ResourceType: "kimi_subscription_account", Group: "blue",
		Status: StatusActive, Healthy: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{
		ID: "route_plugin_grouped_resource_missing", ModelName: modelName,
		ProviderID: provider.ID, ResourceGroup: "green", ProviderModel: modelName,
		Status: StatusActive, Priority: 1, Weight: 100,
	})

	attempts := 0
	callbackName := "test:plugin-account-grouped-resource-missing"
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "providers" {
			attempts++
			if attempts == 1 {
				_ = tx.AddError(errors.New("force individual candidate lookup"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	}()

	candidates, err := store.SelectRouteCandidates(modelName)
	httpErr := AsHTTPError(err)
	if httpErr == nil || httpErr.Code != "provider_resource_missing" {
		t.Fatalf("availability error=%+v want missing resource candidates=%+v", httpErr, candidates)
	}
	if attempts != 2 {
		t.Fatalf("provider lookup attempts=%d want batch plus individual", attempts)
	}
}

func TestProviderLevelRouteKeepsResourceOptional(t *testing.T) {
	now := time.Now().UTC()
	for _, providerType := range []string{ProviderOpenAICompatible, "custom_stdio"} {
		t.Run(providerType, func(t *testing.T) {
			for _, test := range []struct {
				name         string
				resource     *ProviderResource
				wantResource bool
			}{
				{name: "no resource"},
				{name: "disabled resource", resource: &ProviderResource{Status: StatusDisabled, Healthy: false}},
				{name: "unhealthy resource", resource: &ProviderResource{Status: StatusActive, Healthy: false}},
				{name: "cooling resource", resource: &ProviderResource{Status: StatusActive, Healthy: false, CooldownUntil: ptrTime(now.Add(time.Minute))}},
				{name: "expired cooldown resource", resource: &ProviderResource{Status: StatusActive, Healthy: false, CooldownUntil: ptrTime(now.Add(-time.Minute))}, wantResource: true},
				{name: "healthy resource", resource: &ProviderResource{Status: StatusActive, Healthy: true}, wantResource: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					store := NewMemoryStore()
					modelName := "provider-level-resource-optional-" + providerType
					store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
					provider := store.AddProvider(Provider{
						ID: "prv_provider_level_resource_optional_" + providerType, Name: "Provider-level credentials",
						Type: providerType, Status: StatusActive, Healthy: true,
					})
					resourceID := "rsrc_provider_level_resource_optional_" + providerType
					if test.resource != nil {
						resource := *test.resource
						resource.ID = resourceID
						resource.ProviderID = provider.ID
						resource.Name = "Optional resource"
						resource.ResourceType = ProviderResourceAPIKey
						if err := store.db.Create(&resource).Error; err != nil {
							t.Fatal(err)
						}
					}
					store.AddRoute(ModelRoute{
						ID: "route_provider_level_resource_optional_" + providerType, ModelName: modelName,
						ProviderID: provider.ID, ProviderModel: modelName,
						Status: StatusActive, Priority: 1, Weight: 100,
					})

					candidates, err := store.SelectRouteCandidates(modelName)
					if err != nil || len(candidates) != 1 {
						t.Fatalf("provider-level candidates=%+v err=%v", candidates, err)
					}
					if test.wantResource {
						if routeResourceID(candidates[0]) != resourceID {
							t.Fatalf("eligible optional resource was not selected: %+v", candidates[0])
						}
					} else if candidates[0].Resource != nil {
						t.Fatalf("unavailable optional resource blocked provider-level fallback: %+v", candidates[0])
					}
				})
			}
		})
	}
}

func TestProviderLevelFallbackSurvivesBatchLookupFallback(t *testing.T) {
	store := NewMemoryStore()
	modelName := "provider-level-batch-fallback"
	store.AddModel(Model{Name: modelName, Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{
		ID: "prv_provider_level_batch_fallback", Name: "Provider-level batch fallback",
		Type: ProviderOpenAICompatible, Status: StatusActive, Healthy: true,
	})
	if err := store.db.Create(&ProviderResource{
		ID: "rsrc_provider_level_batch_fallback", ProviderID: provider.ID, Name: "Disabled optional resource",
		ResourceType: ProviderResourceAPIKey, Status: StatusDisabled, Healthy: false,
	}).Error; err != nil {
		t.Fatal(err)
	}
	store.AddRoute(ModelRoute{
		ID: "route_provider_level_batch_fallback", ModelName: modelName,
		ProviderID: provider.ID, ProviderModel: modelName,
		Status: StatusActive, Priority: 1, Weight: 100,
	})

	attempts := 0
	callbackName := "test:provider-level-batch-fallback"
	if err := store.db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "providers" {
			attempts++
			if attempts == 1 {
				_ = tx.AddError(errors.New("force individual candidate lookup"))
			}
		}
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.db.Callback().Query().Remove(callbackName); err != nil {
			t.Errorf("remove query callback: %v", err)
		}
	}()

	candidates, err := store.SelectRouteCandidates(modelName)
	if err != nil || len(candidates) != 1 || candidates[0].Resource != nil {
		t.Fatalf("individual provider-level fallback candidates=%+v err=%v", candidates, err)
	}
	if attempts != 2 {
		t.Fatalf("provider lookup attempts=%d want batch plus individual", attempts)
	}
}

func TestProviderResourceCapacityRejectsResourceDisabledAfterSelection(t *testing.T) {
	store := NewMemoryStore()
	provider := store.AddProvider(Provider{
		ID: "prv_disable_after_selection", Name: "Disable after selection",
		Type: ProviderOpenAICodex, Status: StatusActive, Healthy: true,
	})
	resource := ProviderResource{
		ID: "rsrc_disable_after_selection", ProviderID: provider.ID, Name: "Disable after selection",
		ResourceType: ProviderResourceOpenAISubscription, Status: StatusActive, Healthy: true,
	}
	if err := store.db.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.db.Model(&ProviderResource{}).Where("id = ?", resource.ID).Update("status", StatusDisabled).Error; err != nil {
		t.Fatal(err)
	}

	if _, _, err := store.CheckProviderResourceCapacity(t.Context(), resource.ID); AsHTTPError(err).Code != "provider_resource_disabled" {
		t.Fatalf("disabled resource capacity error=%v", err)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
