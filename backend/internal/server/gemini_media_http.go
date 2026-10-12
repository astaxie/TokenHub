package server

import (
	"context"
	"encoding/json"
	"net/http"
)

func (s *Server) routeSupportsGeminiMedia(call CallContext, route RouteSelection) bool {
	_, implemented := resolveTypedAdapter[providerGeminiMedia](s.adapterRegistry, route.Provider.Type)
	return implemented && s.routeSupportsAdapterCapability(route, AdapterCapabilityGeminiMedia) || s.hasGatewayProviderCallHookForRoute(call, route, providerRouteProtocolGemini)
}

func (s *Server) geminiMediaModelAccessible(model Model, key APIKey) bool {
	routes, err := s.store.SelectRouteCandidates(model.Name)
	if err != nil {
		return false
	}
	for _, route := range routes {
		if !routeMatchesProject(route.Route, key.ProjectID) {
			continue
		}
		for _, stream := range []bool{false, true} {
			call := CallContext{Model: model, Key: key, Project: Project{ID: key.ProjectID}, RouteProtocol: providerRouteProtocolGemini, Stream: stream}
			if s.routeSupportsGeminiMedia(call, route) {
				return true
			}
		}
	}
	return false
}

func validateGeminiMediaPayload(payload map[string]any, model string, stream bool) error {
	if payload == nil {
		return NewHTTPError(400, "invalid_contents", "contents must be a non-empty array")
	}
	contents, ok := payload["contents"].([]any)
	if !ok || len(contents) == 0 {
		return NewHTTPError(400, "invalid_contents", "contents must be a non-empty array")
	}
	if value, present := payload["model"]; present && value != model {
		return NewHTTPError(400, "invalid_model", "Body model must match the requested model")
	}
	if value, present := payload["stream"]; present && value != stream {
		return NewHTTPError(400, "invalid_stream", "Body stream must match the requested operation")
	}
	return nil
}

