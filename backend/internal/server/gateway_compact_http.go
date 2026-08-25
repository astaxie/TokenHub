package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

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
	request, err = s.runGatewayCompactDecodeNormalizeHooks(r.Context(), call, r.Header, request)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAdmissionHooks(r.Context(), call, r.Header, request, requestTokenReservation(request)); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
		writeError(w, r, err)
		return
	}
	request, err = s.runGatewayCompactGuardrailPreHooks(r.Context(), call, request)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: model})
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
	response, err = s.runGatewayGuardrailPostHooks(r.Context(), routed.Call, route, response, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	response, err = s.runGatewayResponsePostHooks(r.Context(), routed.Call, route, response)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	usage, err = s.runGatewayUsageAttributionHooks(r.Context(), routed.Call, route, response, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, response)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	writeCodexResponseHeaders(w.Header(), usage.ResponseHeaders)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, response)
}
