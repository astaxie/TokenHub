package server

import (
	"context"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestExternalMockProviderFixtureRegistersAdapterContract(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external mock provider fixture uses POSIX sh")
	}
	packages, err := pluginmeta.NewRuntime(externalMockProviderFixtureDir()).LoadIntoWithActions(pluginmeta.NewRegistry(), pluginmeta.NewGatewayChainRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("load external mock provider fixture: %v", err)
	}
	registry := NewAdapterRegistry()
	registerExternalProviderPluginAdapters(registry, packages)

	descriptor, ok := registry.Describe("external_mock")
	if !ok {
		t.Fatal("external mock provider adapter was not registered")
	}
	for _, capability := range []AdapterCapability{
		AdapterCapabilityChat,
		AdapterCapabilityChatStream,
		AdapterCapabilityResponses,
		AdapterCapabilityResponseStream,
		AdapterCapabilityEmbeddings,
		AdapterCapabilityModels,
		AdapterCapabilityProbe,
	} {
		if !adapterSupports(descriptor, capability) {
			t.Fatalf("descriptor capabilities = %+v, missing %s", descriptor.Capabilities, capability)
		}
	}
	if descriptor.PluginID != "tokenhub.provider.external-mock" {
		t.Fatalf("descriptor plugin id = %q", descriptor.PluginID)
	}
	if len(descriptor.ProviderPolicy.RouteProtocols) != 2 ||
		descriptor.ProviderPolicy.RouteProtocols[0] != "chat/completions" ||
		descriptor.ProviderPolicy.RouteProtocols[1] != "responses" {
		t.Fatalf("route protocols = %+v", descriptor.ProviderPolicy.RouteProtocols)
	}
	if descriptor.ProviderPolicy.SupportsCustomHeaders {
		t.Fatal("external mock provider should disable custom headers through manifest policy")
	}
	if !descriptor.ProviderPolicy.APIKeyRequired ||
		descriptor.ProviderPolicy.CredentialsScope != providerCredentialsScopeProvider ||
		descriptor.ProviderPolicy.DefaultBaseURL != "https://mock-provider.example/v1" ||
		descriptor.ProviderPolicy.ErrorProfile != "generic" ||
		descriptor.ProviderPolicy.ModelDiscovery.Path != "/models" ||
		descriptor.ProviderPolicy.ModelDiscovery.Auth != "bearer_header" {
		t.Fatalf("provider policy = %+v, want external mock manifest policy", descriptor.ProviderPolicy)
	}
	if len(descriptor.ResourceTypes) != 1 || descriptor.ResourceTypes[0].Type != "external_mock_account" {
		t.Fatalf("resource types = %+v, want external_mock_account", descriptor.ResourceTypes)
	}

	adapter, ok := resolveTypedAdapter[ProviderAdapter](registry, "external_mock")
	if !ok {
		t.Fatal("external mock provider adapter does not implement ProviderAdapter")
	}
	response, usage, err := adapter.Chat(context.Background(), Provider{
		ID:     "prv_external_mock",
		Type:   "external_mock",
		APIKey: "provider-secret",
	}, "external-upstream-chat", ChatCompletionRequest{
		Model:    "gateway-chat",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("chat through external mock provider fixture: %v", err)
	}
	if response.(map[string]any)["id"] != "chatcmpl_external_mock" || usage.TotalTokens != 5 {
		t.Fatalf("chat response=%+v usage=%+v", response, usage)
	}
}

func TestExternalMockProviderFixtureServesGatewayCoreEndpoints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external mock provider fixture uses POSIX sh")
	}
	cases := []struct {
		name          string
		model         string
		modality      string
		providerModel string
		path          string
		request       map[string]any
		want          []string
	}{
		{
			name:          "chat",
			model:         "external-mock-chat",
			modality:      "chat",
			providerModel: "external-upstream-chat",
			path:          "/v1/chat/completions",
			request: map[string]any{
				"model":    "external-mock-chat",
				"messages": []map[string]any{{"role": "user", "content": "hello"}},
			},
			want: []string{`"id":"chatcmpl_external_mock"`, "external mock chat"},
		},
		{
			name:          "responses",
			model:         "external-mock-responses",
			modality:      "chat",
			providerModel: "external-upstream-responses",
			path:          "/v1/responses",
			request: map[string]any{
				"model": "external-mock-responses",
				"input": "hello",
			},
			want: []string{`"id":"resp_external_mock"`, "external mock responses"},
		},
		{
			name:          "chat stream",
			model:         "external-mock-chat-stream",
			modality:      "chat",
			providerModel: "external-upstream-chat-stream",
			path:          "/v1/chat/completions",
			request: map[string]any{
				"model":    "external-mock-chat-stream",
				"stream":   true,
				"messages": []map[string]any{{"role": "user", "content": "hello"}},
			},
			want: []string{`"id":"chatcmpl_external_mock_stream"`, "external mock stream", "data: [DONE]"},
		},
		{
			name:          "embeddings",
			model:         "external-mock-embeddings",
			modality:      "embedding",
			providerModel: "external-upstream-embeddings",
			path:          "/v1/embeddings",
			request: map[string]any{
				"model": "external-mock-embeddings",
				"input": "hello",
			},
			want: []string{`"object":"list"`, `"embedding":[0.1,0.2,0.3]`},
		},
		{
			name:          "responses stream",
			model:         "external-mock-responses-stream",
			modality:      "chat",
			providerModel: "external-upstream-responses-stream",
			path:          "/v1/responses",
			request: map[string]any{
				"model":  "external-mock-responses-stream",
				"input":  "hello",
				"stream": true,
			},
			want: []string{"event: response.output_text.delta", "external mock responses stream", `"id":"resp_external_mock_stream"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, secret := newExternalMockProviderGatewayServer(t, tc.model, tc.modality, tc.providerModel)
			response := doJSON(t, server.Handler(), http.MethodPost, tc.path, tc.request, secret)
			if response.Code != http.StatusOK {
				t.Fatalf("gateway %s through external mock provider fixture: expected 200, got %d: %s", tc.name, response.Code, response.Body)
			}
			for _, want := range tc.want {
				if !strings.Contains(response.Body, want) {
					t.Fatalf("gateway %s response missing %q: %s", tc.name, want, response.Body)
				}
			}
			if strings.Contains(response.Body, "provider-secret") {
				t.Fatalf("gateway %s leaked provider credentials: %s", tc.name, response.Body)
			}
		})
	}
}

func TestExternalMockProviderFixtureServesCatalogAndProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external mock provider fixture uses POSIX sh")
	}
	packages, err := pluginmeta.NewRuntime(externalMockProviderFixtureDir()).LoadIntoWithActions(pluginmeta.NewRegistry(), pluginmeta.NewGatewayChainRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("load external mock provider fixture: %v", err)
	}
	registry := NewAdapterRegistry()
	registerExternalProviderPluginAdapters(registry, packages)

	provider := Provider{ID: "prv_external_mock", Type: "external_mock", APIKey: "provider-secret"}
	resource := ProviderResource{ID: "rsrc_external_mock", ProviderID: "prv_external_mock", ResourceType: "external_mock_account"}
	cataloger, ok := resolveTypedAdapter[ProviderResourceModelCataloger](registry, "external_mock")
	if !ok {
		t.Fatal("external mock provider adapter does not implement ProviderResourceModelCataloger")
	}
	catalog, status, err := cataloger.ResourceModels(context.Background(), provider, resource, "")
	if err != nil {
		t.Fatalf("catalog through external mock provider fixture: %v", err)
	}
	if status != http.StatusOK || catalog.Type != "external_mock" || catalog.ETag != "mock-etag" || len(catalog.Models) != 2 {
		t.Fatalf("catalog status=%d catalog=%+v", status, catalog)
	}

	prober, ok := resolveTypedAdapter[ProviderResourceProber](registry, "external_mock")
	if !ok {
		t.Fatal("external mock provider adapter does not implement ProviderResourceProber")
	}
	probe, err := prober.Probe(context.Background(), provider, resource, ProviderProbeRequest{Model: "external-upstream-chat"})
	if err != nil {
		t.Fatalf("probe through external mock provider fixture: %v", err)
	}
	if probe.ResourceID != "rsrc_external_mock" || probe.Model != "external-upstream-chat" ||
		probe.OutputText != "external mock provider is reachable" || probe.LatencyMS != 12 {
		t.Fatalf("probe result = %+v", probe)
	}
}

func newExternalMockProviderGatewayServer(t *testing.T, model string, modality string, providerModel string) (*Server, string) {
	t.Helper()
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "External Mock Provider Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "External Mock Provider Key", Allowed: []string{model}, Status: StatusActive}, "thk_external_mock_provider")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: model, Modality: modality, Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_external_mock", Name: "External Mock Provider", Type: "external_mock", APIKey: "provider-secret", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName:     model,
		ProviderID:    provider.ID,
		ProviderModel: providerModel,
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})
	return NewWithConfig(store, Config{AdminToken: "plugin-admin", PluginDir: externalMockProviderFixtureDir()}), secret
}

func externalMockProviderFixtureDir() string {
	return filepath.Join("..", "plugin", "testdata", "external-mock-provider")
}
