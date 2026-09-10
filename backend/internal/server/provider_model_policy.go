package server

import "strings"

func providerModelInAllowlist(providerModel string, allowlist []string) bool {
	model := strings.ToLower(strings.TrimSpace(providerModel))
	if model == "" {
		return false
	}
	for _, candidate := range allowlist {
		if model == strings.ToLower(strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}
