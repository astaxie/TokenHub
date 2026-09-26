package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"tokenhub/backend/internal/guardrails"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) registerMediaRoutes() {
	for _, path := range []string{"/v1/audio/speech", "/v1/audio/transcriptions", "/v1/audio/translations", "/v1/images/variations"} {
		s.registerPublicSingleMethodRoute(http.MethodPost, path, s.gatewayInFlight(s.handleMedia), jsonMethodNotAllowed(http.MethodPost))
	}
}

// Preserve managed image jobs and native client aliases, while allowing ordinary
// published image models to use the provider's complete Images API contract.
func (s *Server) handleCompatibleImages(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	limit := max(s.config.MaxMultimodalRequestBytes, int64(maxImageEditRequestBytes))
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		writeError(w, r, NewHTTPError(413, "request_too_large", "Image request exceeds the configured request size limit"))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	request, decodeErr := decodeMediaRequest(w, r, limit)
	model := request.model()
	managed, _ := s.imageGenerationModelsAndDefault()
	alias := imageGenerationRequest{Model: model}
	s.applyImageGenerationRequestAliases(r, &alias)
	if model == "" || imageModelIsSupported(model, managed) || alias.Model != model {
		r.Body = io.NopCloser(bytes.NewReader(data))
		if strings.HasSuffix(r.URL.Path, "/edits") {
			s.handleImageEdits(w, r)
		} else {
			s.handleImageGenerations(w, r)
		}
		return
	}
	if int64(len(data)) > s.config.MaxMultimodalRequestBytes {
		writeError(w, r, NewHTTPError(413, "request_too_large", "Image request exceeds the configured request size limit"))
		return
	}
	if decodeErr != nil {
		writeError(w, r, decodeErr)
		return
	}
	s.handleAdmittedMedia(w, r, project, key, request)
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	project, key, err := s.authenticate(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	request, err := decodeMediaRequest(w, r, s.config.MaxMultimodalRequestBytes)
	if err != nil {
		writeError(w, r, err)
		return
	}
	s.handleAdmittedMedia(w, r, project, key, request)
}

func (s *Server) handleAdmittedMedia(w http.ResponseWriter, r *http.Request, project Project, key APIKey, request mediaRequest) {
	model := request.model()
	audit := guardrailAuditSummary{Model: model}
	admittedAt := time.Now().UTC()
	call, err := s.admitRoutedCall(w, r, project, key, model, request.stream(), requestTokenReservation(request.Fields))
	if err != nil {
		w.Header().Set("x-request-id", s.finishRejectedCall(r, admittedAt, project, key, model, request.stream(), err, audit))
		writeError(w, r, err)
		return
	}
	if err = s.runMediaPreflight(r, &call, &request); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, audit)
		writeError(w, r, err)
		return
	}
	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, model, audit)
	if !ok {
		return
	}
	routes := routed.Routes[:0]
	for _, route := range routed.Routes {
		if s.routeSupportsMedia(routed.Call, route) {
			routes = append(routes, route)
		}
	}
	routed.Routes = routes
	if len(routes) == 0 {
		err := NewHTTPError(501, "provider_capability_not_supported", "Publish a media model with a compatible provider or matching provider_call hook")
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, audit)
		writeError(w, r, err)
		return
	}
	result, route, usage, attempts, err := executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (mediaResponse, Usage, error) {
		prepared, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return mediaResponse{}, Usage{}, err
		}
		upstream := request
		upstream.Fields = cloneRawJSON(request.Fields, 0)
		if err := s.runGatewayRequestTransformHooks(ctx, routed.Call, prepared, upstream.Fields, call.RouteProtocol, upstream.applyPatch); err != nil {
			return mediaResponse{}, Usage{}, err
		}
		return s.invokeMediaRoute(ctx, routed.Call, prepared, strings.TrimPrefix(r.URL.Path, "/v1"), upstream)
	})
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, audit)
		writeError(w, r, err)
		return
	}
	s.store.MarkRouteUsed(route.Route.ID)
	s.store.MarkProviderResourceUsed(routeResourceID(route))
	result, usage, err = s.finishMediaHooks(r.Context(), routed.Call, route, result, usage)
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, audit)
		writeError(w, r, err)
		return
	}
	attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	s.finishRoutedCall(r, GatewayCallCompletion{Call: routed.Call, Route: route, Usage: usage, Attempts: attempts, StatusCode: result.Status, RequestPayload: audit, ResponsePayload: map[string]any{"content_type": result.ContentType, "bytes": len(result.Body)}})
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	w.Header().Set("Content-Type", firstNonEmpty(result.ContentType, "application/octet-stream"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

func (m *mediaRequest) applyPatch(data json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := decodeGatewayHookRequestPatch(data, &fields); err != nil {
		return err
	}
	patched := mediaRequest{Fields: fields, Multipart: m.Multipart}
	if err := validateGatewayHookRequestInvariant(m.model(), m.stream(), patched.model(), patched.stream()); err != nil {
		return err
	}
	m.Fields = fields
	return nil
}

func mediaGuardrailTargets(fields map[string]json.RawMessage) []guardrailTextTarget {
	targets := responsesCompactGuardrailTargets(fields)
	for _, name := range []string{"prompt", "negative_prompt", "text"} {
		var value string
		if json.Unmarshal(fields[name], &value) == nil && value != "" {
			targets = append(targets, guardrailTextTarget{fragment: guardrails.Fragment{ID: name, Text: value, Mutable: true}, replace: func(value string) { setRawJSONField(fields, name, value, true) }})
		}
	}
	return targets
}

func (s *Server) runMediaPreflight(r *http.Request, call *CallContext, request *mediaRequest) error {
	ctx := r.Context()
	if err := s.runGatewayAuthContextHooks(ctx, call, r.Header); err != nil {
		return err
	}
	if err := s.runGatewayDecodeNormalizeHooks(ctx, *call, r.Header, request.Fields, request.applyPatch); err != nil {
		return err
	}
	if err := s.runGatewayAdmissionHooks(ctx, *call, r.Header, request.Fields, requestTokenReservation(request.Fields)); err != nil {
		return err
	}
	if err := s.runGatewayPrivacyPreHooks(ctx, *call, r.Header, request.Fields, request.applyPatch); err != nil {
		return err
	}
	if err := s.runGatewayGuardrailPreHooks(ctx, *call, request.Fields, mediaGuardrailTargets(request.Fields), request.applyPatch); err != nil {
		return err
	}
	if err := s.runGatewayContextOptimizeHooks(ctx, *call, request.Fields, request.applyPatch); err != nil {
		return err
	}
	_, err := s.evaluateOutboundGuardrails(ctx, call.Project.ID, mediaGuardrailTargets(request.Fields))
	return err
}

func (s *Server) finishMediaHooks(ctx context.Context, call CallContext, route RouteSelection, result mediaResponse, usage Usage) (mediaResponse, Usage, error) {
	hasHooks := false
	for _, stage := range []pluginmeta.GatewayHookStage{pluginmeta.StageResponsePost, pluginmeta.StageGuardrailPost, pluginmeta.StageUsageAttribution} {
		hasHooks = hasHooks || len(s.gatewayRouteHooksForRoute(stage, route, call.RouteProtocol, true)) > 0
	}
	if !hasHooks {
		return result, usage, nil
	}
	jsonResponse := strings.Contains(strings.ToLower(result.ContentType), "application/json")
	var payload any
	if jsonResponse {
		payload = json.RawMessage(result.Body)
	} else {
		payload = map[string]any{"data_base64": base64.StdEncoding.EncodeToString(result.Body)}
	}
	payload, err := s.runGatewayResponsePostHooks(ctx, call, route, payload, call.RouteProtocol)
	if err != nil {
		return result, usage, err
	}
	payload, err = s.runGatewayGuardrailPostHooks(ctx, call, route, payload, usage, call.RouteProtocol)
	if err != nil {
		return result, usage, err
	}
	usage, err = s.runGatewayUsageAttributionHooks(ctx, call, route, payload, usage, call.RouteProtocol)
	if err != nil {
		return result, usage, err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return result, usage, err
	}
	if jsonResponse {
		result.Body = data
	} else {
		var wrapped struct {
			Data *string `json:"data_base64"`
		}
		if err := json.Unmarshal(data, &wrapped); err != nil || wrapped.Data == nil {
			return result, usage, NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Media response hooks must preserve a string data_base64 field")
		}
		result.Body, err = base64.StdEncoding.DecodeString(*wrapped.Data)
		if err != nil {
			return result, usage, NewHTTPError(http.StatusBadGateway, "gateway_hook_response_invalid", "Media response hooks must return valid base64 data")
		}
	}
	return result, usage, err
}
