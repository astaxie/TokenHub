package server

import "strings"

func gatewayRequestProtocol(path string) string {
	switch {
	case strings.Contains(path, "/messages"):
		return providerRouteProtocolAnthropic
	case strings.Contains(path, "/responses"):
		return providerRouteProtocolResponses
	case strings.Contains(path, "/images/"):
		return providerRouteProtocolImageGeneration
	case strings.Contains(path, "/embeddings"):
		return providerRouteProtocolEmbeddings
	case strings.Contains(path, "/v1beta/"):
		return providerRouteProtocolGemini
	default:
		return providerRouteProtocolChatCompletions
	}
}
