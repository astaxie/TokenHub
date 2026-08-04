package server

import (
	"encoding/json"
	"math"
	"strings"
)

const defaultOutputTokenReservation int64 = 4096

func requestTokenReservation(payload any) int64 {
	switch request := payload.(type) {
	case ChatCompletionRequest:
		input := saturatingAddNonNegative(estimateChatMessagesTokens(request.Messages), estimateJSONTokens(request.Tools))
		input = saturatingAddNonNegative(input, estimateJSONTokens(request.ToolChoice))
		input = saturatingAddNonNegative(input, estimateJSONTokens(request.ResponseFormat))
		return saturatingAddNonNegative(input, outputTokenReservation(chatMaximumOutputTokens(request)))
	case ResponsesRequest:
		input := EstimateTextTokens(strings.TrimSpace(request.Instructions + "\n" + ResponsesInputText(request.Input)))
		input = saturatingAddNonNegative(input, estimateRawJSONTokens(request.raw["tools"]))
		input = saturatingAddNonNegative(input, estimateRawJSONTokens(request.raw["text"]))
		return saturatingAddNonNegative(input, outputTokenReservation(int64(request.MaxTokens)))
	case EmbeddingsRequest:
		return EstimateTextTokens(EmbeddingInputText(request.Input))
	case map[string]json.RawMessage:
		return compactResponsesTokenReservation(request)
	default:
		return estimateJSONTokens(payload)
	}
}

func compactResponsesTokenReservation(request map[string]json.RawMessage) int64 {
	var maxOutput int64
	if raw := request["max_output_tokens"]; len(raw) > 0 {
		_ = json.Unmarshal(raw, &maxOutput)
	}
	input := saturatingAddNonNegative(estimateRawJSONTokens(request["input"]), estimateRawJSONTokens(request["tools"]))
	input = saturatingAddNonNegative(input, estimateRawJSONTokens(request["text"]))
	if instructions := request["instructions"]; len(instructions) > 0 {
		input = saturatingAddNonNegative(input, estimateJSONTokens(instructions))
	}
	return saturatingAddNonNegative(input, outputTokenReservation(maxOutput))
}

func estimateChatMessagesTokens(messages []ChatMessage) int64 {
	var total int64
	for _, message := range messages {
		total = saturatingAddNonNegative(total, estimateJSONTokens(message))
	}
	return total
}

func chatMaximumOutputTokens(request ChatCompletionRequest) int64 {
	maximum := int64(request.MaxTokens)
	if raw := request.raw["max_completion_tokens"]; len(raw) > 0 {
		var compatibleMaximum int64
		if json.Unmarshal(raw, &compatibleMaximum) == nil && compatibleMaximum > maximum {
			maximum = compatibleMaximum
		}
	}
	return maximum
}

func anthropicTokenReservation(request anthropicMessagesRequest) int64 {
	return saturatingAddNonNegative(estimateAnthropicInputTokens(request.Raw), outputTokenReservation(int64(request.MaxTokens)))
}

func outputTokenReservation(requested int64) int64 {
	if requested > 0 {
		return requested
	}
	return defaultOutputTokenReservation
}

func estimateJSONTokens(value any) int64 {
	if value == nil {
		return 0
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return EstimateTextTokens(string(encoded))
}

func estimateRawJSONTokens(value json.RawMessage) int64 {
	if len(value) == 0 {
		return 0
	}
	return estimateJSONTokens(value)
}

// meteredTokens treats the Provider's total as authoritative. When an adapter
// cannot provide it, prompt and completion totals are the next-best portable
// definition: cached input is already part of prompt tokens and reasoning is
// already part of completion tokens for OpenAI-compatible usage payloads.
func meteredTokens(usage Usage) int64 {
	if usage.TotalTokens > 0 {
		return usage.TotalTokens
	}
	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
		return saturatingAddNonNegative(usage.PromptTokens, usage.CompletionTokens)
	}
	total := saturatingAddNonNegative(usage.CachedInputTokens, usage.CacheWriteInputTokens)
	total = saturatingAddNonNegative(total, usage.InputAudioTokens)
	total = saturatingAddNonNegative(total, usage.ReasoningOutputTokens)
	total = saturatingAddNonNegative(total, usage.OutputAudioTokens)
	total = saturatingAddNonNegative(total, usage.AcceptedPredictionTokens)
	return saturatingAddNonNegative(total, usage.RejectedPredictionTokens)
}

func saturatingAddNonNegative(a int64, b int64) int64 {
	a = maxInt64(a, 0)
	b = maxInt64(b, 0)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}
