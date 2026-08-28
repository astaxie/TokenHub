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
	if runtime.codexSubscription == nil {
		t.Fatal("Codex subscription runtime adapter was not built")
	}
	if runtime.codexSubscription.StreamIdleTimeout != 3*time.Second {
		t.Fatalf("Codex stream idle timeout = %v, want 3s", runtime.codexSubscription.StreamIdleTimeout)
	}
	if runtime.codexSubscription.Client == nil || runtime.codexSubscription.Client.Transport == nil {
		t.Fatal("Codex subscription client or transport was not configured")
	}
	if runtime.codexSubscription.RefreshCredentials == nil {
		t.Fatal("Codex subscription credential refresh callback was not configured")
	}
}
