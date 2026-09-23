package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type semanticTestTransport func(*http.Request) (*http.Response, error)

func (f semanticTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func semanticTestResponse() map[string]any {
	return map[string]any{"model": "jev-1.13.0", "answers": map[string]any{"selected_candidate": map[string]any{"type": "choice", "choice": "candidate_2", "confidence": 0.8, "probabilities": map[string]any{"candidate_1": 0.1, "candidate_2": 0.85, "no_preference": 0.05}}}, "usage": map[string]any{"input_tokens": 23, "output_tokens": 2}}
}
func semanticTestCandidates() []semanticCandidate {
	return []semanticCandidate{{ID: "candidate_1", Model: "small"}, {ID: "candidate_2", Model: "reasoning"}}
}

func TestJevRoutingClientWireContract(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("incorrect request contract")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["model"] != "jev-1.13.0" {
			t.Errorf("unexpected model: %v", body["model"])
		}
		question := body["questions"].(map[string]any)["selected_candidate"].(map[string]any)
		if question["type"] != "choice" || len(question["criteria"].(map[string]any)) != 3 {
			t.Error("missing bounded choices")
		}
		_ = json.NewEncoder(w).Encode(semanticTestResponse())
	}))
	defer upstream.Close()
	client := newJevRoutingClient(Config{TypeSafeAPIKey: "test-secret"})
	client.client.Transport = semanticTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != jevRoutingEndpoint {
			t.Errorf("unexpected destination %s", r.URL)
		}
		clone := r.Clone(r.Context())
		clone.URL.Scheme = "http"
		clone.URL.Host = strings.TrimPrefix(upstream.URL, "http://")
		return http.DefaultTransport.RoundTrip(clone)
	})
	decision, err := client.Evaluate(context.Background(), "Write a synthetic test", semanticTestCandidates(), "")
	if err != nil || decision.Choice != "candidate_2" || decision.InputTokens != 23 || decision.OutputTokens != 2 {
		t.Fatalf("unexpected decision: %+v %v", decision, err)
	}
}

func TestJevRoutingClientRejectsInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
		status int
		raw    string
	}{
		{name: "unknown choice", mutate: func(r map[string]any) {
			r["answers"].(map[string]any)["selected_candidate"].(map[string]any)["choice"] = "injected_provider"
		}},
		{name: "missing confidence", mutate: func(r map[string]any) {
			delete(r["answers"].(map[string]any)["selected_candidate"].(map[string]any), "confidence")
		}},
		{name: "out of range confidence", mutate: func(r map[string]any) {
			r["answers"].(map[string]any)["selected_candidate"].(map[string]any)["confidence"] = 1.1
		}},
		{name: "missing probability", mutate: func(r map[string]any) {
			delete(r["answers"].(map[string]any)["selected_candidate"].(map[string]any)["probabilities"].(map[string]any), "no_preference")
		}},
		{name: "invalid probability sum", mutate: func(r map[string]any) {
			r["answers"].(map[string]any)["selected_candidate"].(map[string]any)["probabilities"].(map[string]any)["candidate_1"] = 0.9
		}},
		{name: "wrong model", mutate: func(r map[string]any) { r["model"] = "unexpected" }},
		{name: "negative usage", mutate: func(r map[string]any) { r["usage"].(map[string]any)["input_tokens"] = -1 }},
		{name: "rate limit", status: 429}, {name: "overload", status: 529}, {name: "invalid JSON", raw: "not JSON"}, {name: "oversized body", raw: strings.Repeat("x", 65537)},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := semanticTestResponse()
			if test.mutate != nil {
				test.mutate(response)
			}
			body, _ := json.Marshal(response)
			if test.raw != "" {
				body = []byte(test.raw)
			}
			status := 200
			if test.status != 0 {
				status = test.status
			}
			calls := 0
			client := newJevRoutingClient(Config{})
			client.client.Transport = semanticTestTransport(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})
			_, err := client.Evaluate(context.Background(), "test", semanticTestCandidates(), "")
			if err == nil || calls != 1 {
				t.Fatalf("expected single failed attempt, calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestJevRoutingClientTimeoutCancellationConcurrencyAndRedirect(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelled_%v", cancelled), func(t *testing.T) {
			client := newJevRoutingClient(Config{SemanticRoutingTimeoutMS: 20})
			client.client.Transport = semanticTestTransport(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				cancel()
			}
			started := time.Now()
			_, err := client.Evaluate(ctx, "test", semanticTestCandidates(), "")
			if err == nil || time.Since(started) > time.Second {
				t.Fatalf("unbounded request: %v", err)
			}
		})
	}
	client := newJevRoutingClient(Config{})
	for range cap(client.slots) {
		client.slots <- struct{}{}
	}
	_, err := client.Evaluate(context.Background(), "test", semanticTestCandidates(), "")
	if err == nil || err.Error() != "busy" {
		t.Fatalf("expected concurrency fallback: %v", err)
	}
	var calls atomic.Int32
	client = newJevRoutingClient(Config{})
	client.client.Transport = semanticTestTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"https://untrusted.example.test/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	_, err = client.Evaluate(context.Background(), "test", semanticTestCandidates(), "")
	if err == nil || calls.Load() != 1 {
		t.Fatalf("redirect followed: calls=%d err=%v", calls.Load(), err)
	}
}
