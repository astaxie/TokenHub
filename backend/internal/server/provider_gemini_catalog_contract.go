package server

import "net/http"

// The existing adapter handles one synchronous GenerateContent response.
// Argon's larger long-decode limit requires a separate continuation workflow.
func validateGeminiCatalogContract(model string, req ChatCompletionRequest) error {
	if geminiModelID(model) != "gemini-4-argon" {
		return nil
	}
	if req.requestedMaxTokens() > 65536 {
		return NewHTTPError(http.StatusBadRequest, "model_output_limit_exceeded", "Gemini 4 Argon supports at most 65536 output tokens through this synchronous adapter; long decode requires an unsupported continuation workflow")
	}
	if effort := normalizedReasoningEffort(req.ReasoningEffort); effort != nil {
		switch *effort {
		case "low", "medium", "high":
		default:
			return NewHTTPError(http.StatusBadRequest, "unsupported_reasoning_effort", "Gemini 4 Argon supports low, medium, or high thinking levels")
		}
	}
	return nil
}
