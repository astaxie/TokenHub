package server

import "strings"

const modelPricingSourceBatchSize = 500

func (s *GormStore) AccessibleModels(key APIKey) []Model {
	// Membership holds the store lock only while evaluating authorization and
	// routes. Loading and validating pricing policies does not need that lock.
	return s.publicPricedModels(s.AccessibleModelMembership(key))
}

func accessibleModelMembership(store Store, key APIKey) []Model {
	if reader, ok := store.(interface{ AccessibleModelMembership(APIKey) []Model }); ok {
		return reader.AccessibleModelMembership(key)
	}
	return store.AccessibleModels(key)
}

func publicPricedModels(store Store, models []Model) []Model {
	if resolver, ok := store.(interface{ publicPricedModels([]Model) []Model }); ok {
		return resolver.publicPricedModels(models)
	}
	items := make([]Model, 0, len(models))
	for _, model := range models {
		resolved, err := resolvePlaygroundModelPricing(store, model)
		if err != nil {
			resolved.pricingUnavailable = true
		}
		items = append(items, publicModelPricingMetadata(resolved))
	}
	return items
}

func (s *GormStore) publicPricedModels(models []Model) []Model {
	ids := make([]string, 0)
	seen := make(map[string]bool)
	for _, model := range models {
		if strings.TrimSpace(model.Metadata[modelPricingModeKey]) != "provider" {
			continue
		}
		id, err := modelPricingSourceID(model)
		if err == nil && !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	sources := s.availableModelPricingSources(ids)
	items := make([]Model, 0, len(models))
	for _, model := range models {
		mode := strings.TrimSpace(model.Metadata[modelPricingModeKey])
		if mode != "" && mode != "custom" {
			id, err := modelPricingSourceID(model)
			if source, ok := sources[id]; mode == "provider" && err == nil && ok {
				model = applyModelPricingSource(model, source)
			} else {
				model.pricingUnavailable = true
			}
		}
		items = append(items, publicModelPricingMetadata(model))
	}
	return items
}

func (s *GormStore) availableModelPricingSources(ids []string) map[string]ProviderModel {
	available := make(map[string]ProviderModel)
	if len(ids) == 0 {
		return available
	}
	var sources []ProviderModel
	for start := 0; start < len(ids); start += modelPricingSourceBatchSize {
		var batch []ProviderModel
		if err := s.db.Where("id IN ?", ids[start:min(start+modelPricingSourceBatchSize, len(ids))]).Find(&batch).Error; err != nil {
			return nil
		}
		sources = append(sources, batch...)
	}
	providerIDs := make([]string, 0)
	seen := make(map[string]bool)
	for _, source := range sources {
		if !seen[source.ProviderID] {
			providerIDs = append(providerIDs, source.ProviderID)
			seen[source.ProviderID] = true
		}
	}
	providers := make(map[string]Provider)
	for start := 0; start < len(providerIDs); start += modelPricingSourceBatchSize {
		var batch []Provider
		if err := s.db.Select("id", "status").Where("id IN ?", providerIDs[start:min(start+modelPricingSourceBatchSize, len(providerIDs))]).Find(&batch).Error; err != nil {
			return nil
		}
		for _, provider := range batch {
			providers[provider.ID] = provider
		}
	}
	for _, source := range sources {
		provider, ok := providers[source.ProviderID]
		if ok && validateModelPricingSourceRecord(source) == nil && validateModelPricingSourceProvider(source, provider) == nil {
			available[source.ID] = source
		}
	}
	return available
}
