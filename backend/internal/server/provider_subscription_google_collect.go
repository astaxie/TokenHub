package server

import "io"

// Code Assist only streams inference responses. A non-streaming client still
// uses that upstream protocol and receives a bounded, assembled completion.
func collectGoogleSubscriptionChat(reader io.Reader, model string) (any, Usage, error) {
	events := newSSEDecoder(reader)
	parts := []any{}
	usage := map[string]any{}
	measuredUsage := func() Usage { return geminiUsage(map[string]any{"usageMetadata": usage}) }
	finish := ""
	total := 0
	for {
		event, err := events.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, measuredUsage(), err
		}
		total += len(event.Data)
		if total > 32<<20 {
			return nil, measuredUsage(), invalidProviderResponseError("Google subscription completion exceeds the response limit")
		}
		payload, err := decodeSSEData(event)
		if err != nil {
			return nil, measuredUsage(), err
		}
		if payload == nil {
			continue
		}
		body, err := googleSubscriptionUnwrap(payload)
		if err != nil {
			return nil, measuredUsage(), err
		}
		if snapshot, ok := body["usageMetadata"].(map[string]any); ok {
			for key, value := range snapshot {
				if value != nil {
					usage[key] = value
				}
			}
		}
		if reason := geminiPromptBlockReason(body); reason != "" {
			return nil, measuredUsage(), newProviderPolicyRefusal("Google subscription blocked the prompt")
		}
		candidates, _ := body["candidates"].([]any)
		if len(candidates) == 0 {
			continue
		}
		candidate, _ := candidates[0].(map[string]any)
		content, _ := candidate["content"].(map[string]any)
		next, _ := content["parts"].([]any)
		parts = append(parts, next...)
		if reason, ok := candidate["finishReason"].(string); ok && reason != "" {
			finish = reason
		}
	}
	body := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": parts}, "finishReason": finish}}, "usageMetadata": usage}
	measured := geminiUsage(body)
	if finish == "" {
		return nil, measured, invalidProviderResponseError("Google subscription stream ended before reporting a finish reason")
	}
	result, err := geminiChatResponse(body, model, measured)
	return result, measured, err
}
