package server

import (
	"reflect"
	"testing"
)

// builtinAdapterCapabilities pins the capability set every built-in provider
// type advertises. Capabilities gate routing and the admin adapter listing, so
// an unintended change here is a product behaviour change, not a refactor.
var builtinAdapterCapabilities = map[string][]AdapterCapability{
	ProviderMock: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityResponses,
	},
	ProviderOpenAI: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityImageGenerate,
		AdapterCapabilityProbe, AdapterCapabilityResponses,
		AdapterCapabilityResponseStream,
	},
	ProviderOpenAICompatible: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
	ProviderOpenAICodex: {
		AdapterCapabilityImageGenerate, AdapterCapabilityModels,
		AdapterCapabilityOAuth, AdapterCapabilityProbe,
		AdapterCapabilityQuota, AdapterCapabilityResponses,
		AdapterCapabilityCompact, AdapterCapabilityResponseStream,
		AdapterCapabilityAffinity,
	},
	ProviderAzureOpenAI: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
	},
	ProviderAnthropic: {
		AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityProbe,
	},
	ProviderGemini: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
	},
	ProviderKronk: {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityModels,
		AdapterCapabilityProbe, AdapterCapabilityResponses,
		AdapterCapabilityResponseStream,
	},
	"deepseek": {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
	"qwen": {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
	"local": {
		AdapterCapabilityChat, AdapterCapabilityChatStream,
		AdapterCapabilityEmbeddings, AdapterCapabilityProbe,
		AdapterCapabilityResponses, AdapterCapabilityResponseStream,
	},
}

func TestBuiltinAdaptersResolveWithUnchangedCapabilities(t *testing.T) {
	server := New(NewMemoryStore())

	for adapterType, want := range builtinAdapterCapabilities {
		adapter, err := server.adapterRegistry.Resolve(adapterType)
		if err != nil {
			t.Fatalf("resolve %q: %v", adapterType, err)
		}
		if adapter == nil {
			t.Fatalf("resolve %q returned a nil adapter", adapterType)
		}
		descriptor, ok := server.adapterRegistry.Describe(adapterType)
		if !ok {
			t.Fatalf("describe %q: no descriptor", adapterType)
		}
		if !reflect.DeepEqual(descriptor.Capabilities, want) {
			t.Fatalf("capabilities for %q = %v, want %v", adapterType, descriptor.Capabilities, want)
		}
	}

	listed := server.adapterRegistry.List()
	if len(listed) != len(builtinAdapterCapabilities) {
		t.Fatalf("registry lists %d adapters, want %d", len(listed), len(builtinAdapterCapabilities))
	}
}

func TestResolveReportsUnregisteredAdapterType(t *testing.T) {
	server := New(NewMemoryStore())

	if _, err := server.adapterRegistry.Resolve("not_a_provider"); AsHTTPError(err).Code != "provider_adapter_missing" {
		t.Fatalf("resolving an unknown type returned %v, want provider_adapter_missing", err)
	}
	if _, ok := server.adapterRegistry.Describe("not_a_provider"); ok {
		t.Fatal("an unknown type reported a capability descriptor")
	}
}

// The gateway resolves the concrete adapter types for the Anthropic native path
// and OpenAI image generation, so a wrong registration would only surface as a
// runtime downgrade rather than a compile error.
func TestRegistryResolvesConcreteAdapterTypes(t *testing.T) {
	server := New(NewMemoryStore())

	anthropic, ok := resolveTypedAdapter[AnthropicAdapter](server.adapterRegistry, ProviderAnthropic)
	if !ok {
		t.Fatal("anthropic type did not resolve to an AnthropicAdapter")
	}
	if anthropic.Client == nil {
		t.Fatal("resolved AnthropicAdapter carries no HTTP client")
	}
	if _, ok := resolveTypedAdapter[OpenAICompatibleAdapter](server.adapterRegistry, ProviderOpenAI); !ok {
		t.Fatal("openai type did not resolve to an OpenAICompatibleAdapter")
	}
	if _, ok := resolveTypedAdapter[AnthropicAdapter](server.adapterRegistry, ProviderOpenAI); ok {
		t.Fatal("openai type resolved to an AnthropicAdapter")
	}
}

func TestRegisterTestAdapterInjectsAndOverridesWithoutTouchingCapabilities(t *testing.T) {
	server := New(NewMemoryStore())
	injected := MockAdapter{}

	registerTestAdapter(server, "injected_type", injected)
	resolved, err := server.adapterRegistry.Resolve("injected_type")
	if err != nil {
		t.Fatalf("resolve injected type: %v", err)
	}
	if _, ok := resolved.(MockAdapter); !ok {
		t.Fatalf("injected type resolved to %T, want MockAdapter", resolved)
	}
	if _, ok := server.adapterRegistry.Describe("injected_type"); ok {
		t.Fatal("injecting an adapter declared capabilities it does not have")
	}

	// Overriding a built-in must take effect, which is what the gateway tests
	// that swap in a failing or blocking upstream depend on.
	registerTestAdapter(server, ProviderOpenAI, injected)
	overridden, err := server.adapterRegistry.Resolve(ProviderOpenAI)
	if err != nil {
		t.Fatalf("resolve overridden built-in: %v", err)
	}
	if _, ok := overridden.(MockAdapter); !ok {
		t.Fatalf("override of %q resolved to %T, want MockAdapter", ProviderOpenAI, overridden)
	}
	descriptor, ok := server.adapterRegistry.Describe(ProviderOpenAI)
	if !ok || !reflect.DeepEqual(descriptor.Capabilities, builtinAdapterCapabilities[ProviderOpenAI]) {
		t.Fatalf("overriding an adapter changed its capabilities to %v", descriptor.Capabilities)
	}
}
