package server

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const semanticRoutingMetadataKey = "tokenhub_semantic_routing"

// SemanticRoutingPolicy configures the Jev strategy. Mode is retained for
// previously saved overlays; new Jev strategies use explicit model choices.
type SemanticRoutingPolicy struct {
	ResponseBindingRequired bool                       `json:"response_binding_required,omitempty"`
	Mode                    string                     `json:"mode"`
	MinConfidence           float64                    `json:"min_confidence"`
	Instructions            string                     `json:"instructions,omitempty"`
	DefaultCandidateID      string                     `json:"default_candidate_id,omitempty"`
	Candidates              []SemanticRoutingCandidate `json:"candidates,omitempty"`
}

type SemanticRoutingCandidate struct {
	ID            string `json:"id"`
	ProviderID    string `json:"provider_id"`
	ProviderModel string `json:"provider_model"`
	Criteria      string `json:"criteria"`
}

func (p *SemanticRoutingPolicy) UnmarshalJSON(data []byte) error {
	type policyAlias SemanticRoutingPolicy
	var raw struct {
		policyAlias
		MinConfidence *float64 `json:"min_confidence"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.MinConfidence == nil {
		return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", "Semantic routing requires an explicit confidence threshold")
	}
	*p = SemanticRoutingPolicy(raw.policyAlias)
	p.MinConfidence = *raw.MinConfidence
	return validateSemanticRoutingPolicy(p)
}

func validateSemanticRoutingPolicy(policy *SemanticRoutingPolicy) error {
	if policy == nil {
		return nil
	}
	if (policy.Mode != "off" && policy.Mode != "shadow" && policy.Mode != "enforce") ||
		math.IsNaN(policy.MinConfidence) || math.IsInf(policy.MinConfidence, 0) || policy.MinConfidence < 0 || policy.MinConfidence > 1 {
		return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", "Semantic routing requires mode off, shadow, or enforce and a confidence threshold between 0 and 1")
	}
	if len(policy.Instructions) > 4096 || len(policy.Candidates) > semanticMaxCandidates {
		return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", "Jev instructions or candidate count exceeds the limit")
	}
	seen := map[string]bool{}
	models := map[semanticModelKey]bool{}
	for _, candidate := range policy.Candidates {
		key := semanticModelKey{candidate.ProviderID, candidate.ProviderModel}
		if strings.TrimSpace(candidate.ID) == "" || candidate.ID == "no_preference" || len(candidate.ID) > 200 || seen[candidate.ID] || models[key] || strings.TrimSpace(candidate.Criteria) == "" || len(candidate.Criteria) > 2048 || candidate.ProviderID == "" || candidate.ProviderModel == "" {
			return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", "Jev candidates require unique identifiers, distinct models, and nonempty criteria up to 2048 bytes")
		}
		seen[candidate.ID], models[key] = true, true
	}
	if len(policy.Candidates) > 0 && !seen[policy.DefaultCandidateID] {
		return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", "The default model must be a configured candidate")
	}
	return nil
}

func validateJevStrategyPolicy(policy ModelRoutePolicy, routes []ModelRoute) error {
	if policy.Strategy != RouteStrategyJev {
		return nil
	}
	p := policy.SemanticRouting
	if p == nil || p.Mode != "enforce" || strings.TrimSpace(p.Instructions) == "" || len(p.Candidates) == 0 {
		return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", "Jev routing requires instructions, candidates, a default model, and enforce mode")
	}
	for _, candidate := range p.Candidates {
		found := false
		for _, route := range routes {
			if route.ProviderID == candidate.ProviderID && route.ProviderModel == candidate.ProviderModel {
				found = true
				break
			}
		}
		if !found {
			return NewHTTPError(http.StatusBadRequest, "invalid_semantic_routing_policy", "Jev candidates must reference this model's configured routes")
		}
	}
	return nil
}

func modelSemanticRoutingPolicy(model Model) SemanticRoutingPolicy {
	policy := SemanticRoutingPolicy{Mode: "off", MinConfidence: 0.65}
	if raw := model.Metadata[semanticRoutingMetadataKey]; raw != "" {
		if json.Unmarshal([]byte(raw), &policy) != nil || validateSemanticRoutingPolicy(&policy) != nil {
			return SemanticRoutingPolicy{Mode: "off", MinConfidence: 0.65}
		}
	}
	return policy
}

func saveSemanticRoutingPolicy(tx *gorm.DB, modelName string, policy *SemanticRoutingPolicy) error {
	if policy == nil {
		return nil
	}
	var model Model
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, "name = ?", modelName).Error; err != nil {
		return notFound(err, "model_not_found", "Model not found")
	}
	if model.Metadata == nil {
		model.Metadata = map[string]string{}
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	model.Metadata[semanticRoutingMetadataKey] = string(encoded)
	return tx.Model(&model).Select("Metadata").Updates(&model).Error
}

// Only the routing policy endpoint may replace this managed field on an existing
// model. Ordinary edits and catalog imports can carry stale or absent metadata.
func preserveSemanticRoutingMetadata(current, incoming map[string]string) map[string]string {
	if incoming == nil && current[semanticRoutingMetadataKey] == "" {
		return nil
	}
	metadata := make(map[string]string, len(incoming)+1)
	for key, value := range incoming {
		if key != semanticRoutingMetadataKey {
			metadata[key] = value
		}
	}
	if value, ok := current[semanticRoutingMetadataKey]; ok {
		metadata[semanticRoutingMetadataKey] = value
	}
	return metadata
}
