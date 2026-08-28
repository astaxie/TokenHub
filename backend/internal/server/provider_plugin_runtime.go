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
	adapters          map[string]ProviderAdapter
	codexSubscription *CodexSubscriptionAdapter
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
		adapters: map[string]ProviderAdapter{
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
		},
		codexSubscription: codexSubscription,
	}
}
