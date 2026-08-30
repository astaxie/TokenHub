package server

import (
	"context"
	"crypto/x509"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"tokenhub/backend/internal/admin"
	"tokenhub/backend/internal/billing"
	billingadapters "tokenhub/backend/internal/billing/adapters"
	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

type Server struct {
	store                   Store
	pluginRegistry          *pluginmeta.Registry
	gatewayChain            *pluginmeta.GatewayChainRegistry
	gatewayHooks            *pluginmeta.GatewayHookRunner
	adminUI                 *pluginmeta.AdminUIRegistry
	pluginActions           *pluginmeta.ActionBroker
	pluginBackgroundJobs    *pluginmeta.BackgroundJobBroker
	pluginBackgroundRunner  *pluginmeta.BackgroundJobRunner
	adapterRegistry         *AdapterRegistry
	integrations            *IntegrationService
	providerCatalog         *providerCatalogService
	billing                 *billing.Service
	billingAdmin            *admin.BillingHandler
	billingAvailable        bool
	reconciliation          *ReconciliationService
	credentialRefresh       *ProviderCredentialRefreshService
	payloadRetention        *requestPayloadRetentionService
	mux                     *http.ServeMux
	publicGatewayOperations map[gatewayOperation]bool
	config                  Config
	metrics                 *GatewayMetrics
	traceEmitter            TraceEmitter
	imageStorageDir         string
	imageRunner             func(context.Context, RouteSelection, ImageJob) ([]byte, string, Usage, error)
	imageContext            context.Context
	imageCancel             context.CancelFunc
	imageQueue              chan imageJobWork
	imageWorkerStart        sync.Once
	imageWorkerStop         sync.Once
	imageWorkerGroup        sync.WaitGroup
	imageAccountMu          sync.Mutex
	imageAccountSlots       map[string]chan struct{}
	responseContext         context.Context
	responseCancel          context.CancelFunc
	responseWorkerStart     sync.Once
	responseWorkerStop      sync.Once
	responseWorkerGroup     sync.WaitGroup
	responseInstanceID      string
	stopHeartbeat           func()
	versions                *versionService
	guardrailEngine         *guardrails.Engine
	upstreamClient          *http.Client
	pluginInstallClient     *http.Client
	pluginMarketplaceClient *http.Client
	syntheticDNSPolicy      *providerSyntheticDNSPolicy
	providerProxyPolicy     *providerProxyPolicy
	syntheticDNSSetting     sync.Mutex
	// smtpRootCAs is a test seam for implicit-TLS SMTP delivery. When nil the
	// production dial validates the server certificate against the platform
	// roots; tests inject the in-process fake server's certificate here.
	smtpRootCAs *x509.CertPool
}

func New(store Store) *Server {
	return NewWithConfig(store, Config{AdminToken: "dev_admin_token"})
}

func NewWithConfig(store Store, config Config) *Server {
	return newWithConfig(store, config, billingDependenciesFromStore(store))
}

// BillingDependencies are composition-only billing capabilities. They allow a
// Store decorator to preserve billing behavior without widening Store itself.
type BillingDependencies struct {
	Repository           billing.Repository
	ReconciliationReader ReconciliationBillingReader
}

// NewWithConfigAndBillingDependencies constructs a server with explicitly
// supplied billing dependencies. It is intended for Store decorators that do
// not expose the private composition hooks implemented by GormStore.
func NewWithConfigAndBillingDependencies(store Store, config Config, dependencies BillingDependencies) *Server {
	return newWithConfig(store, config, dependencies)
}

func newWithConfig(store Store, config Config, billingDependencies BillingDependencies) *Server {
	billingAvailable := billingDependencies.Repository != nil && billingDependencies.ReconciliationReader != nil
	billingDependencies = normalizeBillingDependencies(billingDependencies)
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
	if config.ResponseWorkerConcurrency <= 0 {
		config.ResponseWorkerConcurrency = 2
	}
	if config.ResponsePollIntervalMillis <= 0 {
		config.ResponsePollIntervalMillis = 250
	}
	if config.ResponseJobTimeoutSeconds <= 0 {
		config.ResponseJobTimeoutSeconds = 300
	}
	if config.ResponseLeaseTTLSeconds <= 0 {
		config.ResponseLeaseTTLSeconds = 30
	}
	if config.ResponseResultTTLSeconds <= 0 {
		config.ResponseResultTTLSeconds = 3600
	}
	if config.ResponseMaxQueuedJobs <= 0 {
		config.ResponseMaxQueuedJobs = 1000
	}
	if config.MaxJSONRequestBytes <= 0 {
		config.MaxJSONRequestBytes = defaultMaxJSONRequestBytes
	}
	if config.MaxMultimodalRequestBytes <= 0 {
		config.MaxMultimodalRequestBytes = defaultMaxMultimodalRequestBytes
	}
	imageContext, imageCancel := context.WithCancel(context.Background())
	responseContext, responseCancel := context.WithCancel(context.Background())
	syntheticDNSPolicy := newProviderSyntheticDNSPolicy(store)
	providerProxyPolicy := newProviderProxyPolicy(store)
	client, streamClient, streamIdleTimeout := newUpstreamClientsWithPolicies(config, syntheticDNSPolicy, providerProxyPolicy)
	catalogClient := &http.Client{
		Transport:     client.Transport,
		CheckRedirect: strictProviderUpstreamRedirect,
		Timeout:       providerCatalogUpstreamTimeout,
	}
	if gormStore, ok := store.(*GormStore); ok {
		gormStore.providerProxyPolicy = providerProxyPolicy
	}
	providerRuntime := newBuiltinProviderRuntime(builtinProviderRuntimeDependencies{
		Store:               store,
		Client:              client,
		StreamClient:        streamClient,
		StreamIdleTimeout:   streamIdleTimeout,
		SyntheticDNSPolicy:  syntheticDNSPolicy,
		ProviderProxyPolicy: providerProxyPolicy,
	})
	pluginBootstrap, err := bootstrapServerPlugins(store, config, providerRuntime.adapters)
	if err != nil {
		panic(err)
	}
	providerCatalog := newProviderCatalogService(store, config.ProviderCatalogFile, catalogClient)
	providerCatalog.UsePluginCatalogTypes(pluginBootstrap.adapterRegistry)
	s := &Server{
		store:                   store,
		pluginRegistry:          pluginBootstrap.pluginRegistry,
		gatewayChain:            pluginBootstrap.gatewayChain,
		gatewayHooks:            pluginBootstrap.gatewayHooks,
		adminUI:                 pluginBootstrap.adminUI,
		pluginActions:           pluginBootstrap.pluginActions,
		pluginBackgroundJobs:    pluginBootstrap.pluginBackgroundJobs,
		pluginBackgroundRunner:  pluginBootstrap.pluginBackgroundRunner,
		adapterRegistry:         pluginBootstrap.adapterRegistry,
		integrations:            NewIntegrationService(store, pluginBootstrap.adapterRegistry, client),
		providerCatalog:         providerCatalog,
		billing:                 billing.NewService(billingDependencies.Repository, billingadapters.NewRegistry(&http.Client{Timeout: 30 * time.Second})),
		billingAvailable:        billingAvailable,
		reconciliation:          newReconciliationService(store, billingDependencies.ReconciliationReader),
		credentialRefresh:       newProviderCredentialRefreshService(store),
		payloadRetention:        newRequestPayloadRetentionService(store),
		mux:                     http.NewServeMux(),
		publicGatewayOperations: make(map[gatewayOperation]bool),
		config:                  config,
		imageStorageDir:         config.ImageStorageDir,
		imageContext:            imageContext,
		imageCancel:             imageCancel,
		imageQueue:              make(chan imageJobWork, config.ImageQueueCapacity),
		imageAccountSlots:       make(map[string]chan struct{}),
		responseContext:         responseContext,
		responseCancel:          responseCancel,
		responseInstanceID:      NewID("response-worker"),
		versions:                newVersionService(config),
		guardrailEngine: guardrails.NewEngine(guardrails.NewQwenDetector(guardrails.QwenDetectorConfig{
			URL:     config.GuardrailModelURL,
			APIKey:  config.GuardrailModelAPIKey,
			Model:   config.GuardrailModelName,
			Timeout: time.Duration(config.GuardrailModelTimeoutSeconds) * time.Second,
		})),
		upstreamClient: client,
		pluginInstallClient: &http.Client{
			Transport:     client.Transport,
			CheckRedirect: strictProviderUpstreamRedirect,
			Timeout:       60 * time.Second,
		},
		pluginMarketplaceClient: &http.Client{
			Transport:     client.Transport,
			CheckRedirect: strictProviderUpstreamRedirect,
			Timeout:       30 * time.Second,
		},
		syntheticDNSPolicy:  syntheticDNSPolicy,
		providerProxyPolicy: providerProxyPolicy,
	}
	s.installServerPluginHandlers()
	s.billingAdmin = admin.NewBillingHandler(billingDependencies.Repository, s.billing, admin.BillingTransport{
		DecodeJSON:         s.decodeJSON,
		DecodeJSONOptional: s.decodeJSONOptional,
		IsPayloadTooLarge:  isPayloadTooLarge,
		NewError: func(status int, code, message string) error {
			return NewHTTPError(status, code, message)
		},
		MapError:   func(err error) error { return billingHTTPError(err) },
		WriteJSON:  writeJSON,
		WriteError: writeError,
		Audit: func(r *http.Request, actor admin.BillingActor, event admin.BillingAudit) {
			s.recordAdminAuditWithStatus(r, AdminUser{ID: actor.ID, Name: actor.Name, Role: actor.Role},
				event.Action, "billing_connector", event.ResourceID, event.Status, event.Message, event.Before, event.After)
		},
	})
	if jobs, err := store.FailUnfinishedImageJobs("image_worker_restarted", "Image generation stopped because the server restarted"); err != nil {
		log.Printf("[tokenhub] failed to mark unfinished image jobs after startup: %v", err)
	} else if len(jobs) > 0 {
		log.Printf("[tokenhub] marked %d unfinished image jobs as failed after startup", len(jobs))
	}
	if gormStore, ok := store.(*GormStore); ok {
		s.stopHeartbeat = gormStore.StartInstanceHeartbeat(config.AppVersion)
	}
	backfillProviderModelsFromRoutes(store)
	backfillExternalModelRolesFromRoutes(store)
	backfillCodexImageRoutes(store)
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
	if config.ResponseWorkerStartupEnabled {
		// Every replica must poll the durable queue even when it was empty at startup.
		// Otherwise a replica that never handled a submission cannot take over after
		// the submitting replica fails.
		s.startResponseWorkers()
	}
	return s
}
func (s *Server) Handler() http.Handler {
	return s.cors(s.mux)
}
func (s *Server) handleAdminProviderAdapters(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.adapterRegistry.List()})
}

