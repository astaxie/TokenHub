package server

import (
	"encoding/json"
	"io"
	"strings"
)

// Read each frame before releasing it so malformed/error payloads never expose
// credentials. The callback includes credentials exchanged during this request;
// ordinary model output and the original SSE framing remain unchanged.
type subscriptionErrorStream struct {
	body             io.ReadCloser
	events           *sseDecoder
	provider         func() Provider
	malformedMessage string
	pending          []byte
	err              error
}

func newSubscriptionErrorStream(body io.ReadCloser, provider func() Provider, eventLimit int) *subscriptionErrorStream {
	events := newSSEDecoder(body)
	events.assembler.limit = eventLimit
	return &subscriptionErrorStream{body: body, events: events, provider: provider, malformedMessage: "Subscription provider returned a malformed stream event"}
}

func (r *subscriptionErrorStream) Read(p []byte) (int, error) {
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
				r.pending, _ = r.sanitizeEvent(pending)
			}
			continue
		}
		output, malformed := r.sanitizeEvent(event)
		r.pending = output
		if malformed {
			r.err = invalidProviderResponseError(r.malformedMessage)
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}

func (r *subscriptionErrorStream) sanitizeEvent(event serverSentEvent) ([]byte, bool) {
	payload := strings.TrimSpace(event.Data)
	malformed := payload != "" && payload != "[DONE]" && !json.Valid([]byte(payload))
	if providerStreamEventIsError(event) || malformed {
		return redactProviderStreamEventSecrets(event, r.provider()), malformed
	}
	return event.Raw, false
}

func (r *subscriptionErrorStream) Close() error { return r.body.Close() }
