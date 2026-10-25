package server

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSubscriptionErrorStreamPreservesValidPendingOutput(t *testing.T) {
	const frame = "data: {\"choices\":[{\"delta\":{\"content\":\"session-secret\"}}]}\n"
	failure := errors.New("upstream connection interrupted")
	for _, copilot := range []bool{false, true} {
		body := io.NopCloser(&failingReader{data: []byte(frame), err: failure})
		var reader io.ReadCloser = newSubscriptionErrorStream(body, func() Provider { return Provider{APIKey: "session-secret"} }, 0)
		if copilot {
			reader = newCopilotSubscriptionStream(body, Provider{}, "account-secret", "session-secret", 0)
		}
		data, err := io.ReadAll(reader)
		if !errors.Is(err, failure) || string(data) != frame {
			t.Fatalf("ordinary pending output changed (copilot=%t): %q, %v", copilot, data, err)
		}
	}
}

func TestSubscriptionErrorStreamUsesCurrentRequestSecretsAndClosesBody(t *testing.T) {
	const ordinary = "data: {\"choices\":[{\"delta\":{\"content\":\"original-secret\"}}]}\n\n"
	const failure = "data: {\"error\":{\"message\":\"rotated-secret\"}}\n\n"
	secret := "original-secret"
	body := &subscriptionTrackedReadCloser{Reader: strings.NewReader(ordinary + failure)}
	reader := newSubscriptionErrorStream(body, func() Provider { return Provider{APIKey: secret} }, 0)
	first := make([]byte, len(ordinary))
	if _, err := io.ReadFull(reader, first); err != nil || string(first) != ordinary {
		t.Fatalf("ordinary model output changed: %q, %v", first, err)
	}
	secret = "rotated-secret"
	rest, err := io.ReadAll(reader)
	if err != nil || bytes.Contains(rest, []byte(secret)) {
		t.Fatalf("rotated credential was exposed: %q, %v", rest, err)
	}
	if err := reader.Close(); err != nil || !body.closed {
		t.Fatalf("stream did not close the underlying body: %v", err)
	}
}

type subscriptionTrackedReadCloser struct {
	io.Reader
	closed bool
}

func (r *subscriptionTrackedReadCloser) Close() error {
	r.closed = true
	return nil
}
