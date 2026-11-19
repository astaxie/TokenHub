package server

import (
	"fmt"
	"net/http"
)

// Carbon and Argon require continuation after 32K on non-streaming calls.
// The adapter makes one GenerateContent request and does not follow continuations.
func gemini4OutputLimit(modelID string, stream bool) int64 {
	switch modelID {
	case "gemini-4-carbon", "gemini-4-argon":
		if !stream {
			return 32768
		}
	case "gemini-4-hydrogen", "gemini-4-neon":
	default:
		return 0
	}
	return 65536
}

func validateGeminiCatalogContract(modelID string, req ChatCompletionRequest, outputLimit int64) error {
	if outputLimit == 0 {
		return nil
	}
	if req.requestedMaxTokens() > outputLimit {
		return NewHTTPError(http.StatusBadRequest, "model_output_limit_exceeded",
			fmt.Sprintf("%s supports at most %d output tokens per request through this adapter", modelID, outputLimit))
	}
	if effort := normalizedReasoningEffort(req.ReasoningEffort); effort != nil {
		switch *effort {
		case "low", "medium", "high":
			return nil
		case "xhigh":
			if geminiSupportsExtraHighThinking(modelID) {
				return nil
			}
		}
		levels := "low, medium, or high"
		if geminiSupportsExtraHighThinking(modelID) {
			levels = "low, medium, high, or xhigh"
		}
		return NewHTTPError(http.StatusBadRequest, "unsupported_reasoning_effort",
			fmt.Sprintf("%s supports %s thinking levels", modelID, levels))
	}
	return nil
}

func geminiSupportsExtraHighThinking(modelID string) bool {
	return modelID == "gemini-4-hydrogen" || modelID == "gemini-4-neon"
}
