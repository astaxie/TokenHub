package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	pluginmeta "tokenhub/backend/internal/plugin"
)

type gatewayRouteCandidateView struct {
	RouteID          string `json:"route_id"`
	ProviderID       string `json:"provider_id,omitempty"`
	ProviderName     string `json:"provider_name,omitempty"`
	ProviderType     string `json:"provider_type,omitempty"`
	ProviderModel    string `json:"provider_model,omitempty"`
	ResourceID       string `json:"resource_id,omitempty"`
	ResourceName     string `json:"resource_name,omitempty"`
	ResourceType     string `json:"resource_type,omitempty"`
	ResourceGroup    string `json:"resource_group,omitempty"`
	RoutePriority    int    `json:"route_priority"`
	ResourcePriority int    `json:"resource_priority"`
	Weight           int    `json:"weight"`
	Strategy         string `json:"strategy,omitempty"`
}

type gatewayRouteOrderPatch struct {
	RouteIDs []string `json:"route_ids"`
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

func gatewayTextSegmentsFromGuardrailTargets(targets []guardrailTextTarget) []pluginmeta.TextSegment {
	segments := make([]pluginmeta.TextSegment, 0, len(targets))
	for _, target := range targets {
		if target.fragment.ID == "" && target.fragment.Text == "" {
			continue
		}
		segments = append(segments, pluginmeta.TextSegment{
			ID:   target.fragment.ID,
			Text: target.fragment.Text,
		})
	}
	return segments
}

func compactRequestModel(request map[string]json.RawMessage) string {
	var model string
	if request != nil {
		_ = json.Unmarshal(request["model"], &model)
	}
	return strings.TrimSpace(model)
}

func applyEmbeddingsGatewayRequestPatch(req *EmbeddingsRequest, data json.RawMessage) error {
	if req == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	originalModel := req.Model
	var patched EmbeddingsRequest
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	if strings.TrimSpace(patched.Model) != originalModel {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	*req = patched
	return nil
}

func applyImageGatewayRequestPatch(req *imageGenerationRequest, data json.RawMessage) error {
	if req == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid request patch")
	}
	originalModel := req.Model
	originalResponseFormat := req.ResponseFormat
	var patched imageGenerationRequest
	if err := decodeGatewayHookRequestPatch(data, &patched); err != nil {
		return err
	}
	if err := normalizeImageGenerationRequest(&patched); err != nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin returned an invalid image request patch")
	}
	if patched.Model != originalModel {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested model")
	}
	if patched.ResponseFormat != originalResponseFormat {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_patch_invalid", "Gateway plugin cannot change the requested image response format")
	}
	*req = patched
	return nil
}

func gatewayRouteCandidateViews(routes []RouteSelection) []gatewayRouteCandidateView {
	views := make([]gatewayRouteCandidateView, 0, len(routes))
	for _, route := range routes {
		views = append(views, gatewayRouteCandidateView{
			RouteID:          route.Route.ID,
			ProviderID:       route.Provider.ID,
			ProviderName:     route.Provider.Name,
			ProviderType:     route.Provider.Type,
			ProviderModel:    route.ProviderModel,
			ResourceID:       routeResourceID(route),
			ResourceName:     routeResourceName(route),
			ResourceType:     routeResourceType(route),
			ResourceGroup:    routeResourceGroup(route),
			RoutePriority:    route.Route.Priority,
			ResourcePriority: routeResourcePriority(route),
			Weight:           routeEffectiveWeight(route),
			Strategy:         routeStrategy(route.Route),
		})
	}
	return views
}

func routeResourceName(route RouteSelection) string {
	if route.Resource == nil {
		return ""
	}
	return route.Resource.Name
}

func routeResourceType(route RouteSelection) string {
	if route.Resource == nil {
		return ""
	}
	return route.Resource.ResourceType
}

func routeResourceGroup(route RouteSelection) string {
	if route.Resource == nil {
		return ""
	}
	return route.Resource.Group
}

