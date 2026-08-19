package server

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req ChatCompletionRequest
	if err := s.decodeJSONLimit(w, r, &req, s.config.MaxMultimodalRequestBytes); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, req.Model, req.Stream, requestTokenReservation(req))
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, req.Model, req.Stream, err, guardrailAuditSummary{Model: req.Model})
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	decision, err := s.evaluateOutboundGuardrails(r.Context(), call.Project.ID, chatGuardrailTargets(&req))
	auditPayload := guardrailRequestAuditPayload(req.Model, decision, req)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}

	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, req.Model, auditPayload)
	if !ok {
		return
	}
	routed, err = compatibleChatRoutes(routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}

	affinity, err := s.chatGatewayAffinity(key.ID, r.Header, req, routed.Routes)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if affinity != nil {
		routed.Affinity = affinity
		routed.Call.Affinity = affinity
		routed.Routes = s.planRouteOrder(routed.Call, routed.Routes)
	}

	if req.Stream {
		tracker := &streamWriteTracker{writer: w}
		allowEffortFallback := normalizedReasoningEffort(req.ReasoningEffort) != nil
		_, route, usage, attempts, streamErr := executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback,
			func(ctx context.Context, candidate RouteSelection, omitReasoningEffort bool, attempt int) (struct{}, Usage, error) {
				prepared, prepareErr := s.prepareRouteForUpstream(ctx, candidate)
				if prepareErr != nil {
					return struct{}{}, Usage{}, prepareErr
				}
				upstreamReq := req
				if omitReasoningEffort {
					upstreamReq.ReasoningEffort = nil
				}
				// Defer the response headers until the first byte is written, at
				// which point prepared is the route that actually served it.
				tracker.onFirstWrite = func() {
					w.Header().Set("content-type", "text/event-stream")
					w.Header().Set("cache-control", "no-cache")
					w.Header().Set("x-request-id", routed.Call.RequestID)
					s.writeRouteHeaders(w, routed.Call, prepared, attempt)
				}
				streamUsage, err := s.streamChatRoute(ctx, prepared, upstreamReq, r.Header, tracker)
				return struct{}{}, streamUsage, classifyStreamError(ctx, err, tracker.Wrote())
			})

		status, code := statusAndCode(streamErr)
		if streamErr == nil {
			// An upstream may complete with 200 and an empty body, in which case
			// onFirstWrite never ran and the client would receive none of the
			// streaming headers.
			tracker.ensureStarted()
			s.store.MarkRouteUsed(route.Route.ID)
			s.store.MarkProviderResourceUsed(routeResourceID(route))
		}
		routed.Call.StreamOutputCommitted = tracker.WroteData()
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
			// Nothing reached the client, so the response is a plain JSON error.
			// Still emit routing headers here: onFirstWrite never ran, and callers
			// rely on these headers to see how many candidates were attempted.
			w.Header().Del("cache-control")
			s.writeRouteHeaders(w, routed.Call, lastAttemptRoute(attempts), len(attempts))
			writeError(w, r, streamErr)
		}
		return
	}

	resp, route, usage, attempts, err := s.executeRoutedChat(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req ResponsesRequest
	if err := s.decodeJSONLimit(w, r, &req, s.config.MaxMultimodalRequestBytes); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	if req.Background {
		s.submitResponseJob(w, r, project, key, req)
		return
	}
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, req.Model, req.Stream, requestTokenReservation(req))
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, req.Model, req.Stream, err, guardrailAuditSummary{Model: req.Model})
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	decision, err := s.evaluateOutboundGuardrails(r.Context(), call.Project.ID, responsesGuardrailTargets(&req))
	auditPayload := guardrailRequestAuditPayload(req.Model, decision, req)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, req.Model, auditPayload)
	if !ok {
		return
	}
	routed.Routes = s.routesWithAdapterCapability(routed.Routes, AdapterCapabilityResponses)
	if len(routed.Routes) == 0 {
		err := NewHTTPError(
			http.StatusNotImplemented,
			"provider_capability_not_supported",
			"Responses are not supported",
		)
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if req.Stream {
		routed.Routes = s.routesWithAdapterCapability(routed.Routes, AdapterCapabilityResponseStream)
		if len(routed.Routes) == 0 {
			err := NewHTTPError(
				http.StatusNotImplemented,
				"provider_capability_not_supported",
				"Streaming responses are not supported",
			)
			s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
			writeError(w, r, err)
			return
		}
	}
	affinity, err := resolveCodexSessionAffinity(s.config.SecretKey, key.ID, r.Header, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	codexAffinityApplied := affinity != nil && routesContainAdapterType(routed.Routes, ProviderOpenAICodex)
	if codexAffinityApplied {
		routed.Affinity = affinity
		routed.Call.Affinity = affinity
		routed.Routes = s.planRouteOrder(routed.Call, routed.Routes)
	}
	if !codexAffinityApplied {
		affinity, err = s.responsesCacheLocalityAffinity(key.ID, r.Header, req)
		if err != nil {
			s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
			writeError(w, r, err)
			return
		}
		if affinity != nil {
			routed.Affinity = affinity
			routed.Call.Affinity = affinity
			routed.Routes = s.planRouteOrder(routed.Call, routed.Routes)
		}
	}
	if req.Stream {
		s.handleStreamingResponses(w, r, routed, req, auditPayload)
		return
	}
	resp, route, usage, attempts, err := s.executeRoutedResponses(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	writeCodexResponseHeaders(w.Header(), usage.ResponseHeaders)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var request map[string]json.RawMessage
	if err := s.decodeJSONLimit(w, r, &request, s.config.MaxMultimodalRequestBytes); err != nil {
		writeError(w, r, err)
		return
	}
	var model string
	if value, ok := request["model"]; ok {
		_ = json.Unmarshal(value, &model)
	}
	model = strings.TrimSpace(model)
	if model == "" {
		writeError(w, r, NewHTTPError(http.StatusBadRequest, "missing_model", "model is required"))
		return
	}
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, model, false, requestTokenReservation(request))
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, model, false, err, guardrailAuditSummary{Model: model})
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	decision, err := s.evaluateOutboundGuardrails(r.Context(), call.Project.ID, responsesCompactGuardrailTargets(request))
	auditPayload := guardrailRequestAuditPayload(model, decision, request)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, model, auditPayload)
	if !ok {
		return
	}
	affinityRequest := ResponsesRequest{Model: model, raw: request}
	affinity, err := resolveCodexSessionAffinity(s.config.SecretKey, key.ID, r.Header, affinityRequest)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if affinity != nil && routesContainAdapterType(routed.Routes, ProviderOpenAICodex) {
		routed.Affinity = affinity
		routed.Call.Affinity = affinity
		routed.Routes = s.planRouteOrder(routed.Call, routed.Routes)
	}
	response, route, usage, attempts, err := s.executeRoutedCompact(r, routed, request)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, response)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	writeCodexResponseHeaders(w.Header(), usage.ResponseHeaders)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleEmbeddings(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req EmbeddingsRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	routed, ok := s.startRoutedCall(w, r, project, key, req.Model, false, req)
	if !ok {
		return
	}
	resp, route, usage, attempts, err := s.executeRoutedEmbeddings(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, req)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, req, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) startRoutedCall(w http.ResponseWriter, r *http.Request, project Project, key APIKey, model string, stream bool, requestPayload any) (RoutedCall, bool) {
	return s.startRoutedCallWithAudit(w, r, project, key, model, stream, requestPayload, requestPayload)
}

