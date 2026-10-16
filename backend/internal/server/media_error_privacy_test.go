package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pluginmeta "tokenhub/backend/internal/plugin"
)

func TestTranscriptionHookErrorsKeepPayloadsOutOfEveryErrorSink(t *testing.T) {
	for _, path := range []string{"/v1/audio/transcriptions", "/v1/audio/translations"} {
		for _, tc := range []struct {
			stage    pluginmeta.GatewayHookStage
			upstream bool
			stream   bool
		}{
			{stage: pluginmeta.StageAuthContext},
			{stage: pluginmeta.StageDecodeNormalize},
			{stage: pluginmeta.StageAdmission},
			{stage: pluginmeta.StagePrivacyPre},
			{stage: pluginmeta.StageGuardrailPre},
			{stage: pluginmeta.StageContextOptimize},
			{stage: pluginmeta.StageRouteCandidates},
			{stage: pluginmeta.StageRequestTransform},
			{stage: pluginmeta.StageProviderCall},
			{stage: pluginmeta.StageResponsePost, upstream: true},
			{stage: pluginmeta.StageGuardrailPost, upstream: true},
			{stage: pluginmeta.StageUsageAttribution, upstream: true},
			{stage: pluginmeta.StageStreamTransform, upstream: true, stream: true},
			{stage: pluginmeta.StageResponsePost, upstream: true, stream: true},
			{stage: pluginmeta.StageGuardrailPost, upstream: true, stream: true},
			{stage: pluginmeta.StageUsageAttribution, upstream: true, stream: true},
		} {
			name := path + "/" + string(tc.stage)
			if tc.stream {
				name += "/stream"
			}
			t.Run(name, func(t *testing.T) {
				const sensitive = "private-transcription-hook-payload"
				calls := 0
				server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
					calls++
					if tc.stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"text\":\""+sensitive+"\"}\n\n")
						return
					}
					writeJSON(w, http.StatusOK, map[string]string{"text": sensitive})
				}, "audio")
				traces := &recordingTraceEmitter{}
				server.traceEmitter = traces
				server.config.TracingCapturePayloads = true
				hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.transcription-privacy", HookID: "reject", Stage: tc.stage, Priority: 2000, FailurePolicy: pluginmeta.FailurePolicyFailClosed, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{strings.TrimPrefix(path, "/v1/")}}}
				if err := server.gatewayChain.RegisterHook(hook); err != nil {
					t.Fatal(err)
				}
				if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
					return pluginmeta.GatewayHookResult{}, &HTTPError{
						Status: http.StatusUnprocessableEntity, Code: sensitive, Message: sensitive,
						Details: map[string]string{"transcript": sensitive},
						Headers: map[string]string{"x-provider-diagnostic": sensitive, "x-ratelimit-remaining-requests": sensitive, "x-ratelimit-limit-requests": "7"},
					}
				})); err != nil {
					t.Fatal(err)
				}
				fields := [][2]string{{"model", "public-media"}, {"prompt", sensitive}}
				if tc.stream {
					fields = append(fields, [2]string{"stream", "true"})
				}
				request := transcriptionFixtureRequest(t, key, fields, []mediaFile{transcriptionFixtureFile("file", "fixture.wav", "audio/wav", transcriptionWAVFixture(sensitive))})
				request.URL.Path = path
				response := httptest.NewRecorder()
				server.Handler().ServeHTTP(response, request)
				wantCalls := 0
				if tc.upstream {
					wantCalls = 1
				}
				if response.Code != http.StatusUnprocessableEntity || calls != wantCalls {
					t.Fatalf("hook failure changed execution: status=%d calls=%d body=%s", response.Code, calls, response.Body)
				}
				if response.Header().Get("x-ratelimit-limit-requests") != "7" || response.Header().Get("x-provider-diagnostic") != "" || response.Header().Get("x-ratelimit-remaining-requests") != "" {
					t.Fatalf("unsafe or missing error headers: %v", response.Header())
				}
				assertTranscriptionFailurePrivate(t, store, response, traces.take(), sensitive)
			})
		}
	}
}

func assertTranscriptionFailurePrivate(t *testing.T, store *GormStore, response *httptest.ResponseRecorder, traces []GatewayCallCompletion, sensitive string) {
	t.Helper()
	var attempts []RouteAttemptLog
	var payloads []RequestPayloadLog
	var audits []AuditEvent
	for _, target := range []any{&attempts, &payloads, &audits} {
		if err := store.db.Find(target).Error; err != nil {
			t.Fatal(err)
		}
	}
	if len(payloads) != 1 || len(traces) != 1 {
		t.Fatalf("missing completion evidence: payloads=%d traces=%d", len(payloads), len(traces))
	}
	encoded, err := json.Marshal(map[string]any{"response": response.Body.String(), "headers": response.Header(), "attempts": attempts, "payloads": payloads, "audits": audits, "requests": store.ListRequestLogs(), "traces": traces})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), sensitive) {
		t.Fatal("transcription payload escaped into an error response, stored log, audit, or trace")
	}
}

