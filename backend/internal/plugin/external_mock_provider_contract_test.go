package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestExternalMockProviderFixtureDeclaresProviderPluginContract(t *testing.T) {
	pkg := loadExternalMockProviderFixture(t)

	manifest := pkg.Manifest
	if manifest.ID != "tokenhub.provider.external-mock" {
		t.Fatalf("manifest id = %q", manifest.ID)
	}
	if len(manifest.Kinds) != 1 || manifest.Kinds[0] != KindProvider {
		t.Fatalf("manifest kinds = %+v, want provider only", manifest.Kinds)
	}
	if manifest.Entry.Backend == nil ||
		manifest.Entry.Backend.Protocol != BackendProtocolStdioJSONV1 ||
		manifest.Entry.Backend.Command != "provider.sh" {
		t.Fatalf("backend entry = %+v, want stdio-json-v1 provider.sh", manifest.Entry.Backend)
	}
	if !externalMockContainsString(manifest.Capabilities.ProviderTypes, "external_mock") {
		t.Fatalf("provider types = %+v, want external_mock", manifest.Capabilities.ProviderTypes)
	}
	for _, capability := range []string{"chat", "chat_stream", "responses", "responses_stream", "embeddings", "models", "probe"} {
		if !externalMockContainsString(manifest.Capabilities.Gateway, capability) {
			t.Fatalf("gateway capabilities = %+v, missing %q", manifest.Capabilities.Gateway, capability)
		}
	}
	if !containsDataClass(manifest.Permissions.Data.Read, DataProviderCredentials) {
		t.Fatalf("data read permissions = %+v, want provider_credentials", manifest.Permissions.Data.Read)
	}

	descriptor := manifest.Descriptor()
	if !descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider", Name: "chat", Subject: "external_mock"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider", Name: "responses_stream", Subject: "external_mock"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "supports_custom_headers", Subject: "external_mock", Value: "false"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "api_key_required", Subject: "external_mock", Value: "true"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_resource_type", Name: "external_mock_account", Subject: "external_mock"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "credentials_scope", Subject: "external_mock", Value: "provider"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "default_base_url", Subject: "external_mock", Value: "https://mock-provider.example/v1"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "error_profile", Subject: "external_mock", Value: "generic"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_path", Subject: "external_mock", Value: "/models"}) ||
		!descriptorHasCapability(descriptor, CapabilityDescriptor{Kind: "provider_policy", Name: "model_discovery_auth", Subject: "external_mock", Value: "bearer_header"}) {
		t.Fatalf("descriptor capabilities = %+v, missing external mock provider contract", descriptor.Capabilities)
	}
}