func (s *Server) startRoutedCallWithAudit(w http.ResponseWriter, r *http.Request, project Project, key APIKey, model string, stream bool, requestPayload any, auditPayload any) (RoutedCall, bool) {
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, model, stream, requestTokenReservation(requestPayload))
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, model, stream, err, auditPayload)
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return RoutedCall{}, false
	}
	return s.prepareAdmittedRoutedCallWithAudit(w, r, call, model, auditPayload)
}

func (s *Server) admitRoutedCall(w http.ResponseWriter, r *http.Request, project Project, key APIKey, model string, stream bool, tokenReservation int64) (CallContext, error) {
	call, err := s.store.StartCall(r.Context(), project, key, model, tokenReservation)
	call.Stream = stream
	if err != nil {
		return CallContext{}, err
	}
	w.Header().Set("x-request-id", call.RequestID)
	writeRateLimitHeaders(w.Header(), call.RateLimitHeaders)
	if call.requestContext != nil {
		*r = *r.WithContext(call.requestContext)
	}
	return call, nil
}

func (s *Server) prepareAdmittedRoutedCallWithAudit(w http.ResponseWriter, r *http.Request, call CallContext, model string, auditPayload any) (RoutedCall, bool) {
	routed, err := s.prepareAdmittedRoutedCall(r.Context(), call, model)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: routed.Call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return RoutedCall{}, false
	}
	return routed, true
}

func (s *Server) prepareAdmittedRoutedCall(ctx context.Context, call CallContext, model string) (RoutedCall, error) {
	routes, err := s.store.SelectRouteCandidates(model)
	if err != nil {
		err = s.annotateRoutingPolicyForCandidateError(&call, err)
		return RoutedCall{Call: call}, err
	}
	routes, err = s.filterCodexRoutesByModel(ctx, model, routes)
	if err != nil {
		err = s.annotateRoutingPolicyForCandidateError(&call, err)
		return RoutedCall{Call: call}, err
	}
	var resolution RoutingPolicyResolution
	routes, resolution, err = s.resolveScopedRoutingPolicy(call, routes)
	applyRoutingPolicyResolution(&call, resolution)
	if err != nil {
		return RoutedCall{Call: call}, err
	}
	return RoutedCall{Call: call, Routes: s.planRouteOrder(call, routes)}, nil
}

func (s *Server) executeRoutedPlaygroundChat(r *http.Request, routed RoutedCall, req ChatCompletionRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	allowEffortFallback := normalizedReasoningEffort(req.ReasoningEffort) != nil
	return executeRoutedWithStore(r.Context(), s.store, routed, allowEffortFallback, func(ctx context.Context, route RouteSelection, omitReasoningEffort bool, _ int) (any, Usage, error) {
		responsesReq, useResponses, err := playgroundResponsesRequestForRoute(route, req)
		if err != nil {
			return nil, Usage{}, err
		}
		route, err = s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		if useResponses {
			upstreamReq := responsesReq
			if omitReasoningEffort {
				upstreamReq = withoutResponsesReasoningEffort(upstreamReq)
			}
			resp, usage, err := s.invokeResponsesAdapter(ctx, route, upstreamReq, r.Header)
			if isCodexModelUnsupportedError(err) {
				s.removeCodexResourceModel(routeResourceID(route), route.ProviderModel)
			}
			return resp, usage, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		upstreamReq := req
		if omitReasoningEffort {
			upstreamReq.ReasoningEffort = nil
		}
		return adapter.Chat(ctx, route.Provider, route.ProviderModel, upstreamReq)
	})
}

func (s *Server) executeRoutedResponses(r *http.Request, routed RoutedCall, req ResponsesRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return s.executeRoutedResponsesContext(r.Context(), r.Header, routed, req)
}

func (s *Server) invokeResponsesAdapter(ctx context.Context, route RouteSelection, req ResponsesRequest, incoming http.Header) (any, Usage, error) {
	adapter, err := s.responsesAdapterForRoute(route)
	if err != nil {
		return nil, Usage{}, err
	}
	if envelopeAdapter, ok := adapter.(ResponsesEnvelopeAdapter); ok {
		return envelopeAdapter.ResponsesWithHeaders(ctx, route.Provider, route.ProviderModel, req, incoming)
	}
	if responsesAdapter, ok := adapter.(ResponsesInvoker); ok {
		return responsesAdapter.Responses(ctx, route.Provider, route.ProviderModel, req)
	}
	return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support Responses")
}

func (s *Server) executeRoutedCompact(r *http.Request, routed RoutedCall, request map[string]json.RawMessage) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
		prepared, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		adapter, err := s.responsesAdapterForRoute(prepared)
		if err != nil {
			return nil, Usage{}, err
		}
		compactAdapter, ok := adapter.(ResponsesCompactAdapter)
		if !ok {
			return nil, Usage{}, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support Responses compact")
		}
		body := make(map[string]json.RawMessage, len(request))
		for key, value := range request {
			body[key] = append(json.RawMessage(nil), value...)
		}
		return compactAdapter.CompactWithHeaders(ctx, prepared.Provider, prepared.ProviderModel, body, r.Header)
	})
}

