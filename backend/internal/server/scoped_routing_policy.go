package server

import (
	"net/http"
	"sort"
	"strings"
)

const routingPolicyResourceKind = "routing-policies"

const (
	RoutingPolicyScopeUnbound = "unbound"
	RoutingPolicyScopeGlobal  = "global"
	RoutingPolicyScopeProject = "project"
	RoutingPolicyScopeAPIKey  = "api_key"
)

// ScopedRoutingPolicy is the typed runtime view of a routing-policies
// AdminResource. Keeping persistence generic preserves the existing governance
// CRUD and audit machinery; callers only learn this small routing interface.
type ScopedRoutingPolicy struct {
	ID                         string   `json:"id"`
	Name                       string   `json:"name"`
	Status                     string   `json:"status"`
	Scope                      string   `json:"scope"`
	ScopeID                    string   `json:"scope_id,omitempty"`
	Priority                   int      `json:"priority"`
	Strategy                   string   `json:"strategy,omitempty"`
	AllowedProviderIDs         []string `json:"allowed_provider_ids,omitempty"`
	AllowedProviderResourceIDs []string `json:"allowed_provider_resource_ids,omitempty"`
	AllowedModels              []string `json:"allowed_models,omitempty"`
	RequiredTags               []string `json:"required_tags,omitempty"`
	AllowedRegions             []string `json:"allowed_regions,omitempty"`
	AllowedEnvironments        []string `json:"allowed_environments,omitempty"`
}

type RoutingPolicyCandidateDecision struct {
	RouteID       string   `json:"route_id"`
	ProviderID    string   `json:"provider_id"`
	ResourceID    string   `json:"resource_id,omitempty"`
	ProviderModel string   `json:"provider_model"`
	Allowed       bool     `json:"allowed"`
	Reasons       []string `json:"reasons"`
}

type RoutingPolicyResolution struct {
	EffectivePolicy *ScopedRoutingPolicy             `json:"effective_policy,omitempty"`
	MatchReason     string                           `json:"match_reason"`
	Candidates      []RoutingPolicyCandidateDecision `json:"candidates"`
	SelectedRouteID string                           `json:"selected_route_id,omitempty"`
	ErrorCode       string                           `json:"error_code,omitempty"`
	ErrorMessage    string                           `json:"error_message,omitempty"`
	AccessAllowed   bool                             `json:"access_allowed"`
	AccessReasons   []string                         `json:"access_reasons,omitempty"`
}

func scopedRoutingPolicy(resource AdminResource) ScopedRoutingPolicy {
	fields := resource.Fields
	scope := normalizeRoutingPolicyScope(stringField(fields, "scope"))
	scopeID := strings.TrimSpace(stringField(fields, "scope_id"))
	if scope == RoutingPolicyScopeGlobal {
		scopeID = RoutingPolicyScopeGlobal
	}
	return ScopedRoutingPolicy{
		ID:                         resource.ID,
		Name:                       resource.Name,
		Status:                     resource.Status,
		Scope:                      scope,
		ScopeID:                    scopeID,
		Priority:                   routingPolicyPriority(scope),
		Strategy:                   strings.TrimSpace(stringField(fields, "strategy")),
		AllowedProviderIDs:         normalizedPolicyValues(fields["allowed_provider_ids"]),
		AllowedProviderResourceIDs: normalizedPolicyValues(fields["allowed_provider_resource_ids"]),
		AllowedModels:              normalizedPolicyValues(fields["allowed_models"]),
		RequiredTags:               normalizedPolicyValues(fields["required_tags"]),
		AllowedRegions:             normalizedPolicyValues(fields["allowed_regions"]),
		AllowedEnvironments:        normalizedPolicyValues(fields["allowed_environments"]),
	}
}

func normalizedPolicyValues(value any) []string {
	items := uniqueStrings(stringSliceFromPayload(value))
	for index := range items {
		items[index] = strings.TrimSpace(items[index])
	}
	sort.Strings(items)
	return items
}

func normalizeRoutingPolicyScope(scope string) string {
	scope = strings.ToLower(strings.TrimSpace(scope))
	switch scope {
	case RoutingPolicyScopeGlobal, RoutingPolicyScopeProject, RoutingPolicyScopeAPIKey:
		return scope
	default:
		return RoutingPolicyScopeUnbound
	}
}

func routingPolicyPriority(scope string) int {
	switch normalizeRoutingPolicyScope(scope) {
	case RoutingPolicyScopeAPIKey:
		return 1
	case RoutingPolicyScopeProject:
		return 2
	case RoutingPolicyScopeGlobal:
		return 3
	default:
		return 0
	}
}

