package server

import (
	"context"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

const providerPluginCommandTimeout = 120 * time.Second

type providerPluginAdapter struct {
	dir                     string
	command                 string
	timeout                 time.Duration
	routeProtocols          []string
	supportsProviderHeaders bool
}

type providerPluginRequest struct {
	Operation     string                    `json:"operation"`
	Provider      Provider                  `json:"provider"`
	Resource      *ProviderResource         `json:"resource,omitempty"`
	ProviderModel string                    `json:"provider_model"`
	ETag          string                    `json:"etag,omitempty"`
	Request       any                       `json:"request"`
	Credentials   providerPluginCredentials `json:"credentials,omitempty"`
}

type providerPluginCredentials struct {
	APIKey string `json:"api_key,omitempty"`
}

type providerPluginResponse struct {
	Response any   `json:"response"`
	Usage    Usage `json:"usage,omitempty"`
}

type providerPluginModelsResponse struct {
	Catalog ProviderCatalogEntry `json:"catalog"`
	Status  int                  `json:"status,omitempty"`
}

type providerPluginProbeResponse struct {
	Result ProviderProbeResult `json:"result"`
}

func newProviderPluginAdapter(pkg pluginmeta.Package) providerPluginAdapter {
	return providerPluginAdapter{
		dir:                     pkg.Dir,
		command:                 pkg.Manifest.Entry.Backend.Command,
		timeout:                 providerPluginCommandTimeout,
		routeProtocols:          providerPluginRouteProtocols(pkg.Manifest),
		supportsProviderHeaders: providerPluginSupportsCustomHeaders(pkg.Manifest),
	}
}

func (a providerPluginAdapter) RouteProtocols() []string {
	return append([]string(nil), a.routeProtocols...)
}

func (a providerPluginAdapter) SupportsProviderHeaders() bool {
	return a.supportsProviderHeaders
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

func (a providerPluginAdapter) Responses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest) (any, Usage, error) {
	req.Stream = false
	var result providerPluginResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:     "responses",
		Provider:      provider,
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentials{APIKey: provider.APIKey},
	}, &result); err != nil {
		return nil, Usage{}, err
	}
	return result.Response, result.Usage, nil
}

func (a providerPluginAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	var result providerPluginResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:     "embeddings",
		Provider:      provider,
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentials{APIKey: provider.APIKey},
	}, &result); err != nil {
		return nil, Usage{}, err
	}
	return result.Response, result.Usage, nil
}

func (a providerPluginAdapter) ResourceModels(ctx context.Context, provider Provider, resource ProviderResource, etag string) (ProviderCatalogEntry, int, error) {
	effective := effectiveProviderResourceConfig(provider, &resource)
	var result providerPluginModelsResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:   "models",
		Provider:    effective,
		Resource:    &resource,
		ETag:        etag,
		Credentials: providerPluginCredentials{APIKey: effective.APIKey},
	}, &result); err != nil {
		return ProviderCatalogEntry{}, 0, err
	}
	status := result.Status
	if status == 0 {
		status = http.StatusOK
	}
	return result.Catalog, status, nil
}

func (a providerPluginAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{
		Prompt: "Reply with exactly one short sentence confirming that the provider connection works.",
	}
}

func (a providerPluginAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, req ProviderProbeRequest) (ProviderProbeResult, error) {
	effective := effectiveProviderResourceConfig(provider, &resource)
	var result providerPluginProbeResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:   "probe",
		Provider:    effective,
		Resource:    &resource,
		Request:     req,
		Credentials: providerPluginCredentials{APIKey: effective.APIKey},
	}, &result); err != nil {
		return ProviderProbeResult{}, err
	}
	if result.Result.ResourceID == "" {
		result.Result.ResourceID = resource.ID
	}
	if result.Result.Model == "" {
		result.Result.Model = req.Model
	}
	return result.Result, nil
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
		switch AdapterCapability(strings.TrimSpace(capability)) {
		case AdapterCapabilityChat, AdapterCapabilityResponses, AdapterCapabilityEmbeddings, AdapterCapabilityModels, AdapterCapabilityProbe:
			supported = append(supported, AdapterCapability(strings.TrimSpace(capability)))
		}
	}
	return supported
}

func providerPluginRouteProtocols(manifest pluginmeta.Manifest) []string {
	protocols := manifest.Capabilities.Provider.RouteProtocols
	if len(protocols) == 0 {
		protocols = providerPluginDefaultRouteProtocols(manifest.Capabilities.Gateway)
	}
	return normalizedProviderPluginProtocols(protocols)
}

func providerPluginDefaultRouteProtocols(capabilities []string) []string {
	protocols := []string{}
	for _, capability := range capabilities {
		switch AdapterCapability(strings.TrimSpace(capability)) {
		case AdapterCapabilityChat:
			protocols = append(protocols, "chat/completions")
		case AdapterCapabilityResponses:
			protocols = append(protocols, "responses")
		case AdapterCapabilityEmbeddings:
			protocols = append(protocols, "embeddings")
		case AdapterCapabilityImageGenerate:
			protocols = append(protocols, "images/generations")
		}
	}
	return protocols
}

func normalizedProviderPluginProtocols(protocols []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(protocols))
	for _, protocol := range protocols {
		protocol = strings.ToLower(strings.TrimSpace(protocol))
		if protocol == "" || seen[protocol] {
			continue
		}
		seen[protocol] = true
		normalized = append(normalized, protocol)
	}
	sort.Strings(normalized)
	return normalized
}

func providerPluginSupportsCustomHeaders(manifest pluginmeta.Manifest) bool {
	if manifest.Capabilities.Provider.SupportsCustomHeaders == nil {
		return true
	}
	return *manifest.Capabilities.Provider.SupportsCustomHeaders
}

func providerPluginCapabilityUnsupported(capability string) error {
	return NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider plugin does not support "+capability)
}
