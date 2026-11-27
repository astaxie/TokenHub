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
		return geminiRouteIncompatible("model_output_limit_exceeded",
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
		return geminiRouteIncompatible("unsupported_reasoning_effort",
			fmt.Sprintf("%s supports %s thinking levels", modelID, levels))
	}
	return nil
}

func geminiSupportsExtraHighThinking(modelID string) bool {
	return modelID == "gemini-4-hydrogen" || modelID == "gemini-4-neon"
}

// Rejections from local model checks do not indicate an upstream failure.
func geminiRouteIncompatible(code, message string) error {
	return &ProviderInvocationError{
		Err:         NewHTTPError(http.StatusBadRequest, code, message),
		Disposition: ProviderErrorRouteSkipped,
	}
}

// Fast admission avoids acquiring provider capacity for a request the native
// adapter will reject. A matching plugin can change or handle that request.
func (s *Server) geminiChatRouteCompatibility(call CallContext, route RouteSelection, req ChatCompletionRequest) error {
	modelID := geminiModelID(route.ProviderModel)
	if gemini4OutputLimit(modelID, true) == 0 {
		return nil
	}
	adapter, err := s.adapterForRoute(route)
	if err != nil {
		return nil
	}
	stream := req.Stream
	switch adapter.(type) {
	case GeminiAdapter, *GeminiAdapter:
	case *GoogleSubscriptionAdapter:
		// The subscription transport always uses streamGenerateContent.
		stream = true
	default:
		return nil
	}
	const protocol = providerRouteProtocolChatCompletions
	if len(s.gatewayRequestTransformHooksForRoute(route, protocol)) > 0 ||
		len(s.gatewayProviderCallHooksForRoute(call, route, protocol, req.Stream)) > 0 {
		return nil
	}
	return validateGeminiCatalogContract(modelID, req, gemini4OutputLimit(modelID, stream))
}
