package server

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type modelCatalogSeed struct {
	Name                      string               `yaml:"name"`
	Title                     string               `yaml:"title"`
	Description               string               `yaml:"description"`
	Category                  string               `yaml:"category"`
	Family                    string               `yaml:"family"`
	Modality                  string               `yaml:"modality"`
	ContextWindow             int64                `yaml:"context_window"`
	InputPriceUSDPer1M        float64              `yaml:"input_price_usd_per_1m"`
	CacheReadPriceUSDPer1M    float64              `yaml:"cache_read_price_usd_per_1m"`
	CacheWritePriceUSDPer1M   *float64             `yaml:"cache_write_price_usd_per_1m"`
	CacheWrite5mPriceUSDPer1M *float64             `yaml:"cache_write_5m_price_usd_per_1m"`
	CacheWrite1hPriceUSDPer1M *float64             `yaml:"cache_write_1h_price_usd_per_1m"`
	OutputPriceUSDPer1M       float64              `yaml:"output_price_usd_per_1m"`
	EmbeddingPriceUSDPer1M    float64              `yaml:"embedding_price_usd_per_1m"`
	PricingPeriods            []ModelPricingPeriod `yaml:"pricing_periods"`
	InputModalities           []string             `yaml:"input_modalities"`
	OutputModalities          []string             `yaml:"output_modalities"`
	Capabilities              []string             `yaml:"capabilities"`
	SupportedParameters       []string             `yaml:"supported_parameters"`
	Metadata                  map[string]string    `yaml:"metadata"`
}

type modelCatalogDocument struct {
	Version int                `yaml:"version"`
	Models  []modelCatalogSeed `yaml:"models"`
}

func defaultModelCatalog(catalogFile string) ([]Model, error) {
	seeds, err := loadModelCatalogSeeds(catalogFile)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	models := make([]Model, 0, len(seeds))
	for _, seed := range seeds {
		name := strings.TrimSpace(seed.Name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		model, err := buildCatalogModel(seed)
		if err != nil {
			return nil, fmt.Errorf("build catalog model %q: %w", name, err)
		}
		models = append(models, model)
	}
	return models, nil
}

func loadModelCatalogSeeds(catalogFile string) ([]modelCatalogSeed, error) {
	content, err := os.ReadFile(catalogFile)
	if err != nil {
		return nil, fmt.Errorf("read model catalog %s: %w", catalogFile, err)
	}

	var doc modelCatalogDocument
	if err := yaml.Unmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("parse model catalog %s: %w", catalogFile, err)
	}
	if len(doc.Models) == 0 {
		return nil, fmt.Errorf("model catalog %s has no models", catalogFile)
	}
	return doc.Models, nil
}

func buildCatalogModel(seed modelCatalogSeed) (Model, error) {
	name := strings.TrimSpace(seed.Name)
	category := strings.TrimSpace(seed.Category)
	if category == "" {
		return Model{}, fmt.Errorf("category is required")
	}
	modality := strings.TrimSpace(seed.Modality)
	if modality == "" {
		return Model{}, fmt.Errorf("modality is required")
	}
	capabilities := seed.Capabilities
	if len(capabilities) == 0 {
		return Model{}, fmt.Errorf("capabilities are required")
	}
	supportedParameters := seed.SupportedParameters
	if supportedParameters == nil {
		return Model{}, fmt.Errorf("supported_parameters is required")
	}
	family := strings.TrimSpace(seed.Family)
	if family == "" {
		return Model{}, fmt.Errorf("family is required")
	}
	contextWindow := seed.ContextWindow
	if modality == "chat" && contextWindow == 0 {
		return Model{}, fmt.Errorf("context_window is required for chat models")
	}
	inputPrice := seed.InputPriceUSDPer1M
	outputPrice := seed.OutputPriceUSDPer1M
	if len(seed.InputModalities) == 0 {
		return Model{}, fmt.Errorf("input_modalities are required")
	}
	if len(seed.OutputModalities) == 0 {
		return Model{}, fmt.Errorf("output_modalities are required")
	}
	metadata := map[string]string{}
	for key, value := range seed.Metadata {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}
	if strings.TrimSpace(metadata["source"]) == "" {
		metadata["source"] = "tokenhub-standard-catalog"
	}
	if title := strings.TrimSpace(seed.Title); title != "" {
		metadata["title"] = title
	}
	if description := strings.TrimSpace(seed.Description); description != "" {
		metadata["description"] = description
	}
	return Model{
		ID:                        name,
		Name:                      name,
		Category:                  category,
		Family:                    family,
		Modality:                  modality,
		ContextWindow:             contextWindow,
		InputModalities:           seed.InputModalities,
		OutputModalities:          seed.OutputModalities,
		Capabilities:              capabilities,
		SupportedParameters:       supportedParameters,
		InputPriceUSDPer1M:        inputPrice,
		CacheReadPriceUSDPer1M:    seed.CacheReadPriceUSDPer1M,
		CacheWritePriceUSDPer1M:   catalogOptionalPrice(seed.CacheWritePriceUSDPer1M),
		CacheWrite5mPriceUSDPer1M: catalogOptionalPrice(seed.CacheWrite5mPriceUSDPer1M),
		CacheWrite1hPriceUSDPer1M: catalogOptionalPrice(seed.CacheWrite1hPriceUSDPer1M),
		OutputPriceUSDPer1M:       outputPrice,
		CacheWritePriceConfiguration: CacheWritePriceConfiguration{
			CacheWritePriceConfigured:   seed.CacheWritePriceUSDPer1M != nil,
			CacheWrite5mPriceConfigured: seed.CacheWrite5mPriceUSDPer1M != nil,
			CacheWrite1hPriceConfigured: seed.CacheWrite1hPriceUSDPer1M != nil,
		},
		EmbeddingPriceUSDPer1M: seed.EmbeddingPriceUSDPer1M,
		PricingPeriods:         append([]ModelPricingPeriod(nil), seed.PricingPeriods...),
		Status:                 StatusActive,
		Metadata:               metadata,
	}, nil
}

func catalogOptionalPrice(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}
