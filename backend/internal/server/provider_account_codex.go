package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	openAICodexBaseURL           = "https://chatgpt.com/backend-api/codex"
	openAICodexResponsesURL      = openAICodexBaseURL + "/responses"
	openAICodexModelsURL         = openAICodexBaseURL + "/models"
	openAICodexVersion           = "0.145.0"
	openAICodexUserAgent         = "codex_cli_rs/0.145.0 (Mac OS 15.0.0; arm64) xterm-256color"
	openAICodexDefaultProbeModel = "gpt-5.6-luna"
	openAICodexMaxRequestRetries = 4
	openAICodexStreamIdleTimeout = 5 * time.Minute
)

func (a *CodexSubscriptionAdapter) streamIdleTimeout() time.Duration {
	if a == nil || a.StreamIdleTimeout <= 0 {
		return openAICodexStreamIdleTimeout
	}
	return a.StreamIdleTimeout
}

var errCodexStreamIdle = NewHTTPError(
	http.StatusGatewayTimeout,
	"codex_stream_idle_timeout",
	"Codex stream was idle for too long",
)

type CodexSubscriptionAdapter struct {
	Client *http.Client
	// Client deliberately carries no total deadline: a Codex stream is bounded by
	// how long it stays silent, not by how long it runs. StreamIdleTimeout is that
	// budget; zero keeps the historical five minutes.
	StreamIdleTimeout  time.Duration
	RefreshCredentials func(context.Context, string, bool) (ProviderResourceCredentials, error)
	ModelsURL          string
	QuotaURL           string
	MaxRequestRetries  int
}

type ProviderProbeRequest struct {
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort"`
	Speed           string `json:"speed"`
	Prompt          string `json:"prompt"`
}

type ProviderProbeResult struct {
	ResourceID          string         `json:"resource_id"`
	Model               string         `json:"model"`
	ReasoningEffort     string         `json:"reasoning_effort"`
	Speed               string         `json:"speed"`
	UpstreamServiceTier string         `json:"upstream_service_tier,omitempty"`
	OutputText          string         `json:"output_text"`
	Usage               Usage          `json:"usage"`
	LatencyMS           int64          `json:"latency_ms"`
	Response            map[string]any `json:"response"`
}

type codexSubscriptionTestRequest = ProviderProbeRequest

func (a CodexSubscriptionAdapter) Responses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest) (any, Usage, error) {
	return a.ResponsesWithHeaders(ctx, provider, providerModel, request, nil)
}

func (a CodexSubscriptionAdapter) ResponsesWithHeaders(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest, incoming http.Header) (any, Usage, error) {
	resp, err := a.OpenResponses(ctx, provider, providerModel, request, incoming)
	if err != nil {
		return nil, Usage{}, err
	}
	defer resp.Body.Close()
	response, outputText, usage, err := consumeCodexResponsesStream(resp.Body, nil)
	applyCodexResponseMetadata(&usage, resp.Header)
	if err != nil {
		return nil, usage, err
	}
	if outputText != "" {
		response["output_text"] = outputText
		if output, _ := response["output"].([]any); len(output) == 0 {
			response["output"] = []map[string]any{{
				"type":    "message",
				"role":    "assistant",
				"status":  "completed",
				"content": []map[string]any{{"type": "output_text", "text": outputText, "annotations": []any{}}},
			}}
		}
	}
	return response, usage, nil
}

