package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type gatewayRouteCandidateView struct {
	RouteID          string `json:"route_id"`
	ProviderID       string `json:"provider_id,omitempty"`
	ProviderType     string `json:"provider_type,omitempty"`
	ProviderModel    string `json:"provider_model,omitempty"`
	ResourceID       string `json:"resource_id,omitempty"`
	RoutePriority    int    `json:"route_priority"`
	ResourcePriority int    `json:"resource_priority"`
	Weight           int    `json:"weight"`
	Strategy         string `json:"strategy,omitempty"`
}

type gatewayRouteOrderPatch struct {
	RouteIDs []string `json:"route_ids"`
}

func (s *Server) runGatewayCacheLookupHooks(ctx context.Context, call CallContext, payload any) (any, Usage, bool, error) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageCacheLookup)) == 0 {
		return nil, Usage{}, false, nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return nil, Usage{}, false, NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "cache_lookup",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: gatewayHookCallData(call, body),
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageCacheLookup, input)
	if err != nil {
		return nil, Usage{}, false, gatewayHookHTTPError(pluginmeta.StageCacheLookup, err)
	}
	if report.TerminalDecision != pluginmeta.HookDecisionShortCircuit {
		return nil, Usage{}, false, nil
	}
	result := report.Results[len(report.Results)-1]
	responsePatch, ok := result.Writes[pluginmeta.DataProviderResponse]
	if !ok {
		return nil, Usage{}, false, NewHTTPError(http.StatusBadGateway, "gateway_hook_cache_hit_invalid", "Gateway cache plugin did not return a response")
	}
	var response any
	if err := decodeGatewayHookPayload(responsePatch.Value, &response, "gateway_hook_cache_hit_invalid", "Gateway cache plugin returned an invalid response"); err != nil {
		return nil, Usage{}, false, err
	}
	var usage Usage
	if usagePatch, ok := result.Writes[pluginmeta.DataUsage]; ok {
		if err := decodeGatewayHookPayload(usagePatch.Value, &usage, "gateway_hook_cache_hit_invalid", "Gateway cache plugin returned invalid usage"); err != nil {
			return nil, Usage{}, false, err
		}
	}
	return response, usage, true, nil
}

func (s *Server) runGatewayCacheWriteHooks(ctx context.Context, call CallContext, payload any, response any, usage Usage) {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageCacheWrite)) == 0 {
		return
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		log.Printf("[tokenhub] gateway cache_write hooks skipped for request %s: input could not be encoded", call.RequestID)
		return
	}
	responseBody, ok := marshalGatewayHookData(response)
	if !ok {
		log.Printf("[tokenhub] gateway cache_write hooks skipped for request %s: response could not be encoded", call.RequestID)
		return
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "cache_write",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: gatewayHookCallData(call, body),
	}
	input.Data[pluginmeta.DataProviderResponse] = responseBody
	if encodedUsage, ok := marshalGatewayHookData(usage); ok {
		input.Data[pluginmeta.DataUsage] = encodedUsage
	}
	if _, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageCacheWrite, input); err != nil {
		log.Printf("[tokenhub] gateway cache_write hooks failed for request %s: %v", call.RequestID, err)
	}
}

