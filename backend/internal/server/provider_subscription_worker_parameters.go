package server

import (
	"slices"
	"strings"
)

func (a *SubscriptionWorkerAdapter) modelParameters(model subscriptionWorkerModel) []string {
	if !slices.Contains([]string{"chat", "responses", "anthropic", "gemini"}, model.Protocol) {
		return nil
	}
	reasoning := model.Capabilities.Reasoning || len(subscriptionWorkerReasoningEfforts(model)) > 0
	if model.Protocol == "gemini" {
		_, encoded := geminiThinkingConfig(model.ID, "high")
		reasoning = reasoning && encoded
	}
	parameters := subscriptionCodecParameters(model.Protocol, model.Capabilities.ToolCall, reasoning)
	var represented []string
	// These pinned adapters translate SDK-shaped JSON to private protocols.
	// Preserve their actual field sets, not just the Go codec's input shape.
	switch a.Definition.ID {
	case "kiro":
		represented = []string{"tools", "reasoning_effort"}
	case "cursor":
		represented = []string{"tools", "reasoning_effort", "service_tier"}
	case "qoder", "qoder-cn", "qwenwork", "qwenwork-enterprise":
		represented = []string{"max_tokens", "max_completion_tokens", "tools", "reasoning_effort"}
	case "devin":
		represented = []string{"max_tokens", "max_completion_tokens", "temperature", "top_p", "tools", "reasoning_effort"}
	case "trae-cn", "trae-global":
		represented = []string{"max_tokens", "max_completion_tokens", "temperature", "tools", "tool_choice", "parallel_tool_calls", "reasoning_effort"}
	case "commandcode-plan":
		// Go-plan accounts use /alpha/generate, whose controls are a subset
		// of the other plans' Chat endpoint; the catalog does not bind a plan.
		represented = []string{"max_tokens", "max_completion_tokens", "temperature", "tools", "reasoning_effort"}
	case "autoclaw", "autoclaw-cn":
		parameters = slices.DeleteFunc(parameters, func(field string) bool { return field == "max_tokens" || field == "max_completion_tokens" })
	case "factory":
		// Factory strips sampling for its OpenAI reasoning models. Its xAI
		// Responses models retain these controls on the same wire protocol.
		if model.Protocol == "responses" && strings.HasPrefix(firstNonEmpty(model.API.ID, model.ID), "gpt-") {
			parameters = slices.DeleteFunc(parameters, func(field string) bool { return field == "temperature" || field == "top_p" })
		}
	}
	if represented != nil {
		parameters = slices.DeleteFunc(parameters, func(field string) bool { return !slices.Contains(represented, field) })
	}
	return parameters
}