func (a CodexSubscriptionAdapter) OpenResponses(ctx context.Context, provider Provider, providerModel string, request ResponsesRequest, incoming http.Header) (*http.Response, error) {
	resourceID := strings.TrimSpace(provider.Options["resource_id"])
	if resourceID == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "provider_resource_missing", "Codex Subscription resource is missing")
	}
	if a.RefreshCredentials == nil {
		return nil, NewHTTPError(http.StatusServiceUnavailable, "provider_credentials_unavailable", "Codex Subscription credentials are unavailable")
	}
	creds, err := a.RefreshCredentials(ctx, resourceID, false)
	if err != nil {
		return nil, err
	}
	endpoint, err := codexResponsesEndpoint(provider)
	if err != nil {
		return nil, err
	}
	fingerprintIDs := prepareCodexFingerprintRequest(provider, incoming, &request)
	resp, err := a.openResponsesWithRetry(ctx, endpoint, creds, providerModel, request, incoming, fingerprintIDs)
	if err == nil || providerErrorDisposition(err) != ProviderErrorAuthBroken {
		return resp, err
	}
	creds, refreshErr := a.RefreshCredentials(ctx, resourceID, true)
	if refreshErr != nil {
		return nil, refreshErr
	}
	return a.openResponsesWithRetry(ctx, endpoint, creds, providerModel, request, incoming, fingerprintIDs)
}

func (a CodexSubscriptionAdapter) openResponsesWithRetry(ctx context.Context, endpoint string, creds ProviderResourceCredentials, providerModel string, request ResponsesRequest, incoming http.Header, fingerprintIDs *codexFingerprintIDs) (*http.Response, error) {
	maxRetries := a.MaxRequestRetries
	if maxRetries <= 0 {
		maxRetries = openAICodexMaxRequestRetries
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		resp, err := a.openResponsesWithCredentials(ctx, endpoint, creds, providerModel, request, incoming, fingerprintIDs)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if providerErrorDisposition(err) != ProviderErrorTransientSame || attempt == maxRetries {
			return nil, err
		}
		delay := providerErrorRetryAfter(err)
		if delay <= 0 {
			delay = time.Duration(100*(1<<attempt)) * time.Millisecond
		}
		if delay > 2*time.Second {
			delay = 2 * time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func (a CodexSubscriptionAdapter) openResponsesWithCredentials(ctx context.Context, endpoint string, creds ProviderResourceCredentials, providerModel string, request ResponsesRequest, incoming http.Header, fingerprintIDs *codexFingerprintIDs) (*http.Response, error) {
	accessToken := strings.TrimSpace(creds.AccessToken)
	accountID := strings.TrimSpace(creds.AccountID)
	if accessToken == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "openai_account_token_missing", "OpenAI account access token is missing")
	}
	if accountID == "" {
		return nil, NewHTTPError(http.StatusBadRequest, "openai_account_id_missing", "OpenAI ChatGPT account ID is missing")
	}
	request.Model = strings.TrimSpace(providerModel)
	if inputText, ok := request.Input.(string); ok {
		request.Input = []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": inputText}},
		}}
	}
	request.Stream = true
	if request.Store == nil {
		store := false
		request.Store = &store
	}
	if strings.EqualFold(strings.TrimSpace(request.ServiceTier), "fast") {
		request.ServiceTier = "priority"
	}
	request = withoutUnsupportedCodexGenerationControls(request)
	applyCodexRequestEnvelope(&request, incoming)
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, NewHTTPError(http.StatusBadRequest, "invalid_request", err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, NewHTTPError(http.StatusBadGateway, "codex_request_failed", err.Error())
	}
	if parsed, parseErr := url.Parse(endpoint); parseErr == nil && strings.EqualFold(parsed.Hostname(), "chatgpt.com") {
		req.Host = parsed.Host
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("ChatGPT-Account-ID", accountID)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	applyCodexRequestHeaders(req.Header, incoming)
	applyCodexFingerprintHeaders(req.Header, fingerprintIDs)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, &ProviderInvocationError{
			Err:         NewHTTPError(http.StatusBadGateway, "codex_request_failed", fmt.Sprintf("Codex request failed: %v", err)),
			Disposition: ProviderErrorTransientSame,
		}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
		return nil, classifyCodexHTTPError(resp.StatusCode, resp.Header, data)
	}
	resp.Body = newIdleTimeoutReadCloser(resp.Body, a.streamIdleTimeout(), errCodexStreamIdle)
	return resp, nil
}

