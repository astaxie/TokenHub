package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const fixtureProviderKey = "synthetic-provider-credential"
const failureEnvelope = `{"code":400,"message":"invalid apikey: synthetic-provider-credential","success":false}`

func TestGatewayRejectsUnframedProviderStreams(t *testing.T) {
	for _, protocol := range []struct{ path, provider string }{
		{"/v1/chat/completions", ProviderOpenAICompatible},
		{"/v1/responses", ProviderOpenAICompatible},
		{"/v1/messages", ProviderAnthropic},
	} {
		for _, mediaType := range []string{"text/event-stream", "application/json"} {
			for _, ending := range []string{"", "\n", "\n\n"} {
				t.Run(fmt.Sprintf("%s/%s/%d", protocol.path, mediaType, len(ending)), func(t *testing.T) {
					var calls atomic.Int32
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("Content-Type", mediaType)
						if calls.Add(1) == 1 {
							_, _ = io.WriteString(w, "\r\n"+failureEnvelope+ending)
							return
						}
						switch protocol.path {
						case "/v1/messages":
							_, _ = io.WriteString(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
						case "/v1/responses":
							_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"output\":[]}}\n\n")
						default:
							_, _ = io.WriteString(w, "data: [DONE]\n\n")
						}
					}))
					defer upstream.Close()
					server, store, key := classificationGateway(t, upstream.URL, upstream.URL, protocol.provider)
					defer func() { _ = server.Shutdown(context.Background()) }()
					for _, resource := range store.ListProviderResources() {
						resource.APIKey = fixtureProviderKey
						if _, err := store.UpdateProviderResource(resource.ID, resource); err != nil {
							t.Fatal(err)
						}
					}
					payload := map[string]any{"model": "classified-model", "stream": true}
					if protocol.path == "/v1/responses" {
						payload["input"] = "hello"
					} else {
						payload["messages"] = []map[string]string{{"role": "user", "content": "hello"}}
						payload["max_tokens"] = 16
					}
					request := func() responseBody { return doJSON(t, server.Handler(), http.MethodPost, protocol.path, payload, key) }
					failure := request()
					if failure.Code != http.StatusBadGateway || strings.Contains(failure.Body, fixtureProviderKey) || strings.Contains(failure.Body, "invalid apikey") {
						t.Fatalf("unexpected public error: %d %s", failure.Code, failure.Body)
					}
					var attempts []RouteAttemptLog
					if err := store.db.Find(&attempts).Error; err != nil {
						t.Fatal(err)
					}
					if len(attempts) != 1 || attempts[0].UpstreamStatus != http.StatusOK || strings.Contains(attempts[0].ErrorMessage, fixtureProviderKey) || !strings.Contains(attempts[0].ErrorMessage, providerHeaderMask) {
						t.Fatalf("unexpected attempt diagnostics: %+v", attempts)
					}
					if next := request(); next.Code != http.StatusOK || next.Body == "" {
						t.Fatalf("next request failed: %+v", next)
					}
					var last RouteAttemptLog
					if err := store.db.Order("created_at DESC").First(&last).Error; err != nil || last.ProviderID != attempts[0].ProviderID {
						t.Fatalf("request did not recover on the same provider: %v, %+v", err, last)
					}
				})
			}
		}
	}
}

func TestProviderStreamFramingAndUsage(t *testing.T) {
	const frame = "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\r\n\r\n"
	const metadata = ": heartbeat\r\nx-trace: " + fixtureProviderKey + "\r\n"
	const done = "data: [DONE]\r\n\r\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "\ufeff\r\n"+metadata+frame+failureEnvelope+"\n"+done)
	}))
	defer upstream.Close()
	adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
	var output strings.Builder
	usage, err := adapter.ChatStream(context.Background(), Provider{BaseURL: upstream.URL, APIKey: fixtureProviderKey}, "model", ChatCompletionRequest{}, &output)
	if err != nil || usage.TotalTokens != 5 || output.String() != strings.ReplaceAll(metadata, fixtureProviderKey, providerHeaderMask)+frame+done {
		t.Fatalf("unexpected stream: %q, usage=%+v, error=%v", output.String(), usage, err)
	}
}

func TestProviderResultFieldTakesPrecedence(t *testing.T) {
	for _, test := range []struct {
		body     string
		accepted bool
	}{
		{failureEnvelope, false},
		{`{"error":{"message":"insufficient balance"}}`, false},
		{`{"code":400,"message":"insufficient balance"}`, false},
		{`{"success":false}`, false},
		{`{"choices":[],"error":""}`, true},
		{`{"choices":[],"error":{}}`, true},
		{`{"choices":[],"success":false}`, true},
		{`{"message":"invalid apikey","code":"401","error":null}`, true},
	} {
		t.Run(test.body, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, test.body) }))
			defer upstream.Close()
			adapter := OpenAICompatibleAdapter{Client: upstream.Client()}
			result, _, err := adapter.Chat(context.Background(), Provider{BaseURL: upstream.URL, APIKey: fixtureProviderKey}, "model", ChatCompletionRequest{})
			if test.accepted {
				var want map[string]any
				if decodeErr := json.Unmarshal([]byte(test.body), &want); decodeErr != nil {
					t.Fatal(decodeErr)
				}
				if err != nil || !reflect.DeepEqual(result, want) {
					t.Fatalf("changed successful result: %+v, %v", result, err)
				}
			} else if err == nil || err.Error() != unexpectedProviderMessage || providerAttemptOutcome(err) != AttemptNeutral || shouldFailoverRoutedError(err, false) {
				t.Fatalf("unexpected error disposition: %v", err)
			}
		})
	}
}
