package server

import (
	"bytes"
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
	raw, framed := subscriptionStreamEventFraming(event.Raw)
	if !framed {
		return nil, true
	}
	event.Raw = raw
	payload := strings.TrimSpace(event.Data)
	malformed := payload != "" && payload != "[DONE]" && !json.Valid([]byte(payload))
	if providerStreamEventIsError(event) || malformed {
		return redactProviderStreamEventSecrets(event, r.provider()), malformed
	}
	return event.Raw, false
}

// The SSE decoder ignores unknown fields. A bare JSON or HTML failure body
// therefore has no data payload, but its raw bytes must not become public output.
// Ignore unknown fields as required by SSE, preserving standard fields and
// keepalives byte for byte even when an unframed diagnostic follows them.
func subscriptionStreamEventFraming(raw []byte) ([]byte, bool) {
	var output []byte
	framed := len(bytes.TrimSpace(raw)) == 0
	for line := range bytes.Lines(raw) {
		trimmed := strings.TrimRight(string(line), "\r\n")
		if trimmed == "" || strings.HasPrefix(trimmed, ":") {
			output = append(output, line...)
			framed = framed || trimmed != ""
			continue
		}
		name, _ := splitSSEField(trimmed)
		switch name {
		case "data", "event", "id", "retry":
			output = append(output, line...)
			framed = true
		}
	}
	return output, framed
}

func (r *subscriptionErrorStream) Close() error { return r.body.Close() }
