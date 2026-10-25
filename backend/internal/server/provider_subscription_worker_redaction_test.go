package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestSubscriptionWorkerRedactsEmbeddedStreamErrors(t *testing.T) {
	for _, protocol := range []string{"chat", "anthropic", "responses", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			pool := newSubscriptionWorkerPool(&http.Client{})
			defer pool.Close()
			pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
				return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
					if command["operation"] == "models" {
						return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"test-model","protocol":"` + protocol + `"}]}`)}}
					}
					data := "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\",\"code\":401,\"message\":\"Bearer synthetic-secret nested-secret\"}}\n\n"
					return []subscriptionWorkerEvent{{Event: "response", Status: 200, Headers: http.Header{"Content-Type": []string{"text/event-stream"}}}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(data))}, {Event: "end"}}
				}), nil
			}
			a := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, Pool: pool, RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
				return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-secret","refresh":"{\"token\":\"nested-secret\"}"}`}, nil
			}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
			provider := Provider{Type: a.Definition.providerType(), Options: map[string]string{"resource_id": "account"}}
			var output bytes.Buffer
			_, err := a.ChatStream(context.Background(), provider, "test-model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}, &output)
			if err == nil {
				t.Fatal("Expected stream error")
			}
			for _, secret := range []string{"synthetic-secret", "nested-secret"} {
				if strings.Contains(err.Error(), secret) || strings.Contains(output.String(), secret) {
					t.Fatalf("Credential leaked through %s stream error", protocol)
				}
			}
		})
	}
}

func TestSubscriptionWorkerRedactsCollectedErrors(t *testing.T) {
	pool := newSubscriptionWorkerPool(&http.Client{})
	defer pool.Close()
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if command["operation"] == "models" {
				return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"test-model","protocol":"chat"}]}`)}}
			}
			data := `{"error":{"message":"Bearer synthetic-secret"}}`
			return []subscriptionWorkerEvent{{Event: "response", Status: 200, Headers: http.Header{"Content-Type": []string{"application/json"}}}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(data))}, {Event: "end"}}
		}), nil
	}
	a := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, Pool: pool, RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-secret"}`}, nil
	}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
	provider := Provider{Type: a.Definition.providerType(), Options: map[string]string{"resource_id": "account"}}
	output, _, err := a.Chat(context.Background(), provider, "test-model", ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}})
	if err == nil {
		t.Fatal("Embedded JSON error was accepted as a successful completion")
	}
	data, _ := json.Marshal(output)
	if bytes.Contains(data, []byte("synthetic-secret")) || err != nil && strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("Collected response exposed the account credential")
	}
}

func TestSubscriptionWorkerStreamTracksRotationsAndRequestHeaders(t *testing.T) {
	var process *subscriptionWorkerProcess
	call := 0
	process = fakeSubscriptionProcess(`{"type":"oauth","access":"saved-secret"}`, func(command map[string]any) []subscriptionWorkerEvent {
		if command["operation"] != "fetch" {
			return nil
		}
		call++
		id := command["id"].(string)
		process.network(subscriptionWorkerEvent{ID: id, RequestID: "upstream-" + strconv.Itoa(call), URL: "https://api.cursor.com/chat", Method: "POST", Headers: http.Header{
			"Authorization": []string{"Bearer signed-secret-" + strconv.Itoa(call)}, "Ptkey": []string{"vendor-secret"}, "Cookie": []string{"session=cookie-secret"},
		}})
		process.updateAuth(subscriptionWorkerEvent{ID: id, UpdateID: "rotation", Auth: json.RawMessage(`{"type":"oauth","access":"rotated-secret"}`)})
		data := "data: {\"choices\":[{\"delta\":{\"content\":\"saved-secret rotated-secret signed-secret-1 vendor-secret cookie-secret\"}}]}\n\n" +
			"event: error\ndata: {\"error\":{\"message\":\"saved-secret rotated-secret signed-secret-1 signed-secret-2 vendor-secret cookie-secret\"}}\n\n"
		events := []subscriptionWorkerEvent{{Event: "response", Status: 200}}
		for len(data) > 0 {
			n := min(len(data), 7)
			events = append(events, subscriptionWorkerEvent{Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(data[:n]))})
			data = data[n:]
		}
		return append(events, subscriptionWorkerEvent{Event: "end"})
	})
	defer process.cancel()
	process.client = &http.Client{Transport: subscriptionWorkerRoundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	transport := subscriptionWorkerTransport{adapter: &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}, process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}, stream: true}
	for attempt := 1; attempt <= 2; attempt++ {
		request, _ := http.NewRequest(http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(string(body), "event: error")
		if len(parts) != 2 || !strings.Contains(parts[0], "saved-secret rotated-secret signed-secret-1 vendor-secret cookie-secret") {
			t.Fatal("Normal model content was changed")
		}
		for _, secret := range []string{"rotated-secret", "signed-secret-" + strconv.Itoa(attempt), "vendor-secret", "cookie-secret"} {
			if strings.Contains(parts[1], secret) {
				t.Fatalf("Invocation %d leaked a current credential", attempt)
			}
		}
		if attempt == 1 && strings.Contains(parts[1], "saved-secret") {
			t.Fatal("Rotation discarded the previous credential too soon")
		}
		if attempt == 2 && !strings.Contains(parts[1], "signed-secret-1") {
			t.Fatal("Redaction context survived into another invocation")
		}
	}
}

