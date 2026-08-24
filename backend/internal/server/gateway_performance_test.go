package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"tokenhub/backend/internal/perfbench"
)

const benchmarkModel = "tokenhub-benchmark-model"

type gatewayBenchmarkOptions struct {
	config                    Config
	failover                  bool
	requestPadding            int
	disablePayloadPersistence bool
}

type gatewayBenchmarkFixture struct {
	handler http.Handler
	secret  string
	body    []byte
	path    string
}

func BenchmarkGatewayChat(b *testing.B) {
	silenceGatewayBenchmarkLogs(b)
	benchmarkDatabaseMatrix(b, func(b *testing.B, store *GormStore) {
		runGatewayBenchmark(b, store, "/v1/chat/completions", false, gatewayBenchmarkOptions{})
	})
}

func BenchmarkGatewayResponses(b *testing.B) {
	silenceGatewayBenchmarkLogs(b)
	benchmarkDatabaseMatrix(b, func(b *testing.B, store *GormStore) {
		runGatewayBenchmark(b, store, "/v1/responses", false, gatewayBenchmarkOptions{})
	})
}

func BenchmarkGatewayStreaming(b *testing.B) {
	silenceGatewayBenchmarkLogs(b)
	benchmarkDatabaseMatrix(b, func(b *testing.B, store *GormStore) {
		runGatewayBenchmark(b, store, "/v1/chat/completions", true, gatewayBenchmarkOptions{})
	})
}

func BenchmarkGatewayFailover(b *testing.B) {
	silenceGatewayBenchmarkLogs(b)
	benchmarkDatabaseMatrix(b, func(b *testing.B, store *GormStore) {
		runGatewayBenchmark(b, store, "/v1/chat/completions", false, gatewayBenchmarkOptions{failover: true})
	})
}

func BenchmarkGatewayGovernanceCosts(b *testing.B) {
	silenceGatewayBenchmarkLogs(b)
	cases := []struct {
		name    string
		options gatewayBenchmarkOptions
	}{
		{name: "AuditSmall"},
		{name: "LargePayloadAuditPersistenceOff", options: gatewayBenchmarkOptions{requestPadding: 32 * 1024, disablePayloadPersistence: true}},
		{name: "LargePayloadAuditPersistenceOn", options: gatewayBenchmarkOptions{requestPadding: 32 * 1024}},
		{name: "Metrics", options: gatewayBenchmarkOptions{config: Config{MetricsEnabled: true}}},
		{name: "Tracing", options: gatewayBenchmarkOptions{config: Config{TracingEnabled: true}}},
		{name: "TracingWithPayloads", options: gatewayBenchmarkOptions{config: Config{TracingEnabled: true, TracingCapturePayloads: true}}},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			store := newBenchmarkMemoryStore(b)
			runGatewayBenchmark(b, store, "/v1/chat/completions", false, test.options)
		})
	}
}

func BenchmarkPlanRouteOrderRendezvous(b *testing.B) {
	server := &Server{}
	call := CallContext{
		RequestID: "req_rendezvous_benchmark",
		Affinity: &RequestAffinity{
			Kind:    AffinityKindCodexSession,
			KeyHash: "rendezvous-benchmark-affinity",
		},
	}
	for _, candidateCount := range []int{1, 8, 32, 128} {
		b.Run(fmt.Sprintf("Candidates%d", candidateCount), func(b *testing.B) {
			routes := make([]RouteSelection, candidateCount)
			for index := range routes {
				routes[index] = RouteSelection{Route: ModelRoute{
					ID:       fmt.Sprintf("route_rendezvous_%03d", index),
					Priority: 1,
					Weight:   100,
					Strategy: RouteStrategyBalanced,
				}}
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				planned := server.planRouteOrder(call, routes)
				if len(planned) != candidateCount {
					b.Fatalf("planned routes = %d, want %d", len(planned), candidateCount)
				}
			}
		})
	}
}

func BenchmarkGatewayPayloadAuditRendering(b *testing.B) {
	for _, size := range []int{256, 32 * 1024} {
		b.Run(fmt.Sprintf("Bytes%d", size), func(b *testing.B) {
			payload := map[string]any{"prompt": strings.Repeat("x", size), "metadata": map[string]any{"source": "benchmark"}}
			b.ReportAllocs()
			b.SetBytes(int64(size))
			for range b.N {
				body, truncated := auditPayloadBody(payload)
				if body == "" || truncated {
					b.Fatal("unexpected rendered audit payload")
				}
			}
		})
	}
}

func silenceGatewayBenchmarkLogs(b *testing.B) {
	b.Helper()
	previous := log.Writer()
	log.SetOutput(io.Discard)
	b.Cleanup(func() { log.SetOutput(previous) })
}

type benchmarkPayloadPersistenceStore struct {
	Store
}

func (benchmarkPayloadPersistenceStore) RecordRequestPayload(string, string, bool, string, bool) {}

func benchmarkDatabaseMatrix(b *testing.B, benchmark func(*testing.B, *GormStore)) {
	b.Helper()
	b.Run("SQLite", func(b *testing.B) {
		benchmark(b, newBenchmarkMemoryStore(b))
	})
	if postgresURL := strings.TrimSpace(os.Getenv("TOKENHUB_BENCHMARK_POSTGRES_URL")); postgresURL != "" {
		b.Run("PostgreSQL", func(b *testing.B) {
			baseStore, err := NewStoreWithDialect(postgresURL, benchmarkServerConfig())
			if err != nil {
				b.Fatal(err)
			}
			closeBenchmarkStore(b, baseStore)
			transaction := baseStore.db.Begin()
			if transaction.Error != nil {
				b.Fatal(transaction.Error)
			}
			store := *baseStore
			store.db = transaction
			b.Cleanup(func() { _ = transaction.Rollback().Error })
			benchmark(b, &store)
		})
	}
}