func loadExternalMockProviderFixture(t *testing.T) Package {
	t.Helper()
	packages, err := NewRuntime(filepath.Join("testdata", "external-mock-provider")).Discover()
	if err != nil {
		t.Fatalf("discover external mock provider fixture: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("packages = %d, want external mock provider fixture", len(packages))
	}
	return packages[0]
}

func TestExternalMockProviderFixtureRunsCoreProviderOperations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external mock provider fixture uses POSIX sh")
	}
	pkg := loadExternalMockProviderFixture(t)
	runner := NewProviderCommandRunner(pkg.Dir, pkg.Manifest.Entry.Backend.Command)
	base := ProviderCommandRequest{
		Provider: ProviderCommandProvider{
			ID:   "prv_external_mock",
			Type: "external_mock",
		},
		ProviderModel: "external-upstream-chat",
		Request: map[string]any{
			"model":    "gateway-model",
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
		},
		Credentials: ProviderCommandCredentials{APIKey: "provider-secret"},
	}

	t.Run("chat", func(t *testing.T) {
		invocation := base
		invocation.Operation = "chat"
		invocation.ProviderModel = "external-upstream-chat"
		var result struct {
			Response map[string]any `json:"response"`
			Usage    map[string]any `json:"usage"`
		}
		executeExternalMockProviderCommand(t, runner, invocation, &result)
		if result.Response["id"] != "chatcmpl_external_mock" || result.Usage["total_tokens"] != float64(5) {
			t.Fatalf("chat result = %+v usage=%+v", result.Response, result.Usage)
		}
	})

	t.Run("chat stream", func(t *testing.T) {
		invocation := base
		invocation.Operation = "chat_stream"
		invocation.ProviderModel = "external-upstream-chat-stream"
		invocation.Request = map[string]any{
			"model":    "gateway-model",
			"messages": []map[string]string{{"role": "user", "content": "hello"}},
			"stream":   true,
		}
		var result struct {
			Events []map[string]any `json:"events"`
			Usage  map[string]any   `json:"usage"`
		}
		executeExternalMockProviderCommand(t, runner, invocation, &result)
		if len(result.Events) != 2 || result.Usage["total_tokens"] != float64(7) {
			t.Fatalf("chat stream result = %+v usage=%+v", result.Events, result.Usage)
		}
	})

	t.Run("responses", func(t *testing.T) {
		invocation := base
		invocation.Operation = "responses"
		invocation.ProviderModel = "external-upstream-responses"
		invocation.Request = map[string]any{"model": "gateway-model", "input": "hello"}
		var result struct {
			Response map[string]any `json:"response"`
			Usage    map[string]any `json:"usage"`
		}
		executeExternalMockProviderCommand(t, runner, invocation, &result)
		if result.Response["id"] != "resp_external_mock" || result.Usage["total_tokens"] != float64(9) {
			t.Fatalf("responses result = %+v usage=%+v", result.Response, result.Usage)
		}
	})

	t.Run("responses stream", func(t *testing.T) {
		invocation := base
		invocation.Operation = "responses_stream"
		invocation.ProviderModel = "external-upstream-responses-stream"
		invocation.Request = map[string]any{"model": "gateway-model", "input": "hello", "stream": true}
		var result struct {
			Events []map[string]any `json:"events"`
		}
		executeExternalMockProviderCommand(t, runner, invocation, &result)
		if len(result.Events) != 2 {
			t.Fatalf("responses stream events = %+v, want 2", result.Events)
		}
	})

	t.Run("embeddings", func(t *testing.T) {
		invocation := base
		invocation.Operation = "embeddings"
		invocation.ProviderModel = "external-upstream-embeddings"
		invocation.Request = map[string]any{"model": "gateway-model", "input": "hello"}
		var result struct {
			Response map[string]any `json:"response"`
			Usage    map[string]any `json:"usage"`
		}
		executeExternalMockProviderCommand(t, runner, invocation, &result)
		if result.Response["object"] != "list" || result.Usage["total_tokens"] != float64(6) {
			t.Fatalf("embeddings result = %+v usage=%+v", result.Response, result.Usage)
		}
	})

	t.Run("models", func(t *testing.T) {
		invocation := base
		invocation.Operation = "models"
		invocation.Resource = &ProviderCommandResource{ID: "rsrc_external_mock", ProviderID: "prv_external_mock"}
		var result struct {
			Status  int            `json:"status"`
			Catalog map[string]any `json:"catalog"`
		}
		executeExternalMockProviderCommand(t, runner, invocation, &result)
		if result.Status != 200 || result.Catalog["type"] != "external_mock" || result.Catalog["etag"] != "mock-etag" {
			t.Fatalf("models result = status %d catalog=%+v", result.Status, result.Catalog)
		}
	})

	t.Run("probe", func(t *testing.T) {
		invocation := base
		invocation.Operation = "probe"
		invocation.Resource = &ProviderCommandResource{ID: "rsrc_external_mock", ProviderID: "prv_external_mock"}
		var result struct {
			Result map[string]any `json:"result"`
		}
		executeExternalMockProviderCommand(t, runner, invocation, &result)
		if result.Result["output_text"] != "external mock provider is reachable" || result.Result["resource_id"] != "rsrc_external_mock" {
			t.Fatalf("probe result = %+v", result.Result)
		}
	})
}

func TestExternalMockProviderFixtureExecutesLiteralCommandWithoutShellExpansion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("external mock provider fixture uses POSIX sh")
	}
	dir := t.TempDir()
	command := "provider.sh; echo injected"
	if err := os.WriteFile(filepath.Join(dir, command), []byte(`#!/bin/sh
cat >/dev/null
printf '{"response":{"id":"literal-command"}}'
`), 0o755); err != nil {
		t.Fatal(err)
	}

	runner := NewProviderCommandRunner(dir, command)
	var result struct {
		Response map[string]any `json:"response"`
	}
	executeExternalMockProviderCommand(t, runner, ProviderCommandRequest{
		Operation:     "chat",
		Provider:      ProviderCommandProvider{ID: "prv_external_mock", Type: "external_mock"},
		ProviderModel: "external-upstream-chat",
		Request:       map[string]any{"model": "gateway-model"},
		Credentials:   ProviderCommandCredentials{APIKey: "provider-secret"},
	}, &result)
	if result.Response["id"] != "literal-command" {
		t.Fatalf("response = %+v, want literal command execution", result.Response)
	}
}

func executeExternalMockProviderCommand(t *testing.T, runner ProviderCommandRunner, invocation ProviderCommandRequest, output any) {
	t.Helper()
	if err := runner.ExecuteProviderCommand(context.Background(), invocation, output); err != nil {
		t.Fatalf("execute external mock provider command %q: %v", invocation.Operation, err)
	}
}

func externalMockContainsString(items []string, candidate string) bool {
	for _, item := range items {
		if item == candidate {
			return true
		}
	}
	return false
}

func containsDataClass(items []GatewayDataClass, candidate GatewayDataClass) bool {
	for _, item := range items {
		if item == candidate {
			return true
		}
	}
	return false
}
