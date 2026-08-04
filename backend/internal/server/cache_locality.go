package server

import (
	"net/http"
	"strings"
)

// AffinityKindCacheLocality marks stateless cache locality routing. Unlike
// AffinityKindCodexSession it persists no binding at all and converges purely
// through rendezvous scoring.
const AffinityKindCacheLocality = "cache_locality"

// cacheDomainID returns the upstream cache domain a candidate belongs to.
//
// The cache domain answers "at which layer is the cache isolated". Measured
// against a live upstream: DeepSeek does not share prefix cache between
// deepseek-v4-pro and deepseek-v4-flash under the same API key, so the domain
// must cover both the account and the upstream model.
//
// Every segment carries a type prefix so identifiers from different sources
// cannot collide.
//
// route.Route.ID must never be part of this: route IDs are generated at creation
// time, so deleting and recreating a rule that points at the same account would
// reshuffle every session and invalidate the upstream cache wholesale.
//
// This is a durable contract: changing the concatenation order or any segment's
// source is equivalent to invalidating every live cache and must be versioned.
func cacheDomainID(route RouteSelection) string {
	account := ""
	switch {
	case strings.TrimSpace(route.Provider.Options["organization_id"]) != "":
		account = "org:" + strings.TrimSpace(route.Provider.Options["organization_id"])
	case strings.TrimSpace(route.Provider.Options["account_id"]) != "":
		account = "acct:" + strings.TrimSpace(route.Provider.Options["account_id"])
	case strings.TrimSpace(routeResourceID(route)) != "":
		account = "rsrc:" + strings.TrimSpace(routeResourceID(route))
	default:
		// routeResourceID returns an empty string when the route has no resource
		// bound. Without this fallback all such candidates collapse into one
		// identity, score identically under rendezvous, and lose all separation.
		account = "prov:" + strings.TrimSpace(route.Provider.ID)
	}
	return account + "|model:" + strings.TrimSpace(route.ProviderModel)
}

// resolveCacheLocalityAffinity derives a stateless cache locality affinity key
// for the non-Codex protocol paths.
//
// Identifiers reported as sessionScopeUser (Anthropic metadata.user_id, OpenAI
// user) are coarser: one user's concurrent sessions share the value, pinning all
// of their traffic to a single account. Whether to accept them is up to
// allowUserScope.
func resolveCacheLocalityAffinity(
	secret string,
	apiKeyID string,
	identifier string,
	scope sessionIdentifierScope,
	allowUserScope bool,
) (*RequestAffinity, error) {
	if scope == sessionScopeNone {
		return nil, nil
	}
	if scope == sessionScopeUser && !allowUserScope {
		return nil, nil
	}
	keyHash, err := sessionAffinityKey(secret, apiKeyID, identifier)
	if err != nil {
		return nil, err
	}
	return &RequestAffinity{
		// AdapterType is left empty because this mode does not distinguish
		// adapters. What keeps it out of the binding table is Kind:
		// persistsBinding() only returns true for Codex sessions.
		Kind:    AffinityKindCacheLocality,
		KeyHash: keyHash,
	}, nil
}

// cacheAffinityEnabledForModel reports whether cache locality routing applies to
// a model. An empty allowlist means the master switch covers every model.
func (s *Server) cacheAffinityEnabledForModel(model string) bool {
	if !s.config.CacheAffinityEnabled {
		return false
	}
	if len(s.config.CacheAffinityModels) == 0 {
		return true
	}
	model = strings.TrimSpace(model)
	for _, candidate := range s.config.CacheAffinityModels {
		if strings.EqualFold(strings.TrimSpace(candidate), model) {
			return true
		}
	}
	return false
}

// anthropicCacheLocalityAffinity resolves the cache locality affinity key for
// /v1/messages.
func (s *Server) anthropicCacheLocalityAffinity(apiKeyID string, model string, headers http.Header, raw map[string]any) (*RequestAffinity, error) {
	if !s.cacheAffinityEnabledForModel(model) {
		return nil, nil
	}
	identifier, scope := anthropicSessionIdentifier(headers, raw)
	return resolveCacheLocalityAffinity(s.config.SecretKey, apiKeyID, identifier, scope, s.config.CacheAffinityAllowUserScope)
}

// chatCacheLocalityAffinity resolves the cache locality affinity key for
// /v1/chat/completions.
func (s *Server) chatCacheLocalityAffinity(apiKeyID string, headers http.Header, request ChatCompletionRequest) (*RequestAffinity, error) {
	if !s.cacheAffinityEnabledForModel(request.Model) {
		return nil, nil
	}
	identifier, scope := chatCompletionSessionIdentifier(headers, request)
	return resolveCacheLocalityAffinity(s.config.SecretKey, apiKeyID, identifier, scope, s.config.CacheAffinityAllowUserScope)
}

// responsesCacheLocalityAffinity resolves stateless cache locality for generic
// /v1/responses providers such as DeepSeek. The upstream may ignore
// prompt_cache_key (DeepSeek manages its prefix cache automatically), but Codex
// still sends stable session hints that are useful to keep successive turns on
// the same upstream account.
func (s *Server) responsesCacheLocalityAffinity(apiKeyID string, headers http.Header, request ResponsesRequest) (*RequestAffinity, error) {
	if !s.cacheAffinityEnabledForModel(request.Model) {
		return nil, nil
	}
	identifier, scope := responsesSessionIdentifier(headers, request)
	return resolveCacheLocalityAffinity(s.config.SecretKey, apiKeyID, identifier, scope, s.config.CacheAffinityAllowUserScope)
}

func responsesSessionIdentifier(headers http.Header, request ResponsesRequest) (string, sessionIdentifierScope) {
	for _, value := range []string{
		headers.Get("x-tokenhub-session-id"),
		headers.Get("session-id"),
		codexClientMetadataSessionID(request),
		codexRawStringField(request, "prompt_cache_key"),
		headers.Get("thread-id"),
	} {
		if value = strings.TrimSpace(value); value != "" {
			return value, sessionScopeSession
		}
	}
	if value := strings.TrimSpace(codexRawStringField(request, "user")); value != "" {
		return value, sessionScopeUser
	}
	return "", sessionScopeNone
}
