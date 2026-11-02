package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestResponsesRequestCompressionSwitch(t *testing.T) {
	payload := []byte(strings.Repeat("screenshot ", 4000))
	if _, encoding := encodeResponsesUpstreamBody(nil, "chatgpt.com", payload); encoding != "" {
		t.Fatal("chatgpt.com compressed without responses_request_compression=on")
	}
	if _, encoding := encodeResponsesUpstreamBody(nil, "127.0.0.1", payload); encoding != "" {
		t.Fatal("loopback compressed without the option")
	}
	off := map[string]string{responsesRequestCompressionOption: "off"}
	if _, encoding := encodeResponsesUpstreamBody(off, "chatgpt.com", payload); encoding != "" {
		t.Fatal("responses_request_compression=off still compressed")
	}
	on := map[string]string{responsesRequestCompressionOption: "on"}
	encoded, encoding := encodeResponsesUpstreamBody(on, "api.openai.com", payload)
	if encoding != "zstd" || len(encoded) >= len(payload) {
		t.Fatalf("explicit compression = %s bytes %d of %d", encoding, len(encoded), len(payload))
	}
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()
	decoded, err := decoder.DecodeAll(encoded, nil)
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("decoded %d bytes, err=%v", len(decoded), err)
	}
	if responsesCompressionRejected(http.StatusUnsupportedMediaType, nil) != true {
		t.Fatal("415 was not treated as an encoding refusal")
	}
	if responsesCompressionRejected(http.StatusBadRequest, []byte(`{"error":"model not found"}`)) {
		t.Fatal("an ordinary 400 was treated as an encoding refusal")
	}
	provider := Provider{Options: map[string]string{responsesRequestCompressionOption: "on"}}
	stampResponsesRequestCompression(&provider, &ProviderResource{Options: map[string]string{responsesRequestCompressionOption: "off"}})
	if provider.Options[responsesRequestCompressionOption] != "off" {
		t.Fatalf("resource compression option = %s", provider.Options[responsesRequestCompressionOption])
	}
}

func TestCodexResponsesCompressesAndRetriesUncompressed(t *testing.T) {
	var calls int
	var sawEncoding bool
	adapter := CodexSubscriptionAdapter{
		Client: &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			if request.Header.Get("Content-Encoding") == "zstd" {
				sawEncoding = true
				data, err := io.ReadAll(request.Body)
				if err != nil {
					t.Fatal(err)
				}
				decoder, err := zstd.NewReader(nil)
				if err != nil {
					t.Fatal(err)
				}
				defer decoder.Close()
				if _, err := decoder.DecodeAll(data, nil); err != nil {
					t.Fatal(err)
				}
				return &http.Response{
					StatusCode: http.StatusUnsupportedMediaType,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"unsupported content-encoding"}}`)),
					Request:    request,
				}, nil
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["store"] != false {
				t.Fatalf("store = %#v", body["store"])
			}
			if _, ok := body["previous_response_id"]; ok {
				t.Fatal("previous_response_id was forwarded")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		})},
		RefreshCredentials: func(context.Context, string, bool) (ProviderResourceCredentials, error) {
			return ProviderResourceCredentials{AccessToken: "access", AccountID: "account"}, nil
		},
	}
	provider := Provider{
		Type:    ProviderOpenAICodex,
		BaseURL: "http://localhost/backend-api/codex",
		Options: map[string]string{"resource_id": "rsrc_compress", responsesRequestCompressionOption: "on"},
	}
	var request ResponsesRequest
	if err := json.Unmarshal([]byte(`{"model":"gpt-test","store":true,"previous_response_id":"resp_old","input":"`+strings.Repeat("screenshot ", 4000)+`"}`), &request); err != nil {
		t.Fatal(err)
	}
	original, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := adapter.OpenResponses(context.Background(), provider, "gpt-test", request, nil)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	after, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !sawEncoding || string(after) != string(original) {
		t.Fatalf("calls=%d compressed=%v caller mutated=%v", calls, sawEncoding, string(after) != string(original))
	}
}

func TestChatGPTSubscriptionCompressesWhenEnabled(t *testing.T) {
	var calls int
	a, provider := chatGPTSubscriptionFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Content-Encoding") == "zstd" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"unsupported content-encoding"}}`)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["store"] != false || body["stream"] != true {
			t.Errorf("envelope = %#v", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-zstd\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	})
	provider.Options[responsesRequestCompressionOption] = "on"
	_, _, err := a.Responses(context.Background(), provider, "gpt-model", ResponsesRequest{Input: strings.Repeat("screenshot ", 4000)})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("upstream calls = %d", calls)
	}
}

type closeAwareBody struct {
	closed chan struct{}
	once   sync.Once
}

func (b *closeAwareBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.EOF
}

func (b *closeAwareBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestSubscriptionPostDoesNotWaitOnSilentCompressionError(t *testing.T) {
	body := &closeAwareBody{closed: make(chan struct{})}
	calls := 0
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusBadRequest, Body: body, Header: make(http.Header), Request: request}, nil
		}
		t.Errorf("ordinary 400 was retried")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: request}, nil
	})}
	request, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader("compressed"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err = subscriptionPost(context.Background(), client, request, func() (*http.Request, error) {
		return http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader("raw"))
	}, "zstd", 40*time.Millisecond)
	elapsed := time.Since(started)
	if err == nil {
		t.Fatal("silent 400 succeeded")
	}
	if calls != 1 {
		t.Fatalf("upstream calls = %d", calls)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("silent error body blocked for %s", elapsed)
	}
}

func TestSubscriptionPostRetries415WithoutReadingBody(t *testing.T) {
	body := &closeAwareBody{closed: make(chan struct{})}
	calls := 0
	client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Header.Get("Content-Encoding") == "zstd" {
			return &http.Response{StatusCode: http.StatusUnsupportedMediaType, Body: body, Header: make(http.Header), Request: request}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header), Request: request}, nil
	})}
	request, err := http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader("compressed"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Encoding", "zstd")
	started := time.Now()
	response, err := subscriptionPost(context.Background(), client, request, func() (*http.Request, error) {
		return http.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader("raw"))
	}, "zstd", time.Minute)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if calls != 2 {
		t.Fatalf("upstream calls = %d", calls)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("415 body was read for %s", elapsed)
	}
}