func (s *Server) executeRoutedEmbeddings(r *http.Request, routed RoutedCall, req EmbeddingsRequest) (any, RouteSelection, Usage, []RouteAttempt, error) {
	return executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (any, Usage, error) {
		route, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return nil, Usage{}, err
		}
		adapter, err := s.adapterForRoute(route)
		if err != nil {
			return nil, Usage{}, err
		}
		return adapter.Embeddings(ctx, route.Provider, route.ProviderModel, req)
	})
}

func (s *Server) handleAdminPlaygroundChat(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireAdmin(w, r, "playground", r.Method)
	if !ok {
		return
	}
	var playgroundReq playgroundChatRequest
	if err := s.decodeJSONLimit(w, r, &playgroundReq, s.config.MaxMultimodalRequestBytes); err != nil {
		writeError(w, r, err)
		return
	}
	req := playgroundReq.ChatCompletionRequest
	if err := validatePlaygroundRequest(&req); err != nil {
		writeError(w, r, err)
		return
	}
	req.Stream = false
	req.StreamOptions = nil
	guardrailProjectID, err := s.resolvePlaygroundGuardrailProjectID(user, playgroundReq.ProjectID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	startedAt := time.Now()
	call := s.newPlaygroundCallContext(user, req.Model, startedAt)
	requestID := call.RequestID
	w.Header().Set("x-request-id", requestID)
	decision, guardrailErr := s.evaluateOutboundGuardrails(r.Context(), guardrailProjectID, chatGuardrailTargets(&req))
	requestAuditPayload := guardrailRequestAuditPayload(req.Model, decision, playgroundAuditRequest(req))
	if guardrailErr != nil {
		httpErr := AsHTTPError(guardrailErr)
		s.finishRoutedCall(r, GatewayCallCompletion{
			Kind: CompletionKindPlayground, Call: call, StatusCode: httpErr.Status,
			ErrorCode: httpErr.Code, ErrorMessage: httpErr.Message, RequestPayload: requestAuditPayload,
			ResponsePayload: auditErrorPayload(guardrailErr, requestID),
		})
		s.recordAdminAudit(r, user, "chat_failed", "playground", req.Model, "", map[string]any{
			"model": req.Model, "attempts": []PlaygroundRouteAttempt{}, "error": httpErr.Code,
		})
		writeError(w, r, guardrailErr)
		return
	}
	routed := RoutedCall{Call: call}
	finishRoutingFailure := func(routeErr error) {
		routeErr = s.annotateRoutingPolicyForCandidateError(&routed.Call, routeErr)
		httpErr := AsHTTPError(routeErr)
		s.finishRoutedCall(r, GatewayCallCompletion{
			Kind: CompletionKindPlayground, Call: routed.Call, StatusCode: httpErr.Status,
			ErrorCode: httpErr.Code, ErrorMessage: httpErr.Message, RequestPayload: requestAuditPayload,
			ResponsePayload: auditErrorPayload(routeErr, requestID),
		})
		writeError(w, r, routeErr)
	}
	routes, err := s.store.SelectRouteCandidates(req.Model)
	if err != nil {
		finishRoutingFailure(err)
		return
	}
	routes, err = s.filterCodexRoutesByModel(r.Context(), req.Model, routes)
	if err != nil {
		finishRoutingFailure(err)
		return
	}
	routes, resolution, err := s.resolveScopedRoutingPolicy(routed.Call, routes)
	applyRoutingPolicyResolution(&routed.Call, resolution)
	if err != nil {
		httpErr := AsHTTPError(err)
		s.finishRoutedCall(r, GatewayCallCompletion{
			Kind: CompletionKindPlayground, Call: routed.Call, StatusCode: httpErr.Status,
			ErrorCode: httpErr.Code, ErrorMessage: httpErr.Message, RequestPayload: requestAuditPayload,
			ResponsePayload: auditErrorPayload(err, requestID),
		})
		writeError(w, r, err)
		return
	}
	routed.Routes = s.planRouteOrder(routed.Call, routes)
	resp, route, usage, attempts, err := s.executeRoutedPlaygroundChat(r, routed, req)
	if err != nil {
		httpErr := AsHTTPError(err)
		route = lastAttemptRoute(attempts)
		s.finishRoutedCall(r, GatewayCallCompletion{
			Kind:            CompletionKindPlayground,
			Call:            routed.Call,
			Route:           route,
			Attempts:        attempts,
			StatusCode:      httpErr.Status,
			ErrorCode:       httpErr.Code,
			ErrorMessage:    httpErr.Message,
			RequestPayload:  requestAuditPayload,
			ResponsePayload: auditErrorPayload(err, requestID),
		})
		s.recordAdminAudit(r, user, "chat_failed", "playground", req.Model, "", map[string]any{
			"model":    req.Model,
			"attempts": playgroundRouteAttempts(attempts),
			"error":    httpErr.Code,
		})
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	usage = priceUsage(routed.Call.Model, usage)
	s.finishRoutedCall(r, GatewayCallCompletion{
		Kind:            CompletionKindPlayground,
		Call:            routed.Call,
		Route:           route,
		Usage:           usage,
		Attempts:        attempts,
		StatusCode:      http.StatusOK,
		RequestPayload:  requestAuditPayload,
		ResponsePayload: resp,
	})
	s.recordAdminAudit(r, user, "chat", "playground", req.Model, "", map[string]any{
		"model":    req.Model,
		"route":    playgroundRouteSummary(route),
		"usage":    usage,
		"attempts": len(attempts),
	})
	w.Header().Set("x-request-id", requestID)
	writeJSON(w, http.StatusOK, PlaygroundChatResponse{
		Response:  resp,
		Route:     playgroundRouteForUser(user, route, usage),
		Usage:     playgroundUsageForUser(user, usage),
		Attempts:  playgroundAttemptsForUser(user, attempts),
		RequestID: requestID,
	})
}

func executeRoutedWithStore[T any](
	ctx context.Context,
	store Store,
	routed RoutedCall,
	allowReasoningEffortFallback bool,
	// call receives the 1-based attempt number, counted across every candidate
	// including ones that never ran because capacity acquisition failed. Callbacks
	// must not derive it locally: those failures are appended to attempts here
	// without invoking the callback, so a local counter would undercount them.
	call func(context.Context, RouteSelection, bool, int) (T, Usage, error),
) (T, RouteSelection, Usage, []RouteAttempt, error) {
	var zero T
	var lastErr error = ErrProviderMissing
	var cumulativeTokens int64
	var affinityBindings map[string]AdapterSessionBinding
	var err error
	routed, affinityBindings, err = applyAdapterSessionAffinity(ctx, store, routed)
	if err != nil {
		return zero, RouteSelection{}, Usage{}, nil, err
	}
	attempts := make([]RouteAttempt, 0, len(routed.Routes)+1)
	for _, route := range routed.Routes {
		if leaseErr := coordinationLeaseError(ctx); leaseErr != nil {
			return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, leaseErr
		}
		resourceID := routeResourceID(route)
		binding, hasBinding := affinityBindings[route.Provider.ID]
		routeIsBound := hasBinding && binding.ResourceID == resourceID
		leaseID, leaseCtx, err := store.CheckProviderResourceCapacity(ctx, resourceID)
		if err != nil {
			status, code := statusAndCode(err)
			attempts = append(attempts, RouteAttempt{
				Selection:      route,
				Status:         status,
				UpstreamStatus: AsHTTPError(err).upstreamStatusOrZero(),
				ErrorCode:      code,
				Error:          errorMessage(err),
			})
			lastErr = err
			if !shouldFailoverRoutedError(err, routeIsBound) {
				return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, err
			}
			continue
		}
		omitReasoningEffort := false
		for {
			// The attempt window covers the whole routed callback, including stream
			// translation and writing to the client: a slow reader blocks the
			// gateway here, and that backpressure is part of the attempt duration,
			// not of the gateway overhead reported in overhead_seconds.
			attemptStartedAt := time.Now()
			resp, usage, err := call(leaseCtx, route, omitReasoningEffort, len(attempts)+1)
			cumulativeTokens = saturatingAddNonNegative(cumulativeTokens, meteredTokens(usage))
			usage.RateLimitTokens = cumulativeTokens
			attemptEndedAt := time.Now()
			latencyMS := maxInt64(1, attemptEndedAt.Sub(attemptStartedAt).Milliseconds())
			if leaseErr := coordinationLeaseError(leaseCtx); leaseErr != nil {
				err = leaseErr
			}
			// Neither a committed stream nor a client disconnect may be retried,
			// not even via the effort fallback on the same route. These checks are
			// load-bearing: ProviderInvocationError implements Unwrap, so
			// isReasoningEffortRejection sees through the wrapper and would
			// otherwise return true for an error that must not be retried. The
			// disconnect is excluded directly rather than through the client
			// disposition, which an effort rejection now also carries: refusing a
			// bad parameter is a client error, and dropping the effort is exactly
			// how the gateway fixes it.
			disposition := providerErrorDisposition(err)
			retryWithoutEffort := allowReasoningEffortFallback &&
				!omitReasoningEffort &&
				disposition != ProviderErrorStreamCommitted &&
				!clientDisconnected(leaseCtx, err) &&
				isReasoningEffortRejection(err)
			if !retryWithoutEffort {
				finishProviderResourceAttempt(leaseCtx, store, resourceID, leaseID, err, usage)
			}
			status, code := routeAttemptStatusAndCode(err, retryWithoutEffort)
			attempts = append(attempts, RouteAttempt{
				Selection:      route,
				Status:         status,
				UpstreamStatus: AsHTTPError(err).upstreamStatusOrZero(),
				ErrorCode:      code,
				Error:          errorMessage(err),
				Invoked:        true,
				LatencyMS:      latencyMS,
				// Priced per attempt so a failover reports what each candidate cost
				// rather than attributing the whole request to the winner. Tokens
				// burned by an attempt that later failed were still billed.
				Usage:     priceUsage(routed.Call.Model, usage),
				StartedAt: attemptStartedAt.UTC(),
				EndedAt:   attemptEndedAt.UTC(),
			})
			if err == nil {
				rebindReason := ""
				if binding, ok := affinityBindings[route.Provider.ID]; ok && binding.ResourceID != resourceID {
					rebindReason = "resource_failover"
				}
				if bindErr := commitAdapterSessionAffinity(ctx, store, routed, affinityBindings, route, rebindReason); bindErr != nil {
					return zero, route, usage, attempts, bindErr
				}
				return resp, route, usage, attempts, nil
			}
			lastErr = err
			if retryWithoutEffort {
				if retryErr := store.CheckProviderResourceRetryCapacity(leaseCtx, resourceID, leaseID); retryErr != nil {
					store.ReleaseProviderResourceCapacity(resourceID, leaseID)
					status, code = statusAndCode(retryErr)
					attempts = append(attempts, RouteAttempt{
						Selection:      route,
						Status:         status,
						UpstreamStatus: AsHTTPError(retryErr).upstreamStatusOrZero(),
						ErrorCode:      code,
						Error:          errorMessage(retryErr),
					})
					lastErr = retryErr
					if !shouldFailoverRoutedError(retryErr, routeIsBound) {
						return zero, route, Usage{RateLimitTokens: cumulativeTokens}, attempts, retryErr
					}
					break
				}
				omitReasoningEffort = true
				continue
			}
			if !shouldFailoverRoutedError(err, routeIsBound) {
				return zero, route, usage, attempts, err
			}
			break
		}
	}
	return zero, RouteSelection{}, Usage{RateLimitTokens: cumulativeTokens}, attempts, lastErr
}

// clientDisconnected reports that there is no longer anyone to answer. The
// context is consulted as well as the error because classifyStreamError marks a
// mid-stream disconnect by its disposition without the error itself carrying a
// context.Canceled chain.
func clientDisconnected(ctx context.Context, err error) bool {
	return errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled))
}

