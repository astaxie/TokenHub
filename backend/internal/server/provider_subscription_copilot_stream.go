package server

import (
	"encoding/json"
	"io"
	"strings"
)

// The session bearer can differ from the saved GitHub token and can rotate on
// retry. Redact at the response boundary so every downstream protocol decoder,
// including public Responses passthrough, sees only sanitized error frames.
type copilotSubscriptionStream struct {
	body     io.ReadCloser
	events   *sseDecoder
	provider Provider
	pending  []byte
	err      error
}

func newCopilotSubscriptionStream(body io.ReadCloser, provider Provider, accountToken, sessionToken string, eventLimit int) io.ReadCloser {
	provider.Headers = cloneStringMap(provider.Headers)
	if provider.Headers == nil {
		provider.Headers = make(map[string]string)
	}
	// These fields exist only in the private redaction context, never in an
	// outgoing request or the returned response headers.
	provider.Headers["TokenHub-Copilot-Account"] = accountToken
	provider.Headers["TokenHub-Copilot-Session"] = sessionToken
	provider.SensitiveHeaders = append(append([]string{}, provider.SensitiveHeaders...), "TokenHub-Copilot-Account", "TokenHub-Copilot-Session")
	events := newSSEDecoder(body)
	events.assembler.limit = eventLimit
	return &copilotSubscriptionStream{body: body, events: events, provider: provider}
}

func (r *copilotSubscriptionStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.err != nil {
			return 0, r.err
		}
		event, err := r.events.Next()
		if err != nil {
			r.err = err
			if pending := r.events.PendingEvent(); len(pending.Raw) > 0 {
				r.pending = redactProviderStreamEventSecrets(pending, r.provider)
			}
			continue
		}
		r.pending = event.Raw
		payload := strings.TrimSpace(event.Data)
		malformed := payload != "" && payload != "[DONE]" && !json.Valid([]byte(payload))
		if providerStreamEventIsError(event) || malformed {
			r.pending = redactProviderStreamEventSecrets(event, r.provider)
		}
		if malformed {
			r.err = invalidProviderResponseError("Copilot returned a malformed stream event")
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *copilotSubscriptionStream) Close() error { return r.body.Close() }
