package server

// Reviewed per-model entries survive a stale public snapshot, while unreviewed
// models and new upstream IDs remain refreshable. This also preserves retirement
// annotations without removing third-party models that happen to share an ID.
func hasReviewedCatalogModels(entry ProviderCatalogEntry) bool {
	for _, model := range entry.Models {
		if model.Metadata["catalog_reviewed_at"] != "" {
			return true
		}
	}
	return false
}

func mergeReviewedCatalogModels(upstream, local ProviderCatalogEntry) ProviderCatalogEntry {
	reviewed := map[string]ProviderCatalogModel{}
	for _, model := range local.Models {
		if model.Metadata["catalog_reviewed_at"] != "" {
			reviewed[model.ID] = model
		}
	}
	if len(reviewed) == 0 {
		return upstream
	}
	models := make([]ProviderCatalogModel, 0, len(upstream.Models)+len(reviewed))
	for _, model := range upstream.Models {
		if replacement, ok := reviewed[model.ID]; ok {
			models = append(models, replacement)
			delete(reviewed, model.ID)
		} else {
			models = append(models, model)
		}
	}
	for _, model := range local.Models {
		if _, ok := reviewed[model.ID]; ok {
			models = append(models, model)
		}
	}
	upstream.Models = models
	upstream.ModelsCount = len(models)
	upstream.Categories, upstream.CategoryCounts = catalogCategorySummary(models)
	return upstream
}
