package server

import (
	"context"
	"encoding/json"
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
	protocoler, ok := resolved.(ProviderRouteProtocoler)
	if !ok {
		t.Fatal("external provider adapter was not a ProviderRouteProtocoler")
	}
	protocols := protocoler.RouteProtocols()
	if len(protocols) != 1 || protocols[0] != "chat/completions" {
		t.Fatalf("default route protocols = %v", protocols)
	}
}

func TestExternalProviderPluginAdapterExecutesChatStreamCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"chat", "chat_stream"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"chat_stream"'*'"provider_model":"upstream-model"'*'"stream":true'*'"api_key":"provider-secret"'*)
    cat <<'JSON'
{"events":[{"data":{"id":"chatcmpl_plugin_stream","object":"chat.completion.chunk","choices":[{"delta":{"content":"from plugin stream"}}]}},{"data":"[DONE]"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}
JSON
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
	if !ok || !adapterSupports(descriptor, AdapterCapabilityChatStream) {
		t.Fatalf("adapter descriptor = %+v, want chat_stream", descriptor)
	}
	adapter, ok := resolveTypedAdapter[ProviderAdapter](registry, "custom_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ProviderAdapter")
	}

	var stream strings.Builder
	usage, err := adapter.ChatStream(context.Background(), Provider{Type: "custom_stdio", APIKey: "provider-secret"}, "upstream-model", ChatCompletionRequest{Model: "gateway-model"}, &stream)
	if err != nil {
		t.Fatalf("chat stream through provider plugin: %v", err)
	}
	if usage.TotalTokens != 8 {
		t.Fatalf("usage = %+v, want total 8", usage)
	}
	output := stream.String()
	if !strings.Contains(output, `data: {"id":"chatcmpl_plugin_stream"`) || !strings.Contains(output, "from plugin stream") || !strings.Contains(output, "data: [DONE]") {
		t.Fatalf("stream output did not come from provider plugin:\n%s", output)
	}
}

func TestExternalProviderPluginAdapterUsesManifestProviderPolicy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.yaml"), []byte(`
schema_version: 1
id: tokenhub.provider.native-stdio
name: Native stdio Provider
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
    - native_stdio
  provider:
    route_protocols:
      - native/messages
      - native/messages
    supports_custom_headers: false
  gateway:
    - chat
permissions:
  data:
    read:
      - provider_credentials
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
printf '{"response":{},"usage":{}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}
	packages, err := pluginmeta.NewRuntime(root).LoadIntoWithActions(pluginmeta.NewRegistry(), pluginmeta.NewGatewayChainRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("load plugin packages: %v", err)
	}
	registry := NewAdapterRegistry()
	registerExternalProviderPluginAdapters(registry, packages)

	protocoler, ok := resolveTypedAdapter[ProviderRouteProtocoler](registry, "native_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ProviderRouteProtocoler")
	}
	protocols := protocoler.RouteProtocols()
	if len(protocols) != 1 || protocols[0] != "native/messages" {
		t.Fatalf("route protocols = %v", protocols)
	}
	policyer, ok := resolveTypedAdapter[ProviderHeaderPolicyer](registry, "native_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ProviderHeaderPolicyer")
	}
	if policyer.SupportsProviderHeaders() {
		t.Fatal("provider header policy was not loaded from manifest")
	}
	if err := validateProviderHeaderSupportWithRegistry(registry, "native_stdio", map[string]string{"X-Tenant": "tenant"}); AsHTTPError(err).Code != "provider_headers_unsupported" {
		t.Fatalf("header validation error = %v", err)
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

func TestExternalProviderPluginAdapterServesGatewayChatStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"chat", "chat_stream"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
cat >/dev/null
cat <<'JSON'
{"events":[{"data":{"id":"chatcmpl_stdio_stream","object":"chat.completion.chunk","choices":[{"delta":{"content":"streamed by plugin"}}]}},{"data":"[DONE]"}],"usage":{"prompt_tokens":2,"completion_tokens":4,"total_tokens":6}}
JSON
`), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Provider Plugin Stream Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Provider Plugin Stream Key", Allowed: []string{"plugin-stream-chat"}, Status: StatusActive}, "thk_provider_plugin_stream")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "plugin-stream-chat", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_plugin_stream", Name: "Provider Plugin Stream", Type: "custom_stdio", APIKey: "provider-secret", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName:     "plugin-stream-chat",
		ProviderID:    provider.ID,
		ProviderModel: "plugin-upstream-stream-chat",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})
	server := NewWithConfig(store, Config{AdminToken: "plugin-admin", PluginDir: root})

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":  "plugin-stream-chat",
		"stream": true,
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
		},
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("gateway chat stream through provider plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"id":"chatcmpl_stdio_stream"`) || !strings.Contains(response.Body, "streamed by plugin") || !strings.Contains(response.Body, "data: [DONE]") {
		t.Fatalf("gateway chat stream response did not come from provider plugin: %s", response.Body)
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

func TestExternalProviderPluginAdapterExecutesResponsesStreamCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"responses", "responses_stream"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"responses_stream"'*'"provider_model":"upstream-responses"'*'"stream":true'*'"api_key":"provider-secret"'*)
    cat <<'JSON'
{"events":[{"event":"response.output_text.delta","data":{"type":"response.output_text.delta","delta":"from plugin responses stream"}},{"event":"response.completed","data":{"type":"response.completed","response":{"id":"resp_plugin_stream","status":"completed","output":[],"usage":{"input_tokens":4,"output_tokens":5,"total_tokens":9}}}}]}
JSON
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
	if !ok || !adapterSupports(descriptor, AdapterCapabilityResponseStream) {
		t.Fatalf("adapter descriptor = %+v, want responses_stream", descriptor)
	}
	adapter, ok := resolveTypedAdapter[ResponsesStreamOpener](registry, "custom_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ResponsesStreamOpener")
	}

	opened, err := adapter.OpenResponses(context.Background(), Provider{Type: "custom_stdio", APIKey: "provider-secret"}, "upstream-responses", ResponsesRequest{Model: "gateway-responses"}, nil)
	if err != nil {
		t.Fatalf("open responses stream through provider plugin: %v", err)
	}
	defer opened.Body.Close()
	if opened.StatusCode != http.StatusOK || !strings.Contains(opened.Header.Get("content-type"), "text/event-stream") {
		t.Fatalf("opened response = status %d headers %+v", opened.StatusCode, opened.Header)
	}
	response, output, usage, err := consumeCodexResponsesStream(opened.Body, nil)
	if err != nil {
		t.Fatalf("consume plugin responses stream: %v", err)
	}
	if response["id"] != "resp_plugin_stream" || output != "from plugin responses stream" || usage.TotalTokens != 9 {
		t.Fatalf("response=%+v output=%q usage=%+v", response, output, usage)
	}
}

