package server

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	store             Store
	adapterRegistry   *AdapterRegistry
	integrations      *IntegrationService
	codexSubscription *CodexSubscriptionAdapter
	providerCatalog   *providerCatalogService
	billing           *BillingService
	reconciliation    *ReconciliationService
	mux               *http.ServeMux
	config            Config
	metrics           *GatewayMetrics
	traceEmitter      TraceEmitter
	imageStorageDir   string
	imageRunner       func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error)
	imageContext      context.Context
	imageCancel       context.CancelFunc
	imageQueue        chan imageJobWork
	imageWorkerStart  sync.Once
	imageWorkerStop   sync.Once
	imageWorkerGroup  sync.WaitGroup
	imageAccountMu    sync.Mutex
	imageAccountSlots map[string]chan struct{}
	versions          *versionService
	requestBodyLimit  requestBodyLimitCache
}

func New(store Store) *Server {
	return NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
}
func NewWithConfig(store Store, config Config) *Server {
	if strings.TrimSpace(config.ImageStorageDir) == "" {
		config.ImageStorageDir = defaultImageStorageDir()
	}
	if config.ImageWorkerConcurrency <= 0 {
		config.ImageWorkerConcurrency = 2
	}
	if config.ImageQueueCapacity <= 0 {
		config.ImageQueueCapacity = 64
	}
	if config.ImageJobTimeoutSeconds <= 0 {
		config.ImageJobTimeoutSeconds = 300
	}
	if config.ImageCapabilityRetrySecs <= 0 {
		config.ImageCapabilityRetrySecs = 86400
	}
	imageContext, imageCancel := context.WithCancel(context.Background())
	client, streamClient, streamIdleTimeout := newUpstreamClients(config)
	openai := OpenAICompatibleAdapter{Client: client, StreamClient: streamClient, StreamIdleTimeout: streamIdleTimeout}
	codexSubscription := &CodexSubscriptionAdapter{
		Client:             &http.Client{},
		StreamIdleTimeout:  streamIdleTimeout,
		RefreshCredentials: store.RefreshProviderResourceCredentials,
	}
	adapters := map[string]ProviderAdapter{
		ProviderMock:             MockAdapter{},
		ProviderOpenAI:           openai,
		ProviderOpenAICompatible: openai,
		"deepseek":               openai,
		"qwen":                   openai,
		"local":                  openai,
		ProviderAzureOpenAI:      AzureOpenAIAdapter{Client: client, StreamClient: streamClient, StreamIdleTimeout: streamIdleTimeout},
		ProviderAnthropic:        AnthropicAdapter{Client: client, StreamClient: streamClient, StreamIdleTimeout: streamIdleTimeout},
		ProviderGemini:           GeminiAdapter{Client: client, StreamClient: streamClient, StreamIdleTimeout: streamIdleTimeout},
	}
	registry := NewAdapterRegistry()
	registry.Register(ProviderMock, adapters[ProviderMock], AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityEmbeddings)
	registry.Register(ProviderOpenAI, adapters[ProviderOpenAI], AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityEmbeddings, AdapterCapabilityProbe, AdapterCapabilityImageGenerate)
	registry.Register(ProviderOpenAICompatible, adapters[ProviderOpenAICompatible], AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityEmbeddings, AdapterCapabilityProbe)
	registry.Register(ProviderOpenAICodex, codexSubscription, AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityModels, AdapterCapabilityProbe, AdapterCapabilityQuota, AdapterCapabilityOAuth, AdapterCapabilityAffinity, AdapterCapabilityCompact, AdapterCapabilityImageGenerate)
	registry.Register(ProviderAzureOpenAI, adapters[ProviderAzureOpenAI], AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityEmbeddings, AdapterCapabilityProbe)
	registry.Register(ProviderAnthropic, adapters[ProviderAnthropic], AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityProbe)
	registry.Register(ProviderGemini, adapters[ProviderGemini], AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityEmbeddings, AdapterCapabilityProbe)
	for _, adapterType := range []string{"deepseek", "qwen", "local"} {
		registry.Register(adapterType, adapters[adapterType], AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityEmbeddings, AdapterCapabilityProbe)
	}
	s := &Server{
		store:             store,
		adapterRegistry:   registry,
		integrations:      NewIntegrationService(store, registry),
		codexSubscription: codexSubscription,
		providerCatalog:   newProviderCatalogService(store, config.ProviderCatalogFile),
		billing:           newBillingService(store),
		reconciliation:    newReconciliationService(store),
		mux:               http.NewServeMux(),
		config:            config,
		imageStorageDir:   config.ImageStorageDir,
		imageContext:      imageContext,
		imageCancel:       imageCancel,
		imageQueue:        make(chan imageJobWork, config.ImageQueueCapacity),
		imageAccountSlots: make(map[string]chan struct{}),
		versions:          newVersionService(config),
	}
	if jobs, err := store.FailUnfinishedImageJobs("image_worker_restarted", "Image generation stopped because the server restarted"); err != nil {
		log.Printf("[tokenhub] failed to mark unfinished image jobs after startup: %v", err)
	} else if len(jobs) > 0 {
		log.Printf("[tokenhub] marked %d unfinished image jobs as failed after startup", len(jobs))
	}
	backfillProviderModelsFromRoutes(store)
	backfillExternalModelRolesFromRoutes(store)
	if config.MetricsEnabled {
		s.metrics = NewGatewayMetrics(config.MetricsProjectLabel)
		// Assert against the narrow MetricsSink interface rather than *GormStore, and
		// report failure loudly: silently collecting nothing would be worse than not
		// offering the endpoint at all.
		if sink, ok := store.(MetricsSink); ok {
			sink.SetGatewayMetrics(s.metrics)
		} else {
			log.Printf("[tokenhub] store does not implement MetricsSink; gateway request metrics will stay empty")
		}
	}
	s.installTraceEmitter(config)
	s.routes()
	return s
}
func (s *Server) Handler() http.Handler {
	return s.cors(s.withRequestBodyLimit(s.mux))
}
func (s *Server) handleAdminProviderAdapters(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.adapterRegistry.List()})
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tokenhub-backend"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "service": "tokenhub-backend"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tokenhub-backend"})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	models := s.store.AccessibleModels(key)
	data := make([]modelListItem, 0, len(models))
	for _, model := range models {
		data = append(data, buildModelListItem(model))
	}
	payload := map[string]any{
		"object":   "list",
		"data":     data,
		"models":   []any{},
		"has_more": false,
	}
	if len(data) > 0 {
		payload["first_id"] = data[0].ID
		payload["last_id"] = data[len(data)-1].ID
	}
	writeJSON(w, http.StatusOK, payload)
}