func routingPolicyBindingKey(fields map[string]any) *string {
	scope := normalizeRoutingPolicyScope(stringField(fields, "scope"))
	scopeID := strings.TrimSpace(stringField(fields, "scope_id"))
	if scope == RoutingPolicyScopeGlobal {
		scopeID = RoutingPolicyScopeGlobal
	}
	if scope == RoutingPolicyScopeUnbound || scopeID == "" {
		return nil
	}
	key := scope + ":" + scopeID
	return &key
}

func effectiveScopedRoutingPolicy(resources []AdminResource, projectID string, keyID string) (*ScopedRoutingPolicy, error) {
	projectID = strings.TrimSpace(projectID)
	keyID = strings.TrimSpace(keyID)
	levels := []struct {
		scope   string
		scopeID string
	}{
		{scope: RoutingPolicyScopeAPIKey, scopeID: keyID},
		{scope: RoutingPolicyScopeProject, scopeID: projectID},
		{scope: RoutingPolicyScopeGlobal, scopeID: RoutingPolicyScopeGlobal},
	}
	for _, level := range levels {
		if level.scopeID == "" {
			continue
		}
		matches := make([]ScopedRoutingPolicy, 0, 1)
		for _, resource := range resources {
			policy := scopedRoutingPolicy(resource)
			if policy.Scope == level.scope && policy.ScopeID == level.scopeID {
				matches = append(matches, policy)
			}
		}
		if len(matches) > 1 {
			return nil, NewHTTPError(http.StatusConflict, "routing_policy_conflict", "Multiple routing policies are bound to the same scope")
		}
		if len(matches) == 1 {
			if matches[0].Status != StatusActive {
				return &matches[0], NewHTTPError(http.StatusServiceUnavailable, "routing_policy_unavailable", "The effective routing policy is unavailable")
			}
			return &matches[0], nil
		}
	}
	return nil, nil
}

func (s *Server) resolveScopedRoutingPolicy(call CallContext, routes []RouteSelection) ([]RouteSelection, RoutingPolicyResolution, error) {
	policy, err := effectiveScopedRoutingPolicy(s.store.ListResources(routingPolicyResourceKind), call.Project.ID, call.Key.ID)
	resolution := RoutingPolicyResolution{EffectivePolicy: policy, MatchReason: "no_binding", Candidates: make([]RoutingPolicyCandidateDecision, 0, len(routes)), AccessAllowed: true}
	if policy != nil {
		resolution.MatchReason = policy.Scope + "_binding"
	}
	if err != nil {
		return nil, resolution, err
	}
	allowed := make([]RouteSelection, 0, len(routes))
	for _, route := range routes {
		decision := RoutingPolicyCandidateDecision{
			RouteID:       route.Route.ID,
			ProviderID:    route.Provider.ID,
			ResourceID:    routeResourceID(route),
			ProviderModel: route.ProviderModel,
			Allowed:       true,
			Reasons:       []string{"eligible"},
		}
		reasons := routingPolicyCandidateReasons(call, route, policy)
		if len(reasons) > 0 {
			decision.Allowed = false
			decision.Reasons = reasons
		}
		resolution.Candidates = append(resolution.Candidates, decision)
		if decision.Allowed {
			if policy != nil && policy.Strategy != "" && policy.Strategy != "inherit" {
				route.Route.Strategy = policy.Strategy
			}
			allowed = append(allowed, route)
		}
	}
	if policy != nil && len(allowed) == 0 {
		return nil, resolution, NewHTTPError(http.StatusServiceUnavailable, "routing_policy_no_candidate", "No eligible provider route for the effective routing policy")
	}
	return allowed, resolution, nil
}

