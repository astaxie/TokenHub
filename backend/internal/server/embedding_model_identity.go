package server

import "strings"

// Name inference is a fallback for discovery APIs that omit type metadata.
// Rerank names must be classified before entering this function.
func isKnownEmbeddingModelName(value string) bool {
	for _, word := range strings.Fields(strings.ToLower(value)) {
		if i := strings.LastIndex(word, "/"); i >= 0 {
			word = word[i+1:]
		}
		for _, prefix := range []string{"bge-", "gte-", "e5-", "multilingual-e5-", "voyage-", "all-minilm-", "all-mpnet-"} {
			if strings.HasPrefix(word, prefix) {
				return true
			}
		}
	}
	return false
}
