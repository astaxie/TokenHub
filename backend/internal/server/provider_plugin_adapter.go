package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type providerPluginAdapter struct {
	commandRunner           pluginmeta.ProviderCommandRunner
	supportsChatStream      bool
	supportsResponsesStream bool
	supportsImageGenerate   bool
	supportsCompact         bool
}

type providerPluginRequest = pluginmeta.ProviderCommandRequest
type providerPluginProvider = pluginmeta.ProviderCommandProvider
type providerPluginResource = pluginmeta.ProviderCommandResource
type providerPluginCredentials = pluginmeta.ProviderCommandCredentials

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
		commandRunner:           pluginmeta.NewProviderCommandRunner(pkg.Dir, pkg.Manifest.Entry.Backend.Command),
		supportsChatStream:      providerPluginHasGatewayCapability(pkg.Manifest, AdapterCapabilityChatStream),
		supportsResponsesStream: providerPluginHasGatewayCapability(pkg.Manifest, AdapterCapabilityResponseStream),
		supportsImageGenerate:   providerPluginHasGatewayCapability(pkg.Manifest, AdapterCapabilityImageGenerate),
		supportsCompact:         providerPluginHasGatewayCapability(pkg.Manifest, AdapterCapabilityCompact),
	}
}

func (a providerPluginAdapter) executeProviderCommand(ctx context.Context, invocation providerPluginRequest, output any) error {
	return a.commandRunner.ExecuteProviderCommand(ctx, invocation, output)
}

func providerPluginProviderFromRuntime(provider Provider) providerPluginProvider {
	return providerPluginProvider{
		ID:        provider.ID,
		Name:      provider.Name,
		Type:      provider.Type,
		BaseURL:   provider.BaseURL,
		Status:    provider.Status,
		Healthy:   provider.Healthy,
		Priority:  provider.Priority,
		CreatedAt: provider.CreatedAt,
	}
}

func providerPluginResourceFromRuntime(resource *ProviderResource) *providerPluginResource {
	if resource == nil {
		return nil
	}
	converted := providerPluginResource{
		ID:             resource.ID,
		ProviderID:     resource.ProviderID,
		Name:           resource.Name,
		Group:          resource.Group,
		ResourceType:   resource.ResourceType,
		BaseURL:        resource.BaseURL,
		Region:         resource.Region,
		Environment:    resource.Environment,
		Status:         resource.Status,
		Healthy:        resource.Healthy,
		Priority:       resource.Priority,
		Weight:         resource.Weight,
		RateLimitRPM:   resource.RateLimitRPM,
		TokenLimitTPM:  resource.TokenLimitTPM,
		MaxConcurrency: resource.MaxConcurrency,
		CreatedAt:      resource.CreatedAt,
	}
	if resource.Credentials != nil {
		credentials := providerPluginCredentialsFromRuntimeCredentials(*resource.Credentials)
		converted.Credentials = &credentials
	}
	return &converted
}

func (a providerPluginAdapter) Chat(ctx context.Context, provider Provider, providerModel string, req ChatCompletionRequest) (any, Usage, error) {
	req.Stream = false
	var result providerPluginResponse
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:     "chat",
		Provider:      providerPluginProviderFromRuntime(provider),
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentialsFromRuntime(provider, nil),
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
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:     "chat_stream",
		Provider:      providerPluginProviderFromRuntime(provider),
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentialsFromRuntime(provider, nil),
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
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:     "responses",
		Provider:      providerPluginProviderFromRuntime(provider),
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentialsFromRuntime(provider, nil),
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
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:     "responses_stream",
		Provider:      providerPluginProviderFromRuntime(provider),
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentialsFromRuntime(provider, nil),
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

func (a providerPluginAdapter) CompactWithHeaders(ctx context.Context, provider Provider, providerModel string, body map[string]json.RawMessage, _ http.Header) (any, Usage, error) {
	if !a.supportsCompact {
		return nil, Usage{}, providerPluginCapabilityUnsupported("Responses compact")
	}
	var result providerPluginResponse
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:     "responses_compact",
		Provider:      providerPluginProviderFromRuntime(provider),
		ProviderModel: providerModel,
		Request:       body,
		Credentials:   providerPluginCredentialsFromRuntime(provider, nil),
	}, &result); err != nil {
		return nil, Usage{}, err
	}
	return result.Response, result.Usage, nil
}