func (s *Server) handleModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	modelID, err := modelIDFromPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, model := range s.store.AccessibleModels(key) {
		if model.Name == modelID || model.ID == modelID {
			writeJSON(w, http.StatusOK, buildModelListItem(model))
			return
		}
	}
	writeError(w, r, NewHTTPError(404, "model_not_found", "Model not found"))
}

func modelIDFromPath(r *http.Request) (string, error) {
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/v1/models/")
	escaped = strings.Trim(escaped, "/")
	if escaped == "" {
		return "", NewHTTPError(404, "model_not_found", "Model not found")
	}
	modelID, err := url.PathUnescape(escaped)
	if err != nil || strings.TrimSpace(modelID) == "" {
		return "", NewHTTPError(400, "invalid_model", "model path parameter is invalid")
	}
	return strings.TrimSpace(modelID), nil
}

type modelListItem struct {
	ID                   string `json:"id"`
	Created              int64  `json:"created"`
	Object               string `json:"object"`
	Type                 string `json:"type"`
	OwnedBy              string `json:"owned_by,omitempty"`
	InputTokenPricePerM  int64  `json:"input_token_price_per_m"`
	OutputTokenPricePerM int64  `json:"output_token_price_per_m"`
	Title                string `json:"title"`
	DisplayName          string `json:"display_name"`
	Description          string `json:"description"`
	ContextSize          int64  `json:"context_size"`
	CreatedAt            string `json:"created_at"`
	MaxInputTokens       int64  `json:"max_input_tokens"`
	MaxTokens            int64  `json:"max_tokens"`
}

func buildModelListItem(model Model) modelListItem {
	inputPrice := model.InputPriceUSDPer1M
	if inputPrice == 0 && model.EmbeddingPriceUSDPer1M > 0 {
		inputPrice = model.EmbeddingPriceUSDPer1M
	}
	return modelListItem{
		ID:                   model.Name,
		Created:              modelCreatedUnix(model),
		Object:               "model",
		Type:                 "model",
		OwnedBy:              "tokenhub",
		InputTokenPricePerM:  modelTokenPricePerM(inputPrice),
		OutputTokenPricePerM: modelTokenPricePerM(model.OutputPriceUSDPer1M),
		Title:                modelTitle(model),
		DisplayName:          modelTitle(model),
		Description:          modelDescription(model),
		ContextSize:          model.ContextWindow,
		CreatedAt:            modelCreatedAt(model),
		MaxInputTokens:       model.ContextWindow,
		MaxTokens:            modelMaxOutputTokens(model),
	}
}

func modelCreatedUnix(model Model) int64 {
	if model.CreatedAt.IsZero() {
		return 0
	}
	return model.CreatedAt.Unix()
}

func modelCreatedAt(model Model) string {
	if model.CreatedAt.IsZero() {
		return time.Unix(0, 0).UTC().Format(time.RFC3339)
	}
	return model.CreatedAt.UTC().Format(time.RFC3339)
}

func modelMaxOutputTokens(model Model) int64 {
	for _, key := range []string{"max_output_tokens", "max_tokens"} {
		if value := strings.TrimSpace(model.Metadata[key]); value != "" {
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err == nil && parsed >= 0 {
				return parsed
			}
		}
	}
	return 0
}

func modelTokenPricePerM(priceUSDPer1M float64) int64 {
	if priceUSDPer1M <= 0 {
		return 0
	}
	// JieKou-compatible model listings use integer price units; 1 USD/1M tokens is 10000.
	return int64(math.Round(priceUSDPer1M * 10000))
}

func modelTitle(model Model) string {
	if value := strings.TrimSpace(model.Metadata["title"]); value != "" {
		return value
	}
	return model.Name
}

func modelDescription(model Model) string {
	if value := strings.TrimSpace(model.Metadata["description"]); value != "" {
		return value
	}
	modality := firstNonEmpty(model.Modality, "chat")
	family := firstNonEmpty(model.Family, model.Category, "custom")
	return fmt.Sprintf("TokenHub %s model in the %s family.", modality, family)
}