func newBenchmarkMemoryStore(b *testing.B) *GormStore {
	b.Helper()
	store := NewMemoryStoreWithConfig(benchmarkServerConfig())
	closeBenchmarkStore(b, store)
	return store
}

func closeBenchmarkStore(b *testing.B, store *GormStore) {
	b.Helper()
	database, err := store.db.DB()
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
}

func benchmarkServerConfig() Config {
	return Config{
		AdminToken:               "benchmark-admin",
		Environment:              "benchmark",
		SecretKey:                "benchmark-secret-key",
		TracingSampleRatio:       1,
		TracingTimeoutSeconds:    2,
		TracingQueueSize:         65536,
		DBMaxOpenConns:           32,
		DBMaxIdleConns:           32,
		GracefulShutdownSeconds:  2,
		ResourceFailureThreshold: 1_000_000_000,
	}
}

func runGatewayBenchmark(b *testing.B, store *GormStore, path string, stream bool, options gatewayBenchmarkOptions) {
	b.Helper()
	fixture := newGatewayBenchmarkFixture(b, store, path, stream, options)
	b.ReportAllocs()
	b.SetBytes(int64(len(fixture.body)))
	b.ResetTimer()
	for range b.N {
		request := httptest.NewRequest(http.MethodPost, fixture.path, bytes.NewReader(fixture.body))
		request.Header.Set("content-type", "application/json")
		request.Header.Set("authorization", "Bearer "+fixture.secret)
		response := httptest.NewRecorder()
		fixture.handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			b.Fatalf("gateway status %d: %s", response.Code, response.Body.String())
		}
	}
}

func newGatewayBenchmarkFixture(b *testing.B, store *GormStore, path string, stream bool, options gatewayBenchmarkOptions) gatewayBenchmarkFixture {
	b.Helper()
	model := benchmarkModel + "-" + NewID("case")
	project := store.CreateProject(Project{ID: NewID("prj_bench"), Name: "Performance benchmark", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "benchmark-key", Allowed: []string{model}, Status: StatusActive}, "thk_benchmark_"+NewID("key"))
	if err != nil {
		b.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: "chat", Status: StatusActive})

	upstreams := make([]*httptest.Server, 0, 2)
	if options.failover {
		upstreams = append(upstreams, httptest.NewServer(perfbench.NewMockHandler(perfbench.MockConfig{FailureEvery: 1, FailureStatus: http.StatusServiceUnavailable})))
	}
	upstreams = append(upstreams, httptest.NewServer(perfbench.NewMockHandler(perfbench.MockConfig{ResponseBytes: 1024, StreamChunks: 4})))
	for _, upstream := range upstreams {
		server := upstream
		b.Cleanup(server.Close)
	}
	for index, upstream := range upstreams {
		providerID := NewID(fmt.Sprintf("prv_bench_%d", index))
		provider := store.AddProvider(Provider{ID: providerID, Name: providerID, Type: ProviderOpenAICompatible, BaseURL: upstream.URL, Status: StatusActive, Healthy: true})
		resource, addErr := store.AddProviderResource(ProviderResource{ID: NewID(fmt.Sprintf("rsrc_bench_%d", index)), ProviderID: provider.ID, Name: providerID, ResourceType: "openai", Status: StatusActive, Healthy: true, Priority: 1, Weight: 100, MaxConcurrency: 10000})
		if addErr != nil {
			b.Fatal(addErr)
		}
		store.AddRoute(ModelRoute{ID: NewID(fmt.Sprintf("route_bench_%d", index)), ModelName: model, ProviderID: provider.ID, ProviderResourceID: resource.ID, ProviderModel: "benchmark-upstream-model", Priority: index + 1, Weight: 100, Status: StatusActive, Strategy: RouteStrategyPriorityOnly})
	}

	config := benchmarkServerConfig()
	config.MetricsEnabled = options.config.MetricsEnabled
	config.TracingEnabled = options.config.TracingEnabled
	config.TracingCapturePayloads = options.config.TracingCapturePayloads
	var traceCollector *httptest.Server
	if config.TracingEnabled {
		traceCollector = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		config.TracingEndpoint = traceCollector.URL + "/v1/traces"
		b.Cleanup(traceCollector.Close)
	}
	var appStore Store = store
	if options.disablePayloadPersistence {
		appStore = benchmarkPayloadPersistenceStore{Store: store}
	}
	app := NewWithConfig(appStore, config)
	if config.TracingEnabled && app.traceEmitter == nil {
		b.Fatal("tracing benchmark did not enable the trace emitter")
	}
	b.Cleanup(func() { _ = app.Shutdown(context.Background()) })
	prompt := "benchmark_request_" + strings.Repeat("x", options.requestPadding)
	body := []byte(fmt.Sprintf(`{"model":%q,"messages":[{"role":"user","content":%q}],"input":%q,"stream":%t}`, model, prompt, prompt, stream))
	return gatewayBenchmarkFixture{handler: app.Handler(), secret: secret, body: body, path: path}
}
