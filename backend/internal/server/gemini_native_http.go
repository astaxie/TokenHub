package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

func (s *Server) registerModelRoutes() {
	s.mux.HandleFunc("/v1/models", s.handleModels)
	s.mux.HandleFunc("/v1/models/", s.handleModel)
	s.mux.HandleFunc("/v1beta/models", s.handleGeminiModels)
	s.mux.HandleFunc("/v1beta/models/", s.handleGeminiModel)
}

func (s *Server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	models := s.geminiAccessibleModels(key)
	items := make([]any, 0, len(models))
	for _, model := range models {
		items = append(items, geminiModelObject(model))
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": items})
}

func (s *Server) handleGeminiModel(w http.ResponseWriter, r *http.Request) {
	model, action, err := geminiModelPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if action == "" && r.Method == http.MethodGet {
		s.handleGeminiModelGet(w, r, model)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, r, NewHTTPError(http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed"))
		return
	}
	switch action {
	case "generateContent", "streamGenerateContent":
		s.gatewayInFlight(s.handleGeminiGenerate)(w, r)
	case "countTokens":
		s.handleGeminiCountTokens(w, r, model)
	default:
		writeError(w, r, NewHTTPError(http.StatusNotFound, "operation_not_found", "Gemini model operation not found"))
	}
}

func (s *Server) handleGeminiModelGet(w http.ResponseWriter, r *http.Request, modelName string) {
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	for _, model := range s.geminiAccessibleModels(key) {
		if model.Name == modelName || model.ID == modelName {
			writeJSON(w, http.StatusOK, geminiModelObject(model))
			return
		}
	}
	writeError(w, r, NewHTTPError(http.StatusNotFound, "model_not_found", "Model not found"))
}

func (s *Server) handleGeminiCountTokens(w http.ResponseWriter, r *http.Request, model string) {
	_, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if !geminiModelAllowed(s.geminiAccessibleModels(key), model) {
		writeError(w, r, ErrModelNotAllowed)
		return
	}
	var payload map[string]any
	if err := s.decodeJSONLimit(w, r, &payload, s.config.MaxMultimodalRequestBytes); err != nil {
		writeError(w, r, err)
		return
	}
	tokens := estimateAnthropicValueTokens(payload["contents"]) + estimateAnthropicValueTokens(payload["systemInstruction"]) + estimateAnthropicValueTokens(payload["tools"])
	if tokens < 1 {
		tokens = 1
	}
	writeJSON(w, http.StatusOK, map[string]any{"totalTokens": tokens})
}

func (s *Server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request) {
	model, action, err := geminiModelPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	stream := action == "streamGenerateContent"
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var payload map[string]any
	if err := s.decodeJSONLimit(w, r, &payload, s.config.MaxMultimodalRequestBytes); err != nil {
		writeError(w, r, err)
		return
	}
	request, reverseNames, err := geminiToResponsesRequest(model, payload, stream)
	if err != nil {
		writeError(w, r, err)
		return
	}
	routed, ok := s.startRoutedCall(w, r, project, key, model, stream, payload)
	if !ok {
		return
	}
	capability := AdapterCapabilityResponses
	if stream {
		capability = AdapterCapabilityResponseStream
	}
	routed.Routes = s.routesWithAdapterCapability(routed.Routes, capability)
	if len(routed.Routes) == 0 {
		err := NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", "No route supports the Gemini CLI compatibility protocol")
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, payload)
		writeError(w, r, err)
		return
	}
	affinity, err := s.geminiGatewayAffinity(key.ID, r.Header, payload, routed.Routes)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, payload)
		writeError(w, r, err)
		return
	}
	if affinity != nil {
		routed.Affinity = affinity
		routed.Call.Affinity = affinity
		routed.Routes = s.planRouteOrder(routed.Call, routed.Routes)
	}
	if stream {
		s.handleStreamingGemini(w, r, routed, request, payload, reverseNames)
		return
	}
	response, route, usage, attempts, err := s.executeRoutedGemini(r, routed, request, payload)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, payload)
		writeError(w, r, err)
		return
	}
	converted, err := codexResponsesToGemini(response, model, usage, reverseNames)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, payload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, payload, converted)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, converted)
}

func (s *Server) executeRoutedGemini(r *http.Request, routed RoutedCall, request ResponsesRequest, payload map[string]any) (map[string]any, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(responsesReasoningEffort(request)) != nil
	response, route, usage, attempts, err := executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool, _ int) (map[string]any, Usage, error) {
		prepared, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstream := request
		if omitReasoningEffort {
			upstream = withoutResponsesReasoningEffort(upstream)
		}
		response, usage, err := s.invokeResponsesAdapter(ctx, prepared, upstream, geminiCodexCompatibilityHeaders(r.Header, payload))
		if isCodexModelUnsupportedError(err) {
			s.removeCodexResourceModel(routeResourceID(prepared), prepared.ProviderModel)
		}
		if err != nil {
			return nil, usage, err
		}
		body, ok := response.(map[string]any)
		if !ok {
			return nil, usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Responses provider returned an invalid payload")
		}
		return body, usage, nil
	})
	return response, route, usage, attempts, err
}

