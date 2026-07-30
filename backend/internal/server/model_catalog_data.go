package server

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type modelCatalogSeed struct {
	Name                   string            `yaml:"name"`
	Title                  string            `yaml:"title"`
	Description            string            `yaml:"description"`
	Category               string            `yaml:"category"`
	Family                 string            `yaml:"family"`
	Modality               string            `yaml:"modality"`
	ContextWindow          int64             `yaml:"context_window"`
	InputPriceUSDPer1M     float64           `yaml:"input_price_usd_per_1m"`
	CacheReadPriceUSDPer1M float64           `yaml:"cache_read_price_usd_per_1m"`
	OutputPriceUSDPer1M    float64           `yaml:"output_price_usd_per_1m"`
	EmbeddingPriceUSDPer1M float64           `yaml:"embedding_price_usd_per_1m"`
	InputModalities        []string          `yaml:"input_modalities"`
	OutputModalities       []string          `yaml:"output_modalities"`
	Capabilities           []string          `yaml:"capabilities"`
	SupportedParameters    []string          `yaml:"supported_parameters"`
	Metadata               map[string]string `yaml:"metadata"`
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
		models = append(models, buildCatalogModel(seed))
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

func buildCatalogModel(seed modelCatalogSeed) Model {
	name := strings.TrimSpace(seed.Name)
	category := strings.TrimSpace(seed.Category)
	modality := strings.TrimSpace(seed.Modality)
	if modality == "" {
		modality = inferCatalogModelModality(name)
	}
	capabilities := seed.Capabilities
	if len(capabilities) == 0 {
		capabilities = standardModelCapabilities(name, modality)
	}
	supportedParameters := seed.SupportedParameters
	if len(supportedParameters) == 0 {
		supportedParameters = standardModelSupportedParameters(capabilities)
	}
	family := strings.TrimSpace(seed.Family)
	if family == "" {
		family = inferCatalogModelFamily(name, category)
	}
	contextWindow := seed.ContextWindow
	if contextWindow == 0 {
		contextWindow = inferCatalogContextWindow(name, modality)
	}
	inputPrice := seed.InputPriceUSDPer1M
	subscriptionBilled := strings.EqualFold(strings.TrimSpace(seed.Metadata["billing_mode"]), "subscription")
	if inputPrice == 0 && !subscriptionBilled {
		inputPrice = catalogInputPrice(name, modality)
	}
	outputPrice := seed.OutputPriceUSDPer1M
	if outputPrice == 0 && !subscriptionBilled {
		outputPrice = catalogOutputPrice(name, modality)
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
		ID:                     name,
		Name:                   name,
		Category:               category,
		Family:                 family,
		Modality:               modality,
		ContextWindow:          contextWindow,
		InputModalities:        seed.InputModalities,
		OutputModalities:       seed.OutputModalities,
		Capabilities:           capabilities,
		SupportedParameters:    supportedParameters,
		InputPriceUSDPer1M:     inputPrice,
		CacheReadPriceUSDPer1M: seed.CacheReadPriceUSDPer1M,
		OutputPriceUSDPer1M:    outputPrice,
		EmbeddingPriceUSDPer1M: seed.EmbeddingPriceUSDPer1M,
		Status:                 StatusActive,
		Metadata:               metadata,
	}
}

func inferCatalogModelFamily(name string, category string) string {
	normalized := strings.ToLower(name)
	if strings.Contains(normalized, "/") {
		parts := strings.Split(normalized, "/")
		normalized = parts[len(parts)-1]
	}
	for _, sep := range []string{"-", ":", "."} {
		if index := strings.Index(normalized, sep); index > 0 {
			return normalized[:index]
		}
	}
	if category != "" {
		return category
	}
	return normalized
}

func inferCatalogModelModality(name string) string {
	normalized := strings.ToLower(name)
	switch {
	case strings.Contains(normalized, "embedding") || strings.Contains(normalized, "embed") || strings.Contains(normalized, "bge-"):
		return "embedding"
	case strings.Contains(normalized, "rerank"):
		return "rerank"
	case strings.Contains(normalized, "ocr"):
		return "ocr"
	case strings.Contains(normalized, "tts") || strings.Contains(normalized, "asr") || strings.Contains(normalized, "audio") || strings.Contains(normalized, "lyria"):
		return "audio"
	case strings.Contains(normalized, "veo") || strings.Contains(normalized, "sora") || strings.Contains(normalized, "i2v") || strings.Contains(normalized, "t2v") || strings.Contains(normalized, "seedance") || strings.Contains(normalized, "video"):
		return "video"
	case strings.Contains(normalized, "image") || strings.Contains(normalized, "imagen") || strings.Contains(normalized, "dall-e") || strings.Contains(normalized, "seedream") || strings.Contains(normalized, "cogview") || strings.Contains(normalized, "wanx") || strings.Contains(normalized, "gpt-image"):
		return "image"
	default:
		return "chat"
	}
}

func inferCatalogContextWindow(name string, modality string) int64 {
	if modality != "chat" {
		return 0
	}
	normalized := strings.ToLower(name)
	switch {
	case strings.Contains(normalized, "1m") || strings.Contains(normalized, "1048576"):
		return 1048576
	case strings.Contains(normalized, "256k"):
		return 256000
	case strings.Contains(normalized, "200k"):
		return 200000
	case strings.Contains(normalized, "128k"):
		return 128000
	case strings.Contains(normalized, "80k"):
		return 80000
	case strings.Contains(normalized, "32k"):
		return 32000
	case strings.Contains(normalized, "16k"):
		return 16000
	case strings.Contains(normalized, "8k"):
		return 8000
	case strings.Contains(normalized, "gpt-5") || strings.Contains(normalized, "gpt-4.1"):
		return 400000
	case strings.Contains(normalized, "gemini") || strings.Contains(normalized, "gemma"):
		return 1048576
	case strings.Contains(normalized, "claude"):
		return 200000
	default:
		return 128000
	}
}

func standardModelCapabilities(name string, modality string) []string {
	normalized := strings.ToLower(name)
	switch modality {
	case "embedding":
		return []string{"embedding"}
	case "rerank":
		return []string{"rerank"}
	case "ocr":
		return []string{"ocr", "vision"}
	case "image":
		if strings.Contains(normalized, "edit") {
			return []string{"image", "image_edit"}
		}
		return []string{"image"}
	case "video":
		return []string{"video"}
	case "audio":
		if strings.Contains(normalized, "asr") {
			return []string{"audio", "speech_to_text"}
		}
		if strings.Contains(normalized, "tts") {
			return []string{"audio", "text_to_speech"}
		}
		return []string{"audio"}
	default:
		capabilities := []string{"chat"}
		if strings.Contains(normalized, "vision") || strings.Contains(normalized, "vl") || strings.Contains(normalized, "multimodal") || strings.Contains(normalized, "image") || strings.Contains(normalized, "4v") || strings.Contains(normalized, "4.5v") || strings.Contains(normalized, "4.6v") {
			capabilities = append(capabilities, "vision")
		}
		if strings.Contains(normalized, "think") || strings.Contains(normalized, "reasoning") || strings.Contains(normalized, "reasoner") || strings.Contains(normalized, "r1") || strings.Contains(normalized, "o1") || strings.Contains(normalized, "o3") || strings.Contains(normalized, "z1") {
			capabilities = append(capabilities, "reasoning")
		}
		if strings.Contains(normalized, "coder") || strings.Contains(normalized, "codex") || strings.Contains(normalized, "code") {
			capabilities = append(capabilities, "code")
		}
		return capabilities
	}
}

func standardModelSupportedParameters(capabilities []string) []string {
	params := []string{}
	for _, capability := range capabilities {
		switch capability {
		case "chat":
			params = append(params, "temperature")
		case "reasoning":
			params = append(params, "reasoning")
		case "vision":
			params = append(params, "image_input")
		}
	}
	return params
}

func catalogInputPrice(name string, modality string) float64 {
	if modality != "chat" {
		return 0
	}
	normalized := strings.ToLower(name)
	switch {
	case strings.Contains(normalized, "gpt-5") || strings.Contains(normalized, "gpt-4") || strings.Contains(normalized, "claude-sonnet"):
		return 1
	case strings.Contains(normalized, "mini") || strings.Contains(normalized, "flash") || strings.Contains(normalized, "lite"):
		return 0.3
	default:
		return 0
	}
}

func catalogOutputPrice(name string, modality string) float64 {
	if modality != "chat" {
		return 0
	}
	input := catalogInputPrice(name, modality)
	if input == 0 {
		return 0
	}
	return input * 4
}
