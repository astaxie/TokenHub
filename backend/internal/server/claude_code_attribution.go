package server

import (
	"net/http"
	"strings"
)

const (
	systemPromptTransformPolicyOption  = "system_prompt_transform_policy"
	systemPromptTransformDefaultPolicy = "system_prompt_transform_default"
	systemPromptTransformPreserve      = "preserve"
	systemPromptTransformStrip         = "strip"
	claudeCodeAttributionPolicyOption  = "claude_code_attribution_policy"
	claudeCodeAttributionDefaultPolicy = "claude_code_attribution_default"
	claudeCodeAttributionPreserve      = systemPromptTransformPreserve
	claudeCodeAttributionStrip         = systemPromptTransformStrip
	claudeCodeAttributionPrefix        = "x-anthropic-billing-header:"
)

func anthropicRequestForRoute(req anthropicMessagesRequest, route RouteSelection) anthropicMessagesRequest {
	if effectiveSystemPromptTransformPolicy(route.Provider.Options) != systemPromptTransformStrip {
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

func defaultClaudeCodeAttributionPolicyForDescriptor(descriptor AdapterDescriptor) string {
	return defaultSystemPromptTransformPolicyForDescriptor(descriptor)
}

func defaultSystemPromptTransformPolicyForDescriptor(descriptor AdapterDescriptor) string {
	if policy := normalizeSystemPromptTransformPolicyOrEmpty(descriptor.ProviderPolicy.SystemPromptTransformDefault); policy != "" {
		return policy
	}
	if policy := normalizeSystemPromptTransformPolicyOrEmpty(descriptor.ProviderPolicy.ClaudeCodeAttributionDefault); policy != "" {
		return policy
	}
	return systemPromptTransformStrip
}

func normalizeClaudeCodeAttributionPolicyOrEmpty(value string) string {
	return normalizeSystemPromptTransformPolicyOrEmpty(value)
}

func normalizeSystemPromptTransformPolicyOrEmpty(value string) string {
	policy := strings.ToLower(strings.TrimSpace(value))
	if policy == systemPromptTransformPreserve || policy == systemPromptTransformStrip {
		return policy
	}
	return ""
}

func applyClaudeCodeAttributionPolicy(options map[string]string, requested *string) (map[string]string, error) {
	return applySystemPromptTransformPolicy(options, nil, requested)
}

func applySystemPromptTransformPolicy(options map[string]string, requested *string, legacyRequested *string) (map[string]string, error) {
	if requested == nil && legacyRequested == nil {
		return options, nil
	}
	source := requested
	if source == nil {
		source = legacyRequested
	}
	policy, err := normalizeSystemPromptTransformPolicy(*source)
	if err != nil {
		return nil, err
	}
	next := make(map[string]string, len(options)+1)
	for key, value := range options {
		next[key] = value
	}
	next[systemPromptTransformPolicyOption] = policy
	delete(next, claudeCodeAttributionPolicyOption)
	return next, nil
}

func validateClaudeCodeAttributionOptions(options map[string]string) error {
	return validateSystemPromptTransformOptions(options)
}

func validateSystemPromptTransformOptions(options map[string]string) error {
	if value, exists := options[systemPromptTransformPolicyOption]; exists {
		policy, err := normalizeSystemPromptTransformPolicy(value)
		if err != nil {
			return err
		}
		options[systemPromptTransformPolicyOption] = policy
		delete(options, claudeCodeAttributionPolicyOption)
		return nil
	}
	if value, exists := options[claudeCodeAttributionPolicyOption]; exists {
		policy, err := normalizeSystemPromptTransformPolicy(value)
		if err != nil {
			return err
		}
		options[claudeCodeAttributionPolicyOption] = policy
	}
	return nil
}

func normalizeClaudeCodeAttributionPolicy(value string) (string, error) {
	return normalizeSystemPromptTransformPolicy(value)
}

func normalizeSystemPromptTransformPolicy(value string) (string, error) {
	if policy := normalizeClaudeCodeAttributionPolicyOrEmpty(value); policy != "" {
		return policy, nil
	}
	return "", NewHTTPError(
		http.StatusBadRequest,
		"invalid_system_prompt_transform_policy",
		"system_prompt_transform_policy must be preserve or strip",
	)
}

func effectiveSystemPromptTransformPolicy(options map[string]string) string {
	if policy := normalizeSystemPromptTransformPolicyOrEmpty(options[systemPromptTransformPolicyOption]); policy != "" {
		return policy
	}
	return normalizeSystemPromptTransformPolicyOrEmpty(options[claudeCodeAttributionPolicyOption])
}