func (s *Server) runGatewayPrivacyPreHooks(ctx context.Context, call CallContext, headers http.Header, payload any, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StagePrivacyPre)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "privacy_pre",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	if requestHeaders, ok := marshalGatewayHookData(sanitizedGatewayHookHeaders(headers)); ok {
		input.Data[pluginmeta.DataRequestHeaders] = requestHeaders
	}
	input.Data[pluginmeta.DataRequestBody] = body
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StagePrivacyPre, input)
	if err != nil {
		return gatewayHookHTTPError(pluginmeta.StagePrivacyPre, err)
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRequestBody]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayContextOptimizeHooks(ctx context.Context, call CallContext, payload any, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageContextOptimize)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "context_optimize",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: gatewayHookCallData(call, body),
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageContextOptimize, input)
	if err != nil {
		return gatewayHookHTTPError(pluginmeta.StageContextOptimize, err)
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRequestBody]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayChatContextOptimizeHooks(ctx context.Context, call CallContext, req *ChatCompletionRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayResponsesContextOptimizeHooks(ctx context.Context, call CallContext, req *ResponsesRequest) error {
	return s.runGatewayContextOptimizeHooks(ctx, call, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ResponsesRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, payload any, apply func(json.RawMessage) error) error {
	if s == nil || s.gatewayHooks == nil || s.gatewayChain == nil || len(s.gatewayChain.Hooks(pluginmeta.StageRequestTransform)) == 0 {
		return nil
	}
	body, ok := marshalGatewayHookData(payload)
	if !ok {
		return NewHTTPError(http.StatusInternalServerError, "gateway_hook_input_invalid", "Gateway plugin input could not be encoded")
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:     "v1",
			Protocol:    "gateway",
			Operation:   "request_transform",
			Model:       call.Model.Name,
			RequestBody: body,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:         gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata:     call.Project,
		pluginmeta.DataProviderCredentials: gatewayProviderCredentialsView(route),
		pluginmeta.DataProviderRequest:     body,
		pluginmeta.DataRouteCandidates:     gatewayRouteCandidateViews([]RouteSelection{route}),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageRequestTransform, input)
	if err != nil {
		if pluginmeta.IsGatewayHookRouteSkipped(err) {
			return &ProviderInvocationError{
				Err:         NewHTTPError(http.StatusBadGateway, "gateway_hook_route_skipped", "Gateway plugin skipped this route"),
				Disposition: ProviderErrorRouteSkipped,
			}
		}
		return &ProviderInvocationError{
			Err:         gatewayHookHTTPError(pluginmeta.StageRequestTransform, err),
			Disposition: ProviderErrorPolicy,
		}
	}
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataProviderRequest]
		if !ok {
			continue
		}
		if err := apply(patch.Value); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) runGatewayChatRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *ChatCompletionRequest) error {
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ChatCompletionRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayResponsesRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *ResponsesRequest) error {
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		originalStream := req.Stream
		var patched ResponsesRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if err := validateGatewayHookRequestInvariant(originalModel, originalStream, patched.Model, patched.Stream); err != nil {
			return err
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayEmbeddingsRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, req *EmbeddingsRequest) error {
	return s.runGatewayRequestTransformHooks(ctx, call, route, *req, func(data json.RawMessage) error {
		originalModel := req.Model
		var patched EmbeddingsRequest
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		if patched.Model != originalModel {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
		}
		*req = patched
		return nil
	})
}

func (s *Server) runGatewayCompactRequestTransformHooks(ctx context.Context, call CallContext, route RouteSelection, request map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	body := cloneRawJSON(request, 0)
	err := s.runGatewayRequestTransformHooks(ctx, call, route, body, func(data json.RawMessage) error {
		var patched map[string]json.RawMessage
		if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
			return err
		}
		body = cloneRawJSON(patched, 0)
		return nil
	})
	return body, err
}

func (s *Server) runGatewayRouteRankHooks(ctx context.Context, call CallContext, planned []RouteSelection) []RouteSelection {
	if s == nil || s.gatewayHooks == nil || len(planned) < 2 {
		return planned
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: "route_rank",
			Model:     call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataRouteCandidates: gatewayRouteCandidateViews(planned),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageRouteRank, input)
	if err != nil {
		log.Printf("[tokenhub] gateway route_rank hooks failed for request %s: %v", call.RequestID, err)
		return planned
	}
	ordered := planned
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRouteCandidates]
		if !ok {
			continue
		}
		next, err := applyGatewayRouteOrderPatch(ordered, patch.Value)
		if err != nil {
			log.Printf("[tokenhub] gateway route_rank hook %s/%s returned an invalid route order for request %s: %v", result.PluginID, result.HookID, call.RequestID, err)
			continue
		}
		ordered = next
	}
	return ordered
}

func (s *Server) runGatewayTraceExportHooks(ctx context.Context, completion GatewayCallCompletion) {
	if s == nil || s.gatewayHooks == nil {
		return
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: completion.Call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: string(completion.Kind),
			Model:     completion.Call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	if usage, ok := marshalGatewayHookData(completion.Usage); ok {
		input.Data[pluginmeta.DataUsage] = usage
	}
	if audit, ok := marshalGatewayHookData(gatewayTraceExportAuditView(completion)); ok {
		input.Data[pluginmeta.DataAudit] = audit
	}
	if len(input.Data) == 0 {
		input.Data = nil
	}
	if _, err := s.gatewayHooks.RunStage(ctx, pluginmeta.StageTraceExport, input); err != nil {
		log.Printf("[tokenhub] gateway trace_export hooks failed for request %s: %v", completion.Call.RequestID, err)
	}
}

func gatewayTraceExportAuditView(completion GatewayCallCompletion) map[string]any {
	return map[string]any{
		"kind":        completion.Kind,
		"request_id":  completion.Call.RequestID,
		"project_id":  completion.Call.Project.ID,
		"api_key_id":  completion.Call.Key.ID,
		"model":       completion.Call.Model.Name,
		"stream":      completion.Call.Stream,
		"route_id":    completion.Route.Route.ID,
		"provider_id": completion.Route.Provider.ID,
		"status_code": completion.StatusCode,
		"error_code":  completion.ErrorCode,
		"finished_at": completion.FinishedAt,
	}
}

func gatewayHookCallData(call CallContext, requestBody json.RawMessage) pluginmeta.GatewayHookData {
	data := pluginmeta.GatewayHookData{}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataRequestBody:     requestBody,
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			data[dataClass] = encoded
		}
	}
	return data
}

