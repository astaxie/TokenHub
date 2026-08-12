package server

import (
	"net/http"
	"strings"
)

const (
	claudeCodeAttributionPolicyOption = "claude_code_attribution_policy"
	claudeCodeAttributionPreserve     = "preserve"
	claudeCodeAttributionStrip        = "strip"
	claudeCodeAttributionPrefix       = "x-anthropic-billing-header:"
)

func anthropicRequestForRoute(req anthropicMessagesRequest, route RouteSelection) anthropicMessagesRequest {
	if strings.TrimSpace(route.Provider.Options[claudeCodeAttributionPolicyOption]) != claudeCodeAttributionStrip {
		return req
	}
	system, ok := req.Raw["system"].([]any)
	if !ok || len(system) == 0 {
		return req
	}
	first, ok := system[0].(map[string]any)
	if !ok || first["type"] != "text" {
		return req
	}
	text, ok := first["text"].(string)
	if !ok || !strings.HasPrefix(text, claudeCodeAttributionPrefix) {
		return req
	}

	filtered := cloneAnyMap(req.Raw)
	if len(system) == 1 {
		delete(filtered, "system")
	} else {
		filtered["system"] = append([]any(nil), system[1:]...)
	}
	req.Raw = filtered
	return req
}

func defaultClaudeCodeAttributionPolicy(providerType string, catalogID string) string {
	if strings.TrimSpace(providerType) != ProviderAnthropic {
		return claudeCodeAttributionStrip
	}
	catalogID = strings.ToLower(strings.TrimSpace(catalogID))
	if catalogID == "" || catalogID == "custom" || catalogID == "anthropic" {
		return claudeCodeAttributionPreserve
	}
	return claudeCodeAttributionStrip
}

func applyClaudeCodeAttributionPolicy(options map[string]string, requested *string) (map[string]string, error) {
	if requested == nil {
		return options, nil
	}
	policy, err := normalizeClaudeCodeAttributionPolicy(*requested)
	if err != nil {
		return nil, err
	}
	next := make(map[string]string, len(options)+1)
	for key, value := range options {
		next[key] = value
	}
	next[claudeCodeAttributionPolicyOption] = policy
	return next, nil
}

func validateClaudeCodeAttributionOptions(options map[string]string) error {
	value, exists := options[claudeCodeAttributionPolicyOption]
	if !exists {
		return nil
	}
	policy, err := normalizeClaudeCodeAttributionPolicy(value)
	if err != nil {
		return err
	}
	options[claudeCodeAttributionPolicyOption] = policy
	return nil
}

func normalizeClaudeCodeAttributionPolicy(value string) (string, error) {
	policy := strings.ToLower(strings.TrimSpace(value))
	if policy == claudeCodeAttributionPreserve || policy == claudeCodeAttributionStrip {
		return policy, nil
	}
	return "", NewHTTPError(
		http.StatusBadRequest,
		"invalid_claude_code_attribution_policy",
		"claude_code_attribution_policy must be preserve or strip",
	)
}
