package server

import (
	"fmt"
	"net/http"
	"strings"
)

// playgroundResponsesRequestForRoute converts only Codex route attempts, leaving
// other providers on their native Chat request path.
func playgroundResponsesRequestForRoute(route RouteSelection, req ChatCompletionRequest) (ResponsesRequest, bool, error) {
	if route.Provider.Type != ProviderOpenAICodex {
		return ResponsesRequest{}, false, nil
	}
	converted, err := playgroundChatResponsesRequest(req)
	if err != nil {
		return ResponsesRequest{}, false, err
	}
	return converted, true, nil
}

// playgroundChatResponsesRequest preserves the Playground's dedicated instructions
// and reasoning behavior while sharing Codex content-block conversion with /v1/chat.
func playgroundChatResponsesRequest(req ChatCompletionRequest) (ResponsesRequest, error) {
	instructions := make([]string, 0, len(req.Messages))
	input := make([]map[string]any, 0, len(req.Messages))
	for _, message := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		switch role {
		case "system", "developer":
			text, err := playgroundCodexInstructionText(message.Content)
			if err != nil {
				return ResponsesRequest{}, err
			}
			if strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
		case "assistant", "user":
			parts, err := playgroundCodexMessageContent(message.Content, role == "assistant")
			if err != nil {
				return ResponsesRequest{}, err
			}
			input = append(input, map[string]any{"role": role, "content": parts})
		default:
			return ResponsesRequest{}, NewHTTPError(http.StatusBadRequest, "invalid_message", fmt.Sprintf("unsupported Playground message role %q", message.Role))
		}
	}
	responsesReq := ResponsesRequest{
		Model:        req.Model,
		Input:        input,
		Instructions: strings.Join(instructions, "\n\n"),
	}
	if effort := normalizedReasoningEffort(req.ReasoningEffort); effort != nil {
		responsesReq.Reasoning = &ResponsesReasoning{Effort: effort}
	}
	return responsesReq, nil
}

func playgroundCodexMessageContent(content any, assistant bool) ([]any, error) {
	parts, err := chatContentToCodex(content, assistant)
	if err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		partType := "input_text"
		if assistant {
			partType = "output_text"
		}
		parts = []any{map[string]any{"type": partType, "text": ""}}
	}
	return parts, nil
}

func playgroundCodexInstructionText(content any) (string, error) {
	parts, err := chatContentToCodex(content, false)
	if err != nil {
		return "", err
	}
	texts := make([]string, 0, len(parts))
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok || part["type"] != "input_text" {
			return "", NewHTTPError(http.StatusBadRequest, "unsupported_content_block", "Playground instructions support text content only")
		}
		text, ok := part["text"].(string)
		if !ok {
			return "", NewHTTPError(http.StatusBadRequest, "invalid_content_block", "instruction text must be a string")
		}
		texts = append(texts, text)
	}
	return strings.Join(texts, "\n"), nil
}