func TestExternalProviderPluginAdapterServesGatewayResponsesStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"responses", "responses_stream"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
cat >/dev/null
cat <<'JSON'
{"events":[{"event":"response.output_text.delta","data":{"type":"response.output_text.delta","delta":"gateway plugin responses stream"}},{"event":"response.completed","data":{"type":"response.completed","response":{"id":"resp_gateway_plugin_stream","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":6,"total_tokens":8}}}}]}
JSON
`), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Provider Plugin Responses Stream Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Provider Plugin Responses Stream Key", Allowed: []string{"plugin-responses-stream"}, Status: StatusActive}, "thk_provider_plugin_responses_stream")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "plugin-responses-stream", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_plugin_responses_stream", Name: "Provider Plugin Responses Stream", Type: "custom_stdio", APIKey: "provider-secret", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName:     "plugin-responses-stream",
		ProviderID:    provider.ID,
		ProviderModel: "plugin-upstream-responses-stream",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})
	server := NewWithConfig(store, Config{AdminToken: "plugin-admin", PluginDir: root})

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses", map[string]any{
		"model":  "plugin-responses-stream",
		"stream": true,
		"input":  "hello",
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("gateway responses stream through provider plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, "event: response.output_text.delta") || !strings.Contains(response.Body, "gateway plugin responses stream") || !strings.Contains(response.Body, `"id":"resp_gateway_plugin_stream"`) {
		t.Fatalf("gateway responses stream did not come from provider plugin: %s", response.Body)
	}
}

func TestExternalProviderPluginAdapterExecutesResponsesCompactCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"responses_compact"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"responses_compact"'*'"provider_model":"upstream-compact"'*'"model":"gateway-compact"'*'"api_key":"provider-secret"'*)
    printf '{"response":{"id":"resp_compact_plugin","status":"completed","output_text":"compacted by plugin"},"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}'
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
	if !ok || !adapterSupports(descriptor, AdapterCapabilityCompact) {
		t.Fatalf("adapter descriptor = %+v, want responses_compact", descriptor)
	}
	adapter, ok := resolveTypedAdapter[ResponsesCompactAdapter](registry, "custom_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ResponsesCompactAdapter")
	}

	body := map[string]json.RawMessage{
		"model": json.RawMessage(`"gateway-compact"`),
		"input": json.RawMessage(`"hello"`),
	}
	response, usage, err := adapter.CompactWithHeaders(context.Background(), Provider{Type: "custom_stdio", APIKey: "provider-secret"}, "upstream-compact", body, nil)
	if err != nil {
		t.Fatalf("compact responses through provider plugin: %v", err)
	}
	payload := response.(map[string]any)
	if payload["id"] != "resp_compact_plugin" || usage.TotalTokens != 6 {
		t.Fatalf("compact response=%+v usage=%+v", response, usage)
	}
}

func TestExternalProviderPluginAdapterServesGatewayResponsesCompact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"responses_compact"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"responses_compact"'*'"provider_model":"plugin-upstream-compact"'*'"model":"plugin-compact"'*'"api_key":"provider-secret"'*)
    printf '{"response":{"id":"resp_gateway_compact_plugin","status":"completed","output_text":"gateway compacted by plugin"},"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}'
    ;;
  *)
    printf 'unexpected provider payload: %s' "$payload" >&2
    exit 2
    ;;
esac
`), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Provider Plugin Compact Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Provider Plugin Compact Key", Allowed: []string{"plugin-compact"}, Status: StatusActive}, "thk_provider_plugin_compact")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: "plugin-compact", Modality: "chat", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_plugin_compact", Name: "Provider Plugin Compact", Type: "custom_stdio", APIKey: "provider-secret", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName:     "plugin-compact",
		ProviderID:    provider.ID,
		ProviderModel: "plugin-upstream-compact",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})
	server := NewWithConfig(store, Config{AdminToken: "plugin-admin", PluginDir: root})

	response := doJSON(t, server.Handler(), http.MethodPost, "/v1/responses/compact", map[string]any{
		"model": "plugin-compact",
		"input": "hello",
	}, secret)
	if response.Code != http.StatusOK {
		t.Fatalf("gateway compact responses through provider plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"id":"resp_gateway_compact_plugin"`) || !strings.Contains(response.Body, "gateway compacted by plugin") {
		t.Fatalf("gateway compact response did not come from provider plugin: %s", response.Body)
	}
}

