package server

import "net/http"

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
	resp, err = s.runGatewayGuardrailPostHooks(r.Context(), routed.Call, route, resp, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, req)
		writeError(w, r, err)
		return
	}
	resp, err = s.runGatewayResponsePostHooks(r.Context(), routed.Call, route, resp)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, req)
		writeError(w, r, err)
		return
	}
	usage, err = s.runGatewayUsageAttributionHooks(r.Context(), routed.Call, route, resp, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, req)
		writeError(w, r, err)
		return
	}
	attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	s.finishSuccessfulRoutedCall(r, routed, route, usage, attempts, req, resp)
	w.Header().Set("x-request-id", routed.Call.RequestID)
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	writeJSON(w, http.StatusOK, resp)
}