func routingPolicyCandidateReasons(call CallContext, route RouteSelection, policy *ScopedRoutingPolicy) []string {
	reasons := make([]string, 0, 7)
	if !routeMatchesProject(route.Route, call.Project.ID) {
		reasons = append(reasons, "project_scope_excluded")
	}
	if policy == nil {
		return reasons
	}
	if allowedModels := AllowedModelSet(policy.AllowedModels); len(allowedModels) > 0 && !allowedModels[call.Model.Name] {
		reasons = append(reasons, "model_not_allowed")
	}
	if allowedProviders := AllowedModelSet(policy.AllowedProviderIDs); len(allowedProviders) > 0 && !allowedProviders[route.Provider.ID] {
		reasons = append(reasons, "provider_not_allowed")
	}
	if allowedResources := AllowedModelSet(policy.AllowedProviderResourceIDs); len(allowedResources) > 0 && !allowedResources[routeResourceID(route)] {
		reasons = append(reasons, "resource_not_allowed")
	}
	if !routeHasRequiredTags(route.Route.Tags, policy.RequiredTags) {
		reasons = append(reasons, "required_tags_missing")
	}
	region, environment := "", ""
	if route.Resource != nil {
		region = strings.ToLower(strings.TrimSpace(route.Resource.Region))
		environment = strings.ToLower(strings.TrimSpace(route.Resource.Environment))
	}
	if allowedRegions := lowerStringSet(policy.AllowedRegions); len(allowedRegions) > 0 && !allowedRegions[region] {
		reasons = append(reasons, "region_not_allowed")
	}
	if allowedEnvironments := lowerStringSet(policy.AllowedEnvironments); len(allowedEnvironments) > 0 && !allowedEnvironments[environment] {
		reasons = append(reasons, "environment_not_allowed")
	}
	return reasons
}

func applyRoutingPolicyResolution(call *CallContext, resolution RoutingPolicyResolution) {
	if call == nil || resolution.EffectivePolicy == nil {
		return
	}
	call.RoutingPolicyID = resolution.EffectivePolicy.ID
	call.RoutingPolicyScope = resolution.EffectivePolicy.Scope
	call.RoutingPolicyPriority = resolution.EffectivePolicy.Priority
}

func (s *Server) annotateRoutingPolicyForCandidateError(call *CallContext, candidateErr error) error {
	if call == nil {
		return candidateErr
	}
	policy, policyErr := effectiveScopedRoutingPolicy(s.store.ListResources(routingPolicyResourceKind), call.Project.ID, call.Key.ID)
	applyRoutingPolicyResolution(call, RoutingPolicyResolution{EffectivePolicy: policy})
	if policyErr != nil {
		return policyErr
	}
	return candidateErr
}

func (s *Server) resolveScopedRoutingPolicyForCall(call *CallContext, routes []RouteSelection) ([]RouteSelection, error) {
	filtered, resolution, err := s.resolveScopedRoutingPolicy(*call, routes)
	applyRoutingPolicyResolution(call, resolution)
	return filtered, err
}

func modelAccessReasons(project Project, key APIKey, modelName string) []string {
	hydrateProject(&project)
	hydrateAPIKey(&key)
	reasons := []string{}
	if project.ModelAccessMode == ModelAccessModeRestricted && !AllowedModelSet(project.AllowedModels)[modelName] {
		reasons = append(reasons, "project_model_not_allowed")
	}
	if key.ModelAccessMode == ModelAccessModeRestricted && !key.AllowedModels[modelName] {
		reasons = append(reasons, "api_key_model_not_allowed")
	}
	return reasons
}

