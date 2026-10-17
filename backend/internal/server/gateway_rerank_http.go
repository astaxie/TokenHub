package server

import (
	"net/http"
	"time"
)

func (s *Server) handleRerank(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req RerankRequest
	if err := s.decodeJSON(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	if req.Model == "" {
		writeError(w, r, NewHTTPError(400, "missing_model", "model is required"))
		return
	}
	if err := validateRerankRequest(req); err != nil {
		writeError(w, r, err)
		return
	}
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, req.Model, false, requestTokenReservation(req))
	if err != nil {
		requestID := s.finishRejectedCall(r, admittedAt, project, key, req.Model, false, err, rerankRequestAuditPayload(req))
		w.Header().Set("x-request-id", requestID)
		writeError(w, r, err)
		return
	}
	if call.Model.Modality != "rerank" || (!retrievalPriceConfigured(call.Model, false, false) && !retrievalPriceConfigured(call.Model, true, false)) {
		err := NewHTTPError(400, "rerank_model_not_configured", "Publish a rerank model with an explicit tenant price before calling it")
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, rerankRequestAuditPayload(req))
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAuthContextHooks(r.Context(), &call, r.Header); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, rerankRequestAuditPayload(req))
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayRerankDecodeNormalizeHooks(r.Context(), call, r.Header, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, rerankRequestAuditPayload(req))
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayAdmissionHooks(r.Context(), call, r.Header, req, requestTokenReservation(req)); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, rerankRequestAuditPayload(req))
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayRerankPrivacyPreHooks(r.Context(), call, r.Header, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, rerankRequestAuditPayload(req))
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayRerankGuardrailPreHooks(r.Context(), call, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, rerankRequestAuditPayload(req))
		writeError(w, r, err)
		return
	}
	if err := s.runGatewayRerankContextOptimizeHooks(r.Context(), call, &req); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, rerankRequestAuditPayload(req))
		writeError(w, r, err)
		return
	}
	decision, err := s.evaluateOutboundGuardrails(r.Context(), call.Project.ID, rerankGuardrailTargets(&req))
	if err == nil && len(decision.Replacements) > 0 {
		err = validateRerankGuardrailBounds(req, s.config.MaxJSONRequestBytes)
	}
	auditPayload := rerankRequestAuditPayload(req)
	decision.Replacements = nil
	auditPayload.Guardrail = &decision
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	routed, err := s.prepareAdmittedRoutedCall(r.Context(), call, req.Model)
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: routed.Call}, nil, Usage{DocumentCount: len(req.Documents)}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	routed.Routes = s.pricedRerankRoutes(routed.Call, s.routesWithAdapterCapabilityOrProviderCall(routed.Call, routed.Routes, AdapterCapabilityRerank, providerRouteProtocolRerank))
	if len(routed.Routes) == 0 {
		err := NewHTTPError(http.StatusNotImplemented, "provider_capability_not_supported", "No rerank route has a supported protocol, matching text model inventory, and a configured tenant price")
		s.finishFailedRoutedCall(r, routed, nil, Usage{DocumentCount: len(req.Documents)}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	call = routed.Call
	var cacheRoute RouteSelection
	var resp any
	var usage Usage
	var hit bool
	// Probe every eligible route so results from a previous failover remain reusable.
	for _, candidate := range routed.Routes {
		cacheRoute = candidate
		call.RerankCacheKey = rerankCacheKey(call, candidate, req)
		resp, usage, hit, err = s.runGatewayCacheLookupHooks(r.Context(), call, req)
		usage.DocumentCount = len(req.Documents)
		if err != nil || hit {
			break
		}
	}
	routed.Call = call
	if err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{DocumentCount: len(req.Documents)}, err, auditPayload)
		writeError(w, r, err)
		return
	}
	if hit {
		// Cache plugins can provide their own usage evidence. Validate it against
		// the selected rerank protocol before any response or attribution hooks
		// run; otherwise an invalid native-unit claim can be priced as a valid
		// tenant charge when the request is rejected.
		usage, err = pluginRetrievalUsage(resp, usage, providerRerankProtocol(cacheRoute.Provider) == "cohere")
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		resp, err = s.runGatewayResponsePostHooks(r.Context(), call, cacheRoute, resp, providerRouteProtocolRerank)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		resp, err = s.runGatewayGuardrailPostHooks(r.Context(), call, cacheRoute, resp, usage, providerRouteProtocolRerank)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		usage, err = s.attributeRerankUsage(r.Context(), call, cacheRoute, resp, usage)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		resp, err = validateRerankResult(resp, req)
		if err != nil {
			s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, usage, err, auditPayload)
			writeError(w, r, err)
			return
		}
		s.finishSuccessfulRoutedCall(r, RoutedCall{Call: call}, RouteSelection{}, usage, nil, auditPayload, rerankResponseAuditPayload(req))
		w.Header().Set("x-request-id", call.RequestID)
		w.Header().Set("x-tokenhub-cache", "hit")
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp, route, usage, attempts, err := s.executeRoutedRerank(r, routed, req)
	usage.DocumentCount = len(req.Documents)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	resp, err = s.runGatewayResponsePostHooks(r.Context(), routed.Call, route, resp, providerRouteProtocolRerank)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	resp, err = s.runGatewayGuardrailPostHooks(r.Context(), routed.Call, route, resp, usage, providerRouteProtocolRerank)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	usage, err = s.attributeRerankUsage(r.Context(), routed.Call, route, resp, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	resp, err = validateRerankResult(resp, req)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, auditPayload)
		writeError(w, r, err)
		return
	}
	routed.Call.RerankCacheKey = rerankCacheKey(routed.Call, route, req)
	s.runGatewayCacheWriteHooks(r.Context(), routed.Call, route, req, resp, usage, providerRouteProtocolRerank)
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, auditPayload, rerankResponseAuditPayload(req))
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}