func routeAttemptStatusAndCode(err error, reasoningEffortRejected bool) (int, string) {
	if !reasoningEffortRejected {
		return statusAndCode(err)
	}
	httpErr := AsHTTPError(err)
	return httpErr.UpstreamStatus, "reasoning_effort_rejected"
}

func coordinationLeaseError(ctx context.Context) error {
	if ctx != nil && errors.Is(context.Cause(ctx), ErrCoordinationLeaseLost) {
		return ErrCoordinationLeaseLost
	}
	return nil
}

func finishProviderResourceAttempt(ctx context.Context, store Store, resourceID string, leaseID string, err error, usage Usage) {
	if resourceID == "" {
		return
	}
	if errors.Is(err, ErrCoordinationLeaseLost) {
		store.ReleaseProviderResourceCapacity(resourceID, leaseID)
		return
	}
	store.FinishProviderResourceAttempt(ctx, resourceID, leaseID, providerAttemptOutcome(err), usage)
}

type streamWriteTracker struct {
	writer       io.Writer
	wrote        bool
	bytesWritten int64
	// onFirstWrite runs once, just before the first byte is written. Response
	// headers must wait until that moment: failover can move to another candidate,
	// and writing early would expose the preferred route rather than the one that
	// actually served the request.
	onFirstWrite func()
}

