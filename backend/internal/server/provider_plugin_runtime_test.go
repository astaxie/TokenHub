package server

import (
	"net/http"
	"testing"
	"time"
)

func TestBuiltinProviderRuntimeBuildsAdaptersForPluginRegistration(t *testing.T) {
	store := NewMemoryStore()
	client := &http.Client{Timeout: time.Second}
	streamClient := &http.Client{Timeout: 2 * time.Second}

	runtime := newBuiltinProviderRuntime(builtinProviderRuntimeDependencies{
		Store:             store,
		Client:            client,
		StreamClient:      streamClient,
		StreamIdleTimeout: 3 * time.Second,
	})

	for _, providerType := range []string{
		ProviderMock,
		ProviderOpenAI,
		ProviderOpenAICompatible,
		"deepseek",
		"qwen",
		"local",
		ProviderKronk,
		ProviderAzureOpenAI,
		ProviderAnthropic,
		ProviderGemini,
	} {
		if runtime.adapters[providerType] == nil {
			t.Fatalf("runtime adapter %q was not built", providerType)
		}
	}
	openai, ok := runtime.adapters[ProviderOpenAI].(OpenAICompatibleAdapter)
	if !ok || openai.Client != client || openai.StreamClient != streamClient || openai.StreamIdleTimeout != 3*time.Second {
		t.Fatalf("OpenAI runtime adapter = %+v ok=%t", openai, ok)
	}
	if _, ok := runtime.adapters[ProviderKronk].(KronkAdapter); !ok {
		t.Fatalf("Kronk runtime adapter = %T, want KronkAdapter", runtime.adapters[ProviderKronk])
	}
	codexSubscription := codexSubscriptionAdapterFrom(runtime.adapters)
	if codexSubscription == nil {
		t.Fatal("Codex subscription runtime adapter was not built")
	}
	if codexSubscription.StreamIdleTimeout != 3*time.Second {
		t.Fatalf("Codex stream idle timeout = %v, want 3s", codexSubscription.StreamIdleTimeout)
	}
	if codexSubscription.Client == nil || codexSubscription.Client.Transport == nil {
		t.Fatal("Codex subscription client or transport was not configured")
	}
	if codexSubscription.RefreshCredentials == nil {
		t.Fatal("Codex subscription credential refresh callback was not configured")
	}
}

func TestServerCodexSubscriptionAdapterResolvesFromRegistry(t *testing.T) {
	server := NewWithConfig(NewMemoryStore(), Config{AdminToken: "dev_admin_token"})
	registered := server.codexSubscription
	if registered == nil {
		t.Fatal("test server did not initialize the Codex subscription adapter")
	}

	server.codexSubscription = nil
	resolved, err := server.codexSubscriptionAdapter()
	if err != nil {
		t.Fatalf("resolve Codex subscription adapter: %v", err)
	}
	if resolved != registered {
		t.Fatal("Codex subscription adapter should resolve from the adapter registry")
	}
}
