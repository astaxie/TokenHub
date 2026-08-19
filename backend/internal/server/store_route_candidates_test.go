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
