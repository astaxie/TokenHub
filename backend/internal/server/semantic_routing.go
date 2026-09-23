package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

const semanticMaxCandidates = 32

type semanticModelKey struct{ provider, model string }
type semanticModelReader interface {
	SemanticProviderModels(context.Context, []RouteSelection) ([]ProviderModel, error)
}

func (c Config) validateSemanticRouting() error {
	if !c.SemanticRoutingEnabled {
		return nil
	}
	if strings.TrimSpace(c.TypeSafeAPIKey) == "" {
		return fmt.Errorf("TOKENHUB_TYPESAFE_API_KEY is required when semantic routing is enabled")
	}
	if len(c.SemanticRoutingProjects) == 0 {
		return fmt.Errorf("TOKENHUB_SEMANTIC_ROUTING_PROJECTS must explicitly allow at least one project")
	}
	if c.SemanticRoutingTimeoutMS < 0 || c.SemanticRoutingTimeoutMS > 10000 {
		return fmt.Errorf("TOKENHUB_SEMANTIC_ROUTING_TIMEOUT_MS must be between 1 and 10000")
	}
	return nil
}

// applySemanticRouting runs exactly once after admission, privacy processing,
// protocol filtering, and affinity planning, before either upstream execution path.
// Failover uses the resulting slice without invoking this evaluator again.
func (s *Server) applyLegacySemanticRouting(ctx context.Context, routed *RoutedCall, req ChatCompletionRequest, headers http.Header) {
	policy := modelSemanticRoutingPolicy(routed.Call.Model)
	if !s.config.SemanticRoutingEnabled || policy.Mode == "off" || s.semanticRouter == nil || strings.TrimSpace(s.config.TypeSafeAPIKey) == "" || !slices.Contains(s.config.SemanticRoutingProjects, routed.Call.Project.ID) {
		return
	}
	started := time.Now()
	reason := "ineligible_request"
	decision := semanticDecision{}
	selectedRoutes := []string{}
	defer func() {
		s.store.RecordAuditEvent(AuditEvent{Action: "routing.semantic", ResourceType: "gateway_request", ResourceID: routed.Call.RequestID, Status: reason,
			Message: "Semantic routing decision", AfterSnapshot: auditSnapshotJSON(map[string]any{
				"project_id": routed.Call.Project.ID, "model": routed.Call.Model.Name, "mode": policy.Mode, "reason": reason,
				"prompt_version": semanticRoutingPromptVersion, "evaluator_model": decision.Model, "confidence": decision.Confidence,
				"min_confidence": policy.MinConfidence, "selected_route_ids": selectedRoutes, "latency_ms": time.Since(started).Milliseconds(),
				"input_tokens": decision.InputTokens, "output_tokens": decision.OutputTokens,
			})})
	}()
	if identifier, _ := chatCompletionSessionIdentifier(headers, req); identifier != "" {
		reason = "session_affinity"
		return
	}
	text, ok := semanticRequestText(req)
	if !ok || routed.Affinity != nil || routed.Call.Affinity != nil || ctx.Err() != nil {
		return
	}
	for _, route := range routed.Routes {
		if route.Route.StickySession {
			reason = "session_affinity"
			return
		}
	}
	if len(routed.Routes) < 2 {
		reason = "insufficient_candidates"
		return
	}
	// Restrict promotion to the leading priority tier, including resource priority.
	end := 1
	for end < len(routed.Routes) && routed.Routes[end].Route.Priority == routed.Routes[0].Route.Priority && routeResourcePriority(routed.Routes[end]) == routeResourcePriority(routed.Routes[0]) {
		end++
	}
	tier := routed.Routes[:end]
	reader, ok := s.store.(semanticModelReader)
	if !ok {
		reason = "metadata_unavailable"
		return
	}
	timeout := s.config.SemanticRoutingTimeoutMS
	if timeout <= 0 {
		timeout = 1000
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	models, err := reader.SemanticProviderModels(ctx, tier)
	if err != nil {
		reason = "metadata_unavailable"
		return
	}
	candidates, keys := buildSemanticCandidates(tier, models, req)
	if len(candidates) < 2 || len(candidates) > semanticMaxCandidates {
		reason = "insufficient_candidates"
		return
	}
	decision, err = s.semanticRouter.Evaluate(ctx, text, candidates, "")
	if err != nil {
		reason = "evaluator_unavailable"
		return
	}
	if ctx.Err() != nil {
		reason = "evaluator_unavailable"
		return
	}
	if decision.Choice == "no_preference" {
		reason = "no_preference"
		return
	}
	key, ok := keys[decision.Choice]
	if !ok || !unitProbability(decision.Confidence) {
		reason = "invalid_decision"
		return
	}
	if decision.Confidence < policy.MinConfidence {
		reason = "low_confidence"
		return
	}
	promoted := make([]RouteSelection, 0, len(routed.Routes))
	for _, route := range tier {
		if semanticKey(route) == key {
			promoted = append(promoted, route)
			if !slices.Contains(selectedRoutes, route.Route.ID) {
				selectedRoutes = append(selectedRoutes, route.Route.ID)
			}
		}
	}
	for _, route := range tier {
		if semanticKey(route) != key {
			promoted = append(promoted, route)
		}
	}
	promoted = append(promoted, routed.Routes[end:]...)
	reason = "shadow"
	if policy.Mode == "enforce" {
		routed.Routes = promoted
		reason = "applied"
	}
}

func semanticKey(route RouteSelection) semanticModelKey {
	return semanticModelKey{route.Provider.ID, route.ProviderModel}
}

func buildSemanticCandidates(routes []RouteSelection, models []ProviderModel, req ChatCompletionRequest) ([]semanticCandidate, map[string]semanticModelKey) {
	messageJSON, err := json.Marshal(req.Messages)
	if err != nil {
		return nil, nil
	}
	promptBytes := int64(len(messageJSON))
	metadata := map[semanticModelKey]ProviderModel{}
	for _, model := range models {
		if model.Status == StatusActive {
			metadata[semanticModelKey{model.ProviderID, model.UpstreamModel}] = model
		}
	}
	seen := map[semanticModelKey]bool{}
	keys := map[string]semanticModelKey{}
	candidates := []semanticCandidate{}
	for _, route := range routes {
		key := semanticKey(route)
		if seen[key] {
			continue
		}
		seen[key] = true
		model, ok := metadata[key]
		if !ok || !semanticModelFitsRequest(model, req, promptBytes) {
			continue
		}
		id := fmt.Sprintf("candidate_%d", len(candidates)+1)
		candidate := semanticCandidate{ID: id, ProviderType: route.Provider.Type, Model: route.ProviderModel, Description: model.Metadata["routing_description"], Capabilities: model.Capabilities, InputModalities: model.InputModalities, SupportedParameters: model.SupportedParameters}
		encoded, _ := json.Marshal(candidate)
		// Bound metadata as well as prompt size; do not truncate capability claims.
		if len(encoded) > 2048 {
			continue
		}
		candidates = append(candidates, candidate)
		keys[id] = key
	}
	return candidates, keys
}

func semanticRequestText(req ChatCompletionRequest) (string, bool) {
	if req.MinP != nil || req.TopK != nil || req.Tools != nil || req.ToolChoice != nil || req.ParallelToolCalls != nil || req.ResponseFormat != nil || req.ReasoningEffort != nil || req.PromptCacheKey != nil || req.User != nil || req.Metadata != nil {
		return "", false
	}
	allowed := map[string]bool{"model": true, "messages": true, "stream": true, "stream_options": true, "max_tokens": true, "temperature": true, "top_p": true, "presence_penalty": true, "frequency_penalty": true, "stop": true}
	for key := range req.raw {
		if !allowed[key] {
			return "", false
		}
	}
	var userText []string
	size := 0
	for _, message := range req.Messages {
		if message.Name != "" || message.ToolCalls != nil || message.ToolCallID != "" || message.ReasoningContent != "" || message.ReasoningSignature != "" || message.RedactedReasoningContent != "" {
			return "", false
		}
		if message.Role != "user" && message.Role != "assistant" && message.Role != "system" && message.Role != "developer" {
			return "", false
		}
		for key := range message.raw {
			if key != "role" && key != "content" {
				return "", false
			}
		}
		content, ok := message.Content.(string)
		if !ok {
			return "", false
		}
		if message.Role == "user" {
			size += len(content) + 2
			if size > 8192 {
				return "", false
			}
			userText = append(userText, content)
		}
	}
	text := strings.TrimSpace(strings.Join(userText, "\n\n"))
	return text, text != ""
}

// SemanticProviderModels reads only catalog entries for the admitted candidates.
// Optional Store capability keeps decorators without catalog access fail-open.
func (s *GormStore) SemanticProviderModels(ctx context.Context, routes []RouteSelection) ([]ProviderModel, error) {
	keys := map[semanticModelKey]bool{}
	clauses := []string{}
	args := []any{}
	for _, route := range routes {
		key := semanticKey(route)
		if keys[key] {
			continue
		}
		keys[key] = true
		if len(keys) > semanticMaxCandidates {
			return nil, nil
		}
		clauses = append(clauses, "(provider_id = ? AND upstream_model = ?)")
		args = append(args, key.provider, key.model)
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	var models []ProviderModel
	err := s.db.WithContext(ctx).Where(strings.Join(clauses, " OR "), args...).Find(&models).Error
	return models, err
}

// Byte length is a conservative text-token bound. Known catalog limits and
// explicit parameter support are hard constraints, never model judgments.
func semanticModelFitsRequest(model ProviderModel, req ChatCompletionRequest, promptBytes int64) bool {
	if len(model.InputModalities) > 0 && !slices.Contains(model.InputModalities, "text") {
		return false
	}
	if len(model.OutputModalities) > 0 && !slices.Contains(model.OutputModalities, "text") {
		return false
	}
	if req.MaxTokens < 0 {
		return false
	}
	if model.ContextWindow > 0 && (promptBytes > model.ContextWindow || int64(req.MaxTokens) > model.ContextWindow-promptBytes) {
		return false
	}
	if len(model.SupportedParameters) == 0 {
		return true
	}
	for key, used := range map[string]bool{"temperature": req.Temperature != nil, "top_p": req.TopP != nil, "presence_penalty": req.PresencePenalty != nil, "frequency_penalty": req.FrequencyPenalty != nil, "stop": req.Stop != nil} {
		if used && !slices.Contains(model.SupportedParameters, key) {
			return false
		}
	}
	// OpenAI-compatible Chat forwards max_tokens unchanged. Other budget fields do not
	// establish compatibility without an adapter-specific translation contract.
	return req.MaxTokens == 0 || slices.Contains(model.SupportedParameters, "max_tokens")
}