func TestTranscriptionRouteRankErrorDoesNotLogPayload(t *testing.T) {
	const sensitive = "private-transcription-routing-payload"
	server, store, key := newMediaGatewayFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"text": "fixture"})
	}, "audio")
	store.AddRoute(ModelRoute{ID: "transcription-alternate", ModelName: "public-media", ProviderID: "media-provider", ProviderModel: "vendor-alternate", Status: StatusActive, Priority: 2, Weight: 100})
	hook := pluginmeta.GatewayHookDescriptor{PluginID: "test.transcription-privacy", HookID: "rank", Stage: pluginmeta.StageRouteRank, Priority: 2000, FailurePolicy: pluginmeta.FailurePolicyFailOpen, Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"audio/transcriptions"}}}
	if err := server.gatewayChain.RegisterHook(hook); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := server.gatewayHooks.RegisterHandler(hook, pluginmeta.GatewayHookHandlerFunc(func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
		calls++
		return pluginmeta.GatewayHookResult{}, NewHTTPError(http.StatusBadRequest, "route_rank_failed", sensitive)
	})); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	original := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(original) })
	response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
	if response.Code != http.StatusOK || calls != 1 || strings.Contains(logs.String(), sensitive) {
		t.Fatalf("routing failure leaked or changed delivery: status=%d logs=%s", response.Code, &logs)
	}
}

func TestTranscriptionProviderHookPreservesFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		status      int
		disposition ProviderErrorDisposition
		retryAfter  time.Duration
	}{
		{name: "timeout", err: &ProviderInvocationError{Err: context.DeadlineExceeded, Disposition: ProviderErrorOutcomeUnknown}, status: http.StatusGatewayTimeout, disposition: ProviderErrorOutcomeUnknown},
		{name: "canceled", err: &ProviderInvocationError{Err: context.Canceled, Disposition: ProviderErrorClient}, disposition: ProviderErrorClient},
		{name: "rate limited", err: &ProviderInvocationError{Err: &HTTPError{Status: http.StatusTooManyRequests, Code: "provider_rate_limited", Message: "private hook failure", UpstreamStatus: http.StatusTooManyRequests}, Disposition: ProviderErrorQuotaExhausted, RetryAfter: 7 * time.Second}, status: http.StatusTooManyRequests, disposition: ProviderErrorQuotaExhausted, retryAfter: 7 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, store, key := newMediaGatewayFixture(t, func(http.ResponseWriter, *http.Request) { t.Error("failed hook reached upstream") }, "audio")
			calls := 0
			registerMediaProviderTestHook(t, server, pluginmeta.GatewayHookDescriptor{HookID: "classified-error", Scope: pluginmeta.GatewayHookScope{RouteProtocols: []string{"audio/transcriptions"}}, FailurePolicy: pluginmeta.FailurePolicyFailClosed}, func(context.Context, pluginmeta.GatewayHookInput) (pluginmeta.GatewayHookResult, error) {
				calls++
				return pluginmeta.GatewayHookResult{}, tc.err
			})
			provider, _ := store.GetProvider("media-provider")
			call := CallContext{Model: Model{Name: "public-media", Modality: "audio"}, RouteProtocol: "audio/transcriptions"}
			_, _, err := server.invokeMediaRoute(context.Background(), call, RouteSelection{Provider: provider, ProviderModel: "vendor-media"}, "/audio/transcriptions", mediaRequest{Fields: map[string]json.RawMessage{"model": json.RawMessage(`"public-media"`)}})
			if providerErrorDisposition(err) != tc.disposition || providerErrorRetryAfter(err) != tc.retryAfter || strings.Contains(err.Error(), "private hook failure") {
				t.Fatalf("hook failure metadata changed: %v disposition=%s retry=%s", err, providerErrorDisposition(err), providerErrorRetryAfter(err))
			}
			if tc.name == "canceled" {
				if !errors.Is(err, context.Canceled) || shouldFailoverRoutedError(err, false) {
					t.Fatalf("cancellation became retryable: %v", err)
				}
				return
			}
			if AsHTTPError(err).Status != tc.status {
				t.Fatalf("hook failure status changed: %v", err)
			}
			if tc.name == "timeout" {
				calls = 0
				store.AddRoute(ModelRoute{ID: "transcription-timeout-fallback", ModelName: "public-media", ProviderID: provider.ID, ProviderModel: "alternate", Status: StatusActive, Priority: 2, Weight: 100})
				response := doMediaMultipartReviewRequest(t, server, key, "/v1/audio/transcriptions", [][2]string{{"model", "public-media"}})
				if response.Code != http.StatusGatewayTimeout || calls != 1 {
					t.Fatalf("uncertain transcription was retried: status=%d calls=%d", response.Code, calls)
				}
			}
		})
	}
}
