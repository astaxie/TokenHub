package server

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type claudeSubscriptionTool struct {
	Name         string         `json:"name"`
	Description  string         `json:"description,omitempty"`
	InputSchema  map[string]any `json:"inputSchema"`
	OriginalName string         `json:"-"`
}

func claudeSubscriptionTools(request ChatCompletionRequest) ([]claudeSubscriptionTool, error) {
	if request.Temperature != nil || request.TopP != nil || request.Stop != nil || request.ResponseFormat != nil {
		return nil, unsupportedCapabilityError("Claude Code subscriptions do not support temperature, top_p, stop, or response_format controls")
	}
	choice, err := parseChatToolChoice(request.ToolChoice)
	if err != nil {
		return nil, err
	}
	if choice.Mode != "" && choice.Mode != "auto" && choice.Mode != "none" {
		return nil, unsupportedCapabilityError("Claude Code subscriptions support auto or none tool choice")
	}
	parsed, err := parseChatTools(request.Tools)
	if err != nil {
		return nil, err
	}
	if len(parsed) > 64 {
		return nil, unsupportedCapabilityError("Claude Code subscriptions accept at most 64 tools")
	}
	tools := make([]claudeSubscriptionTool, 0, len(parsed))
	if choice.Mode == "none" {
		return tools, nil
	}
	if len(parsed) > 0 && request.ParallelToolCalls != nil && !*request.ParallelToolCalls {
		return nil, unsupportedCapabilityError("Claude Code subscriptions do not support parallel_tool_calls=false with enabled tools")
	}
	for index, tool := range parsed {
		tools = append(tools, claudeSubscriptionTool{Name: fmt.Sprintf("tool_%d", index), Description: tool.Description, InputSchema: tool.Parameters, OriginalName: tool.Name})
	}
	return tools, nil
}

func claudeSubscriptionPrompt(messages []ChatMessage) (string, []map[string]any, error) {
	system, history, err := anthropicMessagesFromChat(messages)
	if err != nil {
		return "", nil, err
	}
	var instructions strings.Builder
	for _, block := range system {
		if value, ok := block["text"].(string); ok {
			instructions.WriteString(value)
			instructions.WriteString("\n")
		}
	}
	var content []map[string]any
	for _, message := range history {
		content = append(content, map[string]any{"type": "text", "text": "[" + message["role"].(string) + "]"})
		blocks, _ := message["content"].([]map[string]any)
		for _, block := range blocks {
			if block["type"] == "text" || block["type"] == "image" {
				content = append(content, block)
				continue
			}
			encoded, _ := json.Marshal(block)
			content = append(content, map[string]any{"type": "text", "text": string(encoded)})
		}
	}
	if len(content) == 0 {
		return "", nil, unsupportedContentError("Claude Code subscriptions require conversation messages")
	}
	return instructions.String(), content, nil
}

func claudeSubscriptionResults(messages []ChatMessage) []ChatMessage {
	start := len(messages)
	for start > 0 && messages[start-1].Role == "tool" {
		start--
	}
	return messages[start:]
}

func (a *ClaudeSubscriptionAdapter) resume(scope string, tools [32]byte, request ChatCompletionRequest, results []ChatMessage) (*claudeSubscriptionRun, error) {
	invalid := NewHTTPError(400, "claude_subscription_continuation_invalid", "Claude tool continuation is expired or does not match this account, caller, or conversation")
	a.mu.Lock()
	run := a.calls[results[0].ToolCallID]
	a.mu.Unlock()
	if run == nil {
		return nil, invalid
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.scope != scope || run.toolHash != tools || run.busy || run.closed {
		return nil, invalid
	}
	prefix, err := json.Marshal(request.Messages[:len(request.Messages)-len(results)])
	if err != nil {
		return nil, invalid
	}
	var candidate []ChatMessage
	if json.Unmarshal(prefix, &candidate) != nil || len(candidate) != len(run.history)+1 {
		return nil, invalid
	}
	// A continuation may only append the assistant tool-call message and its
	// results. Never allow a different tenant's or conversation's history to reuse
	// a process merely because a client supplied a tool id.
	old, _ := json.Marshal(run.history)
	current, _ := json.Marshal(candidate[:len(run.history)])
	var oldValue, currentValue any
	if json.Unmarshal(old, &oldValue) != nil || json.Unmarshal(current, &currentValue) != nil || !reflect.DeepEqual(oldValue, currentValue) {
		return nil, invalid
	}
	supplied, err := parseChatToolCalls(candidate[len(candidate)-1].ToolCalls)
	if err != nil || len(supplied) != len(run.waiting) || len(results) != len(run.waiting) {
		return nil, invalid
	}
	seen := map[string]bool{}
	for _, call := range supplied {
		pending := run.waiting[call.ID]
		if pending == nil || call.Name != pending.tool.OriginalName || !reflect.DeepEqual(call.Arguments, pending.arguments) {
			return nil, invalid
		}
	}
	for _, result := range results {
		if run.waiting[result.ToolCallID] == nil || seen[result.ToolCallID] {
			return nil, invalid
		}
		seen[result.ToolCallID] = true
	}
	run.busy = true
	run.history = append([]ChatMessage(nil), request.Messages...)
	if run.timer != nil {
		run.timer.Stop()
		run.timer = nil
	}
	a.mu.Lock()
	for _, result := range results {
		delete(a.calls, result.ToolCallID)
	}
	a.mu.Unlock()
	for _, result := range results {
		text, ok := result.Content.(string)
		if !ok {
			encoded, _ := json.Marshal(result.Content)
			text = string(encoded)
		}
		run.waiting[result.ToolCallID].result <- text
	}
	run.waiting = make(map[string]*claudeSubscriptionPending)
	return run, nil
}