func (s *Server) handleStreamingGemini(w http.ResponseWriter, r *http.Request, routed RoutedCall, request ResponsesRequest, payload map[string]any, reverseNames map[string]string) {
	tracker := &streamWriteTracker{writer: w}
	_, route, usage, attempts, streamErr := executeRoutedWithStore(r.Context(), s.store, routed, true, func(ctx context.Context, candidate RouteSelection, omitReasoningEffort bool, attempt int) (struct{}, Usage, error) {
		prepared, err := s.prepareRouteForUpstream(ctx, candidate)
		if err != nil {
			return struct{}{}, Usage{}, err
		}
		upstream := request
		if omitReasoningEffort {
			upstream = withoutResponsesReasoningEffort(upstream)
		}
		tracker.onFirstWrite = func() {
			w.Header().Set("content-type", "text/event-stream")
			w.Header().Set("cache-control", "no-cache")
			w.Header().Set("x-request-id", routed.Call.RequestID)
			s.writeRouteHeaders(w, routed.Call, prepared, attempt)
		}
		sink := newCodexGeminiStreamSink(tracker, request.Model, reverseNames)
		usage, err := s.streamCodexCompatibility(ctx, prepared, upstream, geminiCodexCompatibilityHeaders(r.Header, payload), sink)
		return struct{}{}, usage, classifyStreamError(ctx, err, tracker.Wrote())
	})
	status, code := statusAndCode(streamErr)
	if streamErr == nil {
		tracker.ensureStarted()
		s.store.MarkRouteUsed(route.Route.ID)
		s.store.MarkProviderResourceUsed(routeResourceID(route))
	}
	s.finishRoutedCall(r, GatewayCallCompletion{
		Call:            routed.Call,
		Route:           route,
		Usage:           usage,
		Attempts:        attempts,
		StatusCode:      status,
		ErrorCode:       code,
		ErrorMessage:    errorMessageOrEmpty(streamErr),
		RequestPayload:  payload,
		ResponsePayload: auditStreamPayload(status, code, streamErr),
	})
	if streamErr != nil && !tracker.Wrote() {
		w.Header().Del("cache-control")
		s.writeRouteHeaders(w, routed.Call, lastAttemptRoute(attempts), len(attempts))
		writeError(w, r, streamErr)
	}
}

func (s *Server) geminiGatewayAffinity(apiKeyID string, headers http.Header, payload map[string]any, routes []RouteSelection) (*RequestAffinity, error) {
	if !routesContainAdapterType(routes, ProviderOpenAICodex) {
		return nil, nil
	}
	identifier := geminiSessionIdentifier(headers, payload)
	return resolveCodexBridgeAffinity(s.config.SecretKey, apiKeyID, codexBridgeProtocolGemini, identifier)
}

func geminiSessionIdentifier(headers http.Header, payload map[string]any) string {
	for _, value := range []string{headers.Get("x-tokenhub-session-id"), headers.Get("session-id")} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return geminiConversationFingerprint(payload)
}

func geminiCodexCompatibilityHeaders(source http.Header, payload map[string]any) http.Header {
	headers := codexCompatibilityHeaders(source)
	if identifier := geminiSessionIdentifier(source, payload); identifier != "" {
		headers.Set("session-id", codexShortIdentifier(identifier))
	}
	return headers
}

func geminiModelPath(r *http.Request) (string, string, error) {
	escaped := strings.TrimPrefix(r.URL.EscapedPath(), "/v1beta/models/")
	escaped = strings.Trim(escaped, "/")
	if escaped == "" {
		return "", "", NewHTTPError(http.StatusNotFound, "model_not_found", "Model not found")
	}
	decoded, err := url.PathUnescape(escaped)
	if err != nil {
		return "", "", NewHTTPError(http.StatusBadRequest, "invalid_model", "model path parameter is invalid")
	}
	model, action, _ := strings.Cut(decoded, ":")
	model = strings.TrimPrefix(strings.TrimSpace(model), "models/")
	if model == "" {
		return "", "", NewHTTPError(http.StatusBadRequest, "invalid_model", "model path parameter is invalid")
	}
	return model, strings.TrimSpace(action), nil
}

func geminiModelAllowed(models []Model, name string) bool {
	for _, model := range models {
		if model.Name == name || model.ID == name {
			return true
		}
	}
	return false
}

// geminiAccessibleModels only advertises models that this native Gemini
// surface can actually execute. AccessibleModels deliberately answers the
// broader gateway question and may include chat-only routes; Gemini requests
// are translated to Responses and, by contract, are backed by Codex
// subscription resources.
func (s *Server) geminiAccessibleModels(key APIKey) []Model {
	models := s.store.AccessibleModels(key)
	compatible := make([]Model, 0, len(models))
	for _, model := range models {
		if model.Modality == "image" || model.Modality == "embedding" {
			continue
		}
		routes, err := s.store.SelectRouteCandidates(model.Name)
		if err != nil {
			continue
		}
		routes = s.routesWithAdapterCapability(routes, AdapterCapabilityResponses)
		for _, route := range routes {
			if route.Provider.Type == ProviderOpenAICodex && routeMatchesProject(route.Route, key.ProjectID) {
				compatible = append(compatible, model)
				break
			}
		}
	}
	return compatible
}

func geminiModelObject(model Model) map[string]any {
	inputLimit := model.ContextWindow
	if inputLimit <= 0 {
		inputLimit = 128000
	}
	return map[string]any{
		"name":                       "models/" + model.Name,
		"baseModelId":                model.Name,
		"version":                    model.Name,
		"displayName":                model.Name,
		"description":                "TokenHub routed model",
		"inputTokenLimit":            inputLimit,
		"outputTokenLimit":           32768,
		"supportedGenerationMethods": []string{"generateContent", "countTokens"},
	}
}
