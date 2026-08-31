package server

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// These categories have compatible Provider inventory but no reviewed public
// model template yet. Keeping the exceptions explicit turns today's known gaps
// into a ratchet: a newly inferred category cannot silently become Provider-only,
// and an exception must be removed once its first standard template is added.
var providerOnlyModelCategoryExceptions = map[string]string{
	"baichuan":     "Provider aliases do not yet have a reviewed canonical template and client-facing price",
	"microsoft":    "Phi aliases span several Providers and do not yet have a reviewed canonical template and client-facing price",
	"paddlepaddle": "PaddlePaddle aliases do not yet have a reviewed canonical template and client-facing price",
}

// Only list first-party catalogs whose compatible models are intentionally
// mirrored in the standard catalog. Aggregators are excluded because aliases,
// previews, and historical versions are valid Provider inventory without being
// public model templates.
var fullyMirroredProviderCatalogs = map[string]string{
	"stepfun":        "stepfun",
	"stepfun-global": "stepfun",
	"stepfun-plan":   "stepfun",
	"stepfun-ai":     "stepfun",
}

func TestTrackedModelCatalogsDoNotDrift(t *testing.T) {
	standardModels, err := defaultModelCatalog("../../../data/model-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	providerEntries, err := loadLocalProviderCatalog("../../../data/provider-catalog.json")
	if err != nil {
		t.Fatal(err)
	}

	report := findModelCatalogDrift(standardModels, providerEntries)
	if len(report.missingCategories) > 0 || len(report.staleCategoryExceptions) > 0 ||
		len(report.missingMirroredModels) > 0 || len(report.missingProviderModels) > 0 ||
		len(report.mismatchedMirroredModels) > 0 {
		t.Fatalf(
			"model catalogs drifted:\nmissing standard categories: %v\nstale category exceptions: %v\nmissing standard models: %v\nmissing Provider models: %v\nmismatched mirrored models: %v",
			report.missingCategories,
			report.staleCategoryExceptions,
			report.missingMirroredModels,
			report.missingProviderModels,
			report.mismatchedMirroredModels,
		)
	}
}

func TestDefaultModelCatalogRequiresExplicitModelMetadata(t *testing.T) {
	catalogFile := filepath.Join(t.TempDir(), "model-catalog.yaml")
	if err := os.WriteFile(catalogFile, []byte(`
version: 1
models:
  - name: "opaque-chat"
    category: "custom"
    family: "opaque"
    modality: "chat"
    context_window: 128000
    input_modalities: ["text"]
    output_modalities: ["text"]
    capabilities: ["chat"]
`), 0o600); err != nil {
		t.Fatalf("write catalog fixture: %v", err)
	}

	_, err := defaultModelCatalog(catalogFile)
	if err == nil || !strings.Contains(err.Error(), "supported_parameters is required") {
		t.Fatalf("default model catalog error = %v, want supported_parameters requirement", err)
	}
}

func TestFindModelCatalogDrift(t *testing.T) {
	standard := []Model{
		{
			Name: "step-1", Category: "stepfun", Modality: "chat",
			InputModalities: []string{"text"}, OutputModalities: []string{"text"}, Capabilities: []string{"chat"},
		},
		{
			Name: "step-orphan", Category: "stepfun", Modality: "chat",
			InputModalities: []string{"text"}, OutputModalities: []string{"text"}, Capabilities: []string{"chat"},
		},
	}
	providers := []ProviderCatalogEntry{
		{
			ID: "stepfun",
			Models: []ProviderCatalogModel{
				{ID: "step-1", Name: "Step 1", Category: "stepfun", Type: "chat", InputModalities: []string{"text"}, OutputModalities: []string{"text"}, Capabilities: []string{"chat", "reasoning"}},
				{ID: "step-2", Name: "Step 2", Category: "stepfun", Type: "chat", OutputModalities: []string{"text"}},
				{ID: "step-tts", Name: "Step TTS", Category: "stepfun", Type: "chat", OutputModalities: []string{"audio"}},
			},
		},
		{
			ID: "new-provider",
			Models: []ProviderCatalogModel{
				{ID: "new-chat", Name: "New Chat", Category: "new-category", Type: "chat", OutputModalities: []string{"text"}},
				{ID: "new-video", Name: "New Video", Category: "video-only", Type: "chat", OutputModalities: []string{"video"}},
			},
		},
	}

	report := findModelCatalogDriftWithPolicy(
		standard,
		providers,
		map[string]string{},
		map[string]string{"stepfun": "stepfun"},
		map[string]bool{"stepfun": true, "new-category": true, "video-only": true},
	)
	if !slices.Equal(report.missingCategories, []string{"new-category"}) {
		t.Fatalf("unexpected missing categories: %v", report.missingCategories)
	}
	if !slices.Equal(report.missingMirroredModels, []string{"stepfun:step-2"}) {
		t.Fatalf("unexpected missing mirrored models: %v", report.missingMirroredModels)
	}
	if !slices.Equal(report.missingProviderModels, []string{"stepfun:step-orphan"}) {
		t.Fatalf("unexpected missing Provider models: %v", report.missingProviderModels)
	}
	if !slices.Equal(report.mismatchedMirroredModels, []string{"stepfun:step-1:reasoning"}) {
		t.Fatalf("unexpected mismatched mirrored models: %v", report.mismatchedMirroredModels)
	}
	if len(report.staleCategoryExceptions) != 0 {
		t.Fatalf("unexpected stale exceptions: %v", report.staleCategoryExceptions)
	}

	excepted := findModelCatalogDriftWithPolicy(
		standard,
		providers,
		map[string]string{"new-category": "review pending"},
		map[string]string{"stepfun": "stepfun"},
		map[string]bool{"stepfun": true, "new-category": true, "video-only": true},
	)
	if len(excepted.missingCategories) != 0 || len(excepted.staleCategoryExceptions) != 0 {
		t.Fatalf("documented category exception was not honored: %+v", excepted)
	}

	standardWithNewCategory := append(slices.Clone(standard), Model{
		Name: "new-chat", Category: "new-category", Modality: "chat", OutputModalities: []string{"text"},
	})
	stale := findModelCatalogDriftWithPolicy(
		standardWithNewCategory,
		providers,
		map[string]string{"new-category": "review pending"},
		map[string]string{},
		map[string]bool{"stepfun": true, "new-category": true, "video-only": true},
	)
	if !slices.Equal(stale.staleCategoryExceptions, []string{"new-category"}) {
		t.Fatalf("expected stale category exception, got %v", stale.staleCategoryExceptions)
	}
}

type modelCatalogDriftReport struct {
	missingCategories        []string
	staleCategoryExceptions  []string
	missingMirroredModels    []string
	missingProviderModels    []string
	mismatchedMirroredModels []string
}

func findModelCatalogDrift(standardModels []Model, providerEntries []ProviderCatalogEntry) modelCatalogDriftReport {
	return findModelCatalogDriftWithPolicy(
		standardModels,
		providerEntries,
		providerOnlyModelCategoryExceptions,
		fullyMirroredProviderCatalogs,
		standardModelCategorySet(),
	)
}

func findModelCatalogDriftWithPolicy(
	standardModels []Model,
	providerEntries []ProviderCatalogEntry,
	categoryExceptions map[string]string,
	mirroredProviders map[string]string,
	knownCategories map[string]bool,
) modelCatalogDriftReport {
	standardCategories := map[string]bool{}
	standardByName := map[string]Model{}
	standardModalities := map[string]bool{}
	standardOutputModalities := map[string]bool{}
	for _, model := range standardModels {
		standardCategories[normalizeKnownModelCategory(model.Category, knownCategories)] = true
		standardByName[canonicalModelName(model.Name, model.Name)] = model
		standardModalities[strings.ToLower(strings.TrimSpace(model.Modality))] = true
		for _, modality := range model.OutputModalities {
			standardOutputModalities[strings.ToLower(strings.TrimSpace(modality))] = true
		}
	}

	providerCategories := map[string]bool{}
	entriesByID := make(map[string]ProviderCatalogEntry, len(providerEntries))
	for _, entry := range providerEntries {
		entriesByID[entry.ID] = entry
		for _, model := range entry.Models {
			if !providerModelFitsStandardCatalog(model, standardModalities, standardOutputModalities) {
				continue
			}
			category := normalizeKnownModelCategory(model.Category, knownCategories)
			if category != "custom" && knownCategories[category] {
				providerCategories[category] = true
			}
		}
	}

	report := modelCatalogDriftReport{}
	for category := range providerCategories {
		if standardCategories[category] || strings.TrimSpace(categoryExceptions[category]) != "" {
			continue
		}
		report.missingCategories = append(report.missingCategories, category)
	}
	for category, reason := range categoryExceptions {
		if strings.TrimSpace(reason) == "" || !providerCategories[category] || standardCategories[category] {
			report.staleCategoryExceptions = append(report.staleCategoryExceptions, category)
		}
	}
	mirroredCategories := map[string]bool{}
	mirroredNamesByCategory := map[string]map[string]bool{}
	for providerID, category := range mirroredProviders {
		mirroredCategories[category] = true
		if mirroredNamesByCategory[category] == nil {
			mirroredNamesByCategory[category] = map[string]bool{}
		}
		entry, ok := entriesByID[providerID]
		if !ok {
			report.missingMirroredModels = append(report.missingMirroredModels, providerID+":<provider-missing>")
			continue
		}
		for _, model := range entry.Models {
			if normalizeKnownModelCategory(model.Category, knownCategories) != category ||
				!providerModelFitsStandardCatalog(model, standardModalities, standardOutputModalities) {
				continue
			}
			canonicalName := canonicalModelName(model.CanonicalName, model.ID)
			if canonicalName == "" {
				canonicalName = canonicalModelName(model.ID, model.Name)
			}
			mirroredNamesByCategory[category][canonicalName] = true
			standardModel, ok := standardByName[canonicalName]
			if !ok {
				report.missingMirroredModels = append(report.missingMirroredModels, providerID+":"+model.ID)
				continue
			}
			for _, mismatch := range mirroredModelMismatches(standardModel, model) {
				report.mismatchedMirroredModels = append(report.mismatchedMirroredModels, providerID+":"+model.ID+":"+mismatch)
			}
		}
	}
	for _, model := range standardModels {
		category := normalizeKnownModelCategory(model.Category, knownCategories)
		if !mirroredCategories[category] {
			continue
		}
		canonicalName := canonicalModelName(model.Name, model.Name)
		if canonicalName != "" && !mirroredNamesByCategory[category][canonicalName] {
			report.missingProviderModels = append(report.missingProviderModels, category+":"+model.Name)
		}
	}

	sort.Strings(report.missingCategories)
	sort.Strings(report.staleCategoryExceptions)
	sort.Strings(report.missingMirroredModels)
	sort.Strings(report.missingProviderModels)
	sort.Strings(report.mismatchedMirroredModels)
	return report
}

func mirroredModelMismatches(standard Model, provider ProviderCatalogModel) []string {
	mismatches := []string{}
	providerModality := normalizeModelModality(provider.Type)
	if providerModality != strings.ToLower(strings.TrimSpace(standard.Modality)) {
		mismatches = append(mismatches, "modality")
	}
	if !slices.Equal(normalizedModelValues(standard.InputModalities), normalizedModelValues(provider.InputModalities)) {
		mismatches = append(mismatches, "input-modalities")
	}
	providerOutputs := provider.OutputModalities
	if len(providerOutputs) == 0 && normalizeModelModality(provider.Type) == "chat" {
		providerOutputs = []string{"text"}
	}
	if !slices.Equal(normalizedModelValues(standard.OutputModalities), normalizedModelValues(providerOutputs)) {
		mismatches = append(mismatches, "output-modalities")
	}
	if slices.Contains(standard.Capabilities, "reasoning") != slices.Contains(provider.Capabilities, "reasoning") {
		mismatches = append(mismatches, "reasoning")
	}
	if slices.Contains(standard.Capabilities, "tools") != slices.Contains(provider.Capabilities, "tool_call") {
		mismatches = append(mismatches, "tools")
	}
	if normalizedCSV(standard.Metadata["reasoning_effort_options"]) != normalizedCSV(provider.Metadata["reasoning_effort_options"]) {
		mismatches = append(mismatches, "reasoning-effort-options")
	}
	return mismatches
}

func normalizedModelValues(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.ToLower(strings.TrimSpace(value)); value != "" && !slices.Contains(normalized, value) {
			normalized = append(normalized, value)
		}
	}
	sort.Strings(normalized)
	return normalized
}

func normalizedCSV(value string) string {
	return strings.Join(normalizedModelValues(strings.Split(value, ",")), ",")
}

func normalizeKnownModelCategory(category string, knownCategories map[string]bool) string {
	category = strings.ToLower(strings.TrimSpace(category))
	if knownCategories[category] {
		return category
	}
	return inferModelCategory(category, "")
}

func providerModelFitsStandardCatalog(
	model ProviderCatalogModel,
	standardModalities map[string]bool,
	standardOutputModalities map[string]bool,
) bool {
	modality := normalizeModelModality(model.Type)
	if !standardModalities[modality] {
		return false
	}
	outputs := model.OutputModalities
	if len(outputs) == 0 && normalizeModelModality(model.Type) == "chat" {
		outputs = []string{"text"}
	}
	for _, output := range outputs {
		if standardOutputModalities[strings.ToLower(strings.TrimSpace(output))] {
			return true
		}
	}
	return false
}