func TestExternalProviderPluginAdapterExecutesImageGenerationCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"image_generation"})
	imageB64 := encodeBase64(realPNGFixture(t))
	script := strings.ReplaceAll(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"image_generation"'*'"provider_model":"plugin-image-model"'*'"action":"generate"'*'"prompt":"draw plugin image"'*'"api_key":"provider-secret"'*)
    cat <<'JSON'
{"response":{"data":[{"b64_json":"__IMAGE_B64__","revised_prompt":"revised by plugin"}]},"usage":{"prompt_tokens":6,"completion_tokens":7,"total_tokens":13}}
JSON
    ;;
  *)
    printf 'unexpected provider payload: %s' "$payload" >&2
    exit 2
    ;;
esac
`, "__IMAGE_B64__", imageB64)
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	packages, err := pluginmeta.NewRuntime(root).LoadIntoWithActions(pluginmeta.NewRegistry(), pluginmeta.NewGatewayChainRegistry(), nil, nil)
	if err != nil {
		t.Fatalf("load plugin packages: %v", err)
	}
	registry := NewAdapterRegistry()
	registerExternalProviderPluginAdapters(registry, packages)
	descriptor, ok := registry.Describe("custom_stdio")
	if !ok || !adapterSupports(descriptor, AdapterCapabilityImageGenerate) {
		t.Fatalf("adapter descriptor = %+v, want image_generation", descriptor)
	}
	adapter, ok := resolveTypedAdapter[ProviderImageGenerator](registry, "custom_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ProviderImageGenerator")
	}

	imageBytes, revisedPrompt, usage, err := adapter.GenerateImage(context.Background(), Provider{Type: "custom_stdio", APIKey: "provider-secret"}, "plugin-image-model", ProviderImageGenerationRequest{
		Action: "generate",
		Model:  "plugin-image-model",
		Prompt: "draw plugin image",
	})
	if err != nil {
		t.Fatalf("image generation through provider plugin: %v", err)
	}
	if len(imageBytes) == 0 || revisedPrompt != "revised by plugin" || usage.TotalTokens != 13 || usage.ServedModel != "plugin-image-model" {
		t.Fatalf("image bytes=%d revised=%q usage=%+v", len(imageBytes), revisedPrompt, usage)
	}
}

func TestExternalProviderPluginAdapterServesGatewayImageGeneration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"image_generation"})
	imageB64 := encodeBase64(realPNGFixture(t))
	script := strings.ReplaceAll(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"image_generation"'*'"provider_model":"plugin-image-model"'*'"action":"generate"'*'"prompt":"gateway plugin image"'*'"api_key":"provider-secret"'*)
    cat <<'JSON'
{"response":{"data":[{"b64_json":"__IMAGE_B64__","revised_prompt":"gateway revised prompt"}]},"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}
JSON
    ;;
  *)
    printf 'unexpected provider payload: %s' "$payload" >&2
    exit 2
    ;;
esac
`, "__IMAGE_B64__", imageB64)
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryStore()
	project := store.CreateProject(Project{Name: "Provider Plugin Image Project", Status: StatusActive})
	_, secret, err := store.CreateAPIKey(project.ID, APIKey{Name: "Provider Plugin Image Key", Allowed: []string{openAIImageModelName}, Status: StatusActive}, "thk_provider_plugin_image")
	if err != nil {
		t.Fatal(err)
	}
	store.AddModel(Model{Name: openAIImageModelName, Modality: "image", Status: StatusActive})
	provider := store.AddProvider(Provider{ID: "prv_plugin_image", Name: "Provider Plugin Image", Type: "custom_stdio", APIKey: "provider-secret", Status: StatusActive, Healthy: true})
	store.AddRoute(ModelRoute{
		ModelName:     openAIImageModelName,
		ProviderID:    provider.ID,
		ProviderModel: "plugin-image-model",
		Priority:      1,
		Weight:        100,
		Status:        StatusActive,
	})
	server := NewWithConfig(store, Config{AdminToken: "plugin-admin", PluginDir: root})

	response := doImageJSON(t, server.Handler(), http.MethodPost, "/v1/images/generations", map[string]any{
		"model":           openAIImageModelName,
		"prompt":          "gateway plugin image",
		"response_format": "b64_json",
	}, secret, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("gateway image generation through provider plugin: expected 200, got %d: %s", response.Code, response.Body)
	}
	if !strings.Contains(response.Body, `"b64_json"`) || !strings.Contains(response.Body, "gateway revised prompt") {
		t.Fatalf("gateway image response did not come from provider plugin: %s", response.Body)
	}
}