func gatewayProviderCredentialsView(route RouteSelection) map[string]any {
	view := map[string]any{
		"provider": map[string]any{
			"id":                route.Provider.ID,
			"type":              route.Provider.Type,
			"base_url":          route.Provider.BaseURL,
			"api_key":           route.Provider.APIKey,
			"headers":           cloneStringMap(route.Provider.Headers),
			"sensitive_headers": append([]string(nil), route.Provider.SensitiveHeaders...),
			"options":           cloneStringMap(route.Provider.Options),
		},
		"provider_model": route.ProviderModel,
	}
	if route.Resource != nil {
		view["resource"] = map[string]any{
			"id":                route.Resource.ID,
			"type":              route.Resource.ResourceType,
			"base_url":          route.Resource.BaseURL,
			"api_key":           route.Resource.APIKey,
			"headers":           cloneStringMap(route.Resource.Headers),
			"sensitive_headers": append([]string(nil), route.Resource.SensitiveHeaders...),
			"options":           cloneStringMap(route.Resource.Options),
		}
	}
	return view
}

func marshalGatewayHookData(value any) (json.RawMessage, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return data, true
}

func sanitizedGatewayHookHeaders(headers http.Header) map[string][]string {
	sanitized := map[string][]string{}
	for key, values := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if sensitiveGatewayHookHeader(canonical) {
			sanitized[canonical] = []string{"[redacted]"}
			continue
		}
		sanitized[canonical] = append([]string(nil), values...)
	}
	return sanitized
}

func sensitiveGatewayHookHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Authorization", "Cookie", "Set-Cookie", "Proxy-Authorization", "X-Api-Key":
		return true
	default:
		return false
	}
}

func gatewayHookHTTPError(stage pluginmeta.GatewayHookStage, err error) error {
	if pluginmeta.IsGatewayHookDenied(err) {
		return NewHTTPError(http.StatusForbidden, "gateway_hook_denied", fmt.Sprintf("Request blocked by the %s plugin stage", stage))
	}
	httpErr := AsHTTPError(err)
	if httpErr.Code != "internal_error" {
		return httpErr
	}
	return NewHTTPError(http.StatusInternalServerError, "gateway_hook_failed", fmt.Sprintf("Gateway plugin stage %s failed", stage))
}

func decodeGatewayHookRequestPatch(data json.RawMessage, target any) error {
	return decodeGatewayHookPayload(data, target, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
}

func decodeGatewayHookPayload(data json.RawMessage, target any, code string, message string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return NewHTTPError(http.StatusBadGateway, code, message)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return NewHTTPError(http.StatusBadGateway, code, "Gateway plugin payload must contain a single JSON value")
	}
	return nil
}

func validateGatewayHookRequestInvariant(originalModel string, originalStream bool, patchedModel string, patchedStream bool) error {
	if patchedModel != originalModel {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	if patchedStream != originalStream {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested stream mode")
	}
	return nil
}

func gatewayRouteCandidateViews(routes []RouteSelection) []gatewayRouteCandidateView {
	views := make([]gatewayRouteCandidateView, 0, len(routes))
	for _, route := range routes {
		views = append(views, gatewayRouteCandidateView{
			RouteID:          route.Route.ID,
			ProviderID:       route.Provider.ID,
			ProviderType:     route.Provider.Type,
			ProviderModel:    route.ProviderModel,
			ResourceID:       routeResourceID(route),
			RoutePriority:    route.Route.Priority,
			ResourcePriority: routeResourcePriority(route),
			Weight:           routeEffectiveWeight(route),
			Strategy:         routeStrategy(route.Route),
		})
	}
	return views
}

func gatewayAuthContextView(call CallContext) map[string]any {
	return map[string]any{
		"project_id": call.Project.ID,
		"api_key_id": call.Key.ID,
		"model":      call.Model.Name,
		"stream":     call.Stream,
	}
}

func gatewayAPIKeyMetadataView(key APIKey) map[string]any {
	return map[string]any{
		"id":                key.ID,
		"project_id":        key.ProjectID,
		"owner_user_id":     key.OwnerUserID,
		"group":             key.Group,
		"model_access_mode": key.ModelAccessMode,
		"allowed_models":    key.Allowed,
		"rate_limit_rpm":    key.RateLimitRPM,
		"token_limit_tpm":   key.TokenLimitTPM,
		"metadata":          key.Metadata,
	}
}

func applyGatewayRouteOrderPatch(routes []RouteSelection, data json.RawMessage) ([]RouteSelection, error) {
	var patch gatewayRouteOrderPatch
	if err := json.Unmarshal(data, &patch); err != nil {
		return nil, err
	}
	if len(patch.RouteIDs) != len(routes) {
		return nil, fmt.Errorf("route_ids has %d entries, want %d", len(patch.RouteIDs), len(routes))
	}
	routesByID := make(map[string]RouteSelection, len(routes))
	for _, route := range routes {
		if route.Route.ID == "" {
			return nil, fmt.Errorf("route candidate has no route id")
		}
		routesByID[route.Route.ID] = route
	}
	ordered := make([]RouteSelection, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))
	for _, routeID := range patch.RouteIDs {
		if _, ok := seen[routeID]; ok {
			return nil, fmt.Errorf("route id %s is duplicated", routeID)
		}
		route, ok := routesByID[routeID]
		if !ok {
			return nil, fmt.Errorf("route id %s is not a core-approved candidate", routeID)
		}
		seen[routeID] = struct{}{}
		ordered = append(ordered, route)
	}
	return ordered, nil
}