func gatewayAuthContextView(call CallContext) map[string]any {
	view := map[string]any{
		"project_id": call.Project.ID,
		"api_key_id": call.Key.ID,
		"model":      call.Model.Name,
		"stream":     call.Stream,
	}
	if len(call.GatewayAuthMetadata) > 0 {
		view["metadata"] = call.GatewayAuthMetadata
	}
	return view
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

func applyGatewayAuthContextPatch(call *CallContext, data json.RawMessage) error {
	if call == nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned an invalid auth context")
	}
	var patch map[string]json.RawMessage
	if err := decodeGatewayHookPayload(data, &patch, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned an invalid auth context"); err != nil {
		return err
	}
	if err := validateGatewayAuthStringInvariant(patch, "project_id", call.Project.ID); err != nil {
		return err
	}
	if err := validateGatewayAuthStringInvariant(patch, "api_key_id", call.Key.ID); err != nil {
		return err
	}
	if err := validateGatewayAuthStringInvariant(patch, "model", call.Model.Name); err != nil {
		return err
	}
	if err := validateGatewayAuthBoolInvariant(patch, "stream", call.Stream); err != nil {
		return err
	}
	metadataRaw, ok := patch["metadata"]
	if !ok {
		return nil
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(metadataRaw, &metadata); err != nil {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned invalid metadata")
	}
	if call.GatewayAuthMetadata == nil {
		call.GatewayAuthMetadata = map[string]json.RawMessage{}
	}
	for key, value := range metadata {
		key = strings.TrimSpace(key)
		if key == "" {
			return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin returned an empty metadata key")
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			delete(call.GatewayAuthMetadata, key)
			continue
		}
		call.GatewayAuthMetadata[key] = append(json.RawMessage(nil), value...)
	}
	return nil
}

func validateGatewayAuthStringInvariant(patch map[string]json.RawMessage, key string, expected string) error {
	raw, ok := patch[key]
	if !ok {
		return nil
	}
	var actual string
	if err := json.Unmarshal(raw, &actual); err != nil || strings.TrimSpace(actual) != expected {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin cannot change core authentication context")
	}
	return nil
}

func validateGatewayAuthBoolInvariant(patch map[string]json.RawMessage, key string, expected bool) error {
	raw, ok := patch[key]
	if !ok {
		return nil
	}
	var actual bool
	if err := json.Unmarshal(raw, &actual); err != nil || actual != expected {
		return NewHTTPError(http.StatusBadGateway, "gateway_hook_auth_context_invalid", "Gateway auth plugin cannot change core authentication context")
	}
	return nil
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

func applyGatewayRouteCandidatesPatch(routes []RouteSelection, data json.RawMessage) ([]RouteSelection, error) {
	var patch gatewayRouteOrderPatch
	if err := json.Unmarshal(data, &patch); err != nil {
		return nil, err
	}
	routesByID := make(map[string]RouteSelection, len(routes))
	for _, route := range routes {
		if route.Route.ID == "" {
			return nil, fmt.Errorf("route candidate has no route id")
		}
		routesByID[route.Route.ID] = route
	}
	selected := make([]RouteSelection, 0, len(patch.RouteIDs))
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
		selected = append(selected, route)
	}
	return selected, nil
}

func attemptsWithAttributedUsage(call CallContext, attempts []RouteAttempt, route RouteSelection, usage Usage) []RouteAttempt {
	if len(attempts) == 0 {
		return attempts
	}
	patched := append([]RouteAttempt(nil), attempts...)
	for index := len(patched) - 1; index >= 0; index-- {
		attempt := patched[index]
		if !attempt.Invoked || attempt.Status < 200 || attempt.Status >= 300 {
			continue
		}
		if attempt.Selection.Route.ID != route.Route.ID || attempt.Selection.Provider.ID != route.Provider.ID {
			continue
		}
		attempt.Usage = priceUsageAt(call.Model, usage, call.StartedAt)
		patched[index] = attempt
		return patched
	}
	return patched
}