func TestExternalProviderPluginAdapterExecutesModelsAndProbe(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses POSIX sh")
	}
	root := t.TempDir()
	pluginDir := filepath.Join(root, "provider")
	writeProviderPluginManifestWithCapabilities(t, pluginDir, true, []string{"chat", "models", "probe"})
	if err := os.WriteFile(filepath.Join(pluginDir, "provider.sh"), []byte(`#!/bin/sh
payload="$(cat)"
case "$payload" in
  *'"operation":"models"'*'"etag":"etag-old"'*'"api_key":"resource-secret"'*)
    printf '{"status":200,"catalog":{"id":"custom-stdio","name":"Custom stdio","display_name":"Custom stdio","type":"custom_stdio","models_count":1,"source":"plugin-live","etag":"etag-new","models":[{"id":"plugin-model","name":"plugin-model","type":"chat"}]}}'
    ;;
  *'"operation":"probe"'*'"id":"rsrc_plugin"'*'"api_key":"resource-secret"'*)
    printf '{"result":{"model":"plugin-model","output_text":"plugin probe ok","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3},"latency_ms":4}}'
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
	for _, capability := range []AdapterCapability{AdapterCapabilityChat, AdapterCapabilityModels, AdapterCapabilityProbe} {
		if !adapterSupports(descriptor, capability) {
			t.Fatalf("descriptor capabilities = %+v, want %s", descriptor.Capabilities, capability)
		}
	}
	adapter, ok := resolveTypedAdapter[ProviderResourceModelCataloger](registry, "custom_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ProviderResourceModelCataloger")
	}
	provider := Provider{Type: "custom_stdio", APIKey: "provider-secret"}
	resource := ProviderResource{ID: "rsrc_plugin", ProviderID: "prv_plugin", APIKey: "resource-secret"}
	catalog, status, err := adapter.ResourceModels(context.Background(), provider, resource, "etag-old")
	if err != nil {
		t.Fatalf("models through provider plugin: %v", err)
	}
	if status != http.StatusOK || catalog.ETag != "etag-new" || catalog.ModelsCount != 1 || catalog.Models[0].ID != "plugin-model" {
		t.Fatalf("catalog = %+v status=%d, want plugin catalog", catalog, status)
	}
	prober, ok := resolveTypedAdapter[ProviderResourceProber](registry, "custom_stdio")
	if !ok {
		t.Fatal("external provider adapter was not a ProviderResourceProber")
	}
	result, err := prober.Probe(context.Background(), provider, resource, ProviderProbeRequest{Model: "plugin-model", Prompt: "ping"})
	if err != nil {
		t.Fatalf("probe through provider plugin: %v", err)
	}
	if result.ResourceID != resource.ID || result.OutputText != "plugin probe ok" || result.Usage.TotalTokens != 3 {
		t.Fatalf("probe result = %+v, want plugin probe result", result)
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
