package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
)

func usesJevStrategy(call CallContext, routes []RouteSelection) bool {
	policy := modelSemanticRoutingPolicy(call.Model)
	if policy.Mode == "enforce" && len(policy.Candidates) > 0 {
		return true
	}
	for _, route := range routes {
		if routeStrategy(route.Route) == RouteStrategyJev {
			return true
		}
	}
	return false
}

func (s *Server) applySemanticRouting(ctx context.Context, routed *RoutedCall, req ChatCompletionRequest, headers http.Header) error {
	if !usesJevStrategy(routed.Call, routed.Routes) {
		if len(modelSemanticRoutingPolicy(routed.Call.Model).Candidates) == 0 {
			s.applyLegacySemanticRouting(ctx, routed, req, headers)
		}
		return nil
	}
	text, eligible := jevChatText(req)
	_, scope := chatCompletionSessionIdentifier(headers, req)
	return s.applyJevRouting(ctx, routed, text, eligible && scope == sessionScopeNone, func(model ProviderModel) jevCompatibility {
		return jevChatCompatibility(model, req)
	})
}

func jevChatText(req ChatCompletionRequest) (string, bool) {
	// Classify the latest human task. Preserve the full request for generation.
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			return jevTextContent(req.Messages[i].Content)
		}
	}
	return "", false
}