func (s *Server) validateScopedRoutingPolicyMutation(resourceID string, req AdminResource) error {
	resource := req
	if resourceID != "" {
		existing, err := s.findResource(routingPolicyResourceKind, resourceID)
		if err != nil {
			return err
		}
		resource = mergedAdminResource(existing, req)
	}
	if resource.Fields == nil {
		resource.Fields = map[string]any{}
	}
	fields := resource.Fields
	scope := strings.ToLower(strings.TrimSpace(stringField(fields, "scope")))
	switch scope {
	case "", RoutingPolicyScopeUnbound:
		scope = RoutingPolicyScopeUnbound
	case RoutingPolicyScopeGlobal, RoutingPolicyScopeProject, RoutingPolicyScopeAPIKey:
	default:
		return NewHTTPError(http.StatusBadRequest, "invalid_routing_policy_scope", "scope must be unbound, global, project, or api_key")
	}
	scopeID := strings.TrimSpace(stringField(fields, "scope_id"))
	switch scope {
	case RoutingPolicyScopeUnbound:
		scopeID = ""
	case RoutingPolicyScopeGlobal:
		if scopeID != "" && !strings.EqualFold(scopeID, RoutingPolicyScopeGlobal) {
			return NewHTTPError(http.StatusBadRequest, "invalid_routing_policy_scope_id", "global policies must use the global scope ID")
		}
		scopeID = RoutingPolicyScopeGlobal
	case RoutingPolicyScopeProject:
		if scopeID == "" {
			return NewHTTPError(http.StatusBadRequest, "routing_policy_scope_id_required", "scope_id is required for project policies")
		}
		if _, ok := s.store.GetProject(scopeID); !ok {
			return NewHTTPError(http.StatusNotFound, "project_not_found", "Project not found")
		}
	case RoutingPolicyScopeAPIKey:
		if scopeID == "" {
			return NewHTTPError(http.StatusBadRequest, "routing_policy_scope_id_required", "scope_id is required for API key policies")
		}
		if _, ok := s.routingPolicyAPIKey(scopeID); !ok {
			return NewHTTPError(http.StatusNotFound, "api_key_not_found", "API key not found")
		}
	}
	if scope != RoutingPolicyScopeUnbound {
		for _, candidate := range s.store.ListResources(routingPolicyResourceKind) {
			if candidate.ID == resourceID {
				continue
			}
			policy := scopedRoutingPolicy(candidate)
			if policy.Scope == scope && policy.ScopeID == scopeID {
				return NewHTTPError(http.StatusConflict, "routing_policy_binding_conflict", "A routing policy is already bound to this scope")
			}
		}
	}

	strategy := strings.ToLower(strings.TrimSpace(stringField(fields, "strategy")))
	if strategy != "" && strategy != "inherit" && !validScopedRoutingStrategy(strategy) {
		return NewHTTPError(http.StatusBadRequest, "invalid_routing_policy_strategy", "Unsupported routing policy strategy")
	}
	providers := normalizedPolicyValues(fields["allowed_provider_ids"])
	resources := normalizedPolicyValues(fields["allowed_provider_resource_ids"])
	models := normalizedPolicyValues(fields["allowed_models"])
	requiredTags := normalizedPolicyValues(fields["required_tags"])
	regions := normalizedLowerPolicyValues(fields["allowed_regions"])
	environments := normalizedLowerPolicyValues(fields["allowed_environments"])

	providerSet := AllowedModelSet(providers)
	knownProviders := map[string]bool{}
	for _, provider := range s.store.ListProviders() {
		knownProviders[provider.ID] = true
	}
	for _, providerID := range providers {
		if !knownProviders[providerID] {
			return NewHTTPError(http.StatusNotFound, "provider_not_found", "Provider not found")
		}
	}
	knownResources := map[string]ProviderResource{}
	for _, providerResource := range s.store.ListProviderResources() {
		knownResources[providerResource.ID] = providerResource
	}
	for _, providerResourceID := range resources {
		providerResource, ok := knownResources[providerResourceID]
		if !ok {
			return NewHTTPError(http.StatusNotFound, "provider_resource_not_found", "Provider resource not found")
		}
		if len(providerSet) > 0 && !providerSet[providerResource.ProviderID] {
			return NewHTTPError(http.StatusBadRequest, "routing_policy_resource_provider_mismatch", "Allowed provider resources must belong to an allowed Provider")
		}
	}
	knownModels := map[string]bool{}
	for _, model := range s.store.ListModels() {
		knownModels[model.Name] = true
	}
	for _, modelName := range models {
		if !knownModels[modelName] {
			return NewHTTPError(http.StatusNotFound, "model_not_found", "Model not found")
		}
	}

	fields["scope"] = scope
	fields["scope_id"] = scopeID
	fields["priority"] = routingPolicyPriority(scope)
	fields["strategy"] = strategy
	fields["allowed_provider_ids"] = providers
	fields["allowed_provider_resource_ids"] = resources
	fields["allowed_models"] = models
	fields["required_tags"] = requiredTags
	fields["allowed_regions"] = regions
	fields["allowed_environments"] = environments
	return nil
}

func (s *Server) routingPolicyAPIKey(id string) (APIKey, bool) {
	for _, key := range s.store.ListAPIKeys() {
		if key.ID == id {
			return key, true
		}
	}
	return APIKey{}, false
}

func normalizedLowerPolicyValues(value any) []string {
	values := normalizedPolicyValues(value)
	for index := range values {
		values[index] = strings.ToLower(values[index])
	}
	return uniqueStrings(values)
}

func validScopedRoutingStrategy(strategy string) bool {
	switch strategy {
	case RouteStrategyBalanced, RouteStrategyAdaptive, RouteStrategyCost, RouteStrategyQuality, RouteStrategyPriorityWeighted, RouteStrategyPriorityOnly:
		return true
	default:
		return false
	}
}

func lowerStringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[strings.ToLower(strings.TrimSpace(value))] = true
	}
	return set
}

func routeHasRequiredTags(routeTags []string, requiredTags []string) bool {
	if len(requiredTags) == 0 {
		return true
	}
	available := lowerStringSet(routeTags)
	for _, required := range requiredTags {
		if !available[strings.ToLower(strings.TrimSpace(required))] {
			return false
		}
	}
	return true
}
