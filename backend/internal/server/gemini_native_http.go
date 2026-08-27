package server

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Server) registerModelRoutes() {
	s.registerPublicSingleMethodRoute(http.MethodGet, "/v1/models", s.handleModels, jsonMethodNotAllowed(http.MethodGet))
	s.registerPublicDynamicGETRoute("/v1/models/{model...}", s.handleModelGet, jsonMethodNotAllowed(http.MethodGet))
	s.mux.HandleFunc("/v1/models/", s.handleModel)
	s.registerPublicSingleMethodRoute(http.MethodGet, "/v1beta/models", s.handleGeminiModels, jsonMethodNotAllowed(http.MethodGet))
	s.registerPublicDynamicGETRoute("/v1beta/models/{model...}", s.handleGeminiModelGetRoute, geminiModelHeadFallback)
	s.registerDynamicMethodRoute(http.MethodPost, "/v1beta/models/{model...}", s.handleGeminiModelPostRoute)
	for _, action := range []string{"generateContent", "streamGenerateContent", "countTokens"} {
		s.registerPublicGatewayOperation(http.MethodPost, "/v1beta/models/{model}:"+action)
	}
	s.mux.HandleFunc("/v1beta/models/", s.handleGeminiModel)
}

func (s *Server) handleGeminiModels(w http.ResponseWriter, r *http.Request) {
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
	_, action, err := geminiModelPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	jsonMethodNotAllowed(geminiModelAllowedMethod(action))(w, r)
}

func (s *Server) handleGeminiModelGetRoute(w http.ResponseWriter, r *http.Request) {
	model, action, err := geminiModelPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if action != "" {
		jsonMethodNotAllowed(http.MethodPost)(w, r)
		return
	}
	s.handleGeminiModelGet(w, r, model)
}

func (s *Server) handleGeminiModelPostRoute(w http.ResponseWriter, r *http.Request) {
	model, action, err := geminiModelPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	switch action {
	case "generateContent":
		s.gatewayInFlight(func(w http.ResponseWriter, r *http.Request) {
			s.handleGeminiGenerate(w, r, model, false)
		})(w, r)
	case "streamGenerateContent":
		s.gatewayInFlight(func(w http.ResponseWriter, r *http.Request) {
			s.handleGeminiGenerate(w, r, model, true)
		})(w, r)
	case "countTokens":
		s.handleGeminiCountTokens(w, r, model)
	default:
		writeError(w, r, NewHTTPError(http.StatusNotFound, "operation_not_found", "Gemini model operation not found"))
	}
}

