package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const providerPluginCommandTimeout = 120 * time.Second

type providerPluginAdapter struct {
	dir     string
	command string
	timeout time.Duration
}

type providerPluginRequest struct {
	Operation     string                    `json:"operation"`
	Provider      Provider                  `json:"provider"`
	ProviderModel string                    `json:"provider_model"`
	Request       ChatCompletionRequest     `json:"request"`
	Credentials   providerPluginCredentials `json:"credentials,omitempty"`
}

type providerPluginCredentials struct {
	APIKey string `json:"api_key,omitempty"`
}

type providerPluginResponse struct {
	Response any   `json:"response"`
	Usage    Usage `json:"usage,omitempty"`
}

func newProviderPluginAdapter(pkg pluginmeta.Package) providerPluginAdapter {
	return providerPluginAdapter{
		dir:     pkg.Dir,
		command: pkg.Manifest.Entry.Backend.Command,
		timeout: providerPluginCommandTimeout,
	}
}

func (a providerPluginAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	req.Stream = false
	var result providerPluginResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:     "chat",
		Provider:      provider,
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentials{APIKey: provider.APIKey},
	}, &result); err != nil {
		return nil, Usage{}, err
	}
	return result.Response, result.Usage, nil
}

func (a providerPluginAdapter) ChatStream(context.Context, Provider, string, ChatCompletionRequest, io.Writer) (Usage, error) {
	return Usage{}, providerPluginCapabilityUnsupported("chat streaming")
}

func (a providerPluginAdapter) Responses(context.Context, Provider, string, ResponsesRequest) (any, Usage, error) {
	return nil, Usage{}, providerPluginCapabilityUnsupported("Responses")
}

func (a providerPluginAdapter) Embeddings(context.Context, Provider, string, EmbeddingsRequest) (any, Usage, error) {
	return nil, Usage{}, providerPluginCapabilityUnsupported("embeddings")
}

func registerExternalProviderPluginAdapters(registry *AdapterRegistry, packages []pluginmeta.Package) {
	for _, pkg := range packages {
		if !externalProviderPackageHasBackend(pkg) {
			continue
		}
		capabilities := externalProviderAdapterCapabilities(pkg.Manifest.Capabilities.Gateway)
		if len(capabilities) == 0 {
			continue
		}
		adapter := newProviderPluginAdapter(pkg)
		descriptor := pkg.Manifest.Descriptor()
		for _, providerType := range pkg.Manifest.Capabilities.ProviderTypes {
			providerType = strings.TrimSpace(providerType)
			if providerType == "" {
				continue
			}
			registry.register(providerType, adapter, descriptor.ID, capabilities...)
		}
	}
}

func externalProviderPackageHasBackend(pkg pluginmeta.Package) bool {
	if len(pkg.Manifest.Capabilities.ProviderTypes) == 0 || pkg.Manifest.Entry.Backend == nil {
		return false
	}
	if !manifestAllowsProviderCredentials(pkg.Manifest) {
		return false
	}
	return strings.TrimSpace(pkg.Manifest.Entry.Backend.Protocol) == pluginmeta.BackendProtocolStdioJSONV1 &&
		strings.TrimSpace(pkg.Manifest.Entry.Backend.Command) != ""
}

func manifestAllowsProviderCredentials(manifest pluginmeta.Manifest) bool {
	for _, dataClass := range manifest.Permissions.Data.Read {
		if dataClass == pluginmeta.DataProviderCredentials {
			return true
		}
	}
	return false
}

func externalProviderAdapterCapabilities(capabilities []string) []AdapterCapability {
	supported := []AdapterCapability{}
	for _, capability := range capabilities {
		if AdapterCapability(strings.TrimSpace(capability)) == AdapterCapabilityChat {
			supported = append(supported, AdapterCapabilityChat)
		}
	}
	return supported
}

func providerPluginCapabilityUnsupported(capability string) error {
	return NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider plugin does not support "+capability)
}
