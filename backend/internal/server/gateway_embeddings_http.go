package server

import (
	"net/http"
	"time"
)

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
	if err := s.runGatewayEmbeddingsDecodeNormalizeHooks(r.Context(), call, r.Header, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAdmissionHooks(r.Context(), call, r.Header, req, requestTokenReservation(req)); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayEmbeddingsPrivacyPreHooks(r.Context(), call, r.Header, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayEmbeddingsGuardrailPreHooks(r.Context(), call, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayEmbeddingsContextOptimizeHooks(r.Context(), call, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, guardrailAuditSummary{Model: req.Model})
		writeError(w, r, err)
		return
	}
	decision, err := s.evaluateOutboundGuardrails(r.Context(), call.Project.ID, embeddingsGuardrailTargets(&req))
	auditPayload := guardrailRequestAuditPayload(req.Model, decision, req)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	resp, usage, hit, err := s.runGatewayCacheLookupHooks(r.Context(), call, req)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if hit {
		resp, err = s.runGatewayResponsePostHooks(r.Context(), call, RouteSelection{}, resp, providerRouteProtocolEmbeddings)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		resp, err = s.runGatewayGuardrailPostHooks(r.Context(), call, RouteSelection{}, resp, usage, providerRouteProtocolEmbeddings)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		usage, err = s.runGatewayUsageAttributionHooks(r.Context(), call, RouteSelection{}, resp, usage, providerRouteProtocolEmbeddings)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		s.finishSuccessfulRoutedCall(r, RoutedCall{Call: call}, RouteSelection{}, usage, nil, auditPayload, resp)
		w.Header().Set("x-request-id", call.RequestID)
		w.Header().Set("x-tokenhub-cache", "hit")
		writeJSON(w, http.StatusOK, resp)
		return
	}
	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, req.Model, auditPayload)
	if !ok {
		return
	}
	resp, route, usage, attempts, err := s.executeRoutedEmbeddings(r, routed, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	resp, err = s.runGatewayResponsePostHooks(r.Context(), routed.Call, route, resp, providerRouteProtocolEmbeddings)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	resp, err = s.runGatewayGuardrailPostHooks(r.Context(), routed.Call, route, resp, usage, providerRouteProtocolEmbeddings)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	usage, err = s.runGatewayUsageAttributionHooks(r.Context(), routed.Call, route, resp, usage, providerRouteProtocolEmbeddings)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	s.runGatewayCacheWriteHooks(r.Context(), routed.Call, route, req, resp, usage, providerRouteProtocolEmbeddings)
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}
