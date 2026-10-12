package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	pluginmeta "tokenhub/backend/internal/plugin"
)

// Bind cached rankings to caller scope, input and effective upstream configuration.
func rerankCacheKey(call CallContext, route RouteSelection, request RerankRequest) string {
	data, _ := json.Marshal(struct {
		Project, Key, User, Route, Resource, Provider, Type, Model, URL, Protocol string
		Options, Headers                                                          map[string]string
		Request                                                                   RerankRequest
	}{call.Project.ID, call.Key.ID, call.AttributedUserID, route.Route.ID, routeResourceID(route), route.Provider.ID, route.Provider.Type, route.ProviderModel, route.Provider.BaseURL, providerRerankProtocol(route.Provider), route.Provider.Options, route.Provider.Headers, request})
	sum := sha256.Sum256(data)
	return "rerank:v1:" + hex.EncodeToString(sum[:])
}
func addRerankCacheContract(input *pluginmeta.GatewayHookInput, call CallContext) {
	if call.RerankCacheKey == "" {
		return
	}
	key, _ := json.Marshal(call.RerankCacheKey)
	input.Data[pluginmeta.DataCacheKey] = key
	if input.Envelope.Metadata == nil {
		input.Envelope.Metadata = map[string]json.RawMessage{}
	}
	input.Envelope.Metadata["rerank_cache_key"] = key
}
func rerankCacheHitMatches(call CallContext, result pluginmeta.GatewayHookRunResult) bool {
	if call.RerankCacheKey == "" {
		return true
	}
	patch, ok := result.Writes[pluginmeta.DataCacheKey]
	var key string
	return ok && json.Unmarshal(patch.Value, &key) == nil && key == call.RerankCacheKey
}
