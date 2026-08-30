package server

import (
	"net/http"
	"time"
)

type builtinProviderRuntimeDependencies struct {
	Store               Store
	Client              *http.Client
	StreamClient        *http.Client
	StreamIdleTimeout   time.Duration
	SyntheticDNSPolicy  *providerSyntheticDNSPolicy
	ProviderProxyPolicy *providerProxyPolicy
}

type builtinProviderRuntime struct {
	adapters map[string]any
}

type providerResourceModelSupportConfigurer interface {
	ConfigureProviderResourceModelSupport(func(providerType string, resourceType string) bool)
}

type providerImageCapabilityProfileConfigurer interface {
	ConfigureProviderImageCapabilityProfiles(func(providerType string) []providerImageCapabilityRouteProfile)
}

func newBuiltinProviderRuntime(deps builtinProviderRuntimeDependencies) builtinProviderRuntime {
	openai := OpenAICompatibleAdapter{
		Client:            deps.Client,
		StreamClient:      deps.StreamClient,
		StreamIdleTimeout: deps.StreamIdleTimeout,
	}
	kronk := KronkAdapter{OpenAICompatibleAdapter: openai}
	codexSubscription := &CodexSubscriptionAdapter{
		Client: &http.Client{
			// The same SSRF guard the other provider adapters get: a custom
			// Codex endpoint is validated at save time, but DNS answers can
			// change afterwards and redirects must not bounce
			// credential-bearing responses/compact/probe/image calls into
			// the internal network. No Client.Timeout: streaming stays
			// bounded by StreamIdleTimeout, exactly as before.
			Transport: rotatingProviderUpstreamTransport(
				allowedProviderUpstreamCIDRs(),
				deps.SyntheticDNSPolicy,
				deps.ProviderProxyPolicy,
				nil,
			),
			CheckRedirect: strictProviderUpstreamRedirect,
		},
		StreamIdleTimeout: deps.StreamIdleTimeout,
	}
	if deps.Store != nil {
		codexSubscription.RefreshCredentials = deps.Store.RefreshProviderResourceCredentials
	}
	return builtinProviderRuntime{
		adapters: map[string]any{
			ProviderMock:             MockAdapter{},
			ProviderOpenAI:           openai,
			ProviderOpenAICompatible: openai,
			"deepseek":               openai,
			"qwen":                   openai,
			"local":                  openai,
			ProviderKronk:            kronk,
			ProviderAzureOpenAI: AzureOpenAIAdapter{
				Client:            deps.Client,
				StreamClient:      deps.StreamClient,
				StreamIdleTimeout: deps.StreamIdleTimeout,
			},
			ProviderAnthropic: AnthropicAdapter{
				Client:            deps.Client,
				StreamClient:      deps.StreamClient,
				StreamIdleTimeout: deps.StreamIdleTimeout,
			},
			ProviderGemini: GeminiAdapter{
				Client:            deps.Client,
				StreamClient:      deps.StreamClient,
				StreamIdleTimeout: deps.StreamIdleTimeout,
			},
			ProviderOpenAICodex: codexSubscription,
		},
	}
}

func codexSubscriptionAdapterFrom(adapters map[string]any) *CodexSubscriptionAdapter {
	if adapters == nil {
		return nil
	}
	switch adapter := adapters[ProviderOpenAICodex].(type) {
	case *CodexSubscriptionAdapter:
		return adapter
	case CodexSubscriptionAdapter:
		return &adapter
	default:
		return nil
	}
}

func configureProviderResourceModelSupport(adapters map[string]any, registry *AdapterRegistry) {
	for _, adapter := range adapters {
		configurer, ok := adapter.(providerResourceModelSupportConfigurer)
		if !ok {
			continue
		}
		configurer.ConfigureProviderResourceModelSupport(func(providerType string, resourceType string) bool {
			descriptor, ok := registry.Describe(providerType)
			return ok && adapterSupportsResourceType(descriptor, resourceType)
		})
	}
}

func configureProviderImageCapabilityProfiles(adapters map[string]any, profiles func(providerType string) []providerImageCapabilityRouteProfile) {
	for _, adapter := range adapters {
		configurer, ok := adapter.(providerImageCapabilityProfileConfigurer)
		if !ok {
			continue
		}
		configurer.ConfigureProviderImageCapabilityProfiles(profiles)
	}
}

func (s *Server) codexSubscriptionAdapter() (*CodexSubscriptionAdapter, error) {
	if s == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "provider_adapter_missing", "Provider adapter is not registered")
	}
	if s.adapterRegistry != nil {
		adapter, err := s.adapterRegistry.Resolve(ProviderOpenAICodex)
		if err == nil {
			switch typed := adapter.(type) {
			case *CodexSubscriptionAdapter:
				if typed != nil {
					return typed, nil
				}
			case CodexSubscriptionAdapter:
				return &typed, nil
			}
		}
	}
	return nil, NewHTTPError(http.StatusServiceUnavailable, "provider_adapter_missing", "Provider adapter is not registered")
}