func withoutUnsupportedCodexGenerationControls(request ResponsesRequest) ResponsesRequest {
	request.MaxTokens = 0
	request.Temperature = nil
	if request.raw != nil {
		request.raw = cloneRawJSON(request.raw, 0)
		delete(request.raw, "max_output_tokens")
		delete(request.raw, "temperature")
	}
	return request
}

func codexResponsesEndpoint(provider Provider) (string, error) {
	baseURL := strings.TrimSpace(provider.BaseURL)
	if baseURL == "" {
		return openAICodexResponsesURL, nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", NewHTTPError(http.StatusBadRequest, "codex_base_url_invalid", "Codex base URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && !strings.EqualFold(parsed.Hostname(), "localhost") {
		return "", NewHTTPError(http.StatusBadRequest, "codex_base_url_invalid", "Codex base URL must use HTTPS")
	}
	if err := validateCodexEndpointHost(provider, parsed); err != nil {
		return "", err
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/responses") {
		parsed.Path += "/responses"
	}
	return parsed.String(), nil
}

func validateCodexEndpointHost(provider Provider, endpoint *url.URL) error {
	hostname := strings.ToLower(strings.TrimSpace(endpoint.Hostname()))
	if hostname == "chatgpt.com" || hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" {
		return nil
	}
	for _, allowed := range strings.Split(provider.Options["allowed_codex_hosts"], ",") {
		if hostname == strings.ToLower(strings.TrimSpace(allowed)) {
			return nil
		}
	}
	return NewHTTPError(http.StatusBadRequest, "codex_endpoint_host_not_allowed", "Codex Subscription credentials cannot be sent to this host")
}

