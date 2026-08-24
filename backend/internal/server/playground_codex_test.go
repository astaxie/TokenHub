package server

import "testing"

func TestPlaygroundResponsesRequestForRouteSkipsNonCodexProviders(t *testing.T) {
	request, useResponses, err := playgroundResponsesRequestForRoute(
		RouteSelection{Provider: Provider{Type: ProviderOpenAI}},
		ChatCompletionRequest{Messages: []ChatMessage{{Role: "tool", Content: "not valid for Codex"}}},
	)
	if err != nil || useResponses || request.Input != nil {
		t.Fatalf("non-Codex route unexpectedly converted request: request=%+v use_responses=%t err=%v", request, useResponses, err)
	}
}

func TestPlaygroundChatResponsesRequestRejectsUnsupportedInstructionContent(t *testing.T) {
	_, err := playgroundChatResponsesRequest(ChatCompletionRequest{Messages: []ChatMessage{{
		Role: "system", Content: []any{playgroundImagePart("data:image/png;base64,YWJj")},
	}}})
	if got := AsHTTPError(err).Code; got != "unsupported_content_block" {
		t.Fatalf("error code = %q, want unsupported_content_block: %v", got, err)
	}
}

func TestPlaygroundChatResponsesRequestRejectsUnsupportedRole(t *testing.T) {
	_, err := playgroundChatResponsesRequest(ChatCompletionRequest{Messages: []ChatMessage{{Role: "tool", Content: "result"}}})
	if got := AsHTTPError(err).Code; got != "invalid_message" {
		t.Fatalf("error code = %q, want invalid_message: %v", got, err)
	}
}
