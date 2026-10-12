package server

import (
	"net/http"
	"strings"
)

func currentAnthropicModelContract(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	for _, family := range []string{"claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"} {
		index := strings.LastIndex(normalized, family)
		if index >= 0 && anthropicModelBoundary(normalized, index-1) && anthropicModelBoundary(normalized, index+len(family)) {
			return family
		}
	}
	return ""
}

func validateCurrentAnthropicToolChoice(model string, payload map[string]any) error {
	switch currentAnthropicModelContract(model) {
	case "claude-fable-5-1", "claude-opus-5-5", "claude-sonnet-5-5":
		choice, _ := payload["tool_choice"].(map[string]any)
		if choice["type"] == "any" || choice["type"] == "tool" {
			return NewHTTPError(http.StatusBadRequest, "unsupported_tool_choice", "This model supports auto or none tool choice, but not forced tool use")
		}
	}
	return nil
}
