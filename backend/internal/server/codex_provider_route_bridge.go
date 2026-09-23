package server

func init() {
	registerProviderRouteBridge(providerRouteBridge{
		Protocol:             providerRouteProtocolCodexResponses,
		ChatCompatible:       validateCodexChatBridge,
		ExecuteChat:          (*Server).executeCodexChatRoute,
		StreamChat:           (*Server).streamCodexAsChat,
		ValidateAnthropic:    validateCodexAnthropicBridge,
		ExecuteAnthropic:     (*Server).executeCodexAnthropicMessages,
		StreamAnthropic:      (*Server).streamCodexAsAnthropic,
		GeminiHeaders:        geminiCodexCompatibilityHeaders,
		WriteResponseHeaders: writeCodexResponseHeaders,
	})
}

func validateCodexChatBridge(req ChatCompletionRequest) error {
	_, err := chatToCodexResponsesRequest(req)
	return err
}

func validateCodexAnthropicBridge(req anthropicMessagesRequest) error {
	_, err := anthropicToCodexResponsesRequest(req)
	return err
}