func consumeCodexResponsesStream(body io.Reader, destination io.Writer) (map[string]any, string, Usage, error) {
	reader := bufio.NewReader(body)
	var response map[string]any
	var textBuilder strings.Builder
	var usage Usage
	collectedOutput := make(map[int]map[string]any)
	collectedOutputOrder := make([]int, 0, 4)
	nextUnindexedOutput := -1
	completed := false
	for {
		line, err := reader.ReadString('\n')
		if line != "" && destination != nil {
			if _, writeErr := io.WriteString(destination, line); writeErr != nil {
				return response, textBuilder.String(), usage, writeErr
			}
			if flusher, ok := destination.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			if data != "" && data != "[DONE]" {
				var event map[string]any
				if json.Unmarshal([]byte(data), &event) == nil {
					switch event["type"] {
					case "response.output_text.delta":
						if delta, ok := event["delta"].(string); ok {
							textBuilder.WriteString(delta)
						}
					case "response.output_item.done":
						if item, ok := event["item"].(map[string]any); ok {
							index := nextUnindexedOutput
							if rawIndex, exists := event["output_index"]; exists {
								index = int(int64FromAny(rawIndex))
							} else {
								nextUnindexedOutput--
							}
							if _, exists := collectedOutput[index]; !exists {
								collectedOutputOrder = append(collectedOutputOrder, index)
							}
							collectedOutput[index] = item
						}
					case "response.completed", "response.done", "response.incomplete":
						response, _ = event["response"].(map[string]any)
						if output, _ := anySlice(response["output"]); len(output) == 0 && len(collectedOutputOrder) > 0 {
							output = make([]any, 0, len(collectedOutputOrder))
							for _, index := range collectedOutputOrder {
								output = append(output, collectedOutput[index])
							}
							response["output"] = output
						}
						usage = usageFromMap(response)
						completed = true
					case "response.failed", "error":
						return response, textBuilder.String(), usage, codexStreamEventError(event)
					}
				}
			}
		}
		if completed {
			if destination != nil {
				if _, writeErr := io.WriteString(destination, "\n"); writeErr != nil {
					return response, textBuilder.String(), usage, writeErr
				}
				if flusher, ok := destination.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			if textBuilder.Len() == 0 {
				textBuilder.WriteString(codexResponseOutputText(response))
			}
			return response, textBuilder.String(), usage, nil
		}
		if err != nil {
			if err == io.EOF {
				return response, textBuilder.String(), usage, NewHTTPError(http.StatusBadGateway, "codex_stream_incomplete", "Codex stream ended before response.completed")
			}
			return response, textBuilder.String(), usage, NewHTTPError(http.StatusBadGateway, "codex_stream_failed", err.Error())
		}
	}
}

func (s *Server) handleStreamingResponses(w http.ResponseWriter, r *http.Request, routed RoutedCall, request ResponsesRequest, auditPayload any) {
	tracker := &streamWriteTracker{writer: w}
	attemptNumber := 0
	allowEffortFallback := normalizedReasoningEffort(responsesReasoningEffort(request)) != nil
	response, route, usage, attempts, err := executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool, attempt int) (map[string]any, Usage, error) {
		attemptNumber = attempt
		prepared, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		adapter, err := s.responsesAdapterForRoute(prepared)
		if err != nil {
			return nil, Usage{}, err
		}
		streamAdapter, ok := adapter.(ResponsesStreamOpener)
		if !ok {
			return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support streaming Responses")
		}
		upstreamRequest := request
		if omitReasoningEffort {
			upstreamRequest = withoutResponsesReasoningEffort(upstreamRequest)
		}
		opened, err := streamAdapter.OpenResponses(ctx, prepared.Provider, prepared.ProviderModel, upstreamRequest, r.Header)
		if isCodexModelUnsupportedError(err) {
			s.removeCodexResourceModel(routeResourceID(route), route.ProviderModel)
		}
		if err != nil {
			return nil, Usage{}, err
		}
		defer opened.Body.Close()
		w.Header().Set("content-type", "text/event-stream")
		w.Header().Set("cache-control", "no-cache")
		w.Header().Set("connection", "keep-alive")
		w.Header().Set("x-accel-buffering", "no")
		w.Header().Set("x-request-id", routed.Call.RequestID)
		writeCodexResponseHeaders(w.Header(), opened.Header)
		s.writeRouteHeaders(w, routed.Call, prepared, attemptNumber)
		tracker.ensureStarted()
		response, _, usage, streamErr := consumeCodexResponsesStream(opened.Body, tracker)
		applyCodexResponseMetadata(&usage, opened.Header)
		if streamErr != nil {
			return response, usage, &ProviderInvocationError{
				Err:         streamErr,
				Disposition: ProviderErrorStreamCommitted,
			}
		}
		return response, usage, nil
	})
	streamStarted := tracker.Wrote()
	routed.Call.StreamOutputCommitted = tracker.WroteData()
	if err != nil {
		if streamStarted {
			// The client already has a 200, so any usage the upstream reported
			// before failing is real and must not be discarded.
			httpErr := AsHTTPError(err)
			s.finishRoutedCall(r, GatewayCallCompletion{
				Call:            routed.Call,
				Route:           route,
				Usage:           usage,
				Attempts:        attempts,
				StatusCode:      httpErr.Status,
				ErrorCode:       httpErr.Code,
				ErrorMessage:    httpErr.Message,
				RequestPayload:  auditPayload,
				ResponsePayload: auditErrorPayload(err, routed.Call.RequestID),
			})
		} else {
			s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		}
		if !streamStarted {
			writeError(w, r, err)
		}
		return
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, response)
}

func codexStreamEventError(event map[string]any) error {
	payload := map[string]any{"message": "Codex response failed"}
	if errorPayload, ok := event["error"].(map[string]any); ok {
		payload = errorPayload
	}
	if response, ok := event["response"].(map[string]any); ok {
		if errorPayload, ok := response["error"].(map[string]any); ok {
			payload = errorPayload
		}
	}
	encoded, _ := json.Marshal(map[string]any{"error": payload})
	return classifyCodexHTTPError(http.StatusBadGateway, nil, encoded)
}

