package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionProviderRejectionsDoNotPersistEchoedAudio(t *testing.T) {
	const privateAudio = "private-transcription-audio-sentinel"
	encodedAudio := base64.StdEncoding.EncodeToString([]byte(privateAudio))
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, status := range []int{http.StatusBadRequest, http.StatusTooManyRequests} {
			t.Run(path+"/"+http.StatusText(status), func(t *testing.T) {
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					w.Header().Set("Retry-After", "4")
					writeJSON(w, status, map[string]any{"error": map[string]any{"message": privateAudio + " " + encodedAudio}})
				}, "audio")
				store.AddRoute(ModelRoute{ID: "error-fallback-transcription", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "fallback-model", Priority: 2, Weight: 100, Status: StatusActive})
				request := transcriptionFixtureRequest(t, key, [][2]string{{"model", "public-media"}}, []mediaFile{transcriptionFixtureFile("file", "sample.wav", "audio/wav", transcriptionWAVFixture(privateAudio))})
				request.URL.Path = path
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				wantCalls := 1
				if status == http.StatusTooManyRequests {
					wantCalls = 2
					if got := response.Header().Get("Retry-After"); got != "4" {
						t.Errorf("retry delay changed: %q", got)
					}
				}
				if response.Code != status || calls != wantCalls {
					t.Fatalf("provider failure behavior changed: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				var attempts []RouteAttemptLog
				if err := store.db.Find(&attempts).Error; err != nil {
					t.Fatal(err)
				}
				if len(attempts) != wantCalls {
					t.Fatalf("attempts=%d, want %d", len(attempts), wantCalls)
				}
				for _, attempt := range attempts {
					if attempt.UpstreamStatus != status || attempt.StatusCode != status {
						t.Errorf("attempt status changed: %+v", attempt)
					}
				}
				var payloads []RequestPayloadLog
				if err := store.db.Find(&payloads).Error; err != nil {
					t.Fatal(err)
				}
				stored, err := json.Marshal(map[string]any{"attempts": attempts, "payloads": payloads})
				if err != nil {
					t.Fatal(err)
				}
				for _, sensitive := range []string{privateAudio, encodedAudio} {
					if strings.Contains(response.Body.String()+string(stored), sensitive) {
						t.Errorf("provider-echoed audio escaped redaction: %s", sensitive)
					}
				}
			})
		}
	}
}

func TestTranscriptionErrorSanitizationPreservesRoutingMetadata(t *testing.T) {
	original := &ProviderInvocationError{Err: &HTTPError{Status: http.StatusTooManyRequests, Code: "provider_rate_limited", Message: "private audio", UpstreamStatus: http.StatusTooManyRequests, Headers: map[string]string{"x-ratelimit-remaining-requests": "0"}}, Disposition: ProviderErrorQuotaExhausted, RetryAfter: 7 * time.Second}
	sanitized := sanitizeTranscriptionProviderError(original)
	httpError := AsHTTPError(sanitized)
	if httpError.Status != http.StatusTooManyRequests || httpError.Code != "provider_rate_limited" || httpError.UpstreamStatus != http.StatusTooManyRequests || httpError.Headers["x-ratelimit-remaining-requests"] != "0" || providerErrorDisposition(sanitized) != ProviderErrorQuotaExhausted || providerErrorRetryAfter(sanitized) != 7*time.Second || strings.Contains(sanitized.Error(), "private audio") {
		t.Fatalf("sanitization changed routing metadata or retained audio: %+v %v", httpError, sanitized)
	}
}