func (w *streamWriteTracker) Write(data []byte) (int, error) {
	if !w.wrote {
		if w.onFirstWrite != nil {
			w.onFirstWrite()
		}
		if responseWriter, ok := w.writer.(http.ResponseWriter); ok {
			responseWriter.WriteHeader(http.StatusOK)
			if flusher, ok := responseWriter.(http.Flusher); ok {
				flusher.Flush()
			}
		}
	}
	w.wrote = true
	n, err := w.writer.Write(data)
	w.bytesWritten = saturatingAddNonNegative(w.bytesWritten, int64(n))
	return n, err
}

// classifyStreamError decides whether a streaming failure may move to the next
// candidate.
//
// Two cases must never fail over:
//   - The first byte was already written: the client has a 200 and partial events,
//     so switching upstreams would emit two contradictory streams. Note this
//     disposition is not produced automatically and must be wrapped explicitly here.
//   - The client disconnected: retrying only burns the next account's quota.
//     Without this check, a cancellation error falls through to the status >= 500
//     branch at the end of shouldFailoverRoutedError and is mistaken for retryable.
func classifyStreamError(ctx context.Context, err error, wrote bool) error {
	if err == nil {
		return nil
	}
	// Cancellation is checked before commitment. Both dispositions forbid failover,
	// but only ProviderErrorClient counts as healthy in
	// providerAttemptCountsAsHealthy. Classifying a mid-stream client disconnect as
	// StreamCommitted would charge it to the upstream, so repeated disconnects could
	// accumulate failures and cool down a perfectly healthy account.
	if errors.Is(err, context.Canceled) || (ctx != nil && errors.Is(ctx.Err(), context.Canceled)) {
		return &ProviderInvocationError{Err: err, Disposition: ProviderErrorClient}
	}
	if wrote {
		return &ProviderInvocationError{Err: err, Disposition: ProviderErrorStreamCommitted}
	}
	return err
}

func (w *streamWriteTracker) Wrote() bool {
	return w != nil && w.wrote
}

func (w *streamWriteTracker) WroteData() bool {
	return w != nil && w.bytesWritten > 0
}

// ensureStarted runs the deferred hook even when the upstream produced no bytes.
// A 200 response with an empty body would otherwise reach the client with none of
// the headers onFirstWrite installs, including content-type.
func (w *streamWriteTracker) ensureStarted() {
	if w == nil || w.wrote {
		return
	}
	if w.onFirstWrite != nil {
		w.onFirstWrite()
	}
	if responseWriter, ok := w.writer.(http.ResponseWriter); ok {
		responseWriter.WriteHeader(http.StatusOK)
	}
	w.wrote = true
}

func (w *streamWriteTracker) Flush() {
	if w == nil {
		return
	}
	if flusher, ok := w.writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) adapterForRoute(route RouteSelection) (ProviderAdapter, error) {
	adapter, err := s.adapterRegistry.Resolve(route.Provider.Type)
	if err != nil {
		return nil, err
	}
	legacy, ok := adapter.(ProviderAdapter)
	if !ok {
		return nil, NewHTTPError(http.StatusBadRequest, "adapter_capability_unsupported", "Provider adapter does not support this operation")
	}
	return legacy, nil
}

func (s *Server) responsesAdapterForRoute(route RouteSelection) (any, error) {
	return s.adapterRegistry.Resolve(route.Provider.Type)
}

func (s *Server) routesWithAdapterCapability(routes []RouteSelection, capability AdapterCapability) []RouteSelection {
	filtered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if s.routeSupportsAdapterCapability(route, capability) {
			filtered = append(filtered, route)
		}
	}
	return filtered
}

func (s *Server) routeSupportsAdapterCapability(route RouteSelection, capability AdapterCapability) bool {
	descriptor, ok := s.adapterRegistry.Describe(route.Provider.Type)
	if !ok || !adapterSupports(descriptor, capability) {
		return false
	}
	if route.Provider.Type != "deepseek" ||
		(capability != AdapterCapabilityResponses && capability != AdapterCapabilityResponseStream) {
		return true
	}
	// DeepSeek's Responses API is model-scoped rather than provider-scoped. Keep
	// this allowlist aligned with the models advertised by the upstream endpoint.
	model := strings.ToLower(strings.TrimSpace(route.ProviderModel))
	return model == "deepseek-v4-flash" || model == "deepseek-v4-pro"
}

