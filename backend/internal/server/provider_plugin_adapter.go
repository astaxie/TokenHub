package server

import (
	"bytes"
	"context"
	"encoding/json"
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
	supportsChatStream      bool
	supportsResponsesStream bool
	supportsImageGenerate   bool
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

type providerPluginStreamResponse struct {
	Events []providerPluginStreamEvent `json:"events"`
	Usage  Usage                       `json:"usage,omitempty"`
}

type providerPluginStreamEvent struct {
	Event string          `json:"event,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

type providerPluginModelsResponse struct {
	Catalog ProviderCatalogEntry `json:"catalog"`
	Status  int                  `json:"status,omitempty"`
}

type providerPluginProbeResponse struct {
	Result ProviderProbeResult `json:"result"`
}

type providerPluginImageResponse struct {
	Response openAIImageResponse `json:"response"`
	Usage    Usage               `json:"usage,omitempty"`
}

func newProviderPluginAdapter(pkg pluginmeta.Package) providerPluginAdapter {
	return providerPluginAdapter{
		dir:                     pkg.Dir,
		command:                 pkg.Manifest.Entry.Backend.Command,
		timeout:                 providerPluginCommandTimeout,
		routeProtocols:          providerPluginRouteProtocols(pkg.Manifest),
		supportsProviderHeaders: providerPluginSupportsCustomHeaders(pkg.Manifest),
		supportsChatStream:      providerPluginHasGatewayCapability(pkg.Manifest, AdapterCapabilityChatStream),
		supportsResponsesStream: providerPluginHasGatewayCapability(pkg.Manifest, AdapterCapabilityResponseStream),
		supportsImageGenerate:   providerPluginHasGatewayCapability(pkg.Manifest, AdapterCapabilityImageGenerate),
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

func (a providerPluginAdapter) ChatStream(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest, writer io.Writer) (Usage, error) {
	if !a.supportsChatStream {
		return Usage{}, providerPluginCapabilityUnsupported("chat streaming")
	}
	req.Stream = true
	var result providerPluginStreamResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:     "chat_stream",
		Provider:      provider,
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentials{APIKey: provider.APIKey},
	}, &result); err != nil {
		return Usage{}, err
	}
	for _, event := range result.Events {
		rendered, err := renderProviderPluginStreamEvent(event)
		if err != nil {
			return Usage{}, err
		}
		if _, err := writer.Write(rendered); err != nil {
			return Usage{}, err
		}
		if flusher, ok := writer.(streamFlusher); ok {
			flusher.Flush()
		}
	}
	return result.Usage, nil
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

func (a providerPluginAdapter) OpenResponses(ctx context.Context, provider Provider, providerModel string, req ResponsesRequest, _ http.Header) (*http.Response, error) {
	if !a.supportsResponsesStream {
		return nil, providerPluginCapabilityUnsupported("streaming Responses")
	}
	req.Stream = true
	var result providerPluginStreamResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:     "responses_stream",
		Provider:      provider,
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentials{APIKey: provider.APIKey},
	}, &result); err != nil {
		return nil, err
	}
	var body bytes.Buffer
	for _, event := range result.Events {
		rendered, err := renderProviderPluginStreamEvent(event)
		if err != nil {
			return nil, err
		}
		if _, err := body.Write(rendered); err != nil {
			return nil, err
		}
	}
	header := make(http.Header)
	header.Set("content-type", "text/event-stream")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(body.Bytes())),
	}, nil
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

func (a providerPluginAdapter) GenerateImage(ctx context.Context, provider Provider, providerModel string, req ProviderImageGenerationRequest) ([]byte, string, Usage, error) {
	if !a.supportsImageGenerate {
		return nil, "", Usage{}, providerPluginCapabilityUnsupported("image generation")
	}
	var result providerPluginImageResponse
	if err := pluginmeta.RunCommandJSON(ctx, a.dir, a.command, a.timeout, providerPluginRequest{
		Operation:     "image_generation",
		Provider:      provider,
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentials{APIKey: provider.APIKey},
	}, &result); err != nil {
		return nil, "", Usage{}, err
	}
	if len(result.Response.Data) == 0 || strings.TrimSpace(result.Response.Data[0].B64JSON) == "" {
		return nil, "", Usage{}, NewHTTPError(http.StatusBadGateway, "image_result_missing", "Provider plugin image generation completed without an image result")
	}
	imageBytes, err := decodeGeneratedImage(result.Response.Data[0].B64JSON)
	if err != nil {
		return nil, "", Usage{}, NewHTTPError(http.StatusBadGateway, "image_result_invalid", err.Error())
	}
	usage := result.Usage
	if usage.TotalTokens == 0 && usage.PromptTokens == 0 && usage.CompletionTokens == 0 {
		usage = usageFromMap(map[string]any{"usage": result.Response.Usage})
	}
	usage.ServedModel = firstNonEmpty(strings.TrimSpace(providerModel), req.Model)
	return imageBytes, result.Response.Data[0].RevisedPrompt, usage, nil
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
		case AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityEmbeddings, AdapterCapabilityModels, AdapterCapabilityProbe, AdapterCapabilityImageGenerate:
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
		case AdapterCapabilityChat, AdapterCapabilityChatStream:
			protocols = append(protocols, "chat/completions")
		case AdapterCapabilityResponses, AdapterCapabilityResponseStream:
			protocols = append(protocols, "responses")
		case AdapterCapabilityEmbeddings:
			protocols = append(protocols, "embeddings")
		case AdapterCapabilityImageGenerate:
			protocols = append(protocols, "images/generations")
		}
	}
	return protocols
}

func providerPluginHasGatewayCapability(manifest pluginmeta.Manifest, capability AdapterCapability) bool {
	for _, candidate := range manifest.Capabilities.Gateway {
		if AdapterCapability(strings.TrimSpace(candidate)) == capability {
			return true
		}
	}
	return false
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

func renderProviderPluginStreamEvent(event providerPluginStreamEvent) ([]byte, error) {
	if strings.ContainsAny(event.Event, "\r\n") {
		return nil, invalidProviderResponseError("provider plugin returned an invalid stream event name")
	}
	data, err := providerPluginStreamEventData(event.Data)
	if err != nil {
		return nil, err
	}
	return renderSSEEvent(serverSentEvent{Event: event.Event, Data: data}), nil
}

func providerPluginStreamEventData(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] == '"' {
		var data string
		if err := json.Unmarshal(raw, &data); err != nil {
			return "", invalidProviderResponseError("provider plugin returned invalid stream event data")
		}
		return data, nil
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return "", invalidProviderResponseError("provider plugin returned invalid stream event data")
	}
	return compact.String(), nil
}

func providerPluginCapabilityUnsupported(capability string) error {
	return NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider plugin does not support "+capability)
}
