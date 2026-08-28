package server

import (
	"context"
	"log"
	"net/http"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func (s *Server) runGatewayRouteCandidatesHooks(ctx context.Context, call CallContext, routes []RouteSelection) ([]RouteSelection, error) {
	if len(routes) == 0 || !s.hasGatewayHookStage(pluginmeta.StageRouteCandidates) {
		return routes, nil
	}
	input := pluginmeta.GatewayHookInput{
		RequestID: call.RequestID,
		Envelope: pluginmeta.GatewayEnvelope{
			Version:   "v1",
			Protocol:  "gateway",
			Operation: "route_candidates",
			Model:     call.Model.Name,
		},
		Data: pluginmeta.GatewayHookData{},
	}
	for dataClass, value := range map[pluginmeta.GatewayDataClass]any{
		pluginmeta.DataAuthContext:     gatewayAuthContextView(call),
		pluginmeta.DataProjectMetadata: call.Project,
		pluginmeta.DataAPIKeyMetadata:  gatewayAPIKeyMetadataView(call.Key),
		pluginmeta.DataRouteCandidates: gatewayRouteCandidateViews(routes),
	} {
		if encoded, ok := marshalGatewayHookData(value); ok {
			input.Data[dataClass] = encoded
		}
	}
	report, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StageRouteCandidates, input)
	if err != nil {
		return nil, gatewayHookHTTPError(pluginmeta.StageRouteCandidates, err)
	}
	selected := routes
	for _, result := range report.Results {
		patch, ok := result.Writes[pluginmeta.DataRouteCandidates]
		if !ok {
			continue
		}
		next, err := applyGatewayRouteCandidatesPatch(selected, patch.Value)
		if err != nil {
			if result.FailurePolicy == pluginmeta.FailurePolicyFailOpen {
				log.Printf("[tokenhub] gateway route_candidates hook %s/%s returned invalid candidates for request %s: %v", result.PluginID, result.HookID, call.RequestID, err)
				continue
			}
			return nil, NewHTTPError(http.StatusBadGateway, "gateway_hook_route_candidates_invalid", "Gateway routing plugin returned invalid route candidates")
		}
		selected = next
	}
	if len(selected) == 0 {
		return nil, ErrProviderMissing
	}
	return selected, nil
}

func (s *Server) runGatewayRouteRankHooks(ctx context.Context, call CallContext, planned []RouteSelection) []RouteSelection {
	if len(planned) < 2 || !s.hasGatewayHookStage(pluginmeta.StageRouteRank) {
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
	report, err := s.runAuditedGatewayHookStage(ctx, call, pluginmeta.StageRouteRank, input)
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
