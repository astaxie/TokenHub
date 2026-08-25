package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestExternalProviderPluginAdapterExecutesChatCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifest(t, pluginDir, true)
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"chat"'*'"provider_model":"upstream-model"'*'"api_key":"provider-secret"'*)
    printf '{"response":{"id":"chatcmpl_plugin","choices":[{"message":{"role":"assistant","content":"from plugin"}}]},"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}'
    ;;
  *)
    printf 'unexpected provider payload: %s' "$payload" >&2
    exit 2
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	packages, err := pluginmeta.NewRuntime(root).LoadIntoWithActions(pluginmeta.NewRegistry(), pluginmeta.NewGatewayChainRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("load plugin packages: %v", err)
	}
	registry := NewAdapterRegistry()
	registerExternalProviderPluginAdapters(registry, packages)

	resolved, err := registry.Resolve("custom_stdio")
	if err != nil {
		t.Fatalf("resolve external provider adapter: %v", err)
	}
	adapter, ok := resolved.(ProviderAdapter)
	if !ok {
		t.Fatalf("resolved adapter = %T, want ProviderAdapter", resolved)
	}
	response, usage, err := adapter.Chat(context.Background(), Provider{Type: "custom_stdio", APIKey: "provider-secret"}, "upstream-model", ChatCompletionRequest{Model: "gateway-model"})
	if err != nil {
		t.Fatalf("chat through provider plugin: %v", err)
	}
	if usage.TotalTokens != 7 {
		t.Fatalf("usage = %+v, want total 7", usage)
	}
	body := response.(map[string]any)
	if body["id"] != "chatcmpl_plugin" {
		t.Fatalf("response = %+v, want plugin response", body)
	}
	descriptor, ok := registry.Describe("custom_stdio")
	if !ok || descriptor.PluginID != "tokenhub.provider.custom-stdio" || !adapterSupports(descriptor, AdapterCapabilityChat) {
		t.Fatalf("adapter descriptor = %+v", descriptor)
	}
}

func TestExternalProviderPluginAdapterServesGatewayChat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifest(t, pluginDir, true)
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
cat >/dev/null
printf '{"response":{"id":"chatcmpl_stdio","object":"chat.completion","choices":[{"message":{"role":"assistant","content":"served by plugin"}}]},"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Provider Plugin Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Provider Plugin Key", Allowed: []string{"plugin-chat"}, Status: StatusActive}, "thk_provider_plugin")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "plugin-chat", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_plugin", Name: "Provider Plugin", Type: "custom_stdio", APIKey: "provider-secret", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName:     "plugin-chat",
		ProviderID:    provider.ID,
		ProviderModel: "plugin-upstream-chat",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})
	server := NewWithConfig(store, Config{AdminToken: "plugin-admin", PluginDir: root})

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model": "plugin-chat",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("gateway chat through provider plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"id":"chatcmpl_stdio"`) || !strings.Contains(response.Body, "served by plugin") {
		t.Fatalf("gateway chat response did not come from provider plugin: %s", response.Body)
	}
}

func TestExternalProviderPluginAdapterExecutesResponsesAndEmbeddings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"chat", "responses", "embeddings"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"responses"'*)
    printf '{"response":{"id":"resp_plugin","object":"response","output_text":"response plugin"},"usage":{"prompt_tokens":5,"completion_tokens":6,"total_tokens":11}}'
    ;;
  *'"operation":"embeddings"'*)
    printf '{"response":{"object":"list","data":[{"embedding":[0.1,0.2]}]},"usage":{"prompt_tokens":7,"total_tokens":7}}'
    ;;
  *)
    printf 'unexpected provider payload: %s' "$payload" >&2
    exit 2
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	packages, err := pluginmeta.NewRuntime(root).LoadIntoWithActions(pluginmeta.NewRegistry(), pluginmeta.NewGatewayChainRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("load plugin packages: %v", err)
	}
	registry := NewAdapterRegistry()
	registerExternalProviderPluginAdapters(registry, packages)
	descriptor, ok := registry.Describe("custom_stdio")
	if !ok {
		t.Fatal("external provider descriptor was not registered")
	}
	for _, capability := range []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityResponses, AdapterCapabilityEmbeddings} {
		if !adapterSupports(descriptor, capability) {
			t.Fatalf("descriptor capabilities = %+v, want %s", descriptor.Capabilities, capability)
		}
	}
	adapter, ok := resolveTypedAdapter[ProviderAdapter](registry, "custom_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ProviderAdapter")
	}

	response, usage, err := adapter.Responses(context.Background(), Provider{Type: "custom_stdio", APIKey: "provider-secret"}, "upstream-responses", ResponsesRequest{Model: "gateway-responses"})
	if err != nil {
		t.Fatalf("responses through provider plugin: %v", err)
	}
	if response.(map[string]any)["id"] != "resp_plugin" || usage.TotalTokens != 11 {
		t.Fatalf("responses result = %+v usage=%+v", response, usage)
	}
	response, usage, err = adapter.Embeddings(context.Background(), Provider{Type: "custom_stdio", APIKey: "provider-secret"}, "upstream-embed", EmbeddingsRequest{Model: "gateway-embed", Input: "hello"})
	if err != nil {
		t.Fatalf("embeddings through provider plugin: %v", err)
	}
	if response.(map[string]any)["object"] != "list" || usage.TotalTokens != 7 {
		t.Fatalf("embeddings result = %+v usage=%+v", response, usage)
	}
}

func TestExternalProviderPluginAdapterRequiresCredentialPermission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifest(t, pluginDir, false)
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
printf '{"response":{},"usage":{}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	packages, err := pluginmeta.NewRuntime(root).Discover()
	if err != nil {
		t.Fatalf("discover plugin packages: %v", err)
	}
	registry := NewAdapterRegistry()
	registerExternalProviderPluginAdapters(registry, packages)

	if _, err := registry.Resolve("custom_stdio"); err == nil || !strings.Contains(err.Error(), "Provider adapter is not registered") {
		t.Fatalf("resolve external provider without credential permission err = %v", err)
	}
}

func writeProviderPluginManifest(t *testing.T, dir string, includeCredentials bool) {
	t.Helper()
	writeProviderPluginManifestWithCapabilities(t, dir, includeCredentials, []string{"chat"})
}

func writeProviderPluginManifestWithCapabilities(t *testing.T, dir string, includeCredentials bool, capabilities []string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	permissions := ""
	if includeCredentials {
		permissions = `
permissions:
  data:
    read:
      - provider_credentials
`
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(`
schema_version: 1
id: tokenhub.provider.custom-stdio
name: Custom stdio Provider
version: 1.0.0
tokenhub:
  plugin_api: v1
kinds:
  - provider
placement:
  - gateway_chain
entry:
  backend:
    protocol: stdio-json-v1
    command: provider.sh
capabilities:
  provider_types:
    - custom_stdio
  gateway:
`+providerPluginGatewayCapabilityYAML(capabilities)+
		permissions), 0o644); err != nil {
		t.Fatal(err)
	}
}

func providerPluginGatewayCapabilityYAML(capabilities []string) string {
	var builder strings.Builder
	for _, capability := range capabilities {
		builder.WriteString("    - ")
		builder.WriteString(capability)
		builder.WriteByte('\n')
	}
	return builder.String()
}