func (s *Server) handleGeminiMedia(w http.ResponseWriter, r *http.Request, call CallContext, payload map[string]any, audit guardrailAuditSummary) {
	audit.Guardrail.Replacements = nil
	if err := validateGeminiMediaPayload(payload, call.Model.Name, call.Stream); err != nil {
		s.finishFailedRoutedCall(r, RoutedCall{Call: call}, nil, Usage{}, err, audit)
		writeError(w, r, err)
		return
	}
	routed, ok := s.prepareAdmittedRoutedCallWithAudit(w, r, call, call.Model.Name, audit)
	if !ok {
		return
	}
	routes := routed.Routes[:0]
	for _, route := range routed.Routes {
		if s.routeSupportsGeminiMedia(routed.Call, route) {
			routes = append(routes, route)
		}
	}
	routed.Routes = routes
	if len(routes) == 0 {
		err := NewHTTPError(501, "provider_capability_not_supported", "Publish a Gemini media model with a native Gemini provider or matching provider_call hook")
		s.finishFailedRoutedCall(r, routed, nil, Usage{}, err, audit)
		writeError(w, r, err)
		return
	}
	result, route, usage, attempts, err := executeRoutedWithStore(r.Context(), s.store, routed, false, func(ctx context.Context, route RouteSelection, _ bool, _ int) (mediaResponse, Usage, error) {
		prepared, err := s.prepareRouteForUpstream(ctx, route)
		if err != nil {
			return mediaResponse{}, Usage{}, err
		}
		data, _ := json.Marshal(payload)
		var upstream map[string]any
		if err = decodeResponsesJSON(data, &upstream); err != nil {
			return mediaResponse{}, Usage{}, err
		}
		err = s.runGatewayRequestTransformHooks(ctx, routed.Call, prepared, upstream, providerRouteProtocolGemini, func(data json.RawMessage) error {
			var patched map[string]any
			if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
				return err
			}
			if err := validateGeminiMediaPayload(patched, call.Model.Name, call.Stream); err != nil {
				return NewHTTPError(502, "gateway_hook_patch_invalid", "Gateway plugin changed native Gemini request controls")
			}
			upstream = patched
			return nil
		})
		if err != nil {
			return mediaResponse{}, Usage{}, err
		}
		// The path owns native Gemini controls; never forward a public alias.
		delete(upstream, "model")
		delete(upstream, "stream")
		return s.invokeGeminiMedia(ctx, routed.Call, prepared, upstream)
	})
	if err == nil {
		s.store.MarkRouteUsed(route.Route.ID)
		s.store.MarkProviderResourceUsed(routeResourceID(route))
		result, usage, err = s.finishMediaHooks(r.Context(), routed.Call, route, result, usage)
		if err == nil {
			_, err = inspectGeminiMediaResponse(result, call.Stream)
		}
		attempts = attemptsWithAttributedUsage(routed.Call, attempts, route, usage)
	}
	if err != nil {
		s.finishFailedRoutedCall(r, routed, attempts, usage, err, audit)
		writeError(w, r, err)
		return
	}
	s.finishRoutedCall(r, GatewayCallCompletion{Call: routed.Call, Route: route, Usage: usage, Attempts: attempts, StatusCode: result.Status, RequestPayload: audit, ResponsePayload: map[string]any{"content_type": result.ContentType, "bytes": len(result.Body)}})
	s.writeRouteHeaders(w, routed.Call, route, len(attempts))
	w.Header().Set("Content-Type", result.ContentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if call.Stream {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.WriteHeader(result.Status)
	_, _ = w.Write(result.Body)
}

func (s *Server) invokeGeminiMedia(ctx context.Context, call CallContext, route RouteSelection, payload map[string]any) (mediaResponse, Usage, error) {
	return s.invokeGeminiMediaWithResponseLimit(ctx, call, route, payload, maxMediaResponseBytes)
}

func (s *Server) invokeGeminiMediaWithResponseLimit(ctx context.Context, call CallContext, route RouteSelection, payload map[string]any, responseLimit int) (mediaResponse, Usage, error) {
	var output any
	var usage Usage
	var handled, reported bool
	var err error
	stream := mediaHookStreamBuffer{limit: responseLimit}
	if call.Stream {
		output, usage, handled, err = s.runGatewayProviderCallHooksOutputWithUsagePresence(ctx, call, route, payload, providerRouteProtocolGemini, &stream, &reported)
	} else {
		output, usage, handled, err = s.runGatewayProviderCallHooksOutputWithUsagePresence(ctx, call, route, payload, providerRouteProtocolGemini, nil, &reported)
	}
	result := mediaResponse{Body: stream.Bytes(), ContentType: "text/event-stream", Status: 200}
	if err != nil {
		if call.Stream && handled {
			if !reported {
				usage, _ = inspectGeminiMediaResponse(result, true)
			}
			usage.MeteringInvalid = true
		}
		return mediaResponse{}, usage, err
	}
	if handled {
		if !call.Stream {
			result, err = mediaProviderHookResponse(output, responseLimit)
			if err != nil {
				if !reported {
					fields, _ := output.(map[string]any)
					usage = geminiUsage(fields)
				}
				usage.MeteringInvalid = true
				return mediaResponse{}, usage, err
			}
		}
		parsed, inspectErr := inspectGeminiMediaResponse(result, call.Stream)
		if !reported {
			usage = parsed
		}
		if inspectErr != nil {
			return mediaResponse{}, usage, invalidMediaProviderHookResponse()
		}
		return result, usage, nil
	}
	adapter, ok := resolveTypedAdapter[providerGeminiMedia](s.adapterRegistry, route.Provider.Type)
	if !ok || !s.routeSupportsAdapterCapability(route, AdapterCapabilityGeminiMedia) {
		return mediaResponse{}, usage, invalidMediaProviderHookResponse()
	}
	return adapter.GeminiMedia(ctx, route.Provider, route.ProviderModel, payload, call.Stream)
}