func (s *Server) planRouteOrder(call CallContext, routes []RouteSelection) []RouteSelection {
	ordered := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		if routeMatchesProject(route.Route, call.Project.ID) {
			ordered = append(ordered, route)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Route.Priority != ordered[j].Route.Priority {
			return ordered[i].Route.Priority < ordered[j].Route.Priority
		}
		if routeResourcePriority(ordered[i]) != routeResourcePriority(ordered[j]) {
			return routeResourcePriority(ordered[i]) < routeResourcePriority(ordered[j])
		}
		if routeWeight(ordered[i].Route) != routeWeight(ordered[j].Route) {
			return routeWeight(ordered[i].Route) > routeWeight(ordered[j].Route)
		}
		return routeSortID(ordered[i]) < routeSortID(ordered[j])
	})

	var planned []RouteSelection
	for len(ordered) > 0 {
		priority := ordered[0].Route.Priority
		end := 0
		for end < len(ordered) && ordered[end].Route.Priority == priority {
			end++
		}
		priorityGroup := append([]RouteSelection(nil), ordered[:end]...)
		for len(priorityGroup) > 0 {
			resourcePriority := routeResourcePriority(priorityGroup[0])
			groupEnd := 0
			for groupEnd < len(priorityGroup) && routeResourcePriority(priorityGroup[groupEnd]) == resourcePriority {
				groupEnd++
			}
			group := append([]RouteSelection(nil), priorityGroup[:groupEnd]...)
			strategy := routeStrategy(group[0].Route)
			applyRouteRuntimeWeights(strategy, group)
			if strategy == RouteStrategyPriorityOnly || strategy == RouteStrategyQuality || strategy == RouteStrategyCost {
				sortRouteGroupByStrategy(strategy, group)
				planned = append(planned, group...)
				priorityGroup = priorityGroup[groupEnd:]
				continue
			}
			cacheLocality := call.Affinity != nil &&
				call.Affinity.KeyHash != "" &&
				call.Affinity.Kind == AffinityKindCacheLocality
			// Sticky pops one candidate out of the group first, which weakens session
			// affinity. Cache locality works at the finer session granularity, so it
			// takes over; Codex's stateful binding relies on sticky choosing the
			// provider first and is left untouched.
			if group[0].Route.StickySession && !cacheLocality {
				index := stickyRouteIndex(call, group)
				planned = append(planned, group[index])
				group = append(group[:index], group[index+1:]...)
			}
			routingKey := call.RequestID
			if call.Affinity != nil && call.Affinity.KeyHash != "" {
				routingKey = call.Affinity.KeyHash
			}
			if call.Affinity != nil && call.Affinity.KeyHash != "" {
				identity := routeSortID
				score := weightedRendezvousScore
				if cacheLocality {
					// Score by cache domain rather than route identity: several routes
					// sharing an account and upstream model score identically and sort
					// adjacently, so failover prefers a sibling whose cache is still warm.
					identity = cacheDomainID
					score = weightedCacheDomainScore
				}
				sortRouteGroupByRendezvous(routingKey, group, identity, score)
				planned = append(planned, group...)
				priorityGroup = priorityGroup[groupEnd:]
				continue
			}
			for len(group) > 0 {
				index := weightedRouteIndex(routingKey, len(planned), group)
				planned = append(planned, group[index])
				group = append(group[:index], group[index+1:]...)
			}
			priorityGroup = priorityGroup[groupEnd:]
		}
		ordered = ordered[end:]
	}
	return planned
}

type rendezvousRouteRanking struct {
	routes []RouteSelection
	scores []float64
}

func (ranking rendezvousRouteRanking) Len() int { return len(ranking.routes) }

func (ranking rendezvousRouteRanking) Less(i, j int) bool {
	if ranking.scores[i] != ranking.scores[j] {
		return ranking.scores[i] > ranking.scores[j]
	}
	return routeSortID(ranking.routes[i]) < routeSortID(ranking.routes[j])
}

func (ranking rendezvousRouteRanking) Swap(i, j int) {
	ranking.routes[i], ranking.routes[j] = ranking.routes[j], ranking.routes[i]
	ranking.scores[i], ranking.scores[j] = ranking.scores[j], ranking.scores[i]
}

func sortRouteGroupByRendezvous(routingKey string, group []RouteSelection, identity func(RouteSelection) string, score func(string, string, int) float64) {
	if len(group) < 2 {
		return
	}
	scores := make([]float64, len(group))
	for index := range group {
		scores[index] = score(routingKey, identity(group[index]), routeEffectiveWeight(group[index]))
	}
	sort.Stable(rendezvousRouteRanking{routes: group, scores: scores})
}

// weightedRendezvousScore scores candidates for Codex session affinity, where
// identity is routeSortID.
//
// FNV-1a is kept deliberately: routeSortID embeds randomly generated route and
// resource IDs, so it has enough entropy and none of the similarity problem that
// cache domain identities have. Changing the hash would reassign every Codex
// session that has no binding yet, and that change is not guarded by
// CACHE_AFFINITY_ENABLED - with the switch off, routing must stay byte-identical
// to the pre-change behaviour.
func weightedRendezvousScore(key string, identity string, weight int) float64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(key))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(identity))
	// Deliberately keeps the original normalisation, without the clamp applied to
	// cache-domain scoring. Adding the clamp here would change scores for
	// near-maximum hashes from -Inf to a large finite value, which contradicts the
	// byte-identical guarantee this function is required to uphold.
	unit := (float64(hash.Sum64()) + 1) / (float64(^uint64(0)) + 2)
	return float64(weight) / -math.Log(unit)
}

// weightedCacheDomainScore scores candidates for cache locality routing, where
// identity is cacheDomainID.
//
// SHA-256 is required here: cacheDomainID values are highly similar (under one
// provider they often differ only in the last few characters), and FNV-1a's
// avalanche is too weak to separate them - measured, three equally weighted
// candidates received 154/66/80 instead of an even 100/100/100.
//
// This is a durable contract: changing the hash, the concatenation order, or the
// scoring formula invalidates every upstream cache at once and must be versioned.
func weightedCacheDomainScore(key string, identity string, weight int) float64 {
	sum := sha256.Sum256(append(append([]byte(key), 0), identity...))
	return cacheDomainScoreFromHash(binary.BigEndian.Uint64(sum[:8]), weight)
}

