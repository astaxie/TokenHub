package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

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

func marshalGatewayHookData(value any) (json.RawMessage, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	return data, true
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