func (s *Server) handleAdminPlugins(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	plugins, err := s.adminPluginDescriptors()
	if err != nil {
		writeError(w, r, NewHTTPError(http.StatusInternalServerError, "plugin_discovery_failed", "Plugin packages could not be inspected"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": plugins})
}

func (s *Server) handleAdminPluginChain(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.gatewayChain.Plan()})
}

func (s *Server) handleAdminPluginUIManifest(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": s.adminUI.List()})
}

func (s *Server) handleAdminPluginActions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": adminPluginActionDescriptors(s.pluginActions.List())})
}

func (s *Server) handleAdminPluginBackgroundJobs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r, "providers", r.Method); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": s.pluginBackgroundJobs.List(),
		"runs": sanitizePluginBackgroundJobRunRecords(s.pluginBackgroundRunner.LastRuns()),
	})
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tokenhub-backend"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"status": "unavailable", "service": "tokenhub-backend"})
		return
	}
	// Readiness reflects the database evolution state: a dirty or unverifiable
	// migration ledger and incomplete blocking backfills keep the instance out
	// of rotation; pending online backfills do not.
	if evolution, ok := s.store.(interface {
		DatabaseEvolutionStatus(ctx context.Context) DatabaseEvolutionStatus
	}); ok {
		if state := evolution.DatabaseEvolutionStatus(ctx); !state.Ready {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":  "unavailable",
				"service": "tokenhub-backend",
				"reason":  state.Reason,
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": "tokenhub-backend"})
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.syncProviderImageCapabilityRouteProfiles()
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
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, r, NewHTTPError(405, "method_not_allowed", "Method not allowed"))
		return
	}
	s.handleModelGet(w, r)
}

func (s *Server) handleModelGet(w http.ResponseWriter, r *http.Request) {
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
	s.syncProviderImageCapabilityRouteProfiles()
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
