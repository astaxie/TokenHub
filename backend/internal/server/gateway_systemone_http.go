package server

import (
	"net/http"
	"time"
)

func (s *Server) handleSystemOne(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req SystemOneRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if err := req.validate(); err != nil {
		writeError(w, r, err)
		return
	}
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, req.Model, false, requestTokenReservation(req))
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, req.Model, false, err, guardrailAuditSummary{Model: req.Model})
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAuthContextHooks(r.Context(), &call, r.Header); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewaySystemOneDecodeNormalizeHooks(r.Context(), call, r.Header, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAdmissionHooks(r.Context(), call, r.Header, req, requestTokenReservation(req)); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewaySystemOnePrivacyPreHooks(r.Context(), call, r.Header, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewaySystemOneGuardrailPreHooks(r.Context(), call, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewaySystemOneContextOptimizeHooks(r.Context(), call, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	decision, err := s.evaluateOutboundGuardrails(r.Context(), call.Project.ID, systemOneGuardrailTargets(&req))
	if err == nil && req.keyRedacted {
		err = NewHTTPError(http.StatusForbidden, "guardrail_blocked", "Content policy requires redacting a structural key")
	}
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
	routed.Routes = s.routesWithAdapterCapabilityOrProviderCall(routed.Call, routed.Routes, AdapterCapabilitySystemOne, providerRouteProtocolSystemOne)
	if len(routed.Routes) == 0 {
		err := NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", "System One is not supported")
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	resp, route, usage, attempts, err := s.executeRoutedSystemOne(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	// Keep the winning route's rubric private and unwrap before hooks or serialization.
	result, ok := resp.(systemOneRouteResult)
	if !ok {
		err := invalidSystemOneResponse()
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	resp = result.response
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	resp, err = s.runGatewayResponsePostHooks(r.Context(), routed.Call, route, resp, providerRouteProtocolSystemOne)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	resp, err = s.runGatewayGuardrailPostHooks(r.Context(), routed.Call, route, resp, usage, providerRouteProtocolSystemOne)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	usage, err = s.runGatewayUsageAttributionHooks(r.Context(), routed.Call, route, resp, usage, providerRouteProtocolSystemOne)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if err := validateSystemOneGatewayResponse(resp, req, result.request); err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	if usage.UpstreamRequestID != "" {
		w.Header().Set("x-typesafe-request-id", usage.UpstreamRequestID)
	}
	w.Header().Add("access-control-expose-headers", "x-request-id, x-typesafe-request-id")
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}