func cacheDomainScoreFromHash(raw uint64, weight int) float64 {
	// The +1 / +2 keeps unit inside the open interval (0,1): float64 rounding near
	// the maximum can otherwise yield exactly 1, and -math.Log(1) returns negative
	// zero, making the score -Inf so that candidate would sort last forever.
	unit := (float64(raw) + 1) / (float64(^uint64(0)) + 2)
	if unit >= 1 {
		unit = 0.99999999999999988
	}
	return float64(weight) / -math.Log(unit)
}

func routesContainAdapterType(routes []RouteSelection, adapterType string) bool {
	for _, route := range routes {
		if route.Provider.Type == adapterType {
			return true
		}
	}
	return false
}

func sortRouteGroupByStrategy(strategy string, routes []RouteSelection) {
	sort.SliceStable(routes, func(i, j int) bool {
		switch strategy {
		case RouteStrategyQuality:
			if routeQualityScore(routes[i].Route) != routeQualityScore(routes[j].Route) {
				return routeQualityScore(routes[i].Route) > routeQualityScore(routes[j].Route)
			}
		case RouteStrategyCost:
			if routeCostScore(routes[i].Route) != routeCostScore(routes[j].Route) {
				return routeCostScore(routes[i].Route) > routeCostScore(routes[j].Route)
			}
		}
		if routeWeight(routes[i].Route) != routeWeight(routes[j].Route) {
			return routeWeight(routes[i].Route) > routeWeight(routes[j].Route)
		}
		return routeSortID(routes[i]) < routeSortID(routes[j])
	})
}

func stickyRouteIndex(call CallContext, routes []RouteSelection) int {
	if len(routes) <= 1 {
		return 0
	}
	stickyKey := call.Key.ID
	if stickyKey == "" {
		stickyKey = call.Project.ID
	}
	return stableHashInt(stickyKey, len(routes)) % len(routes)
}

func weightedRouteIndex(requestID string, salt int, routes []RouteSelection) int {
	if len(routes) <= 1 {
		return 0
	}
	total := 0
	for _, route := range routes {
		total += routeEffectiveWeight(route)
	}
	if total <= 0 {
		return 0
	}
	needle := stableHashInt(requestID, salt) % total
	for index, route := range routes {
		needle -= routeEffectiveWeight(route)
		if needle < 0 {
			return index
		}
	}
	return len(routes) - 1
}

func stableHashInt(value string, salt int) int {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	_, _ = hash.Write([]byte(":"))
	_, _ = hash.Write([]byte(strconv.Itoa(salt)))
	return int(hash.Sum64() % uint64(^uint(0)>>1))
}

func routeWeight(route ModelRoute) int {
	if route.Weight <= 0 {
		return 1
	}
	return route.Weight
}

func routeEffectiveWeight(route RouteSelection) int {
	if route.Runtime.EffectiveWeight > 0 {
		return route.Runtime.EffectiveWeight
	}
	weight := routeWeight(route.Route)
	switch routeStrategy(route.Route) {
	case RouteStrategyBalanced:
		return maxInt(1, weight+routeQualityScore(route.Route)+routeCostScore(route.Route))
	default:
		return weight
	}
}

func applyRouteRuntimeWeights(strategy string, routes []RouteSelection) {
	if strategy != RouteStrategyAdaptive {
		return
	}
	for index := range routes {
		routes[index].Runtime.EffectiveWeight = routeWeight(routes[index].Route)
	}
	latencies := make([]int64, 0, len(routes))
	for _, route := range routes {
		if route.Runtime.Samples >= 5 && route.Runtime.LatencyMS > 0 {
			latencies = append(latencies, route.Runtime.LatencyMS)
		}
	}
	referenceLatency := float64(0)
	if len(latencies) > 0 {
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		referenceLatency = float64(latencies[len(latencies)/2])
	}
	for index := range routes {
		route := &routes[index]
		if route.Runtime.Samples < 5 {
			continue
		}
		latencyFactor := float64(1)
		if referenceLatency > 0 && route.Runtime.LatencyMS > 0 {
			latencyFactor = clampFloat(referenceLatency/float64(route.Runtime.LatencyMS), 0.25, 4)
		}
		successFactor := clampFloat(route.Runtime.SuccessRate, 0.25, 1)
		baseWeight := routeWeight(route.Route)
		effective := int(math.Round(float64(baseWeight) * latencyFactor * successFactor))
		route.Runtime.EffectiveWeight = routeMinInt(maxInt(1, effective), baseWeight*4)
	}
}