func TestSubscriptionWorkerRejectsMalformedSecretFrames(t *testing.T) {
	process := fakeSubscriptionProcess(`{"type":"oauth","access":"synthetic-secret"}`, func(map[string]any) []subscriptionWorkerEvent {
		return []subscriptionWorkerEvent{{Event: "response", Status: 200}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte("event: error\ndata: {\"error\":\"synthetic-secret"))}, {Event: "end"}}
	})
	defer process.cancel()
	transport := subscriptionWorkerTransport{adapter: &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}, process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}, stream: true}
	request, _ := http.NewRequest(http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err == nil || bytes.Contains(data, []byte("synthetic-secret")) || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("Malformed terminal frame was not safely rejected")
	}
}

func TestSubscriptionWorkerPreservesFailedResponsesUsage(t *testing.T) {
	pool := newSubscriptionWorkerPool(&http.Client{})
	defer pool.Close()
	pool.start = func(_ *http.Client, auth string, _ func(context.Context, string, string) error) (*subscriptionWorkerProcess, error) {
		return fakeSubscriptionProcess(auth, func(command map[string]any) []subscriptionWorkerEvent {
			if command["operation"] == "models" {
				return []subscriptionWorkerEvent{{Event: "result", Result: json.RawMessage(`{"models":[{"id":"test-model","protocol":"responses"}]}`)}}
			}
			data := "event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"synthetic-secret\"},\"usage\":{\"input_tokens\":9,\"output_tokens\":3,\"total_tokens\":12}}}\n\n"
			return []subscriptionWorkerEvent{{Event: "response", Status: 200}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(data))}, {Event: "end"}}
		}), nil
	}
	a := &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}, Pool: pool, RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
		return ProviderResourceCredentials{SessionJSON: `{"type":"oauth","access":"synthetic-secret"}`}, nil
	}, SaveSession: func(context.Context, string, string, string, string) error { return nil }}
	provider := Provider{Type: a.Definition.providerType(), Options: map[string]string{"resource_id": "account"}}
	request := ChatCompletionRequest{Model: "public", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
	for _, stream := range []bool{false, true} {
		var usage Usage
		var err error
		if stream {
			usage, err = a.ChatStream(context.Background(), provider, "test-model", request, io.Discard)
		} else {
			_, usage, err = a.Chat(context.Background(), provider, "test-model", request)
		}
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") || usage.PromptTokens != 9 || usage.CompletionTokens != 3 {
			t.Fatalf("Failed Responses stream=%v did not retain safe usage: %+v, %v", stream, usage, err)
		}
	}
}

func TestSubscriptionWorkerPreservesSuccessfulJSONBytes(t *testing.T) {
	data := " {\n  \"choices\": [{\"message\": {\"content\": \"synthetic-secret\"}}], \"usage\": {\"prompt_tokens\": 2}\n}\n"
	process := fakeSubscriptionProcess(`{"type":"oauth","access":"synthetic-secret"}`, func(map[string]any) []subscriptionWorkerEvent {
		return []subscriptionWorkerEvent{{Event: "response", Status: 200}, {Event: "chunk", Data: base64.StdEncoding.EncodeToString([]byte(data))}, {Event: "end"}}
	})
	defer process.cancel()
	transport := subscriptionWorkerTransport{adapter: &SubscriptionWorkerAdapter{Definition: subscriptionWorkerDefinition{"cursor", "Cursor"}}, process: process, resourceID: "account", model: subscriptionWorkerModel{ID: "model", Protocol: "chat"}}
	request, _ := http.NewRequest(http.MethodPost, "https://subscription.tokenhub.invalid", strings.NewReader(`{}`))
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	output, err := io.ReadAll(response.Body)
	if err != nil || string(output) != data {
		t.Fatalf("Successful JSON changed: %q, %v", output, err)
	}
}

func TestSubscriptionWorkerDistinguishesProtocolVersionFromShortCredentials(t *testing.T) {
	secrets := &subscriptionWorkerSecrets{}
	secrets.addHeaders(http.Header{"Connect-Protocol-Version": {"1"}})
	if values := providerErrorSecretValues(secrets.provider()); len(values) != 0 {
		t.Fatalf("fixed protocol version was treated as a credential: %v", values)
	}
	secrets.addHeaders(http.Header{"X-Synthetic-Token": {"1"}})
	redacted := redactProviderErrorSecrets([]byte(`{"message":"token 1"}`), secrets.provider())
	if strings.Contains(string(redacted), "1") {
		t.Fatal("short credentials were excluded from redaction")
	}
}
