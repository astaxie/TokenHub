package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	pluginmeta "tokenhub/backend/internal/plugin"
)

func embeddingCacheKey(call CallContext, space string, request EmbeddingsRequest) (string, error) {
	data, err := json.Marshal(struct {
		Space, Project, Key, User string
		Request                   EmbeddingsRequest
	}{space, call.Project.ID, call.Key.ID, call.AttributedUserID, request})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "embedding:v2:" + hex.EncodeToString(sum[:]), nil
}
func addEmbeddingCacheContract(input *pluginmeta.GatewayHookInput, call CallContext) {
	if call.EmbeddingCacheKey == "" {
		return
	}
	key, _ := json.Marshal(call.EmbeddingCacheKey)
	input.Data[pluginmeta.DataCacheKey] = key
	input.Envelope.Metadata = map[string]json.RawMessage{"embedding_cache_key": key}
}
func embeddingCacheHitMatches(call CallContext, result pluginmeta.GatewayHookRunResult) bool {
	if call.EmbeddingCacheKey == "" {
		return true
	}
	patch, ok := result.Writes[pluginmeta.DataCacheKey]
	if !ok {
		return false
	}
	var key string
	return json.Unmarshal(patch.Value, &key) == nil && key == call.EmbeddingCacheKey
}