func TestTranscriptionNetworkErrorsPreserveCancellationAndTimeout(t *testing.T) {
	if err := sanitizeTranscriptionProviderError(nil); err != nil {
		t.Fatalf("nil error became %v", err)
	}
	const privateURL = "https://private-provider.example/audio?credential=private"
	canceled := sanitizeTranscriptionProviderError(&url.Error{Op: "Post", URL: privateURL, Err: context.Canceled})
	if !errors.Is(canceled, context.Canceled) || providerErrorDisposition(canceled) != ProviderErrorClient || strings.Contains(canceled.Error(), privateURL) {
		t.Fatalf("cancellation lost classification or exposed provider URL: %v", canceled)
	}
	deadline := &url.Error{Op: "Post", URL: privateURL, Err: context.DeadlineExceeded}
	for _, original := range []error{deadline, &ProviderInvocationError{Err: deadline, Disposition: ProviderErrorOutcomeUnknown}} {
		sanitized := sanitizeTranscriptionProviderError(original)
		if AsHTTPError(sanitized).Status != http.StatusGatewayTimeout || providerErrorDisposition(sanitized) != ProviderErrorOutcomeUnknown || shouldFailoverRoutedError(sanitized, false) || strings.Contains(sanitized.Error(), privateURL) {
			t.Fatalf("timeout lost classification, became retryable, or exposed provider URL: %v", sanitized)
		}
	}
}

func TestTranscriptionProviderHookErrorDoesNotExposeAudio(t *testing.T) {
	const privateAudio = "private-hook-audio-sentinel"
	server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("failed provider hook reached upstream") }, "audio")
	registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "private-error", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"audio/transcriptions"}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		return pluginmeta.GatewayHookResult{}, NewHTTPError(http.StatusBadRequest, "transcription_rejected", privateAudio)
	})
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
	if response.Code < http.StatusBadRequest {
		t.Fatalf("failed hook accepted: status=%d body=%s", response.Code, response.Body)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	var payloads []RequestPayloadLog
	if err := store.db.Find(&payloads).Error; err != nil {
		t.Fatal(err)
	}
	stored, err := json.Marshal(map[string]any{"attempts": attempts, "payloads": payloads})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String()+string(stored), privateAudio) {
		t.Fatalf("provider hook error retained audio: response=%s stored=%s", response.Body, stored)
	}
}

type transcriptionStalledBody struct {
	*bytes.Reader
	closed chan struct{}
	once   sync.Once
}

func (b *transcriptionStalledBody) Read(data []byte) (int, error) {
	if b.Len() > 0 {
		return b.Reader.Read(data)
	}
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *transcriptionStalledBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

func TestTranscriptionResponseBodyIdleTimeoutIsStructuredAndNotRetried(t *testing.T) {
	server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("fake transport reached upstream") }, "audio")
	calls := 0
	adapter := OpenAICompatibleAdapter{StreamIdleTimeout: 20 * time.Millisecond, Client: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: &transcriptionStalledBody{Reader: bytes.NewReader([]byte(`{"text":"fixture","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}`)), closed: make(chan struct{})}}, nil
	})}}
	server.adapterRegistry.Register(ProviderOpenAICompatible, adapter, AdapterCapabilityTranscription)
	store.AddRoute(ModelRoute{ID: "idle-fallback-transcription", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "fallback-model", Priority: 2, Weight: 100, Status: StatusActive})
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
	if response.Code != http.StatusGatewayTimeout || calls != 1 || !strings.Contains(response.Body.String(), "provider_stream_idle_timeout") {
		t.Fatalf("idle timeout was changed or retried: status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
	var attempts []RouteAttemptLog
	if err := store.db.Find(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].StatusCode != http.StatusGatewayTimeout || attempts[0].ErrorCode != "provider_stream_idle_timeout" || attempts[0].TotalTokens != 5 {
		t.Fatalf("idle timeout lost status or observed usage: %+v", attempts)
	}
	_, _, err := adapter.Media(context.Background(), Provider{BaseURL: "http://127.0.0.1:1/v1"}, "fixture-model", "/audio/transcriptions", mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"fixture-model"`)}})
	if AsHTTPError(err).Status != http.StatusGatewayTimeout || providerErrorDisposition(err) != ProviderErrorOutcomeUnknown || shouldFailoverRoutedError(err, false) {
		t.Fatalf("idle timeout changed its no-retry disposition: %v", err)
	}
}