func geminiModelHeadFallback(w http.ResponseWriter, r *http.Request) {
	_, action, err := geminiModelPath(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	jsonMethodNotAllowed(geminiModelAllowedMethod(action))(w, r)
}

func geminiModelAllowedMethod(action string) string {
	if action == "" {
		return http.MethodGet
	}
	return http.MethodPost
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

func (s *Server) handleGeminiGenerate(w http.ResponseWriter, r *http.Request, model string, stream bool) {
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
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, model, stream, requestTokenReservation(payload))
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, model, stream, err, guardrailAuditSummary{Model: model})
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAuthContextHooks(r.Context(), &call, r.Header); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayGeminiDecodeNormalizeHooks(r.Context(), call, r.Header, &payload, model, stream); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAdmissionHooks(r.Context(), call, r.Header, payload, requestTokenReservation(payload)); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayGeminiPrivacyPreHooks(r.Context(), call, r.Header, &payload, model, stream); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayGeminiContextOptimizeHooks(r.Context(), call, &payload, model, stream); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayGeminiGuardrailPreHooks(r.Context(), call, &payload, model, stream); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	decision, err := s.evaluateOutboundGuardrails(r.Context(), call.Project.ID, geminiGuardrailTargets(payload))
	auditPayload := guardrailRequestAuditPayload(model, decision, payload)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if !stream {
		resp, usage, hit, err := s.runGatewayCacheLookupHooks(r.Context(), call, payload)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
			writeError(w, r, err)
			return
		}
		if hit {
			resp, err = s.runGatewayGuardrailPostHooks(r.Context(), call, RouteSelection{}, resp, usage)
			if err != nil {
				s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
				writeError(w, r, err)
				return
			}
			resp, err = s.runGatewayResponsePostHooks(r.Context(), call, RouteSelection{}, resp)
			if err != nil {
				s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
				writeError(w, r, err)
				return
			}
			body, ok := resp.(map[string]any)
			if !ok {
				err := NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response")
				s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
				writeError(w, r, err)
				return
			}
			usage, err = s.runGatewayUsageAttributionHooks(r.Context(), call, RouteSelection{}, body, usage)
			if err != nil {
				s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
				writeError(w, r, err)
				return
			}
			s.finishSuccessfulRoutedCall(r, RoutedCall{Call: call}, RouteSelection{}, usage, nil, auditPayload, body)
			w.Header().Set("x-request-id", call.RequestID)
			w.Header().Set("x-tokenhub-cache", "hit")
			writeJSON(w, http.StatusOK, body)
			return
		}
	}
	request, reverseNames, err := geminiToResponsesRequest(model, payload, stream)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, model, auditPayload)
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
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	affinity, err := s.geminiGatewayAffinity(key.ID, r.Header, payload, routed.Routes)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if affinity != nil {
		routed.Affinity = affinity
		routed.Call.Affinity = affinity
		routed.Routes = s.planRouteOrderWithContext(r.Context(), routed.Call, routed.Routes)
	}
	if stream {
		s.handleStreamingGemini(w, r, routed, request, payload, reverseNames, auditPayload)
		return
	}
	response, route, usage, attempts, err := s.executeRoutedGemini(r, routed, request, payload)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	converted, err := codexResponsesToGemini(response, model, usage, reverseNames)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	postResp, err := s.runGatewayGuardrailPostHooks(r.Context(), routed.Call, route, converted, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	converted, ok = postResp.(map[string]any)
	if !ok {
		err := NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response")
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	postResp, err = s.runGatewayResponsePostHooks(r.Context(), routed.Call, route, converted)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	converted, ok = postResp.(map[string]any)
	if !ok {
		err := NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Gateway plugin returned an invalid response")
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	usage, err = s.runGatewayUsageAttributionHooks(r.Context(), routed.Call, route, converted, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	s.runGatewayCacheWriteHooks(r.Context(), routed.Call, payload, converted, usage)
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, converted)
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
		if transformErr := s.runGatewayResponsesRequestTransformHooksForProtocol(ctx, routed.Call, prepared, &upstream, providerRouteProtocolGemini); transformErr != nil {
			return nil, Usage{}, transformErr
		}
		if resp, usage, handled, err := s.runGatewayProviderCallHooks(ctx, routed.Call, prepared, upstream, providerRouteProtocolGemini); err != nil || handled {
			if err != nil {
				return nil, usage, err
			}
			body, ok := resp.(map[string]any)
			if !ok {
				return nil, usage, NewHTTPError(http.StatusBadGateway, "provider_invalid_response", "Responses provider returned an invalid payload")
			}
			return body, usage, nil
		}
		response, usage, err := s.invokeResponsesAdapter(ctx, prepared, upstream, geminiCodexCompatibilityHeaders(r.Header, payload))
		if providerResourceModelUnsupportedError(err) {
			s.removeProviderResourceModel(routeResourceID(prepared), prepared.ProviderModel)
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

func (s *Server) handleStreamingGemini(w http.ResponseWriter, r *http.Request, routed RoutedCall, request ResponsesRequest, payload map[string]any, reverseNames map[string]string, auditPayload any) {
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
		if transformErr := s.runGatewayResponsesRequestTransformHooksForProtocol(ctx, routed.Call, prepared, &upstream, providerRouteProtocolGemini); transformErr != nil {
			return struct{}{}, Usage{}, transformErr
		}
		tracker.onFirstWrite = func() {
			w.Header().Set("content-type", "text/event-stream")
			w.Header().Set("cache-control", "no-cache")
			w.Header().Set("x-request-id", routed.Call.RequestID)
			s.writeRouteHeaders(w, routed.Call, prepared, attempt)
		}
		streamWriter := io.Writer(tracker)
		var transformer *gatewayStreamTransformWriter
		if s.hasGatewayStreamTransformHooks() {
			transformer = s.newGatewayStreamTransformWriter(ctx, routed.Call, prepared, tracker)
			streamWriter = transformer
		}
		sink := newCodexGeminiStreamSink(streamWriter, request.Model, reverseNames)
		usage, err := s.streamCodexCompatibility(ctx, prepared, upstream, geminiCodexCompatibilityHeaders(r.Header, payload), sink)
		if transformer != nil {
			if closeErr := transformer.Close(); err == nil && closeErr != nil {
				err = closeErr
			}
		}
		return struct{}{}, usage, classifyStreamError(ctx, err, tracker.Wrote())
	})
	status, code := statusAndCode(streamErr)
	if streamErr == nil {
		tracker.ensureStarted()
		s.store.MarkRouteUsed(route.Route.ID)
		s.store.MarkProviderResourceUsed(routeResourceID(route))
	}
	// StreamOutputCommitted stays unwired: it would keep the full TPM
	// reservation on an interrupted stream with no provider token usage
	// (store_routing_calls.go), changing quota behavior beyond this
	// observability-only change. FirstByteAt and StreamFailed only label
	// observability output, so wiring them is safe.
	routed.Call.FirstByteAt = tracker.firstByteTime(streamErr == nil)
	routed.Call.StreamFailed = streamErr != nil && tracker.Wrote()
	s.finishRoutedCall(r, GatewayCallCompletion{
		Call:            routed.Call,
		Route:           route,
		Usage:           usage,
		Attempts:        attempts,
		StatusCode:      status,
		ErrorCode:       code,
		ErrorMessage:    errorMessageOrEmpty(streamErr),
		RequestPayload:  auditPayload,
		ResponsePayload: auditStreamPayload(status, code, streamErr),
	})
	if streamErr != nil && !tracker.Wrote() {
		w.Header().Del("cache-control")
		s.writeRouteHeaders(w, routed.Call, lastAttemptRoute(attempts), len(attempts))
		writeError(w, r, streamErr)
	}
}

func (s *Server) geminiGatewayAffinity(apiKeyID string, headers http.Header, payload map[string]any, routes []RouteSelection) (*RequestAffinity, error) {
	adapterType := s.firstRouteAdapterTypeWithCapability(routes, AdapterCapabilityAffinity)
	if adapterType == "" {
		return nil, nil
	}
	identifier := geminiSessionIdentifier(headers, payload)
	return resolveProviderBridgeAffinity(s.config.SecretKey, apiKeyID, adapterType, adapterSessionAffinityKind(s.adapterRegistry, adapterType), codexBridgeProtocolGemini, identifier)
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
// are translated through the Codex Responses compatibility protocol, which is
// declared by provider plugins instead of inferred from a provider type.
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
			if routeSupportsProviderProtocol(s.adapterRegistry, route, providerRouteProtocolCodexResponses) && routeMatchesProject(route.Route, key.ProjectID) {
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
		"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent", "countTokens"},
	}
}
