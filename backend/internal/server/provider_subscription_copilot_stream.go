package server

import "io"

// The session bearer can differ from the saved GitHub token and can rotate on
// retry. Redact at the response boundary so every downstream protocol decoder,
// including public Responses passthrough, sees only sanitized error frames.
func newCopilotSubscriptionStream(body io.ReadCloser, provider Provider, accountToken, sessionToken string, eventLimit int) io.ReadCloser {
	provider = copilotSubscriptionRedactionProvider(provider, accountToken, sessionToken)
	stream := newSubscriptionErrorStream(body, func() Provider { return provider }, eventLimit)
	stream.malformedMessage = "Copilot returned a malformed stream event"
	return stream
}

func copilotSubscriptionRedactionProvider(provider Provider, accountToken, sessionToken string) Provider {
	provider.Headers = cloneStringMap(provider.Headers)
	if provider.Headers == nil {
		provider.Headers = make(map[string]string)
	}
	// These fields exist only in the private redaction context, never in an
	// outgoing request or the returned response headers.
	provider.Headers["TokenHub-Copilot-Account"] = accountToken
	provider.Headers["TokenHub-Copilot-Session"] = sessionToken
	provider.SensitiveHeaders = append(append([]string{}, provider.SensitiveHeaders...), "TokenHub-Copilot-Account", "TokenHub-Copilot-Session")
	return provider
}