func clampFloat(value float64, minimum float64, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func routeMatchesProject(route ModelRoute, projectID string) bool {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || projectID == "admin_playground" {
		return true
	}
	matched := false
	for _, candidate := range route.ProjectIDs {
		if strings.TrimSpace(candidate) == projectID {
			matched = true
			break
		}
	}
	switch routeProjectScope(route) {
	case RouteProjectScopeInclude:
		return matched
	case RouteProjectScopeExclude:
		return !matched
	default:
		return true
	}
}

func routeProjectScope(route ModelRoute) string {
	scope := strings.ToLower(strings.TrimSpace(route.ProjectScope))
	if scope == RouteProjectScopeInclude || scope == RouteProjectScopeExclude {
		return scope
	}
	return RouteProjectScopeAll
}

func routeQualityScore(route ModelRoute) int {
	if route.QualityScore <= 0 {
		return 50
	}
	if route.QualityScore > 100 {
		return 100
	}
	return route.QualityScore
}

func routeCostScore(route ModelRoute) int {
	if route.CostScore <= 0 {
		return 50
	}
	if route.CostScore > 100 {
		return 100
	}
	return route.CostScore
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func routeMinInt(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func routeResourcePriority(route RouteSelection) int {
	if route.Resource == nil || route.Resource.Priority == 0 {
		return 9999
	}
	return route.Resource.Priority
}

func routeResourceID(route RouteSelection) string {
	if route.Resource != nil {
		return route.Resource.ID
	}
	return route.Route.ProviderResourceID
}

func routeSortID(route RouteSelection) string {
	if resourceID := routeResourceID(route); resourceID != "" {
		return route.Route.ID + ":" + resourceID
	}
	return route.Route.ID
}

func routeStrategy(route ModelRoute) string {
	if strings.TrimSpace(route.Strategy) == "" {
		return RouteStrategyBalanced
	}
	return strings.TrimSpace(route.Strategy)
}

func shouldFailoverRoutedError(err error, routeIsBound bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCoordinationLeaseLost) {
		return false
	}
	// The client is gone; trying the next account only burns its quota. This also
	// covers cancellations surfaced by the capacity check, which never reach
	// classifyStreamError.
	if errors.Is(err, context.Canceled) {
		return false
	}
	switch providerErrorDisposition(err) {
	case ProviderErrorClient, ProviderErrorPolicy, ProviderErrorStreamCommitted:
		return false
	case ProviderErrorTransientSame:
		return !routeIsBound
	case ProviderErrorQuotaExhausted, ProviderErrorAuthBroken, ProviderErrorResourceBroken:
		return true
	case ProviderErrorModelUnsupported:
		return !routeIsBound
	}
	if isCodexModelUnsupportedError(err) {
		return !routeIsBound
	}
	httpErr := AsHTTPError(err)
	if routeIsBound {
		return false
	}
	switch httpErr.Status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return httpErr.Status >= 500
	}
}

func isCodexModelUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code == "codex_model_unsupported" || providerErrorDisposition(err) == ProviderErrorModelUnsupported {
		return true
	}
	if httpErr.Code != "codex_upstream_error" {
		return false
	}
	message := strings.ToLower(httpErr.Message)
	return strings.Contains(message, "model is not supported") ||
		(strings.Contains(message, "model") && strings.Contains(message, "not supported") && strings.Contains(message, "chatgpt account"))
}

func lastAttemptRoute(attempts []RouteAttempt) RouteSelection {
	if len(attempts) == 0 {
		return RouteSelection{}
	}
	return attempts[len(attempts)-1].Selection
}

func playgroundRouteAttempts(attempts []RouteAttempt) []PlaygroundRouteAttempt {
	out := make([]PlaygroundRouteAttempt, 0, len(attempts))
	for _, attempt := range attempts {
		out = append(out, PlaygroundRouteAttempt{
			Route:          playgroundRouteSummary(attempt.Selection),
			Status:         attempt.Status,
			UpstreamStatus: attempt.UpstreamStatus,
			Code:           attempt.ErrorCode,
			Error:          attempt.Error,
			Invoked:        attempt.Invoked,
			LatencyMS:      attempt.LatencyMS,
			Usage:          attempt.Usage,
			StartedAt:      attempt.StartedAt,
			EndedAt:        attempt.EndedAt,
		})
	}
	return out
}

func playgroundRouteSummary(route RouteSelection) PlaygroundRouteSummary {
	summary := PlaygroundRouteSummary{
		RouteID:          route.Route.ID,
		ProviderID:       route.Provider.ID,
		ProviderName:     route.Provider.Name,
		ResourceID:       routeResourceID(route),
		ProviderModel:    route.ProviderModel,
		Priority:         route.Route.Priority,
		ResourcePriority: routeResourcePriority(route),
		Weight:           routeWeight(route.Route),
		QualityScore:     routeQualityScore(route.Route),
		CostScore:        routeCostScore(route.Route),
		Strategy:         routeStrategy(route.Route),
		EffectiveWeight:  routeEffectiveWeight(route),
		Samples:          route.Runtime.Samples,
		SuccessRate:      route.Runtime.SuccessRate,
		LatencyMS:        route.Runtime.LatencyMS,
	}
	if route.Resource != nil {
		summary.ResourceName = route.Resource.Name
	}
	return summary
}

func errorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *Server) writeRouteHeaders(w http.ResponseWriter, call CallContext, route RouteSelection, attempts int) {
	w.Header().Set("x-tokenhub-project-id", call.Project.ID)
	w.Header().Set("x-tokenhub-provider", route.Provider.ID)
	if resourceID := routeResourceID(route); resourceID != "" {
		w.Header().Set("x-tokenhub-provider-resource-id", resourceID)
	}
	w.Header().Set("x-tokenhub-model", route.ProviderModel)
	w.Header().Set("x-tokenhub-route-id", route.Route.ID)
	w.Header().Set("x-tokenhub-route-attempts", strconv.Itoa(attempts))
}

func (s *Server) authenticate(r *http.Request) (Project, APIKey, error) {
	auth := r.Header.Get("authorization")
	if auth != "" {
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			return Project{}, APIKey{}, ErrInvalidAPIKey
		}
		return s.store.ValidateAPIKey(strings.TrimSpace(strings.TrimPrefix(auth, prefix)), s.clientIP(r))
	}
	if apiKey := strings.TrimSpace(r.Header.Get("x-api-key")); apiKey != "" {
		return s.store.ValidateAPIKey(apiKey, s.clientIP(r))
	}
	// The official Google Gen AI SDK, and therefore Gemini CLI in API-key mode,
	// sends its credential in x-goog-api-key when GOOGLE_GEMINI_BASE_URL points
	// at a compatible gateway.
	if apiKey := strings.TrimSpace(r.Header.Get("x-goog-api-key")); apiKey != "" {
		return s.store.ValidateAPIKey(apiKey, s.clientIP(r))
	}
	return Project{}, APIKey{}, ErrInvalidAPIKey
}

// playgroundModel resolves the catalog entry for a playground request so its usage
// is priced like any other call. It falls back to a name-only model when the catalog
// does not know the name, which keeps the console usable against a model that was
// just removed.
func playgroundModel(store Store, name string) Model {
	name = strings.TrimSpace(name)
	for _, model := range store.ListModels() {
		if model.Name == name {
			return model
		}
	}
	return Model{Name: name, Status: StatusActive}
}