func (a providerPluginAdapter) Embeddings(ctx context.Context, provider Provider, providerModel string, req EmbeddingsRequest) (any, Usage, error) {
	var result providerPluginResponse
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:     "embeddings",
		Provider:      providerPluginProviderFromRuntime(provider),
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentialsFromRuntime(provider, nil),
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
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:     "image_generation",
		Provider:      providerPluginProviderFromRuntime(provider),
		ProviderModel: providerModel,
		Request:       req,
		Credentials:   providerPluginCredentialsFromRuntime(provider, nil),
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
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:   "models",
		Provider:    providerPluginProviderFromRuntime(effective),
		Resource:    providerPluginResourceFromRuntime(&resource),
		ETag:        etag,
		Credentials: providerPluginCredentialsFromRuntime(effective, &resource),
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
	if err := a.executeProviderCommand(ctx, providerPluginRequest{
		Operation:   "probe",
		Provider:    providerPluginProviderFromRuntime(effective),
		Resource:    providerPluginResourceFromRuntime(&resource),
		Request:     req,
		Credentials: providerPluginCredentialsFromRuntime(effective, &resource),
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
		if !pkg.State.Enabled() {
			continue
		}
		if !externalProviderPackageHasBackend(pkg) {
			continue
		}
		capabilities := externalProviderAdapterCapabilities(pkg.Manifest.Capabilities.Gateway)
		if len(capabilities) == 0 {
			continue
		}
		descriptor := pkg.Manifest.Descriptor()
		adapter := newProviderPluginAdapter(pkg)
		registrations := []AdapterRegistration{}
		for _, providerType := range pkg.Manifest.Capabilities.ProviderTypes {
			providerType = strings.TrimSpace(providerType)
			if providerType == "" {
				continue
			}
			registrations = append(registrations, AdapterRegistration{
				Type:         providerType,
				Adapter:      adapter,
				Capabilities: capabilities,
			})
		}
		if len(registrations) > 0 {
			if err := registry.RegisterPlugin(descriptor, registrations...); err != nil {
				continue
			}
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
		case AdapterCapabilityChat, AdapterCapabilityChatStream, AdapterCapabilityResponses, AdapterCapabilityResponseStream, AdapterCapabilityEmbeddings, AdapterCapabilityModels, AdapterCapabilityProbe, AdapterCapabilityImageGenerate, AdapterCapabilityCompact, AdapterCapabilityAffinity:
			supported = append(supported, AdapterCapability(strings.TrimSpace(capability)))
		}
	}
	return supported
}

func providerPluginHasGatewayCapability(manifest pluginmeta.Manifest, capability AdapterCapability) bool {
	for _, candidate := range manifest.Capabilities.Gateway {
		if AdapterCapability(strings.TrimSpace(candidate)) == capability {
			return true
		}
	}
	return false
}

func providerPluginCredentialsFromRuntime(provider Provider, resource *ProviderResource) providerPluginCredentials {
	credentials := providerPluginCredentials{
		APIKey:      strings.TrimSpace(provider.APIKey),
		AccessToken: strings.TrimSpace(provider.APIKey),
	}
	if provider.Options != nil {
		credentials.AuthType = strings.TrimSpace(provider.Options["auth_type"])
		credentials.ExpiresAt = strings.TrimSpace(provider.Options["token_expires_at"])
		credentials.AccountID = strings.TrimSpace(provider.Options["account_id"])
		credentials.UserID = strings.TrimSpace(provider.Options["user_id"])
		credentials.Email = strings.TrimSpace(provider.Options["account_email"])
		credentials.OrganizationID = strings.TrimSpace(provider.Options["organization_id"])
		credentials.PlanType = strings.TrimSpace(provider.Options["plan_type"])
		credentials.Scopes = strings.TrimSpace(provider.Options["scopes"])
	}
	if resource != nil && resource.Credentials != nil {
		source := *resource.Credentials
		credentials.AuthType = firstNonEmpty(source.AuthType, credentials.AuthType)
		credentials.APIKey = firstNonEmpty(source.AccessToken, credentials.APIKey)
		credentials.AccessToken = firstNonEmpty(source.AccessToken, credentials.AccessToken)
		credentials.RefreshToken = firstNonEmpty(source.RefreshToken, credentials.RefreshToken)
		credentials.IDToken = firstNonEmpty(source.IDToken, credentials.IDToken)
		credentials.ClientID = firstNonEmpty(source.ClientID, credentials.ClientID)
		credentials.Scopes = firstNonEmpty(source.Scopes, credentials.Scopes)
		credentials.TokenType = firstNonEmpty(source.TokenType, credentials.TokenType)
		credentials.ExpiresAt = firstNonEmpty(source.ExpiresAt, credentials.ExpiresAt)
		credentials.AccountID = firstNonEmpty(source.AccountID, credentials.AccountID)
		credentials.UserID = firstNonEmpty(source.UserID, credentials.UserID)
		credentials.Email = firstNonEmpty(source.Email, credentials.Email)
		credentials.OrganizationID = firstNonEmpty(source.OrganizationID, credentials.OrganizationID)
		credentials.PlanType = firstNonEmpty(source.PlanType, credentials.PlanType)
	}
	if credentials.AuthType == "" && credentials.APIKey != "" {
		credentials.AuthType = "api_key"
	}
	return credentials
}

func providerPluginCredentialsFromRuntimeCredentials(source ProviderResourceCredentials) providerPluginCredentials {
	credentials := providerPluginCredentials{
		AuthType:       strings.TrimSpace(source.AuthType),
		APIKey:         strings.TrimSpace(source.AccessToken),
		AccessToken:    strings.TrimSpace(source.AccessToken),
		RefreshToken:   strings.TrimSpace(source.RefreshToken),
		IDToken:        strings.TrimSpace(source.IDToken),
		ClientID:       strings.TrimSpace(source.ClientID),
		Scopes:         strings.TrimSpace(source.Scopes),
		TokenType:      strings.TrimSpace(source.TokenType),
		ExpiresAt:      strings.TrimSpace(source.ExpiresAt),
		AccountID:      strings.TrimSpace(source.AccountID),
		UserID:         strings.TrimSpace(source.UserID),
		Email:          strings.TrimSpace(source.Email),
		OrganizationID: strings.TrimSpace(source.OrganizationID),
		PlanType:       strings.TrimSpace(source.PlanType),
	}
	if credentials.AuthType == "" && credentials.APIKey != "" {
		credentials.AuthType = "api_key"
	}
	return credentials
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