func codexResponseOutputText(response map[string]any) string {
	if value, ok := response["output_text"].(string); ok {
		return value
	}
	var result strings.Builder
	outputs, _ := response["output"].([]any)
	for _, output := range outputs {
		item, _ := output.(map[string]any)
		content, _ := item["content"].([]any)
		for _, part := range content {
			payload, _ := part.(map[string]any)
			if payload["type"] == "output_text" {
				if value, ok := payload["text"].(string); ok {
					result.WriteString(value)
				}
			}
		}
	}
	return result.String()
}

func (a CodexSubscriptionAdapter) DefaultProbeRequest() ProviderProbeRequest {
	return ProviderProbeRequest{
		Model:           openAICodexDefaultProbeModel,
		ReasoningEffort: "medium",
		Speed:           "standard",
		Prompt:          "Reply with exactly one short sentence confirming that the Codex connection works.",
	}
}

func (a CodexSubscriptionAdapter) Probe(ctx context.Context, provider Provider, resource ProviderResource, request ProviderProbeRequest) (ProviderProbeResult, error) {
	defaults := a.DefaultProbeRequest()
	request.Model = strings.TrimSpace(request.Model)
	request.ReasoningEffort = strings.ToLower(strings.TrimSpace(request.ReasoningEffort))
	request.Speed = strings.ToLower(strings.TrimSpace(request.Speed))
	request.Prompt = strings.TrimSpace(request.Prompt)
	if request.Model == "" {
		request.Model = defaults.Model
	}
	if request.ReasoningEffort == "" {
		request.ReasoningEffort = defaults.ReasoningEffort
	}
	if request.Speed == "" {
		request.Speed = defaults.Speed
	}
	if request.Prompt == "" {
		request.Prompt = defaults.Prompt
	}
	if models, _, cached := codexResourceCachedModels(&resource); cached && !codexModelInList(request.Model, models) {
		return ProviderProbeResult{}, NewHTTPError(http.StatusBadRequest, "codex_model_invalid", "Select a supported Codex model")
	}
	if !stringInList(request.ReasoningEffort, []string{"none", "minimal", "low", "medium", "high", "xhigh"}) {
		return ProviderProbeResult{}, NewHTTPError(http.StatusBadRequest, "codex_reasoning_effort_invalid", "Select a supported reasoning effort")
	}
	if request.Speed != "standard" && request.Speed != "fast" {
		return ProviderProbeResult{}, NewHTTPError(http.StatusBadRequest, "codex_speed_invalid", "Speed must be standard or fast")
	}
	if provider.Options == nil {
		provider.Options = map[string]string{}
	}
	provider.Options = mergedStringMap(provider.Options, resource.Options)
	provider.Options["resource_id"] = resource.ID
	reasoningEffort := request.ReasoningEffort
	responsesRequest := ResponsesRequest{
		Model: request.Model,
		Input: []map[string]any{{
			"role":    "user",
			"content": []map[string]any{{"type": "input_text", "text": request.Prompt}},
		}},
		Reasoning: &ResponsesReasoning{Effort: &reasoningEffort},
	}
	if request.Speed == "fast" {
		responsesRequest.ServiceTier = "priority"
	}
	startedAt := time.Now()
	resp, err := a.OpenResponses(ctx, provider, request.Model, responsesRequest, nil)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	defer resp.Body.Close()
	response, outputText, usage, err := consumeCodexResponsesStream(resp.Body, nil)
	applyCodexResponseMetadata(&usage, resp.Header)
	if err != nil {
		return ProviderProbeResult{}, err
	}
	return ProviderProbeResult{
		ResourceID:          resource.ID,
		Model:               request.Model,
		ReasoningEffort:     request.ReasoningEffort,
		Speed:               request.Speed,
		UpstreamServiceTier: stringFromMap(response, "service_tier"),
		OutputText:          outputText,
		Usage:               usage,
		LatencyMS:           time.Since(startedAt).Milliseconds(),
		Response:            response,
	}, nil
}

func stringFromMap(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func (s *Server) providerByID(providerID string) (Provider, bool) {
	for _, provider := range s.store.ListProviders() {
		if provider.ID == providerID {
			return provider, true
		}
	}
	return Provider{}, false
}

func stringInList(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