func jevTextContent(content any) (string, bool) {
	if text, ok := content.(string); ok {
		text = strings.TrimSpace(text)
		return text, text != "" && len(text) <= 8192
	}
	data, err := json.Marshal(content)
	if err != nil {
		return "", false
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(data, &parts) != nil {
		return "", false
	}
	var texts []string
	for _, part := range parts {
		if part.Type != "text" && part.Type != "input_text" {
			return "", false
		}
		texts = append(texts, part.Text)
	}
	return jevTextContent(strings.Join(texts, "\n"))
}

func (s *Server) applyJevRouting(ctx context.Context, routed *RoutedCall, text string, eligible bool, fits func(ProviderModel) jevCompatibility) error {
	policy := modelSemanticRoutingPolicy(routed.Call.Model)
	timeout := s.config.SemanticRoutingTimeoutMS
	if timeout <= 0 {
		timeout = 1000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	if policy.Mode != "enforce" || len(policy.Candidates) == 0 {
		return NewHTTPError(503, "jev_policy_unavailable", "Jev routing requires a configured model policy")
	}
	reader, ok := s.store.(semanticModelReader)
	if !ok {
		return NewHTTPError(503, "jev_candidates_unavailable", "Jev candidate metadata is unavailable")
	}
	configured := map[semanticModelKey]bool{}
	for _, candidate := range policy.Candidates {
		configured[semanticModelKey{candidate.ProviderID, candidate.ProviderModel}] = true
	}
	var candidateRoutes []RouteSelection
	for _, route := range routed.Routes {
		if configured[semanticKey(route)] {
			candidateRoutes = append(candidateRoutes, route)
		}
	}
	models, err := reader.SemanticProviderModels(ctx, candidateRoutes)
	if err != nil {
		return NewHTTPError(503, "jev_candidates_unavailable", "Jev candidate metadata is unavailable")
	}
	metadata := map[semanticModelKey]ProviderModel{}
	for _, model := range models {
		metadata[semanticModelKey{model.ProviderID, model.UpstreamModel}] = model
	}
	groups := map[string][]RouteSelection{}
	candidates := []semanticCandidate{}
	rejections := []jevCandidateRejection{}
	for _, candidate := range policy.Candidates {
		key := semanticModelKey{candidate.ProviderID, candidate.ProviderModel}
		model, exists := metadata[key]
		if !exists {
			rejections = append(rejections, jevCandidateRejection{CandidateID: candidate.ID, jevCompatibility: jevReject("model_metadata_missing", "")})
			continue
		}
		if result := fits(model); result.Reason != "" {
			rejections = append(rejections, jevCandidateRejection{CandidateID: candidate.ID, jevCompatibility: result})
			continue
		}
		for _, route := range routed.Routes {
			if semanticKey(route) == key {
				groups[candidate.ID] = append(groups[candidate.ID], route)
			}
		}
		if len(groups[candidate.ID]) == 0 {
			rejections = append(rejections, jevCandidateRejection{CandidateID: candidate.ID, jevCompatibility: jevReject("route_unavailable", "")})
			continue
		}
		candidates = append(candidates, semanticCandidate{ID: candidate.ID, Model: candidate.ProviderModel, Criteria: candidate.Criteria})
	}
	s.recordJevAdmission(routed.Call.RequestID, rejections)
	if len(candidates) == 0 {
		return NewHTTPError(503, "jev_no_eligible_candidates", "No configured Jev model can serve this request")
	}
	// The default leads deterministic failover. Resource ordering within each
	// provider/model remains the order produced by the existing planner.
	fallback := policy.DefaultCandidateID
	if len(groups[fallback]) == 0 {
		fallback = candidates[0].ID
	}
	order := func(selected string) {
		routes := append([]RouteSelection(nil), groups[selected]...)
		if selected != fallback {
			routes = append(routes, groups[fallback]...)
		}
		for _, candidate := range candidates {
			if candidate.ID != selected && candidate.ID != fallback {
				routes = append(routes, groups[candidate.ID]...)
			}
		}
		routed.Routes = routes
	}
	// Existing affinity decisions must remain authoritative. Restrict their pool
	// to approved models without changing the order they already established.
	bound := routed.Affinity != nil || routed.Call.Affinity != nil || (routed.Call.RoutingStrategyOverride != "" && routed.Call.RoutingStrategyOverride != "inherit")
	for _, route := range routed.Routes {
		bound = bound || route.Route.StickySession
	}
	if bound {
		allowed := map[semanticModelKey]bool{}
		for _, group := range groups {
			allowed[semanticKey(group[0])] = true
		}
		routes := []RouteSelection{}
		for _, route := range routed.Routes {
			if allowed[semanticKey(route)] {
				routes = append(routes, route)
			}
		}
		routed.Routes = routes
		return nil
	}
	order(fallback)
	started := time.Now()
	reason, selected := "fallback", fallback
	decision := semanticDecision{}
	defer func() {
		s.store.RecordAuditEvent(AuditEvent{Action: "routing.semantic", ResourceType: "gateway_request", ResourceID: routed.Call.RequestID, Status: reason, Message: "Jev model selection", AfterSnapshot: auditSnapshotJSON(map[string]any{
			"project_id": routed.Call.Project.ID, "model": routed.Call.Model.Name, "strategy": RouteStrategyJev, "protocol": routed.Call.RouteProtocol, "selected_candidate_id": selected, "selected_model": routed.Routes[0].ProviderModel, "confidence": decision.Confidence, "min_confidence": policy.MinConfidence, "probabilities": decision.Probabilities, "prompt_version": semanticRoutingPromptVersion, "evaluator_model": decision.Model, "input_tokens": decision.InputTokens, "output_tokens": decision.OutputTokens, "latency_ms": time.Since(started).Milliseconds(),
		})})
	}()
	if !s.config.SemanticRoutingEnabled || s.semanticRouter == nil || s.config.TypeSafeAPIKey == "" || !slices.Contains(s.config.SemanticRoutingProjects, routed.Call.Project.ID) {
		reason = "evaluator_disabled"
		return nil
	}
	if !eligible {
		reason = "ineligible_request"
		return nil
	}
	if len(candidates) == 1 {
		reason = "single_candidate"
		return nil
	}
	decision, err = s.semanticRouter.Evaluate(ctx, text, candidates, policy.Instructions)
	if err != nil || ctx.Err() != nil {
		reason = "evaluator_unavailable"
		return nil
	}
	if decision.Choice == "no_preference" {
		reason = "no_preference"
		return nil
	}
	if len(groups[decision.Choice]) == 0 || !unitProbability(decision.Confidence) {
		reason = "invalid_decision"
		return nil
	}
	if decision.Confidence < policy.MinConfidence {
		reason = "low_confidence"
		return nil
	}
	selected, reason = decision.Choice, "applied"
	order(selected)
	return nil
}
