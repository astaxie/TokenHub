package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

type subscriptionWorkerSecretsKey struct{}

// Each invocation retains its own credential history until the response has
// been consumed. Vendor fetch hooks can rotate credentials or mint request
// signatures, so the saved account alone is not the full redaction context.
type subscriptionWorkerSecrets struct {
	mu     sync.Mutex
	values map[string]struct{}
}

func (s *subscriptionWorkerSecrets) add(values ...string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = make(map[string]struct{})
	}
	for _, value := range values {
		if value != "" {
			s.values[value] = struct{}{}
		}
	}
}

func (s *subscriptionWorkerSecrets) addAuth(auth string) {
	if s == nil || len(auth) > 512<<10 {
		return
	}
	var walk func(any, int)
	walk = func(value any, depth int) {
		if depth > 42 {
			return
		}
		switch node := value.(type) {
		case map[string]any:
			for key, item := range node {
				if depth != 0 || key != "type" {
					walk(item, depth+1)
				}
			}
		case []any:
			for _, item := range node {
				walk(item, depth+1)
			}
		case string:
			s.add(node)
			// Several vendors store a structured refresh session as JSON text.
			var nested any
			if json.Unmarshal([]byte(node), &nested) == nil {
				walk(nested, depth+1)
			}
		}
	}
	var value any
	if json.Unmarshal([]byte(auth), &value) == nil {
		walk(value, 0)
	}
}

func (s *subscriptionWorkerSecrets) addHeaders(headers http.Header) {
	if s == nil {
		return
	}
	for name, values := range headers {
		switch strings.ToLower(name) {
		case "accept", "accept-encoding", "accept-language", "cache-control", "content-length", "content-type", "pragma", "user-agent", "host", "connection", "anthropic-version", "anthropic-beta", "connect-protocol-version":
			continue
		}
		// Proprietary credentials include ptKey and X-Authorization. Treat
		// every non-transport header as sensitive instead of guessing names.
		for _, value := range values {
			s.add(value)
			if scheme, token, ok := strings.Cut(value, " "); ok && (strings.EqualFold(scheme, "Bearer") || strings.EqualFold(scheme, "Basic")) {
				s.add(strings.TrimSpace(token))
			}
			if strings.EqualFold(name, "Cookie") {
				for _, cookie := range (&http.Request{Header: http.Header{"Cookie": []string{value}}}).Cookies() {
					s.add(cookie.Value)
				}
			}
		}
	}
}

func (s *subscriptionWorkerSecrets) provider() Provider {
	provider := Provider{Headers: make(map[string]string)}
	s.mu.Lock()
	defer s.mu.Unlock()
	for value := range s.values {
		name := "TokenHub-Subscription-Secret-" + strconv.Itoa(len(provider.Headers))
		provider.Headers[name] = value
		provider.SensitiveHeaders = append(provider.SensitiveHeaders, name)
	}
	return provider
}

func (p *subscriptionWorkerProcess) invocationSecrets(id string) *subscriptionWorkerSecrets {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.secrets[id]
}

func subscriptionWorkerSecretsFromContext(ctx context.Context) *subscriptionWorkerSecrets {
	secrets, _ := ctx.Value(subscriptionWorkerSecretsKey{}).(*subscriptionWorkerSecrets)
	return secrets
}

// JSON responses have to be complete before distinguishing model output from
// an embedded error. Match the worker relay's total response limit.
type subscriptionWorkerJSONBody struct {
	body    io.ReadCloser
	secrets *subscriptionWorkerSecrets
	reader  *bytes.Reader
}

func (r *subscriptionWorkerJSONBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.reader == nil {
		const limit = 64 << 20
		data, err := io.ReadAll(io.LimitReader(r.body, limit+1))
		if err != nil {
			return 0, err
		}
		if len(data) > limit || len(bytes.TrimSpace(data)) > 0 && !json.Valid(data) {
			return 0, invalidProviderResponseError("Subscription returned an invalid JSON response")
		}
		if _, err := subscriptionJSONError(data, r.secrets.provider()); err != nil {
			return 0, err
		}
		r.reader = bytes.NewReader(data)
	}
	return r.reader.Read(p)
}

func (r *subscriptionWorkerJSONBody) Close() error { return r.body.Close() }
